package burn

import (
	"encoding/json"
	"strconv"
	"sync"

	"scrapheap/internal/proto"
	"scrapheap/internal/sched"
)

// Source provides a stable list of CPU-burn tasks.
type Source struct {
	tasks []sched.TaskSpec
}

func NewSource(taskCount, iters int) *Source {
	if taskCount < 0 {
		taskCount = 0
	}
	tasks := make([]sched.TaskSpec, 0, taskCount)
	payload, _ := json.Marshal(proto.BurnPayload{Iters: iters})
	for i := 0; i < taskCount; i++ {
		tasks = append(tasks, sched.TaskSpec{
			ID:      "burn-" + strconv.Itoa(i),
			Kind:    proto.TaskKindBurn,
			Payload: append([]byte(nil), payload...),
			Cost:    float64(iters),
		})
	}
	return &Source{tasks: tasks}
}

func (s *Source) Tasks() []sched.TaskSpec {
	out := make([]sched.TaskSpec, len(s.tasks))
	for i, task := range s.tasks {
		out[i] = task
		out[i].Payload = append([]byte(nil), task.Payload...)
	}
	return out
}

// Burn deterministically mixes integers for the requested number of iterations.
func Burn(iters int) uint64 {
	x := uint64(0x9e3779b97f4a7c15)
	for i := 0; i < iters; i++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		x += uint64(i) * 0x5851f42d4c957f2d
	}
	return x
}

// Reducer validates checksums returned for burn tasks.
type Reducer struct {
	mu       sync.Mutex
	expected uint64
	accepted int
	wrong    map[string]struct{}
}

func NewReducer(iters int) *Reducer {
	return &Reducer{expected: Burn(iters), wrong: make(map[string]struct{})}
}

func (r *Reducer) Accept(spec sched.TaskSpec, result []byte, workerID string) {
	var got proto.BurnResult
	valid := json.Unmarshal(result, &got) == nil
	checksum, err := strconv.ParseUint(got.Checksum, 10, 64)
	valid = valid && err == nil

	r.mu.Lock()
	defer r.mu.Unlock()
	r.accepted++
	if !valid || checksum != r.expected {
		r.wrong[spec.ID] = struct{}{}
	}
}

func (r *Reducer) Stats() (accepted, wrong int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.accepted, len(r.wrong)
}

func (r *Reducer) WrongTasks() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.wrong))
	for id := range r.wrong {
		out = append(out, id)
	}
	return out
}
