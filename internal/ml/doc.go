// Package ml provides deterministic, pure-Go MNIST training.
//
// Training and evaluation will use a package-owned seeded PRNG, fixed dataset
// splits, single-threaded arithmetic, and deterministic math routines. The
// implementation must avoid architecture-dependent math intrinsics and map
// iteration in calculations. Dataset contents are verified by SHA-256 before
// use. Ordinary training reports validation metrics only; test data is reserved
// for final evaluation.
package ml
