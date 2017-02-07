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
	"time"
)

// Buckets is a slice of buckets
type Buckets []Bucket

func (b Buckets) String() string {
	values := make([]string, len(b))
	for i := range values {
		values[i] = b[i].String()
	}
	return fmt.Sprintf("%v", values)
}

// Values returns the slice of buckets as values
func (b Buckets) Values() []float64 {
	values := make([]float64, len(b))
	for i := range values {
		values[i] = b[i].Value()
	}
	return values
}

// Durations returns the slice of buckets as durations
func (b Buckets) Durations() []time.Duration {
	values := make([]time.Duration, len(b))
	for i := range values {
		values[i] = b[i].Duration()
	}
	return values
}

// ValueBucket is a value bucket
type ValueBucket float64

// BucketType implements Bucket
func (v ValueBucket) BucketType() BucketType {
	return ValueBucketType
}

// Value implements Bucket
func (v ValueBucket) Value() float64 {
	return float64(v)
}

// String implements Bucket
func (v ValueBucket) String() string {
	return fmt.Sprintf("%f", v)
}

// Duration implements Bucket
func (v ValueBucket) Duration() time.Duration {
	return time.Duration(v.Value() * float64(time.Second))
}

// DurationBucket is a duration bucket
type DurationBucket time.Duration

// BucketType implements Bucket
func (d DurationBucket) BucketType() BucketType {
	return DurationBucketType
}

// Value implements Bucket
func (d DurationBucket) Value() float64 {
	return float64(d.Duration() / time.Second)
}

// Duration implements Bucket
func (d DurationBucket) Duration() time.Duration {
	return time.Duration(d)
}

// String implements Bucket
func (d DurationBucket) String() string {
	return d.Duration().String()
}

// ValueBuckets returns a slice of value buckets.
func ValueBuckets(values ...float64) []Bucket {
	buckets := make([]Bucket, len(values))
	for i, v := range values {
		buckets[i] = ValueBucket(v)
	}
	return buckets
}

// DurationBuckets returns a slice of duration buckets.
func DurationBuckets(values ...time.Duration) []Bucket {
	buckets := make([]Bucket, len(values))
	for i, v := range values {
		buckets[i] = DurationBucket(v)
	}
	return buckets
}

// LinearValueBuckets creates a set of linear value buckets.
func LinearValueBuckets(start, width float64, count int) []Bucket {
	if count <= 0 {
		panic("count needs to be > 0")
	}
	buckets := make([]Bucket, count)
	for i := range buckets {
		buckets[i] = ValueBucket(start + (float64(i) * width))
	}
	return buckets
}

// LinearDurationBuckets creates a set of linear duration buckets.
func LinearDurationBuckets(start, width time.Duration, count int) []Bucket {
	if count <= 0 {
		panic("count needs to be > 0")
	}
	buckets := make([]Bucket, count)
	for i := range buckets {
		buckets[i] = DurationBucket(start + (time.Duration(i) * width))
	}
	return buckets
}

// ExponentialValueBuckets creates a set of exponential value buckets.
func ExponentialValueBuckets(start, factor float64, count int) []Bucket {
	if count <= 0 {
		panic("count needs to be > 0")
	}
	if start <= 0 {
		panic("start needs to be > 0")
	}
	if factor <= 1 {
		panic("factor needs to be > 1")
	}
	buckets := make([]Bucket, count)
	curr := start
	for i := range buckets {
		buckets[i] = ValueBucket(curr)
		curr *= factor
	}
	return buckets
}

// ExponentialDurationBuckets creates a set of exponential duration buckets.
func ExponentialDurationBuckets(start time.Duration, factor float64, count int) []Bucket {
	if count <= 0 {
		panic("count needs to be > 0")
	}
	if start <= 0 {
		panic("start needs to be > 0")
	}
	if factor <= 1 {
		panic("factor needs to be > 1")
	}
	buckets := make([]Bucket, count)
	curr := start
	for i := range buckets {
		buckets[i] = DurationBucket(curr)
		curr = time.Duration(float64(curr) * factor)
	}
	return buckets
}
