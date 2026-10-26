package hub

import (
	"encoding/json"
	"testing"

	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

func TestInitialSearchSpaceHasStable48Tasks(t *testing.T) {
	source := NewSearchSource(InitialSearchSpace())
	tasks := source.Tasks()
	if len(tasks) != 48 {
		t.Fatalf("task count = %d, want 48", len(tasks))
	}
	if tasks[0].ID != "mnist-001" || tasks[len(tasks)-1].ID != "mnist-048" {
		t.Fatalf("unexpected task ID range: %q .. %q", tasks[0].ID, tasks[len(tasks)-1].ID)
	}
	for _, task := range tasks {
		if task.Kind != proto.TaskKindMnist || task.Cost <= 0 {
			t.Fatalf("invalid task spec: %+v", task)
		}
		var config proto.Config
		if err := json.Unmarshal(task.Payload, &config); err != nil {
			t.Fatalf("invalid config payload: %v", err)
		}
		if config.Hidden == 0 || config.LR == 0 || config.Epochs == 0 || config.Batch == 0 {
			t.Fatalf("incomplete config: %+v", config)
		}
	}
}

func TestSearchResultsRankByValidationAccuracy(t *testing.T) {
	results := NewSearchResults()
	firstConfig, _ := json.Marshal(proto.Config{Hidden: 32, LR: 0.01, Epochs: 3, Batch: 32, Seed: 1})
	secondConfig, _ := json.Marshal(proto.Config{Hidden: 64, LR: 0.003, Epochs: 5, Batch: 64, Seed: 2})
	results.Accept(sched.TaskSpec{ID: "mnist-001", Payload: firstConfig}, []byte(`{"valAcc":0.91,"valLoss":0.3,"trainSec":3}`), "worker-a")
	results.Accept(sched.TaskSpec{ID: "mnist-002", Payload: secondConfig}, []byte(`{"valAcc":0.95,"valLoss":0.2,"trainSec":4}`), "worker-b")
	leaderboard, best := results.Snapshot()
	if len(leaderboard) != 2 || leaderboard[0].ValAcc != 0.95 || best.Config.Hidden != 64 || best.ValAcc != 0.95 {
		t.Fatalf("unexpected leaderboard=%+v best=%+v", leaderboard, best)
	}
}

func TestMNISTVerifierUsesConfiguredToleranceAndIgnoresTiming(t *testing.T) {
	verifier := MNISTVerifier{AccuracyTolerance: 0.01, LossTolerance: 0.02}
	first := []byte(`{"valAcc":0.95,"valLoss":0.2,"trainSec":3}`)
	close := []byte(`{ "valAcc": 0.956, "valLoss": 0.21, "trainSec": 9 }`)
	different := []byte(`{"valAcc":0.98,"valLoss":0.2,"trainSec":3}`)
	if !verifier.Matches(sched.TaskSpec{}, first, close) {
		t.Fatal("within-tolerance metrics did not match")
	}
	if verifier.Matches(sched.TaskSpec{}, first, different) {
		t.Fatal("out-of-tolerance metrics unexpectedly matched")
	}
}
