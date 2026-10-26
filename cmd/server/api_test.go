package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/scrap-heap/backend/internal/hub"
	"github.com/scrap-heap/backend/internal/proto"
	"github.com/scrap-heap/backend/internal/sched"
)

type testWorker struct {
	conn    *websocket.Conn
	ctx     context.Context
	threads int
	delay   time.Duration
	mu      sync.Mutex
	writeMu sync.Mutex
	jobID   uint32
	active  bool
	tasks   chan proto.Task
	done    chan uint32
}

type apiSnapshot struct {
	JobID      uint32   `json:"jobId"`
	Done       int      `json:"done"`
	Total      int      `json:"total"`
	Devices    int      `json:"devices"`
	Mode       string   `json:"mode"`
	View       hub.View `json:"view"`
	Canvas     int      `json:"canvas"`
	TileSize   int      `json:"tileSize"`
	Cols       int      `json:"cols"`
	PublicHost string   `json:"publicHost"`
}

func TestServerAPIRunAndTiles(t *testing.T) {
	workerHub := hub.New(hub.Config{Mode: sched.CapabilityAware, Job: hub.JobConfig{X0: -2.5, Y0: -1.75, Scale: .01, MaxIter: 40, CanvasSize: 128}, HeartbeatTimeout: 4 * time.Second, PublicHost: "worker.example"}, newLogCollector(false), nil)
	ctx, cancel := context.WithCancel(context.Background())
	go workerHub.Run(ctx)
	server := httptest.NewServer(newHTTPHandler(workerHub, t.TempDir()))
	t.Cleanup(func() { server.Close(); cancel() })
	workers := make([]*testWorker, 0, 3)
	for i := 0; i < 3; i++ {
		workers = append(workers, startTestWorker(t, server.URL, fmt.Sprintf("test-%d", i), 2, 500*time.Millisecond))
	}
	api := server.Client()
	if response, err := api.Get(server.URL + "/api/tile/0"); err != nil {
		t.Fatal(err)
	} else {
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("tile before acceptance status=%d", response.StatusCode)
		}
	}

	firstJob := postJob(t, api, server.URL)
	waitForDone(t, api, server.URL, firstJob)
	checkDone(t, api, server.URL, firstJob, 4)
	response, err := api.Get(server.URL + "/api/job")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot apiSnapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if snapshot.Mode != "aware" || snapshot.View != (hub.View{X0: -2.5, Y0: -1.75, Scale: .01, MaxIter: 40}) || snapshot.Canvas != 128 || snapshot.TileSize != 64 || snapshot.Cols != 2 || snapshot.PublicHost != "worker.example" {
		t.Fatalf("snapshot metadata=%+v", snapshot)
	}
	response, err = api.Get(server.URL + "/api/tile/0")
	if err != nil {
		t.Fatal(err)
	}
	pixels, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/octet-stream" || response.Header.Get("X-Job-Id") != strconv.FormatUint(uint64(firstJob), 10) || len(pixels) != 64*64*4 {
		t.Fatalf("tile response status=%d headers=%v bytes=%d", response.StatusCode, response.Header, len(pixels))
	}

	for _, w := range workers {
		draining := true
		for draining {
			select {
			case <-w.tasks:
			default:
				draining = false
			}
		}
	}
	middleJob := postJob(t, api, server.URL)
	select {
	case <-workers[0].tasks:
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not receive a task for mid-run reset test")
	}
	newJob := postJob(t, api, server.URL)
	if newJob != middleJob+1 {
		t.Fatalf("job id after reset=%d, want %d", newJob, middleJob+1)
	}
	waitForDone(t, api, server.URL, newJob)
	checkDone(t, api, server.URL, newJob, 4)
	if response, err := api.Post(server.URL+"/api/run-again", "", nil); err != nil {
		t.Fatal(err)
	} else {
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("run-again status=%d", response.StatusCode)
		}
	}
}

func startTestWorker(t *testing.T, baseURL, id string, threads int, delay time.Duration) *testWorker {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	url := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws/worker"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	w := &testWorker{conn: conn, ctx: ctx, threads: threads, delay: delay, tasks: make(chan proto.Task, 128), done: make(chan uint32, 16)}
	t.Cleanup(func() { cancel(); _ = conn.CloseNow() })
	writeTestJSON(t, w, proto.Message{Type: proto.MessageHello, DeviceID: id, UserAgent: "server integration test", Cores: uint32(threads), Threads: threads, Mock: true})
	msg := readTestJSON(t, conn)
	if msg.Type != proto.MessageWelcome {
		t.Fatalf("first server message=%s, want WELCOME", msg.Type)
	}
	bench := proto.BenchTile()
	pixels := make([]byte, int(bench.W)*int(bench.H)*4)
	for i := 3; i < len(pixels); i += 4 {
		pixels[i] = 255
	}
	digest := sha256.Sum256(pixels)
	hash := hex.EncodeToString(digest[:])
	writeTestJSON(t, w, proto.Message{Type: proto.MessageBenchResult, TilesPerSec: 40, PerThread: 20, Threads: threads, Hash: hash})
	go w.readLoop()
	return w
}

func (w *testWorker) readLoop() {
	for {
		typ, data, err := w.conn.Read(w.ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			msg, err := proto.DecodeJSON(data)
			if err != nil {
				return
			}
			switch msg.Type {
			case proto.MessageJobStarted:
				w.mu.Lock()
				w.jobID, w.active = msg.JobID, true
				w.mu.Unlock()
				for i := 0; i < w.threads; i++ {
					_ = w.send(proto.Message{Type: proto.MessageRequestTask})
				}
			case proto.MessageJobDone:
				w.mu.Lock()
				if w.jobID == msg.JobID {
					w.active = false
				}
				w.mu.Unlock()
				select {
				case w.done <- msg.JobID:
				default:
				}
			}
		} else if typ == websocket.MessageBinary {
			task, err := proto.DecodeTask(data)
			if err != nil {
				return
			}
			select {
			case w.tasks <- task:
			default:
			}
			go w.compute(task)
		}
	}
}

func (w *testWorker) compute(task proto.Task) {
	timer := time.NewTimer(w.delay)
	select {
	case <-w.ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
	}
	pixels := make([]byte, int(task.W)*int(task.H)*4)
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = byte(task.X), byte(task.Y), byte(i/4), 255
	}
	frame, err := proto.EncodeResult(proto.Result{JobID: task.JobID, TaskID: task.TaskID, Attempt: task.Attempt, W: task.W, H: task.H, ComputeMs: float32(w.delay.Milliseconds()), RGBA: pixels})
	if err != nil || w.write(websocket.MessageBinary, frame) != nil {
		return
	}
	w.mu.Lock()
	request := w.active && w.jobID == task.JobID
	w.mu.Unlock()
	if request {
		_ = w.send(proto.Message{Type: proto.MessageRequestTask})
	}
}

func (w *testWorker) send(msg proto.Message) error {
	b, err := proto.EncodeJSON(msg)
	if err != nil {
		return err
	}
	return w.write(websocket.MessageText, b)
}
func (w *testWorker) write(typ websocket.MessageType, data []byte) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return w.conn.Write(w.ctx, typ, data)
}
func writeTestJSON(t *testing.T, w *testWorker, msg proto.Message) {
	t.Helper()
	if err := w.send(msg); err != nil {
		t.Fatal(err)
	}
}
func readTestJSON(t *testing.T, conn *websocket.Conn) proto.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("frame type=%v, want text", typ)
	}
	msg, err := proto.DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}
func postJob(t *testing.T, client *http.Client, baseURL string) uint32 {
	t.Helper()
	response, err := client.Post(baseURL+"/api/run-again", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(response.Body)
		t.Fatalf("run-again status=%d body=%s", response.StatusCode, b)
	}
	var result struct {
		JobID uint32 `json:"jobId"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.JobID
}
func waitForDone(t *testing.T, client *http.Client, baseURL string, jobID uint32) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(baseURL + "/api/job")
		if err != nil {
			t.Fatal(err)
		}
		var state apiSnapshot
		err = json.NewDecoder(response.Body).Decode(&state)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if state.JobID == jobID && state.Done == state.Total && state.Total > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %d did not finish", jobID)
}
func checkDone(t *testing.T, client *http.Client, baseURL string, jobID uint32, total int) {
	t.Helper()
	response, err := client.Get(baseURL + "/api/job")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state apiSnapshot
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.JobID != jobID || state.Done != total || state.Total != total {
		t.Fatalf("job snapshot=%+v", state)
	}
}
