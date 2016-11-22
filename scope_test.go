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

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type testIntValue struct {
	val  int64
	tags map[string]string
}

type testFloatValue struct {
	val  float64
	tags map[string]string
}

type testStatsReporter struct {
	cg sync.WaitGroup
	gg sync.WaitGroup
	tg sync.WaitGroup

	scope Scope

	counters   map[string]*testIntValue
	gauges     map[string]*testFloatValue
	histograms map[string]*testFloatValue
	timers     map[string]*testIntValue
}

// newTestStatsReporter returns a new TestStatsReporter
func newTestStatsReporter() *testStatsReporter {
	return &testStatsReporter{
		counters:   make(map[string]*testIntValue),
		gauges:     make(map[string]*testFloatValue),
		histograms: make(map[string]*testFloatValue),
		timers:     make(map[string]*testIntValue),
	}
}

func (r *testStatsReporter) ReportCounter(name string, tags map[string]string, value int64) {
	r.counters[name] = &testIntValue{
		val:  value,
		tags: tags,
	}
	r.cg.Done()
}

func (r *testStatsReporter) ReportGauge(name string, tags map[string]string, value float64) {
	r.gauges[name] = &testFloatValue{
		val:  value,
		tags: tags,
	}
	r.gg.Done()
}

func (r *testStatsReporter) ReportHistogram(name string, tags map[string]string, value float64) {
	r.histograms[name] = &testFloatValue{
		val:  value,
		tags: tags,
	}
	r.gg.Done()
}

func (r *testStatsReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	r.timers[name] = &testIntValue{
		val:  int64(interval),
		tags: tags,
	}
	r.tg.Done()
}

func (r *testStatsReporter) Capabilities() Capabilities {
	return capabilitiesReportingNoTagging
}

func (r *testStatsReporter) Flush() {}

func TestWriteTimerImmediately(t *testing.T) {
	r := newTestStatsReporter()
	s, _ := NewRootScope("", nil, r, 0)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)
	r.tg.Wait()
}

func TestWriteTimerClosureImmediately(t *testing.T) {
	r := newTestStatsReporter()
	s, _ := NewRootScope("", nil, r, 0)
	r.tg.Add(1)
	tm := s.Timer("ticky")
	tm.Stop(tm.Start())
	r.tg.Wait()
}

func TestWriteReportLoop(t *testing.T) {
	r := newTestStatsReporter()
	s, close := NewRootScope("", nil, r, 10)
	defer close.Close()

	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)

	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()
}

func TestWriteOnce(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope("", nil, r, 0)
	s := root.(*scope)

	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("ticky").Record(time.Millisecond * 175)

	s.report(r)
	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()

	assert.EqualValues(t, 1, r.counters["bar"].val)
	assert.EqualValues(t, 1, r.gauges["zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["ticky"].val)

	r = newTestStatsReporter()
	s.report(r)

	assert.Nil(t, r.counters["bar"])
	assert.Nil(t, r.gauges["zed"])
	assert.Nil(t, r.timers["ticky"])
}

func TestRootScopeWithoutPrefix(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope("", nil, r, 0)
	s := root.(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)

	s.report(r)
	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()

	assert.EqualValues(t, 21, r.counters["bar"].val)
	assert.EqualValues(t, 1, r.gauges["zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["blork"].val)
}

func TestRootScopeWithPrefix(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope("foo", nil, r, 0)
	s := root.(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)

	s.report(r)
	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()

	assert.EqualValues(t, 21, r.counters["foo.bar"].val)
	assert.EqualValues(t, 1, r.gauges["foo.zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.blork"].val)
}

func TestSubScope(t *testing.T) {
	r := newTestStatsReporter()

	root, _ := NewRootScope("foo", nil, r, 0)
	s := root.SubScope("mork").(*scope)
	r.cg.Add(1)
	s.Counter("bar").Inc(1)
	s.Counter("bar").Inc(20)
	r.gg.Add(1)
	s.Gauge("zed").Update(1)
	r.tg.Add(1)
	s.Timer("blork").Record(time.Millisecond * 175)

	s.report(r)
	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()

	assert.EqualValues(t, 21, r.counters["foo.mork.bar"].val)
	assert.EqualValues(t, 1, r.gauges["foo.mork.zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.mork.blork"].val)
}

func TestTaggedSubScope(t *testing.T) {
	r := newTestStatsReporter()

	ts := map[string]string{"env": "test"}
	root, _ := NewRootScope("foo", ts, r, 0)
	s := root.(*scope)

	tscope := root.Tagged(map[string]string{"service": "test"}).(*scope)
	scope := root

	r.cg.Add(1)
	scope.Counter("beep").Inc(1)
	r.cg.Add(1)
	tscope.Counter("boop").Inc(1)

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
}

func TestSnapshot(t *testing.T) {
	commonTags := map[string]string{"env": "test"}
	s := NewTestScope("foo", map[string]string{"env": "test"})
	child := s.Tagged(map[string]string{"service": "test"})

	s.Counter("beep").Inc(1)
	s.Gauge("bzzt").Update(2)
	s.Histogram("bading").Record(3.1)
	s.Histogram("bading").Record(4.2)
	s.Timer("brrr").Record(1 * time.Second)
	s.Timer("brrr").Record(2 * time.Second)
	child.Counter("boop").Inc(1)

	snap := s.Snapshot()
	counters, gauges, histograms, timers :=
		snap.Counters(), snap.Gauges(), snap.Histograms(), snap.Timers()

	assert.EqualValues(t, 1, counters["foo.beep"].Value())
	assert.EqualValues(t, commonTags, counters["foo.beep"].Tags())

	assert.EqualValues(t, 2, gauges["foo.bzzt"].Value())
	assert.EqualValues(t, commonTags, gauges["foo.bzzt"].Tags())

	assert.EqualValues(t, []float64{
		3.1,
		4.2,
	}, histograms["foo.bading"].Values())
	assert.EqualValues(t, commonTags, histograms["foo.bading"].Tags())

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
	s, _ := NewRootScope("prefix", nil, r, 0)
	assert.True(t, s.Capabilities().Reporting())
	assert.False(t, s.Capabilities().Tagging())
}

func TestCapabilitiesNoReporter(t *testing.T) {
	s, _ := NewRootScope("prefix", nil, nil, 0)
	assert.False(t, s.Capabilities().Reporting())
	assert.False(t, s.Capabilities().Tagging())
}

func TestNilTagMerge(t *testing.T) {
	assert.Nil(t, nil, mergeRightTags(nil, nil))
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

	root, _ := NewRootScope("", nil, r, 0)
	s := root.(*scope)
	mets := newTestMets(s)

	r.cg.Add(1)
	mets.c.Inc(3)
	s.report(r)
	r.cg.Wait()

	assert.EqualValues(t, 3, r.counters["honk"].val)
}
