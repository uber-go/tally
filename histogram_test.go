// Copyright (c) 2017 Uber Technologies, Inc.
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

func TestBucketPairsDefaultsToNegInfinityToInfinity(t *testing.T) {
	pairs := BucketPairs(nil)
	require.Equal(t, 1, len(pairs))

	assert.Equal(t, -math.MaxFloat64, pairs[0].LowerBoundValue())
	assert.Equal(t, math.MaxFloat64, pairs[0].UpperBoundValue())

	assert.Equal(t, time.Duration(math.MinInt64), pairs[0].LowerBoundDuration())
	assert.Equal(t, time.Duration(math.MaxInt64), pairs[0].UpperBoundDuration())
}

func TestBucketPairsDurationBucketsInsertsMissingZero(t *testing.T) {
	initial := 10
	buckets, err := LinearDurationBuckets(
		10*time.Millisecond,
		10*time.Millisecond,
		initial,
	)
	require.NoError(t, err)
	require.Equal(t, initial, len(buckets))

	pairs := BucketPairs(buckets)
	assert.Equal(t, initial+2, len(pairs))
	assert.Equal(t, time.Duration(math.MinInt64), pairs[0].LowerBoundDuration())
	assert.Equal(t, time.Duration(0), pairs[0].UpperBoundDuration())

	assert.Equal(t, 100*time.Millisecond, pairs[len(pairs)-1].LowerBoundDuration())
	assert.Equal(t, time.Duration(math.MaxInt64), pairs[len(pairs)-1].UpperBoundDuration())
}

func TestLinearValueBuckets(t *testing.T) {
	result, err := LinearValueBuckets(1, 1, 3)
	require.NoError(t, err)
	assert.Equal(t, "[1.000000 2.000000 3.000000]", Buckets(result).String())
}
