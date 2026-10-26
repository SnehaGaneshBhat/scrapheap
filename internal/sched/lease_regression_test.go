package sched

import (
	"testing"
	"time"
)

func TestHeartbeatLeaseRegression(t *testing.T) {
	t.Run("repeated heartbeats keep the lease alive", func(t *testing.T) {
		base := startTime()
		s := New(Config{LeaseDur: 6 * time.Second, HeartbeatTimeout: 12 * time.Second}, makeTasks(1), newReducer())
		workerID, _ := s.Hello("heartbeat-worker", WorkerInfo{}, base)
		s.Start(base)
		assignment := s.Pull(workerID, base)
		if assignment.Status != StatusTask {
			t.Fatalf("initial pull status=%q, want task", assignment.Status)
		}

		for _, elapsed := range []time.Duration{2, 4, 6, 8, 10} {
			now := base.Add(elapsed * time.Second)
			if ok, unknown := s.Heartbeat(workerID, []string{assignment.TaskID}, now); !ok || unknown {
				t.Fatalf("heartbeat at %s = (%v,%v)", elapsed*time.Second, ok, unknown)
			}
			s.Tick(now)
			snapshot := s.Snapshot(now)
			if snapshot.Requeued != 0 || snapshot.InFlight != 1 {
				t.Fatalf("task expired after heartbeat at %s: %+v", elapsed*time.Second, snapshot)
			}
			if got := snapshot.Workers[0].Status; got != WorkerStatusActive {
				t.Fatalf("worker status at %s = %q, want active", elapsed*time.Second, got)
			}
		}

		outcome := s.Result(workerID, assignment.TaskID, assignment.Attempt, []byte(`{"ok":true}`), 11, base.Add(11*time.Second))
		if !outcome.Accepted || outcome.Reason != ReasonOK {
			t.Fatalf("result at t=11s = %+v, want accepted", outcome)
		}
	})

	t.Run("control without heartbeats expires and loses the worker", func(t *testing.T) {
		base := startTime()
		s := New(Config{LeaseDur: 6 * time.Second, HeartbeatTimeout: 12 * time.Second}, makeTasks(1), newReducer())
		workerID, _ := s.Hello("silent-worker", WorkerInfo{}, base)
		s.Start(base)
		assignment := s.Pull(workerID, base)
		if assignment.Status != StatusTask {
			t.Fatalf("initial pull status=%q, want task", assignment.Status)
		}

		now := base.Add(7 * time.Second)
		s.Tick(now)
		snapshot := s.Snapshot(now)
		if snapshot.Requeued != 1 || snapshot.InFlight != 0 {
			t.Fatalf("no-heartbeat expiry snapshot: %+v", snapshot)
		}
		if got := snapshot.Workers[0].Status; got != WorkerStatusLost {
			t.Fatalf("worker status=%q, want lost", got)
		}
	})

	t.Run("only listed tasks receive a lease extension", func(t *testing.T) {
		base := startTime()
		s := New(Config{LeaseDur: 6 * time.Second, HeartbeatTimeout: 30 * time.Second}, makeTasks(2), newReducer())
		workerID, _ := s.Hello("partial-worker", WorkerInfo{}, base)
		s.Start(base)
		first := s.Pull(workerID, base)
		second := s.Pull(workerID, base)
		if first.Status != StatusTask || second.Status != StatusTask {
			t.Fatalf("initial assignments: first=%+v second=%+v", first, second)
		}

		heartbeatAt := base.Add(2 * time.Second)
		if ok, unknown := s.Heartbeat(workerID, []string{first.TaskID}, heartbeatAt); !ok || unknown {
			t.Fatalf("partial heartbeat = (%v,%v)", ok, unknown)
		}
		listed := s.tasks[first.TaskID]
		unlisted := s.tasks[second.TaskID]
		if want := base.Add(8 * time.Second); !listed.deadline.Equal(want) {
			t.Errorf("listed task deadline=%s, want %s", listed.deadline, want)
		}
		if want := base.Add(6 * time.Second); !unlisted.deadline.Equal(want) {
			t.Errorf("unlisted task deadline=%s, want unchanged deadline %s", unlisted.deadline, want)
		}

		late := base.Add(7 * time.Second)
		outcome := s.Result(workerID, first.TaskID, first.Attempt, []byte(`{"ok":true}`), 7, late)
		if !outcome.Accepted {
			t.Fatalf("result for listed task before its extended deadline rejected: %+v", outcome)
		}
		s.Tick(late)
		snapshot := s.Snapshot(late)
		if snapshot.Done != 1 || snapshot.Requeued != 1 || snapshot.InFlight != 0 {
			t.Fatalf("unlisted task did not expire independently: %+v", snapshot)
		}
	})

	t.Run("unknown worker is reported", func(t *testing.T) {
		s := New(Config{LeaseDur: 6 * time.Second}, makeTasks(1), newReducer())
		ok, unknown := s.Heartbeat("not-registered", []string{"task-0000"}, startTime())
		if ok || !unknown {
			t.Fatalf("unknown heartbeat=(%v,%v), want (false,true)", ok, unknown)
		}
	})
}

func TestEmptyHeartbeatKeepsWorkerAliveWithoutExtendingLeases(t *testing.T) {
	base := startTime()
	s := New(Config{LeaseDur: 6 * time.Second, HeartbeatTimeout: 12 * time.Second}, makeTasks(1), newReducer())
	workerID, _ := s.Hello("empty-heartbeat-worker", WorkerInfo{}, base)
	s.Start(base)
	assignment := s.Pull(workerID, base)
	if assignment.Status != StatusTask {
		t.Fatalf("initial pull status=%q, want task", assignment.Status)
	}

	heartbeatAt := base.Add(2 * time.Second)
	if ok, unknown := s.Heartbeat(workerID, []string{}, heartbeatAt); !ok || unknown {
		t.Fatalf("empty heartbeat=(%v,%v), want (true,false)", ok, unknown)
	}
	beforeExpiry := base.Add(5 * time.Second)
	s.Tick(beforeExpiry)
	if snapshot := s.Snapshot(beforeExpiry); snapshot.Requeued != 0 || snapshot.InFlight != 1 || snapshot.Workers[0].Status != WorkerStatusActive {
		t.Fatalf("empty heartbeat should keep worker alive before lease expiry: %+v", snapshot)
	}

	afterExpiry := base.Add(7 * time.Second)
	s.Tick(afterExpiry)
	if snapshot := s.Snapshot(afterExpiry); snapshot.Requeued != 1 || snapshot.InFlight != 0 || snapshot.Workers[0].Status != WorkerStatusLost {
		t.Fatalf("empty heartbeat must not extend task lease: %+v", snapshot)
	}
}

func TestHeartbeatIgnoresTasksTheWorkerDoesNotHold(t *testing.T) {
	base := startTime()
	s := New(Config{LeaseDur: 6 * time.Second, HeartbeatTimeout: 12 * time.Second}, makeTasks(1), newReducer())
	ownerID, _ := s.Hello("owner", WorkerInfo{}, base)
	otherID, _ := s.Hello("other", WorkerInfo{}, base)
	s.Start(base)
	assignment := s.Pull(ownerID, base)
	if assignment.Status != StatusTask {
		t.Fatalf("owner pull status=%q, want task", assignment.Status)
	}

	if ok, unknown := s.Heartbeat(otherID, []string{assignment.TaskID, "unknown-task"}, base.Add(2*time.Second)); !ok || unknown {
		t.Fatalf("heartbeat with unheld IDs=(%v,%v), want (true,false)", ok, unknown)
	}
	if got, want := s.tasks[assignment.TaskID].deadline, base.Add(6*time.Second); !got.Equal(want) {
		t.Fatalf("unheld task deadline=%s, want %s", got, want)
	}
}

func TestPullDoneOnlyWhenAllTasksComplete(t *testing.T) {
	t.Run("in-flight work means wait until requeued work completes", func(t *testing.T) {
		base := startTime()
		s := New(Config{LeaseDur: 6 * time.Second}, makeTasks(3), newReducer())
		workerA, _ := s.Hello("worker-a", WorkerInfo{}, base)
		workerB, _ := s.Hello("worker-b", WorkerInfo{}, base)
		s.Start(base)

		inFlight := s.Pull(workerA, base)
		taskTwo := s.Pull(workerB, base)
		taskThree := s.Pull(workerB, base)
		if inFlight.Status != StatusTask || taskTwo.Status != StatusTask || taskThree.Status != StatusTask {
			t.Fatalf("initial assignments: A=%+v B1=%+v B2=%+v", inFlight, taskTwo, taskThree)
		}
		for _, assignment := range []Assignment{taskTwo, taskThree} {
			outcome := s.Result(workerB, assignment.TaskID, assignment.Attempt, []byte(`true`), 1, base)
			if !outcome.Accepted {
				t.Fatalf("complete %s: %+v", assignment.TaskID, outcome)
			}
		}
		if got := s.Pull(workerB, base); got.Status != StatusWait {
			t.Fatalf("pull with one task in flight returned %q, want wait", got.Status)
		}

		expiredAt := base.Add(7 * time.Second)
		s.Tick(expiredAt)
		if snapshot := s.Snapshot(expiredAt); snapshot.Requeued != 1 || snapshot.Done != 2 {
			t.Fatalf("after A lease expiry: %+v", snapshot)
		}
		reissued := s.Pull(workerB, expiredAt)
		if reissued.Status != StatusTask || reissued.TaskID != inFlight.TaskID {
			t.Fatalf("B pull after requeue=%+v, want task %s", reissued, inFlight.TaskID)
		}
		outcome := s.Result(workerB, reissued.TaskID, reissued.Attempt, []byte(`true`), 1, expiredAt)
		if !outcome.Accepted {
			t.Fatalf("complete reissued task: %+v", outcome)
		}
		if got := s.Pull(workerB, expiredAt); got.Status != StatusDone {
			t.Fatalf("pull after all tasks complete returned %q, want done", got.Status)
		}
	})

	t.Run("before start returns wait", func(t *testing.T) {
		now := startTime()
		s := New(Config{LeaseDur: 6 * time.Second}, makeTasks(1), newReducer())
		workerID, _ := s.Hello("early-worker", WorkerInfo{}, now)
		if got := s.Pull(workerID, now); got.Status != StatusWait {
			t.Fatalf("pull before start returned %q, want wait", got.Status)
		}
	})
}
