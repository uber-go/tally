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
	"sync/atomic"
	"time"
)

// StatsReporter is the bridge between Scopes/metrics and the system where the metrics get sent in the end.
// This interface should be inmplemented for your specific stats backend.
type StatsReporter interface {
	ReportCounter(name string, tags map[string]string, value int64)
	ReportGauge(name string, tags map[string]string, value int64)
	ReportTimer(name string, tags map[string]string, interval time.Duration)
}

type reportableMetric interface {
	report(name string, tags map[string]string, r StatsReporter)
}

// Counter is the interface for logging statsd-counter-type metrics
type Counter interface {
	reportableMetric

	Inc(int64)
}

// Gauge is the interface for logging statsd-gauge-type metrics
type Gauge interface {
	reportableMetric

	Update(int64)
}

// Timer is the interface for logging statsd-timer-type metrics
type Timer interface {
	Record(time.Duration)
	Begin() func()
}

type counter struct {
	prev int64
	curr int64
}

type gauge struct {
	updated int64
	curr    int64
}

type timer struct {
	// Timers are a little special becuase they do no aggregate any data at the timer level. The
	// reporter buffers may timer entries and periodically flushes
	name     string
	tags     map[string]string
	reporter StatsReporter
}

func (c *counter) Inc(v int64) {
	atomic.AddInt64(&c.curr, v)
}

func (c *counter) report(name string, tags map[string]string, r StatsReporter) {
	curr := atomic.LoadInt64(&c.curr)

	prev := c.prev
	if prev == curr {
		return
	}
	atomic.StoreInt64(&c.prev, curr)
	r.ReportCounter(name, tags, curr-prev)
}

func (g *gauge) Update(v int64) {
	atomic.StoreInt64(&g.curr, v)
	atomic.StoreInt64(&g.updated, 1)
}

func (g *gauge) report(name string, tags map[string]string, r StatsReporter) {
	if atomic.SwapInt64(&g.updated, 0) == 1 {
		r.ReportGauge(name, tags, atomic.LoadInt64(&g.curr))
	}
}

func (t *timer) Begin() func() {
	start := globalClock.Now()
	return func() {
		t.Record(globalClock.Now().Sub(start))
	}
}

func (t *timer) Record(interval time.Duration) {
	t.reporter.ReportTimer(t.name, t.tags, interval)
}

// NullStatsReporter is an implementatin of StatsReporter than simply does nothing.
var NullStatsReporter StatsReporter = nullStatsReporter{}

func (r nullStatsReporter) ReportCounter(name string, tags map[string]string, value int64)          {}
func (r nullStatsReporter) ReportGauge(name string, tags map[string]string, value int64)            {}
func (r nullStatsReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {}

type nullStatsReporter struct{}
