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

package tally

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uber-go/tally/internal/identity"
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

func (c *counter) report(
	name string,
	tags map[string]string,
	r StatsReporter,
) bool {
	delta := c.value()
	if delta == 0 {
		return false
	}

	r.ReportCounter(name, tags, delta)
	return true
}

func (c *counter) cachedReport() bool {
	delta := c.value()
	if delta == 0 {
		return false
	}

	c.cachedCount.ReportCount(delta)
	return true
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

func (g *gauge) report(
	name string,
	tags map[string]string,
	r StatsReporter,
) bool {
	if atomic.SwapUint64(&g.updated, 0) == 0 {
		return false
	}

	r.ReportGauge(name, tags, g.value())
	return true
}

func (g *gauge) cachedReport() bool {
	if atomic.SwapUint64(&g.updated, 0) == 0 {
		return false
	}

	g.cachedGauge.ReportGauge(g.value())
	return true
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
	updated     uint32
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

	atomic.StoreUint32(&t.updated, 1)
}

func (t *timer) Start() Stopwatch {
	return NewStopwatch(globalNow(), t)
}

func (t *timer) RecordStopwatch(stopwatchStart time.Time) {
	d := globalNow().Sub(stopwatchStart)
	t.Record(d)
}

func (t *timer) hasReported() bool {
	return atomic.SwapUint32(&t.updated, 0) == 1
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
	bucketLowerBound float64,
	bucketUpperBound float64,
	samples int64,
) {
}

func (r *timerNoReporterSink) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	buckets Buckets,
	bucketLowerBound time.Duration,
	bucketUpperBound time.Duration,
	samples int64,
) {
}

func (r *timerNoReporterSink) Capabilities() Capabilities {
	return capabilitiesReportingTagging
}

func (r *timerNoReporterSink) Flush() {
}

type sampleCounter struct {
	counter      *counter
	cachedBucket CachedHistogramBucket
}

type histogram struct {
	htype            histogramType
	name             string
	tags             map[string]string
	reporter         StatsReporter
	specification    Buckets
	buckets          []histogramBucket
	samples          []sampleCounter
	lookupByValue    []float64
	lookupByDuration []int
}

type histogramType int

const (
	valueHistogramType histogramType = iota
	durationHistogramType
)

func newHistogram(
	htype histogramType,
	name string,
	tags map[string]string,
	reporter StatsReporter,
	storage bucketStorage,
	cachedHistogram CachedHistogram,
) *histogram {
	h := &histogram{
		htype:            htype,
		name:             name,
		tags:             tags,
		reporter:         reporter,
		specification:    storage.buckets,
		buckets:          storage.hbuckets,
		samples:          make([]sampleCounter, len(storage.hbuckets)),
		lookupByValue:    storage.lookupByValue,
		lookupByDuration: storage.lookupByDuration,
	}

	for i := range h.buckets {
		h.samples[i].counter = newCounter(nil)

		if cachedHistogram != nil {
			switch htype {
			case durationHistogramType:
				h.samples[i].cachedBucket = cachedHistogram.DurationBucket(
					h.buckets[i].durationLowerBound,
					h.buckets[i].durationUpperBound,
				)
			case valueHistogramType:
				h.samples[i].cachedBucket = cachedHistogram.ValueBucket(
					h.buckets[i].valueLowerBound,
					h.buckets[i].valueUpperBound,
				)
			}
		}
	}

	return h
}

func (h *histogram) report(
	name string,
	tags map[string]string,
	r StatsReporter,
) (reported bool) {
	for i := range h.buckets {
		samples := h.samples[i].counter.value()
		if samples == 0 {
			continue
		}

		reported = true

		switch h.htype {
		case valueHistogramType:
			r.ReportHistogramValueSamples(
				name,
				tags,
				h.specification,
				h.buckets[i].valueLowerBound,
				h.buckets[i].valueUpperBound,
				samples,
			)
		case durationHistogramType:
			r.ReportHistogramDurationSamples(
				name,
				tags,
				h.specification,
				h.buckets[i].durationLowerBound,
				h.buckets[i].durationUpperBound,
				samples,
			)
		}
	}

	return
}

func (h *histogram) cachedReport() (reported bool) {
	for i := range h.buckets {
		samples := h.samples[i].counter.value()
		if samples == 0 {
			continue
		}

		reported = true

		switch h.htype {
		case valueHistogramType:
			h.samples[i].cachedBucket.ReportSamples(samples)
		case durationHistogramType:
			h.samples[i].cachedBucket.ReportSamples(samples)
		}
	}

	return
}

func (h *histogram) RecordValue(value float64) {
	if h.htype != valueHistogramType {
		return
	}

	// Find the highest inclusive of the bucket upper bound
	// and emit directly to it. Since we use BucketPairs to derive
	// buckets there will always be an inclusive bucket as
	// we always have a math.MaxFloat64 bucket.
	idx := sort.SearchFloat64s(h.lookupByValue, value)
	h.samples[idx].counter.Inc(1)
}

func (h *histogram) RecordDuration(value time.Duration) {
	if h.htype != durationHistogramType {
		return
	}

	// Find the highest inclusive of the bucket upper bound
	// and emit directly to it. Since we use BucketPairs to derive
	// buckets there will always be an inclusive bucket as
	// we always have a math.MaxInt64 bucket.
	idx := sort.SearchInts(h.lookupByDuration, int(value))
	h.samples[idx].counter.Inc(1)
}

func (h *histogram) Start() Stopwatch {
	return NewStopwatch(globalNow(), h)
}

func (h *histogram) RecordStopwatch(stopwatchStart time.Time) {
	d := globalNow().Sub(stopwatchStart)
	h.RecordDuration(d)
}

func (h *histogram) snapshotValues() map[float64]int64 {
	if h.htype != valueHistogramType {
		return nil
	}

	vals := make(map[float64]int64, len(h.buckets))
	for i := range h.buckets {
		vals[h.buckets[i].valueUpperBound] = h.samples[i].counter.snapshot()
	}

	return vals
}

func (h *histogram) snapshotDurations() map[time.Duration]int64 {
	if h.htype != durationHistogramType {
		return nil
	}

	durations := make(map[time.Duration]int64, len(h.buckets))
	for i := range h.buckets {
		durations[h.buckets[i].durationUpperBound] = h.samples[i].counter.snapshot()
	}

	return durations
}

type histogramBucket struct {
	valueLowerBound      float64
	valueUpperBound      float64
	durationLowerBound   time.Duration
	durationUpperBound   time.Duration
	cachedValueBucket    CachedHistogramBucket
	cachedDurationBucket CachedHistogramBucket
}

type bucketStorage struct {
	buckets          Buckets
	hbuckets         []histogramBucket
	lookupByValue    []float64
	lookupByDuration []int
}

func newBucketStorage(
	htype histogramType,
	buckets Buckets,
) bucketStorage {
	var (
		pairs   = BucketPairs(buckets)
		storage bucketStorage
	)

	storage.buckets = buckets
	storage.hbuckets = make([]histogramBucket, 0, len(pairs))

	switch htype {
	case valueHistogramType:
		storage.lookupByValue = make([]float64, 0, len(pairs))
	case durationHistogramType:
		storage.lookupByDuration = make([]int, 0, len(pairs))
	}

	for _, pair := range pairs {
		bucket := histogramBucket{
			valueLowerBound:    pair.LowerBoundValue(),
			valueUpperBound:    pair.UpperBoundValue(),
			durationLowerBound: pair.LowerBoundDuration(),
			durationUpperBound: pair.UpperBoundDuration(),
		}

		switch htype {
		case valueHistogramType:
			storage.lookupByValue = append(storage.lookupByValue, bucket.valueUpperBound)
		case durationHistogramType:
			storage.lookupByDuration = append(storage.lookupByDuration, int(bucket.durationUpperBound))
		}

		storage.hbuckets = append(storage.hbuckets, bucket)
	}

	return storage
}

type bucketCache struct {
	mtx   sync.RWMutex
	cache map[uint64]bucketStorage
}

func newBucketCache() *bucketCache {
	return &bucketCache{
		cache: make(map[uint64]bucketStorage),
	}
}

func (c *bucketCache) Get(
	htype histogramType,
	buckets Buckets,
) bucketStorage {
	id := getBucketsIdentity(buckets)

	c.mtx.RLock()
	storage, ok := c.cache[id]
	if !ok {
		c.mtx.RUnlock()
		c.mtx.Lock()
		storage = newBucketStorage(htype, buckets)
		c.cache[id] = storage
		c.mtx.Unlock()
	} else {
		c.mtx.RUnlock()
	}
	return storage
}

// n.b. This function is used to uniquely identify a given set of buckets
//      commutatively through hash folding, in order to do cache lookups and
//      avoid allocating additional storage for data that is shared among all
//      instances of a particular set of buckets.
func getBucketsIdentity(buckets Buckets) uint64 {
	acc := identity.NewAccumulator()

	if dbuckets, ok := buckets.(DurationBuckets); ok {
		for _, dur := range dbuckets {
			acc = acc.AddUint64(uint64(dur))
		}
	} else {
		vbuckets := buckets.(ValueBuckets)
		for _, val := range vbuckets {
			acc = acc.AddUint64(math.Float64bits(val))
		}
	}

	return acc.Value()
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
