# Scrap Heap backend

Go backend for the Scrap Heap distributed Mandelbrot renderer. The coordinator
provides a worker WebSocket endpoint, run/job/tile APIs, and static file serving.
The browser worker and dashboard UI are maintained separately.

## Requirements

- Go 1.22 or newer
- Windows PowerShell

## Build and verify

```powershell
go fmt ./...
go vet ./...
go build ./...
go test ./...
```

## Worker contract

Workers connect to /ws/worker; the dashboard connects to /ws/dash. The shared
contract defines 48-byte TASK and 20-byte RESULT headers, row-major RGBA output,
job lifecycle behavior, and the `process_tile` kernel signature. See
[docs/protocol.md](docs/protocol.md) for field offsets, pixel mapping, and the
fixed benchmark parameters.

## Run the coordinator (Windows PowerShell)

```powershell
go run ./cmd/server -addr :8080 -web web -autostart=true -mode aware
```

Use `-autostart=false` to wait for `POST /api/run-again`. The server prints LAN
worker and dashboard URLs at startup. `GET /api/job` reports progress;
`GET /api/tile/{taskId}` returns an accepted tile's raw RGBA bytes.

## Run mock devices

In another PowerShell window:

```powershell
go run ./cmd/mockworker -url ws://localhost:8080/ws/worker -devices 3 -threads 2 -tiles-per-sec 20
```

Optional mock-worker flags include `-jitter 0.2`, `-kill-after 3s`, and
`-name phone`.
