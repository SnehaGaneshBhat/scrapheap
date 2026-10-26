package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"scrapheap/internal/jobs/burn"
	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

const (
	m1Tasks             = 200
	m1Lease             = 600 * time.Millisecond
	m1Heartbeat         = 200 * time.Millisecond
	m1PollInterval      = 20 * time.Millisecond
	m1AcceptanceTimeout = 15 * time.Second
)

var m1CalibrationSink uint64

type m1RecordingReducer struct {
	burn  *burn.Reducer
	mu    sync.Mutex
	count map[string]int
}

func (r *m1RecordingReducer) Accept(spec sched.TaskSpec, result []byte, workerID string) {
	r.burn.Accept(spec, result, workerID)
	r.mu.Lock()
	r.count[spec.ID]++
	r.mu.Unlock()
}

func (r *m1RecordingReducer) Counts() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.count))
	for id, count := range r.count {
		out[id] = count
	}
	return out
}

type m1Worker struct {
	name     string
	id       string
	baseURL  string
	client   *http.Client
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	gate     atomic.Bool
	held     chan string
	release  chan struct{}
	activeMu sync.Mutex
	active   string
	errMu    sync.Mutex
	err      error
	stopOnce sync.Once
}

func newM1Worker(parent context.Context, client *http.Client, baseURL, name string) (*m1Worker, proto.HelloResponse, error) {
	ctx, cancel := context.WithCancel(parent)
	w := &m1Worker{
		name: name, baseURL: baseURL, client: client, ctx: ctx, cancel: cancel,
		held: make(chan string, 1), release: make(chan struct{}),
	}
	hello, err := m1DoJSON[proto.HelloResponse](ctx, client, baseURL+"/w/hello", proto.HelloRequest{
		Name: name, CPU: "M1 acceptance test", Cores: 4, Threads: 8, OS: "test", Version: "m1",
	})
	if err != nil {
		cancel()
		return nil, proto.HelloResponse{}, err
	}
	if hello.WorkerID == "" {
		cancel()
		return nil, proto.HelloResponse{}, fmt.Errorf("hello returned empty workerId")
	}
	w.id = hello.WorkerID
	return w, hello, nil
}

func (w *m1Worker) start() {
	w.wg.Add(2)
	go w.pullLoop()
	go w.heartbeatLoop()
}

func (w *m1Worker) crash() {
	w.stop()
}

func (w *m1Worker) stop() {
	w.stopOnce.Do(func() {
		w.cancel()
		w.wg.Wait()
	})
}

func (w *m1Worker) recordError(err error) {
	if err == nil || w.ctx.Err() != nil {
		return
	}
	w.errMu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.errMu.Unlock()
}

func (w *m1Worker) failure() error {
	w.errMu.Lock()
	defer w.errMu.Unlock()
	return w.err
}

func (w *m1Worker) currentTask() string {
	w.activeMu.Lock()
	defer w.activeMu.Unlock()
	return w.active
}

func (w *m1Worker) setCurrentTask(taskID string) {
	w.activeMu.Lock()
	w.active = taskID
	w.activeMu.Unlock()
}

func (w *m1Worker) pullLoop() {
	defer w.wg.Done()
	for w.ctx.Err() == nil {
		pulled, err := m1DoJSON[json.RawMessage](w.ctx, w.client, w.baseURL+"/w/pull", proto.PullRequest{WorkerID: w.id})
		if err != nil {
			w.recordError(err)
			return
		}
		var status struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(pulled, &status); err != nil {
			w.recordError(err)
			return
		}
		switch status.Status {
		case proto.PullStatusTask:
			var task proto.TaskResponse
			if err := json.Unmarshal(pulled, &task); err != nil {
				w.recordError(err)
				return
			}
			w.setCurrentTask(task.TaskID)
			var payload proto.BurnPayload
			if err := json.Unmarshal(task.Payload, &payload); err != nil {
				w.recordError(err)
				return
			}
			started := time.Now()
			checksum := burn.Burn(payload.Iters)
			seconds := time.Since(started).Seconds()
			if w.gate.Load() {
				select {
				case w.held <- task.TaskID:
				case <-w.ctx.Done():
					return
				}
				select {
				case <-w.release:
				case <-w.ctx.Done():
					return
				}
			}
			if w.ctx.Err() != nil {
				return
			}
			result, err := json.Marshal(proto.BurnResult{Checksum: strconv.FormatUint(checksum, 10)})
			if err != nil {
				w.recordError(err)
				return
			}
			request := proto.ResultRequest{
				WorkerID: w.id, TaskID: task.TaskID, Attempt: task.Attempt,
				Result: result, Seconds: seconds,
			}
			if _, err := m1DoJSON[proto.ResultResponse](w.ctx, w.client, w.baseURL+"/w/result", request); err != nil {
				w.recordError(err)
				return
			}
			w.setCurrentTask("")
		case proto.PullStatusWait:
			var waitResponse proto.WaitResponse
			if err := json.Unmarshal(pulled, &waitResponse); err != nil {
				w.recordError(err)
				return
			}
			delay := time.Duration(waitResponse.RetryAfterMs) * time.Millisecond
			if delay < m1PollInterval {
				delay = m1PollInterval
			}
			wait := time.NewTimer(delay)
			select {
			case <-wait.C:
			case <-w.ctx.Done():
				wait.Stop()
				return
			}
		case proto.PullStatusDone:
			return
		default:
			w.recordError(fmt.Errorf("unknown pull status %q", status.Status))
			return
		}
	}
}

func (w *m1Worker) heartbeatLoop() {
	defer w.wg.Done()
	ticker := time.NewTicker(m1Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			running := []string{}
			if taskID := w.currentTask(); taskID != "" {
				running = append(running, taskID)
			}
			_, err := m1DoJSON[proto.HeartbeatResponse](w.ctx, w.client, w.baseURL+"/w/heartbeat", proto.HeartbeatRequest{WorkerID: w.id, Running: running})
			if err != nil {
				w.recordError(err)
				return
			}
		}
	}
}

func m1DoJSON[T any](ctx context.Context, client *http.Client, endpoint string, request any) (T, error) {
	var zero T
	body, err := json.Marshal(request)
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("POST %s returned HTTP %d", endpoint, resp.StatusCode)
	}
	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return zero, err
	}
	return result, nil
}

func m1GetJob(client *http.Client, endpoint string) (proto.JobSnapshot, error) {
	var snapshot proto.JobSnapshot
	resp, err := client.Get(endpoint + "/api/job")
	if err != nil {
		return snapshot, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return snapshot, fmt.Errorf("GET /api/job returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func m1BurnIters() int {
	iters := 1_000_000
	const target = 25 * time.Millisecond
	bestIters := iters
	bestDistance := time.Duration(1<<63 - 1)
	for attempt := 0; attempt < 16; attempt++ {
		started := time.Now()
		m1CalibrationSink = burn.Burn(iters)
		elapsed := time.Since(started)
		distance := elapsed - target
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			bestIters = iters
			bestDistance = distance
		}
		if elapsed >= 20*time.Millisecond && elapsed <= 50*time.Millisecond {
			return iters
		}
		if elapsed < target {
			iters *= 2
		} else {
			iters /= 2
		}
		if iters < 1 {
			iters = 1
		}
	}
	return bestIters
}

func m1ObserveA(snapshot proto.JobSnapshot, workerID string, lost, returned *bool) {
	for _, worker := range snapshot.Workers {
		if worker.ID != workerID {
			continue
		}
		if worker.Status == proto.WorkerStatusLost {
			*lost = true
		}
		if *lost && (worker.Status == proto.WorkerStatusRejoined || worker.Status == proto.WorkerStatusActive) {
			*returned = true
		}
	}
}

func m1WaitFor(t *testing.T, deadline time.Time, client *http.Client, baseURL string, predicate func(proto.JobSnapshot) bool, observe func(proto.JobSnapshot)) proto.JobSnapshot {
	t.Helper()
	ticker := time.NewTicker(m1PollInterval)
	defer ticker.Stop()
	for {
		snapshot, err := m1GetJob(client, baseURL)
		if err != nil {
			t.Fatalf("poll /api/job: %v", err)
		}
		if observe != nil {
			observe(snapshot)
		}
		if predicate(snapshot) {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for job condition; last snapshot: state=%s done=%d/%d inFlight=%d requeued=%d workers=%+v", snapshot.State, snapshot.Done, snapshot.Total, snapshot.InFlight, snapshot.Requeued, snapshot.Workers)
		}
		<-ticker.C
	}
}

func TestM1Acceptance(t *testing.T) {
	oldLogWriter := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(oldLogWriter)
	baselineGoroutines := runtime.NumGoroutine()
	iters := m1BurnIters()
	t.Logf("calibrated burn iterations: %d", iters)
	ctx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	hubCtx, stopHub := context.WithCancel(context.Background())

	reducer := &m1RecordingReducer{burn: burn.NewReducer(iters), count: make(map[string]int)}
	scheduler := sched.New(sched.Config{LeaseDur: m1Lease, HeartbeatTimeout: m1Lease}, burn.NewSource(m1Tasks, iters), reducer)
	serverHub := New(scheduler, Config{
		JobID: "m1-burn", JobKind: proto.TaskKindBurn,
		LeaseSec: 1, HeartbeatSec: 1,
	})
	serverHub.Run(hubCtx)
	server := httptest.NewServer(serverHub.Handler())
	client := server.Client()
	serverClosed := false
	cleanupServer := func() {
		stopHub()
		if !serverClosed {
			server.Close()
			serverClosed = true
		}
		client.CloseIdleConnections()
	}
	workers := make([]*m1Worker, 0, 4)
	cleanup := func() {
		cancelWorkers()
		for _, worker := range workers {
			worker.stop()
			worker.client.CloseIdleConnections()
		}
		cleanupServer()
	}
	defer cleanup()

	deadline := time.Now().Add(m1AcceptanceTimeout)
	startWorker := func(name string) (*m1Worker, proto.HelloResponse) {
		t.Helper()
		worker, hello, err := newM1Worker(ctx, client, server.URL, name)
		if err != nil {
			t.Fatalf("hello worker %s: %v", name, err)
		}
		workers = append(workers, worker)
		return worker, hello
	}

	workerA, helloA := startWorker("A")
	if helloA.Rejoined {
		t.Fatal("initial worker A hello unexpectedly rejoined")
	}
	workerB, helloB := startWorker("B")
	if helloB.Rejoined {
		t.Fatal("initial worker B hello unexpectedly rejoined")
	}
	workerA.start()
	workerB.start()

	startRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/job/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	startResponse, err := client.Do(startRequest)
	if err != nil {
		t.Fatal(err)
	}
	startResponse.Body.Close()
	if startResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("start job returned HTTP %d", startResponse.StatusCode)
	}

	m1WaitFor(t, deadline, client, server.URL, func(s proto.JobSnapshot) bool { return s.Done >= 39 }, nil)
	workerA.gate.Store(true)
	select {
	case heldTaskID := <-workerA.held:
		if heldTaskID == "" {
			t.Fatal("worker A reported an empty held task ID")
		}
	case <-time.After(time.Until(deadline)):
		t.Fatal("worker A did not reach the held in-flight task state")
	}
	m1WaitFor(t, deadline, client, server.URL, func(s proto.JobSnapshot) bool { return s.Done >= 40 }, nil)
	workerA.crash()

	lostA, returnedA := false, false
	m1WaitFor(t, deadline, client, server.URL, func(s proto.JobSnapshot) bool {
		return s.Requeued >= 1 && func() bool {
			for _, worker := range s.Workers {
				if worker.ID == workerA.id && worker.Status == proto.WorkerStatusLost {
					return true
				}
			}
			return false
		}()
	}, func(s proto.JobSnapshot) { m1ObserveA(s, workerA.id, &lostA, &returnedA) })

	m1WaitFor(t, deadline, client, server.URL, func(s proto.JobSnapshot) bool { return s.Done >= 80 }, func(s proto.JobSnapshot) { m1ObserveA(s, workerA.id, &lostA, &returnedA) })
	workerC, helloC := startWorker("C")
	if helloC.Rejoined {
		t.Fatal("initial late-join worker C hello unexpectedly rejoined")
	}
	workerC.start()

	m1WaitFor(t, deadline, client, server.URL, func(s proto.JobSnapshot) bool { return s.Done >= 120 }, func(s proto.JobSnapshot) { m1ObserveA(s, workerA.id, &lostA, &returnedA) })
	workerARestart, helloARestart := startWorker("A")
	if !helloARestart.Rejoined || workerARestart.id != workerA.id {
		t.Fatalf("restarted A hello = workerId %q rejoined %v, want same ID %q and true", workerARestart.id, helloARestart.Rejoined, workerA.id)
	}
	rejoinedSnapshot, err := m1GetJob(client, server.URL)
	if err != nil {
		t.Fatalf("poll rejoined worker A: %v", err)
	}
	m1ObserveA(rejoinedSnapshot, workerA.id, &lostA, &returnedA)
	workerARestart.start()

	final := m1WaitFor(t, deadline, client, server.URL, func(s proto.JobSnapshot) bool { return s.State == proto.JobStateDone }, func(s proto.JobSnapshot) { m1ObserveA(s, workerA.id, &lostA, &returnedA) })
	if final.State != proto.JobStateDone || final.Done != m1Tasks || final.Total != m1Tasks {
		t.Fatalf("final job snapshot: state=%q done=%d total=%d", final.State, final.Done, final.Total)
	}
	if final.Requeued < 1 {
		t.Fatalf("expected at least one requeued task, got %d", final.Requeued)
	}
	if !lostA {
		t.Fatal("worker A was never observed with status lost")
	}
	if !returnedA {
		t.Fatal("worker A was not observed rejoined or active after being lost")
	}
	var workerCInfo *proto.WorkerInfo
	for i := range final.Workers {
		if final.Workers[i].ID == workerC.id {
			workerCInfo = &final.Workers[i]
			break
		}
	}
	if workerCInfo == nil {
		t.Fatal("worker C missing from final /api/job snapshot")
	}
	if workerCInfo.TasksDone < 1 {
		t.Fatalf("worker C completed %d tasks, want at least one", workerCInfo.TasksDone)
	}

	counts := reducer.Counts()
	if len(counts) != m1Tasks {
		t.Fatalf("reducer saw %d task IDs, want %d", len(counts), m1Tasks)
	}
	for i := 0; i < m1Tasks; i++ {
		id := "burn-" + strconv.Itoa(i)
		if counts[id] != 1 {
			t.Errorf("reducer accepted %s %d times, want exactly once", id, counts[id])
		}
	}
	if accepted, wrong := reducer.burn.Stats(); accepted != m1Tasks || wrong != 0 {
		t.Fatalf("burn reducer stats: accepted=%d wrong=%d; want accepted=%d wrong=0", accepted, wrong, m1Tasks)
	}

	duplicateResult, err := json.Marshal(proto.BurnResult{Checksum: strconv.FormatUint(burn.Burn(iters), 10)})
	if err != nil {
		t.Fatal(err)
	}
	duplicateRequest := proto.ResultRequest{
		WorkerID: workerB.id, TaskID: "burn-0", Attempt: 1, Result: duplicateResult, Seconds: 0.025,
	}
	duplicateResponse, err := m1DoJSON[proto.ResultResponse](context.Background(), client, server.URL+"/w/result", duplicateRequest)
	if err != nil {
		t.Fatalf("replay late duplicate result: %v", err)
	}
	if duplicateResponse.Accepted || duplicateResponse.Reason != proto.ResultReasonDuplicate {
		t.Fatalf("duplicate response: %+v; want accepted=false reason=duplicate", duplicateResponse)
	}

	for _, worker := range workers {
		worker.stop()
		if err := worker.failure(); err != nil {
			t.Errorf("worker %s failed: %v", worker.name, err)
		}
	}
	cleanupServer()

	goroutineDeadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baselineGoroutines+8 && time.Now().Before(goroutineDeadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baselineGoroutines+8 {
		t.Fatalf("goroutines after cleanup = %d, baseline %d (allowed +8)", got, baselineGoroutines)
	}
}
