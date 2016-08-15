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
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
)

var (
	errNoData = errors.New("No data")
)

// Counter is the interface for logging statsd-counter-type metrics
type Counter interface {
	Inc(int64)
	report(name string, tags map[string]string, r StatsReporter) error
}

// Gauge is the interface for logging statsd-gauge-type metrics
type Gauge interface {
	Update(int64)
	report(name string, tags map[string]string, r StatsReporter) error
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

func (c *counter) report(name string, tags map[string]string, r StatsReporter) error {
	curr := atomic.LoadInt64(&c.curr)
	prev := c.prev
	if prev == curr {
		return errNoData
	}
	atomic.StoreInt64(&c.prev, curr)
	r.reportCounter(name, tags, curr-prev)
	return nil
}

func (g *gauge) Update(v int64) {
	// Do the gaugey thing
	atomic.StoreInt64(&g.curr, v)
	atomic.StoreInt64(&g.updated, 1)
}

func (g *gauge) report(name string, tags map[string]string, r StatsReporter) error {
	updated := atomic.SwapInt64(&g.updated, 0)
	if updated > 0 {
		curr := atomic.LoadInt64(&g.curr)
		r.reportGauge(name, tags, curr)
		return nil
	}
	return errNoData

}

func (t *timer) Begin() func() {
	start := globalClock.Now()
	return func() {
		t.Record(globalClock.Now().Sub(start))
	}
}

func (t *timer) Record(interval time.Duration) {
	t.reporter.reportTimer(t.name, t.tags, interval)
}

// StatsReporter is the bridge between Scopes/metrics and the system where the metrics get sent in the end.
type StatsReporter interface {
	reportCounter(name string, tags map[string]string, value int64)
	reportGauge(name string, tags map[string]string, value int64)
	reportTimer(name string, tags map[string]string, interval time.Duration)
}

// NullStatsReporter is an implementatin of StatsReporter than simply does nothing.
var NullStatsReporter StatsReporter = nullStatsReporter{}

func (r nullStatsReporter) reportCounter(name string, tags map[string]string, value int64)          {}
func (r nullStatsReporter) reportGauge(name string, tags map[string]string, value int64)            {}
func (r nullStatsReporter) reportTimer(name string, tags map[string]string, interval time.Duration) {}

type nullStatsReporter struct{}

type cactusStatsReporter struct {
	statter  statsd.Statter
	sm       sync.Mutex
	scopes   []Scope
	interval time.Duration
	quit     chan struct{}
}

// NewCactusStatsReporter returns a new StatsReporter that creates a buffered client to a Statsd backend
func NewCactusStatsReporter(statsd statsd.Statter, interval time.Duration) StatsReporter {
	return &cactusStatsReporter{
		quit:     make(chan struct{}),
		statter:  statsd,
		interval: interval,
		scopes:   make([]Scope, 0),
	}
}

func (r *cactusStatsReporter) Start() {
	ticker := time.NewTicker(r.interval)
	for {
		select {
		case <-ticker.C:
			for _, scope := range r.scopes {
				scope.report(r)
			}
		case <-r.quit:
			return
		}
	}
}

func (r *cactusStatsReporter) registerScope(s Scope) {
	r.sm.Lock()
	r.scopes = append(r.scopes, s)
	r.sm.Unlock()
}

func (r *cactusStatsReporter) reportCounter(name string, tags map[string]string, value int64) {
	r.statter.Inc(name, value, 1.0)
}

func (r *cactusStatsReporter) reportGauge(name string, tags map[string]string, value int64) {
	r.statter.Gauge(name, value, 1.0)
}

func (r *cactusStatsReporter) reportTimer(name string, tags map[string]string, interval time.Duration) {
	r.statter.TimingDuration(name, interval, 1.0)
}

type stats struct {
	cm sync.RWMutex
	gm sync.RWMutex
	tm sync.RWMutex

	counters map[string]Counter
	gauges   map[string]Gauge
	timers   map[string]Timer
}

func newStats() *stats {
	return &stats{
		counters: make(map[string]Counter),
		gauges:   make(map[string]Gauge),
		timers:   make(map[string]Timer),
	}
}
