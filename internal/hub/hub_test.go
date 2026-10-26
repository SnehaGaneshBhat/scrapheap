package hub

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

type fixedSource []sched.TaskSpec

func (tasks fixedSource) Tasks() []sched.TaskSpec { return tasks }

func TestWorkerHTTPFlowAndSnapshot(t *testing.T) {
	work := fixedSource{{ID: "task-1", Kind: proto.TaskKindBurn, Payload: []byte(`{"iters":1}`), Cost: 1}}
	scheduler := sched.New(sched.Config{LeaseDur: 10 * time.Second, HeartbeatTimeout: 5 * time.Second}, work, nil)
	var logs bytes.Buffer
	h, err := New(Config{Scheduler: scheduler, JobID: "test-job", JobKind: proto.TaskKindBurn, LeaseDuration: 10 * time.Second, HeartbeatInterval: 2 * time.Second, Logger: log.New(&logs, "", 0)})
	if err != nil {
		t.Fatal(err)
	}

	hello := requestJSON(t, h, http.MethodPost, "/w/hello", `{"name":"laptop-a","cores":4,"threads":3,"browser":"Chrome test"}`)
	var registered proto.HelloResponse
	if err := json.Unmarshal(hello, &registered); err != nil {
		t.Fatal(err)
	}
	if registered.WorkerID == "" || registered.JobKind != proto.TaskKindBurn {
		t.Fatalf("unexpected hello reply: %+v", registered)
	}

	start := httptest.NewRecorder()
	h.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/api/job/start", nil))
	if start.Code != http.StatusNoContent {
		t.Fatalf("start status = %d", start.Code)
	}
	pull := requestJSON(t, h, http.MethodPost, "/w/pull", `{"workerId":"`+registered.WorkerID+`"}`)
	var assigned proto.TaskResponse
	if err := json.Unmarshal(pull, &assigned); err != nil {
		t.Fatal(err)
	}
	if assigned.Status != proto.PullStatusTask || assigned.TaskID != "task-1" {
		t.Fatalf("unexpected task reply: %+v", assigned)
	}

	heartbeat := requestJSON(t, h, http.MethodPost, "/w/heartbeat", `{"workerId":"`+registered.WorkerID+`","running":["task-1"]}`)
	var alive proto.HeartbeatResponse
	if err := json.Unmarshal(heartbeat, &alive); err != nil || !alive.OK || alive.UnknownWorker {
		t.Fatalf("heartbeat reply = %s, err=%v", heartbeat, err)
	}
	requestJSON(t, h, http.MethodPost, "/w/result", `{"workerId":"`+registered.WorkerID+`","taskId":"task-1","attempt":1,"result":{"checksum":"1"},"seconds":0.25}`)

	snapshot := requestJSON(t, h, http.MethodGet, "/api/job", "")
	var job proto.JobSnapshot
	if err := json.Unmarshal(snapshot, &job); err != nil {
		t.Fatal(err)
	}
	if job.JobID != "test-job" || job.State != proto.JobStateDone || job.Done != 1 || len(job.Workers) != 1 || job.Workers[0].Browser != "Chrome test" || job.Best != nil {
		t.Fatalf("unexpected job snapshot: %+v", job)
	}
	for _, event := range []string{"assigned", "completed"} {
		if !strings.Contains(logs.String(), "event="+event) {
			t.Errorf("event %q missing from logs: %s", event, logs.String())
		}
	}
}

func TestBurnTaskRecoversAfterWorkerHeartbeatTimeout(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	work := fixedSource{{ID: "task-1", Kind: proto.TaskKindBurn, Payload: []byte(`{"iters":1}`), Cost: 1}}
	scheduler := sched.New(sched.Config{LeaseDur: 10 * time.Second, HeartbeatTimeout: 5 * time.Second}, work, nil)
	var logs bytes.Buffer
	h, err := New(Config{Scheduler: scheduler, JobID: "recovery-test", JobKind: proto.TaskKindBurn, LeaseDuration: 10 * time.Second, HeartbeatInterval: 2 * time.Second, Logger: log.New(&logs, "", 0), Now: clock})
	if err != nil {
		t.Fatal(err)
	}

	firstHello := requestJSON(t, h, http.MethodPost, "/w/hello", `{"name":"laptop-a","cores":4,"threads":3,"browser":"Chrome"}`)
	var first proto.HelloResponse
	if err := json.Unmarshal(firstHello, &first); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, h, http.MethodPost, "/api/job/start", "")
	firstPull := requestJSON(t, h, http.MethodPost, "/w/pull", `{"workerId":"`+first.WorkerID+`"}`)
	var expired proto.TaskResponse
	if err := json.Unmarshal(firstPull, &expired); err != nil {
		t.Fatal(err)
	}
	if expired.Status != proto.PullStatusTask || expired.Attempt != 1 {
		t.Fatalf("first assignment = %+v", expired)
	}

	now = now.Add(6 * time.Second)
	h.Tick(now)
	secondHello := requestJSON(t, h, http.MethodPost, "/w/hello", `{"name":"laptop-b","cores":8,"threads":7,"browser":"Firefox"}`)
	var second proto.HelloResponse
	if err := json.Unmarshal(secondHello, &second); err != nil {
		t.Fatal(err)
	}
	secondPull := requestJSON(t, h, http.MethodPost, "/w/pull", `{"workerId":"`+second.WorkerID+`"}`)
	var recovered proto.TaskResponse
	if err := json.Unmarshal(secondPull, &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.TaskID != expired.TaskID || recovered.Attempt != expired.Attempt+1 {
		t.Fatalf("recovered assignment = %+v; first = %+v", recovered, expired)
	}

	rejoinedJSON := requestJSON(t, h, http.MethodPost, "/w/hello", `{"name":"laptop-a","cores":4,"threads":3,"browser":"Chrome"}`)
	var rejoined proto.HelloResponse
	if err := json.Unmarshal(rejoinedJSON, &rejoined); err != nil {
		t.Fatal(err)
	}
	if !rejoined.Rejoined || rejoined.WorkerID != first.WorkerID {
		t.Fatalf("rejoin reply = %+v", rejoined)
	}

	staleJSON := requestJSON(t, h, http.MethodPost, "/w/result", `{"workerId":"`+first.WorkerID+`","taskId":"task-1","attempt":1,"result":{"checksum":"old"},"seconds":1}`)
	var stale proto.ResultResponse
	if err := json.Unmarshal(staleJSON, &stale); err != nil {
		t.Fatal(err)
	}
	if stale.Accepted || stale.Reason != proto.ResultReasonStale {
		t.Fatalf("expired result = %+v", stale)
	}
	currentJSON := requestJSON(t, h, http.MethodPost, "/w/result", `{"workerId":"`+second.WorkerID+`","taskId":"task-1","attempt":2,"result":{"checksum":"current"},"seconds":1}`)
	var current proto.ResultResponse
	if err := json.Unmarshal(currentJSON, &current); err != nil {
		t.Fatal(err)
	}
	if !current.Accepted {
		t.Fatalf("current result = %+v", current)
	}

	jobJSON := requestJSON(t, h, http.MethodGet, "/api/job", "")
	var job proto.JobSnapshot
	if err := json.Unmarshal(jobJSON, &job); err != nil {
		t.Fatal(err)
	}
	if job.State != proto.JobStateDone || job.Done != 1 || job.Requeued != 1 {
		t.Fatalf("recovery snapshot = %+v", job)
	}
	for _, event := range []string{"lost", "requeued"} {
		if !strings.Contains(logs.String(), "event="+event) {
			t.Errorf("event %q missing from logs: %s", event, logs.String())
		}
	}
}

func TestHubReportsSampledVerification(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	work := fixedSource{{ID: "task-verify", Kind: proto.TaskKindBurn, Payload: []byte(`{"iters":1}`), Cost: 1}}
	verifier := sched.SampleVerifierFunc(func(_ sched.TaskSpec, first, second []byte) bool {
		var a, b proto.BurnResult
		if json.Unmarshal(first, &a) != nil || json.Unmarshal(second, &b) != nil {
			return false
		}
		return a.Checksum == b.Checksum
	})
	scheduler := sched.New(sched.Config{LeaseDur: 10 * time.Second, HeartbeatTimeout: 5 * time.Second, VerifyRate: 1, Verifier: verifier}, work, nil)
	h, err := New(Config{Scheduler: scheduler, JobKind: proto.TaskKindBurn, LeaseDuration: 10 * time.Second, HeartbeatInterval: 2 * time.Second, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	aHello := requestJSON(t, h, http.MethodPost, "/w/hello", `{"name":"a","cores":2,"threads":1,"browser":"Chrome"}`)
	bHello := requestJSON(t, h, http.MethodPost, "/w/hello", `{"name":"b","cores":2,"threads":1,"browser":"Firefox"}`)
	var a, b proto.HelloResponse
	if err := json.Unmarshal(aHello, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bHello, &b); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, h, http.MethodPost, "/api/job/start", "")
	firstJSON := requestJSON(t, h, http.MethodPost, "/w/pull", `{"workerId":"`+a.WorkerID+`"}`)
	var first proto.TaskResponse
	if err := json.Unmarshal(firstJSON, &first); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, h, http.MethodPost, "/w/result", `{"workerId":"`+a.WorkerID+`","taskId":"task-verify","attempt":1,"result":{"checksum":"abc"},"seconds":1}`)
	secondJSON := requestJSON(t, h, http.MethodPost, "/w/pull", `{"workerId":"`+b.WorkerID+`"}`)
	var second proto.TaskResponse
	if err := json.Unmarshal(secondJSON, &second); err != nil {
		t.Fatal(err)
	}
	if second.TaskID != first.TaskID || second.Attempt != 2 {
		t.Fatalf("verification assignment = %+v", second)
	}
	requestJSON(t, h, http.MethodPost, "/w/result", `{"workerId":"`+b.WorkerID+`","taskId":"task-verify","attempt":2,"result": { "checksum" : "abc" },"seconds":1}`)
	var snapshot proto.JobSnapshot
	if err := json.Unmarshal(requestJSON(t, h, http.MethodGet, "/api/job", ""), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != proto.JobStateDone || snapshot.Verify.Sampled != 1 || snapshot.Verify.Matched != 1 || snapshot.Verify.Mismatched != 0 {
		t.Fatalf("verification snapshot = %+v", snapshot)
	}
}

func TestDatasetManifestAndPathGuard(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mnist.bin"), []byte("dataset"), 0o600); err != nil {
		t.Fatal(err)
	}
	scheduler := sched.New(sched.Config{}, nil, nil)
	h, err := New(Config{Scheduler: scheduler, DatasetDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.dataset) != 1 || h.dataset[0].File != "mnist.bin" || h.dataset[0].Size != 7 || len(h.dataset[0].SHA256) != 64 {
		t.Fatalf("unexpected dataset manifest: %+v", h.dataset)
	}
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/dataset/../go.mod", nil))
	if bad.Code == http.StatusOK {
		t.Fatal("path traversal request unexpectedly succeeded")
	}
}

func TestBrowserWorkerFilesAreServed(t *testing.T) {
	scheduler := sched.New(sched.Config{}, nil, nil)
	h, err := New(Config{Scheduler: scheduler, WebDir: filepath.Join("..", "..", "web")})
	if err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{
		"/":                       "Live coordinator status",
		"/dashboard/dashboard.js": "renderLeaderboard",
		"/worker":                 "Scrap Heap worker",
		"/worker/worker.js":       "task-started",
		"/worker/thread.js":       "runBurn",
	} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("GET %s: status=%d, expected content %q", path, recorder.Code, expected)
		}
	}
}

func requestJSON(t *testing.T, handler http.Handler, method, path, body string) []byte {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	handler.ServeHTTP(recorder, request)
	if recorder.Code < 200 || recorder.Code >= 300 {
		t.Fatalf("%s %s status %d: %s", method, path, recorder.Code, recorder.Body.String())
	}
	return recorder.Body.Bytes()
}
