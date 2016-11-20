// Copyright (c) 2016 Uber Technologies, Inc.
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

import "time"

// Scope is a namespace wrapper around a stats reporter, ensuring that
// all emitted values have a given prefix or set of tags
type Scope interface {
	// Counter returns the Counter object corresponding to the name
	Counter(name string) Counter

	// Gauge returns the Gauge object corresponding to the name
	Gauge(name string) Gauge

	// Timer returns the Timer object corresponding to the name
	Timer(name string) Timer

	// Tagged returns a new scope with the given tags
	Tagged(tags map[string]string) Scope

	// SubScope returns a new child scope with the given name
	SubScope(name string) Scope

	// Capabilities returns a description of metrics reporting capabilities
	Capabilities() Capabilities
}

// Counter is the interface for logging statsd-counter-type metrics
type Counter interface {
	// Inc increments the counter by a delta
	Inc(delta int64)
}

// Gauge is the interface for logging statsd-gauge-type metrics
type Gauge interface {
	// Update sets the gauges absolute value
	Update(value float64)
}

// Timer is the interface for logging statsd-timer-type metrics
type Timer interface {
	// Record Record a specific duration directly
	Record(time.Duration)

	// Start gives you back a specific point in time to report via Stop()
	Start() StopwatchStart

	// Stop records the difference between the current clock and startTime
	Stop(startTime StopwatchStart)
}

// StopwatchStart is returned by a timer's start method, and should be passed
// back to the timer's stop method at the end of the interval
type StopwatchStart time.Time

// Capabilities is a description of metrics reporting capabilities
type Capabilities interface {
	// Reporting returns whether the reporter has the ability to actively report
	Reporting() bool

	// Tagging returns whether the reporter has the capability for tagged metrics
	Tagging() bool
}
