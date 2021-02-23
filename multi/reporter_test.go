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

package multi

import (
	"testing"
	"time"

	"github.com/uber-go/tally"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiReporter(t *testing.T) {
	a, b, c :=
		newCapturingStatsReporter(),
		newCapturingStatsReporter(),
		newCapturingStatsReporter()
	all := []*capturingStatsReporter{a, b, c}

	r := NewMultiReporter(a, b, c)

	tags := []map[string]string{
		{"foo": "bar"},
		{"foo": "baz"},
		{"foo": "qux"},
		{"foo": "bzz"},
		{"foo": "buz"},
	}

	valueBuckets := tally.MustMakeLinearValueBuckets(0, 2, 5)
	durationBuckets := tally.MustMakeLinearDurationBuckets(0, 2*time.Second, 5)

	r.ReportCounter("foo", tags[0], 42)
	r.ReportCounter("foo", tags[0], 84)
	r.ReportGauge("baz", tags[1], 42.0)
	r.ReportTimer("qux", tags[2], 126*time.Millisecond)
	r.ReportHistogramValueSamples("bzz", tags[3], valueBuckets,
		2.0, 4.0, 3)
	r.ReportHistogramDurationSamples("buz", tags[4], durationBuckets,
		2*time.Second, 4*time.Second, 3)
	for _, r := range all {
		require.Equal(t, 2, len(r.counts))

		assert.Equal(t, "foo", r.counts[0].name)
		assert.Equal(t, tags[0], r.counts[0].tags)
		assert.Equal(t, int64(42), r.counts[0].value)

		assert.Equal(t, "foo", r.counts[1].name)
		assert.Equal(t, tags[0], r.counts[1].tags)
		assert.Equal(t, int64(84), r.counts[1].value)

		assert.Equal(t, "baz", r.gauges[0].name)
		assert.Equal(t, tags[1], r.gauges[0].tags)
		assert.Equal(t, float64(42.0), r.gauges[0].value)

		assert.Equal(t, "qux", r.timers[0].name)
		assert.Equal(t, tags[2], r.timers[0].tags)
		assert.Equal(t, 126*time.Millisecond, r.timers[0].value)

		assert.Equal(t, "bzz", r.histogramValueSamples[0].name)
		assert.Equal(t, tags[3], r.histogramValueSamples[0].tags)
		assert.Equal(t, 2.0, r.histogramValueSamples[0].bucketLowerBound)
		assert.Equal(t, 4.0, r.histogramValueSamples[0].bucketUpperBound)
		assert.Equal(t, int64(3), r.histogramValueSamples[0].samples)

		assert.Equal(t, "buz", r.histogramDurationSamples[0].name)
		assert.Equal(t, tags[4], r.histogramDurationSamples[0].tags)
		assert.Equal(t, 2*time.Second, r.histogramDurationSamples[0].bucketLowerBound)
		assert.Equal(t, 4*time.Second, r.histogramDurationSamples[0].bucketUpperBound)
		assert.Equal(t, int64(3), r.histogramDurationSamples[0].samples)
	}

	assert.NotNil(t, r.Capabilities())

	r.Flush()
	for _, r := range all {
		assert.Equal(t, 1, r.flush)
	}
}

func TestMultiCachedReporter(t *testing.T) {
	a, b, c :=
		newCapturingStatsReporter(),
		newCapturingStatsReporter(),
		newCapturingStatsReporter()
	all := []*capturingStatsReporter{a, b, c}

	r := NewMultiCachedReporter(a, b, c)

	tags := []map[string]string{
		{"foo": "bar"},
		{"foo": "baz"},
		{"foo": "qux"},
		{"foo": "bzz"},
		{"foo": "buz"},
	}

	valueBuckets := tally.MustMakeLinearValueBuckets(0, 2, 5)
	durationBuckets := tally.MustMakeLinearDurationBuckets(0, 2*time.Second, 5)

	ctr := r.AllocateCounter("foo", tags[0])
	ctr.ReportCount(42)
	ctr.ReportCount(84)

	gauge := r.AllocateGauge("baz", tags[1])
	gauge.ReportGauge(42.0)

	tmr := r.AllocateTimer("qux", tags[2])
	tmr.ReportTimer(126 * time.Millisecond)

	vhist := r.AllocateHistogram("bzz", tags[3], valueBuckets)
	vhist.ValueBucket(2.0, 4.0).ReportSamples(3)

	dhist := r.AllocateHistogram("buz", tags[4], durationBuckets)
	dhist.DurationBucket(2*time.Second, 4*time.Second).ReportSamples(3)

	for _, r := range all {
		require.Equal(t, 2, len(r.counts))

		assert.Equal(t, "foo", r.counts[0].name)
		assert.Equal(t, tags[0], r.counts[0].tags)
		assert.Equal(t, int64(42), r.counts[0].value)

		assert.Equal(t, "foo", r.counts[1].name)
		assert.Equal(t, tags[0], r.counts[1].tags)
		assert.Equal(t, int64(84), r.counts[1].value)

		assert.Equal(t, "baz", r.gauges[0].name)
		assert.Equal(t, tags[1], r.gauges[0].tags)
		assert.Equal(t, float64(42.0), r.gauges[0].value)

		assert.Equal(t, "qux", r.timers[0].name)
		assert.Equal(t, tags[2], r.timers[0].tags)
		assert.Equal(t, 126*time.Millisecond, r.timers[0].value)

		assert.Equal(t, "bzz", r.histogramValueSamples[0].name)
		assert.Equal(t, tags[3], r.histogramValueSamples[0].tags)
		assert.Equal(t, 2.0, r.histogramValueSamples[0].bucketLowerBound)
		assert.Equal(t, 4.0, r.histogramValueSamples[0].bucketUpperBound)
		assert.Equal(t, int64(3), r.histogramValueSamples[0].samples)

		assert.Equal(t, "buz", r.histogramDurationSamples[0].name)
		assert.Equal(t, tags[4], r.histogramDurationSamples[0].tags)
		assert.Equal(t, 2*time.Second, r.histogramDurationSamples[0].bucketLowerBound)
		assert.Equal(t, 4*time.Second, r.histogramDurationSamples[0].bucketUpperBound)
		assert.Equal(t, int64(3), r.histogramDurationSamples[0].samples)
	}

	assert.NotNil(t, r.Capabilities())

	r.Flush()
	for _, r := range all {
		assert.Equal(t, 1, r.flush)
	}
}

type capturingStatsReporter struct {
	counts                   []capturedCount
	gauges                   []capturedGauge
	timers                   []capturedTimer
	histogramValueSamples    []capturedHistogramValueSamples
	histogramDurationSamples []capturedHistogramDurationSamples
	capabilities             int
	flush                    int
}

type capturedCount struct {
	name  string
	tags  map[string]string
	value int64
}

type capturedGauge struct {
	name  string
	tags  map[string]string
	value float64
}

type capturedTimer struct {
	name  string
	tags  map[string]string
	value time.Duration
}

type capturedHistogramValueSamples struct {
	name             string
	tags             map[string]string
	bucketLowerBound float64
	bucketUpperBound float64
	samples          int64
}

type capturedHistogramDurationSamples struct {
	name             string
	tags             map[string]string
	bucketLowerBound time.Duration
	bucketUpperBound time.Duration
	samples          int64
}

func newCapturingStatsReporter() *capturingStatsReporter {
	return &capturingStatsReporter{}
}

func (r *capturingStatsReporter) ReportCounter(
	name string,
	tags map[string]string,
	value int64,
) {
	r.counts = append(r.counts, capturedCount{name, tags, value})
}

func (r *capturingStatsReporter) ReportGauge(
	name string,
	tags map[string]string,
	value float64,
) {
	r.gauges = append(r.gauges, capturedGauge{name, tags, value})
}

func (r *capturingStatsReporter) ReportTimer(
	name string,
	tags map[string]string,
	value time.Duration,
) {
	r.timers = append(r.timers, capturedTimer{name, tags, value})
}

func (r *capturingStatsReporter) ReportHistogramValueSamples(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
	bucketLowerBound,
	bucketUpperBound float64,
	samples int64,
) {
	elem := capturedHistogramValueSamples{name, tags,
		bucketLowerBound, bucketUpperBound, samples}
	r.histogramValueSamples = append(r.histogramValueSamples, elem)
}

func (r *capturingStatsReporter) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
	bucketLowerBound,
	bucketUpperBound time.Duration,
	samples int64,
) {
	elem := capturedHistogramDurationSamples{name, tags,
		bucketLowerBound, bucketUpperBound, samples}
	r.histogramDurationSamples = append(r.histogramDurationSamples, elem)
}

func (r *capturingStatsReporter) AllocateCounter(
	name string,
	tags map[string]string,
) tally.CachedCount {
	return cachedCount{fn: func(value int64) {
		r.counts = append(r.counts, capturedCount{name, tags, value})
	}}
}

func (r *capturingStatsReporter) DeallocateCounter(tally.CachedCount) {}

func (r *capturingStatsReporter) AllocateGauge(
	name string,
	tags map[string]string,
) tally.CachedGauge {
	return cachedGauge{fn: func(value float64) {
		r.gauges = append(r.gauges, capturedGauge{name, tags, value})
	}}
}

func (r *capturingStatsReporter) DeallocateGauge(tally.CachedGauge) {}

func (r *capturingStatsReporter) AllocateTimer(
	name string,
	tags map[string]string,
) tally.CachedTimer {
	return cachedTimer{fn: func(value time.Duration) {
		r.timers = append(r.timers, capturedTimer{name, tags, value})
	}}
}

func (r *capturingStatsReporter) DeallocateTimer(tally.CachedTimer) {}

func (r *capturingStatsReporter) AllocateHistogram(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
) tally.CachedHistogram {
	return cachedHistogram{
		valueFn: func(bucketLowerBound, bucketUpperBound float64, samples int64) {
			elem := capturedHistogramValueSamples{
				name, tags, bucketLowerBound, bucketUpperBound, samples,
			}
			r.histogramValueSamples = append(r.histogramValueSamples, elem)
		},
		durationFn: func(bucketLowerBound, bucketUpperBound time.Duration, samples int64) {
			elem := capturedHistogramDurationSamples{
				name, tags, bucketLowerBound, bucketUpperBound, samples,
			}
			r.histogramDurationSamples = append(r.histogramDurationSamples, elem)
		},
	}
}

func (r *capturingStatsReporter) DeallocateHistogram(tally.CachedHistogram) {}

func (r *capturingStatsReporter) Capabilities() tally.Capabilities {
	r.capabilities++
	return r
}

func (r *capturingStatsReporter) Reporting() bool {
	return true
}

func (r *capturingStatsReporter) Tagging() bool {
	return true
}

func (r *capturingStatsReporter) Flush() {
	r.flush++
}

type cachedCount struct {
	fn func(value int64)
}

func (c cachedCount) ReportCount(value int64) {
	c.fn(value)
}

type cachedGauge struct {
	fn func(value float64)
}

func (c cachedGauge) ReportGauge(value float64) {
	c.fn(value)
}

type cachedTimer struct {
	fn func(value time.Duration)
}

func (c cachedTimer) ReportTimer(value time.Duration) {
	c.fn(value)
}

type cachedHistogram struct {
	valueFn    func(bucketLowerBound, bucketUpperBound float64, samples int64)
	durationFn func(bucketLowerBound, bucketUpperBound time.Duration, samples int64)
}

func (h cachedHistogram) ValueBucket(
	bucketLowerBound, bucketUpperBound float64,
) tally.CachedHistogramBucket {
	return cachedHistogramValueBucket{&h, bucketLowerBound, bucketUpperBound}
}

func (h cachedHistogram) DurationBucket(
	bucketLowerBound, bucketUpperBound time.Duration,
) tally.CachedHistogramBucket {
	return cachedHistogramDurationBucket{&h, bucketLowerBound, bucketUpperBound}
}

type cachedHistogramValueBucket struct {
	histogram        *cachedHistogram
	bucketLowerBound float64
	bucketUpperBound float64
}

func (b cachedHistogramValueBucket) ReportSamples(v int64) {
	b.histogram.valueFn(b.bucketLowerBound, b.bucketUpperBound, v)
}

type cachedHistogramDurationBucket struct {
	histogram        *cachedHistogram
	bucketLowerBound time.Duration
	bucketUpperBound time.Duration
}

func (b cachedHistogramDurationBucket) ReportSamples(v int64) {
	b.histogram.durationFn(b.bucketLowerBound, b.bucketUpperBound, v)
}
