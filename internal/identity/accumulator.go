// Copyright (c) 2021 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
//
// n.b. Here and elsewhere, we use nosplit to avoid stack size checks, which
//      are unnecessary as memory width is bounded to each instance of `a` (a
//      uint64) and, potentially, a single stack-local loop temporary while
//      iterating.
func NewAccumulator() Accumulator {
	return Accumulator(_hashSeed)
}

// NewAccumulatorWithSeed creates a new Accumulator with the provided seed value.
func NewAccumulatorWithSeed(seed uint64) Accumulator {
	return Accumulator(seed)
}

// AddString hashes str and folds it into the accumulator.
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
func (a Accumulator) Value() uint64 {
	return uint64(a)
}
