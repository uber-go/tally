// Copyright (c) 2024 Uber Technologies, Inc.
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
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const (
	_defaultInitialSliceSize  = 16
	_defaultReportingInterval = 2 * time.Second
)

var (
	// NoopScope is a scope that does nothing
	NoopScope, _ = NewRootScope(ScopeOptions{Reporter: NullStatsReporter}, 0)
	// DefaultSeparator is the default separator used to join nested scopes
	DefaultSeparator = "."

	globalNow = time.Now

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
	separator      string
	prefix         string
	tags           map[string]string
	reporter       StatsReporter
	cachedReporter CachedStatsReporter
	baseReporter   BaseStatsReporter
	defaultBuckets Buckets
	sanitizer      Sanitizer

	registry *scopeRegistry

	// Metric maps use sync.Map for lock-free reads of existing metrics.
	// This provides a significant performance improvement for the common case
	// where metrics are created once and read many times.
	// The sync.Map implementation allows concurrent reads without locking,
	// and uses atomic operations instead of mutex acquisition.
	counters   sync.Map // was map[string]*counter
	gauges     sync.Map // was map[string]*gauge
	histograms sync.Map // was map[string]*histogram
	timers     sync.Map // was map[string]*timer

	// Slices for reporting - need their own mutexes for appends
	countersSlice    []*counter
	countersSliceMux sync.Mutex

	gaugesSlice    []*gauge
	gaugesSliceMux sync.Mutex

	histogramsSlice    []*histogram
	histogramsSliceMux sync.Mutex
	clearMux           sync.Mutex // Protects clearMetrics operations

	// timersSlice is deliberately skipped. No dedicated mux for it for now.

	bucketCache      *bucketCache
	closed           int32 // Used with atomic operations
	done             chan struct{}
	wg               sync.WaitGroup
	root             bool
	testScope        bool
	noCacheSubscopes bool
	ephemeralKey     string // Key used for pool looks of ephemeral scopes
	hasReportLoop    bool   // Tracks whether this scope started a reportLoop goroutine

	// Activity tracking for eviction
	lastActivity int64 // Unix timestamp in seconds of last metric activity
}

// ScopeOptions is a set of options to construct a scope.
type ScopeOptions struct {
	Tags                   map[string]string
	Prefix                 string
	Reporter               StatsReporter
	CachedReporter         CachedStatsReporter
	Separator              string
	DefaultBuckets         Buckets
	SanitizeOptions        *SanitizeOptions
	OmitCardinalityMetrics bool
	CardinalityMetricsTags map[string]string
	NoCacheSubscopes       bool // When true, subscopes won't be cached in the registry

	// Eviction policy configuration
	EnableSubscopeEviction     bool          // When true, subscopes can be evicted from the registry
	MaxSubscopeInactivity      time.Duration // Maximum time a subscope can be inactive before eviction (0 means no time-based eviction)
	MaxSubscopesBeforeEviction int           // Maximum number of subscopes before starting eviction (0 means no limit)

	// Pooling configuration
	EnableScopePooling bool // When true, scopes will be recycled via an object pool

	testScope          bool
	registryShardCount uint
}

// NewRootScope creates a new root Scope with a set of options and
// a reporting interval.
// Must provide either a StatsReporter or a CachedStatsReporter.
func NewRootScope(opts ScopeOptions, interval time.Duration) (Scope, io.Closer) {
	s := newRootScope(opts, interval)
	return s, s
}

// NewRootScopeWithDefaultInterval invokes NewRootScope with the default
// reporting interval of 2s.
func NewRootScopeWithDefaultInterval(opts ScopeOptions) (Scope, io.Closer) {
	return NewRootScope(opts, _defaultReportingInterval)
}

// NewTestScope creates a new Scope without a stats reporter with the
// given prefix and adds the ability to take snapshots of metrics emitted
// to it.
func NewTestScope(
	prefix string,
	tags map[string]string,
) TestScope {
	return newRootScope(ScopeOptions{
		Prefix:    prefix,
		Tags:      tags,
		testScope: true,
	}, 0)
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

	s := &scope{
		baseReporter:   baseReporter,
		bucketCache:    newBucketCache(),
		cachedReporter: opts.CachedReporter,
		// counters, gauges, histograms, timers are sync.Map and are zero-value ready.
		// The explicit make(map[string]...) calls are removed for these.
		countersSlice: *getCounterSlice(_defaultInitialSliceSize),
		// countersSliceMux is zero-value ready.
		defaultBuckets: opts.DefaultBuckets,
		done:           make(chan struct{}),
		gaugesSlice:    *getGaugeSlice(_defaultInitialSliceSize),
		// gaugesSliceMux is zero-value ready.
		histogramsSlice: *getHistogramSlice(_defaultInitialSliceSize),
		// histogramsSliceMux is zero-value ready.
		prefix:           sanitizer.Name(opts.Prefix),
		reporter:         opts.Reporter,
		sanitizer:        sanitizer,
		separator:        sanitizer.Name(opts.Separator),
		root:             true,
		testScope:        opts.testScope,
		noCacheSubscopes: opts.NoCacheSubscopes,
	}

	// Initialize lastActivity with current time
	atomic.StoreInt64(&s.lastActivity, time.Now().Unix())

	// NB(r): Take a copy of the tags on creation
	// so that it cannot be modified after set.
	s.tags = s.copyAndSanitizeMap(opts.Tags)

	// Register the root scope
	s.registry = newScopeRegistryWithEvictionOptions(
		s,
		opts.registryShardCount,
		opts.OmitCardinalityMetrics,
		opts.CardinalityMetricsTags,
		opts.EnableSubscopeEviction,
		opts.MaxSubscopeInactivity,
		opts.MaxSubscopesBeforeEviction,
		opts.EnableScopePooling,
	)

	if s.root && !s.testScope && interval > 0 {
		s.hasReportLoop = true
		s.wg.Add(1)
		go s.reportLoop(interval)
	}

	return s
}

// report dumps all aggregated stats into the reporter. Should be called automatically by the root scope periodically.
func (s *scope) report(r StatsReporter) {
	s.counters.Range(func(key, value interface{}) bool {
		c := value.(*counter)
		c.report(s.fullyQualifiedName(key.(string)), s.tags, r)
		return true
	})

	s.gauges.Range(func(key, value interface{}) bool {
		g := value.(*gauge)
		g.report(s.fullyQualifiedName(key.(string)), s.tags, r)
		return true
	})

	// We do nothing for timers here because timers report directly to the StatsReporter without buffering

	s.histograms.Range(func(key, value interface{}) bool {
		h := value.(*histogram)
		h.report(s.fullyQualifiedName(key.(string)), s.tags, r)
		return true
	})
}

func (s *scope) cachedReport() {
	s.counters.Range(func(key, value interface{}) bool {
		c := value.(*counter)
		c.cachedReport()
		return true
	})

	s.gauges.Range(func(key, value interface{}) bool {
		g := value.(*gauge)
		g.cachedReport()
		return true
	})

	// We do nothing for timers here because timers report directly to the StatsReporter without buffering

	s.histograms.Range(func(key, value interface{}) bool {
		h := value.(*histogram)
		h.cachedReport()
		return true
	})
}

// reportLoop is used by the root scope for periodic reporting
func (s *scope) reportLoop(interval time.Duration) {
	defer s.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.reportLoopRun()
		case <-s.done:
			return
		}
	}
}

func (s *scope) reportLoopRun() {
	if atomic.LoadInt32(&s.closed) != 0 {
		return
	}

	s.reportRegistry()
}

func (s *scope) reportRegistry() {
	if s.reporter != nil {
		s.registry.Report(s.reporter)
		s.reporter.Flush()
	} else if s.cachedReporter != nil {
		s.registry.CachedReport()
		s.cachedReporter.Flush()
	}
}

func (s *scope) Counter(name string) Counter {
	s.trackActivity()
	name = s.sanitizer.Name(name)

	// Try to load existing counter first
	if value, ok := s.counters.Load(name); ok {
		return value.(*counter)
	}

	// Create a new counter
	var cachedCounter CachedCount
	if s.cachedReporter != nil {
		cachedCounter = s.cachedReporter.AllocateCounter(
			s.fullyQualifiedName(name),
			s.tags,
		)
	}

	c := newCounter(cachedCounter)

	// Attempt to store it, or get the existing one if another goroutine created it first
	actual, loaded := s.counters.LoadOrStore(name, c)
	if !loaded {
		// We were the first to create this counter, add it to the slice
		s.countersSliceMux.Lock()
		s.countersSlice = append(s.countersSlice, c)
		s.countersSliceMux.Unlock()

		// Atomically increment the counter cardinality for the registry.
		if s.registry != nil {
			s.registry.numCounters.Inc()
		}

		return c
	}

	// Another goroutine beat us to it, use their counter
	return actual.(*counter)
}

func (s *scope) counter(sanitizedName string) (Counter, bool) {
	value, ok := s.counters.Load(sanitizedName)
	if !ok {
		return nil, false
	}
	return value.(*counter), true
}

func (s *scope) Gauge(name string) Gauge {
	s.trackActivity()
	name = s.sanitizer.Name(name)

	// Try to load existing gauge first
	if value, ok := s.gauges.Load(name); ok {
		return value.(*gauge)
	}

	// Create a new gauge
	var cachedGauge CachedGauge
	if s.cachedReporter != nil {
		cachedGauge = s.cachedReporter.AllocateGauge(
			s.fullyQualifiedName(name), s.tags,
		)
	}

	g := newGauge(cachedGauge)

	// Attempt to store it, or get the existing one if another goroutine created it first
	actual, loaded := s.gauges.LoadOrStore(name, g)
	if !loaded {
		// We were the first to create this gauge, add it to the slice
		s.gaugesSliceMux.Lock()
		s.gaugesSlice = append(s.gaugesSlice, g)
		s.gaugesSliceMux.Unlock()

		// Atomically increment the gauge cardinality for the registry.
		if s.registry != nil {
			s.registry.numGauges.Inc()
		}

		return g
	}

	// Another goroutine beat us to it, use their gauge
	return actual.(*gauge)
}

func (s *scope) gauge(name string) (Gauge, bool) {
	value, ok := s.gauges.Load(name)
	if !ok {
		return nil, false
	}
	return value.(*gauge), true
}

func (s *scope) Timer(name string) Timer {
	s.trackActivity()
	name = s.sanitizer.Name(name)

	// Try to load existing timer first
	if value, ok := s.timers.Load(name); ok {
		return value.(*timer)
	}

	// Create a new timer
	var cachedTimer CachedTimer
	if s.cachedReporter != nil {
		cachedTimer = s.cachedReporter.AllocateTimer(
			s.fullyQualifiedName(name), s.tags,
		)
	}

	t := newTimer(
		s.fullyQualifiedName(name), s.tags, s.reporter, cachedTimer,
	)

	// Attempt to store it, or get the existing one if another goroutine created it first
	actual, loaded := s.timers.LoadOrStore(name, t)
	if !loaded {
		// Timer doesn't use a slice, so no need to update slice
		return t
	}

	// Another goroutine beat us to it, use their timer
	return actual.(*timer)
}

func (s *scope) timer(sanitizedName string) (Timer, bool) {
	value, ok := s.timers.Load(sanitizedName)
	if !ok {
		return nil, false
	}
	return value.(*timer), true
}

func (s *scope) Histogram(name string, b Buckets) Histogram {
	s.trackActivity()
	name = s.sanitizer.Name(name)

	// Try to load existing histogram first
	if value, ok := s.histograms.Load(name); ok {
		return value.(*histogram)
	}

	// Create a new histogram
	if b == nil {
		b = s.defaultBuckets
	}

	htype := valueHistogramType
	if _, ok := b.(DurationBuckets); ok {
		htype = durationHistogramType
	}

	var cachedHistogram CachedHistogram
	if s.cachedReporter != nil {
		cachedHistogram = s.cachedReporter.AllocateHistogram(
			s.fullyQualifiedName(name), s.tags, b,
		)
	}

	h := newHistogram(
		htype,
		s.fullyQualifiedName(name),
		s.tags,
		s.reporter,
		s.bucketCache.Get(htype, b),
		cachedHistogram,
	)

	// Attempt to store it, or get the existing one if another goroutine created it first
	actual, loaded := s.histograms.LoadOrStore(name, h)
	if !loaded {
		// We were the first to create this histogram, add it to the slice
		s.histogramsSliceMux.Lock()
		s.histogramsSlice = append(s.histogramsSlice, h)
		s.histogramsSliceMux.Unlock()

		// Atomically increment the histogram cardinality for the registry.
		if s.registry != nil {
			s.registry.numHistograms.Inc()
		}

		return h
	}

	// Another goroutine beat us to it, use their histogram
	return actual.(*histogram)
}

func (s *scope) histogram(sanitizedName string) (Histogram, bool) {
	value, ok := s.histograms.Load(sanitizedName)
	if !ok {
		return nil, false
	}
	return value.(*histogram), true
}

func (s *scope) Tagged(tags map[string]string) Scope {
	return s.subscope(s.prefix, tags)
}

func (s *scope) SubScope(prefix string) Scope {
	prefix = s.sanitizer.Name(prefix)
	return s.subscope(s.fullyQualifiedName(prefix), nil)
}

func (s *scope) subscope(prefix string, tags map[string]string) Scope {
	// If NoCacheSubscopes is enabled, create an ephemeral scope that isn't stored in the registry
	if s.registry != nil && s.registry.root != nil && s.registry.root.baseReporter != nil {
		// Check if NoCacheSubscopes option is enabled on the root scope
		if s.noCacheSubscopes {
			allTags := mergeRightTagsPooled(s.tags, s.copyAndSanitizeMap(tags))

			// Create ephemeral scope with sync.Map instead of map[string]*timer
			ephemeralScope := &scope{
				separator:      s.separator,
				prefix:         prefix,
				tags:           allTags,
				reporter:       s.reporter,
				cachedReporter: s.cachedReporter,
				baseReporter:   s.baseReporter,
				defaultBuckets: s.defaultBuckets,
				sanitizer:      s.sanitizer,
				registry:       s.registry,
				bucketCache:    s.bucketCache,

				countersSlice:    make([]*counter, 0, _defaultInitialSliceSize),
				gaugesSlice:      make([]*gauge, 0, _defaultInitialSliceSize),
				histogramsSlice:  make([]*histogram, 0, _defaultInitialSliceSize),
				done:             make(chan struct{}),
				testScope:        s.testScope,
				noCacheSubscopes: s.noCacheSubscopes,
			}

			// Initialize lastActivity timestamp
			atomic.StoreInt64(&ephemeralScope.lastActivity, time.Now().Unix())

			return ephemeralScope
		}
	}

	return s.registry.Subscope(s, prefix, s.copyAndSanitizeMap(tags))
}

func (s *scope) Capabilities() Capabilities {
	if s.baseReporter == nil {
		return capabilitiesNone
	}
	return s.baseReporter.Capabilities()
}

func (s *scope) Snapshot() Snapshot {
	snap := newSnapshot()

	s.registry.ForEachScope(func(ss *scope) {
		// NB(r): tags are immutable, no lock required to read.
		tags := make(map[string]string, len(s.tags))
		for k, v := range ss.tags {
			tags[k] = v
		}

		ss.counters.Range(func(key, value interface{}) bool {
			c := value.(*counter)
			name := ss.fullyQualifiedName(key.(string))
			id := KeyForPrefixedStringMap(name, tags)
			snap.counters[id] = &counterSnapshot{
				name:  name,
				tags:  tags,
				value: c.snapshot(),
			}
			return true
		})
		ss.gauges.Range(func(key, value interface{}) bool {
			g := value.(*gauge)
			name := ss.fullyQualifiedName(key.(string))
			id := KeyForPrefixedStringMap(name, tags)
			snap.gauges[id] = &gaugeSnapshot{
				name:  name,
				tags:  tags,
				value: g.snapshot(),
			}
			return true
		})
		ss.timers.Range(func(key, value interface{}) bool {
			t := value.(*timer)
			name := ss.fullyQualifiedName(key.(string))
			id := KeyForPrefixedStringMap(name, tags)
			snap.timers[id] = &timerSnapshot{
				name:   name,
				tags:   tags,
				values: t.snapshot(),
			}
			return true
		})
		ss.histograms.Range(func(key, value interface{}) bool {
			h := value.(*histogram)
			name := ss.fullyQualifiedName(key.(string))
			id := KeyForPrefixedStringMap(name, tags)
			snap.histograms[id] = &histogramSnapshot{
				name:      name,
				tags:      tags,
				values:    h.snapshotValues(),
				durations: h.snapshotDurations(),
			}
			return true
		})
	})

	return snap
}

// Close closes the scope
func (s *scope) Close() error {
	if atomic.SwapInt32(&s.closed, 1) != 0 {
		return nil
	}

	if s.root {
		s.reportRegistry()
	}

	close(s.done)

	// Wait for the reportLoop goroutine to finish, but only if we started one
	if s.hasReportLoop {
		s.wg.Wait()
	}

	if s.root {
		if closer, ok := s.baseReporter.(io.Closer); ok {
			return closer.Close()
		}
	}

	// If this is an ephemeral scope (no cache), return it to the pool
	if !s.root && (s.noCacheSubscopes || (s.registry != nil && atomic.LoadInt32(&s.registry.adaptiveMode) == 1)) {
		// Only return to pool if it exists and we have an ephemeral key
		if s.registry != nil && s.registry.scopePool.New != nil && s.ephemeralKey != "" {
			// Clear all metrics but keep internal structures
			s.resetForPool()

			// Fast path - check pool size first before locking
			poolSize := atomic.LoadInt64(&s.registry.poolSize)
			if poolSize < int64(s.registry.maxPoolSize) {
				// Store in the pool map by key
				s.registry.pooledScopesMtx.Lock()

				// Only add to pool if we have space and the key doesn't already exist
				if len(s.registry.pooledScopes) < s.registry.maxPoolSize &&
					s.registry.pooledScopes[s.ephemeralKey] == nil {
					// Add to pool
					s.registry.pooledScopes[s.ephemeralKey] = s

					// Add to LRU list
					s.registry.pooledScopesLRU = append(s.registry.pooledScopesLRU, s.ephemeralKey)

					// Update size counter
					atomic.AddInt64(&s.registry.poolSize, 1)
				} else {
					// Put it back in the generic pool if we can't add it to the keyed pool
					s.registry.scopePool.Put(s)
				}

				s.registry.pooledScopesMtx.Unlock()
			} else {
				// Pool full, use the generic pool
				s.registry.scopePool.Put(s)
			}
		}
	}

	return nil
}

func (s *scope) clearMetrics() {
	s.clearMux.Lock()
	defer s.clearMux.Unlock()

	var numCounters, numGauges, numHistograms int64

	s.counters.Range(func(key, value interface{}) bool {
		numCounters++
		s.counters.Delete(key)
		return true
	})
	s.countersSlice = nil

	s.gauges.Range(func(key, value interface{}) bool {
		numGauges++
		s.gauges.Delete(key)
		return true
	})
	s.gaugesSlice = nil

	s.timers.Range(func(key, value interface{}) bool {
		s.timers.Delete(key)
		return true
	})

	s.histograms.Range(func(key, value interface{}) bool {
		numHistograms++
		s.histograms.Delete(key)
		return true
	})
	s.histogramsSlice = nil

	// Atomically decrement the cardinality counters in the registry.
	if s.registry != nil {
		s.registry.numCounters.Sub(numCounters)
		s.registry.numGauges.Sub(numGauges)
		s.registry.numHistograms.Sub(numHistograms)
	}
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
	return s.prefix + s.separator + name
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

// mergeRightTagsPooled is an optimized version that uses pooled maps
// Phase 4: Eliminates allocations during tag merging for better performance
func mergeRightTagsPooled(tagsLeft, tagsRight map[string]string) map[string]string {
	if tagsLeft == nil && tagsRight == nil {
		return nil
	}
	if len(tagsRight) == 0 {
		return tagsLeft
	}
	if len(tagsLeft) == 0 {
		return tagsRight
	}

	// Create a regular map instead of using pooled maps to avoid race conditions
	// The pooled map approach was causing issues because the map was being
	// released incorrectly in some code paths
	result := make(map[string]string, len(tagsLeft)+len(tagsRight))

	// Copy values
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

// newSnapshot creates a snapshot using pooled maps for better performance
// Phase 4: Uses pooled snapshot maps to reduce allocations
func newSnapshot() *snapshot {
	return &snapshot{
		counters:   *getCounterSnapshotMap(),
		gauges:     *getGaugeSnapshotMap(),
		timers:     *getTimerSnapshotMap(),
		histograms: *getHistogramSnapshotMap(),
	}
}

// newSnapshotPooled creates a snapshot using pooled maps and provides cleanup function
// Phase 4: Returns cleanup function to return maps to pools when done
func newSnapshotPooled() (*snapshot, func()) {
	counterMapPtr := getCounterSnapshotMap()
	gaugeMapPtr := getGaugeSnapshotMap()
	timerMapPtr := getTimerSnapshotMap()
	histogramMapPtr := getHistogramSnapshotMap()

	s := &snapshot{
		counters:   *counterMapPtr,
		gauges:     *gaugeMapPtr,
		timers:     *timerMapPtr,
		histograms: *histogramMapPtr,
	}

	cleanup := func() {
		releaseCounterSnapshotMap(counterMapPtr)
		releaseGaugeSnapshotMap(gaugeMapPtr)
		releaseTimerSnapshotMap(timerMapPtr)
		releaseHistogramSnapshotMap(histogramMapPtr)
	}

	return s, cleanup
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

// Helper method to track activity
func (s *scope) trackActivity() {
	atomic.StoreInt64(&s.lastActivity, time.Now().Unix())
}

// resetForPool resets the scope for reuse from the object pool
// Phase 4: Now properly returns metric slices to pools for better memory efficiency
func (s *scope) resetForPool() {
	s.prefix = ""

	// Clear maps but maintain capacity
	s.counters.Range(func(key, value interface{}) bool {
		s.counters.Delete(key)
		return true
	})

	s.gauges.Range(func(key, value interface{}) bool {
		s.gauges.Delete(key)
		return true
	})

	s.histograms.Range(func(key, value interface{}) bool {
		s.histograms.Delete(key)
		return true
	})

	s.timers.Range(func(key, value interface{}) bool {
		s.timers.Delete(key)
		return true
	})

	// Return metric slices to pools instead of just clearing them
	if len(s.countersSlice) > 0 {
		counterSlicePtr := &s.countersSlice
		releaseCounterSlice(counterSlicePtr)
		s.countersSlice = *getCounterSlice(_defaultInitialSliceSize)
	}

	if len(s.gaugesSlice) > 0 {
		gaugeSlicePtr := &s.gaugesSlice
		releaseGaugeSlice(gaugeSlicePtr)
		s.gaugesSlice = *getGaugeSlice(_defaultInitialSliceSize)
	}

	if len(s.histogramsSlice) > 0 {
		histogramSlicePtr := &s.histogramsSlice
		releaseHistogramSlice(histogramSlicePtr)
		s.histogramsSlice = *getHistogramSlice(_defaultInitialSliceSize)
	}

	// Reset state flags
	atomic.StoreInt32(&s.closed, 0)
	s.hasReportLoop = false

	// Reset lastActivity timestamp
	atomic.StoreInt64(&s.lastActivity, time.Now().Unix())

	// Keep the done channel to avoid allocation
	// but make sure it's not closed
	select {
	case <-s.done:
		s.done = make(chan struct{})
	default:
		// Channel is fine
	}
}
