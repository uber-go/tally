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
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var (
	capabilitiesNone = &capabilities{
		reporting: false,
		tagging:   false,
	}
	capabilitiesReportingNoTagging = &capabilities{
		reporting: true,
		tagging:   false,
	}
	capabilitiesReportingTagging = &capabilities{
		reporting: true,
		tagging:   true,
	}
)

type capabilities struct {
	reporting bool
	tagging   bool
}

func (c *capabilities) Reporting() bool {
	return c.reporting
}

func (c *capabilities) Tagging() bool {
	return c.tagging
}

type counter struct {
	prev           int64
	curr           int64
	cachedCount    CachedCount
	lastUpdateUnix int64
	expired        uint32
	scope          *scope
	name           string
}

func newCounter(cachedCount CachedCount, name string, scope *scope) *counter {
	return &counter{
		cachedCount:    cachedCount,
		scope:          scope,
		name:           name,
		lastUpdateUnix: globalNow().Unix(),
	}
}

func (c *counter) Inc(v int64) {
	atomic.AddInt64(&c.curr, v)

	if atomic.LoadUint32(&c.expired) == 1 && c.scope != nil {
		if !atomic.CompareAndSwapUint32(&c.expired, 1, 0) {
			// Another routine got here first
			return
		}
		// Counter has expired, but direct ref was held and counter
		// was incremented, therefore insert back into scope
		scope := c.scope.getCurrentScope()

		scope.cm.Lock()
		if _, ok := scope.counters[c.name]; !ok {
			// No counters with the same name were created since expiry
			scope.counters[c.name] = []*counter{c}
		} else {
			// Another counter with the same name was created
			// and this expired counter is not in the list so add to slice
			scope.counters[c.name] = append(scope.counters[c.name], c)
		}

		scope.cm.Unlock()
	}
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

func (c *counter) report(name string, tags map[string]string, r StatsReporter) bool {
	delta := c.value()
	if delta != 0 {
		r.ReportCounter(name, tags, delta)
		atomic.StoreInt64(&c.lastUpdateUnix, globalNow().Unix())

		return false
	}

	// Check if counter has expired
	if c.scope != nil && globalNow().Unix() >
		atomic.LoadInt64(&c.lastUpdateUnix)+c.scope.registry.expirePeriodSeconds {
		atomic.StoreUint32(&c.expired, 1)
		// Check once more for possible race where value updated between
		// beginning of this call
		delta = c.value()
		if delta != 0 {
			r.ReportCounter(name, tags, delta)
			atomic.StoreInt64(&c.lastUpdateUnix, globalNow().Unix())
			atomic.StoreUint32(&c.expired, 0)

			return false
		}

		return true
	}

	return false
}

func (c *counter) cachedReport() bool {
	delta := c.value()
	if delta != 0 {
		c.cachedCount.ReportCount(delta)
		atomic.StoreInt64(&c.lastUpdateUnix, globalNow().Unix())
		return false
	}

	// Check if counter has expired
	if c.scope != nil && globalNow().Unix() >
		atomic.LoadInt64(&c.lastUpdateUnix)+c.scope.registry.expirePeriodSeconds {
		atomic.StoreUint32(&c.expired, 1)

		// Check once more for possible race where value updated between
		// beginning of this call
		delta = c.value()
		if delta != 0 {
			c.cachedCount.ReportCount(delta)
			atomic.StoreInt64(&c.lastUpdateUnix, globalNow().Unix())
			atomic.StoreUint32(&c.expired, 0)

			return false
		}

		return true
	}

	return false
}

func (c *counter) snapshot() int64 {
	return atomic.LoadInt64(&c.curr) - atomic.LoadInt64(&c.prev)
}

type gauge struct {
	updated        uint64
	curr           uint64
	cachedGauge    CachedGauge
	lastUpdateUnix int64
	expired        uint32
	scope          *scope
	name           string
}

func newGauge(cachedGauge CachedGauge, name string, scope *scope) *gauge {
	return &gauge{
		cachedGauge:    cachedGauge,
		scope:          scope,
		lastUpdateUnix: globalNow().Unix(),
		name:           name,
	}
}

func (g *gauge) Update(v float64) {
	atomic.StoreUint64(&g.curr, math.Float64bits(v))
	atomic.StoreUint64(&g.updated, 1)

	if atomic.LoadUint32(&g.expired) == 1 && g.scope != nil {
		if !atomic.CompareAndSwapUint32(&g.expired, 1, 0) {
			// Another routine got here first
			return
		}

		// Gauge has expired, but direct ref was held and gauge
		// was updated, therefore insert back into scope
		scope := g.scope.getCurrentScope()

		scope.gm.Lock()
		if _, ok := scope.gauges[g.name]; !ok {
			scope.gauges[g.name] = []*gauge{g}
		} else {
			// Another gauge with the same name was created, so add this
			// to slice
			scope.gauges[g.name] = append(scope.gauges[g.name], g)
		}

		scope.gm.Unlock()
	}
}

func (g *gauge) value() float64 {
	return math.Float64frombits(atomic.LoadUint64(&g.curr))
}

func (g *gauge) report(name string, tags map[string]string, r StatsReporter) bool {
	if atomic.SwapUint64(&g.updated, 0) == 1 {
		r.ReportGauge(name, tags, g.value())
		atomic.StoreInt64(&g.lastUpdateUnix, globalNow().Unix())
		return false
	}

	// Check if gauge has expired
	if g.scope != nil && globalNow().Unix() >
		atomic.LoadInt64(&g.lastUpdateUnix)+g.scope.registry.expirePeriodSeconds {
		atomic.StoreUint32(&g.expired, 1)

		// Check once more for possible race where value updated between
		// beginning of this call
		if atomic.SwapUint64(&g.updated, 0) == 1 {
			r.ReportGauge(name, tags, g.value())
			atomic.StoreInt64(&g.lastUpdateUnix, globalNow().Unix())
			atomic.StoreUint32(&g.expired, 0)

			return false
		}

		return true
	}

	return false
}

func (g *gauge) cachedReport() bool {
	if atomic.SwapUint64(&g.updated, 0) == 1 {
		g.cachedGauge.ReportGauge(g.value())
		atomic.StoreInt64(&g.lastUpdateUnix, globalNow().Unix())
		return false
	}

	// Check if gauge has expired
	if g.scope != nil && globalNow().Unix() >
		atomic.LoadInt64(&g.lastUpdateUnix)+g.scope.registry.expirePeriodSeconds {
		atomic.StoreUint32(&g.expired, 1)

		// Check once more for possible race where value updated between
		// beginning of this call
		if atomic.SwapUint64(&g.updated, 0) == 1 {
			g.cachedGauge.ReportGauge(g.value())
			atomic.StoreInt64(&g.lastUpdateUnix, globalNow().Unix())
			atomic.StoreUint32(&g.expired, 0)

			return false
		}

		return true
	}

	return false
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

func (t *timer) Record(interval time.Duration) {
	if t.cachedTimer != nil {
		t.cachedTimer.ReportTimer(interval)
	} else {
		t.reporter.ReportTimer(t.name, t.tags, interval)
	}
}

func (t *timer) Start() Stopwatch {
	return NewStopwatch(globalNow(), t)
}

func (t *timer) RecordStopwatch(stopwatchStart time.Time) {
	d := globalNow().Sub(stopwatchStart)
	t.Record(d)
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
	return capabilitiesReportingTagging
}

func (r *timerNoReporterSink) Flush() {
}

type histogram struct {
	htype            histogramType
	tags             map[string]string
	reporter         StatsReporter
	specification    Buckets
	buckets          []histogramBucket
	lookupByValue    []float64
	lookupByDuration []int
	lastUpdateUnix   int64
	expired          uint32
	scope            *scope
	name             string
}

type histogramType int

const (
	valueHistogramType histogramType = iota
	durationHistogramType
)

func newHistogram(
	tags map[string]string,
	reporter StatsReporter,
	buckets Buckets,
	cachedHistogram CachedHistogram,
	scope *scope,
	name string,
) *histogram {
	htype := valueHistogramType
	if _, ok := buckets.(DurationBuckets); ok {
		htype = durationHistogramType
	}

	pairs := BucketPairs(buckets)

	h := &histogram{
		htype:            htype,
		tags:             tags,
		reporter:         reporter,
		specification:    buckets,
		buckets:          make([]histogramBucket, 0, len(pairs)),
		lookupByValue:    make([]float64, 0, len(pairs)),
		lookupByDuration: make([]int, 0, len(pairs)),
		scope:            scope,
		name:             name,
		lastUpdateUnix:   globalNow().Unix(),
	}

	for _, pair := range pairs {
		h.addBucket(newHistogramBucket(h,
			pair.LowerBoundValue(), pair.UpperBoundValue(),
			pair.LowerBoundDuration(), pair.UpperBoundDuration(),
			cachedHistogram))
	}

	return h
}

func (h *histogram) addBucket(b histogramBucket) {
	h.buckets = append(h.buckets, b)
	h.lookupByValue = append(h.lookupByValue, b.valueUpperBound)
	h.lookupByDuration = append(h.lookupByDuration, int(b.durationUpperBound))
}

func (h *histogram) reportValues(name string, tags map[string]string, r StatsReporter) bool {
	newValues := false
	for i := range h.buckets {
		samples := h.buckets[i].samples.value()
		if samples == 0 {
			continue
		}
		switch h.htype {
		case valueHistogramType:
			r.ReportHistogramValueSamples(name, tags, h.specification,
				h.buckets[i].valueLowerBound, h.buckets[i].valueUpperBound,
				samples)
		case durationHistogramType:
			r.ReportHistogramDurationSamples(name, tags, h.specification,
				h.buckets[i].durationLowerBound, h.buckets[i].durationUpperBound,
				samples)
		}

		newValues = true
	}

	return newValues
}

func (h *histogram) report(name string, tags map[string]string, r StatsReporter) bool {
	if h.reportValues(name, tags, r) {
		atomic.StoreInt64(&h.lastUpdateUnix, globalNow().Unix())
		return false
	}

	// Check if histogram has expired
	if h.scope != nil && globalNow().Unix() >
		atomic.LoadInt64(&h.lastUpdateUnix)+h.scope.registry.expirePeriodSeconds {
		atomic.StoreUint32(&h.expired, 1)

		// Check once more for possible race where values updated between
		// beginning of this call
		if h.reportValues(name, tags, r) {
			atomic.StoreInt64(&h.lastUpdateUnix, globalNow().Unix())
			atomic.StoreUint32(&h.expired, 0)

			return false
		}

		return true
	}

	return false
}

func (h *histogram) cachedReportValues() bool {
	newValues := false
	for i := range h.buckets {
		samples := h.buckets[i].samples.value()
		if samples == 0 {
			continue
		}
		switch h.htype {
		case valueHistogramType:
			h.buckets[i].cachedValueBucket.ReportSamples(samples)
		case durationHistogramType:
			h.buckets[i].cachedDurationBucket.ReportSamples(samples)
		}

		newValues = true
	}

	return newValues
}

func (h *histogram) cachedReport() bool {
	if h.cachedReportValues() {
		atomic.StoreInt64(&h.lastUpdateUnix, globalNow().Unix())
		return false
	}

	// Check if histogram has expired
	if h.scope != nil && globalNow().Unix() >
		atomic.LoadInt64(&h.lastUpdateUnix)+h.scope.registry.expirePeriodSeconds {
		atomic.StoreUint32(&h.expired, 1)

		// Check once more for possible race where values updated between
		// beginning of this call
		if h.cachedReportValues() {
			atomic.StoreInt64(&h.lastUpdateUnix, globalNow().Unix())
			atomic.StoreUint32(&h.expired, 0)

			return false
		}

		return true
	}

	return false
}

func (h *histogram) RecordValue(value float64) {
	// Find the highest inclusive of the bucket upper bound
	// and emit directly to it. Since we use BucketPairs to derive
	// buckets there will always be an inclusive bucket as
	// we always have a math.MaxFloat64 bucket.
	idx := sort.SearchFloat64s(h.lookupByValue, value)
	h.buckets[idx].samples.Inc(1)

	if atomic.LoadUint32(&h.expired) == 1 && h.scope != nil {
		if !atomic.CompareAndSwapUint32(&h.expired, 1, 0) {
			// Another routine got here first
			return
		}

		// Histogram has expired, but direct ref was held and histogram
		// was updated, therefore insert back into scope
		scope := h.scope.getCurrentScope()

		scope.hm.Lock()
		if _, ok := scope.histograms[h.name]; !ok {
			scope.histograms[h.name] = []*histogram{h}
		} else {
			// Another histogram with the same name was created
			// so add this to slice
			scope.histograms[h.name] = append(scope.histograms[h.name], h)
		}

		scope.hm.Unlock()
	}
}

func (h *histogram) RecordDuration(value time.Duration) {
	// Find the highest inclusive of the bucket upper bound
	// and emit directly to it. Since we use BucketPairs to derive
	// buckets there will always be an inclusive bucket as
	// we always have a math.MaxInt64 bucket.
	idx := sort.SearchInts(h.lookupByDuration, int(value))
	h.buckets[idx].samples.Inc(1)

	if atomic.LoadUint32(&h.expired) == 1 && h.scope != nil {
		// Histogram has expired, but direct ref was held and histogram
		// was updated, therefore insert back into scope
		scope := h.scope.getCurrentScope()

		scope.hm.Lock()
		if histograms, ok := scope.histograms[h.name]; !ok {
			scope.histograms[h.name] = []*histogram{h}
		} else {
			exists := false
			for _, histogram := range histograms {
				if histogram == h {
					exists = true
					// Another routine has already inserted this histogram
					break
				}
			}

			if !exists {
				// Another histogram with the same name was created
				// so add this to slice
				scope.histograms[h.name] = append(scope.histograms[h.name], h)
			}
		}
		scope.hm.Unlock()

		atomic.StoreUint32(&h.expired, 0)
	}
}

func (h *histogram) Start() Stopwatch {
	return NewStopwatch(globalNow(), h)
}

func (h *histogram) RecordStopwatch(stopwatchStart time.Time) {
	d := globalNow().Sub(stopwatchStart)
	h.RecordDuration(d)
}

func (h *histogram) snapshotValues() map[float64]int64 {
	if h.htype == durationHistogramType {
		return nil
	}

	vals := make(map[float64]int64, len(h.buckets))
	for i := range h.buckets {
		vals[h.buckets[i].valueUpperBound] = h.buckets[i].samples.value()
	}

	return vals
}

func (h *histogram) snapshotDurations() map[time.Duration]int64 {
	if h.htype == valueHistogramType {
		return nil
	}

	durations := make(map[time.Duration]int64, len(h.buckets))
	for i := range h.buckets {
		durations[h.buckets[i].durationUpperBound] = h.buckets[i].samples.value()
	}

	return durations
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
		samples:            newCounter(nil, "", nil),
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
