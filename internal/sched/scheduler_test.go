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
	t.Run("normal completion", func(t *testing.T) {
		now := startTime()
		r := newReducer()
		s := newTestScheduler(makeTasks(1), r)
		id, rejoined := s.Hello("alpha", WorkerInfo{CPU: "cpu"}, now)
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
		s := newTestScheduler(makeTasks(1), newReducer())
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

	t.Run("late result for requeued task wins once", func(t *testing.T) {
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
		if !out.Accepted {
			t.Fatalf("late result rejected: %+v", out)
		}
		if duplicate := s.Result(newID, new.TaskID, new.Attempt, []byte(`"other"`), 1, now.Add(2200*time.Millisecond)); duplicate.Accepted || duplicate.Reason != ReasonDuplicate {
			t.Fatalf("subsequent result outcome = %+v", duplicate)
		}
		if r.seen[old.TaskID] != 1 || r.workers[old.TaskID] != oldID {
			t.Fatalf("late result was not the sole accepted result: seen=%v workers=%v", r.seen, r.workers)
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
		s.Tick(now.Add(2 * time.Second))
		gotID, rejoined := s.Hello("alpha", WorkerInfo{CPU: "new cpu"}, now.Add(3*time.Second))
		if gotID != id || !rejoined {
			t.Fatalf("hello after loss got (%q,%v), want (%q,true)", gotID, rejoined, id)
		}
		if ok, unknown := s.Heartbeat(id, nil, now.Add(3*time.Second)); !ok || unknown {
			t.Fatalf("rejoined heartbeat = (%v,%v)", ok, unknown)
		}
		if got := s.Snapshot(now.Add(3 * time.Second)).Workers[0].Status; got != WorkerStatusRejoined {
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

func TestHeartbeatExtendsAllWorkerLeases(t *testing.T) {
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
	if got := s.Snapshot(now.Add(2500 * time.Millisecond)); got.InFlight != 2 || got.Requeued != 0 {
		t.Fatalf("heartbeat failed to extend every held lease: %+v", got)
	}
}
