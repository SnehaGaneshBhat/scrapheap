package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"scrapheap/internal/jobs/burn"
	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

type heartbeatRegressionSource []sched.TaskSpec

func (s heartbeatRegressionSource) Tasks() []sched.TaskSpec { return s }

func TestHubHeartbeatKeepsLongTaskLease(t *testing.T) {
	const iters = 13
	const taskDuration = 1200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	source := heartbeatRegressionSource{{
		ID: "long-burn", Kind: proto.TaskKindBurn, Payload: []byte(`{"iters":13}`),
	}}
	reducer := burn.NewReducer(iters)
	scheduler := sched.New(sched.Config{
		LeaseDur: 300 * time.Millisecond, HeartbeatTimeout: 300 * time.Millisecond,
	}, source, reducer)
	serverHub := New(scheduler, Config{JobID: "heartbeat-regression", JobKind: proto.TaskKindBurn, LeaseSec: 1, HeartbeatSec: 1})
	serverHub.Run(ctx)
	server := httptest.NewServer(serverHub.Handler())
	defer func() {
		cancel()
		server.Close()
	}()
	client := server.Client()

	hello, err := heartbeatRegressionPost[proto.HelloResponse](ctx, client, server.URL+"/w/hello", proto.HelloRequest{
		Name: "slow-worker", CPU: "test", Cores: 1, Threads: 1, OS: "test", Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	startRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/job/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	startResponse, err := client.Do(startRequest)
	if err != nil {
		t.Fatal(err)
	}
	startResponse.Body.Close()
	if startResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("start returned HTTP %d, want 204", startResponse.StatusCode)
	}
	assignment, err := heartbeatRegressionPost[proto.TaskResponse](ctx, client, server.URL+"/w/pull", proto.PullRequest{WorkerID: hello.WorkerID})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.Status != proto.PullStatusTask || assignment.TaskID != "long-burn" {
		t.Fatalf("pull assignment=%+v, want long-burn task", assignment)
	}

	heartbeatCtx, stopHeartbeats := context.WithCancel(ctx)
	heartbeatDone := make(chan error, 1)
	var heartbeatCount atomic.Int32
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				reply, err := heartbeatRegressionPost[proto.HeartbeatResponse](heartbeatCtx, client, server.URL+"/w/heartbeat", proto.HeartbeatRequest{
					WorkerID: hello.WorkerID, Running: []string{assignment.TaskID},
				})
				if err != nil {
					if heartbeatCtx.Err() != nil {
						heartbeatDone <- nil
					} else {
						heartbeatDone <- err
					}
					return
				}
				if !reply.OK || reply.UnknownWorker {
					heartbeatDone <- fmt.Errorf("heartbeat response=%+v", reply)
					return
				}
				heartbeatCount.Add(1)
			}
		}
	}()

	workTimer := time.NewTimer(taskDuration)
	select {
	case <-workTimer.C:
	case <-ctx.Done():
		workTimer.Stop()
		stopHeartbeats()
		<-heartbeatDone
		t.Fatalf("task wait ended before %s: %v", taskDuration, ctx.Err())
	}
	stopHeartbeats()
	if err := <-heartbeatDone; err != nil {
		t.Fatalf("heartbeat loop: %v", err)
	}
	if got := heartbeatCount.Load(); got < 9 {
		t.Fatalf("sent %d heartbeats during %s, want at least 9", got, taskDuration)
	}

	resultJSON, err := json.Marshal(proto.BurnResult{Checksum: strconv.FormatUint(burn.Burn(iters), 10)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := heartbeatRegressionPost[proto.ResultResponse](ctx, client, server.URL+"/w/result", proto.ResultRequest{
		WorkerID: hello.WorkerID, TaskID: assignment.TaskID, Attempt: assignment.Attempt,
		Result: resultJSON, Seconds: taskDuration.Seconds(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.Reason != proto.ResultReasonOK {
		t.Fatalf("long-running task result=%+v, want accepted", result)
	}

	job, err := heartbeatRegressionGetJob(ctx, client, server.URL+"/api/job")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != proto.JobStateDone || job.Done != 1 || job.Total != 1 || job.Requeued != 0 {
		t.Fatalf("long-running task snapshot: %+v", job)
	}
	if accepted, wrong := reducer.Stats(); accepted != 1 || wrong != 0 {
		t.Fatalf("burn reducer stats=(%d,%d), want (1,0)", accepted, wrong)
	}
}

func heartbeatRegressionPost[T any](ctx context.Context, client *http.Client, endpoint string, body any) (T, error) {
	var zero T
	data, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
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
	var value T
	if err := json.NewDecoder(resp.Body).Decode(&value); err != nil {
		return zero, err
	}
	return value, nil
}

func heartbeatRegressionGetJob(ctx context.Context, client *http.Client, endpoint string) (proto.JobSnapshot, error) {
	var snapshot proto.JobSnapshot
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return snapshot, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return snapshot, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return snapshot, fmt.Errorf("GET %s returned HTTP %d", endpoint, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}
