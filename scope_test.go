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

type testValue struct {
	val  int64
	tags map[string]string
}

type testStatsReporter struct {
	cg sync.WaitGroup
	gg sync.WaitGroup
	tg sync.WaitGroup

	scope Scope

	counters map[string]*testValue
	gauges   map[string]*testValue
	timers   map[string]*testValue
}

func (r *testStatsReporter) ReportCounter(name string, tags map[string]string, value int64) {
	r.counters[name] = &testValue{
		val:  value,
		tags: tags,
	}
	r.cg.Done()
}

func (r *testStatsReporter) ReportGauge(name string, tags map[string]string, value int64) {
	r.gauges[name] = &testValue{
		val:  value,
		tags: tags,
	}
	r.gg.Done()
}

func (r *testStatsReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	r.timers[name] = &testValue{
		val:  int64(interval),
		tags: tags,
	}
	r.tg.Done()
}

func (r *testStatsReporter) Flush() {}

// newTestStatsReporter returns a new TestStatsReporter
func newTestStatsReporter() *testStatsReporter {
	return &testStatsReporter{
		counters: make(map[string]*testValue),
		gauges:   make(map[string]*testValue),
		timers:   make(map[string]*testValue),
	}
}

func TestWriteTimerImmediately(t *testing.T) {
	r := newTestStatsReporter()
	scope := NewRootScope("", nil, r, 0)
	r.tg.Add(1)
	scope.Timer("ticky").Record(time.Millisecond * 175)
	r.tg.Wait()
}

func TestWriteTimerClosureImmediately(t *testing.T) {
	r := newTestStatsReporter()
	scope := NewRootScope("", nil, r, 0)
	r.tg.Add(1)
	scope.Timer("ticky").Begin()()
	r.tg.Wait()
}

func TestWriteReportLoop(t *testing.T) {
	r := newTestStatsReporter()
	scope := NewRootScope("", nil, r, 10)
	defer scope.Close()

	r.cg.Add(1)
	scope.Counter("bar").Inc(1)
	r.gg.Add(1)
	scope.Gauge("zed").Update(1)
	r.tg.Add(1)
	scope.Timer("ticky").Record(time.Millisecond * 175)

	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()
}

func TestWriteOnce(t *testing.T) {
	r := newTestStatsReporter()

	scope := NewRootScope("", nil, r, 0)

	r.cg.Add(1)
	scope.Counter("bar").Inc(1)
	r.gg.Add(1)
	scope.Gauge("zed").Update(1)
	r.tg.Add(1)
	scope.Timer("ticky").Record(time.Millisecond * 175)

	scope.Report(r)
	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()

	assert.EqualValues(t, 1, r.counters["bar"].val)
	assert.EqualValues(t, 1, r.gauges["zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["ticky"].val)

	r = newTestStatsReporter()
	scope.Report(r)

	assert.Nil(t, r.counters["bar"])
	assert.Nil(t, r.gauges["zed"])
	assert.Nil(t, r.timers["ticky"])
}

func TestRootScopeWithoutPrefix(t *testing.T) {
	r := newTestStatsReporter()

	scope := NewRootScope("", nil, r, 0)
	r.cg.Add(1)
	scope.Counter("bar").Inc(1)
	scope.Counter("bar").Inc(20)
	r.gg.Add(1)
	scope.Gauge("zed").Update(1)
	r.tg.Add(1)
	scope.Timer("blork").Record(time.Millisecond * 175)

	scope.Report(r)
	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()

	assert.EqualValues(t, 21, r.counters["bar"].val)
	assert.EqualValues(t, 1, r.gauges["zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["blork"].val)
}

func TestRootScopeWithPrefix(t *testing.T) {
	r := newTestStatsReporter()

	scope := NewRootScope("foo", nil, r, 0)
	r.cg.Add(1)
	scope.Counter("bar").Inc(1)
	scope.Counter("bar").Inc(20)
	r.gg.Add(1)
	scope.Gauge("zed").Update(1)
	r.tg.Add(1)
	scope.Timer("blork").Record(time.Millisecond * 175)

	scope.Report(r)
	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()

	assert.EqualValues(t, 21, r.counters["foo.bar"].val)
	assert.EqualValues(t, 1, r.gauges["foo.zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.blork"].val)
}

func TestSubScope(t *testing.T) {
	r := newTestStatsReporter()

	scope := NewRootScope("foo", nil, r, 0).SubScope("mork")
	r.cg.Add(1)
	scope.Counter("bar").Inc(1)
	scope.Counter("bar").Inc(20)
	r.gg.Add(1)
	scope.Gauge("zed").Update(1)
	r.tg.Add(1)
	scope.Timer("blork").Record(time.Millisecond * 175)

	scope.Report(r)
	r.cg.Wait()
	r.gg.Wait()
	r.tg.Wait()

	assert.EqualValues(t, 21, r.counters["foo.mork.bar"].val)
	assert.EqualValues(t, 1, r.gauges["foo.mork.zed"].val)
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.mork.blork"].val)
}

func TestTaggedSubScope(t *testing.T) {
	r := newTestStatsReporter()

	scope := NewRootScope("foo", nil, r, 0)

	ts := map[string]string{"service": "test"}
	tscope := scope.Tagged(ts)

	r.cg.Add(1)
	scope.Counter("beep").Inc(1)
	r.cg.Add(1)
	tscope.Counter("boop").Inc(1)

	scope.Report(r)
	tscope.Report(r)
	r.cg.Wait()

	assert.EqualValues(t, 1, r.counters["foo.beep"].val)
	assert.Empty(t, r.counters["foo.beep"].tags)

	assert.EqualValues(t, 1, r.counters["foo.boop"].val)
	assert.EqualValues(t, ts, r.counters["foo.boop"].tags)
}
