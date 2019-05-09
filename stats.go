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
	prev        int64
	curr        int64
	cachedCount CachedCount
	tracker     *metricExpiryTracker
}

func newCounter(cachedCount CachedCount, name string, scope *scope) *counter {
	return &counter{
		cachedCount: cachedCount,
		tracker: &metricExpiryTracker{
			scope:          scope,
			name:           name,
			lastUpdateUnix: globalNow().Unix(),
		},
	}
}

func (c *counter) Inc(v int64) {
	atomic.AddInt64(&c.curr, v)

	if !c.tracker.expiredAndUpdated() {
		return
	}

	// Counter has expired, but direct ref was held and counter
	// was incremented, therefore insert back into scope
	scope := c.tracker.scope.getCurrentScope()
	scope.cm.Lock()
	if _, ok := scope.counters[c.tracker.name]; !ok {
		// No counters with the same name were created since expiry
		scope.counters[c.tracker.name] = []*counter{c}
	} else {
		// Another counter with the same name was created
		// and this expired counter is not in the list so add to slice
		scope.counters[c.tracker.name] = append(scope.counters[c.tracker.name], c)
	}

	scope.cm.Unlock()
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

func (c *counter) report(name string, tags map[string]string, r StatsReporter, nowUnix int64) bool {
	delta := c.value()
	if delta != 0 {
		r.ReportCounter(name, tags, delta)
		c.tracker.resetUpdatedTime()
		return false
	}

	return c.tracker.checkExpiration(nowUnix)
}

func (c *counter) cachedReport(nowUnix int64) bool {
	delta := c.value()
	if delta != 0 {
		c.cachedCount.ReportCount(delta)
		c.tracker.resetUpdatedTime()
		return false
	}

	return c.tracker.checkExpiration(nowUnix)
}

func (c *counter) snapshot() int64 {
	return atomic.LoadInt64(&c.curr) - atomic.LoadInt64(&c.prev)
}

func (c *counter) expired() bool {
	return atomic.LoadUint32(&c.tracker.expired) == 1
}

type gauge struct {
	updated     uint64
	curr        uint64
	cachedGauge CachedGauge
	tracker     *metricExpiryTracker
}

func newGauge(cachedGauge CachedGauge, name string, scope *scope) *gauge {
	return &gauge{
		cachedGauge: cachedGauge,
		tracker: &metricExpiryTracker{
			scope:          scope,
			lastUpdateUnix: globalNow().Unix(),
			name:           name,
		},
	}
}

func (g *gauge) Update(v float64) {
	atomic.StoreUint64(&g.curr, math.Float64bits(v))
	atomic.StoreUint64(&g.updated, 1)

	if !g.tracker.expiredAndUpdated() {
		return
	}

	// Gauge has expired, but direct ref was held and gauge
	// was updated, therefore insert back into scope
	scope := g.tracker.scope.getCurrentScope()

	scope.gm.Lock()
	if _, ok := scope.gauges[g.tracker.name]; !ok {
		scope.gauges[g.tracker.name] = []*gauge{g}
	} else {
		// Another gauge with the same name was created, so add this
		// to slice
		scope.gauges[g.tracker.name] = append(scope.gauges[g.tracker.name], g)
	}

	scope.gm.Unlock()
}

func (g *gauge) value() float64 {
	return math.Float64frombits(atomic.LoadUint64(&g.curr))
}

func (g *gauge) report(name string, tags map[string]string, r StatsReporter, nowUnix int64) bool {
	if atomic.SwapUint64(&g.updated, 0) == 1 {
		r.ReportGauge(name, tags, g.value())
		g.tracker.resetUpdatedTime()
		return false
	}

	return g.tracker.checkExpiration(nowUnix)
}

func (g *gauge) cachedReport(nowUnix int64) bool {
	if atomic.SwapUint64(&g.updated, 0) == 1 {
		g.cachedGauge.ReportGauge(g.value())
		g.tracker.resetUpdatedTime()
		return false
	}

	return g.tracker.checkExpiration(nowUnix)
}

func (g *gauge) snapshot() float64 {
	return math.Float64frombits(atomic.LoadUint64(&g.curr))
}

func (g *gauge) expired() bool {
	return atomic.LoadUint32(&g.tracker.expired) == 1
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
	tracker          *metricExpiryTracker
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
		tracker: &metricExpiryTracker{
			scope:          scope,
			name:           name,
			lastUpdateUnix: globalNow().Unix(),
		},
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

func (h *histogram) report(name string, tags map[string]string, r StatsReporter, nowUnix int64) bool {
	if h.reportValues(name, tags, r) {
		h.tracker.resetUpdatedTime()
		return false
	}

	return h.tracker.checkExpiration(nowUnix)
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

func (h *histogram) cachedReport(nowUnix int64) bool {
	if h.cachedReportValues() {
		h.tracker.resetUpdatedTime()
		return false
	}

	return h.tracker.checkExpiration(nowUnix)
}

func (h *histogram) RecordValue(value float64) {
	// Find the highest inclusive of the bucket upper bound
	// and emit directly to it. Since we use BucketPairs to derive
	// buckets there will always be an inclusive bucket as
	// we always have a math.MaxFloat64 bucket.
	idx := sort.SearchFloat64s(h.lookupByValue, value)
	h.buckets[idx].samples.Inc(1)
	h.checkExpiryAndUpdate()
}

func (h *histogram) checkExpiryAndUpdate() {
	if !h.tracker.expiredAndUpdated() {
		return
	}

	// Histogram has expired, but direct ref was held and histogram
	// was updated, therefore insert back into scope
	scope := h.tracker.scope.getCurrentScope()

	scope.hm.Lock()
	if _, ok := scope.histograms[h.tracker.name]; !ok {
		scope.histograms[h.tracker.name] = []*histogram{h}
	} else {
		// Another histogram with the same name was created
		// so add this to slice
		scope.histograms[h.tracker.name] = append(scope.histograms[h.tracker.name], h)
	}

	scope.hm.Unlock()
}

func (h *histogram) RecordDuration(value time.Duration) {
	// Find the highest inclusive of the bucket upper bound
	// and emit directly to it. Since we use BucketPairs to derive
	// buckets there will always be an inclusive bucket as
	// we always have a math.MaxInt64 bucket.
	idx := sort.SearchInts(h.lookupByDuration, int(value))
	h.buckets[idx].samples.Inc(1)
	h.checkExpiryAndUpdate()
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

func (h *histogram) expired() bool {
	return atomic.LoadUint32(&h.tracker.expired) == 1
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

type metricExpiryTracker struct {
	lastUpdateUnix int64
	expired        uint32
	scope          *scope
	name           string
}

// expiredAndUpdated checks the metric is expired and if so
// will return true only for the first routine that updates
// an expired metric, signalling to insert the metric back
// into the parent scope.
func (m *metricExpiryTracker) expiredAndUpdated() bool {
	// We perform a LoadUint32 first for performance reasons
	if atomic.LoadUint32(&m.expired) == 1 && m.scope != nil &&
		atomic.CompareAndSwapUint32(&m.expired, 1, 0) {
		m.resetUpdatedTime()
		return true
	}

	return false
}

func (m *metricExpiryTracker) resetUpdatedTime() {
	atomic.StoreInt64(&m.lastUpdateUnix, globalNow().Unix())
}

// checkExpiration returns whether the metric has expired and sets the expired
// bit to one if true
func (m *metricExpiryTracker) checkExpiration(nowUnix int64) bool {
	if m.scope != nil && nowUnix >
		atomic.LoadInt64(&m.lastUpdateUnix)+m.scope.registry.expirePeriodSeconds {

		atomic.StoreUint32(&m.expired, 1)
		return true
	}

	return false
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
