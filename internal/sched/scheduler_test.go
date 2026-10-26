package sched

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

type taskList []TaskSpec

func (tasks taskList) Tasks() []TaskSpec { return tasks }

type recordingReducer struct {
	seen    map[string]int
	workers map[string]string
}

func (r *recordingReducer) Accept(spec TaskSpec, result []byte, workerID string) {
	r.seen[spec.ID]++
	r.workers[spec.ID] = workerID
}

func newReducer() *recordingReducer {
	return &recordingReducer{seen: make(map[string]int), workers: make(map[string]string)}
}

func makeTasks(count int) taskList {
	out := make(taskList, count)
	for i := range count {
		out[i] = TaskSpec{ID: fmt.Sprintf("task-%04d", i), Kind: "opaque-kind", Payload: []byte(`{"n":1}`), Cost: 1}
	}
	return out
}

func newTestScheduler(tasks taskList, reducer Reducer) *Scheduler {
	return New(Config{LeaseDur: 2 * time.Second, HeartbeatTimeout: 10 * time.Second}, tasks, reducer)
}

func startTime() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func TestLifecycleScenarios(t *testing.T) {
	t.Run("repeat hello from an active worker is not a rejoin", func(t *testing.T) {
		now := startTime()
		s := newTestScheduler(makeTasks(1), newReducer())
		id, rejoined := s.Hello("alpha", WorkerInfo{Browser: "Chrome"}, now)
		if rejoined {
			t.Fatal("first hello unexpectedly reported rejoin")
		}
		gotID, rejoined := s.Hello("alpha", WorkerInfo{Browser: "Firefox"}, now.Add(time.Second))
		if gotID != id || rejoined {
			t.Fatalf("repeat hello = (%q,%v), want (%q,false)", gotID, rejoined, id)
		}
	})

	t.Run("normal completion", func(t *testing.T) {
		now := startTime()
		r := newReducer()
		s := newTestScheduler(makeTasks(1), r)
		id, rejoined := s.Hello("alpha", WorkerInfo{Browser: "Chrome"}, now)
		if rejoined {
			t.Fatal("first hello unexpectedly rejoined")
		}
		s.Start(now)
		a := s.Pull(id, now)
		if a.Status != StatusTask || a.Attempt != 1 || a.TaskID != "task-0000" {
			t.Fatalf("unexpected assignment: %+v", a)
		}
		out := s.Result(id, a.TaskID, a.Attempt, []byte(`{"ok":true}`), 1.25, now.Add(time.Second))
		if !out.Accepted || out.Reason != ReasonOK {
			t.Fatalf("unexpected result outcome: %+v", out)
		}
		if got := s.Pull(id, now.Add(time.Second)); got.Status != StatusDone {
			t.Fatalf("pull after completion = %q, want done", got.Status)
		}
		if r.seen[a.TaskID] != 1 {
			t.Fatalf("reducer accepted task %d times", r.seen[a.TaskID])
		}
		snap := s.Snapshot(now.Add(2 * time.Second))
		if snap.State != JobStateDone || snap.Done != 1 || snap.InFlight != 0 || snap.Workers[0].TasksDone != 1 {
			t.Fatalf("unexpected snapshot: %+v", snap)
		}
	})

	t.Run("worker dies and task is requeued exactly once", func(t *testing.T) {
		now := startTime()
		s := New(Config{LeaseDur: 2 * time.Second, HeartbeatTimeout: 2 * time.Second}, makeTasks(1), newReducer())
		id, _ := s.Hello("alpha", WorkerInfo{}, now)
		s.Start(now)
		first := s.Pull(id, now)
		s.Tick(now.Add(2 * time.Second))
		if got := s.Snapshot(now.Add(2 * time.Second)); got.Requeued != 1 || got.InFlight != 0 {
			t.Fatalf("unexpected post-expiry snapshot: %+v", got)
		}
		s.Tick(now.Add(4 * time.Second))
		if got := s.Snapshot(now.Add(4 * time.Second)); got.Requeued != 1 {
			t.Fatalf("requeued more than once: %+v", got)
		}
		if got := s.Pull(id, now.Add(4*time.Second)); got.Status != StatusWait {
			t.Fatalf("lost worker pull status = %q, want wait", got.Status)
		}
		s.Hello("bravo", WorkerInfo{}, now.Add(4*time.Second))
		sid, _ := s.Hello("bravo", WorkerInfo{}, now.Add(4*time.Second))
		second := s.Pull(sid, now.Add(4*time.Second))
		if second.Status != StatusTask || second.TaskID != first.TaskID || second.Attempt != first.Attempt+1 {
			t.Fatalf("unexpected reissued task: first=%+v second=%+v", first, second)
		}
	})

	t.Run("completed task duplicate is ignored", func(t *testing.T) {
		now := startTime()
		r := newReducer()
		s := newTestScheduler(makeTasks(1), r)
		id, _ := s.Hello("alpha", WorkerInfo{}, now)
		s.Start(now)
		a := s.Pull(id, now)
		if out := s.Result(id, a.TaskID, a.Attempt, []byte(`1`), 1, now); !out.Accepted {
			t.Fatalf("first result rejected: %+v", out)
		}
		out := s.Result(id, a.TaskID, a.Attempt, []byte(`2`), 1, now)
		if out.Accepted || out.Reason != ReasonDuplicate {
			t.Fatalf("duplicate outcome = %+v", out)
		}
		if r.seen[a.TaskID] != 1 {
			t.Fatalf("reducer accepted duplicate: count=%d", r.seen[a.TaskID])
		}
	})

	t.Run("late result from an expired attempt is stale", func(t *testing.T) {
		now := startTime()
		r := newReducer()
		s := newTestScheduler(makeTasks(1), r)
		oldID, _ := s.Hello("old", WorkerInfo{}, now)
		newID, _ := s.Hello("new", WorkerInfo{}, now)
		s.Start(now)
		old := s.Pull(oldID, now)
		s.Tick(now.Add(2 * time.Second))
		new := s.Pull(newID, now.Add(2*time.Second))
		if new.Attempt != old.Attempt+1 {
			t.Fatalf("attempts old=%d new=%d", old.Attempt, new.Attempt)
		}
		out := s.Result(oldID, old.TaskID, old.Attempt, []byte(`"late"`), 1.5, now.Add(2100*time.Millisecond))
		if out.Accepted || out.Reason != ReasonStale {
			t.Fatalf("expired attempt outcome = %+v, want stale", out)
		}
		if accepted := s.Result(newID, new.TaskID, new.Attempt, []byte(`"current"`), 1, now.Add(2200*time.Millisecond)); !accepted.Accepted {
			t.Fatalf("current attempt rejected: %+v", accepted)
		}
		if duplicate := s.Result(newID, new.TaskID, new.Attempt, []byte(`"other"`), 1, now.Add(2300*time.Millisecond)); duplicate.Accepted || duplicate.Reason != ReasonDuplicate {
			t.Fatalf("duplicate result outcome = %+v", duplicate)
		}
		if r.seen[old.TaskID] != 1 || r.workers[old.TaskID] != newID {
			t.Fatalf("current result was not the sole accepted result: seen=%v workers=%v", r.seen, r.workers)
		}
		if snap := s.Snapshot(now.Add(3 * time.Second)); snap.Requeued != 1 || snap.InFlight != 0 || snap.Done != 1 {
			t.Fatalf("unexpected snapshot: %+v", snap)
		}
	})

	t.Run("late join mid-job", func(t *testing.T) {
		now := startTime()
		s := newTestScheduler(makeTasks(2), newReducer())
		s.Start(now)
		id, rejoined := s.Hello("late", WorkerInfo{}, now.Add(time.Second))
		if rejoined {
			t.Fatal("new mid-job worker reported rejoined")
		}
		if got := s.Pull(id, now.Add(time.Second)); got.Status != StatusTask {
			t.Fatalf("late worker pull status = %q", got.Status)
		}
	})

	t.Run("rejoin after lost", func(t *testing.T) {
		now := startTime()
		s := newTestScheduler(makeTasks(1), newReducer())
		id, _ := s.Hello("alpha", WorkerInfo{}, now)
		s.Start(now)
		s.Pull(id, now)
		s.Tick(now.Add(10 * time.Second))
		gotID, rejoined := s.Hello("alpha", WorkerInfo{Browser: "Chrome"}, now.Add(11*time.Second))
		if gotID != id || !rejoined {
			t.Fatalf("hello after loss got (%q,%v), want (%q,true)", gotID, rejoined, id)
		}
		if ok, unknown := s.Heartbeat(id, nil, now.Add(11*time.Second)); !ok || unknown {
			t.Fatalf("rejoined heartbeat = (%v,%v)", ok, unknown)
		}
		if got := s.Snapshot(now.Add(11 * time.Second)).Workers[0].Status; got != WorkerStatusRejoined {
			t.Fatalf("worker status = %q, want rejoined", got)
		}
	})

	t.Run("pull before start waits", func(t *testing.T) {
		now := startTime()
		s := newTestScheduler(makeTasks(1), newReducer())
		id, _ := s.Hello("alpha", WorkerInfo{}, now)
		if got := s.Pull(id, now); got.Status != StatusWait {
			t.Fatalf("pull status = %q, want wait", got.Status)
		}
	})
}

func TestRandomizedThousandTaskSimulation(t *testing.T) {
	now := startTime()
	rng := rand.New(rand.NewSource(0x5eed))
	tasks := makeTasks(1000)
	reducer := newReducer()
	s := newTestScheduler(tasks, reducer)
	const workers = 8
	workerIDs := make([]string, workers)
	alive := make([]bool, workers)
	for i := range workers {
		workerIDs[i], _ = s.Hello(fmt.Sprintf("worker-%d", i), WorkerInfo{}, now)
		alive[i] = true
	}
	s.Start(now)

	for step := 0; step < 10000; step++ {
		now = now.Add(time.Second)
		for i := range workers {
			if !alive[i] {
				workerIDs[i], _ = s.Hello(fmt.Sprintf("worker-%d", i), WorkerInfo{}, now)
				alive[i] = true
			}
			if rng.Float64() < 0.035 {
				alive[i] = false
				continue
			}
			assignment := s.Pull(workerIDs[i], now)
			if assignment.Status == StatusTask {
				s.Result(workerIDs[i], assignment.TaskID, assignment.Attempt, []byte(`true`), 0.5, now)
			} else {
				s.Heartbeat(workerIDs[i], nil, now)
			}
		}
		s.Tick(now)
		if s.Snapshot(now).Done == len(tasks) {
			break
		}
	}

	if got := s.Snapshot(now).Done; got != len(tasks) {
		t.Fatalf("simulation ended with %d/%d tasks done", got, len(tasks))
	}
	if len(reducer.seen) != len(tasks) {
		t.Fatalf("reducer saw %d unique tasks, want %d", len(reducer.seen), len(tasks))
	}
	for _, task := range tasks {
		if reducer.seen[task.ID] != 1 {
			t.Fatalf("task %s accepted %d times", task.ID, reducer.seen[task.ID])
		}
	}
}

func TestPullOrdersTasksByEstimatedCostWithStableTies(t *testing.T) {
	now := startTime()
	tasks := taskList{
		{ID: "light", Kind: "burn", Payload: []byte(`1`), Cost: 1},
		{ID: "heavy-a", Kind: "burn", Payload: []byte(`1`), Cost: 9},
		{ID: "medium", Kind: "burn", Payload: []byte(`1`), Cost: 5},
		{ID: "heavy-b", Kind: "burn", Payload: []byte(`1`), Cost: 9},
	}
	s := New(Config{LeaseDur: time.Minute}, tasks, nil)
	workerID, _ := s.Hello("worker", WorkerInfo{}, now)
	s.Start(now)
	var got []string
	for range len(tasks) {
		got = append(got, s.Pull(workerID, now).TaskID)
	}
	want := []string{"heavy-a", "heavy-b", "medium", "light"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pull order = %v, want %v", got, want)
	}
}

func TestSnapshotUsesMeasuredWorkerSpeedForETA(t *testing.T) {
	now := startTime()
	s := New(Config{LeaseDur: time.Minute}, makeTasks(2), nil)
	workerID, _ := s.Hello("fast", WorkerInfo{Threads: 4}, now)
	s.Start(now)
	assignment := s.Pull(workerID, now)
	if out := s.Result(workerID, assignment.TaskID, assignment.Attempt, []byte(`true`), 2, now.Add(2*time.Second)); !out.Accepted {
		t.Fatalf("result rejected: %+v", out)
	}
	snapshot := s.Snapshot(now.Add(2 * time.Second))
	if got := snapshot.Workers[0].Speed; got != 2 {
		t.Fatalf("worker speed = %v, want 2 tasks/sec", got)
	}
	if snapshot.ETA != 500*time.Millisecond {
		t.Fatalf("ETA = %v, want 500ms", snapshot.ETA)
	}
}

func TestHeartbeatExtendsOnlyListedLeases(t *testing.T) {
	now := startTime()
	s := newTestScheduler(makeTasks(2), newReducer())
	id, _ := s.Hello("alpha", WorkerInfo{}, now)
	s.Start(now)
	s.Pull(id, now)
	s.Pull(id, now)
	if ok, unknown := s.Heartbeat(id, []string{"task-0000"}, now.Add(time.Second)); !ok || unknown {
		t.Fatalf("heartbeat = (%v,%v)", ok, unknown)
	}
	s.Tick(now.Add(2500 * time.Millisecond))
	if got := s.Snapshot(now.Add(2500 * time.Millisecond)); got.InFlight != 1 || got.Requeued != 1 {
		t.Fatalf("heartbeat did not limit its extension to the listed task: %+v", got)
	}
}

func TestSampledTaskWaitsForDifferentWorkerAndRecordsMatch(t *testing.T) {
	now := startTime()
	tasks := makeTasks(1)
	verifier := SampleVerifierFunc(func(_ TaskSpec, first, second []byte) bool { return string(first) == string(second) })
	s := New(Config{LeaseDur: 2 * time.Second, HeartbeatTimeout: 10 * time.Second, VerifyRate: 1, Verifier: verifier}, tasks, nil)
	firstWorker, _ := s.Hello("first", WorkerInfo{}, now)
	secondWorker, _ := s.Hello("second", WorkerInfo{}, now)
	s.Start(now)
	first := s.Pull(firstWorker, now)
	if out := s.Result(firstWorker, first.TaskID, first.Attempt, []byte(`{"score":1}`), 1, now); !out.Accepted {
		t.Fatalf("first result rejected: %+v", out)
	}
	if got := s.Pull(firstWorker, now.Add(time.Second)); got.Status != StatusWait {
		t.Fatalf("original worker received its own verification: %+v", got)
	}
	if snapshot := s.Snapshot(now.Add(time.Second)); snapshot.State != JobStateRunning || snapshot.Done != 1 || snapshot.Verify.Sampled != 1 || snapshot.InFlight != 0 {
		t.Fatalf("unexpected pending-verification snapshot: %+v", snapshot)
	}
	verification := s.Pull(secondWorker, now.Add(time.Second))
	if verification.Status != StatusTask || verification.TaskID != first.TaskID || verification.Attempt != first.Attempt+1 {
		t.Fatalf("unexpected verification assignment: first=%+v verify=%+v", first, verification)
	}
	if out := s.Result(secondWorker, verification.TaskID, verification.Attempt, []byte(`{"score":1}`), 1, now.Add(1500*time.Millisecond)); !out.Accepted {
		t.Fatalf("verification result rejected: %+v", out)
	}
	snapshot := s.Snapshot(now.Add(2 * time.Second))
	if snapshot.State != JobStateDone || snapshot.Verify.Sampled != 1 || snapshot.Verify.Matched != 1 || snapshot.Verify.Mismatched != 0 {
		t.Fatalf("unexpected completed-verification snapshot: %+v", snapshot)
	}
}

func TestSampleVerifierRecordsMismatch(t *testing.T) {
	now := startTime()
	verifier := SampleVerifierFunc(func(_ TaskSpec, first, second []byte) bool { return string(first) == string(second) })
	s := New(Config{LeaseDur: 2 * time.Second, VerifyRate: 1, Verifier: verifier}, makeTasks(1), nil)
	a, _ := s.Hello("a", WorkerInfo{}, now)
	b, _ := s.Hello("b", WorkerInfo{}, now)
	s.Start(now)
	first := s.Pull(a, now)
	s.Result(a, first.TaskID, first.Attempt, []byte("first"), 1, now)
	check := s.Pull(b, now)
	s.Result(b, check.TaskID, check.Attempt, []byte("different"), 1, now)
	if got := s.Snapshot(now).Verify; got.Sampled != 1 || got.Matched != 0 || got.Mismatched != 1 {
		t.Fatalf("verification mismatch was not recorded: %+v", got)
	}
}
