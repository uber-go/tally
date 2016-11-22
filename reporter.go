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

// StatsReporter is a backend for Scopes to report metrics to
type StatsReporter interface {
	// ReportCounter reports a counter value
	ReportCounter(name string, tags map[string]string, value int64)

	// ReportGauge reports a gauge value
	ReportGauge(name string, tags map[string]string, value float64)

	// ReportHistogram reports a histogram value
	ReportHistogram(name string, tags map[string]string, value float64)

	// ReportTimer reports a timer value
	ReportTimer(name string, tags map[string]string, interval time.Duration)

	// Capabilities returns a description of metrics reporting capabilities
	Capabilities() Capabilities

	// Flush is expected to be called by a Scope when it completes a round or reporting
	Flush()
}
