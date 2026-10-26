# Scrap Heap backend

Go backend for the Scrap Heap distributed Mandelbrot renderer. The protocol
package is available; coordinator HTTP/WebSocket service and worker UI are
future tasks.

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

## Run

The HTTP/WebSocket coordinator is not implemented yet. When it is added, start it
from the repository root with:

```powershell
go run ./cmd/server
```

See [docs/protocol.md](docs/protocol.md) for the worker wire contract.
