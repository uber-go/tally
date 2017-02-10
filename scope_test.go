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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type testIntValue struct {
	val      int64
	tags     map[string]string
	reporter *testStatsReporter
}

func (m *testIntValue) ReportCount(value int64) {
	m.val = value
	m.reporter.cg.Done()
}

func (m *testIntValue) ReportTimer(interval time.Duration) {
	m.val = int64(interval)
	m.reporter.tg.Done()
}

type testFloatValue struct {
	val      float64
	tags     map[string]string
	reporter *testStatsReporter
}

func (m *testFloatValue) ReportGauge(value float64) {
	m.val = value
	m.reporter.gg.Done()
}

type testHistogramValue struct {
	tags            map[string]string
	valueSamples    map[float64]int
	durationSamples map[time.Duration]int
}

func newTestHistogramValue() *testHistogramValue {
	return &testHistogramValue{
		valueSamples:    make(map[float64]int),
		durationSamples: make(map[time.Duration]int),
	}
}

type testStatsReporter struct {
	cg sync.WaitGroup
	gg sync.WaitGroup
	tg sync.WaitGroup
	hg sync.WaitGroup

	scope Scope

	counters   map[string]*testIntValue
	gauges     map[string]*testFloatValue
	timers     map[string]*testIntValue
	histograms map[string]*testHistogramValue
}

// newTestStatsReporter returns a new TestStatsReporter
func newTestStatsReporter() *testStatsReporter {
	return &testStatsReporter{
		counters:   make(map[string]*testIntValue),
		gauges:     make(map[string]*testFloatValue),
		timers:     make(map[string]*testIntValue),
		histograms: make(map[string]*testHistogramValue),
	}
}

func (r *testStatsReporter) WaitAll() {
	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()
	r.hg.Wait()
}

func (r *testStatsReporter) AllocateCounter(
	name string, tags map[string]string,
) CachedCount {
	counter := &testIntValue{
		val:      0,
		tags:     tags,
		reporter: r,
	}
	r.counters[name] = counter
	return counter
}

func (r *testStatsReporter) ReportCounter(name string, tags map[string]string, value int64) {
	r.counters[name] = &testIntValue{
		val:  value,
		tags: tags,
	}
	r.cg.Done()
}

func (r *testStatsReporter) AllocateGauge(
	name string, tags map[string]string,
) CachedGauge {
	gauge := &testFloatValue{
		val:      0,
		tags:     tags,
		reporter: r,
	}
	r.gauges[name] = gauge
	return gauge
}

func (r *testStatsReporter) ReportGauge(name string, tags map[string]string, value float64) {
	r.gauges[name] = &testFloatValue{
		val:  value,
		tags: tags,
	}
	r.gg.Done()
}

func (r *testStatsReporter) AllocateTimer(
	name string, tags map[string]string,
) CachedTimer {
	timer := &testIntValue{
		val:      0,
		tags:     tags,
		reporter: r,
	}
	r.timers[name] = timer
	return timer
}

func (r *testStatsReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	r.timers[name] = &testIntValue{
		val:  int64(interval),
		tags: tags,
	}
	r.tg.Done()
}

func (r *testStatsReporter) AllocateHistogram(
	name string,
	tags map[string]string,
	buckets Buckets,
) CachedHistogram {
	return testStatsReporterCachedHistogram{r, name, tags, buckets}
}

type testStatsReporterCachedHistogram struct {
	r       *testStatsReporter
	name    string
	tags    map[string]string
	buckets Buckets
}

func (h testStatsReporterCachedHistogram) ValueBucket(
	bucketLowerBound, bucketUpperBound float64,
) CachedHistogramBucket {
	return testStatsReporterCachedHistogramValueBucket{h, bucketLowerBound, bucketUpperBound}
}

func (h testStatsReporterCachedHistogram) DurationBucket(
	bucketLowerBound, bucketUpperBound time.Duration,
) CachedHistogramBucket {
	return testStatsReporterCachedHistogramDurationBucket{h, bucketLowerBound, bucketUpperBound}
}

type testStatsReporterCachedHistogramValueBucket struct {
	histogram        testStatsReporterCachedHistogram
	bucketLowerBound float64
	bucketUpperBound float64
}

func (b testStatsReporterCachedHistogramValueBucket) ReportSamples(v int64) {
	b.histogram.r.ReportHistogramValueSamples(b.histogram.name, b.histogram.tags,
		b.histogram.buckets, b.bucketLowerBound, b.bucketUpperBound, v)
}

type testStatsReporterCachedHistogramDurationBucket struct {
	histogram        testStatsReporterCachedHistogram
	bucketLowerBound time.Duration
	bucketUpperBound time.Duration
}

func (b testStatsReporterCachedHistogramDurationBucket) ReportSamples(v int64) {
	b.histogram.r.ReportHistogramDurationSamples(b.histogram.name, b.histogram.tags,
		b.histogram.buckets, b.bucketLowerBound, b.bucketUpperBound, v)
}

func (r *testStatsReporter) ReportHistogramValueSamples(
	name string,
	tags map[string]string,
	buckets Buckets,
	bucketLowerBound,
	bucketUpperBound float64,
	samples int64,
) {
	value, ok := r.histograms[name]
	if !ok {
		value = newTestHistogramValue()
		value.tags = tags
		r.histograms[name] = value
	}
	value.valueSamples[bucketUpperBound] = int(samples)
	r.hg.Done()
}

func (r *testStatsReporter) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	buckets Buckets,
	bucketLowerBound,
	bucketUpperBound time.Duration,
	samples int64,
) {
	value, ok := r.histograms[name]
	if !ok {
		value = newTestHistogramValue()
		value.tags = tags
		r.histograms[name] = value
	}
	value.durationSamples[bucketUpperBound] = int(samples)
	r.hg.Done()
}

func (r *testStatsReporter) Capabilities() Capabilities {
	return capabilitiesReportingNoTaggingNoHistograms // TODO: add support for test stats reporter for histograms
}

func (r *testStatsReporter) Flush() {}

func TestWriteTimerImmediately(t *testing.T) {
	r := newTestStatsReporter()
	s, _ := NewRootScope(ScopeOptions{Reporter: r}, 0)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.tg.Wait()
}

func TestWriteTimerClosureImmediately(t *testing.T) {
	r := newTestStatsReporter()
	s, _ := NewRootScope(ScopeOptions{Reporter: r}, 0)
	r.tg.Add(1)
	tm := s.Timer("ticky")
	tm.Start().Stop()
	r.tg.Wait()
}

func TestWriteReportLoop(t *testing.T) {
	r := newTestStatsReporter()
	s, close := NewRootScope(ScopeOptions{Reporter: r}, 10)
	defer close.Close()

	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", LinearValueBuckets(0, 10, 10)).RecordValue(42.42)

	r.WaitAll()
}

func TestCachedReportLoop(t *testing.T) {
	r := newTestStatsReporter()
	s, close := NewRootScope(ScopeOptions{CachedReporter: r}, 10)
	defer close.Close()

	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", LinearValueBuckets(0, 10, 10)).RecordValue(42.42)

	r.WaitAll()
}

func TestWriteOnce(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope(ScopeOptions{Reporter: r}, 0)
	s := root.(*scope)

	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", LinearValueBuckets(0, 10, 10)).RecordValue(42.42)

	s.report(r)
	r.WaitAll()

	assert.EqualValues(t, 1, r.counters["bar"].val)
	assert.EqualValues(t, 1, r.gauges["zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["ticky"].val)
	assert.EqualValues(t, 1, r.histograms["baz"].valueSamples[50.0])

	r = newTestStatsReporter()
	s.report(r)

	assert.Nil(t, r.counters["bar"])
	assert.Nil(t, r.gauges["zed"])
	assert.Nil(t, r.timers["ticky"])
}

func TestCachedReporter(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope(ScopeOptions{CachedReporter: r}, 0)
	s := root.(*scope)

	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", LinearValueBuckets(0, 10, 10)).RecordValue(42.42)

	s.cachedReport(r)
	r.WaitAll()

	assert.EqualValues(t, 1, r.counters["bar"].val)
	assert.EqualValues(t, 1, r.gauges["zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["ticky"].val)
	assert.EqualValues(t, 1, r.histograms["baz"].valueSamples[50.0])
}

func TestRootScopeWithoutPrefix(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope(ScopeOptions{Reporter: r}, 0)
	s := root.(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", LinearValueBuckets(0, 10, 10)).RecordValue(42.42)

	s.report(r)
	r.WaitAll()

	assert.EqualValues(t, 21, r.counters["bar"].val)
	assert.EqualValues(t, 1, r.gauges["zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["blork"].val)
	assert.EqualValues(t, 1, r.histograms["baz"].valueSamples[50.0])
}

func TestRootScopeWithPrefix(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope(ScopeOptions{Prefix: "foo", Reporter: r}, 0)
	s := root.(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", LinearValueBuckets(0, 10, 10)).RecordValue(42.42)

	s.report(r)
	r.WaitAll()

	assert.EqualValues(t, 21, r.counters["foo.bar"].val)
	assert.EqualValues(t, 1, r.gauges["foo.zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.blork"].val)
	assert.EqualValues(t, 1, r.histograms["foo.baz"].valueSamples[50.0])
}

func TestRootScopeWithDifferentSeparator(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope(ScopeOptions{Prefix: "foo", Separator: "_", Reporter: r}, 0)
	s := root.(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", LinearValueBuckets(0, 10, 10)).RecordValue(42.42)

	s.report(r)
	r.WaitAll()

	assert.EqualValues(t, 21, r.counters["foo_bar"].val)
	assert.EqualValues(t, 1, r.gauges["foo_zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo_blork"].val)
	assert.EqualValues(t, 1, r.histograms["foo_baz"].valueSamples[50.0])
}

func TestSubScope(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope(ScopeOptions{Prefix: "foo", Reporter: r}, 0)
	s := root.SubScope("mork").(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", LinearValueBuckets(0, 10, 10)).RecordValue(42.42)

	s.report(r)
	r.WaitAll()

	assert.EqualValues(t, 21, r.counters["foo.mork.bar"].val)
	assert.EqualValues(t, 1, r.gauges["foo.mork.zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.mork.blork"].val)
	assert.EqualValues(t, 1, r.histograms["foo.mork.baz"].valueSamples[50.0])
}

func TestTaggedSubScope(t *testing.T) {
	r := newTestStatsReporter()

	ts := map[string]string{"env": "test"}
	root, _ := NewRootScope(ScopeOptions{Prefix: "foo", Tags: ts, Reporter: r}, 0)
	s := root.(*scope)

	tscope := root.Tagged(map[string]string{"service": "test"}).(*scope)
	scope := root

	r.cg.Add(1)
	scope.Counter("beep").Inc(1)
	r.cg.Add(1)
	tscope.Counter("boop").Inc(1)
	r.hg.Add(1)
	scope.Histogram("baz", LinearValueBuckets(0, 10, 10)).RecordValue(42.42)
	r.hg.Add(1)
	tscope.Histogram("bar", LinearValueBuckets(0, 10, 10)).RecordValue(42.42)

	s.report(r)
	tscope.report(r)
	r.cg.Wait()

	assert.EqualValues(t, 1, r.counters["foo.beep"].val)
	assert.EqualValues(t, ts, r.counters["foo.beep"].tags)

	assert.EqualValues(t, 1, r.counters["foo.boop"].val)
	assert.EqualValues(t, map[string]string{
		"env":     "test",
		"service": "test",
	}, r.counters["foo.boop"].tags)

	assert.EqualValues(t, 1, r.histograms["foo.baz"].valueSamples[50.0])
	assert.EqualValues(t, ts, r.histograms["foo.baz"].tags)

	assert.EqualValues(t, 1, r.histograms["foo.bar"].valueSamples[50.0])
	assert.EqualValues(t, map[string]string{
		"env":     "test",
		"service": "test",
	}, r.histograms["foo.bar"].tags)
}

func TestSnapshot(t *testing.T) {
	commonTags := map[string]string{"env": "test"}
	s := NewTestScope("foo", map[string]string{"env": "test"})
	child := s.Tagged(map[string]string{"service": "test"})

	s.Counter("beep").Inc(1)
	s.Gauge("bzzt").Update(2)
	s.Timer("brrr").Record(1 * time.Second)
	s.Timer("brrr").Record(2 * time.Second)
	child.Counter("boop").Inc(1)

	snap := s.Snapshot()
	counters, gauges, timers :=
		snap.Counters(), snap.Gauges(), snap.Timers()

	assert.EqualValues(t, 1, counters["foo.beep"].Value())
	assert.EqualValues(t, commonTags, counters["foo.beep"].Tags())

	assert.EqualValues(t, 2, gauges["foo.bzzt"].Value())
	assert.EqualValues(t, commonTags, gauges["foo.bzzt"].Tags())

	assert.EqualValues(t, []time.Duration{
		1 * time.Second,
		2 * time.Second,
	}, timers["foo.brrr"].Values())
	assert.EqualValues(t, commonTags, timers["foo.brrr"].Tags())

	assert.EqualValues(t, 1, counters["foo.boop"].Value())
	assert.EqualValues(t, map[string]string{
		"env":     "test",
		"service": "test",
	}, counters["foo.boop"].Tags())
}

func TestCapabilities(t *testing.T) {
	r := newTestStatsReporter()
	s, _ := NewRootScope(ScopeOptions{Reporter: r}, 0)
	assert.True(t, s.Capabilities().Reporting())
	assert.False(t, s.Capabilities().Tagging())
}

func TestCapabilitiesNoReporter(t *testing.T) {
	s, _ := NewRootScope(ScopeOptions{}, 0)
	assert.False(t, s.Capabilities().Reporting())
	assert.False(t, s.Capabilities().Tagging())
}

func TestNilTagMerge(t *testing.T) {
	assert.Nil(t, nil, mergeRightTags(nil, nil))
}

func TestScopeDefaultBuckets(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope(ScopeOptions{
		DefaultBuckets: DurationBuckets{
			0 * time.Millisecond,
			30 * time.Millisecond,
			60 * time.Millisecond,
			90 * time.Millisecond,
			120 * time.Millisecond,
		},
		Reporter: r,
	}, 0)
	s := root.(*scope)
	r.hg.Add(2)
	s.Histogram("baz", DefaultBuckets).RecordDuration(42 * time.Millisecond)
	s.Histogram("baz", DefaultBuckets).RecordDuration(84 * time.Millisecond)
	s.Histogram("baz", DefaultBuckets).RecordDuration(84 * time.Millisecond)

	s.report(r)
	r.WaitAll()

	assert.EqualValues(t, 1, r.histograms["baz"].durationSamples[60*time.Millisecond])
	assert.EqualValues(t, 2, r.histograms["baz"].durationSamples[90*time.Millisecond])
}

type testMets struct {
	c Counter
}

func newTestMets(scope Scope) testMets {
	return testMets{
		c: scope.Counter("honk"),
	}
}

func TestReturnByValue(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope(ScopeOptions{Reporter: r}, 0)
	s := root.(*scope)
	mets := newTestMets(s)

	r.cg.Add(1)
	mets.c.Inc(3)
	s.report(r)
	r.cg.Wait()

	assert.EqualValues(t, 3, r.counters["honk"].val)
}
