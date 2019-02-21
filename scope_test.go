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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	// alphanumericSanitizerOpts is the options to create a sanitizer which uses
	// the alphanumeric SanitizeFn.
	alphanumericSanitizerOpts = SanitizeOptions{
		NameCharacters: ValidCharacters{
			Ranges:     AlphanumericRange,
			Characters: UnderscoreDashCharacters,
		},
		KeyCharacters: ValidCharacters{
			Ranges:     AlphanumericRange,
			Characters: UnderscoreDashCharacters,
		},
		ValueCharacters: ValidCharacters{
			Ranges:     AlphanumericRange,
			Characters: UnderscoreDashCharacters,
		},
		ReplacementCharacter: DefaultReplacementCharacter,
	}
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

	flushes int32
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
	return capabilitiesReportingNoTagging
}

func (r *testStatsReporter) Flush() {
	atomic.AddInt32(&r.flushes, 1)
}

func TestWriteTimerImmediately(t *testing.T) {
	r := newTestStatsReporter()
	s, closer := NewRootScope(ScopeOptions{Reporter: r}, 0)
	defer closer.Close()
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.tg.Wait()
}

func TestWriteTimerClosureImmediately(t *testing.T) {
	r := newTestStatsReporter()
	s, closer := NewRootScope(ScopeOptions{Reporter: r}, 0)
	defer closer.Close()
	r.tg.Add(1)
	tm := s.Timer("ticky")
	tm.Start().Stop()
	r.tg.Wait()
}

func TestWriteReportLoop(t *testing.T) {
	r := newTestStatsReporter()
	s, closer := NewRootScope(ScopeOptions{Reporter: r}, 10)
	defer closer.Close()

	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)

	r.WaitAll()
}

func TestCachedReportLoop(t *testing.T) {
	r := newTestStatsReporter()
	s, closer := NewRootScope(ScopeOptions{CachedReporter: r}, 10)
	defer closer.Close()

	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)

	r.WaitAll()
}

func TestWriteOnce(t *testing.T) {
	r := newTestStatsReporter()

	root, closer := NewRootScope(ScopeOptions{Reporter: r}, 0)
	defer closer.Close()

	s := root.(*scope)

	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)

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

func TestCounterSanitized(t *testing.T) {
	r := newTestStatsReporter()

	root, closer := NewRootScope(ScopeOptions{
		Reporter:        r,
		SanitizeOptions: &alphanumericSanitizerOpts,
	}, 0)
	defer closer.Close()

	s := root.(*scope)

	r.cg.Add(1)
	s.Counter("how?").Inc(1)
	r.gg.Add(1)
	s.Gauge("does!").Update(1)
	r.tg.Add(1)
	s.Timer("this!").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("work1!?", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)

	s.report(r)
	r.WaitAll()

	assert.Nil(t, r.counters["how?"])
	assert.EqualValues(t, 1, r.counters["how_"].val)
	assert.Nil(t, r.gauges["does!"])
	assert.EqualValues(t, 1, r.gauges["does_"].val)
	assert.Nil(t, r.timers["this!"])
	assert.EqualValues(t, time.Millisecond*175, r.timers["this_"].val)
	assert.Nil(t, r.histograms["work1!?"])
	assert.EqualValues(t, 1, r.histograms["work1__"].valueSamples[50.0])

	r = newTestStatsReporter()
	s.report(r)

	assert.Nil(t, r.counters["how?"])
	assert.Nil(t, r.counters["how_"])
	assert.Nil(t, r.gauges["does!"])
	assert.Nil(t, r.gauges["does_"])
	assert.Nil(t, r.timers["this!"])
	assert.Nil(t, r.timers["this_"])
	assert.Nil(t, r.histograms["work1!?"])
	assert.Nil(t, r.histograms["work1__"])
}

func TestCachedReporter(t *testing.T) {
	r := newTestStatsReporter()

	root, closer := NewRootScope(ScopeOptions{CachedReporter: r}, 0)
	defer closer.Close()

	s := root.(*scope)

	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.hg.Add(2)
	s.Histogram("baz", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)
	s.Histogram("qux", MustMakeLinearDurationBuckets(0, 10*time.Millisecond, 10)).
		RecordDuration(42 * time.Millisecond)

	s.cachedReport(r)
	r.WaitAll()

	assert.EqualValues(t, 1, r.counters["bar"].val)
	assert.EqualValues(t, 1, r.gauges["zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["ticky"].val)
	assert.EqualValues(t, 1, r.histograms["baz"].valueSamples[50.0])
	assert.EqualValues(t, 1, r.histograms["qux"].durationSamples[50*time.Millisecond])
}

func TestRootScopeWithoutPrefix(t *testing.T) {
	r := newTestStatsReporter()

	root, closer := NewRootScope(ScopeOptions{Reporter: r}, 0)
	defer closer.Close()

	s := root.(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)

	s.report(r)
	r.WaitAll()

	assert.EqualValues(t, 21, r.counters["bar"].val)
	assert.EqualValues(t, 1, r.gauges["zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["blork"].val)
	assert.EqualValues(t, 1, r.histograms["baz"].valueSamples[50.0])
}

func TestRootScopeWithPrefix(t *testing.T) {
	r := newTestStatsReporter()

	root, closer := NewRootScope(ScopeOptions{Prefix: "foo", Reporter: r}, 0)
	defer closer.Close()

	s := root.(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)

	s.report(r)
	r.WaitAll()

	assert.EqualValues(t, 21, r.counters["foo.bar"].val)
	assert.EqualValues(t, 1, r.gauges["foo.zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.blork"].val)
	assert.EqualValues(t, 1, r.histograms["foo.baz"].valueSamples[50.0])
}

func TestRootScopeWithDifferentSeparator(t *testing.T) {
	r := newTestStatsReporter()

	root, closer := NewRootScope(ScopeOptions{Prefix: "foo", Separator: "_", Reporter: r}, 0)
	defer closer.Close()

	s := root.(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)

	s.report(r)
	r.WaitAll()

	assert.EqualValues(t, 21, r.counters["foo_bar"].val)
	assert.EqualValues(t, 1, r.gauges["foo_zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo_blork"].val)
	assert.EqualValues(t, 1, r.histograms["foo_baz"].valueSamples[50.0])
}

func TestSubScope(t *testing.T) {
	r := newTestStatsReporter()

	root, closer := NewRootScope(ScopeOptions{Prefix: "foo", Reporter: r}, 0)
	defer closer.Close()

	tags := map[string]string{"foo": "bar"}
	s := root.Tagged(tags).SubScope("mork").(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)
	r.hg.Add(1)
	s.Histogram("baz", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)

	s.report(r)
	r.WaitAll()

	// Assert prefixed correctly
	assert.EqualValues(t, 21, r.counters["foo.mork.bar"].val)
	assert.EqualValues(t, 1, r.gauges["foo.mork.zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.mork.blork"].val)
	assert.EqualValues(t, 1, r.histograms["foo.mork.baz"].valueSamples[50.0])

	// Assert tags inherited
	assert.Equal(t, tags, r.counters["foo.mork.bar"].tags)
	assert.Equal(t, tags, r.gauges["foo.mork.zed"].tags)
	assert.Equal(t, tags, r.timers["foo.mork.blork"].tags)
	assert.Equal(t, tags, r.histograms["foo.mork.baz"].tags)
}

func TestTaggedSubScope(t *testing.T) {
	r := newTestStatsReporter()

	ts := map[string]string{"env": "test"}
	root, closer := NewRootScope(ScopeOptions{Prefix: "foo", Tags: ts, Reporter: r}, 0)
	defer closer.Close()

	s := root.(*scope)

	tscope := root.Tagged(map[string]string{"service": "test"}).(*scope)
	scope := root

	r.cg.Add(1)
	scope.Counter("beep").Inc(1)
	r.cg.Add(1)
	tscope.Counter("boop").Inc(1)
	r.hg.Add(1)
	scope.Histogram("baz", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)
	r.hg.Add(1)
	tscope.Histogram("bar", MustMakeLinearValueBuckets(0, 10, 10)).
		RecordValue(42.42)

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

func TestTaggedSanitizedSubScope(t *testing.T) {
	r := newTestStatsReporter()

	ts := map[string]string{"env": "test:env"}
	root, closer := NewRootScope(ScopeOptions{
		Prefix:          "foo",
		Tags:            ts,
		Reporter:        r,
		SanitizeOptions: &alphanumericSanitizerOpts,
	}, 0)
	defer closer.Close()

	s := root.(*scope)

	tscope := root.Tagged(map[string]string{"service": "test.service"}).(*scope)

	r.cg.Add(1)
	tscope.Counter("beep").Inc(1)

	s.report(r)
	tscope.report(r)
	r.cg.Wait()

	assert.EqualValues(t, 1, r.counters["foo_beep"].val)
	assert.EqualValues(t, map[string]string{
		"env":     "test_env",
		"service": "test_service",
	}, r.counters["foo_beep"].tags)
}

func TestTaggedExistingReturnsSameScope(t *testing.T) {
	r := newTestStatsReporter()

	for _, initialTags := range []map[string]string{
		nil,
		{"env": "test"},
	} {
		root, closer := NewRootScope(ScopeOptions{Prefix: "foo", Tags: initialTags, Reporter: r}, 0)
		defer closer.Close()

		rootScope := root.(*scope)
		fooScope := root.Tagged(map[string]string{"foo": "bar"}).(*scope)

		assert.NotEqual(t, rootScope, fooScope)
		assert.Equal(t, fooScope, fooScope.Tagged(nil))

		fooBarScope := fooScope.Tagged(map[string]string{"bar": "baz"}).(*scope)

		assert.NotEqual(t, fooScope, fooBarScope)
		assert.Equal(t, fooBarScope, fooScope.Tagged(map[string]string{"bar": "baz"}).(*scope))
	}
}

func TestSnapshot(t *testing.T) {
	commonTags := map[string]string{"env": "test"}
	s := NewTestScope("foo", map[string]string{"env": "test"})
	child := s.Tagged(map[string]string{"service": "test"})

	s.Counter("beep").Inc(1)
	s.Gauge("bzzt").Update(2)
	s.Timer("brrr").Record(1 * time.Second)
	s.Timer("brrr").Record(2 * time.Second)
	s.Histogram("fizz", ValueBuckets{0, 2, 4}).RecordValue(1)
	s.Histogram("fizz", ValueBuckets{0, 2, 4}).RecordValue(5)
	s.Histogram("buzz", DurationBuckets{time.Second * 2, time.Second * 4}).RecordDuration(time.Second)
	child.Counter("boop").Inc(1)

	snap := s.Snapshot()
	counters, gauges, timers, histograms :=
		snap.Counters(), snap.Gauges(), snap.Timers(), snap.Histograms()

	assert.EqualValues(t, 1, counters["foo.beep+env=test"].Value())
	assert.EqualValues(t, commonTags, counters["foo.beep+env=test"].Tags())

	assert.EqualValues(t, 2, gauges["foo.bzzt+env=test"].Value())
	assert.EqualValues(t, commonTags, gauges["foo.bzzt+env=test"].Tags())

	assert.EqualValues(t, []time.Duration{
		1 * time.Second,
		2 * time.Second,
	}, timers["foo.brrr+env=test"].Values())
	assert.EqualValues(t, commonTags, timers["foo.brrr+env=test"].Tags())

	assert.EqualValues(t, map[float64]int64{
		0:               0,
		2:               1,
		4:               0,
		math.MaxFloat64: 1,
	}, histograms["foo.fizz+env=test"].Values())
	assert.EqualValues(t, map[time.Duration]int64(nil), histograms["foo.fizz+env=test"].Durations())
	assert.EqualValues(t, commonTags, histograms["foo.fizz+env=test"].Tags())

	assert.EqualValues(t, map[float64]int64(nil), histograms["foo.buzz+env=test"].Values())
	assert.EqualValues(t, map[time.Duration]int64{
		time.Second * 2: 1,
		time.Second * 4: 0,
		math.MaxInt64:   0,
	}, histograms["foo.buzz+env=test"].Durations())
	assert.EqualValues(t, commonTags, histograms["foo.buzz+env=test"].Tags())

	assert.EqualValues(t, 1, counters["foo.boop+env=test,service=test"].Value())
	assert.EqualValues(t, map[string]string{
		"env":     "test",
		"service": "test",
	}, counters["foo.boop+env=test,service=test"].Tags())
}

func TestCapabilities(t *testing.T) {
	r := newTestStatsReporter()
	s, closer := NewRootScope(ScopeOptions{Reporter: r}, 0)
	defer closer.Close()
	assert.True(t, s.Capabilities().Reporting())
	assert.False(t, s.Capabilities().Tagging())
}

func TestCapabilitiesNoReporter(t *testing.T) {
	s, closer := NewRootScope(ScopeOptions{}, 0)
	defer closer.Close()
	assert.False(t, s.Capabilities().Reporting())
	assert.False(t, s.Capabilities().Tagging())
}

func TestNilTagMerge(t *testing.T) {
	assert.Nil(t, nil, mergeRightTags(nil, nil))
}

func TestScopeDefaultBuckets(t *testing.T) {
	r := newTestStatsReporter()

	root, closer := NewRootScope(ScopeOptions{
		DefaultBuckets: DurationBuckets{
			0 * time.Millisecond,
			30 * time.Millisecond,
			60 * time.Millisecond,
			90 * time.Millisecond,
			120 * time.Millisecond,
		},
		Reporter: r,
	}, 0)
	defer closer.Close()

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

	root, closer := NewRootScope(ScopeOptions{Reporter: r}, 0)
	defer closer.Close()

	s := root.(*scope)
	mets := newTestMets(s)

	r.cg.Add(1)
	mets.c.Inc(3)
	s.report(r)
	r.cg.Wait()

	assert.EqualValues(t, 3, r.counters["honk"].val)
}

func TestScopeAvoidReportLoopRunOnClose(t *testing.T) {
	r := newTestStatsReporter()
	root, closer := NewRootScope(ScopeOptions{Reporter: r}, 0)

	s := root.(*scope)
	s.reportLoopRun()

	assert.Equal(t, int32(1), atomic.LoadInt32(&r.flushes))

	assert.NoError(t, closer.Close())

	s.reportLoopRun()
	assert.Equal(t, int32(2), atomic.LoadInt32(&r.flushes))
}

func TestScopeFlushOnClose(t *testing.T) {
	r := newTestStatsReporter()
	root, closer := NewRootScope(ScopeOptions{Reporter: r}, time.Hour)

	r.cg.Add(1)
	root.Counter("foo").Inc(1)
	assert.Nil(t, r.counters["foo"])

	assert.NoError(t, closer.Close())

	assert.EqualValues(t, 1, r.counters["foo"].val)

	assert.NoError(t, closer.Close())
}
