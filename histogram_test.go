// Copyright (c) 2019 Uber Technologies, Inc.
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

package tally

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValueBucketsString(t *testing.T) {
	result, err := LinearValueBuckets(1, 1, 3)
	require.NoError(t, err)
	assert.Equal(t, "[1.000000 2.000000 3.000000]", Buckets(result).String())
}

func TestDurationBucketsString(t *testing.T) {
	result, err := LinearDurationBuckets(time.Second, time.Second, 3)
	require.NoError(t, err)
	assert.Equal(t, "[1s 2s 3s]", Buckets(result).String())
}

func TestBucketPairsDefaultsToNegInfinityToInfinity(t *testing.T) {
	pairs := BucketPairs(nil)
	require.Equal(t, 1, len(pairs))

	assert.Equal(t, -math.MaxFloat64, pairs[0].LowerBoundValue())
	assert.Equal(t, math.MaxFloat64, pairs[0].UpperBoundValue())

	assert.Equal(t, time.Duration(math.MinInt64), pairs[0].LowerBoundDuration())
	assert.Equal(t, time.Duration(math.MaxInt64), pairs[0].UpperBoundDuration())
}

func TestBucketPairsSortsValueBuckets(t *testing.T) {
	pairs := BucketPairs(ValueBuckets{1.0, 3.0, 2.0})
	require.Equal(t, 4, len(pairs))

	assert.Equal(t, -math.MaxFloat64, pairs[0].LowerBoundValue())
	assert.Equal(t, 1.0, pairs[0].UpperBoundValue())

	assert.Equal(t, 1.0, pairs[1].LowerBoundValue())
	assert.Equal(t, 2.0, pairs[1].UpperBoundValue())

	assert.Equal(t, 2.0, pairs[2].LowerBoundValue())
	assert.Equal(t, 3.0, pairs[2].UpperBoundValue())

	assert.Equal(t, 3.0, pairs[3].LowerBoundValue())
	assert.Equal(t, math.MaxFloat64, pairs[3].UpperBoundValue())
}

func TestBucketPairsSortsDurationBuckets(t *testing.T) {
	pairs := BucketPairs(DurationBuckets{0 * time.Second, 2 * time.Second, 1 * time.Second})
	require.Equal(t, 4, len(pairs))

	assert.Equal(t, time.Duration(math.MinInt64), pairs[0].LowerBoundDuration())
	assert.Equal(t, 0*time.Second, pairs[0].UpperBoundDuration())

	assert.Equal(t, 0*time.Second, pairs[1].LowerBoundDuration())
	assert.Equal(t, 1*time.Second, pairs[1].UpperBoundDuration())

	assert.Equal(t, 1*time.Second, pairs[2].LowerBoundDuration())
	assert.Equal(t, 2*time.Second, pairs[2].UpperBoundDuration())

	assert.Equal(t, 2*time.Second, pairs[3].LowerBoundDuration())
	assert.Equal(t, time.Duration(math.MaxInt64), pairs[3].UpperBoundDuration())
}

func TestMustMakeLinearValueBuckets(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.Equal(t, ValueBuckets{
			0.0, 1.0, 2.0,
		}, MustMakeLinearValueBuckets(0, 1, 3))
	})
}

func TestMustMakeLinearValueBucketsPanicsOnBadCount(t *testing.T) {
	assert.Panics(t, func() {
		MustMakeLinearValueBuckets(0, 1, 0)
	})
}

func TestMustMakeLinearDurationBuckets(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.Equal(t, DurationBuckets{
			0, time.Second, 2 * time.Second,
		}, MustMakeLinearDurationBuckets(0*time.Second, 1*time.Second, 3))
	})
}

func TestMustMakeLinearDurationBucketsPanicsOnBadCount(t *testing.T) {
	assert.Panics(t, func() {
		MustMakeLinearDurationBuckets(0*time.Second, 1*time.Second, 0)
	})
}

func TestMustMakeExponentialValueBuckets(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.Equal(t, ValueBuckets{
			2, 4, 8,
		}, MustMakeExponentialValueBuckets(2, 2, 3))
	})
}

func TestMustMakeExponentialValueBucketsPanicsOnBadCount(t *testing.T) {
	assert.Panics(t, func() {
		MustMakeExponentialValueBuckets(2, 2, 0)
	})
}

func TestMustMakeExponentialValueBucketsPanicsOnBadStart(t *testing.T) {
	assert.Panics(t, func() {
		MustMakeExponentialValueBuckets(0, 2, 2)
	})
}

func TestMustMakeExponentialValueBucketsPanicsOnBadFactor(t *testing.T) {
	assert.Panics(t, func() {
		MustMakeExponentialValueBuckets(2, 1, 2)
	})
}

func TestMustMakeExponentialDurationBuckets(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.Equal(t, DurationBuckets{
			2 * time.Second, 4 * time.Second, 8 * time.Second,
		}, MustMakeExponentialDurationBuckets(2*time.Second, 2, 3))
	})
}

func TestMustMakeExponentialDurationBucketsPanicsOnBadCount(t *testing.T) {
	assert.Panics(t, func() {
		MustMakeExponentialDurationBuckets(2*time.Second, 2, 0)
	})
}

func TestMustMakeExponentialDurationBucketsPanicsOnBadStart(t *testing.T) {
	assert.Panics(t, func() {
		MustMakeExponentialDurationBuckets(0, 2, 2)
	})
}

func TestMustMakeExponentialDurationBucketsPanicsOnBadFactor(t *testing.T) {
	assert.Panics(t, func() {
		MustMakeExponentialDurationBuckets(2*time.Second, 1, 2)
	})
}

func TestBucketPairsNoRaceWhenSorted(t *testing.T) {
	buckets := DurationBuckets{}
	for i := 0; i < 99; i++ {
		buckets = append(buckets, time.Duration(i)*time.Second)
	}
	newPair := func() {
		pairs := BucketPairs(buckets)
		require.Equal(t, 100, len(pairs))
	}
	for i := 0; i < 10; i++ {
		go newPair()
	}
}

func TestBucketPairsNoRaceWhenUnsorted(t *testing.T) {
	buckets := DurationBuckets{}
	for i := 100; i > 1; i-- {
		buckets = append(buckets, time.Duration(i)*time.Second)
	}
	newPair := func() {
		pairs := BucketPairs(buckets)
		require.Equal(t, 100, len(pairs))
	}
	for i := 0; i < 10; i++ {
		go newPair()
	}
}
