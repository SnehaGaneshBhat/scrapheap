package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

type Config struct {
	Scheduler         *sched.Scheduler
	Results           ResultSnapshot
	JobID             string
	JobKind           string
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	WebDir            string
	DatasetDir        string
	Logger            *log.Logger
	Now               func() time.Time
}

type ResultSnapshot interface {
	Snapshot() ([]proto.LeaderboardEntry, proto.BestResult)
}

type Hub struct {
	mu      sync.Mutex
	cfg     Config
	dataset []proto.DatasetFile
	handler http.Handler
}

func New(cfg Config) (*Hub, error) {
	if cfg.Scheduler == nil {
		return nil, errors.New("hub: scheduler is required")
	}
	if cfg.JobID == "" {
		cfg.JobID = "scrapheap"
	}
	if cfg.JobKind == "" {
		cfg.JobKind = proto.TaskKindBurn
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 30 * time.Second
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	h := &Hub{cfg: cfg}
	files, err := datasetManifest(cfg.DatasetDir)
	if err != nil {
		return nil, err
	}
	h.dataset = files
	mux := http.NewServeMux()
	mux.HandleFunc("POST /w/hello", h.hello)
	mux.HandleFunc("POST /w/pull", h.pull)
	mux.HandleFunc("POST /w/result", h.result)
	mux.HandleFunc("POST /w/heartbeat", h.heartbeat)
	mux.HandleFunc("GET /api/job", h.job)
	mux.HandleFunc("GET /api/result", h.resultAPI)
	mux.HandleFunc("POST /api/job/start", h.start)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /dataset/", h.datasetFile)
	mux.HandleFunc("GET /trainer.wasm", h.staticFile("trainer.wasm"))
	mux.HandleFunc("GET /wasm_exec.js", h.staticFile("wasm_exec.js"))
	mux.HandleFunc("GET /worker", h.staticFile(filepath.Join("worker", "index.html")))
	mux.HandleFunc("GET /worker/", h.workerFile)
	mux.HandleFunc("GET /dashboard/", h.dashboardFile)
	mux.HandleFunc("GET /", h.dashboard)
	h.handler = mux
	return h, nil
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.handler.ServeHTTP(w, r) }

func (h *Hub) Tick(now time.Time) {
	h.mu.Lock()
	h.cfg.Scheduler.Tick(now)
	h.logEvents(now)
	h.mu.Unlock()
}

func (h *Hub) hello(w http.ResponseWriter, r *http.Request) {
	var req proto.HelloRequest
	if !h.decode(w, r, &req) {
		return
	}
	if req.Name == "" || req.Cores < 1 || req.Threads < 1 || req.Browser == "" {
		writeError(w, http.StatusBadRequest, "name, positive cores and threads, and browser are required")
		return
	}
	now := h.cfg.Now()
	h.mu.Lock()
	id, rejoined := h.cfg.Scheduler.Hello(req.Name, sched.WorkerInfo{Browser: req.Browser, Cores: req.Cores, Threads: req.Threads}, now)
	h.logEvents(now)
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, proto.HelloResponse{WorkerID: id, LeaseSec: int(h.cfg.LeaseDuration.Seconds()), HeartbeatSec: int(h.cfg.HeartbeatInterval.Seconds()), JobKind: h.cfg.JobKind, Rejoined: rejoined, Dataset: h.dataset})
}

func (h *Hub) pull(w http.ResponseWriter, r *http.Request) {
	var req proto.PullRequest
	if !h.decode(w, r, &req) {
		return
	}
	now := h.cfg.Now()
	h.mu.Lock()
	a := h.cfg.Scheduler.Pull(req.WorkerID, now)
	h.logEvents(now)
	h.mu.Unlock()
	switch a.Status {
	case sched.StatusTask:
		writeJSON(w, http.StatusOK, proto.TaskResponse{Status: a.Status, TaskID: a.TaskID, Kind: a.Kind, Payload: a.Payload, LeaseDeadlineUnixMs: a.Deadline.UnixMilli(), Attempt: a.Attempt})
	case sched.StatusDone:
		writeJSON(w, http.StatusOK, proto.DoneResponse{Status: a.Status})
	default:
		writeJSON(w, http.StatusOK, proto.WaitResponse{Status: sched.StatusWait, RetryAfterMs: 1000})
	}
}

func (h *Hub) result(w http.ResponseWriter, r *http.Request) {
	var req proto.ResultRequest
	if !h.decode(w, r, &req) {
		return
	}
	if len(req.Result) == 0 || !json.Valid(req.Result) || req.Seconds < 0 {
		writeError(w, http.StatusBadRequest, "result must be valid JSON and seconds must be non-negative")
		return
	}
	now := h.cfg.Now()
	h.mu.Lock()
	out := h.cfg.Scheduler.Result(req.WorkerID, req.TaskID, req.Attempt, req.Result, req.Seconds, now)
	h.logEvents(now)
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, proto.ResultResponse{Accepted: out.Accepted, Reason: out.Reason})
}

func (h *Hub) heartbeat(w http.ResponseWriter, r *http.Request) {
	var req proto.HeartbeatRequest
	if !h.decode(w, r, &req) {
		return
	}
	now := h.cfg.Now()
	h.mu.Lock()
	ok, unknown := h.cfg.Scheduler.Heartbeat(req.WorkerID, req.Running, now)
	h.logEvents(now)
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, proto.HeartbeatResponse{OK: ok, UnknownWorker: unknown})
}

func (h *Hub) start(w http.ResponseWriter, _ *http.Request) {
	h.mu.Lock()
	h.cfg.Scheduler.Start(h.cfg.Now())
	h.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) job(w http.ResponseWriter, _ *http.Request) {
	now := h.cfg.Now()
	h.mu.Lock()
	s := h.cfg.Scheduler.Snapshot(now)
	h.mu.Unlock()
	workers := make([]proto.WorkerInfo, 0, len(s.Workers))
	for _, item := range s.Workers {
		workers = append(workers, proto.WorkerInfo{ID: item.Name, Browser: item.Browser, Cores: item.Cores, Threads: item.Threads, TasksDone: item.TasksDone, Speed: item.Speed, Status: item.Status})
	}
	leaderboard := []proto.LeaderboardEntry{}
	var best *proto.BestResult
	if h.cfg.Results != nil {
		leaderboard, result := h.cfg.Results.Snapshot()
		if len(leaderboard) > 0 {
			best = &result
		}
	}
	// Estimate single-laptop time: total work seconds ÷ best thread count seen.
	// This represents "one laptop running all tasks with its full thread budget".
	baseline := proto.BaselineInfo{Label: "1 laptop (estimated)"}
	if s.TotalTaskSec > 0 && s.MaxThreads > 0 {
		baseline.Seconds = s.TotalTaskSec / float64(s.MaxThreads)
		baseline.Label = fmt.Sprintf("1 laptop · %d threads (estimated)", s.MaxThreads)
	}
	writeJSON(w, http.StatusOK, proto.JobSnapshot{JobID: h.cfg.JobID, State: s.State, Total: s.Total, Done: s.Done, InFlight: s.InFlight, Requeued: s.Requeued, ElapsedSec: s.Elapsed.Seconds(), ETASec: s.ETA.Seconds(), Workers: workers, Leaderboard: leaderboard, Verify: proto.VerifyStats{Sampled: s.Verify.Sampled, Matched: s.Verify.Matched, Mismatched: s.Verify.Mismatched}, Baseline: baseline, Best: best})
}

func (h *Hub) resultAPI(w http.ResponseWriter, _ *http.Request) {
	h.mu.Lock()
	done := h.cfg.Scheduler.Snapshot(h.cfg.Now()).State == sched.JobStateDone
	h.mu.Unlock()
	if done {
		writeError(w, http.StatusNotImplemented, "final result schema is not defined yet")
		return
	}
	writeJSON(w, http.StatusOK, proto.APIResultResponse{Ready: false})
}

func (h *Hub) datasetFile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/dataset/")
	if h.cfg.DatasetDir == "" || name == "" || filepath.Base(name) != name || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(h.cfg.DatasetDir, name))
}

func (h *Hub) staticFile(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.WebDir == "" {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(h.cfg.WebDir, name)
		http.ServeFile(w, r, path)
	}
}

func (h *Hub) workerFile(w http.ResponseWriter, r *http.Request) {
	if h.cfg.WebDir == "" {
		http.NotFound(w, r)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/worker/")
	if relative == "" {
		relative = "index.html"
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(h.cfg.WebDir, "worker", clean))
}

func (h *Hub) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	h.staticFile(filepath.Join("dashboard", "index.html"))(w, r)
}

func (h *Hub) dashboardFile(w http.ResponseWriter, r *http.Request) {
	if h.cfg.WebDir == "" {
		http.NotFound(w, r)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/dashboard/")
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(h.cfg.WebDir, "dashboard", clean))
}

func (h *Hub) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, "request must contain one JSON value")
		return false
	}
	return true
}

func (h *Hub) logEvents(now time.Time) {
	for _, event := range h.cfg.Scheduler.DrainEvents(now) {
		h.cfg.Logger.Printf("%s worker=%q task=%q event=%s", now.Format(time.RFC3339Nano), event.WorkerName, event.TaskID, event.Kind)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, proto.ErrorResponse{Error: message})
}

func datasetManifest(dir string) ([]proto.DatasetFile, error) {
	if dir == "" {
		return []proto.DatasetFile{}, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []proto.DatasetFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]proto.DatasetFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		files = append(files, proto.DatasetFile{File: entry.Name(), SHA256: hex.EncodeToString(hash.Sum(nil)), Size: info.Size()})
	}
	return files, nil
}
