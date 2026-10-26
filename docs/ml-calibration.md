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
