package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/scrap-heap/backend/internal/hub"
)

func newHTTPHandler(h *hub.Hub, webDir string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/ws/worker", h.Handler())
	mux.HandleFunc("/worker", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		page := "demo.html"
		if _, err := os.Stat(filepath.Join(webDir, "worker.html")); err == nil {
			page = "worker.html"
		}
		http.ServeFile(w, r, filepath.Join(webDir, page))
	})
	mux.HandleFunc("/api/run-again", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]uint32{"jobId": h.StartNewJob()})
	})
	mux.HandleFunc("/api/job", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, h.Snapshot())
	})
	mux.HandleFunc("/api/tile/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		value := strings.TrimPrefix(r.URL.Path, "/api/tile/")
		id, err := strconv.ParseUint(value, 10, 32)
		if err != nil || value == "" || strings.Contains(value, "/") {
			http.NotFound(w, r)
			return
		}
		pixels, jobID, ok := h.Tile(uint32(id))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Job-Id", strconv.FormatUint(uint64(jobID), 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pixels)
	})
	if webDir == "" {
		webDir = "web"
	}
	files := http.FileServer(http.Dir(webDir))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ext := strings.ToLower(filepath.Ext(r.URL.Path))
		if ext == ".js" || ext == ".html" {
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		}
		files.ServeHTTP(w, r)
	}))
	return mux
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
