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

type scope interface {
	fullyQualifiedName(name string) string
}

// Scope is a namespace wrapper around a stats reporter, ensuring that
// all emitted values have a given prefix or set of tags
type Scope interface {
	scope

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

	// Report is the method that dumps a snapshot of all of the aggregated metrics into the StatsReporter via its Report* methods
	Report(r StatsReporter)

	// Reporter returns the underlying stats reporter which was used to initiate the Scope
	Reporter() StatsReporter

	// Tags returns the map of tags usef in Scope creation
	Tags() map[string]string

	// Prefix returns the prefix stirng used in Scope creation
	Prefix() string
}

// RootScope is a scope that manages itself and other Scopes
type RootScope interface {
	Scope

	// Close Ceases periodic reporting of the root scope and subscopes
	Close()
}

// NoopScope is a scope that does nothing
var NoopScope = NewRootScope("", nil, NullStatsReporter, 0)

type scopeRegistry struct {
	sm        sync.Mutex
	subscopes []Scope
}

func (r *scopeRegistry) add(subscope *standardScope) {
	r.sm.Lock()
	r.subscopes = append(r.subscopes, subscope)
	r.sm.Unlock()
}

type standardScope struct {
	prefix   string
	tags     map[string]string
	reporter StatsReporter

	registry *scopeRegistry
	quit     chan struct{}

	cm sync.RWMutex
	gm sync.RWMutex
	tm sync.RWMutex

	counters map[string]Counter
	gauges   map[string]Gauge
	timers   map[string]Timer
}

// NewRootScope creates a new Scope around a given stats reporter with the given prefix
func NewRootScope(prefix string, tags map[string]string, reporter StatsReporter, interval time.Duration) RootScope {
	if tags == nil {
		tags = make(map[string]string)
	}

	scope := &standardScope{
		prefix:   prefix,
		tags:     tags,
		reporter: reporter,

		registry: &scopeRegistry{},
		quit:     make(chan struct{}),

		counters: make(map[string]Counter),
		gauges:   make(map[string]Gauge),
		timers:   make(map[string]Timer),
	}

	scope.registry.add(scope)

	if interval > 0 {
		go scope.reportLoop(interval)
	}

	return scope
}

// Report dumps all aggregated stats into the reporter. Should be called automatically by the root scope periodically.
func (s *standardScope) Report(r StatsReporter) {
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
func (s *standardScope) reportLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-ticker.C:
			s.registry.sm.Lock()
			for _, ss := range s.registry.subscopes {
				ss.Report(s.reporter)
			}
			s.registry.sm.Unlock()
		case <-s.quit:
			return
		}
	}
}

func (s *standardScope) Close() {
	close(s.quit)
}

// Counter returns the counter identified by the scope and the provided name, creating it if it does not already exist
func (s *standardScope) Counter(name string) Counter {
	s.cm.RLock()
	val, ok := s.counters[name]
	s.cm.RUnlock()
	if !ok {
		s.cm.Lock()
		val, ok = s.counters[name]
		if !ok {
			val = &counter{}
			s.counters[name] = val
		}
		s.cm.Unlock()
	}
	return val
}

// Gauge returns the gauge identified by the scope and the provided name, creating it if it does not already exist
func (s *standardScope) Gauge(name string) Gauge {
	s.gm.RLock()
	val, ok := s.gauges[name]
	s.gm.RUnlock()
	if !ok {
		s.gm.Lock()
		val, ok = s.gauges[name]
		if !ok {
			val = &gauge{}
			s.gauges[name] = val
		}
		s.gm.Unlock()
	}
	return val
}

// Timer returns the timer identified by the scope and the provided name, creating it if it does not already exist
func (s *standardScope) Timer(name string) Timer {
	s.tm.RLock()
	val, ok := s.timers[name]
	s.tm.RUnlock()
	if !ok {
		s.tm.Lock()
		val, ok = s.timers[name]
		if !ok {
			val = &timer{
				name:     s.fullyQualifiedName(name),
				tags:     s.tags,
				reporter: s.reporter,
			}
			s.timers[name] = val
		}
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

func (s *standardScope) Tagged(tags map[string]string) Scope {
	subscope := &standardScope{
		prefix:   s.prefix,
		tags:     mergeRightTags(s.tags, tags),
		reporter: s.reporter,
		registry: s.registry,

		counters: make(map[string]Counter),
		gauges:   make(map[string]Gauge),
		timers:   make(map[string]Timer),
	}

	subscope.registry.add(subscope)
	return subscope
}

func (s *standardScope) SubScope(prefix string) Scope {
	subscope := &standardScope{
		prefix:   s.fullyQualifiedName(prefix),
		tags:     s.tags,
		reporter: s.reporter,
		registry: s.registry,

		counters: make(map[string]Counter),
		gauges:   make(map[string]Gauge),
		timers:   make(map[string]Timer),
	}

	subscope.registry.add(subscope)
	return subscope
}

func (s *standardScope) Reporter() StatsReporter {
	return s.reporter
}

func (s *standardScope) Tags() map[string]string {
	return s.tags
}

func (s *standardScope) Prefix() string {
	return s.prefix
}

func (s *standardScope) fullyQualifiedName(name string) string {
	// TODO(mmihic): Consider maintaining a map[string]string for common names so we
	// avoid the cost of continual allocations
	if len(s.prefix) == 0 {
		return name
	}

	return fmt.Sprintf("%s.%s", s.prefix, name)
}

var globalClock = clock.New()
