package sched

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

type byteVerifier struct{ match bool }

func (v byteVerifier) Match(_ TaskSpec, original, rerun []byte) bool {
	return v.match && bytes.Equal(original, rerun)
}

func verificationScheduler(taskCount int, seed uint64, verifier Verifier) *Scheduler {
	return New(Config{
		LeaseDur: 2 * time.Second, HeartbeatTimeout: 10 * time.Second,
		VerifyRate: 1, VerifySeed: seed, VerifyTimeout: 5 * time.Second, Verifier: verifier,
	}, makeTasks(taskCount), newReducer())
}

func completeOriginal(t *testing.T, s *Scheduler, worker, taskID string, now time.Time, result []byte) {
	t.Helper()
	out := s.Result(worker, taskID, 1, result, 0.1, now)
	if !out.Accepted || out.Reason != ReasonOK {
		t.Fatalf("original result = %+v", out)
	}
}

func TestVerificationSamplingIsDeterministicAndSeeded(t *testing.T) {
	newSet := func(seed uint64) []string {
		s := New(Config{VerifyRate: 0.37, VerifySeed: seed}, makeTasks(1000), nil)
		var ids []string
		for _, id := range s.taskOrder {
			if s.tasks[id].sampled {
				ids = append(ids, id)
			}
		}
		return ids
	}
	first := newSet(1)
	if got := newSet(1); fmt.Sprint(got) != fmt.Sprint(first) {
		t.Fatal("same seed produced a different sampled task set")
	}
	if got := newSet(2); fmt.Sprint(got) == fmt.Sprint(first) {
		t.Fatal("different seeds produced the same sampled task set")
	}
	if got := New(Config{}, makeTasks(5), nil).Snapshot(startTime()).Verify.Sampled; got != 0 {
		t.Fatalf("rate zero sampled %d tasks, want 0", got)
	}
}

func TestVerificationRequiresDifferentWorkerAndBypassesReducer(t *testing.T) {
	now := startTime()
	reducer := newReducer()
	s := New(Config{LeaseDur: time.Minute, HeartbeatTimeout: time.Minute, VerifyRate: 1, VerifySeed: 1, VerifyTimeout: time.Minute, Verifier: byteVerifier{match: true}}, makeTasks(2), reducer)
	a, _ := s.Hello("A", WorkerInfo{}, now)
	b, _ := s.Hello("B", WorkerInfo{}, now)
	s.Start(now)
	first := s.Pull(a, now)
	if first.TaskID != "task-0000" {
		t.Fatalf("first normal task = %q", first.TaskID)
	}
	completeOriginal(t, s, a, first.TaskID, now, []byte(`{"v":1}`))
	// An eligible verification takes priority over the still-queued normal task.
	if got := s.Pull(a, now); got.Status != StatusTask || got.TaskID != "task-0001" {
		t.Fatalf("original worker should not verify its own result; next pull = %+v", got)
	}
	// Finish the second task without sampling assertions becoming worker-dependent.
	completeOriginal(t, s, a, "task-0001", now, []byte(`{"v":1}`))
	if got := s.Pull(a, now); got.Status != StatusWait {
		t.Fatalf("same worker received its own verification: %+v", got)
	}
	verification := s.Pull(b, now)
	if verification.Status != StatusTask || verification.TaskID != first.TaskID || verification.Attempt <= first.Attempt {
		t.Fatalf("different worker did not get fresh verification: %+v", verification)
	}
	// Deliberately use the original attempt number: the held verification lease,
	// not the submitted attempt, selects the verification result path.
	out := s.Result(b, first.TaskID, first.Attempt, []byte(`{"v":1}`), 0.1, now)
	if !out.Accepted || out.Reason != ReasonOK {
		t.Fatalf("verification result = %+v", out)
	}
	if reducer.seen[first.TaskID] != 1 || reducer.seen["task-0001"] != 1 {
		t.Fatalf("reducer received verification result: %+v", reducer.seen)
	}
	if got := s.Snapshot(now).Verify.Matched; got != 1 {
		t.Fatalf("matched=%d, want 1", got)
	}
}

func TestVerificationMismatchRecordsBothOpaqueResults(t *testing.T) {
	now := startTime()
	s := verificationScheduler(1, 1, byteVerifier{})
	a, _ := s.Hello("A", WorkerInfo{}, now)
	b, _ := s.Hello("B", WorkerInfo{}, now)
	s.Start(now)
	task := s.Pull(a, now)
	original := []byte(`{"v":1}`)
	rerun := []byte(`{"v":2}`)
	completeOriginal(t, s, a, task.TaskID, now, original)
	verification := s.Pull(b, now)
	out := s.Result(b, verification.TaskID, verification.Attempt, rerun, 0.1, now)
	if !out.Accepted {
		t.Fatalf("mismatching verification was not accepted: %+v", out)
	}
	snapshot := s.Snapshot(now)
	if snapshot.Verify.Mismatched != 1 || snapshot.Verify.Matched != 0 || snapshot.Verify.Pending != 0 {
		t.Fatalf("verification counts = %+v", snapshot.Verify)
	}
	if len(snapshot.Verify.Mismatches) != 1 {
		t.Fatalf("mismatch records = %+v", snapshot.Verify.Mismatches)
	}
	mismatch := snapshot.Verify.Mismatches[0]
	if mismatch.TaskID != task.TaskID || mismatch.OriginalWorker != a || mismatch.VerifierWorker != b || !bytes.Equal(mismatch.Original, original) || !bytes.Equal(mismatch.Rerun, rerun) {
		t.Fatalf("mismatch record = %+v", mismatch)
	}
	if snapshot.Done != 1 || snapshot.State != JobStateDone {
		t.Fatalf("original task completion was lost: %+v", snapshot)
	}
}

func TestEligibleVerificationHasPriorityOverNormalQueue(t *testing.T) {
	now := startTime()
	s := verificationScheduler(2, 1, byteVerifier{match: true})
	a, _ := s.Hello("A", WorkerInfo{}, now)
	b, _ := s.Hello("B", WorkerInfo{}, now)
	s.Start(now)
	original := s.Pull(a, now)
	completeOriginal(t, s, a, original.TaskID, now, []byte(`same`))
	verification := s.Pull(b, now)
	if verification.Status != StatusTask || verification.TaskID != original.TaskID {
		t.Fatalf("normal queued task ran before eligible verification: %+v", verification)
	}
}

func TestSingleWorkerVerificationSkipsAndLateResultIsIgnored(t *testing.T) {
	now := startTime()
	s := verificationScheduler(1, 1, byteVerifier{})
	a, _ := s.Hello("A", WorkerInfo{}, now)
	s.Start(now)
	task := s.Pull(a, now)
	completeOriginal(t, s, a, task.TaskID, now, []byte(`{"v":1}`))
	if got := s.Pull(a, now); got.Status != StatusWait {
		t.Fatalf("single worker pull with pending verification = %+v", got)
	}
	if snapshot := s.Snapshot(now); snapshot.State == JobStateDone || snapshot.Verify.Pending != 1 {
		t.Fatalf("job finished with pending verification: %+v", snapshot)
	}
	s.Tick(now.Add(5 * time.Second))
	snapshot := s.Snapshot(now.Add(5 * time.Second))
	if snapshot.Verify.Skipped != 1 || snapshot.Verify.Pending != 0 || snapshot.State != JobStateDone {
		t.Fatalf("verification did not skip at timeout: %+v", snapshot)
	}
	b, _ := s.Hello("B", WorkerInfo{}, now.Add(time.Second))
	late := s.Result(b, task.TaskID, 2, []byte(`{"v":1}`), 0.1, now.Add(6*time.Second))
	if late.Accepted || late.Reason != ReasonStale {
		t.Fatalf("late verification result = %+v", late)
	}
}

func TestSecondWorkerJoinsBeforeVerificationTimeout(t *testing.T) {
	now := startTime()
	s := verificationScheduler(1, 1, byteVerifier{match: true})
	a, _ := s.Hello("A", WorkerInfo{}, now)
	s.Start(now)
	task := s.Pull(a, now)
	completeOriginal(t, s, a, task.TaskID, now, []byte(`same`))
	b, rejoined := s.Hello("B", WorkerInfo{}, now.Add(time.Second))
	if rejoined {
		t.Fatal("new second worker reported rejoined")
	}
	verification := s.Pull(b, now.Add(time.Second))
	if verification.Status != StatusTask || verification.TaskID != task.TaskID {
		t.Fatalf("second worker did not receive queued verification: %+v", verification)
	}
	if out := s.Result(b, verification.TaskID, verification.Attempt, []byte(`same`), 0.1, now.Add(time.Second)); !out.Accepted {
		t.Fatalf("verification result = %+v", out)
	}
}

func TestVerificationLeaseHeartbeatOnlyExtendsListedTask(t *testing.T) {
	now := startTime()
	s := verificationScheduler(1, 1, byteVerifier{match: true})
	a, _ := s.Hello("A", WorkerInfo{}, now)
	b, _ := s.Hello("B", WorkerInfo{}, now)
	s.Start(now)
	task := s.Pull(a, now)
	completeOriginal(t, s, a, task.TaskID, now, []byte(`same`))
	verification := s.Pull(b, now)
	if ok, unknown := s.Heartbeat(b, nil, now.Add(time.Second)); !ok || unknown {
		t.Fatalf("heartbeat = (%v,%v)", ok, unknown)
	}
	lease := s.tasks[task.TaskID].verifyDeadline
	if ok, _ := s.Heartbeat(b, []string{task.TaskID}, now.Add(time.Second)); !ok {
		t.Fatal("listed heartbeat failed")
	}
	if s.tasks[task.TaskID].verifyDeadline.Equal(lease) {
		t.Fatal("listed heartbeat did not extend verification lease")
	}
	s.Tick(now.Add(2500 * time.Millisecond))
	if got := s.Snapshot(now.Add(2500 * time.Millisecond)).Verify.Pending; got != 1 {
		t.Fatalf("unexpired verification lease pending=%d", got)
	}
	if out := s.Result(b, task.TaskID, verification.Attempt, []byte(`same`), 0.1, now.Add(2500*time.Millisecond)); !out.Accepted {
		t.Fatalf("verification result after lease extension = %+v", out)
	}
}

func TestLostVerificationLeaseReturnsToQueue(t *testing.T) {
	now := startTime()
	s := New(Config{LeaseDur: 2 * time.Second, HeartbeatTimeout: 20 * time.Second, VerifyRate: 1, VerifySeed: 1, VerifyTimeout: time.Minute, Verifier: byteVerifier{match: true}}, makeTasks(1), newReducer())
	a, _ := s.Hello("A", WorkerInfo{}, now)
	b, _ := s.Hello("B", WorkerInfo{}, now)
	c, _ := s.Hello("C", WorkerInfo{}, now)
	s.Start(now)
	task := s.Pull(a, now)
	completeOriginal(t, s, a, task.TaskID, now, []byte(`same`))
	firstVerification := s.Pull(b, now)
	s.Tick(now.Add(2 * time.Second))
	if got := s.workers[b].status; got != WorkerStatusLost {
		t.Fatalf("verification worker status=%q, want lost", got)
	}
	retry := s.Pull(c, now.Add(2*time.Second))
	if retry.Status != StatusTask || retry.TaskID != task.TaskID || retry.Attempt <= firstVerification.Attempt {
		t.Fatalf("lost verification was not requeued with a fresh attempt: first=%+v retry=%+v", firstVerification, retry)
	}
	if out := s.Result(c, task.TaskID, retry.Attempt, []byte(`same`), 0.1, now.Add(2*time.Second)); !out.Accepted {
		t.Fatalf("retried verification result = %+v", out)
	}
}
