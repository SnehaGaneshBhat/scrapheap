package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"scrapheap/internal/jobs/burn"
	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

func TestBrowserStaticRoutesAndDatasetManifest(t *testing.T) {
	root := t.TempDir()
	webDir, dataDir := filepath.Join(root, "web"), filepath.Join(root, "data")
	webFiles := map[string][]byte{
		"worker/index.html": []byte("worker home"), "worker/app.js": []byte("worker app"),
		"dashboard/index.html": []byte("dashboard home"), "dashboard/app.js": []byte("dashboard app"),
		"trainer.wasm": {0, 1, 2, 3},
	}
	dataFiles := map[string][]byte{"mnist/images.gz": []byte("images"), "mnist/labels.gz": []byte("labels")}
	for name, data := range webFiles {
		writeBrowserFile(t, filepath.Join(webDir, filepath.FromSlash(name)), data)
	}
	for name, data := range dataFiles {
		writeBrowserFile(t, filepath.Join(dataDir, filepath.FromSlash(name)), data)
	}
	server := httptest.NewServer(New(sched.New(sched.Config{}, nil, nil), Config{WebDir: webDir, DataDir: dataDir}).Handler())
	defer server.Close()
	checks := []struct {
		path, body, contentType string
		cache                   bool
	}{
		{"/worker", "worker home", "", true},
		{"/worker/app.js", "worker app", "", true},
		{"/trainer.wasm", string(webFiles["trainer.wasm"]), "application/wasm", true},
		{"/", "dashboard home", "", true},
		{"/dashboard/app.js", "dashboard app", "", true},
		{"/dataset/mnist/images.gz", "images", "", false},
	}
	for _, check := range checks {
		t.Run(check.path, func(t *testing.T) {
			response, err := server.Client().Get(server.URL + check.path)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || string(body) != check.body {
				t.Fatalf("GET %s status=%d body=%q", check.path, response.StatusCode, body)
			}
			if check.contentType != "" && response.Header.Get("Content-Type") != check.contentType {
				t.Fatalf("GET %s Content-Type=%q", check.path, response.Header.Get("Content-Type"))
			}
			if check.cache && response.Header.Get("Cache-Control") != "no-cache" {
				t.Fatalf("GET %s Cache-Control=%q", check.path, response.Header.Get("Cache-Control"))
			}
		})
	}
	response, err := server.Client().Get(server.URL + "/dataset/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var got datasetManifest
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(got.Files) != len(dataFiles) {
		t.Fatalf("manifest status=%d files=%+v", response.StatusCode, got.Files)
	}
	for i, file := range got.Files {
		if i > 0 && got.Files[i-1].Name > file.Name {
			t.Fatalf("manifest not sorted: %+v", got.Files)
		}
		data := dataFiles[file.Name]
		sum := sha256.Sum256(data)
		if file.Size != int64(len(data)) || file.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("manifest entry %q=%+v", file.Name, file)
		}
	}
	for _, route := range []string{"/worker/", "/dashboard/", "/dataset/"} {
		resp, err := server.Client().Get(server.URL + route)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("directory route %s status=%d, want 404", route, resp.StatusCode)
		}
	}
}

func TestBrowserRoutesRejectTraversal(t *testing.T) {
	parent := t.TempDir()
	webDir, dataDir := filepath.Join(parent, "web"), filepath.Join(parent, "data")
	writeBrowserFile(t, filepath.Join(parent, "secret.txt"), []byte("outside secret"))
	writeBrowserFile(t, filepath.Join(webDir, "secret.txt"), []byte("web secret"))
	writeBrowserFile(t, filepath.Join(dataDir, "safe.txt"), []byte("safe"))
	server := httptest.NewServer(New(sched.New(sched.Config{}, nil, nil), Config{WebDir: webDir, DataDir: dataDir}).Handler())
	defer server.Close()
	for _, route := range []string{"/dataset/../secret.txt", "/dataset/%2e%2e/secret.txt", "/worker/../secret.txt", "/worker/%2e%2e/secret.txt"} {
		response, err := server.Client().Get(server.URL + route)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if (response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusBadRequest) || strings.Contains(string(body), "secret") {
			t.Errorf("traversal %q status=%d body=%q", route, response.StatusCode, body)
		}
	}
}

func TestEmptyOrMissingBrowserDirectoriesReturn404(t *testing.T) {
	configs := []Config{
		{},
		{WebDir: filepath.Join(t.TempDir(), "absent-web"), DataDir: filepath.Join(t.TempDir(), "absent-data")},
	}
	for i, config := range configs {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			server := httptest.NewServer(New(sched.New(sched.Config{}, nil, nil), config).Handler())
			defer server.Close()
			for _, route := range []string{"/", "/worker", "/worker/a.js", "/trainer.wasm", "/dashboard/a.js", "/dataset/a", "/dataset/manifest.json"} {
				response, err := server.Client().Get(server.URL + route)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusNotFound {
					t.Errorf("GET %s status=%d, want 404", route, response.StatusCode)
				}
			}
		})
	}
}

func TestHelloBrowserFieldsAndLegacyHello(t *testing.T) {
	dataDir := t.TempDir()
	writeBrowserFile(t, filepath.Join(dataDir, "sample.bin"), []byte("x"))
	server := httptest.NewServer(New(sched.New(sched.Config{}, nil, nil), Config{DataDir: dataDir}).Handler())
	defer server.Close()
	client := server.Client()
	modern, err := callJSON(client, server.URL+"/w/hello", http.MethodPost, proto.HelloRequest{
		Name: "browser-worker", CPU: "cpu", Cores: 8, Threads: 16, OS: "linux/amd64", Version: "dev", Browser: "Firefox",
	})
	if err != nil || modern.status != http.StatusOK {
		t.Fatalf("modern hello status=%d err=%v", modern.status, err)
	}
	legacy, err := http.Post(server.URL+"/w/hello", "application/json", strings.NewReader(`{"name":"legacy-worker","cpu":"old","os":"linux/amd64","version":"old"}`))
	if err != nil {
		t.Fatal(err)
	}
	legacy.Body.Close()
	if legacy.StatusCode != http.StatusOK {
		t.Fatalf("legacy hello status=%d", legacy.StatusCode)
	}
	response, err := client.Get(server.URL + "/api/job")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var snapshot struct {
		Workers []jobWorkerResponse `json:"workers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(snapshot.Workers) != 2 {
		t.Fatalf("snapshot status=%d workers=%+v", response.StatusCode, snapshot.Workers)
	}
	if got := snapshot.Workers[0]; got.Browser != "Firefox" || got.Cores != 8 || got.Threads != 16 {
		t.Fatalf("modern worker fields not stored: %+v", got)
	}
	if got := snapshot.Workers[1]; got.Browser != "" || got.Cores != 0 || got.Threads != 0 {
		t.Fatalf("legacy hello defaults: %+v", got)
	}
}

func TestOneWorkerCanHoldThreeLeasesAndHeartbeatAll(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	scheduler := sched.New(sched.Config{LeaseDur: 6 * time.Second, HeartbeatTimeout: 30 * time.Second}, burn.NewSource(3, 1), nil)
	h := New(scheduler, Config{Clock: clock.Now})
	server := httptest.NewServer(h.Handler())
	defer server.Close()
	client := server.Client()
	hello, err := callJSON(client, server.URL+"/w/hello", http.MethodPost, proto.HelloRequest{Name: "multi-lease"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := decodeBody[proto.HelloResponse](hello.body)
	if err != nil {
		t.Fatal(err)
	}
	if response, err := callJSON(client, server.URL+"/api/job/start", http.MethodPost, nil); err != nil || response.status != http.StatusNoContent {
		t.Fatalf("start status=%d err=%v", response.status, err)
	}
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		response, err := callJSON(client, server.URL+"/w/pull", http.MethodPost, proto.PullRequest{WorkerID: worker.WorkerID})
		if err != nil {
			t.Fatal(err)
		}
		task, err := decodeBody[proto.TaskResponse](response.body)
		if err != nil || task.Status != proto.PullStatusTask {
			t.Fatalf("pull=%+v err=%v", task, err)
		}
		ids = append(ids, task.TaskID)
	}
	clock.Advance(5 * time.Second)
	response, err := callJSON(client, server.URL+"/w/heartbeat", http.MethodPost, proto.HeartbeatRequest{WorkerID: worker.WorkerID, Running: ids})
	if err != nil {
		t.Fatal(err)
	}
	heartbeat, err := decodeBody[proto.HeartbeatResponse](response.body)
	if err != nil || !heartbeat.OK || heartbeat.UnknownWorker {
		t.Fatalf("heartbeat=%+v err=%v", heartbeat, err)
	}
	clock.Advance(2 * time.Second)
	h.mu.Lock()
	scheduler.Tick(clock.Now())
	h.mu.Unlock()
	response, err = callJSON(client, server.URL+"/api/job", http.MethodGet, nil)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		InFlight int `json:"inFlight"`
		Requeued int `json:"requeued"`
	}
	if err := json.Unmarshal(response.body, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.InFlight != 3 || snapshot.Requeued != 0 {
		t.Fatalf("after heartbeat: inFlight=%d requeued=%d", snapshot.InFlight, snapshot.Requeued)
	}
}

func TestSingleWorkerVerificationSkipCompletesWithRealTickLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const iters = 3
	scheduler := sched.New(sched.Config{
		LeaseDur: 5 * time.Second, HeartbeatTimeout: 5 * time.Second,
		VerifyRate: 1, VerifySeed: 1, VerifyTimeout: time.Second, Verifier: burn.NewVerifier(),
	}, burn.NewSource(1, iters), burn.NewReducer(iters))
	h := New(scheduler, Config{DataDir: t.TempDir()})
	h.Run(ctx)
	server := httptest.NewServer(h.Handler())
	defer server.Close()
	client := server.Client()
	hello, err := callJSON(client, server.URL+"/w/hello", http.MethodPost, proto.HelloRequest{Name: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := decodeBody[proto.HelloResponse](hello.body)
	if err != nil {
		t.Fatal(err)
	}
	if response, err := callJSON(client, server.URL+"/api/job/start", http.MethodPost, nil); err != nil || response.status != http.StatusNoContent {
		t.Fatalf("start status=%d err=%v", response.status, err)
	}
	response, err := callJSON(client, server.URL+"/w/pull", http.MethodPost, proto.PullRequest{WorkerID: worker.WorkerID})
	if err != nil {
		t.Fatal(err)
	}
	task, err := decodeBody[proto.TaskResponse](response.body)
	if err != nil || task.Status != proto.PullStatusTask {
		t.Fatalf("pull=%+v err=%v", task, err)
	}
	result, _ := json.Marshal(proto.BurnResult{Checksum: strconv.FormatUint(burn.Burn(iters), 10)})
	response, err = callJSON(client, server.URL+"/w/result", http.MethodPost, proto.ResultRequest{
		WorkerID: worker.WorkerID, TaskID: task.TaskID, Attempt: task.Attempt, Result: result, Seconds: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := decodeBody[proto.ResultResponse](response.body)
	if err != nil || !accepted.Accepted {
		t.Fatalf("result=%+v err=%v", accepted, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		httpResponse, err := client.Get(server.URL + "/api/job")
		if err != nil {
			t.Fatal(err)
		}
		var snapshot struct {
			State  string `json:"state"`
			Verify struct {
				Skipped int `json:"skipped"`
			} `json:"verify"`
		}
		err = json.NewDecoder(httpResponse.Body).Decode(&snapshot)
		httpResponse.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.State == proto.JobStateDone {
			if snapshot.Verify.Skipped != 1 {
				t.Fatalf("completed with skipped=%d", snapshot.Verify.Skipped)
			}
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatal("single-worker sampled job did not finish via skipped verification within 5s")
}

func writeBrowserFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
