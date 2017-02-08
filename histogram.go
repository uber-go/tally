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

// Defaults is a type that can represent a constant.
type Defaults int

// DefaultBuckets can be passed to specify to default buckets.
const DefaultBuckets = Defaults(0)

func (v Defaults) String() string {
	return "[defaults]"
}

// AsValues implements Buckets.
func (v Defaults) AsValues() []float64 { return nil }

// AsDurations implements Buckets.
func (v Defaults) AsDurations() []time.Duration { return nil }

// Values is a set of float64 values that implements Buckets.
type Values []float64

func (v Values) String() string {
	values := make([]string, len(v))
	for i := range values {
		values[i] = fmt.Sprintf("%f", v[i])
	}
	return fmt.Sprintf("%v", values)
}

// AsValues implements Buckets.
func (v Values) AsValues() []float64 {
	return []float64(v)
}

// AsDurations implements Buckets.
func (v Values) AsDurations() []time.Duration {
	values := make([]time.Duration, len(v))
	for i := range values {
		values[i] = time.Duration(v[i] * float64(time.Second))
	}
	return values
}

// Durations is a set of float64 values that implements Buckets.
type Durations []time.Duration

func (v Durations) String() string {
	values := make([]string, len(v))
	for i := range values {
		values[i] = v[i].String()
	}
	return fmt.Sprintf("%v", values)
}

// AsValues implements Buckets.
func (v Durations) AsValues() []float64 {
	values := make([]float64, len(v))
	for i := range values {
		values[i] = float64(v[i]) / float64(time.Second)
	}
	return values
}

// AsDurations implements Buckets.
func (v Durations) AsDurations() []time.Duration {
	return []time.Duration(v)
}

// LinearValueBuckets creates a set of linear value buckets.
func LinearValueBuckets(start, width float64, count int) Values {
	if count <= 0 {
		panic("count needs to be > 0")
	}
	buckets := make([]float64, count)
	for i := range buckets {
		buckets[i] = start + (float64(i) * width)
	}
	return Values(buckets)
}

// LinearDurationBuckets creates a set of linear duration buckets.
func LinearDurationBuckets(start, width time.Duration, count int) Durations {
	if count <= 0 {
		panic("count needs to be > 0")
	}
	buckets := make([]time.Duration, count)
	for i := range buckets {
		buckets[i] = start + (time.Duration(i) * width)
	}
	return Durations(buckets)
}

// ExponentialValueBuckets creates a set of exponential value buckets.
func ExponentialValueBuckets(start, factor float64, count int) Values {
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
	return Values(buckets)
}

// ExponentialDurationBuckets creates a set of exponential duration buckets.
func ExponentialDurationBuckets(start time.Duration, factor float64, count int) Durations {
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
	return Durations(buckets)
}
