package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"scrapheap/internal/hub"
	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

const version = "dev"

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	job := flag.String("job", "burn", "job kind (burn or search)")
	tasks := flag.Int("burn-tasks", 16, "number of burn tasks")
	iters := flag.Int("burn-iters", 1000000, "iterations per burn task")
	leaseSec := flag.Int("lease-sec", 30, "task lease duration in seconds")
	heartbeatSec := flag.Int("heartbeat-sec", 10, "worker heartbeat interval in seconds")
	verifyRate := flag.Float64("verify-rate", 0.1, "fraction of completed tasks to re-run on another worker (0 to 1)")
	verifyAccTolerance := flag.Float64("verify-acc-tolerance", 0.005, "MNIST verification tolerance for validation accuracy")
	verifyLossTolerance := flag.Float64("verify-loss-tolerance", 0.02, "MNIST verification tolerance for validation loss")
	webDir := flag.String("web", "web", "static web files directory")
	dataDir := flag.String("data", "data", "dataset files directory")
	journalPath := flag.String("journal", "", "path to journal file for resuming an interrupted search (empty = disabled)")
	flag.Parse()
	if (*job != proto.TaskKindBurn && *job != "search") || *tasks < 1 || *iters < 0 || *leaseSec < 1 || *heartbeatSec < 1 || *heartbeatSec >= *leaseSec || *verifyRate < 0 || *verifyRate > 1 || *verifyAccTolerance < 0 || *verifyLossTolerance < 0 {
		log.Fatal("unsupported job or invalid task counts, durations, verification rate, or tolerance")
	}
	lease, heartbeat := time.Duration(*leaseSec)*time.Second, time.Duration(*heartbeatSec)*time.Second
	var source sched.JobSource
	var reducer sched.Reducer
	var verifier sched.SampleVerifier
	var resultsIface hub.ResultSnapshot // nil interface, safe for hub's nil guard
	jobID, jobKind := "burn-job", proto.TaskKindBurn
	switch *job {
	case proto.TaskKindBurn:
		work := make(taskList, *tasks)
		for i := range work {
			payload, err := json.Marshal(proto.BurnPayload{Iters: *iters})
			if err != nil {
				log.Fatal(err)
			}
			work[i] = sched.TaskSpec{ID: fmt.Sprintf("burn-%06d", i+1), Kind: proto.TaskKindBurn, Payload: payload, Cost: float64(*iters)}
		}
		source = work
		verifier = sched.SampleVerifierFunc(matchesBurnResult)
	case "search":
		sr := hub.NewSearchResults()
		resultsIface = sr
		verifier = hub.MNISTVerifier{AccuracyTolerance: *verifyAccTolerance, LossTolerance: *verifyLossTolerance}
		jobID, jobKind = "mnist-search-01", proto.TaskKindMnist
		allTasks := hub.NewSearchSource(hub.InitialSearchSpace()).Tasks()
		if *journalPath != "" {
			remaining := replayJournal(*journalPath, allTasks, sr)
			log.Printf("journal: replayed %d/%d tasks, %d remaining", len(allTasks)-len(remaining), len(allTasks), len(remaining))
			f, err := os.OpenFile(*journalPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				log.Fatal(err)
			}
			source = taskList(remaining)
			reducer = &journalReducer{next: sr, file: f}
		} else {
			source = taskList(allTasks)
			reducer = sr
		}
	}
	s := sched.New(sched.Config{LeaseDur: lease, HeartbeatTimeout: heartbeat, VerifyRate: *verifyRate, Verifier: verifier}, source, reducer)
	service, err := hub.New(hub.Config{Scheduler: s, Results: resultsIface, JobID: jobID, JobKind: jobKind, LeaseDuration: lease, HeartbeatInterval: heartbeat, WebDir: *webDir, DatasetDir: *dataDir, Logger: log.New(os.Stdout, "", 0)})
	if err != nil {
		log.Fatal(err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		ticker := time.NewTicker(heartbeat / 2)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				service.Tick(now)
			case <-stop:
				return
			}
		}
	}()
	log.Printf("scrapheap-server %s listening on %s", version, *addr)
	log.Fatal(http.ListenAndServe(*addr, service))
}

type taskList []sched.TaskSpec

func (tasks taskList) Tasks() []sched.TaskSpec { return tasks }

func matchesBurnResult(_ sched.TaskSpec, first, second []byte) bool {
	var a, b proto.BurnResult
	if json.Unmarshal(first, &a) != nil || json.Unmarshal(second, &b) != nil {
		return false
	}
	return a.Checksum == b.Checksum
}
