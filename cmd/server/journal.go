package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"

	"scrapheap/internal/sched"
)

// journalLine is one accepted task result, appended to the journal file.
type journalLine struct {
	TaskID  string          `json:"taskId"`
	Worker  string          `json:"worker"`
	Payload json.RawMessage `json:"payload"` // detects a changed search grid
	Result  json.RawMessage `json:"result"`
}

// journalReducer appends every accepted result to disk, then passes it on.
type journalReducer struct {
	next sched.Reducer
	file *os.File
}

func (j *journalReducer) Accept(spec sched.TaskSpec, result []byte, workerID string) {
	line, err := json.Marshal(journalLine{TaskID: spec.ID, Worker: workerID, Payload: spec.Payload, Result: result})
	if err == nil {
		if _, err = j.file.Write(append(line, '\n')); err == nil {
			err = j.file.Sync() // results are rare (one per task), so fsync is cheap
		}
	}
	if err != nil {
		println("journal write failed:", err.Error()) // keep running; only recovery is affected
	}
	j.next.Accept(spec, result, workerID)
}

// replayJournal feeds saved results to next and returns the tasks still to run.
// Unreadable (torn) lines and lines whose payload no longer matches are skipped,
// which just means that task is run again.
func replayJournal(path string, tasks []sched.TaskSpec, next sched.Reducer) []sched.TaskSpec {
	f, err := os.Open(path)
	if err != nil {
		return tasks
	}
	defer f.Close()
	byID := make(map[string]sched.TaskSpec, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	done := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var l journalLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || done[l.TaskID] {
			continue
		}
		spec, ok := byID[l.TaskID]
		if !ok || !bytes.Equal(spec.Payload, l.Payload) {
			continue
		}
		next.Accept(spec, l.Result, l.Worker)
		done[l.TaskID] = true
	}
	remaining := make([]sched.TaskSpec, 0, len(tasks))
	for _, t := range tasks {
		if !done[t.ID] {
			remaining = append(remaining, t)
		}
	}
	return remaining
}
