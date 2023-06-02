// Copyright (c) 2023 Uber Technologies, Inc.
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
	"hash/maphash"
	"runtime"
	"sync"
	"unsafe"

	"go.uber.org/atomic"
)

var (
	scopeRegistryKey = keyForPrefixedStringMaps

	// Metrics related.
	internalTags             = map[string]string{"version": Version}
	counterCardinalityName   = "tally_internal_counter_cardinality"
	gaugeCardinalityName     = "tally_internal_gauge_cardinality"
	histogramCardinalityName = "tally_internal_histogram_cardinality"
)

type scopeRegistry struct {
	seed maphash.Seed
	root *scope
	// We need a subscope per GOPROC so that we can take advantage of all the cpu available to the application.
	subscopes []*scopeBucket
	// Internal metrics related.
	internalMetricsOption             InternalMetricOption
	sanitizedCounterCardinalityName   string
	sanitizedGaugeCardinalityName     string
	sanitizedHistogramCardinalityName string
}

type scopeBucket struct {
	mu sync.RWMutex
	s  map[string]*scope
}

func newScopeRegistryWithShardCount(
	root *scope,
	shardCount uint,
	internalMetricsOption InternalMetricOption,
) *scopeRegistry {
	if shardCount == 0 {
		shardCount = uint(runtime.GOMAXPROCS(-1))
	}

	r := &scopeRegistry{
		root:                              root,
		subscopes:                         make([]*scopeBucket, shardCount),
		seed:                              maphash.MakeSeed(),
		internalMetricsOption:             internalMetricsOption,
		sanitizedCounterCardinalityName:   root.sanitizer.Name(counterCardinalityName),
		sanitizedGaugeCardinalityName:     root.sanitizer.Name(gaugeCardinalityName),
		sanitizedHistogramCardinalityName: root.sanitizer.Name(histogramCardinalityName),
	}
	for i := uint(0); i < shardCount; i++ {
		r.subscopes[i] = &scopeBucket{
			s: make(map[string]*scope),
		}
		r.subscopes[i].s[scopeRegistryKey(root.prefix, root.tags)] = root
	}
	return r
}

func (r *scopeRegistry) Report(reporter StatsReporter) {
	defer r.purgeIfRootClosed()
	r.reportInternalMetrics()

	for _, subscopeBucket := range r.subscopes {
		subscopeBucket.mu.RLock()

		for name, s := range subscopeBucket.s {
			s.report(reporter)

			if s.closed.Load() {
				r.removeWithRLock(subscopeBucket, name)
				s.clearMetrics()
			}
		}

		subscopeBucket.mu.RUnlock()
	}
}

func (r *scopeRegistry) CachedReport() {
	defer r.purgeIfRootClosed()
	r.reportInternalMetrics()

	for _, subscopeBucket := range r.subscopes {
		subscopeBucket.mu.RLock()

		for name, s := range subscopeBucket.s {
			s.cachedReport()

			if s.closed.Load() {
				r.removeWithRLock(subscopeBucket, name)
				s.clearMetrics()
			}
		}

		subscopeBucket.mu.RUnlock()
	}
}

func (r *scopeRegistry) ForEachScope(f func(*scope)) {
	for _, subscopeBucket := range r.subscopes {
		subscopeBucket.mu.RLock()
		for _, s := range subscopeBucket.s {
			f(s)
		}
		subscopeBucket.mu.RUnlock()
	}
}

func (r *scopeRegistry) Subscope(parent *scope, prefix string, tags map[string]string) *scope {
	if r.root.closed.Load() || parent.closed.Load() {
		return NoopScope.(*scope)
	}

	var (
		buf = keyForPrefixedStringMapsAsKey(make([]byte, 0, 256), prefix, parent.tags, tags)
		h   maphash.Hash
	)

	h.SetSeed(r.seed)
	_, _ = h.Write(buf)
	subscopeBucket := r.subscopes[h.Sum64()%uint64(len(r.subscopes))]

	subscopeBucket.mu.RLock()
	// buf is stack allocated and casting it to a string for lookup from the cache
	// as the memory layout of []byte is a superset of string the below casting is safe and does not do any alloc
	// However it cannot be used outside of the stack; a heap allocation is needed if that string needs to be stored
	// in the map as a key
	var (
		unsanitizedKey = *(*string)(unsafe.Pointer(&buf))
		sanitizedKey   string
	)

	s, ok := r.lockedLookup(subscopeBucket, unsanitizedKey)
	if ok {
		// If this subscope isn't closed, or (in order to preserve historical
		// behavior) if it isn't configured to report on close, return it.
		if !s.closed.Load() || !s.reportOnClose {
			subscopeBucket.mu.RUnlock()
			return s
		}

		switch {
		case parent.reporter != nil:
			s.report(parent.reporter)
		case parent.cachedReporter != nil:
			s.cachedReport()
		}
	}

	tags = parent.copyAndSanitizeMap(tags)
	sanitizedKey = scopeRegistryKey(prefix, parent.tags, tags)

	// If a scope was found above but we didn't return, we need to remove the
	// scope from both keys.
	if ok {
		r.removeWithRLock(subscopeBucket, unsanitizedKey)
		r.removeWithRLock(subscopeBucket, sanitizedKey)
		s.clearMetrics()
	}

	subscopeBucket.mu.RUnlock()

	// Force-allocate the unsafe string as a safe string. Note that neither
	// string(x) nor x+"" will have the desired effect (the former is a nop,
	// and the latter will likely be elided), so append a new character and
	// truncate instead.
	//
	// ref: https://go.dev/play/p/sxhExUKSxCw
	unsanitizedKey = (unsanitizedKey + ".")[:len(unsanitizedKey)]

	subscopeBucket.mu.Lock()
	defer subscopeBucket.mu.Unlock()

	if s, ok := r.lockedLookup(subscopeBucket, sanitizedKey); ok {
		if _, ok = r.lockedLookup(subscopeBucket, unsanitizedKey); !ok {
			subscopeBucket.s[unsanitizedKey] = s
		}
		return s
	}

	allTags := mergeRightTags(parent.tags, tags)
	subscope := &scope{
		separator: parent.separator,
		prefix:    prefix,
		// NB(prateek): don't need to copy the tags here,
		// we assume the map provided is immutable.
		tags:           allTags,
		reporter:       parent.reporter,
		cachedReporter: parent.cachedReporter,
		baseReporter:   parent.baseReporter,
		defaultBuckets: parent.defaultBuckets,
		sanitizer:      parent.sanitizer,
		registry:       parent.registry,

		counters:        make(map[string]*counter),
		countersSlice:   make([]*counter, 0, _defaultInitialSliceSize),
		gauges:          make(map[string]*gauge),
		gaugesSlice:     make([]*gauge, 0, _defaultInitialSliceSize),
		histograms:      make(map[string]*histogram),
		histogramsSlice: make([]*histogram, 0, _defaultInitialSliceSize),
		timers:          make(map[string]*timer),
		bucketCache:     parent.bucketCache,
		done:            make(chan struct{}),
		reportOnClose:   parent.reportOnClose,
	}
	subscopeBucket.s[sanitizedKey] = subscope
	if _, ok := r.lockedLookup(subscopeBucket, unsanitizedKey); !ok {
		subscopeBucket.s[unsanitizedKey] = subscope
	}
	return subscope
}

func (r *scopeRegistry) lockedLookup(subscopeBucket *scopeBucket, key string) (*scope, bool) {
	ss, ok := subscopeBucket.s[key]
	return ss, ok
}

func (r *scopeRegistry) purgeIfRootClosed() {
	if !r.root.closed.Load() {
		return
	}

	for _, subscopeBucket := range r.subscopes {
		subscopeBucket.mu.Lock()
		for k, s := range subscopeBucket.s {
			_ = s.Close()
			s.clearMetrics()
			delete(subscopeBucket.s, k)
		}
		subscopeBucket.mu.Unlock()
	}
}

func (r *scopeRegistry) removeWithRLock(subscopeBucket *scopeBucket, key string) {
	// n.b. This function must lock the registry for writing and return it to an
	//      RLocked state prior to exiting. Defer order is important (LIFO).
	subscopeBucket.mu.RUnlock()
	defer subscopeBucket.mu.RLock()
	subscopeBucket.mu.Lock()
	defer subscopeBucket.mu.Unlock()
	delete(subscopeBucket.s, key)
}

// Records internal Metrics' cardinalities.
func (r *scopeRegistry) reportInternalMetrics() {
	if r.internalMetricsOption != SendInternalMetrics {
		return
	}

	counters, gauges, histograms := atomic.Int64{}, atomic.Int64{}, atomic.Int64{}
	rootCounters, rootGauges, rootHistograms := atomic.Int64{}, atomic.Int64{}, atomic.Int64{}
	r.ForEachScope(
		func(ss *scope) {
			counterSliceLen, gaugeSliceLen, histogramSliceLen := int64(len(ss.countersSlice)), int64(len(ss.gaugesSlice)), int64(len(ss.histogramsSlice))
			if ss.root { // Root scope is referenced across all buckets.
				rootCounters.Store(counterSliceLen)
				rootGauges.Store(gaugeSliceLen)
				rootHistograms.Store(histogramSliceLen)
				return
			}
			counters.Add(counterSliceLen)
			gauges.Add(gaugeSliceLen)
			histograms.Add(histogramSliceLen)
		},
	)

	counters.Add(rootCounters.Load())
	gauges.Add(rootGauges.Load())
	histograms.Add(rootHistograms.Load())

	if r.root.reporter != nil {
		r.root.reporter.ReportCounter(r.sanitizedCounterCardinalityName, internalTags, counters.Load())
		r.root.reporter.ReportCounter(r.sanitizedGaugeCardinalityName, internalTags, gauges.Load())
		r.root.reporter.ReportCounter(r.sanitizedHistogramCardinalityName, internalTags, histograms.Load())
	}

	if r.root.cachedReporter != nil {
		numCounters := r.root.cachedReporter.AllocateCounter(r.sanitizedCounterCardinalityName, internalTags)
		numGauges := r.root.cachedReporter.AllocateCounter(r.sanitizedGaugeCardinalityName, internalTags)
		numHistograms := r.root.cachedReporter.AllocateCounter(r.sanitizedHistogramCardinalityName, internalTags)
		numCounters.ReportCount(counters.Load())
		numGauges.ReportCount(gauges.Load())
		numHistograms.ReportCount(histograms.Load())
	}
}
