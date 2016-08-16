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

	// Tagged returns a new scope with the given tags
	Tagged(tags map[string]string) Scope

	Report(r StatsReporter)

	scopedName(name string) string
}

// NoopScope is a scope that does nothing
var NoopScope = NewScope("", nil, NullStatsReporter)

// NewScope creates a new Scope around a given stats reporter with the given prefix
func NewScope(prefix string, tags map[string]string, reporter StatsReporter) Scope {
	if tags == nil {
		tags = make(map[string]string)
	}
	return &scope{
		prefix:   prefix,
		tags:     tags,
		reporter: reporter,

		counters: make(map[string]Counter),
		gauges:   make(map[string]Gauge),
		timers:   make(map[string]Timer),
	}
}

type scope struct {
	prefix   string
	tags     map[string]string
	reporter StatsReporter

	cm sync.RWMutex
	gm sync.RWMutex
	tm sync.RWMutex

	counters map[string]Counter
	gauges   map[string]Gauge
	timers   map[string]Timer
}

func (s *scope) Report(r StatsReporter) {
	for name, counter := range s.counters {
		counter.report(s.scopedName(name), s.tags, r)
	}

	for name, gauge := range s.gauges {
		gauge.report(s.scopedName(name), s.tags, r)
	}

	// we do nothing for timers here because timers report directly to ths StatsReporter without buffering
}

// Counter returns the counter identified by the scope and the provided name, creating it if it does not already exist
func (s *scope) Counter(name string) Counter {
	s.cm.RLock()
	val, ok := s.counters[name]
	s.cm.RUnlock()
	if !ok {
		val = &counter{}
		s.cm.Lock()
		s.counters[name] = val
		s.cm.Unlock()
	}
	return val
}

// Gauge returns the gauge identified by the scope and the provided name, creating it if it does not already exist
func (s *scope) Gauge(name string) Gauge {
	s.gm.RLock()
	val, ok := s.gauges[name]
	s.gm.RUnlock()
	if !ok {
		val = &gauge{}
		s.gm.Lock()
		s.gauges[name] = val
		s.gm.Unlock()
	}
	return val
}

// Timer returns the timer identified by the scope and the provided name, creating it if it does not already exist
func (s *scope) Timer(name string) Timer {
	s.tm.RLock()
	val, ok := s.timers[name]
	s.tm.RUnlock()
	if !ok {
		val = &timer{
			name:     s.scopedName(name),
			tags:     s.tags,
			reporter: s.reporter,
		}
		s.tm.Lock()
		s.timers[name] = val
		s.tm.Unlock()
	}
	return val
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

func (s *scope) Tagged(tags map[string]string) Scope {
	return &scope{
		prefix:   s.prefix,
		tags:     mergeRightTags(s.tags, tags),
		reporter: s.reporter,

		counters: make(map[string]Counter),
		gauges:   make(map[string]Gauge),
		timers:   make(map[string]Timer),
	}
}

func (s *scope) SubScope(prefix string) Scope {
	return &scope{
		prefix:   s.scopedName(prefix),
		tags:     s.tags,
		reporter: s.reporter,

		counters: make(map[string]Counter),
		gauges:   make(map[string]Gauge),
		timers:   make(map[string]Timer),
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
