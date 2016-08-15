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

	"github.com/facebookgo/clock"
)

// A Scope is a namespace wrapper around a stats reporter, ensuring that
// all emitted values have a given prefix or set of tags
type Scope interface {

	// Counter returns the Counter object corresponding to the name
	Counter(name string) Counter

	// Gauge returns the Gauge object corresponding to the name
	Gauge(name string) Gauge

	// Timer returns the Timer object corresponding to the name
	Timer(name string) Timer

	// SubScope returns a new child scope with the given name
	SubScope(name string) Scope

	// // Tagged returns a new scope with the given tags
	// Tagged(tags map[string]string) Scope

	report(r StatsReporter)
	scopedName(name string) string
}

// NoopScope is a scope that does nothing
var NoopScope = NewScope("", NullStatsReporter)

// NewScope creates a new Scope around a given stats reporter with the given prefix
func NewScope(prefix string, reporter StatsReporter) Scope {
	return &scope{
		prefix:   prefix,
		tags:     make(map[string]string),
		stats:    newStats(),
		reporter: reporter,
	}
}

type scope struct {
	prefix   string
	tags     map[string]string
	stats    *stats
	reporter StatsReporter
}

func (s *scope) report(r StatsReporter) {
	for name, counter := range s.stats.counters {
		counter.report(s.scopedName(name), s.tags, r)
	}

	for name, gauge := range s.stats.gauges {
		gauge.report(s.scopedName(name), s.tags, r)
	}

	// we do nothing for timers here because timers report directly to ths StatsReporter without buffering
}

// Counter returns the counter identified by the scope and the provided name, creating it if it does not already exist
func (s *scope) Counter(name string) Counter {
	s.stats.cm.RLock()
	val, ok := s.stats.counters[name]
	s.stats.cm.RUnlock()
	if !ok {
		val = &counter{}
		s.stats.cm.Lock()
		s.stats.counters[name] = val
		s.stats.cm.Unlock()
	}
	return val
}

// Gauge returns the gauge identified by the scope and the provided name, creating it if it does not already exist
func (s *scope) Gauge(name string) Gauge {
	s.stats.gm.RLock()
	val, ok := s.stats.gauges[name]
	s.stats.gm.RUnlock()
	if !ok {
		val = &gauge{}
		s.stats.gm.Lock()
		s.stats.gauges[name] = val
		s.stats.gm.Unlock()
	}
	return val
}

// Timer returns the timer identified by the scope and the provided name, creating it if it does not already exist
func (s *scope) Timer(name string) Timer {
	s.stats.tm.RLock()
	val, ok := s.stats.timers[name]
	s.stats.tm.RUnlock()
	if !ok {
		val = &timer{
			name:     s.scopedName(name),
			tags:     s.tags,
			reporter: s.reporter,
		}
		s.stats.tm.Lock()
		s.stats.timers[name] = val
		s.stats.tm.Unlock()
	}
	return val
}

func (s *scope) SubScope(prefix string) Scope {
	return &scope{
		prefix:   s.scopedName(prefix),
		tags:     s.tags,
		stats:    newStats(),
		reporter: s.reporter,
	}
}

func (s *scope) scopedName(name string) string {
	// TODO(mmihic): Consider maintaining a map[string]string for common names so we
	// avoid the cost of continual allocations
	if len(s.prefix) == 0 {
		return name
	}

	return fmt.Sprintf("%s.%s", s.prefix, name)
}

var globalClock = clock.New()
