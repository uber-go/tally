package identity

import (
	"github.com/twmb/murmur3"
)

const (
	_hashSeed uint64 = 23
	_hashFold uint64 = 31
)

// Accumulator is a commutative folding accumulator.
type Accumulator uint64

// NewAccumulator creates a new Accumulator with a default seed value.
//go:nosplit
func NewAccumulator() Accumulator {
	return Accumulator(_hashSeed)
}

// NewAccumulatorWithSeed creates a new Accumulator with the provided seed value.
//go:nosplit
func NewAccumulatorWithSeed(seed uint64) Accumulator {
	return Accumulator(seed)
}

// AddString hashes str and folds it into the accumulator.
//go:nosplit
func (a Accumulator) AddString(str string) Accumulator {
	return a + (Accumulator(murmur3.StringSum64(str)) * Accumulator(_hashFold))
}

// AddStrings serially hashes and folds each of strs into the accumulator.
//go:nosplit
func (a Accumulator) AddStrings(strs ...string) Accumulator {
	for _, str := range strs {
		a += (Accumulator(murmur3.StringSum64(str)) * Accumulator(_hashFold))
	}

	return a
}

// AddUint64 folds u64 into the accumulator.
//go:nosplit
func (a Accumulator) AddUint64(u64 uint64) Accumulator {
	return a + Accumulator(u64*_hashFold)
}

// AddUint64s serially folds each of u64s into the accumulator.
//go:nosplit
func (a Accumulator) AddUint64s(u64s ...uint64) Accumulator {
	for _, u64 := range u64s {
		a += Accumulator(u64 * _hashFold)
	}

	return a
}

// Value returns the accumulated value.
//go:nosplit
func (a Accumulator) Value() uint64 {
	return uint64(a)
}
