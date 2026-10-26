package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"scrapheap/internal/hub"
	"scrapheap/internal/jobs/burn"
	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

func main() {
	os.Exit(run())
}

func run() int {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	job := flag.String("job", "burn", "job type: burn or search")
	burnTasks := flag.Int("burn-tasks", 200, "number of burn tasks")
	burnIters := flag.Int("burn-iters", 50_000_000, "iterations per burn task")
	verifyRate := flag.Float64("verify-rate", 0, "deterministic sampled verification rate")
	verifySeed := flag.Uint64("verify-seed", 1, "verification sampling seed")
	verifyTimeoutSec := flag.Int("verify-timeout-sec", 30, "time a queued verification may wait before being skipped")
	leaseSec := flag.Int("leasesec", 6, "task lease duration in seconds")
	heartbeatSec := flag.Int("heartbeat-sec", 2, "worker heartbeat interval in seconds")
	dataDir := flag.String("data", "./data", "dataset directory")
	webDir := flag.String("web", "./web", "static frontend directory")
	autostart := flag.Bool("autostart", false, "start the job immediately")
	flag.Parse()

	if *job == "search" {
		fmt.Fprintln(os.Stderr, "not implemented yet")
		return 2
	}
	if *job != "burn" {
		fmt.Fprintf(os.Stderr, "unknown job %q (expected burn or search)\n", *job)
		return 2
	}

	tasks := burn.NewSource(*burnTasks, *burnIters)
	reducer := burn.NewReducer(*burnIters)
	leaseDuration := time.Duration(*leaseSec) * time.Second
	scheduler := sched.New(sched.Config{
		LeaseDur:         leaseDuration,
		HeartbeatTimeout: leaseDuration,
		VerifyRate:       *verifyRate,
		VerifySeed:       *verifySeed,
		VerifyTimeout:    time.Duration(*verifyTimeoutSec) * time.Second,
		Verifier:         burn.NewVerifier(),
	}, tasks, reducer)
	serverHub := hub.New(scheduler, hub.Config{
		JobID:        "burn-search",
		JobKind:      proto.TaskKindBurn,
		LeaseSec:     *leaseSec,
		HeartbeatSec: *heartbeatSec,
		DataDir:      *dataDir,
		WebDir:       *webDir,
	})
	if *autostart {
		serverHub.Start()
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()
	serverHub.Run(ctx)

	httpServer := &http.Server{Addr: *addr, Handler: serverHub.Handler()}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.ListenAndServe() }()

	select {
	case err := <-serveErr:
		cancel()
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server: %v", err)
			return 1
		}
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown: %v", err)
			return 1
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server: %v", err)
			return 1
		}
	}
	return 0
}
