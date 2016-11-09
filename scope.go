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
	"fmt"
	"sync"
	"time"

	"github.com/facebookgo/clock"
)

var (
	// NoopScope is a scope that does nothing
	NoopScope = NewRootScope("", nil, NullStatsReporter, 0)

	globalClock = clock.New()
)

type scopeRegistry struct {
	sm        sync.RWMutex
	subscopes []*scope
}

func (r *scopeRegistry) add(subscope *scope) {
	r.sm.Lock()
	r.subscopes = append(r.subscopes, subscope)
	r.sm.Unlock()
}

type scope struct {
	prefix   string
	tags     map[string]string
	reporter StatsReporter

	registry *scopeRegistry
	quit     chan struct{}

	cm sync.RWMutex
	gm sync.RWMutex
	tm sync.RWMutex

	counters map[string]*counter
	gauges   map[string]*gauge
	timers   map[string]*timer
}

// NewRootScope creates a new Scope around a given stats reporter with the given prefix
func NewRootScope(prefix string, tags map[string]string, reporter StatsReporter, interval time.Duration) RootScope {
	if tags == nil {
		tags = make(map[string]string)
	}

	s := &scope{
		prefix:   prefix,
		tags:     tags,
		reporter: reporter,

		registry: &scopeRegistry{},
		quit:     make(chan struct{}),

		counters: make(map[string]*counter),
		gauges:   make(map[string]*gauge),
		timers:   make(map[string]*timer),
	}

	s.registry.add(s)

	if interval > 0 {
		go s.reportLoop(interval)
	}

	return s
}

// report dumps all aggregated stats into the reporter. Should be called automatically by the root scope periodically.
func (s *scope) report(r StatsReporter) {
	s.cm.RLock()
	for name, counter := range s.counters {
		counter.report(s.fullyQualifiedName(name), s.tags, r)
	}
	s.cm.RUnlock()

	s.gm.RLock()
	for name, gauge := range s.gauges {
		gauge.report(s.fullyQualifiedName(name), s.tags, r)
	}
	s.gm.RUnlock()

	// we do nothing for timers here because timers report directly to ths StatsReporter without buffering

	r.Flush()
}

// reportLoop is used by the root scope for periodic reporting
func (s *scope) reportLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-ticker.C:
			s.registry.sm.Lock()
			for _, ss := range s.registry.subscopes {
				ss.report(s.reporter)
			}
			s.registry.sm.Unlock()
		case <-s.quit:
			return
		}
	}
}

func (s *scope) Counter(name string) Counter {
	s.cm.RLock()
	val, ok := s.counters[name]
	s.cm.RUnlock()
	if !ok {
		s.cm.Lock()
		val, ok = s.counters[name]
		if !ok {
			val = newCounter()
			s.counters[name] = val
		}
		s.cm.Unlock()
	}
	return val
}

func (s *scope) Gauge(name string) Gauge {
	s.gm.RLock()
	val, ok := s.gauges[name]
	s.gm.RUnlock()
	if !ok {
		s.gm.Lock()
		val, ok = s.gauges[name]
		if !ok {
			val = newGauge()
			s.gauges[name] = val
		}
		s.gm.Unlock()
	}
	return val
}

func (s *scope) Timer(name string) Timer {
	s.tm.RLock()
	val, ok := s.timers[name]
	s.tm.RUnlock()
	if !ok {
		s.tm.Lock()
		val, ok = s.timers[name]
		if !ok {
			val = newTimer(s.fullyQualifiedName(name), s.tags, s.reporter)
			s.timers[name] = val
		}
		s.tm.Unlock()
	}
	return val
}

func (s *scope) Tagged(tags map[string]string) Scope {
	subscope := &scope{
		prefix:   s.prefix,
		tags:     mergeRightTags(s.tags, tags),
		reporter: s.reporter,
		registry: s.registry,

		counters: make(map[string]*counter),
		gauges:   make(map[string]*gauge),
		timers:   make(map[string]*timer),
	}

	subscope.registry.add(subscope)
	return subscope
}

func (s *scope) SubScope(prefix string) Scope {
	subscope := &scope{
		prefix:   s.fullyQualifiedName(prefix),
		tags:     s.tags,
		reporter: s.reporter,
		registry: s.registry,

		counters: make(map[string]*counter),
		gauges:   make(map[string]*gauge),
		timers:   make(map[string]*timer),
	}

	subscope.registry.add(subscope)
	return subscope
}

func (s *scope) Capabilities() Capabilities {
	if s.reporter == nil {
		return capabilitiesNone
	}
	return s.reporter.Capabilities()
}

func (s *scope) Snapshot() Snapshot {
	snap := newSnapshot()

	s.registry.sm.RLock()
	for _, ss := range s.registry.subscopes {
		// NB(r): tags are immutable, no lock required to read.
		tags := make(map[string]string, len(s.tags))
		for k, v := range ss.tags {
			tags[k] = v
		}

		ss.cm.RLock()
		for key, c := range ss.counters {
			name := ss.fullyQualifiedName(key)
			snap.counters[name] = &counterSnapshot{
				name:  name,
				tags:  tags,
				value: c.snapshot(),
			}
		}
		ss.cm.RUnlock()
		ss.gm.RLock()
		for key, g := range ss.gauges {
			name := ss.fullyQualifiedName(key)
			snap.gauges[name] = &gaugeSnapshot{
				name:  name,
				tags:  tags,
				value: g.snapshot(),
			}
		}
		ss.gm.RUnlock()
		ss.tm.RLock()
		for key, t := range ss.timers {
			name := ss.fullyQualifiedName(key)
			snap.timers[name] = &timerSnapshot{
				name:   name,
				tags:   tags,
				values: t.snapshot(),
			}
		}
		ss.tm.RUnlock()
	}
	s.registry.sm.RUnlock()

	return snap
}

func (s *scope) Close() {
	close(s.quit)
}

func (s *scope) fullyQualifiedName(name string) string {
	// TODO(mmihic): Consider maintaining a map[string]string for common names so we
	// avoid the cost of continual allocations
	if len(s.prefix) == 0 {
		return name
	}

	return fmt.Sprintf("%s.%s", s.prefix, name)
}

// mergeRightTags merges 2 sets of tags with the tags from tagsRight overriding values from tagsLeft
func mergeRightTags(tagsLeft, tagsRight map[string]string) map[string]string {
	if tagsLeft == nil && tagsRight == nil {
		return nil
	}

	result := make(map[string]string, len(tagsLeft)+len(tagsRight))
	for k, v := range tagsLeft {
		result[k] = v
	}
	for k, v := range tagsRight {
		result[k] = v
	}
	return result
}

type snapshot struct {
	counters map[string]CounterSnapshot
	gauges   map[string]GaugeSnapshot
	timers   map[string]TimerSnapshot
}

func newSnapshot() *snapshot {
	return &snapshot{
		counters: make(map[string]CounterSnapshot),
		gauges:   make(map[string]GaugeSnapshot),
		timers:   make(map[string]TimerSnapshot),
	}
}

func (s *snapshot) Counters() map[string]CounterSnapshot {
	return s.counters
}

func (s *snapshot) Gauges() map[string]GaugeSnapshot {
	return s.gauges
}

func (s *snapshot) Timers() map[string]TimerSnapshot {
	return s.timers
}

type counterSnapshot struct {
	name  string
	tags  map[string]string
	value int64
}

func (s *counterSnapshot) Name() string {
	return s.name
}

func (s *counterSnapshot) Tags() map[string]string {
	return s.tags
}

func (s *counterSnapshot) Value() int64 {
	return s.value
}

type gaugeSnapshot struct {
	name  string
	tags  map[string]string
	value int64
}

func (s *gaugeSnapshot) Name() string {
	return s.name
}

func (s *gaugeSnapshot) Tags() map[string]string {
	return s.tags
}

func (s *gaugeSnapshot) Value() int64 {
	return s.value
}

type timerSnapshot struct {
	name   string
	tags   map[string]string
	values []time.Duration
}

func (s *timerSnapshot) Name() string {
	return s.name
}

func (s *timerSnapshot) Tags() map[string]string {
	return s.tags
}

func (s *timerSnapshot) Values() []time.Duration {
	return s.values
}
