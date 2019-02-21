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
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type statsTestReporter struct {
	last            interface{}
	valueSamples    map[float64]int
	durationSamples map[time.Duration]int
	buckets         Buckets
}

func newStatsTestReporter() *statsTestReporter {
	return &statsTestReporter{
		valueSamples:    make(map[float64]int),
		durationSamples: make(map[time.Duration]int),
	}
}

func (r *statsTestReporter) ReportCounter(name string, tags map[string]string, value int64) {
	r.last = value
}

func (r *statsTestReporter) ReportGauge(name string, tags map[string]string, value float64) {
	r.last = value
}

func (r *statsTestReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	r.last = interval
}

func (r *statsTestReporter) ReportHistogramValueSamples(
	name string,
	tags map[string]string,
	buckets Buckets,
	bucketLowerBound,
	bucketUpperBound float64,
	samples int64,
) {
	r.valueSamples[bucketUpperBound] = int(samples)
	r.buckets = buckets
}
func (r *statsTestReporter) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	buckets Buckets,
	bucketLowerBound,
	bucketUpperBound time.Duration,
	samples int64,
) {
	r.durationSamples[bucketUpperBound] = int(samples)
	r.buckets = buckets
}

func (r *statsTestReporter) Capabilities() Capabilities {
	return capabilitiesReportingNoTagging
}

func (r *statsTestReporter) Flush() {}

func TestCounter(t *testing.T) {
	counter := newCounter(nil)
	r := newStatsTestReporter()

	counter.Inc(1)
	counter.report("", nil, r)
	assert.Equal(t, int64(1), r.last)

	counter.Inc(1)
	counter.report("", nil, r)
	assert.Equal(t, int64(1), r.last)

	counter.Inc(1)
	counter.report("", nil, r)
	assert.Equal(t, int64(1), r.last)
}

func TestGauge(t *testing.T) {
	gauge := newGauge(nil)
	r := newStatsTestReporter()

	gauge.Update(42)
	gauge.report("", nil, r)
	assert.Equal(t, float64(42), r.last)

	gauge.Update(1234)
	gauge.Update(5678)
	gauge.report("", nil, r)
	assert.Equal(t, float64(5678), r.last)
}

func TestTimer(t *testing.T) {
	r := newStatsTestReporter()
	timer := newTimer("t1", nil, r, nil)

	timer.Record(42 * time.Millisecond)
	assert.Equal(t, 42*time.Millisecond, r.last)

	timer.Record(128 * time.Millisecond)
	assert.Equal(t, 128*time.Millisecond, r.last)
}

func TestHistogramValueSamples(t *testing.T) {
	r := newStatsTestReporter()
	buckets := MustMakeLinearValueBuckets(0, 10, 10)
	h := newHistogram("h1", nil, r, buckets, nil)

	var offset float64
	for i := 0; i < 3; i++ {
		h.RecordValue(offset + rand.Float64()*10)
	}
	offset = 50
	for i := 0; i < 5; i++ {
		h.RecordValue(offset + rand.Float64()*10)
	}

	h.report(h.name, h.tags, r)

	assert.Equal(t, 3, r.valueSamples[10.0])
	assert.Equal(t, 5, r.valueSamples[60.0])
	assert.Equal(t, buckets, r.buckets)
}

func TestHistogramDurationSamples(t *testing.T) {
	r := newStatsTestReporter()
	buckets := MustMakeLinearDurationBuckets(0, 10*time.Millisecond, 10)
	h := newHistogram("h1", nil, r, buckets, nil)

	var offset time.Duration
	for i := 0; i < 3; i++ {
		h.RecordDuration(offset +
			time.Duration(rand.Float64()*float64(10*time.Millisecond)))
	}
	offset = 50 * time.Millisecond
	for i := 0; i < 5; i++ {
		h.RecordDuration(offset +
			time.Duration(rand.Float64()*float64(10*time.Millisecond)))
	}

	h.report(h.name, h.tags, r)

	assert.Equal(t, 3, r.durationSamples[10*time.Millisecond])
	assert.Equal(t, 5, r.durationSamples[60*time.Millisecond])
	assert.Equal(t, buckets, r.buckets)
}
