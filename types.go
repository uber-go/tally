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

// Scope is a namespace wrapper around a stats reporter, ensuring that
// all emitted values have a given prefix or set of tags.
type Scope interface {
	// Counter returns the Counter object corresponding to the name.
	Counter(name string) Counter

	// Gauge returns the Gauge object corresponding to the name.
	Gauge(name string) Gauge

	// Timer returns the Timer object corresponding to the name.
	Timer(name string) Timer

	// Histogram returns the Histogram object corresponding to the name.
	// To use default value and duration buckets configured for the scope
	// simply do not pass any buckets.
	// You can use tally.ValueBucket(x) for individual value buckets.
	// You can use tally.DurationBucket(x) for individual duration buckets.
	// You can use tally.ValueBuckets(x, y, ...)... for value buckets.
	// You can use tally.DurationBuckets(x, y, ...)... for duration buckets.
	// You can use tally.LinearValueBuckets(start, width, count)... for linear values.
	// You can use tally.LinearDurationBuckets(start, width, count)... for linear durations.
	// You can use tally.ExponentialValueBuckets(start, factor, count)... for exponential values.
	// You can use tally.ExponentialDurationBuckets(start, factor, count)... for exponential durations.
	Histogram(name string, buckets ...Bucket) Histogram

	// Tagged returns a new child scope with the given tags and current tags.
	Tagged(tags map[string]string) Scope

	// SubScope returns a new child scope appending a further name prefix.
	SubScope(name string) Scope

	// Capabilities returns a description of metrics reporting capabilities.
	Capabilities() Capabilities
}

// Counter is the interface for emitting counter type metrics.
type Counter interface {
	// Inc increments the counter by a delta.
	Inc(delta int64)
}

// Gauge is the interface for emitting gauge metrics.
type Gauge interface {
	// Update sets the gauges absolute value.
	Update(value float64)
}

// Timer is the interface for emitting timer metrics.
type Timer interface {
	// Record a specific duration directly.
	Record(value time.Duration)

	// Start gives you back a specific point in time to report via Stop.
	Start() Stopwatch
}

// Histogram is the interface for emitting histogram metrics
type Histogram interface {
	// RecordValue records a specific value directly.
	// Will use the configured value buckets for the histogram.
	RecordValue(value float64)

	// RecordDuration records a specific duration directly.
	// Will use the configured duration buckets for the histogram.
	RecordDuration(value time.Duration)

	// Start gives you a specific point in time to then record a duration.
	// Will use the configured duration buckets for the histogram.
	Start() Stopwatch
}

// BucketType represents a histogram bucket type.
type BucketType uint

const (
	// ValueBucketType is a float64 histogram bucket type.
	ValueBucketType BucketType = iota
	// DurationBucketType is a time duration histogram bucket type.
	DurationBucketType
)

// Bucket represents a histgram bucket.
type Bucket interface {
	fmt.Stringer

	// BucketType returns the type of bucket, value or duration.
	BucketType() BucketType

	// Value returns the value of the bucket as float64.
	Value() float64

	// Duration returns the duration of the bucket as time.Duration.
	Duration() time.Duration
}

// Stopwatch is a helper for simpler tracking of elapsed time.
type Stopwatch interface {
	// Stop records the difference between the current clock and start time.
	Stop()
}

// Capabilities is a description of metrics reporting capabilities.
type Capabilities interface {
	// Reporting returns whether the reporter has the ability to actively report.
	Reporting() bool

	// Tagging returns whether the reporter has the capability for tagged metrics.
	Tagging() bool

	// Histograms returns whether the reporter has the capability for histogram metrics.
	Histograms() bool
}
