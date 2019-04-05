// Copyright (c) 2019 Uber Technologies, Inc.
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
	"sync/atomic"
	"time"
)

var (
	// NoopScope is a scope that does nothing
	NoopScope, _ = NewRootScope(ScopeOptions{Reporter: NullStatsReporter}, 0)
	// DefaultSeparator is the default separator used to join nested scopes
	DefaultSeparator = "."

	globalNow = time.Now

	// defaultExpiry is the default expiry period for scopes and metrics
	defaultExpiry = 11 * time.Minute

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
	separator           string
	prefix              string
	tags                map[string]string
	reporter            StatsReporter
	cachedReporter      CachedStatsReporter
	baseReporter        BaseStatsReporter
	defaultBuckets      Buckets
	sanitizer           Sanitizer
	expirePeriodSeconds int64

	registry    *scopeRegistry
	status      scopeStatus
	createdUnix int64
	expired     uint32 // indicates whether current scope is expired

	cm sync.RWMutex
	gm sync.RWMutex
	tm sync.RWMutex
	hm sync.RWMutex

	// The metric objects have the names themselves, so could save mem
	// by having slices of counters per scope, but at cost of iterating
	// all metrics of same type per scope
	counters   map[string][]*counter
	gauges     map[string][]*gauge
	timers     map[string][]*timer
	histograms map[string][]*histogram
}

type scopeStatus struct {
	sync.RWMutex
	closed bool
	quit   chan struct{}
}

type scopeRegistry struct {
	sync.RWMutex
	subscopes           map[string]*scope
	expirePeriodSeconds int64
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
	SanitizeOptions *SanitizeOptions
	ExpiryPeriod    time.Duration
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
	sanitizer := NewNoOpSanitizer()
	if o := opts.SanitizeOptions; o != nil {
		sanitizer = NewSanitizer(*o)
	}

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

	if opts.ExpiryPeriod < time.Second {
		// ExpiryPeriod cannot be less than a second since we
		// perform expiry calcuation on time.Unix()
		opts.ExpiryPeriod = defaultExpiry
	}

	s := &scope{
		separator:      sanitizer.Name(opts.Separator),
		prefix:         sanitizer.Name(opts.Prefix),
		reporter:       opts.Reporter,
		cachedReporter: opts.CachedReporter,
		baseReporter:   baseReporter,
		defaultBuckets: opts.DefaultBuckets,
		sanitizer:      sanitizer,
		createdUnix:    globalNow().Unix(),

		registry: &scopeRegistry{
			subscopes:           make(map[string]*scope),
			expirePeriodSeconds: int64(opts.ExpiryPeriod.Seconds()),
		},
		status: scopeStatus{
			closed: false,
			quit:   make(chan struct{}, 1),
		},

		counters:   make(map[string][]*counter),
		gauges:     make(map[string][]*gauge),
		timers:     make(map[string][]*timer),
		histograms: make(map[string][]*histogram),
	}

	// NB(r): Take a copy of the tags on creation
	// so that it cannot be modified after set.
	s.tags = s.copyAndSanitizeMap(opts.Tags)

	// Register the root scope
	s.registry.subscopes[scopeRegistryKey(s.prefix, s.tags)] = s

	if interval > 0 {
		go s.reportLoop(interval)
	}

	return s
}

// report dumps all aggregated stats into the reporter. Should be called automatically by the root scope periodically.
func (s *scope) report(r StatsReporter) bool {
	tracker := newScopeExpiryTracker()

	s.cm.RLock()
	for name, counters := range s.counters {
		for _, counter := range counters {
			if counter.report(s.fullyQualifiedName(name), s.tags, r) {
				tracker.expiredCounters[counter] = struct{}{}
			}
		}
	}

	tracker.numCounters = len(s.counters)
	s.cm.RUnlock()

	s.gm.RLock()
	for name, gauges := range s.gauges {
		for _, gauge := range gauges {
			if gauge.report(s.fullyQualifiedName(name), s.tags, r) {
				tracker.expiredGauges[gauge] = struct{}{}
			}
		}
	}

	tracker.numGauges = len(s.gauges)
	s.gm.RUnlock()

	// We do nothing for timers here because timers report directly to ths StatsReporter without buffering.
	// If the scope is expired, new timers will be added to the fresh scope and refs to existing timers
	// will continue to emit them directly to the reporters.

	s.hm.RLock()
	for name, histograms := range s.histograms {
		for _, histogram := range histograms {
			if histogram.report(s.fullyQualifiedName(name), s.tags, r) {
				tracker.expiredHistograms[histogram] = struct{}{}
			}
		}
	}

	tracker.numHistograms = len(s.histograms)
	s.hm.RUnlock()

	s.clearExpiredMetrics(tracker)
	if tracker.numMetrics() == 0 && globalNow().Unix() > s.createdUnix+s.getExpirySeconds() {
		// All metrics for the scope have expired and it's been longer than the
		// expiry period.
		return true
	}

	r.Flush()
	return false
}

func (s *scope) cachedReport(c CachedStatsReporter) bool {
	tracker := newScopeExpiryTracker()

	s.cm.RLock()
	for _, counters := range s.counters {
		for _, counter := range counters {
			if counter.cachedReport() {
				tracker.expiredCounters[counter] = struct{}{}
			}
		}
	}

	tracker.numCounters = len(s.counters)
	s.cm.RUnlock()

	s.gm.RLock()
	for _, gauges := range s.gauges {
		for _, gauge := range gauges {
			if gauge.cachedReport() {
				tracker.expiredGauges[gauge] = struct{}{}
			}
		}
	}

	tracker.numGauges = len(s.gauges)
	s.gm.RUnlock()

	// We do nothing for timers here because timers report directly to ths StatsReporter without buffering.
	// If the scope is expired, new timers will be added to the fresh scope and refs to existing timers
	// will continue to emit them directly to the reporters.

	s.hm.RLock()
	for _, histograms := range s.histograms {
		for _, histogram := range histograms {
			if histogram.cachedReport() {
				tracker.expiredHistograms[histogram] = struct{}{}
			}
		}
	}

	tracker.numHistograms = len(s.histograms)
	s.hm.RUnlock()

	s.clearExpiredMetrics(tracker)
	if tracker.numMetrics() == 0 && globalNow().Unix() > s.createdUnix+s.getExpirySeconds() {
		// All metrics for the scope have expired and it's been longer than the
		// expiry period.
		return true
	}

	c.Flush()
	return false
}

func (s *scope) clearExpiredMetrics(tracker *scopeExpiryTracker) {
	var i int

	if len(tracker.expiredCounters) > 0 {
		s.cm.Lock()
		for name, counters := range s.counters {
			for _, counter := range counters {
				if _, exists := tracker.expiredCounters[counter]; !exists ||
					(exists && !counter.expired()) {
					counters[i] = counter
					i++
				}
			}

			if i == 0 {
				delete(s.counters, name)
			} else {
				s.counters[name] = counters[:i]
			}

			i = 0
		}

		tracker.numCounters = len(s.counters)
		s.cm.Unlock()
	}

	if len(tracker.expiredGauges) > 0 {
		s.gm.Lock()
		for name, gauges := range s.gauges {
			for _, gauge := range gauges {
				if _, exists := tracker.expiredGauges[gauge]; !exists ||
					(exists && !gauge.expired()) {
					gauges[i] = gauge
					i++
				}
			}

			if i == 0 {
				delete(s.gauges, name)
			} else {
				s.gauges[name] = gauges[:i]
			}

			i = 0
		}

		tracker.numGauges = len(s.gauges)
		s.gm.Unlock()
	}

	if len(tracker.expiredHistograms) > 0 {
		s.hm.Lock()
		for name, histograms := range s.histograms {
			for _, histogram := range histograms {
				if _, exists := tracker.expiredHistograms[histogram]; !exists ||
					(exists && !histogram.expired()) {
					histograms[i] = histogram
					i++
				}
			}

			if i == 0 {
				delete(s.histograms, name)
			} else {
				s.histograms[name] = histograms[:i]
			}

			i = 0
		}

		tracker.numHistograms = len(s.histograms)
		s.hm.Unlock()
	}
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

	s.reportRegistryWithLock()

	s.status.RUnlock()
}

// reports current registry with scope status lock held
func (s *scope) reportRegistryWithLock() {
	scopesToRemove := map[string]struct{}{}
	s.registry.RLock()
	if s.reporter != nil {
		for name, ss := range s.registry.subscopes {
			if ss.report(s.reporter) {
				atomic.StoreUint32(&ss.expired, 1)
				scopesToRemove[name] = struct{}{}
			}
		}
	} else if s.cachedReporter != nil {
		for name, ss := range s.registry.subscopes {
			if ss.cachedReport(s.cachedReporter) {
				atomic.StoreUint32(&ss.expired, 1)
				scopesToRemove[name] = struct{}{}
			}
		}
	}
	s.registry.RUnlock()

	if len(scopesToRemove) > 0 {
		s.registry.Lock()
		for name, ss := range s.registry.subscopes {
			if _, exists := scopesToRemove[name]; exists && ss.hasExpired() {
				// Confirm that no new metrics have been added in the meantime
				mets := 0
				ss.cm.RLock()
				mets += len(ss.counters)
				ss.cm.RUnlock()

				ss.gm.RLock()
				mets += len(ss.gauges)
				ss.gm.RUnlock()

				ss.hm.RLock()
				mets += len(ss.histograms)
				ss.hm.RUnlock()

				if mets == 0 {
					delete(s.registry.subscopes, name)
				}
			}
		}
		s.registry.Unlock()
	}
}

// getExpirySeconds gets the expiry period of metrics for this scope
func (s *scope) getExpirySeconds() int64 {
	return s.registry.expirePeriodSeconds
}

// hasExpired indicates whether the scope is expired
func (s *scope) hasExpired() bool {
	return atomic.LoadUint32(&s.expired) == 1
}

// getCurrentScope gets the current active scope which could
// be this scope or another if another was created after this
// one expired
func (s *scope) getCurrentScope() *scope {
	if s.hasExpired() {
		// scope has expired. Go grab the current one from the registry
		// TODO(martinm) - cache the registryKey in scope if expired occurs often
		registryName := scopeRegistryKey(s.prefix, s.tags)
		s.registry.RLock()
		curScope, exists := s.registry.subscopes[registryName]
		s.registry.RUnlock()

		if exists {
			// A new scope exists, return that instead of the current one
			return curScope
		}

		// No new scope has been created since this one expired, re-insert into the registry
		s.registry.Lock()
		curScope, exists = s.registry.subscopes[registryName]
		if exists {
			s.registry.Unlock()
			return curScope
		}

		s.registry.subscopes[registryName] = s
		atomic.StoreUint32(&s.expired, 0)
		s.createdUnix = globalNow().Unix()
		s.registry.Unlock()
	}

	return s
}

func (s *scope) Counter(name string) Counter {
	name = s.sanitizer.Name(name)
	curScope := s.getCurrentScope()
	curScope.cm.RLock()
	counters, ok := curScope.counters[name]
	curScope.cm.RUnlock()
	if !ok || len(counters) == 0 {
		curScope.cm.Lock()
		counters, ok = curScope.counters[name]
		if !ok || len(counters) == 0 {
			var cachedCounter CachedCount
			if curScope.cachedReporter != nil {
				cachedCounter = curScope.cachedReporter.AllocateCounter(
					curScope.fullyQualifiedName(name), curScope.tags,
				)
			}
			counters = []*counter{newCounter(cachedCounter, name, curScope)}
			curScope.counters[name] = counters
		}
		curScope.cm.Unlock()

		// Protect against race where scope was expired since beginning
		// of this call
		if curScope.hasExpired() {
			return s.Counter(name)
		}
	}

	return counters[0]
}

func (s *scope) Gauge(name string) Gauge {
	name = s.sanitizer.Name(name)
	curScope := s.getCurrentScope()

	curScope.gm.RLock()
	gauges, ok := curScope.gauges[name]
	curScope.gm.RUnlock()
	if !ok || len(gauges) == 0 {
		curScope.gm.Lock()
		gauges, ok = curScope.gauges[name]
		if !ok || len(gauges) == 0 {
			var cachedGauge CachedGauge
			if curScope.cachedReporter != nil {
				cachedGauge = curScope.cachedReporter.AllocateGauge(
					curScope.fullyQualifiedName(name), curScope.tags,
				)
			}

			gauges = []*gauge{newGauge(cachedGauge, name, curScope)}
			curScope.gauges[name] = gauges
		}
		s.gm.Unlock()

		// Protect against race where scope was expired since beginning
		// of this call
		if curScope.hasExpired() {
			return s.Gauge(name)
		}
	}

	return gauges[0]
}

func (s *scope) Timer(name string) Timer {
	name = s.sanitizer.Name(name)
	curScope := s.getCurrentScope()

	curScope.tm.RLock()
	timers, ok := curScope.timers[name]
	curScope.tm.RUnlock()
	if !ok || len(timers) == 0 {
		curScope.tm.Lock()
		timers, ok = curScope.timers[name]
		if !ok || len(timers) == 0 {
			var cachedTimer CachedTimer
			if curScope.cachedReporter != nil {
				cachedTimer = curScope.cachedReporter.AllocateTimer(
					curScope.fullyQualifiedName(name), curScope.tags,
				)
			}

			timers = []*timer{newTimer(
				curScope.fullyQualifiedName(name), curScope.tags, curScope.reporter, cachedTimer,
			)}
			curScope.timers[name] = timers
		}
		curScope.tm.Unlock()

		// Protect against race where scope was expired since beginning
		// of this call
		if curScope.hasExpired() {
			return s.Timer(name)
		}
	}

	return timers[0]
}

func (s *scope) Histogram(name string, b Buckets) Histogram {
	name = s.sanitizer.Name(name)
	curScope := s.getCurrentScope()

	if b == nil {
		b = curScope.defaultBuckets
	}

	curScope.hm.RLock()
	histograms, ok := curScope.histograms[name]
	curScope.hm.RUnlock()
	if !ok || len(histograms) == 0 {
		curScope.hm.Lock()
		histograms, ok = curScope.histograms[name]
		if !ok || len(histograms) == 0 {
			var cachedHistogram CachedHistogram
			if curScope.cachedReporter != nil {
				cachedHistogram = curScope.cachedReporter.AllocateHistogram(
					curScope.fullyQualifiedName(name), curScope.tags, b,
				)
			}

			histograms = []*histogram{newHistogram(curScope.tags, curScope.reporter,
				b, cachedHistogram, curScope, name,
			)}
			curScope.histograms[name] = histograms
		}

		curScope.hm.Unlock()

		// Protect against race where scope was expired since beginning
		// of this call
		if curScope.hasExpired() {
			return s.Histogram(name, b)
		}
	}

	return histograms[0]
}

func (s *scope) Tagged(tags map[string]string) Scope {
	tags = s.copyAndSanitizeMap(tags)
	return s.subscope(s.prefix, tags)
}

func (s *scope) SubScope(prefix string) Scope {
	prefix = s.sanitizer.Name(prefix)
	return s.subscope(s.fullyQualifiedName(prefix), nil)
}

func (s *scope) subscope(prefix string, immutableTags map[string]string) Scope {
	immutableTags = mergeRightTags(s.tags, immutableTags)
	key := scopeRegistryKey(prefix, immutableTags)

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
		// NB(prateek): don't need to copy the tags here,
		// we assume the map provided is immutable.
		tags:           immutableTags,
		reporter:       s.reporter,
		cachedReporter: s.cachedReporter,
		baseReporter:   s.baseReporter,
		defaultBuckets: s.defaultBuckets,
		sanitizer:      s.sanitizer,
		registry:       s.registry,
		createdUnix:    globalNow().Unix(),

		counters:   make(map[string][]*counter),
		gauges:     make(map[string][]*gauge),
		timers:     make(map[string][]*timer),
		histograms: make(map[string][]*histogram),
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
	curScope := s.getCurrentScope()
	curScope.registry.RLock()
	for _, ss := range curScope.registry.subscopes {
		// NB(r): tags are immutable, no lock required to read.
		tags := make(map[string]string, len(curScope.tags))
		for k, v := range ss.tags {
			tags[k] = v
		}

		ss.cm.RLock()
		for key, counters := range ss.counters {
			if len(counters) > 0 {
				name := ss.fullyQualifiedName(key)
				id := KeyForPrefixedStringMap(name, tags)
				var val int64
				for _, counter := range counters {
					// If there are multiple counters created due to expiry,
					// add the values together
					val += counter.snapshot()
				}

				snap.counters[id] = &counterSnapshot{
					name:  name,
					tags:  tags,
					value: val,
				}
			}
		}
		ss.cm.RUnlock()

		ss.gm.RLock()
		for key, gauges := range ss.gauges {
			if len(gauges) > 0 {
				name := ss.fullyQualifiedName(key)
				id := KeyForPrefixedStringMap(name, tags)
				// If there are multiple gauges created due to expiry,
				// pick the value of the first gauge
				snap.gauges[id] = &gaugeSnapshot{
					name:  name,
					tags:  tags,
					value: gauges[0].snapshot(),
				}
			}
		}
		ss.gm.RUnlock()

		ss.tm.RLock()
		for key, timers := range ss.timers {
			if len(timers) > 0 {
				name := ss.fullyQualifiedName(key)
				id := KeyForPrefixedStringMap(name, tags)
				values := timers[0].snapshot()
				for i := 1; i < len(timers); i++ {
					values = append(values, timers[i].snapshot()...)
				}

				snap.timers[id] = &timerSnapshot{
					name:   name,
					tags:   tags,
					values: values,
				}
			}
		}
		ss.tm.RUnlock()

		ss.hm.RLock()
		for key, histograms := range ss.histograms {
			if len(histograms) > 0 {
				name := ss.fullyQualifiedName(key)
				id := KeyForPrefixedStringMap(name, tags)
				values := histograms[0].snapshotValues()
				durations := histograms[0].snapshotDurations()

				// If there are multiple histograms created due to expiry,
				// combine the values
				for i := 1; i < len(histograms); i++ {
					for k, v := range histograms[i].snapshotValues() {
						if _, exists := values[k]; !exists {
							values[k] = 0
						}

						values[k] = values[k] + v
					}

					for k, v := range histograms[i].snapshotDurations() {
						if _, exists := durations[k]; !exists {
							durations[k] = 0
						}

						durations[k] = durations[k] + v
					}
				}

				snap.histograms[id] = &histogramSnapshot{
					name:      name,
					tags:      tags,
					values:    values,
					durations: durations,
				}
			}

		}
		ss.hm.RUnlock()
	}
	s.registry.RUnlock()

	return snap
}

func (s *scope) Close() error {
	s.status.Lock()

	// don't wait to close more than once (panic on double close of
	// s.status.quit)
	if s.status.closed {
		s.status.Unlock()
		return nil
	}

	s.status.closed = true
	close(s.status.quit)
	s.reportRegistryWithLock()

	s.status.Unlock()

	if closer, ok := s.baseReporter.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// NB(prateek): We assume concatenation of sanitized inputs is
// sanitized. If that stops being true, then we need to sanitize the
// output of this function.
func (s *scope) fullyQualifiedName(name string) string {
	if len(s.prefix) == 0 {
		return name
	}
	// NB: we don't need to sanitize the output of this function as we
	// sanitize all the the inputs (prefix, separator, name); and the
	// output we're creating is a concatenation of the sanitized inputs.
	// If we change the concatenation to involve other inputs or characters,
	// we'll need to sanitize them too.
	return fmt.Sprintf("%s%s%s", s.prefix, s.separator, name)
}

func (s *scope) copyAndSanitizeMap(tags map[string]string) map[string]string {
	result := make(map[string]string, len(tags))
	for k, v := range tags {
		k = s.sanitizer.Key(k)
		v = s.sanitizer.Value(v)
		result[k] = v
	}
	return result
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

	// Histograms returns a snapshot of histogram samples since last report execution
	Histograms() map[string]HistogramSnapshot
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

// HistogramSnapshot is a snapshot of a histogram
type HistogramSnapshot interface {
	// Name returns the name
	Name() string

	// Tags returns the tags
	Tags() map[string]string

	// Values returns the sample values by upper bound for a valueHistogram
	Values() map[float64]int64

	// Durations returns the sample values by upper bound for a durationHistogram
	Durations() map[time.Duration]int64
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

type snapshot struct {
	counters   map[string]CounterSnapshot
	gauges     map[string]GaugeSnapshot
	timers     map[string]TimerSnapshot
	histograms map[string]HistogramSnapshot
}

func newSnapshot() *snapshot {
	return &snapshot{
		counters:   make(map[string]CounterSnapshot),
		gauges:     make(map[string]GaugeSnapshot),
		timers:     make(map[string]TimerSnapshot),
		histograms: make(map[string]HistogramSnapshot),
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

func (s *snapshot) Histograms() map[string]HistogramSnapshot {
	return s.histograms
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

type histogramSnapshot struct {
	name      string
	tags      map[string]string
	values    map[float64]int64
	durations map[time.Duration]int64
}

func (s *histogramSnapshot) Name() string {
	return s.name
}

func (s *histogramSnapshot) Tags() map[string]string {
	return s.tags
}

func (s *histogramSnapshot) Values() map[float64]int64 {
	return s.values
}

func (s *histogramSnapshot) Durations() map[time.Duration]int64 {
	return s.durations
}

type scopeExpiryTracker struct {
	expiredCounters   map[*counter]struct{}
	expiredGauges     map[*gauge]struct{}
	expiredHistograms map[*histogram]struct{}

	numCounters   int
	numGauges     int
	numHistograms int
}

func newScopeExpiryTracker() *scopeExpiryTracker {
	return &scopeExpiryTracker{
		expiredCounters:   map[*counter]struct{}{},
		expiredGauges:     map[*gauge]struct{}{},
		expiredHistograms: map[*histogram]struct{}{},
	}
}

func (s *scopeExpiryTracker) numMetrics() int {
	return s.numCounters + s.numGauges + s.numHistograms
}
