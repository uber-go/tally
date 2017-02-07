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

	globalClock = clock.New()
)

type scope struct {
	separator              string
	prefix                 string
	tags                   map[string]string
	reporter               StatsReporter
	cachedReporter         CachedStatsReporter
	baseReporter           BaseStatsReporter
	defaultValueBuckets    []Bucket
	defaultDurationBuckets []Bucket

	registry *scopeRegistry
	quit     chan struct{}

	cm sync.RWMutex
	gm sync.RWMutex
	tm sync.RWMutex

	counters   map[string]*counter
	gauges     map[string]*gauge
	timers     map[string]*timer
	histograms map[string]*histogram
}

type scopeRegistry struct {
	sync.RWMutex
	subscopes map[string]*scope
}

var scopeRegistryKey = KeyForPrefixedStringMap

// ScopeOptions is a set of options to construct a scope.
type ScopeOptions struct {
	Tags                   map[string]string
	Prefix                 string
	Reporter               StatsReporter
	CachedReporter         CachedStatsReporter
	Separator              string
	DefaultValueBuckets    []Bucket
	DefaultDurationBuckets []Bucket
}

// NewRootScope creates a new root Scope with a set of options and
// a reporting interval.
// Must provide either a StatsReporter or a CachedStatsReporter.
func NewRootScope(opts ScopeOptions, interval time.Duration) (Scope, io.Closer) {
	s := newRootScope(opts, interval)
	return s, s
}

// NewTestScope creates a new Scope without a stats reporter with the
// given prefix and adds the ability to take snapshots of metrics emitted
// to it.
func NewTestScope(
	prefix string,
	tags map[string]string,
) TestScope {
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

	// TODO: validate that opts.DefaultValueBuckets is valid
	// TODO: validate that opts.DefaultHistogramBuckets is valid

	s := &scope{
		separator: opts.Separator,
		prefix:    opts.Prefix,
		// NB(r): Take a copy of the tags on creation
		// so that it cannot be modified after set.
		tags:                   copyStringMap(opts.Tags),
		reporter:               opts.Reporter,
		cachedReporter:         opts.CachedReporter,
		baseReporter:           baseReporter,
		defaultValueBuckets:    opts.DefaultValueBuckets,
		defaultDurationBuckets: opts.DefaultDurationBuckets,

		registry: &scopeRegistry{
			subscopes: make(map[string]*scope),
		},
		quit: make(chan struct{}, 1),

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

	c.Flush()
}

// reportLoop is used by the root scope for periodic reporting
func (s *scope) reportLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-ticker.C:
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
			val = newTimer(
				s.fullyQualifiedName(name), s.tags, s.reporter, cachedTimer,
			)
			s.timers[name] = val
		}
		s.tm.Unlock()
	}
	return val
}

func (s *scope) Histogram(name string, b ...Bucket) Histogram {
	var (
		valueBuckets    []Bucket
		durationBuckets []Bucket
	)
	if len(b) == 0 {
		valueBuckets = s.defaultValueBuckets
		durationBuckets = s.defaultDurationBuckets
	} else {
		// TODO: verify valid buckets
		valueBuckets = b
		durationBuckets = b
	}

	key := name +
		Buckets(valueBuckets).String() +
		Buckets(durationBuckets).String()

	s.tm.RLock()
	val, ok := s.histograms[key]
	s.tm.RUnlock()
	if !ok {
		s.tm.Lock()
		val, ok = s.histograms[key]
		if !ok {
			var cachedHistogram CachedHistogram
			if s.cachedReporter != nil {
				cachedHistogram = s.cachedReporter.AllocateHistogram(
					s.fullyQualifiedName(name), s.tags,
					Buckets(valueBuckets).Values(),
					Buckets(durationBuckets).Durations(),
				)
			}
			val = newHistogram(
				s.fullyQualifiedName(name), s.tags, s.reporter,
				Buckets(valueBuckets).Values(),
				Buckets(durationBuckets).Durations(),
				cachedHistogram,
			)
			s.histograms[name] = val
		}
		s.tm.Unlock()
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
	if len(tags) == 0 {
		tags = s.tags
	} else {
		tags = mergeRightTags(s.tags, tags)
	}
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
		tags:           copyStringMap(tags),
		reporter:       s.reporter,
		cachedReporter: s.cachedReporter,
		baseReporter:   s.baseReporter,
		registry:       s.registry,

		counters: make(map[string]*counter),
		gauges:   make(map[string]*gauge),
		timers:   make(map[string]*timer),
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
		// TODO: histograms in snapshot
	}
	s.registry.RUnlock()

	return snap
}

func (s *scope) Close() error {
	close(s.quit)
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

// TestScope is a metrics collector that has no reporting, ensuring that
// all emitted values have a given prefix or set of tags
type TestScope interface {
	Scope

	// Snapshot returns a copy of all values since the last report execution,
	// this is an expensive operation and should only be use for testing purposes
	Snapshot() Snapshot
}

// Snapshot is a snapshot of values since last report execution
type Snapshot interface {
	// Counters returns a snapshot of all counter summations since last report execution
	Counters() map[string]CounterSnapshot

	// Gauges returns a snapshot of gauge last values since last report execution
	Gauges() map[string]GaugeSnapshot

	// Timers returns a snapshot of timer values since last report execution
	Timers() map[string]TimerSnapshot
}

// CounterSnapshot is a snapshot of a counter
type CounterSnapshot interface {
	// Name returns the name
	Name() string

	// Tags returns the tags
	Tags() map[string]string

	// Value returns the value
	Value() int64
}

// GaugeSnapshot is a snapshot of a gauge
type GaugeSnapshot interface {
	// Name returns the name
	Name() string

	// Tags returns the tags
	Tags() map[string]string

	// Value returns the value
	Value() float64
}

// TimerSnapshot is a snapshot of a timer
type TimerSnapshot interface {
	// Name returns the name
	Name() string

	// Tags returns the tags
	Tags() map[string]string

	// Values returns the values
	Values() []time.Duration
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
	value float64
}

func (s *gaugeSnapshot) Name() string {
	return s.name
}

func (s *gaugeSnapshot) Tags() map[string]string {
	return s.tags
}

func (s *gaugeSnapshot) Value() float64 {
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
