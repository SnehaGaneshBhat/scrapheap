# Scrap Heap agent rules

- Use Go 1.22 and the standard library only. Ask before adding dependencies.
- Run `gofmt`, `go vet ./...`, and `go test ./...` before declaring work done.
- The scheduler (`internal/sched`) is pure: no I/O, `net/http`, goroutines, or `time.Now()`. Pass time in as a parameter. Keep workload (ML) knowledge out of it.
- Workers run only built-in executors selected by task `kind`; code is never sent over the network.
- Folder ownership: P1 owns `internal/proto`, `internal/sched`, `internal/hub`, `cmd/server`. P2 owns `internal/ml`. P3 owns `web/`. P4 owns `bench/` and `cmd/worker`. Do not edit files outside the folders named in the task.
- `docs/protocol.md` is the source of truth for the wire format. If code and documentation disagree, stop and report; do not silently change either.
- Logs use one line per event with timestamp, worker name, and task ID (`assigned`, `completed`, `lost`, `requeued`, `verified`).
- After finishing a task, append a short entry to `docs/PROGRESS.md` with date/time, what was done, what is untested, and what is blocked.
