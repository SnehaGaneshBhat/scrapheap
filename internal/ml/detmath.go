package ml

import (
	"errors"
	"math"
)

const (
	ln2                = 0.693147180559945309417232121458176568
	sqrtTwo            = 1.414213562373095048801688724209698079
	sqrtHalf           = 0.707106781186547524400844362104849039
	float64FracMask    = uint64(0x000fffffffffffff)
	float64ExponentMax = uint64(0x7ff)
)

var errInvalidSoftmaxInput = errors.New("softmax requires at least one finite logit")

func detExp(x float64) float64 {
	if x != x {
		return x
	}
	if x > 709.782712893384 {
		return math.Inf(1)
	}
	if x < -746 {
		return 0
	}
	if math.IsInf(x, 1) {
		return math.Inf(1)
	}
	if math.IsInf(x, -1) {
		return 0
	}

	scaled := x / ln2
	k := int(scaled)
	fraction := scaled - float64(k)
	if fraction >= 0.5 {
		k++
	} else if fraction <= -0.5 {
		k--
	}
	kln2 := float64(float64(k) * ln2)
	reduced := x - kln2

	sum := 1.0
	term := 1.0
	for i := 1; i <= 18; i++ {
		term = float64(term * reduced)
		term /= float64(i)
		sum = sum + term
	}

	if k == 1024 {
		return float64(float64(sum*powerOfTwo(1023)) * 2)
	}
	if k == -1075 {
		return float64(float64(sum*0.5) * powerOfTwo(-1074))
	}
	if k < -1075 {
		return 0
	}
	return float64(sum * powerOfTwo(k))
}

func detLog(x float64) float64 {
	if x != x {
		return x
	}
	if x == 0 {
		return math.Inf(-1)
	}
	if x < 0 {
		return math.NaN()
	}

	bits := math.Float64bits(x)
	exponentBits := (bits >> 52) & float64ExponentMax
	if exponentBits == float64ExponentMax {
		if bits&float64FracMask != 0 {
			return x
		}
		return math.Inf(1)
	}

	exponent := int(exponentBits) - 1023
	if exponentBits == 0 {
		x = float64(x * 18014398509481984.0)
		bits = math.Float64bits(x)
		exponentBits = (bits >> 52) & float64ExponentMax
		exponent = int(exponentBits) - 1023 - 54
	}

	mantissaBits := (bits & float64FracMask) | uint64(1023)<<52
	mantissa := math.Float64frombits(mantissaBits)
	if mantissa >= sqrtTwo {
		mantissa = float64(mantissa * 0.5)
		exponent++
	} else if mantissa < sqrtHalf {
		mantissa = float64(mantissa * 2)
		exponent--
	}

	z := (mantissa - 1) / (mantissa + 1)
	zSquared := float64(z * z)
	term := z
	sum := z
	for denominator := 3; denominator <= 51; denominator += 2 {
		term = float64(term * zSquared)
		sum = sum + term/float64(denominator)
	}

	logMantissa := float64(2 * sum)
	logExponent := float64(float64(exponent) * ln2)
	return logExponent + logMantissa
}

func powerOfTwo(exponent int) float64 {
	if exponent >= -1022 && exponent <= 1023 {
		bits := uint64(exponent+1023) << 52
		return math.Float64frombits(bits)
	}
	if exponent >= -1074 && exponent < -1022 {
		bits := uint64(1) << uint(exponent+1074)
		return math.Float64frombits(bits)
	}
	return 0
}

func softmax(logits []float64) ([]float64, error) {
	if len(logits) == 0 {
		return nil, errInvalidSoftmaxInput
	}

	maximum := logits[0]
	if math.IsNaN(maximum) || math.IsInf(maximum, 0) {
		return nil, errInvalidSoftmaxInput
	}
	for i := 1; i < len(logits); i++ {
		if math.IsNaN(logits[i]) || math.IsInf(logits[i], 0) {
			return nil, errInvalidSoftmaxInput
		}
		if logits[i] > maximum {
			maximum = logits[i]
		}
	}

	probabilities := make([]float64, len(logits))
	sum := 0.0
	for i, logit := range logits {
		probabilities[i] = detExp(logit - maximum)
		sum = sum + probabilities[i]
	}
	for i := range probabilities {
		probabilities[i] /= sum
	}
	return probabilities, nil
}
