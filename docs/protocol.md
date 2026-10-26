# Scrap Heap worker protocol and API

Version 1 uses plain HTTP and JSON on one coordinator port, defaulting to `:8080`. Worker endpoints use `Content-Type: application/json` and return JSON. There is no authentication in v1; use only on a trusted LAN. JSON examples below are UTF-8.

## Worker endpoints

All worker calls are `POST` requests with a JSON object body and a JSON object reply. Fields shown in request and response schemas are required unless marked opaque or optional. The coordinator may return whitespace-different JSON; field names and meanings are the contract.

### `POST /w/hello`

Registers a worker or reconnects a worker identified by its name.

Request fields: `name`, `cpu`, `cores`, `threads`, `os`, and `version` are strings except `cores` and `threads`, which are integers.

```json
{
  "name": "laptop-01",
  "cpu": "Intel Core i7-12700H",
  "cores": 14,
  "threads": 20,
  "os": "windows/amd64",
  "version": "dev"
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

`unknownWorker: true` tells the worker to call `/w/hello` again. A heartbeat extends leases only for listed tasks held by that worker.

## Task kinds

Workers execute built-in task kinds. Code is never sent over the network.

### `burn`

Payload:

```json
{"iters": 1000000}
```

Result:

```json
{"checksum": "18446744073709551615"}
```

The unsigned 64-bit checksum is encoded as a decimal JSON string to preserve its full range.

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

The MNIST payload and result are proposals pending P2 sign-off.

## Other endpoints

### `GET /api/job`

Returns a live job snapshot. The object has exactly these top-level fields: `jobId`, `state`, `total`, `done`, `inFlight`, `requeued`, `elapsedSec`, `etaSec`, `workers`, `leaderboard`, `verify`, `baseline`, and `best`. `state` is `waiting`, `running`, or `done`. `workers` entries have `id`, `cpu`, `cores`, `threads`, `tasksDone`, `speed`, and `status`; worker `status` is `active`, `lost`, or `rejoined`. `leaderboard` entries have `config` and `valAcc`. `verify` has `sampled`, `matched`, and `mismatched`. `baseline` has `seconds` and `label`. `best` has `config` and `valAcc`. Counts are integers; elapsed/ETA, speed, baseline seconds, and accuracy are JSON numbers; `jobId`, CPU, and label are strings; config values are JSON objects.

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
      "cpu": "Intel Core i7-12700H",
      "cores": 14,
      "threads": 20,
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

The design specifies that this endpoint returns the final result, but does not define the JSON shape of its `ready: true` response. That shape must be agreed before implementing or consuming it; this document does not add fields to the contract.

### `POST /api/job/start`

Starts the job by transitioning state from `waiting` to `running`. No request body or response body/status is specified in this design. The transition is the endpoint's defined effect.

### `GET /dataset/<file>`

Serves the named dataset file from the coordinator. The response body is the file's raw bytes, not JSON. Dataset transfer encoding/content type is not specified here.

### `GET /healthz`

Returns the text `ok` (plain text, not JSON).

### `GET /`

Serves the static frontend from `web/`. The response is frontend content, not a JSON API response.

## Semantics

- **Pull mode:** workers request work; the coordinator does not push it.
- **Leases:** a task is leased to one worker until its deadline. Heartbeats extend leases for listed tasks held by that worker. On expiry, the task is requeued and the worker is marked `lost`.
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
