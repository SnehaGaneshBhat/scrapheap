package ml

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
)

// Config is an MNIST training configuration. JSON names follow the pending
// MNIST payload proposal in docs/protocol.md. A zero TrainRows uses all
// available training examples; a positive value selects the deterministic
// prefix of that set.
type Config struct {
	Hidden    int     `json:"hidden"`
	LR        float64 `json:"lr"`
	Epochs    int     `json:"epochs"`
	Batch     int     `json:"batch"`
	Seed      int     `json:"seed"`
	TrainRows int     `json:"trainRows,omitempty"`
}

// Result contains validation-only metrics from a training run.
type Result struct {
	ValAcc   float64 `json:"valAcc"`
	ValLoss  float64 `json:"valLoss"`
	TrainSec float64 `json:"trainSec"`
}

// ValidationHash returns a SHA-256 digest of the exact float64 bit patterns for
// validation accuracy and loss, in that order and big-endian byte order.
// TrainSec is intentionally excluded because wall-clock measurements vary.
func ValidationHash(result Result) [32]byte {
	var values [16]byte
	binary.BigEndian.PutUint64(values[:8], math.Float64bits(result.ValAcc))
	binary.BigEndian.PutUint64(values[8:], math.Float64bits(result.ValLoss))
	return sha256.Sum256(values[:])
}

// TestResult contains the final test-set metric. It is reserved for final
// winner evaluation and is not returned by Train.
type TestResult struct {
	TestAcc float64 `json:"testAcc"`
}

// Example is one normalized MNIST image and its digit label.
type Example struct {
	Pixels []float64
	Label  uint8
}

// Data holds the fixed train, validation, and held-back test sets.
type Data struct {
	Train      []Example
	Validation []Example
	Test       []Example
}

// ErrorKind identifies a package-level failure category.
type ErrorKind string

const (
	ErrorKindNotImplemented ErrorKind = "not_implemented"
)

// Error describes an operation failure in this package.
type Error struct {
	Op   string
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Op + ": " + e.Err.Error()
	}
	return e.Op + ": " + string(e.Kind)
}

func (e *Error) Unwrap() error {
	return e.Err
}

var errNotImplemented = errors.New("not implemented")
