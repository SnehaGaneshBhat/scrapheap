package ml

const (
	splitMixIncrement = uint64(0x9e3779b97f4a7c15)
	initStream        = uint64(0x243f6a8885a308d3)
	shuffleStream     = uint64(0x13198a2e03707344)
)

type prng struct {
	state uint64
}

func newPRNG(seed uint64) *prng {
	return &prng{state: seed}
}

func (r *prng) nextUint64() uint64 {
	r.state += splitMixIncrement
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *prng) nextFloat64() float64 {
	return float64(r.nextUint64()>>11) * (1.0 / 9007199254740992.0)
}

func deriveSeed(seed int, stream uint64) uint64 {
	z := uint64(seed) + stream + splitMixIncrement
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
