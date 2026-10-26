package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"scrapheap/internal/jobs/burn"
	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type httpResult struct {
	status int
	body   []byte
}

func callJSON(client *http.Client, url, method string, request any) (httpResult, error) {
	var body io.Reader
	if request != nil {
		data, err := json.Marshal(request)
		if err != nil {
			return httpResult{}, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return httpResult{}, err
	}
	if request != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return httpResult{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return httpResult{status: resp.StatusCode, body: data}, err
}

func decodeBody[T any](body []byte) (T, error) {
	var value T
	err := json.Unmarshal(body, &value)
	return value, err
}

func TestFullLifecycleWithHTTPWorker(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	const iters = 37
	const taskCount = 6
	jobs := burn.NewSource(taskCount, iters)
	reducer := burn.NewReducer(iters)
	scheduler := sched.New(sched.Config{LeaseDur: 6 * time.Second, HeartbeatTimeout: 6 * time.Second}, jobs, reducer)
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "sample.bin"), []byte("dataset"), 0o600); err != nil {
		t.Fatal(err)
	}
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("frontend"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverHub := New(scheduler, Config{
		JobID: "burn-test", JobKind: proto.TaskKindBurn, LeaseSec: 6, HeartbeatSec: 2,
		DataDir: dataDir, WebDir: webDir, Clock: clock.Now,
	})
	server := httptest.NewServer(serverHub.Handler())
	defer server.Close()

	client := server.Client()
	workerID, rejoined := helloWorker(t, client, server.URL, "worker-one")
	if rejoined {
		t.Fatal("first hello unexpectedly reported rejoined")
	}
	workerIDAgain, rejoined := helloWorker(t, client, server.URL, "worker-one")
	if !rejoined || workerIDAgain != workerID {
		t.Fatalf("same-name hello = (%q,%v), want (%q,true)", workerIDAgain, rejoined, workerID)
	}

	before, err := callJSON(client, server.URL+"/api/result", http.MethodGet, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := decodeBody[proto.APIResultResponse](before.body); err != nil || before.status != http.StatusOK || got.Ready {
		t.Fatalf("pre-completion result response: status=%d value=%+v err=%v", before.status, got, err)
	}
	start, err := callJSON(client, server.URL+"/api/job/start", http.MethodPost, nil)
	if err != nil || start.status != http.StatusNoContent {
		t.Fatalf("start status=%d err=%v", start.status, err)
	}

	workerErr := make(chan error, 1)
	go func() {
		var last proto.ResultRequest
		for {
			clock.Advance(25 * time.Millisecond)
			pulled, err := callJSON(client, server.URL+"/w/pull", http.MethodPost, proto.PullRequest{WorkerID: workerID})
			if err != nil {
				workerErr <- err
				return
			}
			if pulled.status != http.StatusOK {
				workerErr <- fmt.Errorf("pull HTTP status %d", pulled.status)
				return
			}
			var task proto.TaskResponse
			if err := json.Unmarshal(pulled.body, &task); err != nil {
				workerErr <- err
				return
			}
			switch task.Status {
			case proto.PullStatusTask:
				var payload proto.BurnPayload
				if err := json.Unmarshal(task.Payload, &payload); err != nil {
					workerErr <- err
					return
				}
				checksum := burn.Burn(payload.Iters)
				result, err := json.Marshal(proto.BurnResult{Checksum: strconv.FormatUint(checksum, 10)})
				if err != nil {
					workerErr <- err
					return
				}
				last = proto.ResultRequest{WorkerID: workerID, TaskID: task.TaskID, Attempt: task.Attempt, Result: result, Seconds: 0.01}
				reply, err := callJSON(client, server.URL+"/w/result", http.MethodPost, last)
				if err != nil {
					workerErr <- err
					return
				}
				outcome, err := decodeBody[proto.ResultResponse](reply.body)
				if err != nil || reply.status != http.StatusOK || !outcome.Accepted {
					workerErr <- fmt.Errorf("result status=%d outcome=%+v err=%v", reply.status, outcome, err)
					return
				}
			case proto.PullStatusWait:
				continue
			case proto.PullStatusDone:
				duplicate, err := callJSON(client, server.URL+"/w/result", http.MethodPost, last)
				if err != nil {
					workerErr <- err
					return
				}
				outcome, err := decodeBody[proto.ResultResponse](duplicate.body)
				if err != nil || duplicate.status != http.StatusOK || outcome.Accepted || outcome.Reason != proto.ResultReasonDuplicate {
					workerErr <- fmt.Errorf("duplicate status=%d outcome=%+v err=%v", duplicate.status, outcome, err)
					return
				}
				workerErr <- nil
				return
			default:
				workerErr <- fmt.Errorf("unknown pull status %q", task.Status)
				return
			}
		}
	}()
	if err := <-workerErr; err != nil {
		t.Fatal(err)
	}

	jobResponse, err := callJSON(client, server.URL+"/api/job", http.MethodGet, nil)
	if err != nil {
		t.Fatal(err)
	}
	job, err := decodeBody[proto.JobSnapshot](jobResponse.body)
	if err != nil {
		t.Fatal(err)
	}
	if jobResponse.status != http.StatusOK || job.Done != taskCount || job.Total != taskCount || job.State != proto.JobStateDone {
		t.Fatalf("job snapshot status=%d value=%+v", jobResponse.status, job)
	}
	if job.Leaderboard == nil || job.Verify != (proto.VerifyStats{}) || job.Baseline.Label != "1 laptop, all cores" {
		t.Fatalf("job snapshot defaults are incorrect: %+v", job)
	}
	if accepted, wrong := reducer.Stats(); accepted != taskCount || wrong != 0 {
		t.Fatalf("reducer stats accepted=%d wrong=%d", accepted, wrong)
	}
	ready, err := callJSON(client, server.URL+"/api/result", http.MethodGet, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := decodeBody[proto.APIResultResponse](ready.body); err != nil || !got.Ready {
		t.Fatalf("completed result response: value=%+v err=%v", got, err)
	}

	health, err := callJSON(client, server.URL+"/healthz", http.MethodGet, nil)
	if err != nil || health.status != http.StatusOK || string(health.body) != "ok" {
		t.Fatalf("health status=%d body=%q err=%v", health.status, health.body, err)
	}
	file, err := callJSON(client, server.URL+"/dataset/sample.bin", http.MethodGet, nil)
	if err != nil || file.status != http.StatusOK || string(file.body) != "dataset" {
		t.Fatalf("dataset status=%d body=%q err=%v", file.status, file.body, err)
	}
	frontend, err := callJSON(client, server.URL+"/", http.MethodGet, nil)
	if err != nil || frontend.status != http.StatusOK || string(frontend.body) != "frontend" {
		t.Fatalf("frontend status=%d body=%q err=%v", frontend.status, frontend.body, err)
	}
}

func helloWorker(t *testing.T, client *http.Client, baseURL, name string) (string, bool) {
	t.Helper()
	response, err := callJSON(client, baseURL+"/w/hello", http.MethodPost, proto.HelloRequest{
		Name: name, CPU: "test-cpu", Cores: 4, Threads: 8, OS: "test/os", Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeBody[proto.HelloResponse](response.body)
	if err != nil {
		t.Fatal(err)
	}
	if response.status != http.StatusOK || got.WorkerID == "" || got.LeaseSec != 6 || got.HeartbeatSec != 2 || got.JobKind != proto.TaskKindBurn {
		t.Fatalf("hello response status=%d value=%+v", response.status, got)
	}
	if len(got.Dataset) != 1 || got.Dataset[0].File != "sample.bin" || got.Dataset[0].Size != int64(len("dataset")) || got.Dataset[0].SHA256 == "" {
		t.Fatalf("unexpected dataset manifest: %+v", got.Dataset)
	}
	return got.WorkerID, got.Rejoined
}

func TestValidationWaitAndMethods(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	scheduler := sched.New(sched.Config{LeaseDur: time.Second}, burn.NewSource(1, 1), burn.NewReducer(1))
	server := httptest.NewServer(New(scheduler, Config{Clock: clock.Now, DataDir: filepath.Join(t.TempDir(), "missing"), WebDir: t.TempDir()}).Handler())
	defer server.Close()
	client := server.Client()

	bad, err := http.NewRequest(http.MethodPost, server.URL+"/w/hello", bytes.NewBufferString("{"))
	if err != nil {
		t.Fatal(err)
	}
	bad.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	badBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad JSON status=%d body=%s", resp.StatusCode, badBody)
	}
	if _, err := decodeBody[proto.ErrorResponse](badBody); err != nil {
		t.Fatalf("bad JSON response is not error JSON: %s (%v)", badBody, err)
	}

	wrongMethod, err := http.Get(server.URL + "/w/hello")
	if err != nil {
		t.Fatal(err)
	}
	wrongMethod.Body.Close()
	if wrongMethod.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status=%d, want 405", wrongMethod.StatusCode)
	}

	helloResponse, err := callJSON(client, server.URL+"/w/hello", http.MethodPost, proto.HelloRequest{Name: "waiting-worker"})
	if err != nil {
		t.Fatal(err)
	}
	hello, err := decodeBody[proto.HelloResponse](helloResponse.body)
	if err != nil || helloResponse.status != http.StatusOK || hello.Dataset == nil || len(hello.Dataset) != 0 {
		t.Fatalf("missing data directory hello status=%d value=%+v err=%v", helloResponse.status, hello, err)
	}
	workerID := hello.WorkerID
	pulled, err := callJSON(client, server.URL+"/w/pull", http.MethodPost, proto.PullRequest{WorkerID: workerID})
	if err != nil {
		t.Fatal(err)
	}
	wait, err := decodeBody[proto.WaitResponse](pulled.body)
	if err != nil || pulled.status != http.StatusOK || wait.Status != proto.PullStatusWait {
		t.Fatalf("pre-start pull status=%d response=%+v err=%v", pulled.status, wait, err)
	}
}
