// Copyright (c) 2017 Uber Technologies, Inc.
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
	"io"
	"sync"
	"time"

	"github.com/facebookgo/clock"
)

var (
	// NoopScope is a scope that does nothing
	NoopScope, _ = NewRootScope(ScopeOptions{Reporter: NullStatsReporter}, 0)

	// DefaultSeparator is the default separator used to join nested scopes
	DefaultSeparator = "."

	// DefaultTimersBufferMax is the default timers max buffer when not
	// reporting directly to a reporter and relying on snapshots
	DefaultTimersBufferMax = 1024

	globalClock = clock.New()

	defaultScopeBuckets = DurationBuckets{
		0 * time.Millisecond,
		10 * time.Millisecond,
		25 * time.Millisecond,
		50 * time.Millisecond,
		75 * time.Millisecond,
		100 * time.Millisecond,
		200 * time.Millisecond,
		300 * time.Millisecond,
		400 * time.Millisecond,
		500 * time.Millisecond,
		600 * time.Millisecond,
		800 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		5 * time.Second,
	}
)

type scope struct {
	separator       string
	prefix          string
	tags            map[string]string
	reporter        StatsReporter
	cachedReporter  CachedStatsReporter
	baseReporter    BaseStatsReporter
	defaultBuckets  Buckets
	timersBufferMax int

	registry *scopeRegistry
	status   scopeStatus

	cm sync.RWMutex
	gm sync.RWMutex
	tm sync.RWMutex
	hm sync.RWMutex

	counters   map[string]*counter
	gauges     map[string]*gauge
	timers     map[string]*timer
	histograms map[string]*histogram
}

type scopeStatus struct {
	sync.RWMutex
	closed bool
	quit   chan struct{}
}

type scopeRegistry struct {
	sync.RWMutex
	subscopes map[string]*scope
}

var scopeRegistryKey = KeyForPrefixedStringMap

// ScopeOptions is a set of options to construct a scope.
type ScopeOptions struct {
	Tags            map[string]string
	Prefix          string
	Reporter        StatsReporter
	CachedReporter  CachedStatsReporter
	Separator       string
	DefaultBuckets  Buckets
	TimersBufferMax int
}

// NewRootScope creates a new root Scope with a set of options and
// a reporting interval.
// Must provide either a StatsReporter or a CachedStatsReporter.
func NewRootScope(opts ScopeOptions, interval time.Duration) (Scope, io.Closer) {
	s := newRootScope(opts, interval)
	return s, s
}

// NewTestScope creates a new Scope without a stats reporter with the
// given prefix.
func NewTestScope(
	prefix string,
	tags map[string]string,
) Scope {
	return newRootScope(ScopeOptions{Prefix: prefix, Tags: tags}, 0)
}

func newRootScope(opts ScopeOptions, interval time.Duration) *scope {
	if opts.Tags == nil {
		opts.Tags = make(map[string]string)
	}
	if opts.Separator == "" {
		opts.Separator = DefaultSeparator
	}

	var baseReporter BaseStatsReporter
	if opts.Reporter != nil {
		baseReporter = opts.Reporter
	} else if opts.CachedReporter != nil {
		baseReporter = opts.CachedReporter
	}

	if opts.DefaultBuckets == nil || opts.DefaultBuckets.Len() < 1 {
		opts.DefaultBuckets = defaultScopeBuckets
	}

	if opts.TimersBufferMax <= 0 {
		opts.TimersBufferMax = DefaultTimersBufferMax
	}

	s := &scope{
		separator: opts.Separator,
		prefix:    opts.Prefix,
		// NB(r): Take a copy of the tags on creation
		// so that it cannot be modified after set.
		tags:            copyStringMap(opts.Tags),
		reporter:        opts.Reporter,
		cachedReporter:  opts.CachedReporter,
		baseReporter:    baseReporter,
		defaultBuckets:  opts.DefaultBuckets,
		timersBufferMax: opts.TimersBufferMax,

		registry: &scopeRegistry{
			subscopes: make(map[string]*scope),
		},
		status: scopeStatus{
			closed: false,
			quit:   make(chan struct{}, 1),
		},

		counters:   make(map[string]*counter),
		gauges:     make(map[string]*gauge),
		timers:     make(map[string]*timer),
		histograms: make(map[string]*histogram),
	}

	// Register the root scope
	s.registry.subscopes[scopeRegistryKey(s.prefix, s.tags)] = s

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

	s.hm.RLock()
	for name, histogram := range s.histograms {
		histogram.report(s.fullyQualifiedName(name), s.tags, r)
	}
	s.hm.RUnlock()

	r.Flush()
}

func (s *scope) cachedReport(c CachedStatsReporter) {
	s.cm.RLock()
	for _, counter := range s.counters {
		counter.cachedReport()
	}
	s.cm.RUnlock()

	s.gm.RLock()
	for _, gauge := range s.gauges {
		gauge.cachedReport()
	}
	s.gm.RUnlock()

	// we do nothing for timers here because timers report directly to ths StatsReporter without buffering

	s.hm.RLock()
	for _, histogram := range s.histograms {
		histogram.cachedReport()
	}
	s.hm.RUnlock()

	c.Flush()
}

// reportLoop is used by the root scope for periodic reporting
func (s *scope) reportLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.reportLoopRun()
		case <-s.status.quit:
			return
		}
	}
}

func (s *scope) reportLoopRun() {
	// Need to hold a status lock to ensure not to report
	// and flush after a close
	s.status.RLock()
	if s.status.closed {
		s.status.RUnlock()
		return
	}

	s.registry.RLock()
	if s.reporter != nil {
		for _, ss := range s.registry.subscopes {
			ss.report(s.reporter)
		}
	} else if s.cachedReporter != nil {
		for _, ss := range s.registry.subscopes {
			ss.cachedReport(s.cachedReporter)
		}
	}
	s.registry.RUnlock()

	s.status.RUnlock()
}

func (s *scope) Counter(name string) Counter {
	s.cm.RLock()
	val, ok := s.counters[name]
	s.cm.RUnlock()
	if !ok {
		s.cm.Lock()
		val, ok = s.counters[name]
		if !ok {
			var cachedCounter CachedCount
			if s.cachedReporter != nil {
				cachedCounter = s.cachedReporter.AllocateCounter(
					s.fullyQualifiedName(name), s.tags,
				)
			}
			val = newCounter(cachedCounter)
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
			var cachedGauge CachedGauge
			if s.cachedReporter != nil {
				cachedGauge = s.cachedReporter.AllocateGauge(
					s.fullyQualifiedName(name), s.tags,
				)
			}
			val = newGauge(cachedGauge)
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
			var cachedTimer CachedTimer
			if s.cachedReporter != nil {
				cachedTimer = s.cachedReporter.AllocateTimer(
					s.fullyQualifiedName(name), s.tags,
				)
			}
			opts := timerOptions{bufferMax: s.timersBufferMax}
			val = newTimer(
				s.fullyQualifiedName(name), s.tags, s.reporter, cachedTimer, opts,
			)
			s.timers[name] = val
		}
		s.tm.Unlock()
	}
	return val
}

func (s *scope) Histogram(name string, b Buckets) Histogram {
	if b == nil {
		b = s.defaultBuckets
	}

	s.hm.RLock()
	val, ok := s.histograms[name]
	s.hm.RUnlock()
	if !ok {
		s.hm.Lock()
		val, ok = s.histograms[name]
		if !ok {
			var cachedHistogram CachedHistogram
			if s.cachedReporter != nil {
				cachedHistogram = s.cachedReporter.AllocateHistogram(
					s.fullyQualifiedName(name), s.tags, b,
				)
			}
			val = newHistogram(
				s.fullyQualifiedName(name), s.tags, s.reporter, b, cachedHistogram,
			)
			s.histograms[name] = val
		}
		s.hm.Unlock()
	}
	return val
}

func (s *scope) Tagged(tags map[string]string) Scope {
	return s.subscope(s.prefix, tags)
}

func (s *scope) SubScope(prefix string) Scope {
	return s.subscope(s.fullyQualifiedName(prefix), nil)
}

func (s *scope) subscope(prefix string, tags map[string]string) Scope {
	tags = mergeRightTags(s.tags, tags)
	key := scopeRegistryKey(prefix, tags)

	s.registry.RLock()
	existing, ok := s.registry.subscopes[key]
	if ok {
		s.registry.RUnlock()
		return existing
	}
	s.registry.RUnlock()

	s.registry.Lock()
	defer s.registry.Unlock()

	existing, ok = s.registry.subscopes[key]
	if ok {
		return existing
	}

	subscope := &scope{
		separator: s.separator,
		prefix:    prefix,
		// NB(r): Take a copy of the tags on creation
		// so that it cannot be modified after set.
		tags:            copyStringMap(tags),
		reporter:        s.reporter,
		cachedReporter:  s.cachedReporter,
		baseReporter:    s.baseReporter,
		defaultBuckets:  s.defaultBuckets,
		timersBufferMax: s.timersBufferMax,

		registry: s.registry,

		counters:   make(map[string]*counter),
		gauges:     make(map[string]*gauge),
		timers:     make(map[string]*timer),
		histograms: make(map[string]*histogram),
	}

	s.registry.subscopes[key] = subscope
	return subscope
}

func (s *scope) Capabilities() Capabilities {
	if s.baseReporter == nil {
		return capabilitiesNone
	}
	return s.baseReporter.Capabilities()
}

func (s *scope) Snapshot() Snapshot {
	return s.snapshot(ResetOptions{})
}

func (s *scope) SnapshotReset(opts ResetOptions) Snapshot {
	return s.snapshot(opts)
}

func (s *scope) snapshot(opts ResetOptions) Snapshot {
	snap := newSnapshot()

	s.registry.RLock()
	for _, ss := range s.registry.subscopes {
		// NB(r): tags are immutable, no lock required to read.
		tags := make(map[string]string, len(s.tags))
		for k, v := range ss.tags {
			tags[k] = v
		}

		ss.cm.RLock()
		for key, c := range ss.counters {
			name := ss.fullyQualifiedName(key)
			id := KeyForPrefixedStringMap(name, tags)

			var value int64
			if opts.ResetCounters {
				value = c.snapshotReset()
			} else {
				value = c.snapshot()
			}
			snap.counters[id] = &counterSnapshot{
				name:  name,
				tags:  tags,
				value: value,
			}
		}
		ss.cm.RUnlock()
		ss.gm.RLock()
		for key, g := range ss.gauges {
			name := ss.fullyQualifiedName(key)
			id := KeyForPrefixedStringMap(name, tags)
			snap.gauges[id] = &gaugeSnapshot{
				name:  name,
				tags:  tags,
				value: g.snapshot(),
			}
		}
		ss.gm.RUnlock()
		ss.tm.RLock()
		for key, t := range ss.timers {
			name := ss.fullyQualifiedName(key)
			id := KeyForPrefixedStringMap(name, tags)

			var values []time.Duration
			if opts.ResetTimers {
				values = t.snapshotReset()
			} else {
				values = t.snapshot()
			}
			snap.timers[id] = &timerSnapshot{
				name:   name,
				tags:   tags,
				values: values,
			}
		}
		ss.tm.RUnlock()
		ss.hm.RLock()
		for key, h := range ss.histograms {
			name := ss.fullyQualifiedName(key)
			id := KeyForPrefixedStringMap(name, tags)

			var values map[float64]int64
			var durations map[time.Duration]int64
			if opts.ResetHistograms {
				values = h.snapshotResetValues()
				durations = h.snapshotResetDurations()
			} else {
				values = h.snapshotValues()
				durations = h.snapshotDurations()
			}
			snap.histograms[id] = &histogramSnapshot{
				name:      name,
				tags:      tags,
				values:    values,
				durations: durations,
			}
		}
		ss.hm.RUnlock()
	}
	s.registry.RUnlock()

	return snap
}

func (s *scope) Close() error {
	s.status.Lock()
	s.status.closed = true
	close(s.status.quit)
	s.status.Unlock()

	if closer, ok := s.baseReporter.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (s *scope) fullyQualifiedName(name string) string {
	if len(s.prefix) == 0 {
		return name
	}
	return fmt.Sprintf("%s%s%s", s.prefix, s.separator, name)
}

// mergeRightTags merges 2 sets of tags with the tags from tagsRight overriding values from tagsLeft
func mergeRightTags(tagsLeft, tagsRight map[string]string) map[string]string {
	if tagsLeft == nil && tagsRight == nil {
		return nil
	}
	if len(tagsRight) == 0 {
		return tagsLeft
	}
	if len(tagsLeft) == 0 {
		return tagsRight
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

func copyStringMap(stringMap map[string]string) map[string]string {
	result := make(map[string]string, len(stringMap))
	for k, v := range stringMap {
		result[k] = v
	}
	return result
}
