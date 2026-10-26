package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

type Config struct {
	JobID        string
	JobKind      string
	LeaseSec     int
	HeartbeatSec int
	DataDir      string
	WebDir       string
	Clock        func() time.Time
}

type Hub struct {
	scheduler *sched.Scheduler
	mu        sync.Mutex
	clock     func() time.Time
	config    Config
	dataset   []proto.DatasetFile
	web       http.Handler
	data      http.Handler
	logger    *log.Logger
	runOnce   sync.Once
}

type jobSnapshotResponse struct {
	JobID       string                   `json:"jobId"`
	State       string                   `json:"state"`
	Total       int                      `json:"total"`
	Done        int                      `json:"done"`
	InFlight    int                      `json:"inFlight"`
	Requeued    int                      `json:"requeued"`
	ElapsedSec  float64                  `json:"elapsedSec"`
	ETASec      float64                  `json:"etaSec"`
	Workers     []proto.WorkerInfo       `json:"workers"`
	Leaderboard []proto.LeaderboardEntry `json:"leaderboard"`
	Verify      verifySnapshotResponse   `json:"verify"`
	Baseline    proto.BaselineInfo       `json:"baseline"`
	Best        proto.BestResult         `json:"best"`
}

type verifySnapshotResponse struct {
	Sampled    int                `json:"sampled"`
	Matched    int                `json:"matched"`
	Mismatched int                `json:"mismatched"`
	Skipped    int                `json:"skipped"`
	Pending    int                `json:"pending"`
	Mismatches []mismatchResponse `json:"mismatches"`
}

type mismatchResponse struct {
	TaskID         string          `json:"taskId"`
	OriginalWorker string          `json:"originalWorker"`
	VerifierWorker string          `json:"verifierWorker"`
	Original       json.RawMessage `json:"original"`
	Rerun          json.RawMessage `json:"rerun"`
}

func New(scheduler *sched.Scheduler, config Config) *Hub {
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.JobID == "" {
		config.JobID = "scrapheap-job"
	}
	if config.JobKind == "" {
		config.JobKind = proto.TaskKindBurn
	}
	if config.LeaseSec <= 0 {
		config.LeaseSec = 6
	}
	if config.HeartbeatSec <= 0 {
		config.HeartbeatSec = 2
	}
	if config.DataDir == "" {
		config.DataDir = "./data"
	}
	if config.WebDir == "" {
		config.WebDir = "./web"
	}
	return &Hub{
		scheduler: scheduler,
		clock:     config.Clock,
		config:    config,
		dataset:   listDataset(config.DataDir),
		web:       http.FileServer(http.Dir(config.WebDir)),
		data:      http.StripPrefix("/dataset/", http.FileServer(http.Dir(config.DataDir))),
		logger:    log.New(log.Writer(), "", 0),
	}
}

func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/w/hello", requireMethod(http.MethodPost, http.HandlerFunc(h.handleHello)))
	mux.Handle("/w/pull", requireMethod(http.MethodPost, http.HandlerFunc(h.handlePull)))
	mux.Handle("/w/result", requireMethod(http.MethodPost, http.HandlerFunc(h.handleResult)))
	mux.Handle("/w/heartbeat", requireMethod(http.MethodPost, http.HandlerFunc(h.handleHeartbeat)))
	mux.Handle("/api/job", requireMethod(http.MethodGet, http.HandlerFunc(h.handleJob)))
	mux.Handle("/api/result", requireMethod(http.MethodGet, http.HandlerFunc(h.handleAPIResult)))
	mux.Handle("/api/job/start", requireMethod(http.MethodPost, http.HandlerFunc(h.handleStart)))
	mux.Handle("/healthz", requireMethod(http.MethodGet, http.HandlerFunc(h.handleHealth)))
	mux.Handle("/dataset/", requireMethod(http.MethodGet, h.data))
	mux.Handle("/", requireMethod(http.MethodGet, h.web))
	return mux
}

func requireMethod(method string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Run starts the background lease-expiration loop. It stops when ctx is canceled.
func (h *Hub) Run(ctx context.Context) {
	h.runOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					now := h.clock()
					h.mu.Lock()
					h.scheduler.Tick(now)
					events := h.scheduler.DrainEvents(now)
					h.mu.Unlock()
					h.logEvents(now, events)
				}
			}
		}()
	})
}

func (h *Hub) Start() {
	now := h.clock()
	h.mu.Lock()
	h.scheduler.Start(now)
	events := h.scheduler.DrainEvents(now)
	h.mu.Unlock()
	h.logEvents(now, events)
}

func (h *Hub) handleHello(w http.ResponseWriter, r *http.Request) {
	var req proto.HelloRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	now := h.clock()
	h.mu.Lock()
	id, rejoined := h.scheduler.Hello(req.Name, sched.WorkerInfo{CPU: req.CPU, Cores: req.Cores, Threads: req.Threads}, now)
	events := h.scheduler.DrainEvents(now)
	h.mu.Unlock()
	h.logEvents(now, events)
	writeJSON(w, http.StatusOK, proto.HelloResponse{
		WorkerID: id, LeaseSec: h.config.LeaseSec, HeartbeatSec: h.config.HeartbeatSec,
		JobKind: h.config.JobKind, Rejoined: rejoined, Dataset: append([]proto.DatasetFile{}, h.dataset...),
	})
}

func (h *Hub) handlePull(w http.ResponseWriter, r *http.Request) {
	var req proto.PullRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	now := h.clock()
	h.mu.Lock()
	assignment := h.scheduler.Pull(req.WorkerID, now)
	events := h.scheduler.DrainEvents(now)
	h.mu.Unlock()
	h.logEvents(now, events)
	switch assignment.Status {
	case sched.StatusTask:
		writeJSON(w, http.StatusOK, proto.TaskResponse{
			Status: proto.PullStatusTask, TaskID: assignment.TaskID, Kind: assignment.Kind,
			Payload: assignment.Payload, LeaseDeadlineUnixMs: assignment.Deadline.UnixMilli(), Attempt: assignment.Attempt,
		})
	case sched.StatusDone:
		writeJSON(w, http.StatusOK, proto.DoneResponse{Status: proto.PullStatusDone})
	default:
		writeJSON(w, http.StatusOK, proto.WaitResponse{Status: proto.PullStatusWait, RetryAfterMs: h.config.HeartbeatSec * 1000})
	}
}

func (h *Hub) handleResult(w http.ResponseWriter, r *http.Request) {
	var req proto.ResultRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	now := h.clock()
	h.mu.Lock()
	outcome := h.scheduler.Result(req.WorkerID, req.TaskID, req.Attempt, req.Result, req.Seconds, now)
	events := h.scheduler.DrainEvents(now)
	h.mu.Unlock()
	h.logEvents(now, events)
	writeJSON(w, http.StatusOK, proto.ResultResponse{Accepted: outcome.Accepted, Reason: outcome.Reason})
}

func (h *Hub) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req proto.HeartbeatRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	now := h.clock()
	h.mu.Lock()
	ok, unknown := h.scheduler.Heartbeat(req.WorkerID, req.Running, now)
	events := h.scheduler.DrainEvents(now)
	h.mu.Unlock()
	h.logEvents(now, events)
	writeJSON(w, http.StatusOK, proto.HeartbeatResponse{OK: ok, UnknownWorker: unknown})
}

func (h *Hub) handleJob(w http.ResponseWriter, r *http.Request) {
	now := h.clock()
	h.mu.Lock()
	snapshot := h.scheduler.Snapshot(now)
	events := h.scheduler.DrainEvents(now)
	h.mu.Unlock()
	h.logEvents(now, events)
	workers := make([]proto.WorkerInfo, 0, len(snapshot.Workers))
	for _, worker := range snapshot.Workers {
		workers = append(workers, proto.WorkerInfo{
			ID: worker.ID, CPU: worker.CPU, Cores: worker.Cores, Threads: worker.Threads,
			TasksDone: worker.TasksDone, Speed: worker.Speed, Status: worker.Status,
		})
	}
	verify := verifySnapshotResponse{
		Sampled: snapshot.Verify.Sampled, Matched: snapshot.Verify.Matched,
		Mismatched: snapshot.Verify.Mismatched, Skipped: snapshot.Verify.Skipped,
		Pending: snapshot.Verify.Pending, Mismatches: make([]mismatchResponse, 0, len(snapshot.Verify.Mismatches)),
	}
	for _, mismatch := range snapshot.Verify.Mismatches {
		verify.Mismatches = append(verify.Mismatches, mismatchResponse{
			TaskID: mismatch.TaskID, OriginalWorker: mismatch.OriginalWorker,
			VerifierWorker: mismatch.VerifierWorker, Original: json.RawMessage(mismatch.Original),
			Rerun: json.RawMessage(mismatch.Rerun),
		})
	}
	writeJSON(w, http.StatusOK, jobSnapshotResponse{
		JobID: h.config.JobID, State: snapshot.State, Total: snapshot.Total, Done: snapshot.Done,
		InFlight: snapshot.InFlight, Requeued: snapshot.Requeued,
		ElapsedSec: snapshot.Elapsed.Seconds(), ETASec: snapshot.ETA.Seconds(),
		Workers: workers, Leaderboard: []proto.LeaderboardEntry{}, Verify: verify,
		Baseline: proto.BaselineInfo{Seconds: 0, Label: "1 laptop, all cores"}, Best: proto.BestResult{},
	})
}

func (h *Hub) handleAPIResult(w http.ResponseWriter, r *http.Request) {
	now := h.clock()
	h.mu.Lock()
	snapshot := h.scheduler.Snapshot(now)
	events := h.scheduler.DrainEvents(now)
	h.mu.Unlock()
	h.logEvents(now, events)
	writeJSON(w, http.StatusOK, proto.APIResultResponse{Ready: snapshot.State == sched.JobStateDone})
}

func (h *Hub) handleStart(w http.ResponseWriter, r *http.Request) {
	h.Start()
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func decodeRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, proto.ErrorResponse{Error: err.Error()})
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			writeJSON(w, http.StatusBadRequest, proto.ErrorResponse{Error: "request body must contain one JSON value"})
		} else {
			writeJSON(w, http.StatusBadRequest, proto.ErrorResponse{Error: err.Error()})
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Hub) logEvents(now time.Time, events []sched.Event) {
	for _, event := range events {
		h.logger.Printf("%s event=%s worker=%q task=%q", now.Format(time.RFC3339), event.Kind, event.WorkerName, event.TaskID)
	}
}

func listDataset(root string) []proto.DatasetFile {
	files := make([]proto.DatasetFile, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && path == root {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		files = append(files, proto.DatasetFile{File: filepath.ToSlash(rel), SHA256: hex.EncodeToString(sum[:]), Size: info.Size()})
		return nil
	})
	if err != nil {
		return []proto.DatasetFile{}
	}
	sort.Slice(files, func(i, j int) bool { return strings.Compare(files[i].File, files[j].File) < 0 })
	return files
}
