// Package hub coordinates workers and the pure scheduler. Device speed is in
// benchmark-tile equivalents per second; BENCH_RESULT.perThread is used directly.
package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/scrap-heap/backend/internal/proto"
	"github.com/scrap-heap/backend/internal/sched"
)

const outboundBuffer = 64

type Clock func() time.Time

type Verifier interface {
	Verify(proto.Task, []byte) (ok bool, mismatched int)
}
type acceptAll struct{}

func (acceptAll) Verify(proto.Task, []byte) (bool, int) { return true, 0 }

// Config controls scheduler policy, jobs, heartbeat timing, and benchmarking.
type Config struct {
	Mode              sched.Mode
	Job               JobConfig
	CostEstimator     CostEstimator
	ExpectedBenchHash string
	TickInterval      time.Duration
	HeartbeatTimeout  time.Duration
	Clock             Clock
}

// Snapshot is a point-in-time overview of the active job.
type Snapshot struct {
	JobID                uint32
	Done, Total, Devices int
}

type outbound struct {
	typ  websocket.MessageType
	data []byte
}
type client struct {
	conn        *websocket.Conn
	send        chan outbound
	close       chan struct{}
	once        sync.Once
	id          string
	registered  bool
	info        DeviceInfo
	hello       proto.Message
	lastSeen    time.Time
	pending     map[int]bool
	tasks       map[leaseKey]proto.Task
	taskThreads map[leaseKey]int
	deadlines   map[leaseKey]time.Time
}
type leaseKey struct {
	jobID, taskID uint32
	attempt       uint16
}
type command struct {
	apply func(*state) any
	reply chan any
}
type state struct {
	h         *Hub
	scheduler *sched.Scheduler
	clients   map[*client]*client
	devices   map[string]*client
	jobID     uint32
	total     int
	tasks     map[uint32]proto.Task
	store     *TileStore
	running   bool
}

// Hub owns all mutable scheduling and job state on its Run loop.
type Hub struct {
	cfg       Config
	collector Collector
	verifier  Verifier
	commands  chan command
	done      chan struct{}
	mu        sync.RWMutex
	running   bool
}

// New constructs a hub. A nil collector or verifier selects a no-op/default implementation.
func New(cfg Config, collector Collector, verifier Verifier) *Hub {
	if cfg.Job.CanvasSize == 0 {
		cfg.Job = DefaultJobConfig()
	}
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 250 * time.Millisecond
	}
	if cfg.HeartbeatTimeout <= 0 {
		cfg.HeartbeatTimeout = 5 * time.Second
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if collector == nil {
		collector = NopCollector{}
	}
	if verifier == nil {
		verifier = acceptAll{}
	}
	return &Hub{cfg: cfg, collector: collector, verifier: verifier, commands: make(chan command), done: make(chan struct{})}
}

// Run starts the sole state-owning loop and returns when ctx is canceled.
func (h *Hub) Run(ctx context.Context) {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true
	h.mu.Unlock()
	s := &state{h: h, scheduler: sched.New(h.cfg.Mode), clients: make(map[*client]*client), devices: make(map[string]*client), tasks: make(map[uint32]proto.Task), store: NewTileStore()}
	ticker := time.NewTicker(h.cfg.TickInterval)
	defer ticker.Stop()
	defer close(h.done)
	for {
		select {
		case cmd := <-h.commands:
			if cmd.apply != nil {
				v := cmd.apply(s)
				if cmd.reply != nil {
					cmd.reply <- v
				}
			}
		case <-ticker.C:
			s.tick(h.cfg.Clock())
		case <-ctx.Done():
			s.shutdown()
			return
		}
	}
}

func (h *Hub) call(apply func(*state) any) any {
	reply := make(chan any, 1)
	cmd := command{apply: apply, reply: reply}
	select {
	case h.commands <- cmd:
	case <-h.done:
		return nil
	}
	select {
	case v := <-reply:
		return v
	case <-h.done:
		return nil
	}
}

// StartNewJob resets active leases, starts the next job, broadcasts JOB_STARTED, and returns its id.
func (h *Hub) StartNewJob() uint32 {
	v := h.call(func(s *state) any { return s.startJob(true) })
	if id, ok := v.(uint32); ok {
		return id
	}
	return 0
}

// Snapshot returns job progress and registered device count.
func (h *Hub) Snapshot() Snapshot {
	v := h.call(func(s *state) any {
		return Snapshot{JobID: s.jobID, Done: s.store.Count(), Total: s.total, Devices: len(s.devices)}
	})
	if snap, ok := v.(Snapshot); ok {
		return snap
	}
	return Snapshot{}
}

// Handler returns the /ws/worker-compatible WebSocket handler.
func (h *Hub) Handler() http.Handler { return http.HandlerFunc(h.serveWorker) }

func (h *Hub) serveWorker(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	conn.SetReadLimit(int64(proto.ResultHeaderSize + proto.MaxTileDim*proto.MaxTileDim*4 + 256))
	c := &client{conn: conn, send: make(chan outbound, outboundBuffer), close: make(chan struct{}), pending: make(map[int]bool), tasks: make(map[leaseKey]proto.Task), taskThreads: make(map[leaseKey]int), deadlines: make(map[leaseKey]time.Time)}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go h.writer(ctx, c)
	h.reader(ctx, c)
	c.stop(websocket.StatusNormalClosure, "connection ended")
	_ = h.post(func(s *state) { s.removeClient(c) })
}

func (c *client) stop(code websocket.StatusCode, reason string) {
	c.once.Do(func() {
		close(c.close)
		// Close performs a bounded but potentially ten-second handshake; never
		// let that block the state-owning hub loop.
		go func() { _ = c.conn.Close(code, reason) }()
	})
}

func (h *Hub) writer(ctx context.Context, c *client) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.close:
			return
		case out := <-c.send:
			if err := c.conn.Write(ctx, out.typ, out.data); err != nil {
				c.stop(websocket.StatusInternalError, "write failed")
				return
			}
		}
	}
}

func (h *Hub) reader(ctx context.Context, c *client) {
	firstCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	typ, data, err := c.conn.Read(firstCtx)
	cancel()
	if err != nil || typ != websocket.MessageText {
		c.stop(websocket.StatusPolicyViolation, "HELLO required")
		return
	}
	msg, err := proto.DecodeJSON(data)
	if err != nil || msg.Type != proto.MessageHello {
		c.stop(websocket.StatusPolicyViolation, "HELLO required")
		return
	}
	first := msg
	if !h.post(func(s *state) { s.hello(c, first) }) {
		return
	}
	for {
		typ, data, err = c.conn.Read(ctx)
		if err != nil {
			return
		}
		now := h.cfg.Clock()
		if typ == websocket.MessageText {
			msg, err = proto.DecodeJSON(data)
			if err != nil {
				c.stop(websocket.StatusPolicyViolation, "invalid JSON")
				return
			}
			current := msg
			eventTime := now
			if !h.post(func(s *state) { s.message(c, current, eventTime) }) {
				return
			}
		} else if typ == websocket.MessageBinary {
			frame, err := proto.DecodeResult(data)
			if err != nil {
				c.stop(websocket.StatusPolicyViolation, "invalid RESULT")
				return
			}
			current := frame
			eventTime := now
			if !h.post(func(s *state) { s.result(c, current, eventTime) }) {
				return
			}
		} else {
			c.stop(websocket.StatusPolicyViolation, "unsupported frame")
			return
		}
	}
}

func (h *Hub) post(fn func(*state)) bool {
	select {
	case h.commands <- command{apply: func(s *state) any { fn(s); return nil }}:
		return true
	case <-h.done:
		return false
	}
}

func (s *state) send(c *client, typ websocket.MessageType, data []byte) bool {
	select {
	case c.send <- outbound{typ: typ, data: append([]byte(nil), data...)}:
		return true
	default:
		c.stop(websocket.StatusPolicyViolation, "send queue full")
		s.removeClient(c)
		return false
	}
}

func (s *state) json(c *client, m proto.Message) bool {
	b, err := proto.EncodeJSON(m)
	if err != nil {
		return false
	}
	return s.send(c, websocket.MessageText, b)
}

func (s *state) hello(c *client, m proto.Message) {
	if m.Type != proto.MessageHello || m.DeviceID == "" {
		c.stop(websocket.StatusPolicyViolation, "invalid HELLO")
		return
	}
	c.id = m.DeviceID
	c.hello = m
	c.lastSeen = s.h.cfg.Clock()
	s.clients[c] = c
	if err := s.json(c, proto.Message{Type: proto.MessageWelcome, HeartbeatIntervalMs: 1000, HeartbeatTimeoutMs: uint32(s.h.cfg.HeartbeatTimeout / time.Millisecond)}); !err {
		return
	}
	if s.running {
		_ = s.json(c, proto.Message{Type: proto.MessageJobStarted, JobID: s.jobID})
	}
}

func (s *state) message(c *client, m proto.Message, now time.Time) {
	c.lastSeen = now
	switch m.Type {
	case proto.MessageBenchResult:
		if c.registered {
			return
		}
		if m.Threads != c.hello.Threads {
			c.stop(websocket.StatusPolicyViolation, "benchmark thread count mismatch")
			s.removeClient(c)
			return
		}
		if s.h.cfg.ExpectedBenchHash != "" && !c.hello.Mock && !equalHash(m.Hash, s.h.cfg.ExpectedBenchHash) {
			c.stop(websocket.StatusPolicyViolation, "benchmark hash mismatch")
			s.removeClient(c)
			return
		}
		c.info = DeviceInfo{UserAgent: c.hello.UserAgent, Cores: c.hello.Cores, Threads: m.Threads, Mock: c.hello.Mock, TilesPerSec: m.TilesPerSec, PerThread: m.PerThread}
		if s.h.cfg.ExpectedBenchHash == "" {
			log.Printf("hub: benchmark device=%s hash=%s mock=%t", c.id, m.Hash, c.info.Mock)
		}
		c.registered = true
		s.devices[c.id] = c
		s.h.collector.DeviceJoined(now, c.id, c.info)
		s.apply(s.scheduler.Handle(sched.DeviceJoined{Now: now, DeviceID: c.id, Threads: c.info.Threads, Speed: m.PerThread}))
	case proto.MessageRequestTask:
		if !c.registered {
			return
		}
		tid := -1
		for i := 0; i < c.info.Threads; i++ {
			if !c.pending[i] && !clientThreadBusy(c, i) {
				tid = i
				break
			}
		}
		if tid < 0 {
			return
		}
		c.pending[tid] = true
		s.apply(s.scheduler.Handle(sched.TaskRequested{Now: now, DeviceID: c.id, ThreadID: tid}))
	case proto.MessageHeartbeat:
	case proto.MessageAck, proto.MessageJobDone, proto.MessageHello:
		// These are not valid worker-originated messages after the handshake.
		if m.Type == proto.MessageHello {
			c.stop(websocket.StatusPolicyViolation, "duplicate HELLO")
		}
	default:
	}
}

func (s *state) result(c *client, r proto.Result, now time.Time) {
	if r.JobID != s.jobID {
		s.h.collector.DupDropped(now, r.TaskID, r.Attempt, true)
		return
	}
	key := leaseKey{r.JobID, r.TaskID, r.Attempt}
	task, ok := c.tasks[key]
	if !ok {
		s.h.collector.DupDropped(now, r.TaskID, r.Attempt, false)
		return
	}
	if r.W != task.W || r.H != task.H {
		log.Printf("hub: rejected wrong-size result device=%s task=%d", c.id, r.TaskID)
		return
	}
	if valid, mismatched := s.h.verifier.Verify(task, r.RGBA); !valid {
		log.Printf("hub: verifier rejected task=%d mismatched=%d", r.TaskID, mismatched)
		return
	}
	s.apply(s.scheduler.Handle(sched.ResultReceived{Now: now, DeviceID: c.id, JobID: r.JobID, TaskID: r.TaskID, Attempt: r.Attempt, ComputeMs: r.ComputeMs}), c, r)
}

func (s *state) apply(actions []sched.Action, accepted ...any) {
	var source *client
	var result proto.Result
	if len(accepted) > 0 {
		source, _ = accepted[0].(*client)
	}
	if len(accepted) > 1 {
		result, _ = accepted[1].(proto.Result)
	}
	for _, a := range actions {
		switch v := a.(type) {
		case sched.AssignTask:
			c := s.devices[v.DeviceID]
			if c == nil {
				continue
			}
			delete(c.pending, v.ThreadID)
			frame, err := proto.EncodeTask(v.Task)
			if err != nil {
				continue
			}
			key := leaseKey{v.Task.JobID, v.Task.TaskID, v.Attempt}
			c.tasks[key] = v.Task
			c.taskThreads[key] = v.ThreadID
			c.deadlines[key] = v.LeaseUntil
			if s.send(c, websocket.MessageBinary, frame) {
				s.h.collector.TaskAssigned(s.h.cfg.Clock(), c.id, v.ThreadID, v.Task.TaskID, v.Attempt)
			}
		case sched.TaskAccepted:
			if source != nil {
				s.store.Put(v.TaskID, result.RGBA)
				s.h.collector.TaskAccepted(s.h.cfg.Clock(), v.DeviceID, v.TaskID, v.Attempt, v.ComputeMs)
				for c := range s.clients {
					for key := range c.tasks {
						if key.jobID == v.JobID && key.taskID == v.TaskID {
							delete(c.tasks, key)
							delete(c.taskThreads, key)
							delete(c.deadlines, key)
						}
					}
				}
				s.json(source, proto.Message{Type: proto.MessageAck, JobID: v.JobID, TaskID: v.TaskID, Attempt: v.Attempt})
			}
		case sched.Requeued:
			s.h.collector.TaskRequeued(s.h.cfg.Clock(), v.TaskID, v.Attempt, v.Reason)
			for c := range s.clients {
				for key := range c.tasks {
					if key.jobID == v.JobID && key.taskID == v.TaskID && key.attempt == v.Attempt {
						delete(c.tasks, key)
						delete(c.taskThreads, key)
						delete(c.deadlines, key)
					}
				}
			}
		case sched.DuplicateDropped:
			s.h.collector.DupDropped(s.h.cfg.Clock(), v.TaskID, v.Attempt, false)
		case sched.StaleDropped:
			s.h.collector.DupDropped(s.h.cfg.Clock(), v.TaskID, v.Attempt, true)
		case sched.DropRequests:
			for c := range s.clients {
				c.pending = make(map[int]bool)
			}
		case sched.JobDone:
			s.running = false
			hash := s.store.ImageHash()
			for c := range s.clients {
				_ = s.json(c, proto.Message{Type: proto.MessageJobDone, JobID: v.JobID})
			}
			s.h.collector.JobDone(s.h.cfg.Clock(), v.JobID, hash)
		}
	}
}

func (s *state) startJob(reset bool) uint32 {
	now := s.h.cfg.Clock()
	if reset {
		s.apply(s.scheduler.Handle(sched.Reset{Now: now}))
		for c := range s.clients {
			c.pending = make(map[int]bool)
			c.tasks = make(map[leaseKey]proto.Task)
			c.taskThreads = make(map[leaseKey]int)
			c.deadlines = make(map[leaseKey]time.Time)
		}
	}
	s.jobID++
	if s.jobID == 0 {
		s.jobID = 1
	}
	s.store.Reset()
	s.tasks = make(map[uint32]proto.Task)
	tiles := buildTiles(s.jobID, s.h.cfg.Job, s.h.cfg.CostEstimator)
	for _, t := range tiles {
		s.tasks[t.Task.TaskID] = t.Task
	}
	s.total = len(tiles)
	s.running = true
	s.h.collector.JobStart(now, s.jobID, s.total)
	s.apply(s.scheduler.Handle(sched.StartJob{Now: now, JobID: s.jobID, Tiles: tiles}))
	for c := range s.clients {
		_ = s.json(c, proto.Message{Type: proto.MessageJobStarted, JobID: s.jobID})
	}
	return s.jobID
}

func (s *state) tick(now time.Time) {
	for c := range s.clients {
		if now.Sub(c.lastSeen) > s.h.cfg.HeartbeatTimeout {
			c.stop(websocket.StatusPolicyViolation, "heartbeat timeout")
			s.removeClient(c)
		}
		for key, deadline := range c.deadlines {
			if !deadline.After(now) {
				delete(c.deadlines, key)
				delete(c.tasks, key)
				delete(c.taskThreads, key)
			}
		}
	}
	s.apply(s.scheduler.Handle(sched.Tick{Now: now}))
}

func (s *state) removeClient(c *client) {
	if _, ok := s.clients[c]; !ok {
		return
	}
	delete(s.clients, c)
	if c.registered {
		delete(s.devices, c.id)
		s.apply(s.scheduler.Handle(sched.DeviceLeft{Now: s.h.cfg.Clock(), DeviceID: c.id}))
		s.h.collector.DeviceLeft(s.h.cfg.Clock(), c.id)
	}
}

func (s *state) shutdown() {
	for c := range s.clients {
		c.stop(websocket.StatusGoingAway, "server stopping")
		s.removeClient(c)
	}
}

func clientThreadBusy(c *client, thread int) bool {
	for _, id := range c.taskThreads {
		if id == thread {
			return true
		}
	}
	return false
}
func equalHash(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	x, _ := hex.DecodeString(a)
	y, _ := hex.DecodeString(b)
	return len(x) == sha256.Size && len(y) == sha256.Size && string(x) == string(y)
}
