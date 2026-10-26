package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scrapheap/internal/jobs/burn"
	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

func TestHTTPVerificationMismatchAppearsInJobSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "sample.bin"), []byte("dataset"), 0o600); err != nil {
		t.Fatal(err)
	}
	const iters = 1
	reducer := burn.NewReducer(iters)
	scheduler := sched.New(sched.Config{
		LeaseDur: time.Minute, HeartbeatTimeout: time.Minute,
		VerifyRate: 1, VerifySeed: 1, VerifyTimeout: time.Minute,
		Verifier: burn.NewVerifier(),
	}, burn.NewSource(1, iters), reducer)
	server := httptest.NewServer(New(scheduler, Config{DataDir: dataDir}).Handler())
	defer server.Close()
	client := server.Client()

	a, _ := helloWorker(t, client, server.URL, "verify-original")
	b, _ := helloWorker(t, client, server.URL, "verify-rerun")
	if response, err := callJSON(client, server.URL+"/api/job/start", http.MethodPost, nil); err != nil || response.status != http.StatusNoContent {
		t.Fatalf("start status=%d err=%v", response.status, err)
	}
	originalTask := httpPull(t, client, server.URL, a)
	originalResult, _ := json.Marshal(proto.BurnResult{Checksum: "1"})
	originalReply, err := callJSON(client, server.URL+"/w/result", http.MethodPost, proto.ResultRequest{
		WorkerID: a, TaskID: originalTask.TaskID, Attempt: originalTask.Attempt,
		Result: originalResult, Seconds: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := decodeBody[proto.ResultResponse](originalReply.body)
	if err != nil || originalReply.status != http.StatusOK || !accepted.Accepted {
		t.Fatalf("original result status=%d response=%+v err=%v", originalReply.status, accepted, err)
	}
	verification := httpPull(t, client, server.URL, b)
	if verification.TaskID != originalTask.TaskID || verification.Attempt <= originalTask.Attempt {
		t.Fatalf("verification assignment=%+v original=%+v", verification, originalTask)
	}
	rerunResult, _ := json.Marshal(proto.BurnResult{Checksum: "2"})
	rerunReply, err := callJSON(client, server.URL+"/w/result", http.MethodPost, proto.ResultRequest{
		WorkerID: b, TaskID: verification.TaskID, Attempt: verification.Attempt,
		Result: rerunResult, Seconds: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := decodeBody[proto.ResultResponse](rerunReply.body)
	if err != nil || rerunReply.status != http.StatusOK || !verified.Accepted {
		t.Fatalf("verification result status=%d response=%+v err=%v", rerunReply.status, verified, err)
	}

	jobReply, err := callJSON(client, server.URL+"/api/job", http.MethodGet, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		State  string `json:"state"`
		Done   int    `json:"done"`
		Total  int    `json:"total"`
		Verify struct {
			Sampled    int `json:"sampled"`
			Matched    int `json:"matched"`
			Mismatched int `json:"mismatched"`
			Skipped    int `json:"skipped"`
			Pending    int `json:"pending"`
			Mismatches []struct {
				TaskID         string          `json:"taskId"`
				OriginalWorker string          `json:"originalWorker"`
				VerifierWorker string          `json:"verifierWorker"`
				Original       json.RawMessage `json:"original"`
				Rerun          json.RawMessage `json:"rerun"`
			} `json:"mismatches"`
		} `json:"verify"`
	}
	if err := json.Unmarshal(jobReply.body, &got); err != nil {
		t.Fatal(err)
	}
	if jobReply.status != http.StatusOK || got.State != proto.JobStateDone || got.Done != 1 || got.Total != 1 {
		t.Fatalf("job response status=%d snapshot=%+v", jobReply.status, got)
	}
	if got.Verify.Sampled != 1 || got.Verify.Matched != 0 || got.Verify.Mismatched != 1 || got.Verify.Skipped != 0 || got.Verify.Pending != 0 || len(got.Verify.Mismatches) != 1 {
		t.Fatalf("verification snapshot=%+v", got.Verify)
	}
	mismatch := got.Verify.Mismatches[0]
	if mismatch.TaskID != originalTask.TaskID || mismatch.OriginalWorker != a || mismatch.VerifierWorker != b || string(mismatch.Original) != `{"checksum":"1"}` || string(mismatch.Rerun) != `{"checksum":"2"}` {
		t.Fatalf("mismatch details=%+v", mismatch)
	}
	if n, _ := reducer.Stats(); n != 1 {
		t.Fatalf("reducer accepted %d results, want only the original", n)
	}
}

func httpPull(t *testing.T, client *http.Client, baseURL, workerID string) proto.TaskResponse {
	t.Helper()
	response, err := callJSON(client, baseURL+"/w/pull", http.MethodPost, proto.PullRequest{WorkerID: workerID})
	if err != nil {
		t.Fatal(err)
	}
	task, err := decodeBody[proto.TaskResponse](response.body)
	if err != nil || response.status != http.StatusOK || task.Status != proto.PullStatusTask {
		t.Fatalf("pull status=%d task=%+v err=%v", response.status, task, err)
	}
	return task
}
