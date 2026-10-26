# MNIST training calibration

## Full validation run

The Iteration 5 run used the full fixed 50,000-example training split and
10,000-example validation split with this configuration:

```json
{"hidden":128,"lr":0.1,"epochs":5,"batch":64,"seed":12345}
```

| Run | Validation accuracy | Validation loss | `Train` seconds |
| ---: | ---: | ---: | ---: |
| 1 | 0.968200 (96.82%) | 0.113559 | 39.225 |
| 2 | 0.968200 (96.82%) | 0.113559 | 39.643 |

These values were emitted by the opt-in `TestTrainRealMNISTCalibration`, which
measures `Train`'s reported duration. The test package took 40.416 and 40.837
seconds wall time respectively, including dataset loading and test setup. The
test-set examples were not read by `Train`.

## Machine and reproduction

- CPU: AMD Ryzen 7 7435HS, 8 cores / 16 logical processors
- OS: Microsoft Windows 11 Home Single Language, build 26200, amd64
- Go toolchain: go1.27.0; the module's minimum is Go 1.22
- Dataset source and checksums: [mnist.md](./mnist.md)

Place the four verified files in `data/`, then run in PowerShell:

```powershell
$env:MNIST_CALIBRATION = "1"
go test ./internal/ml -run '^TestTrainRealMNISTCalibration$' -count=1 -v
```

The calibration test is skipped during ordinary test runs unless
`MNIST_CALIBRATION=1`. When enabled, it requires at least 95% validation
accuracy. The recorded result exceeds that target.

Only this machine has been measured so far. Performance on the slowest team
laptop and cross-CPU numerical agreement remain unverified.

## Iteration 6 cost calibration

The following eight runs varied hidden size, epochs and the deterministic
training prefix. `rows` is the number of training examples used; every run still
evaluated the full 10,000-example validation set. Exact measured values are in
[calibration.csv](../internal/ml/calibration.csv).

| Hidden | Epochs | Rows | Seconds | Validation accuracy |
| ---: | ---: | ---: | ---: | ---: |
| 32 | 2 | 5,000 | 0.576481 | 0.864800 |
| 64 | 3 | 10,000 | 2.485780 | 0.915600 |
| 128 | 2 | 10,000 | 3.405257 | 0.906900 |
| 64 | 5 | 20,000 | 7.051333 | 0.944800 |
| 128 | 3 | 15,000 | 6.703843 | 0.921900 |
| 96 | 5 | 20,000 | 10.436910 | 0.945900 |
| 128 | 5 | 15,000 | 10.681517 | 0.937300 |
| 128 | 4 | 25,000 | 14.314678 | 0.947100 |

The planned proxy is `hidden × epochs × rows`, exposed as `ml.EstimateCost`.
For this sample, linear regression gives Pearson correlation **0.99916** and
`R² = 0.99831` for:

```text
seconds ≈ 0.387710 + 1.073409e-6 × (hidden × epochs × rows)
```

The non-zero intercept reflects fixed work, principally the validation pass.
Fitting that pass as hidden-size-dependent gives a slightly better in-sample
model (`R² = 0.99945`):

```text
seconds ≈ 0.059395 + 1.046536e-6 × (hidden × epochs × rows)
          + 0.005133 × hidden
```

This is an eight-point, single-machine fit, not a portable wall-clock
guarantee. The search grid in [ml-search-space.md](./ml-search-space.md)
contains 360 configs whose corrected estimates are approximately 5.6–16.4
seconds on this host. The slowest team laptop still needs measurement.

`Config.TrainRows` is optional in JSON: zero preserves the full training set;
a positive value trains on the first N rows of the fixed training split.
Because `docs/protocol.md` does not yet list this field, P1 must add the
optional `trainRows` payload field before sending row-limited search tasks.
