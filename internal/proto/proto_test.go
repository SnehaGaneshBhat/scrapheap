package proto

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestRoundTripMessages(t *testing.T) {
	config := Config{Hidden: 128, LR: 0.01, Epochs: 5, Batch: 64, Seed: 12345}
	cases := []struct {
		name  string
		value any
	}{
		{"hello request", HelloRequest{Name: "laptop-01", CPU: "Intel Core i7-12700H", Cores: 14, Threads: 20, OS: "windows/amd64", Version: "dev", Browser: "Chrome"}},
		{"hello response", HelloResponse{WorkerID: "worker-01", LeaseSec: 30, HeartbeatSec: 10, JobKind: TaskKindMnist, Dataset: []DatasetFile{{File: "train-images-idx3-ubyte.gz", SHA256: "abc", Size: 9912422}}}},
		{"pull request", PullRequest{WorkerID: "worker-01"}},
		{"task response", TaskResponse{Status: PullStatusTask, TaskID: "task-0001", Kind: TaskKindBurn, Payload: json.RawMessage(`{"iters":1000000}`), LeaseDeadlineUnixMs: 1793030400000, Attempt: 1}},
		{"wait response", WaitResponse{Status: PullStatusWait, RetryAfterMs: 1000}},
		{"done response", DoneResponse{Status: PullStatusDone}},
		{"result request", ResultRequest{WorkerID: "worker-01", TaskID: "task-0001", Attempt: 1, Result: json.RawMessage(`{"checksum":"18446744073709551615"}`), Seconds: 2.75}},
		{"result response", ResultResponse{Accepted: true, Reason: ResultReasonOK}},
		{"heartbeat request", HeartbeatRequest{WorkerID: "worker-01", Running: []string{"task-0001", "task-0002"}}},
		{"heartbeat response", HeartbeatResponse{OK: true}},
		{"burn payload", BurnPayload{Iters: 1000000}},
		{"burn result", BurnResult{Checksum: "18446744073709551615"}},
		{"MNIST config", config},
		{"MNIST result", MnistResult{ValAcc: 0.97, ValLoss: 0.12, TrainSec: 8.43}},
		{"worker info", WorkerInfo{ID: "worker-01", CPU: "Intel Core i7-12700H", Cores: 14, Threads: 20, TasksDone: 10, Speed: 0.083, Status: WorkerStatusActive}},
		{"leaderboard entry", LeaderboardEntry{Config: config, ValAcc: 0.97}},
		{"verification stats", VerifyStats{Sampled: 3, Matched: 2, Mismatched: 1}},
		{"baseline info", BaselineInfo{Seconds: 12.4, Label: "single-worker"}},
		{"best result", BestResult{Config: config, ValAcc: 0.97}},
		{"job snapshot", JobSnapshot{JobID: "mnist-search-01", State: JobStateRunning, Total: 360, Done: 42, InFlight: 8, Requeued: 2, ElapsedSec: 120.5, ETASec: 900, Workers: []WorkerInfo{{ID: "worker-01", CPU: "Intel Core i7-12700H", Cores: 14, Threads: 20, TasksDone: 10, Speed: 0.083, Status: WorkerStatusActive}}, Leaderboard: []LeaderboardEntry{{Config: config, ValAcc: 0.97}}, Verify: VerifyStats{Sampled: 3, Matched: 2, Mismatched: 1}, Baseline: BaselineInfo{Seconds: 12.4, Label: "single-worker"}, Best: BestResult{Config: config, ValAcc: 0.97}}},
		{"API result response", APIResultResponse{Ready: false}},
		{"error response", ErrorResponse{Error: "unexpected end of JSON input"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			decoded := reflect.New(reflect.TypeOf(tc.value)).Interface()
			if err := json.Unmarshal(encoded, decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := reflect.ValueOf(decoded).Elem().Interface(); !reflect.DeepEqual(got, tc.value) {
				t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", got, tc.value)
			}
		})
	}
}

func TestProtocolDocumentationExamples(t *testing.T) {
	tests := []struct {
		file string
		into any
	}{
		{"hello-request.json", &HelloRequest{}},
		{"hello-response.json", &HelloResponse{}},
		{"pull-request.json", &PullRequest{}},
		{"pull-task.json", &TaskResponse{}},
		{"pull-wait.json", &WaitResponse{}},
		{"pull-done.json", &DoneResponse{}},
		{"result-request.json", &ResultRequest{}},
		{"result-accepted.json", &ResultResponse{}},
		{"result-duplicate.json", &ResultResponse{}},
		{"heartbeat-request.json", &HeartbeatRequest{}},
		{"heartbeat-known.json", &HeartbeatResponse{}},
		{"heartbeat-unknown.json", &HeartbeatResponse{}},
		{"burn-payload.json", &BurnPayload{}},
		{"burn-result.json", &BurnResult{}},
		{"mnist-config.json", &Config{}},
		{"mnist-result.json", &MnistResult{}},
		{"job-snapshot.json", &JobSnapshot{}},
		{"api-result-not-ready.json", &APIResultResponse{}},
		{"error.json", &ErrorResponse{}},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + tc.file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if err := json.Unmarshal(data, tc.into); err != nil {
				t.Fatalf("decode protocol example: %v", err)
			}
		})
	}
}
