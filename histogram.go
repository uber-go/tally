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
	"fmt"
	"math"
	"sort"
	"time"
)

// Defaults is a type that can represent a constant.
type Defaults int

// DefaultBuckets can be passed to specify to default buckets.
const DefaultBuckets = Defaults(0)

func (v Defaults) String() string {
	return "[defaults]"
}

// Len implements Buckets.
func (v Defaults) Len() int { return 0 }

// AsValues implements Buckets.
func (v Defaults) AsValues() []float64 { return nil }

// AsDurations implements Buckets.
func (v Defaults) AsDurations() []time.Duration { return nil }

// ValueBuckets is a set of float64 values that implements Buckets.
type ValueBuckets []float64

// Implements sort.Interface
func (v ValueBuckets) Len() int {
	return len(v)
}

// Implements sort.Interface
func (v ValueBuckets) Swap(i, j int) {
	v[i], v[j] = v[j], v[i]
}

// Implements sort.Interface
func (v ValueBuckets) Less(i, j int) bool {
	return v[i] < v[j]
}

func (v ValueBuckets) String() string {
	values := make([]string, len(v))
	for i := range values {
		values[i] = fmt.Sprintf("%f", v[i])
	}
	return fmt.Sprintf("%v", values)
}

// AsValues implements Buckets.
func (v ValueBuckets) AsValues() []float64 {
	sort.Sort(v) // Always sort first
	return []float64(v)
}

// AsDurations implements Buckets.
func (v ValueBuckets) AsDurations() []time.Duration {
	sort.Sort(v) // Always sort first
	values := make([]time.Duration, len(v))
	for i := range values {
		values[i] = time.Duration(v[i] * float64(time.Second))
	}
	return values
}

// DurationBuckets is a set of time.Duration values that implements Buckets.
type DurationBuckets []time.Duration

// Implements sort.Interface
func (v DurationBuckets) Len() int {
	return len(v)
}

// Implements sort.Interface
func (v DurationBuckets) Swap(i, j int) {
	v[i], v[j] = v[j], v[i]
}

// Implements sort.Interface
func (v DurationBuckets) Less(i, j int) bool {
	return v[i] < v[j]
}

func (v DurationBuckets) String() string {
	sort.Sort(v) // Always sort first
	values := make([]string, len(v))
	for i := range values {
		values[i] = v[i].String()
	}
	return fmt.Sprintf("%v", values)
}

// AsValues implements Buckets.
func (v DurationBuckets) AsValues() []float64 {
	sort.Sort(v) // Always sort first
	values := make([]float64, len(v))
	for i := range values {
		values[i] = float64(v[i]) / float64(time.Second)
	}
	return values
}

// AsDurations implements Buckets.
func (v DurationBuckets) AsDurations() []time.Duration {
	sort.Sort(v) // Always sort first
	return []time.Duration(v)
}

// BucketPairs creates a set of bucket pairs from a set
// of buckets describing the lower and upper bounds for
// each derived bucket.
func BucketPairs(buckets Buckets) []BucketPair {
	var (
		asValueBuckets    = buckets.AsValues()
		asDurationBuckets = buckets.AsDurations()
	)
	var pairs []BucketPair
	if buckets.Len() > 0 {
		pairs = append(pairs, bucketPair{
			-math.MaxFloat64, asValueBuckets[0],
			time.Duration(math.MinInt64), asDurationBuckets[0]})

		prevValueBucket, prevDurationBucket :=
			asValueBuckets[0], asDurationBuckets[0]
		for i := 1; i < buckets.Len(); i++ {
			pairs = append(pairs, bucketPair{
				prevValueBucket, asValueBuckets[i],
				prevDurationBucket, asDurationBuckets[i]})
			prevValueBucket, prevDurationBucket =
				asValueBuckets[i], asDurationBuckets[i]
		}

		pairs = append(pairs, bucketPair{
			prevValueBucket, math.MaxFloat64,
			prevDurationBucket, time.Duration(math.MaxInt64)})
	} else {
		pairs = append(pairs, bucketPair{
			-math.MaxFloat64, math.MaxFloat64,
			time.Duration(math.MinInt64), time.Duration(math.MaxInt64)})
	}
	return pairs
}

type bucketPair struct {
	lowerBoundValue    float64
	upperBoundValue    float64
	lowerBoundDuration time.Duration
	upperBoundDuration time.Duration
}

func (p bucketPair) LowerBoundValue() float64 {
	return p.lowerBoundValue
}

func (p bucketPair) UpperBoundValue() float64 {
	return p.upperBoundValue
}

func (p bucketPair) LowerBoundDuration() time.Duration {
	return p.lowerBoundDuration
}

func (p bucketPair) UpperBoundDuration() time.Duration {
	return p.upperBoundDuration
}

// LinearValueBuckets creates a set of linear value buckets.
func LinearValueBuckets(start, width float64, count int) ValueBuckets {
	if count <= 0 {
		panic("count needs to be > 0")
	}
	buckets := make([]float64, count)
	for i := range buckets {
		buckets[i] = start + (float64(i) * width)
	}
	return ValueBuckets(buckets)
}

// LinearDurationBuckets creates a set of linear duration buckets.
func LinearDurationBuckets(start, width time.Duration, count int) DurationBuckets {
	if count <= 0 {
		panic("count needs to be > 0")
	}
	buckets := make([]time.Duration, count)
	for i := range buckets {
		buckets[i] = start + (time.Duration(i) * width)
	}
	return DurationBuckets(buckets)
}

// ExponentialValueBuckets creates a set of exponential value buckets.
func ExponentialValueBuckets(start, factor float64, count int) ValueBuckets {
	if count <= 0 {
		panic("count needs to be > 0")
	}
	if start <= 0 {
		panic("start needs to be > 0")
	}
	if factor <= 1 {
		panic("factor needs to be > 1")
	}
	buckets := make([]float64, count)
	curr := start
	for i := range buckets {
		buckets[i] = curr
		curr *= factor
	}
	return ValueBuckets(buckets)
}

// ExponentialDurationBuckets creates a set of exponential duration buckets.
func ExponentialDurationBuckets(start time.Duration, factor float64, count int) DurationBuckets {
	if count <= 0 {
		panic("count needs to be > 0")
	}
	if start <= 0 {
		panic("start needs to be > 0")
	}
	if factor <= 1 {
		panic("factor needs to be > 1")
	}
	buckets := make([]time.Duration, count)
	curr := start
	for i := range buckets {
		buckets[i] = curr
		curr = time.Duration(float64(curr) * factor)
	}
	return DurationBuckets(buckets)
}
