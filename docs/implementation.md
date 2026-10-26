# Implementation decisions

`docs/protocol.md` remains the source of truth for messages and HTTP routes. This document records the implementation defaults that the plan left open.

## Burn smoke job

The browser worker runs `burn` in JavaScript inside a Web Worker. This provides a real browser-to-coordinator recovery path without waiting for the MNIST trainer. The checksum recurrence and result format are specified in `docs/protocol.md`.

## MNIST search source

The initial source generates 360 independent configurations from this grid:

| Field | Values |
| --- | --- |
| Hidden units | 32, 64, 128, 256 |
| Learning rate | 0.001, 0.003, 0.01 |
| Epochs | 3, 5, 8, 10, 12 |
| Batch size | 32, 64, 128 |
| Seed | 1, 2 |

The grid is deterministic and adjustable in `internal/hub/search.go`. It is a starter search space, not a measured optimum. The source ranks results by validation accuracy and retains the top ten. Test-set evaluation is not part of the reducer; it remains a single final evaluation after the search.

The scheduler assigns higher estimated costs first and preserves source order for ties. The initial search cost is `hidden units × epochs`; the row count is constant across this grid and therefore does not affect ordering. This estimate has not been calibrated against trainer timings.

Worker throughput uses completed tasks, their reported execution seconds, and the worker's thread count: `tasks × threads ÷ summed task seconds`. Pool ETA divides remaining primary and verification runs by the active workers' summed throughput. A worker without completed timing samples contributes no ETA estimate.

## Verification defaults

The server samples 10% of task IDs by stable FNV-1a hash by default. Use `-verify-rate 0` to disable verification for a one-worker smoke run. Search verification compares validation accuracy within `0.005` and validation loss within `0.02`; `trainSec` is ignored because timing varies by machine. These are initial, unmeasured tolerances and can be changed with `-verify-acc-tolerance` and `-verify-loss-tolerance`.

Each verification rerun goes to a different worker ID and uses the same task ID with a higher attempt. If only the original worker is available, the task waits for another worker. The first result remains the reducer's result; mismatches are counted in the snapshot.

## Dataset caching

The coordinator publishes dataset filenames, byte sizes, and SHA-256 hashes from its `-data` directory. The browser checks downloaded and cached bytes. It uses IndexedDB when available and a JavaScript SHA-256 fallback when Web Crypto is unavailable on a LAN origin. Without IndexedDB, caching lasts only for the current page context; the current burn executor does not need dataset files.

## Trainer boundary

`internal/ml/` and the trainer WASM ABI are being handled by the user's teammate and are intentionally untouched here. Until that trainer is integrated, the browser worker reports `mnist` tasks as unsupported and lets their leases recover. The search source and leaderboard are ready for the trainer result contract in `docs/protocol.md`.
