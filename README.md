# Scrap Heap backend

Go backend for the Scrap Heap distributed Mandelbrot renderer. The repository is
being scaffolded; the protocol package is available, while the coordinator server
and worker-facing UI are future tasks.

## Requirements

- Go 1.22 or newer
- Windows PowerShell

## Build and verify

```powershell
go build ./...
go vet ./...
go test ./... -race
```

## Run

The HTTP/WebSocket coordinator is not implemented yet. When it is added, start it
from the repository root with:

```powershell
go run ./cmd/server
```

See [docs/protocol.md](docs/protocol.md) for the worker wire contract.
