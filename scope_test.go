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

type testStatsReporter struct {
	cg sync.WaitGroup
	gg sync.WaitGroup
	tg sync.WaitGroup

	scope Scope

	counters map[string]int64
	gauges   map[string]int64
	timers   map[string]time.Duration
}

func (r *testStatsReporter) ReportCounter(name string, tags map[string]string, value int64) {
	r.counters[name] = value
	r.cg.Done()
}

func (r *testStatsReporter) ReportGauge(name string, tags map[string]string, value int64) {
	r.gauges[name] = value
	r.gg.Done()
}

func (r *testStatsReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	r.timers[name] = interval
	r.tg.Done()
}

func (r *testStatsReporter) Flush() {}

// newTestStatsReporter returns a new TestStatsReporter
func newTestStatsReporter() *testStatsReporter {
	return &testStatsReporter{
		counters: make(map[string]int64),
		gauges:   make(map[string]int64),
		timers:   make(map[string]time.Duration),
	}
}

func TestWriteTimerImmediately(t *testing.T) {
	r := newTestStatsReporter()
	scope := NewRootScope("", nil, r, 0)
	r.tg.Add(1)
	scope.Timer("ticky").Record(time.Millisecond * 175)
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

	assert.EqualValues(t, 1, r.counters["bar"])
	assert.EqualValues(t, 1, r.gauges["zed"])
	assert.EqualValues(t, time.Millisecond*175, r.timers["ticky"])

	r = newTestStatsReporter()
	scope.Report(r)

	assert.EqualValues(t, 0, r.counters["bar"])
	assert.EqualValues(t, 0, r.gauges["zed"])
	assert.EqualValues(t, 0, r.timers["ticky"])
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

	assert.EqualValues(t, 21, r.counters["bar"])
	assert.EqualValues(t, 1, r.gauges["zed"])
	assert.EqualValues(t, time.Millisecond*175, r.timers["blork"])
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

	assert.EqualValues(t, 21, r.counters["foo.bar"])
	assert.EqualValues(t, 1, r.gauges["foo.zed"])
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.blork"])
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

	assert.EqualValues(t, 21, r.counters["foo.mork.bar"])
	assert.EqualValues(t, 1, r.gauges["foo.mork.zed"])
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.mork.blork"])
}
