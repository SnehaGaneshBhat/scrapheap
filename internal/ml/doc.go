// Package ml provides deterministic, pure-Go MNIST training.
//
// Training uses a package-owned SplitMix64 PRNG, fixed dataset splits,
// single-threaded float64 arithmetic, and package-provided exp and log
// approximations. Calculations avoid architecture-dependent math intrinsics
// and map iteration. Dataset contents are verified by SHA-256 before use.
// ValidationHash hashes exact validation metric bits and excludes wall-clock
// duration. Ordinary training reports validation metrics only; test data is
// reserved for final evaluation.
package ml
