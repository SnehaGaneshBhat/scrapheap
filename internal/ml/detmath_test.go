package ml

import (
	"math"
	"testing"
)

func TestPRNGKnownSeedIsStable(t *testing.T) {
	rng := newPRNG(0)
	want := []uint64{
		0xe220a8397b1dcdaf,
		0x6e789e6aa1b965f4,
		0x06c45d188009454f,
	}
	for i, expected := range want {
		if got := rng.nextUint64(); got != expected {
			t.Fatalf("nextUint64() at %d = %#016x, want %#016x", i, got, expected)
		}
	}

	first := newPRNG(deriveSeed(12345, initStream))
	second := newPRNG(deriveSeed(12345, initStream))
	shuffle := newPRNG(deriveSeed(12345, shuffleStream))
	for i := 0; i < 8; i++ {
		firstValue := first.nextUint64()
		if got := second.nextUint64(); got != firstValue {
			t.Fatalf("same stream differs at %d: %#016x != %#016x", i, firstValue, got)
		}
		if got := shuffle.nextUint64(); got == firstValue {
			t.Fatalf("independent streams unexpectedly match at %d", i)
		}
	}
}

func TestPRNGFloat64Range(t *testing.T) {
	rng := newPRNG(42)
	for i := 0; i < 1000; i++ {
		value := rng.nextFloat64()
		if value < 0 || value >= 1 {
			t.Fatalf("nextFloat64() = %g, outside [0, 1)", value)
		}
	}
}

func TestDetExpMatchesStandardMath(t *testing.T) {
	for step := -7000; step <= 7000; step++ {
		x := float64(step) / 10
		got := detExp(x)
		want := math.Exp(x)
		if !closeRelative(got, want, 1e-12) {
			t.Fatalf("detExp(%g) = %.17g, math.Exp = %.17g", x, got, want)
		}
	}
	for _, x := range []float64{-745.5, -745.2, -745.1, -745, -744.5} {
		got := detExp(x)
		want := math.Exp(x)
		if !closeRelative(got, want, 1e-12) {
			t.Fatalf("detExp(%g) = %.17g, math.Exp = %.17g", x, got, want)
		}
	}
	nearOverflow := detExp(709.78)
	if math.IsInf(nearOverflow, 0) || nearOverflow < math.MaxFloat64*0.99 {
		t.Fatalf("detExp(709.78) = %.17g, want a finite value near MaxFloat64", nearOverflow)
	}
}

func TestDetLogMatchesStandardMath(t *testing.T) {
	for step := -7000; step <= 7000; step++ {
		x := math.Exp(float64(step) / 10)
		got := detLog(x)
		want := math.Log(x)
		if math.Abs(got-want) > 1e-12+1e-12*math.Abs(want) {
			t.Fatalf("detLog(%.17g) = %.17g, math.Log = %.17g", x, got, want)
		}
	}
	for _, x := range []float64{math.MaxFloat64, 0.5, 1, 2} {
		want := math.Log(x)
		got := detLog(x)
		if math.Abs(got-want) > 1e-12+1e-12*math.Abs(want) {
			t.Fatalf("detLog(%g) = %.17g, math.Log = %.17g", x, got, want)
		}
	}

	subnormal := math.SmallestNonzeroFloat64
	wantSubnormal := math.Log(float64(subnormal*18014398509481984.0)) - 54*ln2
	if got := detLog(subnormal); math.Abs(got-wantSubnormal) > 1e-12 {
		t.Fatalf("detLog(%g) = %.17g, want %.17g", subnormal, got, wantSubnormal)
	}
}

func TestSoftmaxRowsSumToOne(t *testing.T) {
	cases := [][]float64{
		{0, 0, 0},
		{1000, 1001, 999},
		{-1000, -1001, -999, -1200},
		{1e-12, -1e-12, 0},
	}
	for _, logits := range cases {
		probabilities, err := softmax(logits)
		if err != nil {
			t.Fatalf("softmax(%v): %v", logits, err)
		}
		sum := 0.0
		for _, probability := range probabilities {
			if probability < 0 || probability > 1 || math.IsNaN(probability) {
				t.Fatalf("softmax(%v) produced invalid probability %g", logits, probability)
			}
			sum += probability
		}
		if math.Abs(sum-1) > 1e-15 {
			t.Fatalf("softmax(%v) sums to %.17g, want 1", logits, sum)
		}
	}
}

func TestSoftmaxRejectsEmptyOrNonFiniteInputs(t *testing.T) {
	for _, logits := range [][]float64{
		nil,
		{math.NaN()},
		{math.Inf(1)},
		{math.Inf(-1)},
	} {
		if _, err := softmax(logits); err == nil {
			t.Fatalf("softmax(%v) unexpectedly succeeded", logits)
		}
	}
}

func closeRelative(got, want, tolerance float64) bool {
	if got == want {
		return true
	}
	if math.IsNaN(got) || math.IsNaN(want) {
		return false
	}
	scale := math.Abs(want)
	if scale < math.SmallestNonzeroFloat64 {
		scale = math.SmallestNonzeroFloat64
	}
	return math.Abs(got-want)/scale <= tolerance
}
