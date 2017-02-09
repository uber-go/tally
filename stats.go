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
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var (
	capabilitiesNone = &capabilities{
		reporting:  false,
		tagging:    false,
		histograms: false,
	}
	capabilitiesReportingNoTaggingNoHistograms = &capabilities{
		reporting:  true,
		tagging:    false,
		histograms: false,
	}
	capabilitiesReportingTaggingNoHistograms = &capabilities{
		reporting:  true,
		tagging:    true,
		histograms: false,
	}
	capabilitiesReportingTaggingHistograms = &capabilities{
		reporting:  true,
		tagging:    true,
		histograms: true,
	}
)

type capabilities struct {
	reporting  bool
	tagging    bool
	histograms bool
}

func (c *capabilities) Reporting() bool {
	return c.reporting
}

func (c *capabilities) Tagging() bool {
	return c.tagging
}

func (c *capabilities) Histograms() bool {
	return c.histograms
}

type counter struct {
	prev        int64
	curr        int64
	cachedCount CachedCount
}

func newCounter(cachedCount CachedCount) *counter {
	return &counter{cachedCount: cachedCount}
}

func (c *counter) Inc(v int64) {
	atomic.AddInt64(&c.curr, v)
}

func (c *counter) value() int64 {
	curr := atomic.LoadInt64(&c.curr)

	prev := atomic.LoadInt64(&c.prev)
	if prev == curr {
		return 0
	}
	atomic.StoreInt64(&c.prev, curr)
	return curr - prev
}

func (c *counter) report(name string, tags map[string]string, r StatsReporter) {
	delta := c.value()
	if delta == 0 {
		return
	}

	r.ReportCounter(name, tags, delta)
}

func (c *counter) cachedReport() {
	delta := c.value()
	if delta == 0 {
		return
	}

	c.cachedCount.ReportCount(delta)
}

func (c *counter) snapshot() int64 {
	return atomic.LoadInt64(&c.curr) - atomic.LoadInt64(&c.prev)
}

type gauge struct {
	updated     uint64
	curr        uint64
	cachedGauge CachedGauge
}

func newGauge(cachedGauge CachedGauge) *gauge {
	return &gauge{cachedGauge: cachedGauge}
}

func (g *gauge) Update(v float64) {
	atomic.StoreUint64(&g.curr, math.Float64bits(v))
	atomic.StoreUint64(&g.updated, 1)
}

func (g *gauge) value() float64 {
	return math.Float64frombits(atomic.LoadUint64(&g.curr))
}

func (g *gauge) report(name string, tags map[string]string, r StatsReporter) {
	if atomic.SwapUint64(&g.updated, 0) == 1 {
		r.ReportGauge(name, tags, g.value())
	}
}

func (g *gauge) cachedReport() {
	if atomic.SwapUint64(&g.updated, 0) == 1 {
		g.cachedGauge.ReportGauge(g.value())
	}
}

func (g *gauge) snapshot() float64 {
	return math.Float64frombits(atomic.LoadUint64(&g.curr))
}

// NB(jra3): timers are a little special because they do no aggregate any data
// at the timer level. The reporter buffers may timer entries and periodically
// flushes.
type timer struct {
	name        string
	tags        map[string]string
	reporter    StatsReporter
	cachedTimer CachedTimer
	unreported  timerValues
}

type timerValues struct {
	sync.RWMutex
	values []time.Duration
}

func newTimer(
	name string,
	tags map[string]string,
	r StatsReporter,
	cachedTimer CachedTimer,
) *timer {
	t := &timer{
		name:        name,
		tags:        tags,
		reporter:    r,
		cachedTimer: cachedTimer,
	}
	if r == nil {
		t.reporter = &timerNoReporterSink{timer: t}
	}
	return t
}

func (t *timer) Start() Stopwatch {
	return timerStopwatch{start: globalClock.Now(), timer: t}
}

func (t *timer) Record(interval time.Duration) {
	if t.cachedTimer != nil {
		t.cachedTimer.ReportTimer(interval)
	} else {
		t.reporter.ReportTimer(t.name, t.tags, interval)
	}
}

func (t *timer) snapshot() []time.Duration {
	t.unreported.RLock()
	snap := make([]time.Duration, len(t.unreported.values))
	for i := range t.unreported.values {
		snap[i] = t.unreported.values[i]
	}
	t.unreported.RUnlock()
	return snap
}

type timerStopwatch struct {
	start   time.Time
	timer   *timer
	stopped int32
}

func (s timerStopwatch) Stop() {
	if atomic.AddInt32(&s.stopped, 1) == 1 {
		d := globalClock.Now().Sub(s.start)
		s.timer.Record(d)
	}
}

type timerNoReporterSink struct {
	sync.RWMutex
	timer *timer
}

func (r *timerNoReporterSink) ReportCounter(
	name string,
	tags map[string]string,
	value int64,
) {
}

func (r *timerNoReporterSink) ReportGauge(
	name string,
	tags map[string]string,
	value float64,
) {
}

func (r *timerNoReporterSink) ReportTimer(
	name string,
	tags map[string]string,
	interval time.Duration,
) {
	r.timer.unreported.Lock()
	r.timer.unreported.values = append(r.timer.unreported.values, interval)
	r.timer.unreported.Unlock()
}

func (r *timerNoReporterSink) ReportHistogramValueSamples(
	name string,
	tags map[string]string,
	buckets Buckets,
	bucketLowerBound,
	bucketUpperBound float64,
	samples int64,
) {
}

func (r *timerNoReporterSink) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	buckets Buckets,
	bucketLowerBound,
	bucketUpperBound time.Duration,
	samples int64,
) {
}

func (r *timerNoReporterSink) Capabilities() Capabilities {
	return capabilitiesReportingTaggingNoHistograms
}

func (r *timerNoReporterSink) Flush() {
}

type histogram struct {
	htype            uint32
	name             string
	tags             map[string]string
	reporter         StatsReporter
	specification    Buckets
	buckets          []histogramBucket
	lookupByValue    []float64
	lookupByDuration []int
}

const (
	undeclaredHistogramType = uint32(0)
	valueHistogramType      = uint32(1)
	durationHistogramType   = uint32(2)
)

func newHistogram(
	name string,
	tags map[string]string,
	reporter StatsReporter,
	buckets Buckets,
	cachedHistogram CachedHistogram,
) *histogram {
	h := &histogram{
		name:          name,
		tags:          tags,
		reporter:      reporter,
		specification: buckets,
	}

	var (
		asValueBuckets    = buckets.AsValues()
		asDurationBuckets = buckets.AsDurations()
	)
	if buckets.Len() > 0 {
		h.addBucket(newHistogramBucket(h,
			-math.MaxFloat64, asValueBuckets[0],
			time.Duration(math.MinInt64), asDurationBuckets[0],
			cachedHistogram))

		prevValueBucket, prevDurationBucket :=
			asValueBuckets[0], asDurationBuckets[0]
		for i := 1; i < buckets.Len(); i++ {
			h.addBucket(newHistogramBucket(h,
				prevValueBucket, asValueBuckets[i],
				prevDurationBucket, asDurationBuckets[i],
				cachedHistogram))
			prevValueBucket, prevDurationBucket =
				asValueBuckets[i], asDurationBuckets[i]
		}

		h.addBucket(newHistogramBucket(h,
			prevValueBucket, math.MaxFloat64,
			prevDurationBucket, time.Duration(math.MaxInt64),
			cachedHistogram))
	} else {
		h.addBucket(newHistogramBucket(h,
			-math.MaxFloat64, math.MaxFloat64,
			time.Duration(math.MinInt64), time.Duration(math.MaxInt64),
			cachedHistogram))
	}

	return h
}

func (h *histogram) addBucket(b histogramBucket) {
	h.buckets = append(h.buckets, b)
	h.lookupByValue = append(h.lookupByValue, b.valueUpperBound)
	h.lookupByDuration = append(h.lookupByDuration, int(b.durationUpperBound))
}

func (h *histogram) report(name string, tags map[string]string, r StatsReporter) {
	htype := atomic.LoadUint32(&h.htype)
	for i := range h.buckets {
		samples := h.buckets[i].samples.value()
		if samples == 0 {
			continue
		}
		switch htype {
		case valueHistogramType:
			r.ReportHistogramValueSamples(name, tags, h.specification,
				h.buckets[i].valueLowerBound, h.buckets[i].valueUpperBound,
				samples)
		case durationHistogramType:
			r.ReportHistogramDurationSamples(name, tags, h.specification,
				h.buckets[i].durationLowerBound, h.buckets[i].durationUpperBound,
				samples)
		}
	}
}

func (h *histogram) cachedReport() {
	htype := atomic.LoadUint32(&h.htype)
	for i := range h.buckets {
		samples := h.buckets[i].samples.value()
		if samples == 0 {
			continue
		}
		switch htype {
		case valueHistogramType:
			h.buckets[i].cachedValueBucket.ReportSamples(samples)
		case durationHistogramType:
			h.buckets[i].cachedDurationBucket.ReportSamples(samples)
		}
	}
}

func (h *histogram) RecordValue(value float64) {
	idx := sort.SearchFloat64s(h.lookupByValue, value)
	h.buckets[idx].samples.Inc(1)
	h.declare(valueHistogramType)
}

func (h *histogram) RecordDuration(value time.Duration) {
	idx := sort.SearchInts(h.lookupByDuration, int(value))
	h.buckets[idx].samples.Inc(1)
	h.declare(durationHistogramType)
}

func (h *histogram) declare(htype uint32) {
	if atomic.LoadUint32(&h.htype) == undeclaredHistogramType {
		atomic.StoreUint32(&h.htype, htype) // First declaration wins
	}
}

func (h *histogram) Start() Stopwatch {
	return histogramStopwatch{start: globalClock.Now(), histogram: h}
}

type histogramBucket struct {
	h                    *histogram
	samples              *counter
	valueLowerBound      float64
	valueUpperBound      float64
	durationLowerBound   time.Duration
	durationUpperBound   time.Duration
	cachedValueBucket    CachedHistogramBucket
	cachedDurationBucket CachedHistogramBucket
}

func newHistogramBucket(
	h *histogram,
	valueLowerBound,
	valueUpperBound float64,
	durationLowerBound,
	durationUpperBound time.Duration,
	cachedHistogram CachedHistogram,
) histogramBucket {
	bucket := histogramBucket{
		samples:            newCounter(nil),
		valueLowerBound:    valueLowerBound,
		valueUpperBound:    valueUpperBound,
		durationLowerBound: durationLowerBound,
		durationUpperBound: durationUpperBound,
	}
	if cachedHistogram != nil {
		bucket.cachedValueBucket = cachedHistogram.ValueBucket(
			bucket.valueLowerBound, bucket.valueUpperBound,
		)
		bucket.cachedDurationBucket = cachedHistogram.DurationBucket(
			bucket.durationLowerBound, bucket.durationUpperBound,
		)
	}
	return bucket
}

type histogramStopwatch struct {
	start     time.Time
	histogram *histogram
	stopped   int32
}

func (s histogramStopwatch) Stop() {
	if atomic.AddInt32(&s.stopped, 1) == 1 {
		d := globalClock.Now().Sub(s.start)
		s.histogram.RecordDuration(d)
	}
}

// NullStatsReporter is an implementation of StatsReporter than simply does nothing.
var NullStatsReporter StatsReporter = nullStatsReporter{}

func (r nullStatsReporter) ReportCounter(name string, tags map[string]string, value int64) {
}
func (r nullStatsReporter) ReportGauge(name string, tags map[string]string, value float64) {
}
func (r nullStatsReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
}
func (r nullStatsReporter) ReportHistogramValueSamples(
	name string,
	tags map[string]string,
	buckets Buckets,
	bucketLowerBound,
	bucketUpperBound float64,
	samples int64,
) {
}

func (r nullStatsReporter) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	buckets Buckets,
	bucketLowerBound,
	bucketUpperBound time.Duration,
	samples int64,
) {
}
func (r nullStatsReporter) Capabilities() Capabilities {
	return capabilitiesNone
}
func (r nullStatsReporter) Flush() {
}

type nullStatsReporter struct{}
