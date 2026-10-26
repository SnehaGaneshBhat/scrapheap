# Scrap Heap browser worker protocol and API

The Version 3 plan uses browser tabs as workers and loads the trainer from `/trainer.wasm`. The coordinator uses plain HTTP and JSON on one port, defaulting to `:8080`. Worker endpoints use `Content-Type: application/json` and return JSON. There is no authentication; use only on a trusted LAN. JSON examples below are UTF-8.

## Worker endpoints

All worker calls are `POST` requests with a JSON object body and a JSON object reply. Fields shown in request and response schemas are required unless marked opaque or optional. The coordinator may return whitespace-different JSON; field names and meanings are the contract.

### `POST /w/hello`

Registers a browser tab or reconnects a worker identified by its name. A worker represents one laptop tab; its `threads` value is the number of task threads the page will run.

Request fields: `name` and `browser` are strings; `cores` and `threads` are integers. `browser` identifies the browser and version reported by the page.

```json
{
  "name": "laptop-01",
  "cores": 8,
  "threads": 7,
  "browser": "Chrome 130"
}
```

Reply fields: `workerId` is a string; `leaseSec` and `heartbeatSec` are integers in seconds; `jobKind` is a string; `rejoined` is a boolean; `dataset` is an array of file descriptors whose `file` and `sha256` are strings and `size` is an integer byte count.

```json
{
  "workerId": "worker-01",
  "leaseSec": 30,
  "heartbeatSec": 10,
  "jobKind": "mnist",
  "rejoined": false,
  "dataset": [
    {
      "file": "train-images-idx3-ubyte.gz",
      "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "size": 9912422
    }
  ]
}
```

A later hello with the same `name` refers to the same worker, not a new worker. When that worker was marked lost, hello reports `rejoined: true`.

### `POST /w/pull`

Workers use pull mode: they ask the coordinator for work. The coordinator never pushes tasks.

Request:

```json
{
  "workerId": "worker-01"
}
```

The reply is exactly one of these three forms.

Task available (`payload` is opaque JSON and its structure depends on `kind`):

```json
{
  "status": "task",
  "taskId": "task-0001",
  "kind": "burn",
  "payload": {"iters": 1000000},
  "leaseDeadlineUnixMs": 1793030400000,
  "attempt": 1
}
```

No task is available now; the worker should wait at least `retryAfterMs` milliseconds before pulling again:

```json
{
  "status": "wait",
  "retryAfterMs": 1000
}
```

The job is complete:

```json
{
  "status": "done"
}
```

Workers may join during a job. If the job has not been started, pull returns `wait`.

### `POST /w/result`

Submits a task result. `result` is opaque JSON and `seconds` is a floating-point duration in seconds.

Request:

```json
{
  "workerId": "worker-01",
  "taskId": "task-0001",
  "attempt": 1,
  "result": {"checksum": "18446744073709551615"},
  "seconds": 2.75
}
```

Reply:

```json
{
  "accepted": true,
  "reason": "ok"
}
```

The allowed `reason` values are `ok`, `duplicate`, `stale`, and `unknown`. A late or duplicate result for an already-completed task is ignored and returns HTTP success with `accepted: false` and `reason: "duplicate"`; it is never an HTTP error. `stale` means the submitted attempt is no longer current; `unknown` means the worker or task is unknown.

```json
{"accepted": false, "reason": "duplicate"}
```

### `POST /w/heartbeat`

Reports the task IDs currently running on this worker and extends leases for the tasks it holds.

Request:

```json
{
  "workerId": "worker-01",
  "running": ["task-0001", "task-0002"]
}
```

Reply for a known worker:

```json
{
  "ok": true,
  "unknownWorker": false
}
```

Reply when the worker ID is unknown:

```json
{
  "ok": false,
  "unknownWorker": true
}
```

`unknownWorker: true` tells the worker to call `/w/hello` again. A heartbeat extends leases only for listed tasks held by that worker; other leases are not extended.

## Task kinds

Workers execute built-in task kinds. Code is never sent over the network.

### `burn`

Payload:

```json
{"iters": 0}
```

Result:

```json
{"checksum": "14695981039346656037"}
```

`iters` is a non-negative integer. Starting from `h = 14695981039346656037`, for each integer `i` from `0` to `iters - 1`, update `h = ((h XOR i) * 1099511628211) mod 2^64`. The unsigned 64-bit result is encoded as a decimal JSON string. The browser worker uses `BigInt` for the same recurrence.

The browser worker runs this test workload in JavaScript inside Web Workers so the full pull/lease/recovery path can be exercised. This does not send code from the coordinator: the executor is built into the worker page.

### `mnist`

The payload is a configuration object:

```json
{
  "hidden": 128,
  "lr": 0.01,
  "epochs": 5,
  "batch": 64,
  "seed": 12345
}
```

Result:

```json
{
  "valAcc": 0.97,
  "valLoss": 0.12,
  "trainSec": 8.43
}
```

The browser worker passes the payload as JSON to the built-in Go WASM trainer. The trainer uses the first 50,000 records from the MNIST training set for training and the remaining 10,000 for validation. The test set is not downloaded by the worker executor.

The trainer ABI is exposed in the worker global scope:

- `scrapHeapLoadDataset(trainImages, trainLabels)` accepts the two manifest-verified training IDX files as `Uint8Array` values. Each file may be raw IDX or gzip-compressed IDX. It returns a JSON string: success is `{"ready":true}`; failure is `{"error":"..."}`.
- `scrapHeapTrain(configJSON)` accepts the task payload serialized as JSON and returns a JSON string matching the result schema above. Failure is `{"error":"..."}`.

The dataset is loaded once per worker thread. The worker runs one trainer thread per browser tab to limit memory use. Each assignment trains independently and returns validation accuracy, validation loss, and elapsed training time. The executor and trainer are fixed assets served by the coordinator; executable code is never sent in a task.

## Sampled verification

The coordinator may reissue a sampled task to verify its first accepted result. The reissued assignment keeps the same `taskId` and payload and has a higher `attempt`; workers execute each assignment they receive as usual. A task selected for verification is assigned only to a worker ID different from the worker that returned the first result. If only that original worker is available, pulls wait until another worker can take it.

The coordinator selects samples deterministically from the FNV-1a hash of the task ID and the configured `-verify-rate`. The current server default is `0.1` (10 percent); this is an adjustable initial policy, not a measured optimum. The first result is passed to the reducer. Verification compares the second result through the job's `SampleVerifier`; it does not replace the accepted result. A mismatch increments `verify.mismatched` and is visible in the job snapshot. The job is not done until all selected verification runs finish.

For the burn job, results are compared by their decoded `checksum` value, so JSON whitespace does not affect a match. A stale result from an earlier attempt is rejected with `accepted: false` and `reason: "stale"`.

## Other endpoints

### `GET /api/job`

Returns a live job snapshot. The object has exactly these top-level fields: `jobId`, `state`, `total`, `done`, `inFlight`, `requeued`, `elapsedSec`, `etaSec`, `workers`, `leaderboard`, `verify`, `baseline`, and `best`. `state` is `waiting`, `running`, or `done`. `workers` entries have `id`, `browser`, `cores`, `threads`, `tasksDone`, `speed`, and `status`; worker `status` is `active`, `lost`, or `rejoined`. `leaderboard` entries have `config` and `valAcc`. `verify` has `sampled`, `matched`, and `mismatched`. `baseline` has `seconds` and `label`. `best` is `null` until a result exists, then has `config` and `valAcc`. Counts are integers; elapsed/ETA, speed, baseline seconds, and accuracy are JSON numbers; `jobId`, browser, and label are strings; config values are JSON objects.

```json
{
  "jobId": "mnist-search-01",
  "state": "running",
  "total": 360,
  "done": 42,
  "inFlight": 8,
  "requeued": 2,
  "elapsedSec": 120.5,
  "etaSec": 900.0,
  "workers": [
    {
      "id": "worker-01",
      "browser": "Chrome 130",
      "cores": 8,
      "threads": 7,
      "tasksDone": 10,
      "speed": 0.083,
      "status": "active"
    }
  ],
  "leaderboard": [
    {
      "config": {"hidden": 128, "lr": 0.01, "epochs": 5, "batch": 64, "seed": 12345},
      "valAcc": 0.97
    }
  ],
  "verify": {"sampled": 3, "matched": 2, "mismatched": 1},
  "baseline": {"seconds": 12.4, "label": "single-worker"},
  "best": {
    "config": {"hidden": 128, "lr": 0.01, "epochs": 5, "batch": 64, "seed": 12345},
    "valAcc": 0.97
  }
}
```

### `GET /api/result`

Before completion, returns:

```json
{"ready": false}
```

When the job is done, the endpoint currently returns HTTP 501 with an `error` string because the final result schema has not been defined. Agree on that schema before implementing the ready response; this document does not invent its fields.

### `POST /api/job/start`

Starts the job by transitioning state from `waiting` to `running`. The request has no body. The coordinator responds with HTTP 204 and no body. Repeated requests are harmless.

### `GET /worker`

Serves the browser worker join page.

### `GET /`

Serves the live dashboard. It polls `GET /api/job` and starts the job through `POST /api/job/start`.

### `GET /trainer.wasm`

Serves the compiled Go WebAssembly trainer. Build it and the matching Go runtime shim with `sh scripts/build-wasm.sh`.

### `GET /wasm_exec.js`

Serves the Go runtime shim built from the same installed Go toolchain as `trainer.wasm`.

### `GET /dataset/<file>`

Serves the named dataset file from the coordinator. The response body is the file's raw bytes, not JSON. Browser workers check byte size and SHA-256 against the `/w/hello` manifest before caching. The page uses IndexedDB when available and an in-memory cache for the current tab otherwise. SHA-256 uses Web Crypto when available and a bundled JavaScript implementation on LAN origins where `crypto.subtle` is unavailable. Cached bytes are rechecked against the manifest hash before reuse.

## Semantics

- **Pull mode:** workers request work; the coordinator does not push it.
- **Leases:** a task is leased to one worker until its deadline. Heartbeats extend leases only for listed tasks held by that worker. An individual lease expiry requeues that task; a heartbeat timeout marks the worker `lost` and requeues its outstanding tasks.
- **Rejoining:** a worker marked `lost` that later calls hello or heartbeat becomes `rejoined`. Hello uses the worker's stable name to identify it.
- **Verification:** the coordinator may issue the same `taskId` again with a different `attempt` to a different worker for verification. The worker is not told that this is a verification run.
- **Joining during a job:** workers may join after the job begins. Before the job is started, pulls return `wait`.
- **No remote code:** workers select built-in executors by task `kind`; executable code is never transmitted.
- **Authentication:** v1 has none and is intended for a trusted LAN.

## Worker lifecycle example

1. Worker `laptop-01` sends `POST /w/hello` and receives `workerId: "worker-01"`.
2. It sends `POST /w/pull`; the coordinator returns task `task-0001`, attempt `1`, and a lease deadline.
3. While executing, it sends `POST /w/heartbeat` with `running: ["task-0001"]`; the coordinator extends the lease.
4. It sends `POST /w/result` with that task ID and attempt. The coordinator replies `{"accepted":true,"reason":"ok"}`.
5. It pulls again. The coordinator returns another task, `wait`, or `done` according to job state and available work.

## Worker killed timeline

Example timings use a 30-second lease and 10-second heartbeat interval; values come from `/w/hello` and can vary.

| Time | Event | Coordinator state |
| --- | --- | --- |
| 12:00:00 | Worker pulls `task-0042`, attempt 1; lease expires at 12:00:30. | Task leased to worker; worker active. |
| 12:00:10 | Worker sends heartbeat listing `task-0042`. | Lease is extended. |
| 12:00:12 | Worker is killed; it sends no more heartbeats or result. | Task remains leased until its deadline. |
| 12:00:30 | Lease expires without another heartbeat. | Task is requeued; worker marked lost. |
| 12:00:31 | Another worker pulls `task-0042`. | The task is issued again with a new attempt. |
| 12:00:40 | Original worker starts again and calls hello with the same name. | It is recognized as the same worker and reports `rejoined: true`. |

## Error handling

Malformed JSON request bodies return HTTP `400` with a JSON error object. Example:

```json
{"error": "unexpected end of JSON input"}
```

No other HTTP error status or error body is defined by this protocol.

## Change policy

Edit this document first, then update code, and tell the team about the change. Never rename or remove an `/api/job` field without telling P3.
