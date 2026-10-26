package hub

import (
	"encoding/json"
	"math"
	"sort"
	"sync"

	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

// SearchSpace describes the finite MNIST configuration grid sent to workers.
type SearchSpace struct {
	Hidden  []int
	Rates   []float64
	Epochs  []int
	Batches []int
	Seeds   []int
}

// InitialSearchSpace is a documented, adjustable starter grid with 360 tasks.
func InitialSearchSpace() SearchSpace {
	return SearchSpace{
		Hidden:  []int{32, 64, 128, 256},
		Rates:   []float64{0.001, 0.003, 0.01},
		Epochs:  []int{3, 5, 8, 10, 12},
		Batches: []int{32, 64, 128},
		Seeds:   []int{1, 2},
	}
}

// SearchSource builds a stable row-major list of independent MNIST tasks.
type SearchSource struct {
	tasks []sched.TaskSpec
}

func NewSearchSource(space SearchSpace) *SearchSource {
	source := &SearchSource{}
	for _, hidden := range space.Hidden {
		for _, rate := range space.Rates {
			for _, epochs := range space.Epochs {
				for _, batch := range space.Batches {
					for _, seed := range space.Seeds {
						config := proto.Config{Hidden: hidden, LR: rate, Epochs: epochs, Batch: batch, Seed: seed}
						payload, _ := json.Marshal(config)
						id := "mnist-" + taskID(len(source.tasks)+1)
						cost := float64(hidden * epochs)
						source.tasks = append(source.tasks, sched.TaskSpec{ID: id, Kind: proto.TaskKindMnist, Payload: payload, Cost: cost})
					}
				}
			}
		}
	}
	return source
}

func (s *SearchSource) Tasks() []sched.TaskSpec {
	return append([]sched.TaskSpec(nil), s.tasks...)
}

type rankedResult struct {
	taskID string
	entry  proto.LeaderboardEntry
}

// SearchResults keeps the validation leaderboard. The test set is intentionally
// absent; it belongs to the single final evaluation after all tasks finish.
type SearchResults struct {
	mu      sync.RWMutex
	results map[string]rankedResult
}

func NewSearchResults() *SearchResults {
	return &SearchResults{results: make(map[string]rankedResult)}
}

func (r *SearchResults) Accept(spec sched.TaskSpec, result []byte, _ string) {
	var config proto.Config
	var metrics proto.MnistResult
	if json.Unmarshal(spec.Payload, &config) != nil || json.Unmarshal(result, &metrics) != nil {
		return
	}
	if metrics.ValAcc < 0 || metrics.ValAcc > 1 || metrics.ValLoss < 0 || metrics.TrainSec < 0 {
		return
	}
	r.mu.Lock()
	r.results[spec.ID] = rankedResult{taskID: spec.ID, entry: proto.LeaderboardEntry{Config: config, ValAcc: metrics.ValAcc}}
	r.mu.Unlock()
}

func (r *SearchResults) Snapshot() ([]proto.LeaderboardEntry, proto.BestResult) {
	r.mu.RLock()
	results := make([]rankedResult, 0, len(r.results))
	for _, item := range r.results {
		results = append(results, item)
	}
	r.mu.RUnlock()
	sort.Slice(results, func(i, j int) bool {
		if results[i].entry.ValAcc == results[j].entry.ValAcc {
			return results[i].taskID < results[j].taskID
		}
		return results[i].entry.ValAcc > results[j].entry.ValAcc
	})
	if len(results) == 0 {
		return []proto.LeaderboardEntry{}, proto.BestResult{}
	}
	limit := min(10, len(results))
	leaderboard := make([]proto.LeaderboardEntry, limit)
	for index := range limit {
		leaderboard[index] = results[index].entry
	}
	best := proto.BestResult{Config: results[0].entry.Config, ValAcc: results[0].entry.ValAcc}
	return leaderboard, best
}

// MNISTVerifier compares validation metrics and ignores timing noise.
type MNISTVerifier struct {
	AccuracyTolerance float64
	LossTolerance     float64
}

func (v MNISTVerifier) Matches(_ sched.TaskSpec, first, second []byte) bool {
	var a, b proto.MnistResult
	if json.Unmarshal(first, &a) != nil || json.Unmarshal(second, &b) != nil {
		return false
	}
	return math.Abs(a.ValAcc-b.ValAcc) <= v.AccuracyTolerance && math.Abs(a.ValLoss-b.ValLoss) <= v.LossTolerance
}

func taskID(n int) string {
	const digits = "000"
	s := itoa(n)
	if len(s) >= len(digits) {
		return s
	}
	return digits[:len(digits)-len(s)] + s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for n > 0 {
		index--
		buffer[index] = byte('0' + n%10)
		n /= 10
	}
	return string(buffer[index:])
}
