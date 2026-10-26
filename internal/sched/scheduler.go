package sched

import (
	"bytes"
	"hash/fnv"
	"sort"
	"time"
)

const (
	StatusTask = "task"
	StatusWait = "wait"
	StatusDone = "done"

	WorkerStatusActive   = "active"
	WorkerStatusLost     = "lost"
	WorkerStatusRejoined = "rejoined"

	JobStateWaiting = "waiting"
	JobStateRunning = "running"
	JobStateDone    = "done"

	ReasonOK        = "ok"
	ReasonDuplicate = "duplicate"
	ReasonStale     = "stale"
	ReasonUnknown   = "unknown"
)

type TaskSpec struct {
	ID      string
	Kind    string
	Payload []byte
	Cost    float64
}

type JobSource interface {
	Tasks() []TaskSpec
}

type Reducer interface {
	Accept(spec TaskSpec, result []byte, workerID string)
}

type SampleVerifier interface {
	Matches(spec TaskSpec, first, second []byte) bool
}

type SampleVerifierFunc func(spec TaskSpec, first, second []byte) bool

func (f SampleVerifierFunc) Matches(spec TaskSpec, first, second []byte) bool {
	return f(spec, first, second)
}

type Config struct {
	LeaseDur         time.Duration
	HeartbeatTimeout time.Duration
	VerifyRate       float64
	Verifier         SampleVerifier
}

type WorkerInfo struct {
	Browser string
	Cores   int
	Threads int
}

type Assignment struct {
	Status   string
	TaskID   string
	Kind     string
	Payload  []byte
	Deadline time.Time
	Attempt  int
}

type Outcome struct {
	Accepted bool
	Reason   string
}

type Event struct {
	Kind       string
	WorkerName string
	TaskID     string
}

type WorkerSnapshot struct {
	ID        string
	Name      string
	Browser   string
	Cores     int
	Threads   int
	TasksDone int
	Status    string
	Speed     float64
}

type Snapshot struct {
	State    string
	Total    int
	Done     int
	InFlight int
	Requeued int
	Elapsed  time.Duration
	ETA      time.Duration
	Workers  []WorkerSnapshot
	Verify   VerificationSnapshot
}

type VerificationSnapshot struct {
	Sampled    int
	Matched    int
	Mismatched int
}

type Scheduler struct {
	cfg           Config
	reducer       Reducer
	state         string
	startedAt     time.Time
	tasks         map[string]*taskState
	taskOrder     []string
	queue         []string
	workers       map[string]*workerState
	workerOrder   []string
	workerIDs     map[string]string
	nextID        int
	done          int
	requeued      int
	events        []Event
	verify        VerificationSnapshot
	verifyPending int
}

type taskState struct {
	spec          TaskSpec
	status        string
	workerID      string
	deadline      time.Time
	attempt       int
	inQueue       bool
	firstResult   []byte
	firstWorkerID string
}

type workerState struct {
	id               string
	name             string
	info             WorkerInfo
	status           string
	lastHeartbeat    time.Time
	tasksDone        int
	completedSeconds float64
}

// New initializes a scheduler with a stable FIFO task queue.
func New(cfg Config, source JobSource, reducer Reducer) *Scheduler {
	s := &Scheduler{
		cfg:       cfg,
		reducer:   reducer,
		state:     JobStateWaiting,
		tasks:     make(map[string]*taskState),
		workers:   make(map[string]*workerState),
		workerIDs: make(map[string]string),
	}
	if source != nil {
		specs := source.Tasks()
		unique := make([]TaskSpec, 0, len(specs))
		seen := make(map[string]struct{}, len(specs))
		for _, spec := range specs {
			if _, exists := seen[spec.ID]; exists {
				continue
			}
			seen[spec.ID] = struct{}{}
			unique = append(unique, spec)
		}
		sort.SliceStable(unique, func(i, j int) bool { return unique[i].Cost > unique[j].Cost })
		for _, spec := range unique {
			if _, exists := s.tasks[spec.ID]; exists {
				continue
			}
			spec.Payload = bytes.Clone(spec.Payload)
			s.tasks[spec.ID] = &taskState{spec: spec, status: "queued", inQueue: true}
			s.taskOrder = append(s.taskOrder, spec.ID)
			s.queue = append(s.queue, spec.ID)
		}
	}
	return s
}

// Hello registers a worker or reconnects a worker with the same name.
func (s *Scheduler) Hello(name string, info WorkerInfo, now time.Time) (workerID string, rejoined bool) {
	if id, ok := s.workerIDs[name]; ok {
		w := s.workers[id]
		w.info = info
		rejoined = w.status == WorkerStatusLost
		if rejoined {
			w.status = WorkerStatusRejoined
		}
		w.lastHeartbeat = now
		return id, rejoined
	}
	s.nextID++
	workerID = "worker-" + itoa(s.nextID)
	s.workers[workerID] = &workerState{
		id:            workerID,
		name:          name,
		info:          info,
		status:        WorkerStatusActive,
		lastHeartbeat: now,
	}
	s.workerOrder = append(s.workerOrder, workerID)
	s.workerIDs[name] = workerID
	return workerID, false
}

// Start transitions the job from waiting to running. Repeated calls are harmless.
func (s *Scheduler) Start(now time.Time) {
	if s.state == JobStateWaiting {
		s.state = JobStateRunning
		s.startedAt = now
	}
}

// Pull leases the next queued task, or returns wait/done according to job state.
func (s *Scheduler) Pull(workerID string, now time.Time) Assignment {
	w, ok := s.workers[workerID]
	if !ok || w.status == WorkerStatusLost || s.state != JobStateRunning {
		return Assignment{Status: StatusWait}
	}
	w.lastHeartbeat = now
	w.status = WorkerStatusActive
	queueLen := len(s.queue)
	for scanned := 0; scanned < queueLen; scanned++ {
		id := s.queue[0]
		s.queue = s.queue[1:]
		t := s.tasks[id]
		if t == nil || !t.inQueue || (t.status != "queued" && t.status != "verify-queued") {
			continue
		}
		verifying := t.status == "verify-queued"
		if verifying && t.firstWorkerID == workerID {
			s.queue = append(s.queue, id)
			continue
		}
		t.inQueue = false
		if verifying {
			t.status = "verify-leased"
		} else {
			t.status = "leased"
		}
		t.workerID = workerID
		if t.attempt == 0 {
			t.attempt = 1
		}
		t.deadline = now.Add(s.cfg.LeaseDur)
		s.events = append(s.events, Event{Kind: "assigned", WorkerName: w.name, TaskID: id})
		return Assignment{
			Status:   StatusTask,
			TaskID:   id,
			Kind:     t.spec.Kind,
			Payload:  bytes.Clone(t.spec.Payload),
			Deadline: t.deadline,
			Attempt:  t.attempt,
		}
	}
	if s.done == len(s.tasks) && s.verifyPending == 0 {
		return Assignment{Status: StatusDone}
	}
	return Assignment{Status: StatusWait}
}

// Result accepts a current task attempt and, when sampled, a verification run
// from a worker other than the worker that supplied the first result.
func (s *Scheduler) Result(workerID, taskID string, attempt int, result []byte, seconds float64, now time.Time) Outcome {
	w, workerOK := s.workers[workerID]
	if !workerOK {
		return Outcome{Reason: ReasonUnknown}
	}
	t, taskOK := s.tasks[taskID]
	if !taskOK {
		return Outcome{Reason: ReasonUnknown}
	}
	if t.status == "done" {
		s.events = append(s.events, Event{Kind: "duplicate", WorkerName: w.name, TaskID: taskID})
		return Outcome{Reason: ReasonDuplicate}
	}
	if attempt != t.attempt || (t.status != "leased" && t.status != "verify-leased") {
		return Outcome{Reason: ReasonStale}
	}
	if t.status == "verify-leased" {
		if workerID == t.firstWorkerID {
			return Outcome{Reason: ReasonStale}
		}
		if s.cfg.Verifier.Matches(t.spec, t.firstResult, result) {
			s.verify.Matched++
		} else {
			s.verify.Mismatched++
		}
		s.verifyPending--
		t.status = "done"
		t.workerID = ""
		t.inQueue = false
		w.tasksDone++
		w.completedSeconds += seconds
		s.events = append(s.events, Event{Kind: "verified", WorkerName: w.name, TaskID: taskID})
		return Outcome{Accepted: true, Reason: ReasonOK}
	}
	s.done++
	w.tasksDone++
	w.completedSeconds += seconds
	if s.reducer != nil {
		s.reducer.Accept(t.spec, bytes.Clone(result), workerID)
	}
	s.events = append(s.events, Event{Kind: "completed", WorkerName: w.name, TaskID: taskID})
	if s.shouldVerify(taskID) {
		t.firstResult = bytes.Clone(result)
		t.firstWorkerID = workerID
		t.status = "verify-queued"
		t.workerID = ""
		t.attempt++
		t.inQueue = true
		s.queue = append(s.queue, taskID)
		s.verify.Sampled++
		s.verifyPending++
		return Outcome{Accepted: true, Reason: ReasonOK}
	}
	t.status = "done"
	t.workerID = ""
	t.inQueue = false
	_ = now
	return Outcome{Accepted: true, Reason: ReasonOK}
}

func (s *Scheduler) shouldVerify(taskID string) bool {
	if s.cfg.Verifier == nil || s.cfg.VerifyRate <= 0 {
		return false
	}
	if s.cfg.VerifyRate >= 1 {
		return true
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(taskID))
	return hash.Sum64()%1_000_000 < uint64(s.cfg.VerifyRate*1_000_000)
}

// Heartbeat refreshes the worker's liveness and all task leases it currently holds.
func (s *Scheduler) Heartbeat(workerID string, running []string, now time.Time) (ok, unknownWorker bool) {
	w, exists := s.workers[workerID]
	if !exists {
		return false, true
	}
	if w.status == WorkerStatusLost {
		w.status = WorkerStatusRejoined
	}
	w.lastHeartbeat = now
	for _, id := range running {
		t := s.tasks[id]
		if t != nil && (t.status == "leased" || t.status == "verify-leased") && t.workerID == workerID {
			t.deadline = now.Add(s.cfg.LeaseDur)
		}
	}
	return true, false
}

// Tick expires workers and leases using only the supplied clock value.
func (s *Scheduler) Tick(now time.Time) {
	for _, workerID := range s.workerOrder {
		w := s.workers[workerID]
		if w.status == WorkerStatusLost {
			continue
		}
		if s.cfg.HeartbeatTimeout > 0 && now.Sub(w.lastHeartbeat) >= s.cfg.HeartbeatTimeout {
			s.loseWorker(w)
			continue
		}
		for _, taskID := range s.taskOrder {
			t := s.tasks[taskID]
			if (t.status == "leased" || t.status == "verify-leased") && t.workerID == w.id && !now.Before(t.deadline) {
				s.requeueTask(w, taskID)
			}
		}
	}
}

func (s *Scheduler) loseWorker(w *workerState) {
	if w.status == WorkerStatusLost {
		return
	}
	w.status = WorkerStatusLost
	lostAny := false
	for _, id := range s.taskOrder {
		t := s.tasks[id]
		if (t.status != "leased" && t.status != "verify-leased") || t.workerID != w.id {
			continue
		}
		s.requeueTask(w, id)
		lostAny = true
	}
	if !lostAny {
		s.events = append(s.events, Event{Kind: "lost", WorkerName: w.name})
	}
}

func (s *Scheduler) requeueTask(w *workerState, id string) {
	t := s.tasks[id]
	if t.status == "verify-leased" {
		t.status = "verify-queued"
	} else {
		t.status = "queued"
	}
	t.workerID = ""
	t.deadline = time.Time{}
	t.attempt++
	t.inQueue = true
	s.queue = append(s.queue, id)
	s.requeued++
	s.events = append(s.events,
		Event{Kind: "lost", WorkerName: w.name, TaskID: id},
		Event{Kind: "requeued", WorkerName: w.name, TaskID: id},
	)
}

// Snapshot returns scheduler counts and worker throughput at now.
func (s *Scheduler) Snapshot(now time.Time) Snapshot {
	out := Snapshot{
		State:    s.state,
		Total:    len(s.tasks),
		Done:     s.done,
		Requeued: s.requeued,
		Workers:  make([]WorkerSnapshot, 0, len(s.workers)),
	}
	if s.state != JobStateWaiting {
		out.Elapsed = now.Sub(s.startedAt)
		if out.Elapsed < 0 {
			out.Elapsed = 0
		}
	}
	for _, t := range s.tasks {
		if t.status == "leased" || t.status == "verify-leased" {
			out.InFlight++
		}
	}
	out.Verify = s.verify
	active := 0
	var poolSpeed float64
	for _, workerID := range s.workerOrder {
		w := s.workers[workerID]
		speed := 0.0
		if w.tasksDone > 0 && w.completedSeconds > 0 && w.info.Threads > 0 {
			speed = float64(w.tasksDone*w.info.Threads) / w.completedSeconds
		}
		if w.status == WorkerStatusActive || w.status == WorkerStatusRejoined {
			active++
			poolSpeed += speed
		}
		out.Workers = append(out.Workers, WorkerSnapshot{
			ID: w.id, Name: w.name, Browser: w.info.Browser, Cores: w.info.Cores,
			Threads: w.info.Threads, TasksDone: w.tasksDone, Status: w.status, Speed: speed,
		})
	}
	remaining := s.tasksLeft() + s.verifyPending
	if active > 0 && poolSpeed > 0 && remaining > 0 {
		out.ETA = time.Duration(float64(remaining) / poolSpeed * float64(time.Second))
	}
	if s.done == len(s.tasks) && s.verifyPending == 0 {
		out.State = JobStateDone
	}
	return out
}

// DrainEvents returns and clears events accumulated since the previous call.
func (s *Scheduler) DrainEvents(now time.Time) []Event {
	_ = now
	out := append([]Event(nil), s.events...)
	s.events = s.events[:0]
	return out
}

func (s *Scheduler) tasksLeft() int {
	return len(s.tasks) - s.done
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
