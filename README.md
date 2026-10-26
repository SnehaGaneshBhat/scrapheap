# Scrap Heap

Scrap Heap is a Go coordinator for independent tasks shared across laptops. Browser workers execute the deterministic burn workload or train the built-in MNIST model with Go WebAssembly.

## Requirements

- Go 1.22

## Run the coordinator

To use the MNIST search with the raw files in the downloaded Kaggle archive, leave the four top-level IDX files in `archive/`. The browser worker also accepts these gzip files in `data/`:

- `train-images-idx3-ubyte.gz`
- `train-labels-idx1-ubyte.gz`
- `t10k-images-idx3-ubyte.gz`
- `t10k-labels-idx1-ubyte.gz`

Build the trainer WASM and matching runtime shim:

```sh
sh scripts/build-wasm.sh
```

Build the Go server:

```sh
go build ./...
```

Start the burn job coordinator:

```sh
go run ./cmd/server -addr :8080
```

The job starts in `waiting` state. Start it from the dashboard or another terminal:

```sh
curl -i -X POST http://127.0.0.1:8080/api/job/start
```

Open `http://127.0.0.1:8080/worker` in a browser tab on the coordinator or another laptop on the same network. Join the job from the page; it starts one Web Worker per selected thread.

The live dashboard is at `http://127.0.0.1:8080/`. It can also start the waiting job and shows worker, progress, and verification status. For a single-tab local run, start the server with `-verify-rate 0`; sampled verification waits for a second, distinct worker. To test verification on one computer, open a second worker tab and give it a different worker name.

Check the coordinator and job snapshot:

```sh
curl -i http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/api/job
```

## Exercise the worker API

Register a temporary browser-shaped worker. Use its returned `workerId` in later requests:

```sh
curl -sS -H 'Content-Type: application/json' \
  -d '{"name":"laptop-a","cores":4,"threads":3,"browser":"Chrome"}' \
  http://127.0.0.1:8080/w/hello
```

Pull one task after starting the job, replacing `worker-1` with the `workerId` returned above:

```sh
curl -sS -H 'Content-Type: application/json' \
  -d '{"workerId":"worker-1"}' \
  http://127.0.0.1:8080/w/pull
```

The browser page runs `burn` tasks by default. For the raw files in `archive/`, start the MNIST search with `go run ./cmd/server -job search -data archive -addr :8080` after building the trainer. For gzip files in `data/`, use the default data directory instead. Each MNIST worker tab uses one trainer thread to limit memory use. A `curl` pull only reserves a task; do not treat it as completed until a worker has run its executor and returned the result.

The coordinator's search source produces a validation leaderboard. The held-back test set is not downloaded by the browser worker. See [implementation decisions](docs/implementation.md) for the initial grid and verification tolerances.

## Checks

```sh
go vet ./...
go test ./...
```
