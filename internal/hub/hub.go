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
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
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
	scheduler     *sched.Scheduler
	mu            sync.Mutex
	clock         func() time.Time
	config        Config
	dataset       []proto.DatasetFile
	manifest      datasetManifest
	browsers      map[string]string
	dataAvailable bool
	logger        *log.Logger
	runOnce       sync.Once
}

type manifestFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type datasetManifest struct {
	Files []manifestFile `json:"files"`
}

type jobWorkerResponse struct {
	ID        string  `json:"id"`
	CPU       string  `json:"cpu"`
	Cores     int     `json:"cores"`
	Threads   int     `json:"threads"`
	Browser   string  `json:"browser"`
	TasksDone int     `json:"tasksDone"`
	Speed     float64 `json:"speed"`
	Status    string  `json:"status"`
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
	Workers     []jobWorkerResponse      `json:"workers"`
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
	mime.AddExtensionType(".wasm", "application/wasm")
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
	dataset := listDataset(config.DataDir)
	dataInfo, dataErr := os.Stat(config.DataDir)
	manifest := datasetManifest{Files: make([]manifestFile, 0, len(dataset))}
	for _, file := range dataset {
		manifest.Files = append(manifest.Files, manifestFile{Name: file.File, Size: file.Size, SHA256: file.SHA256})
	}
	return &Hub{
		scheduler:     scheduler,
		clock:         config.Clock,
		config:        config,
		dataset:       dataset,
		manifest:      manifest,
		browsers:      make(map[string]string),
		dataAvailable: dataErr == nil && dataInfo.IsDir(),
		logger:        log.New(log.Writer(), "", 0),
	}
}

func (h *Hub) Handler() http.Handler {
	routes := map[string]http.Handler{
		"/w/hello":       requireMethod(http.MethodPost, http.HandlerFunc(h.handleHello)),
		"/w/pull":        requireMethod(http.MethodPost, http.HandlerFunc(h.handlePull)),
		"/w/result":      requireMethod(http.MethodPost, http.HandlerFunc(h.handleResult)),
		"/w/heartbeat":   requireMethod(http.MethodPost, http.HandlerFunc(h.handleHeartbeat)),
		"/api/job":       requireMethod(http.MethodGet, http.HandlerFunc(h.handleJob)),
		"/api/result":    requireMethod(http.MethodGet, http.HandlerFunc(h.handleAPIResult)),
		"/api/job/start": requireMethod(http.MethodPost, http.HandlerFunc(h.handleStart)),
		"/healthz":       requireMethod(http.MethodGet, http.HandlerFunc(h.handleHealth)),
		"/worker":        requireMethod(http.MethodGet, h.noCache(http.HandlerFunc(h.handleWorkerIndex))),
		"/trainer.wasm":  requireMethod(http.MethodGet, h.noCache(http.HandlerFunc(h.handleTrainerWasm))),
		"/":              requireMethod(http.MethodGet, h.noCache(http.HandlerFunc(h.handleWebRoot))),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler, ok := routes[r.URL.Path]; ok {
			handler.ServeHTTP(w, r)
			return
		}
		var handler http.Handler
		switch {
		case strings.HasPrefix(r.URL.Path, "/worker/"):
			handler = requireMethod(http.MethodGet, h.noCache(http.HandlerFunc(h.handleWorkerFile)))
		case strings.HasPrefix(r.URL.Path, "/dashboard/"):
			handler = requireMethod(http.MethodGet, h.noCache(http.HandlerFunc(h.handleDashboardFile)))
		case r.URL.Path == "/dataset/manifest.json":
			handler = requireMethod(http.MethodGet, http.HandlerFunc(h.handleManifest))
		case strings.HasPrefix(r.URL.Path, "/dataset/"):
			handler = requireMethod(http.MethodGet, http.HandlerFunc(h.handleDatasetFile))
		default:
			http.NotFound(w, r)
		}
		if handler != nil {
			handler.ServeHTTP(w, r)
		}
	})
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
	h.browsers[id] = req.Browser
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
	browsers := make(map[string]string, len(h.browsers))
	for id, browser := range h.browsers {
		browsers[id] = browser
	}
	events := h.scheduler.DrainEvents(now)
	h.mu.Unlock()
	h.logEvents(now, events)
	workers := make([]jobWorkerResponse, 0, len(snapshot.Workers))
	for _, worker := range snapshot.Workers {
		workers = append(workers, jobWorkerResponse{
			ID: worker.ID, CPU: worker.CPU, Cores: worker.Cores, Threads: worker.Threads,
			Browser: browsers[worker.ID], TasksDone: worker.TasksDone, Speed: worker.Speed, Status: worker.Status,
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

func (h *Hub) handleWebRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	rel := "dashboard/index.html"
	// Preserve the original root index for deployments not yet using dashboard/.
	if h.config.WebDir != "" {
		if _, err := os.Stat(filepath.Join(h.config.WebDir, filepath.FromSlash(rel))); errors.Is(err, os.ErrNotExist) {
			if _, legacyErr := os.Stat(filepath.Join(h.config.WebDir, "index.html")); legacyErr == nil {
				rel = "index.html"
			}
		}
	}
	h.serveFile(w, r, h.config.WebDir, rel, "")
}

func (h *Hub) handleWorkerIndex(w http.ResponseWriter, r *http.Request) {
	h.serveFile(w, r, h.config.WebDir, "worker/index.html", "")
}

func (h *Hub) handleTrainerWasm(w http.ResponseWriter, r *http.Request) {
	h.serveFile(w, r, h.config.WebDir, "trainer.wasm", "application/wasm")
}

func (h *Hub) handleWorkerFile(w http.ResponseWriter, r *http.Request) {
	h.servePathFile(w, r, filepath.Join(h.config.WebDir, "worker"), "/worker/")
}

func (h *Hub) handleDashboardFile(w http.ResponseWriter, r *http.Request) {
	h.servePathFile(w, r, filepath.Join(h.config.WebDir, "dashboard"), "/dashboard/")
}

func (h *Hub) handleDatasetFile(w http.ResponseWriter, r *http.Request) {
	h.servePathFile(w, r, h.config.DataDir, "/dataset/")
}

func (h *Hub) handleManifest(w http.ResponseWriter, r *http.Request) {
	if !h.dataAvailable {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, h.manifest)
}

func (h *Hub) servePathFile(w http.ResponseWriter, r *http.Request, root, prefix string) {
	rel, ok := safeRelativeFile(strings.TrimPrefix(r.URL.EscapedPath(), prefix))
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.serveResolvedFile(w, r, root, rel, "")
}

func (h *Hub) serveFile(w http.ResponseWriter, r *http.Request, root, name, contentType string) {
	if root == "" {
		http.NotFound(w, r)
		return
	}
	rel, ok := safeRelativeFile(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.serveResolvedFile(w, r, root, rel, contentType)
}

func (h *Hub) serveResolvedFile(w http.ResponseWriter, r *http.Request, root, rel, contentType string) {
	if root == "" {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(rel))
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, filepath.Base(rel), info.ModTime(), file)
}

func (h *Hub) noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func safeRelativeFile(escaped string) (string, bool) {
	decoded, err := url.PathUnescape(escaped)
	if err != nil || decoded == "" || strings.Contains(decoded, `\`) || path.IsAbs(decoded) || filepath.IsAbs(decoded) {
		return "", false
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == ".." {
			return "", false
		}
	}
	clean := path.Clean(decoded)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || !filepath.IsLocal(filepath.FromSlash(clean)) {
		return "", false
	}
	return clean, true
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
