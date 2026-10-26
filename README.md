# Scrap Heap

Scrap Heap is a Go coordinator for independent tasks shared across laptops. The browser worker page can execute the deterministic burn workload. The MNIST WASM trainer and its ABI are not implemented yet.

## Requirements

- Go 1.22

## Run the coordinator

Build the repository:

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

The browser page runs `burn` tasks. `mnist` tasks remain unsupported until the trainer WASM ABI and dataset loader are defined. A `curl` pull only reserves a task; do not treat it as completed until a worker has run its executor and returned the result.

The coordinator also has a preconfigured `search` job source and validation leaderboard. Do not start it until the MNIST WASM trainer is integrated; the current browser worker cannot execute `mnist` tasks yet. See [implementation decisions](docs/implementation.md) for the initial grid and verification tolerances.

## Checks

```sh
go vet ./...
go test ./...
```
