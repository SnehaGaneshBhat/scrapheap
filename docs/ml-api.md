# MNIST trainer API integration

This guide describes the P2 package API for the coordinator/reducer (P1) and
native worker (P4). The Go import path is:

```go
import "scrapheap/internal/ml"
```

The package uses only the Go standard library. It is single-threaded during
training, uses `float64`, and does not send executable code over the network.

## Worker dataset lifecycle (P4)

Load the dataset once when initializing the worker executor, then reuse the
returned read-only `*ml.Data` for each task:

```go
data, err := ml.LoadData(datasetDir)
if err != nil {
    return fmt.Errorf("load MNIST data: %w", err)
}

result, err := ml.Train(config, data)
if err != nil {
    return fmt.Errorf("train MNIST task: %w", err)
}
```

`LoadData` expects the four gzip IDX files named in [mnist.md](./mnist.md).
It verifies the compressed-byte SHA-256 digests, validates IDX headers and
payload lengths, normalizes pixels to `[0, 1]` float64s, and creates a fixed
50,000 training / 10,000 validation split. The test set remains separate.

On the measured Windows 11 / AMD Ryzen 7 7435HS host, one `LoadData` call took
**0.399254 seconds**. This measurement includes reading, hashing,
decompressing, parsing and allocating all three sets; it is host- and
filesystem-dependent. The slowest team laptop has not been measured.

Treat `Data` as immutable after loading. The trainer only reads its examples;
multiple tasks may reuse the pointer, but callers should not mutate its slices
while a task is running.

## Worker task config and result

The established JSON keys from [protocol.md](./protocol.md) are:

```json
{
  "hidden": 128,
  "lr": 0.1,
  "epochs": 5,
  "batch": 64,
  "seed": 12345
}
```

The package's `ml.Config` has matching fields `Hidden`, `LR`, `Epochs`,
`Batch`, and `Seed`. The optional `TrainRows` field serializes as `trainRows`
when positive; zero omits it and trains on all available training rows. A
positive value trains on the deterministic prefix of the first 50,000 training
examples, while still scoring the complete validation set.

**Protocol update required:** the current wire payload in `protocol.md` does
not define `trainRows`. P1 must add it as an optional positive integer before
dispatching row-limited configs from [ml-search-space.md](./ml-search-space.md).
Until then, send only configs that omit `trainRows`.

`ml.Train` trains a fresh one-hidden-layer ReLU MLP from the config seed and
returns validation accuracy, validation loss, and elapsed `trainSec`. It never
reads `Data.Test` or reports test accuracy. JSON tags match the protocol's
proposed result keys: `valAcc`, `valLoss`, and `trainSec`.

`trainSec` includes model initialization, SGD training and validation scoring.
It is a worker measurement, not a deterministic metric. `Train` does not mutate
the config or data, and repeated calls with the same config/data produce the
same validation metrics on the same runtime, subject to the documented
cross-CPU verification still pending.

## Reducer final verification (P1)

After selecting the winning config from validation results, call
`ml.EvalTest(winningConfig, data)` **once** for the final test evaluation:

```go
testResult, err := ml.EvalTest(winningConfig, data)
if err != nil {
    return fmt.Errorf("evaluate winning MNIST config: %w", err)
}
fmt.Printf("final test accuracy: %.6f\n", testResult.TestAcc)
```

`EvalTest` re-trains the model from scratch with the same config and seed,
then reads only the held-back test examples for its final accuracy. It returns
`ml.TestResult` with JSON field `testAcc`. Do not call it for ordinary search
tasks or use test accuracy to rank configurations.

**Wire contract pending:** `protocol.md` leaves the final ready `/api/result`
shape unspecified. P1 must agree the final response schema before publishing
`testAcc`; this package does not silently change that protocol document.

## Errors and ownership

`LoadData`, `Train`, and `EvalTest` return errors instead of partial/success-
shaped results. Package failures are `*ml.Error` values with an `Op` and an
underlying `Err`; callers can wrap them with `%w` and inspect the chain with
`errors.As`/`errors.Is`. Missing files, checksum mismatches, invalid IDX data,
invalid config/data shapes, and non-finite loss all fail explicitly.

`Train` needs non-empty training and validation sets. `EvalTest` needs those
sets plus a non-empty test set. `TrainRows` must be zero or between 1 and the
available training set length. Invalid inputs return errors.

## Measured training calibration

The full-data baseline and cost model are recorded in
[ml-calibration.md](./ml-calibration.md). The baseline (hidden 128, lr 0.1,
5 epochs, batch 64, seed 12345) reached 96.82% validation accuracy in
approximately 39.2–39.6 seconds on the measured host. The 360-config grid is
designed for approximately 5–20 seconds there, but the slowest team laptop
still needs timing before relying on those estimates.
