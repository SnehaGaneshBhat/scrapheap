package ml

// Train trains a model and reports validation metrics only.
func Train(cfg Config, d *Data) (Result, error) {
	return Result{}, &Error{Op: "Train", Kind: ErrorKindNotImplemented, Err: errNotImplemented}
}

// EvalTest retrains cfg and evaluates it against the held-back test set.
func EvalTest(cfg Config, d *Data) (TestResult, error) {
	return TestResult{}, &Error{Op: "EvalTest", Kind: ErrorKindNotImplemented, Err: errNotImplemented}
}
