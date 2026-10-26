package hub

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/scrap-heap/backend/internal/proto"
	"github.com/scrap-heap/backend/internal/sched"
)

type events struct {
	sync.Mutex
	joined           []DeviceInfo
	left             int
	assigned         int
	accepted         int
	verified         []VerifyStatus
	verifyFailures   []verifyFailure
	duplicate, stale int
	done             int
}

type verifyFailure struct {
	device     string
	task       uint32
	attempt    uint16
	mismatched int
}

func (e *events) DeviceJoined(_ time.Time, _ string, i DeviceInfo) {
	e.Lock()
	defer e.Unlock()
	e.joined = append(e.joined, i)
}
func (e *events) DeviceLeft(time.Time, string)      { e.Lock(); defer e.Unlock(); e.left++ }
func (*events) JobStart(time.Time, uint32, int)     {}
func (e *events) JobDone(time.Time, uint32, string) { e.Lock(); defer e.Unlock(); e.done++ }
func (e *events) TaskAssigned(time.Time, string, int, uint32, uint16) {
	e.Lock()
	defer e.Unlock()
	e.assigned++
}
func (e *events) TaskAccepted(_ time.Time, _ string, _ uint32, _ uint16, _ float32, status VerifyStatus) {
	e.Lock()
	defer e.Unlock()
	e.accepted++
	e.verified = append(e.verified, status)
}
func (e *events) VerifyFailed(_ time.Time, device string, task uint32, attempt uint16, mismatched int) {
	e.Lock()
	defer e.Unlock()
	e.verifyFailures = append(e.verifyFailures, verifyFailure{device: device, task: task, attempt: attempt, mismatched: mismatched})
}
func (*events) TaskRequeued(time.Time, uint32, uint16, sched.RequeueReason) {}

func (e *events) DupDropped(_ time.Time, _ uint32, _ uint16, stale bool) {
	e.Lock()
	defer e.Unlock()
	if stale {
		e.stale++
	} else {
		e.duplicate++
	}
}

func startTestHub(t *testing.T, cfg Config, collector Collector) (*Hub, *httptest.Server, context.CancelFunc) {
	return startTestHubWithVerifier(t, cfg, collector, nil)
}

func startTestHubWithVerifier(t *testing.T, cfg Config, collector Collector, verifier Verifier) (*Hub, *httptest.Server, context.CancelFunc) {
	t.Helper()
	h := New(cfg, collector, verifier)
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	server := httptest.NewServer(h.Handler())
	t.Cleanup(func() { server.Close(); cancel() })
	return h, server, cancel
}
func connectWorker(t *testing.T, server *httptest.Server, id string, threads int, mock bool) (*websocket.Conn, context.CancelFunc) {
	t.Helper()
	u := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	conn, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	sendJSON(t, conn, proto.Message{Type: proto.MessageHello, DeviceID: id, UserAgent: "test", Cores: 2, Threads: threads, Mock: mock})
	readJSONType(t, conn, proto.MessageWelcome)
	sendJSON(t, conn, proto.Message{Type: proto.MessageBenchResult, TilesPerSec: 10, PerThread: 10, Threads: threads, Hash: strings.Repeat("a", 64)})
	time.Sleep(10 * time.Millisecond)
	t.Cleanup(func() { cancel(); _ = conn.Close(websocket.StatusNormalClosure, "test done") })
	return conn, cancel
}
func sendJSON(t *testing.T, c *websocket.Conn, m proto.Message) {
	t.Helper()
	b, err := proto.EncodeJSON(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}
func readJSON(t *testing.T, c *websocket.Conn) proto.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	typ, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("got frame type %v", typ)
	}
	m, err := proto.DecodeJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func readJSONType(t *testing.T, c *websocket.Conn, want proto.MessageType) proto.Message {
	t.Helper()
	m := readJSON(t, c)
	if m.Type != want {
		t.Fatalf("message type=%s want %s", m.Type, want)
	}
	return m
}
func readTask(t *testing.T, c *websocket.Conn) proto.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	typ, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary {
		t.Fatalf("frame type=%v want binary", typ)
	}
	task, err := proto.DecodeTask(b)
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func sendResult(t *testing.T, c *websocket.Conn, task proto.Task) {
	t.Helper()
	pixels := make([]byte, int(task.W)*int(task.H)*4)
	for i := range pixels {
		pixels[i] = byte(task.TaskID + uint32(i))
	}
	b, err := proto.EncodeResult(proto.Result{JobID: task.JobID, TaskID: task.TaskID, Attempt: task.Attempt, W: task.W, H: task.H, ComputeMs: 1, RGBA: pixels})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Write(context.Background(), websocket.MessageBinary, b); err != nil {
		t.Fatal(err)
	}
}

func TestFullJobAndStore(t *testing.T) {
	e := &events{}
	h, srv, _ := startTestHub(t, Config{Job: JobConfig{X0: -2.5, Y0: -1.75, Scale: .01, MaxIter: 20, CanvasSize: 128}}, e)
	c, _ := connectWorker(t, srv, "w", 1, false)
	if id := h.StartNewJob(); id != 1 {
		t.Fatalf("job id=%d", id)
	}
	readJSONType(t, c, proto.MessageJobStarted)
	for i := 0; i < 4; i++ {
		sendJSON(t, c, proto.Message{Type: proto.MessageRequestTask})
		task := readTask(t, c)
		sendResult(t, c, task)
		readJSONType(t, c, proto.MessageAck)
	}
	readJSONType(t, c, proto.MessageJobDone)
	snap := h.Snapshot()
	if snap.Done != 4 || snap.Total != 4 {
		t.Fatalf("snapshot=%+v", snap)
	}
	e.Lock()
	defer e.Unlock()
	if e.accepted != 4 || e.done != 1 {
		t.Fatalf("events accepted=%d done=%d", e.accepted, e.done)
	}
	for _, status := range e.verified {
		if status != NotChecked {
			t.Fatalf("default verifier status=%d, want NotChecked", status)
		}
	}
}

type acceptingVerifier struct{}

func (acceptingVerifier) Verify(proto.Task, []byte) (bool, int) { return true, 0 }

type rejectingVerifier struct{}

func (rejectingVerifier) Verify(proto.Task, []byte) (bool, int) { return false, 5 }

func TestVerifierStatusAndFailureEvent(t *testing.T) {
	t.Run("configured verifier pass", func(t *testing.T) {
		e := &events{}
		h, srv, _ := startTestHubWithVerifier(t, Config{Job: JobConfig{X0: -2, Y0: -1, Scale: .01, MaxIter: 10, CanvasSize: 64}}, e, acceptingVerifier{})
		c, _ := connectWorker(t, srv, "verified", 1, false)
		h.StartNewJob()
		readJSONType(t, c, proto.MessageJobStarted)
		sendJSON(t, c, proto.Message{Type: proto.MessageRequestTask})
		task := readTask(t, c)
		sendResult(t, c, task)
		readJSONType(t, c, proto.MessageAck)
		e.Lock()
		defer e.Unlock()
		if len(e.verified) != 1 || e.verified[0] != Passed {
			t.Fatalf("statuses=%v, want [Passed]", e.verified)
		}
	})
	t.Run("verifier rejection", func(t *testing.T) {
		e := &events{}
		h, srv, _ := startTestHubWithVerifier(t, Config{Job: JobConfig{X0: -2, Y0: -1, Scale: .01, MaxIter: 10, CanvasSize: 64}}, e, rejectingVerifier{})
		c, _ := connectWorker(t, srv, "rejected", 1, false)
		// Test the failure callback directly through the result path.
		// The one-task job leaves the rejected lease pending until it expires.
		h.StartNewJob()
		readJSONType(t, c, proto.MessageJobStarted)
		sendJSON(t, c, proto.Message{Type: proto.MessageRequestTask})
		task := readTask(t, c)
		sendResult(t, c, task)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			e.Lock()
			n := len(e.verifyFailures)
			e.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		e.Lock()
		defer e.Unlock()
		if len(e.verifyFailures) != 1 || e.verifyFailures[0].mismatched != 5 {
			t.Fatalf("failures=%+v", e.verifyFailures)
		}
	})
}
func TestDisconnectRequeuesAndMockMetadata(t *testing.T) {
	e := &events{}
	h, srv, _ := startTestHub(t, Config{Job: JobConfig{X0: -2, Y0: -1, Scale: .01, MaxIter: 10, CanvasSize: 64}, TickInterval: 10 * time.Millisecond}, e)
	a, _ := connectWorker(t, srv, "a", 1, true)
	if h.StartNewJob() != 1 {
		t.Fatal("start")
	}
	readJSONType(t, a, proto.MessageJobStarted)
	sendJSON(t, a, proto.Message{Type: proto.MessageRequestTask})
	first := readTask(t, a)
	_ = a.CloseNow()
	time.Sleep(30 * time.Millisecond)
	b, _ := connectWorker(t, srv, "b", 1, false)
	readJSONType(t, b, proto.MessageJobStarted)
	sendJSON(t, b, proto.Message{Type: proto.MessageRequestTask})
	second := readTask(t, b)
	if second.TaskID != first.TaskID || second.Attempt != 2 {
		t.Fatalf("retry=%+v first=%+v", second, first)
	}
	sendResult(t, b, second)
	readJSONType(t, b, proto.MessageAck)
	readJSONType(t, b, proto.MessageJobDone)
	e.Lock()
	defer e.Unlock()
	if len(e.joined) < 2 || !e.joined[0].Mock {
		t.Fatalf("mock metadata missing: %+v", e.joined)
	}
}

func TestHashMismatchRejected(t *testing.T) {
	_, srv, _ := startTestHub(t, Config{ExpectedBenchHash: strings.Repeat("b", 64)}, nil)
	u := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := proto.EncodeJSON(proto.Message{Type: proto.MessageHello, DeviceID: "bad", Cores: 2, Threads: 1})
	if err = c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	readJSONType(t, c, proto.MessageWelcome)
	b, _ = proto.EncodeJSON(proto.Message{Type: proto.MessageBenchResult, TilesPerSec: 1, PerThread: 1, Threads: 1, Hash: strings.Repeat("a", 64)})
	if err = c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.Read(ctx); err == nil {
		t.Fatal("expected hash mismatch connection close")
	}
}

func TestHeartbeatTimeoutRequeues(t *testing.T) {
	e := &events{}
	h, srv, _ := startTestHub(t, Config{Job: JobConfig{X0: -2, Y0: -1, Scale: .01, MaxIter: 10, CanvasSize: 64}, TickInterval: 5 * time.Millisecond, HeartbeatTimeout: 250 * time.Millisecond}, e)
	a, _ := connectWorker(t, srv, "silent", 1, false)
	h.StartNewJob()
	readJSONType(t, a, proto.MessageJobStarted)
	sendJSON(t, a, proto.Message{Type: proto.MessageRequestTask})
	first := readTask(t, a)
	time.Sleep(300 * time.Millisecond)
	b, _ := connectWorker(t, srv, "replacement", 1, false)
	readJSONType(t, b, proto.MessageJobStarted)
	sendJSON(t, b, proto.Message{Type: proto.MessageRequestTask})
	retry := readTask(t, b)
	if retry.TaskID != first.TaskID || retry.Attempt != first.Attempt+1 {
		t.Fatalf("retry=%+v first=%+v", retry, first)
	}
}

func TestStaleDuplicateAndWrongSizeResults(t *testing.T) {
	e := &events{}
	h, srv, _ := startTestHub(t, Config{Job: JobConfig{X0: -2, Y0: -1, Scale: .01, MaxIter: 10, CanvasSize: 64}}, e)
	c, _ := connectWorker(t, srv, "w", 1, false)
	h.StartNewJob()
	readJSONType(t, c, proto.MessageJobStarted)
	sendJSON(t, c, proto.Message{Type: proto.MessageRequestTask})
	task := readTask(t, c)
	wrong := proto.Result{JobID: task.JobID, TaskID: task.TaskID, Attempt: task.Attempt, W: 1, H: 1, ComputeMs: 1, RGBA: []byte{1, 2, 3, 4}}
	b, _ := proto.EncodeResult(wrong)
	_ = c.Write(context.Background(), websocket.MessageBinary, b)
	if h.Snapshot().Done != 0 {
		t.Fatal("wrong-size result was accepted")
	}
	sendResult(t, c, task)
	readJSONType(t, c, proto.MessageAck)
	readJSONType(t, c, proto.MessageJobDone)
	sendResult(t, c, task)
	stale := proto.Result{JobID: task.JobID + 1, TaskID: task.TaskID, Attempt: task.Attempt, W: task.W, H: task.H, ComputeMs: 1, RGBA: make([]byte, int(task.W)*int(task.H)*4)}
	b, _ = proto.EncodeResult(stale)
	_ = c.Write(context.Background(), websocket.MessageBinary, b)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		e.Lock()
		d, s := e.duplicate, e.stale
		e.Unlock()
		if d > 0 && s > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	e.Lock()
	defer e.Unlock()
	t.Fatalf("drops duplicate=%d stale=%d", e.duplicate, e.stale)
}

func TestStartNewJobMidRun(t *testing.T) {
	h, srv, _ := startTestHub(t, Config{Job: JobConfig{X0: -2, Y0: -1, Scale: .01, MaxIter: 10, CanvasSize: 64}}, nil)
	c, _ := connectWorker(t, srv, "w", 1, false)
	h.StartNewJob()
	readJSONType(t, c, proto.MessageJobStarted)
	sendJSON(t, c, proto.Message{Type: proto.MessageRequestTask})
	old := readTask(t, c)
	if id := h.StartNewJob(); id != old.JobID+1 {
		t.Fatalf("new id=%d old=%d", id, old.JobID)
	}
	readJSONType(t, c, proto.MessageJobStarted)
	sendJSON(t, c, proto.Message{Type: proto.MessageRequestTask})
	next := readTask(t, c)
	if next.JobID != old.JobID+1 {
		t.Fatalf("assigned stale job task: %+v", next)
	}
}

func TestRequestCapIgnoresExtraRequest(t *testing.T) {
	e := &events{}
	h, srv, _ := startTestHub(t, Config{Job: JobConfig{X0: -2, Y0: -1, Scale: .01, MaxIter: 10, CanvasSize: 64}}, e)
	c, _ := connectWorker(t, srv, "single-thread", 1, false)
	h.StartNewJob()
	readJSONType(t, c, proto.MessageJobStarted)
	sendJSON(t, c, proto.Message{Type: proto.MessageRequestTask})
	task := readTask(t, c)
	sendJSON(t, c, proto.Message{Type: proto.MessageRequestTask})
	time.Sleep(20 * time.Millisecond)
	e.Lock()
	assigned := e.assigned
	e.Unlock()
	if assigned != 1 {
		t.Fatalf("assignments=%d, want 1", assigned)
	}
	sendResult(t, c, task)
	readJSONType(t, c, proto.MessageAck)
	readJSONType(t, c, proto.MessageJobDone)
}
