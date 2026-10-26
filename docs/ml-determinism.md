# Determinism harness

Run the fixed synthetic cases from the repository root:

```powershell
go run ./internal/ml/cmd/mlcheck
```

The command writes newline-delimited JSON. The first record reports `GOOS`,
`GOARCH`, Go version, runtime logical CPU count and CPU information where
available (`PROCESSOR_IDENTIFIER` on Windows or `/proc/cpuinfo` on Linux).
Subsequent records show each fixed config, result and `validationHash`.

`validationHash` is SHA-256 of the big-endian IEEE-754 `float64` bit patterns
for `valAcc` followed by `valLoss`. `trainSec` is included in the result for
context but is intentionally excluded from the hash because wall-clock timing
is not deterministic. Do not compare `trainSec` when checking bit-exact output.

## Compare two machines

1. Use the same checkout/commit and run the command on both machines.
2. Save each output; for example:

   ```powershell
   go run ./internal/ml/cmd/mlcheck > mlcheck.jsonl
   ```
3. Compare each case's `validationHash`. A matching hash means the validation
   metrics have the same exact float64 bit patterns for that case.
4. Keep the environment record with the output so a mismatch can be tied to
   `GOOS`, `GOARCH`, Go version and reported CPU.

## Outcome and tolerance

- Verified on this machine: `windows/amd64`, AMD64 Family 25 Model 68 Stepping
  1 (AuthenticAMD), 16 runtime CPUs, Go 1.27.0. Two `mlcheck` runs produced the
  same hashes:
  - `seed-12345`: `f03311bf72b3c7e4892fa536bd6c8a139077a9311a229edfc9da699cd0938b14`
  - `seed-7`: `105bab9940be15f389bdc6307da5620a3ad91619ad4e68c53d3a6cc6f9320dde`
  - `seed-2026`: `de72a71fefc442674594c81407e00b839bc70597cb152943403421602b2e1c10`
- Cross-machine comparison: **not performed**; no ARM or second-host results
  are available in this workspace.
- Provisional verifier tolerance: **0.0** for these hashes, meaning exact
  validation bit patterns are expected. This is not a cross-CPU claim. If
  another architecture differs, record the observed per-metric differences
  before proposing a nonzero numeric tolerance; do not accept hash mismatches
  as equivalent by default.
