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

package tally

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseDuration(s string) time.Duration {
	duration, _ := time.ParseDuration(s)
	return duration
}

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

func TestDurationBucketsOverflowString(t *testing.T) {
	maxDuration := time.Duration(math.MaxInt64)
	result, err := LinearDurationBuckets(maxDuration-(2*time.Second), time.Second, 4)
	require.NoError(t, err)
	assert.Equal(t, "[2562047h47m14.854775807s 2562047h47m15.854775807s 2562047h47m16.854775807s 2562047h47m16.854775807s]", Buckets(result).String())
}

func TestExponentialDurationBucketsOverflowString(t *testing.T) {
	result, err := ExponentialDurationBuckets(parseDuration("1749144h56m16.554119168s"), 1.3, 4)
	require.NoError(t, err)
	assert.Equal(t, "[1749144h56m16.554119168s 2273888h25m9.520355328s 2562047h47m16.854775807s 2562047h47m16.854775807s]", Buckets(result).String())
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

func TestMustMakeExponentialDurationBucketsOverflow(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.Equal(t, DurationBuckets{
			math.MaxInt64 * time.Nanosecond, math.MaxInt64 * time.Nanosecond, math.MaxInt64 * time.Nanosecond,
		}, MustMakeExponentialDurationBuckets(math.MaxInt64*time.Nanosecond, 2, 3))
	})
	assert.NotPanics(t, func() {
		assert.Equal(t, DurationBuckets{
			parseDuration("471095h35m13.288997632s"),
			parseDuration("612424h15m47.275696896s"),
			parseDuration("796151h32m31.458405888s"),
			parseDuration("1034997h0m16.895927808s"),
			parseDuration("1345496h6m21.964706816s"),
			parseDuration("1749144h56m16.554119168s"),
			parseDuration("2273888h25m9.520355328s"),
			parseDuration("2562047h47m16.854775807s"),
			parseDuration("2562047h47m16.854775807s"),
		}, MustMakeExponentialDurationBuckets(parseDuration("471095h35m13.288997632s"), 1.3, 9))
	})
}

func TestMustMakeLinearDurationBucketsOverflow(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.Equal(t, DurationBuckets{
			parseDuration("0s"),
			parseDuration("1281023h53m38.427387903s"),
			parseDuration("2562047h47m16.854775806s"),
			parseDuration("2562047h47m16.854775807s"),
			parseDuration("2562047h47m16.854775807s"),
			parseDuration("2562047h47m16.854775807s"),
		}, MustMakeLinearDurationBuckets(0, time.Duration(math.MaxInt64/2), 6))
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

func BenchmarkBucketsEqual(b *testing.B) {
	bench := func(b *testing.B, x Buckets, y Buckets) {
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			bucketsEqual(x, y)
		}
	}

	b.Run("same 20 values", func(b *testing.B) {
		buckets := MustMakeLinearValueBuckets(1.0, 1.0, 20)
		bench(b, buckets, buckets)
	})

	b.Run("same 20 durations", func(b *testing.B) {
		buckets := MustMakeLinearDurationBuckets(time.Second, time.Second, 20)
		bench(b, buckets, buckets)
	})
}
