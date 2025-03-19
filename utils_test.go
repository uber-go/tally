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
)

func TestSafeFloat64ToInt64(t *testing.T) {
	assert.Equal(t, int64(math.MaxInt64), safeFloat64ToInt64(float64(math.MaxInt64)*2))
	assert.Equal(t, int64(math.MaxInt64), safeFloat64ToInt64(float64(math.MaxInt64)+1000))

	assert.Equal(t, int64(math.MinInt64), safeFloat64ToInt64(float64(math.MinInt64)*2))
	assert.Equal(t, int64(math.MinInt64), safeFloat64ToInt64(float64(math.MinInt64)-1000))

	assert.Equal(t, int64(1000), safeFloat64ToInt64(1000))
}

func TestSafeDurationSum(t *testing.T) {
	maxDuration := time.Duration(math.MaxInt64)
	assert.Equal(t, maxDuration, safeDurationSum(maxDuration, 1))

	minDuration := time.Duration(math.MinInt64)
	assert.Equal(t, minDuration, safeDurationSum(minDuration, -1))

	assert.Equal(t, 10*time.Second, safeDurationSum(3*time.Second, 7*time.Second))
}
