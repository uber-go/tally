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
	"bytes"
	"fmt"
	"hash/maphash"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"go.uber.org/atomic"
)

var (
	scopeRegistryKey = keyForPrefixedStringMaps

	// Metrics related.
	counterCardinalityName   = "tally.internal.counter_cardinality"
	gaugeCardinalityName     = "tally.internal.gauge_cardinality"
	histogramCardinalityName = "tally.internal.histogram_cardinality"
	scopeCardinalityName     = "tally.internal.num_active_scopes"

	// bytesBufferPool is a pool for bytes.Buffer to reduce allocations.
	bytesBufferPool = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}
)

const (
	// DefaultTagRedactValue is the default tag value to use when redacting
	DefaultTagRedactValue = "global"
	// HighCardinalityThreshold is the number of subscopes after which caching will be disabled
	HighCardinalityThreshold = 5000
)

// EnableParallelFlush controls whether to use parallel processing for flushing
// when reporting metrics. Set to true for higher performance in production,
// but leave as false for tests to avoid wait group issues with the test reporter.
//
// Based on benchmark results, the optimal configuration for high-cardinality
// environments is:
// - For test environments: EnableParallelFlush=false (sequential processing)
// - For production: EnableParallelFlush=true and MaxParallelFlushGoroutines=4 or 8
//
// The per-shard parallel approach (MaxParallelFlushGoroutines=0) should generally be
// avoided as it can lead to too many goroutines under high-cardinality conditions.
var EnableParallelFlush atomic.Bool

// MaxParallelFlushGoroutines controls the maximum number of goroutines to use for parallel flushing.
// Default value of 0 means use one goroutine per shard. Setting this to a positive number
// will use the specified number of worker goroutines for optimal resource usage.
//
// Recommended values:
// - 0: Use one goroutine per shard (not recommended for high-cardinality cases)
// - 4-8: Provides good performance balance for most workloads
// - 16+: May cause degraded performance due to contention
var MaxParallelFlushGoroutines int

type scopeRegistry struct {
	seed maphash.Seed
	root *scope
	// We need a subscope per GOPROC so that we can take advantage of all the cpu available to the application.
	subscopes []*scopeBucket
	// Internal metrics related.
	omitCardinalityMetrics            bool
	cardinalityMetricsTags            map[string]string
	sanitizedCounterCardinalityName   string
	sanitizedGaugeCardinalityName     string
	sanitizedHistogramCardinalityName string
	sanitizedScopeCardinalityName     string
	cachedCounterCardinalityGauge     CachedGauge
	cachedGaugeCardinalityGauge       CachedGauge
	cachedHistogramCardinalityGauge   CachedGauge
	cachedScopeCardinalityGauge       CachedGauge
	// High cardinality adaptive behavior
	adaptiveMode   int32 // Used as atomic boolean
	totalSubScopes int64 // Used with atomic operations

	// Eviction and LRU policy config
	enableEviction             bool
	maxInactivity              time.Duration
	maxSubscopesBeforeEviction int
	lastEvictionCheck          int64 // Unix timestamp in seconds of last eviction check

	// Scope pooling
	scopePool       sync.Pool
	pooledScopes    map[string]*scope // Scopes available for reuse, indexed by key
	pooledScopesMtx sync.Mutex        // Mutex for pool access
	pooledScopesLRU []string          // Keys in LRU order (oldest first)
	poolAccessTimes map[string]int64  // Last access time for each key
	maxPoolSize     int               // Maximum number of scopes in the pool
	poolSize        int64             // Current size of the pool, accessed atomically
	poolHits        int64             // Track hits for stats
	poolMisses      int64             // Track misses for stats

	// String interner for scope keys
	stringInterner map[string]string
	internerMutex  sync.Mutex
}

type scopeBucket struct {
	mu sync.RWMutex
	s  map[string]*scope
}

func newScopeRegistryWithShardCount(
	root *scope,
	shardCount uint,
	omitCardinalityMetrics bool,
	cardinalityMetricsTags map[string]string,
) *scopeRegistry {
	if shardCount == 0 {
		shardCount = uint(runtime.GOMAXPROCS(-1))
	}

	r := &scopeRegistry{
		root:                              root,
		subscopes:                         make([]*scopeBucket, shardCount),
		seed:                              maphash.MakeSeed(),
		omitCardinalityMetrics:            omitCardinalityMetrics,
		sanitizedCounterCardinalityName:   root.sanitizer.Name(counterCardinalityName),
		sanitizedGaugeCardinalityName:     root.sanitizer.Name(gaugeCardinalityName),
		sanitizedHistogramCardinalityName: root.sanitizer.Name(histogramCardinalityName),
		sanitizedScopeCardinalityName:     root.sanitizer.Name(scopeCardinalityName),
		cardinalityMetricsTags: map[string]string{
			"version":  Version,
			"host":     DefaultTagRedactValue,
			"instance": DefaultTagRedactValue,
		},
		stringInterner: make(map[string]string),
	}

	// Initialize the adaptiveMode and totalSubScopes fields
	atomic.StoreInt32(&r.adaptiveMode, 0)
	atomic.StoreInt64(&r.totalSubScopes, 0)

	for k, v := range cardinalityMetricsTags {
		r.cardinalityMetricsTags[root.sanitizer.Key(k)] = root.sanitizer.Value(v)
	}

	for i := uint(0); i < shardCount; i++ {
		r.subscopes[i] = &scopeBucket{
			s: make(map[string]*scope),
		}
		r.subscopes[i].s[scopeRegistryKey(root.prefix, root.tags)] = root
	}
	if r.root.cachedReporter != nil && !omitCardinalityMetrics {
		r.cachedCounterCardinalityGauge = r.root.cachedReporter.AllocateGauge(r.sanitizedCounterCardinalityName, r.cardinalityMetricsTags)
		r.cachedGaugeCardinalityGauge = r.root.cachedReporter.AllocateGauge(r.sanitizedGaugeCardinalityName, r.cardinalityMetricsTags)
		r.cachedHistogramCardinalityGauge = r.root.cachedReporter.AllocateGauge(r.sanitizedHistogramCardinalityName, r.cardinalityMetricsTags)
		r.cachedScopeCardinalityGauge = r.root.cachedReporter.AllocateGauge(r.sanitizedScopeCardinalityName, r.cardinalityMetricsTags)
	}
	return r
}

// internString returns a canonical representation of the given string,
// reducing allocations if the same string value appears multiple times.
func (r *scopeRegistry) internString(s string) string {
	r.internerMutex.Lock()
	defer r.internerMutex.Unlock()

	interned, ok := r.stringInterner[s]
	if ok {
		return interned
	}

	r.stringInterner[s] = s
	return s
}

// Report processes all scopes and reports their metrics to the provided reporter
func (r *scopeRegistry) Report(reporter StatsReporter) {
	defer r.purgeIfRootClosed()
	r.reportInternalMetrics()

	// Check if we should run eviction
	if r.enableEviction {
		r.evictInactiveScopes()
	}

	// Cleanup the scope pool
	if r.scopePool.New != nil {
		r.cleanupScopePool()
	}

	// Choose between sequential or parallel processing based on the feature flag
	if EnableParallelFlush.Load() {
		if MaxParallelFlushGoroutines > 0 {
			r.workerPoolReport(reporter, MaxParallelFlushGoroutines)
		} else {
			r.parallelReport(reporter)
		}
	} else {
		r.sequentialReport(reporter)
	}
}

// sequentialReport processes all scopes sequentially to avoid waitgroup issues with test reporters
func (r *scopeRegistry) sequentialReport(reporter StatsReporter) {
	for _, subscopeBucket := range r.subscopes {
		// Copy out scope references under lock to minimize lock duration
		scopesToReport := make([]*scope, 0, len(subscopeBucket.s))
		scopeKeys := make([]string, 0, len(subscopeBucket.s))

		subscopeBucket.mu.RLock()
		for name, s := range subscopeBucket.s {
			scopesToReport = append(scopesToReport, s)
			scopeKeys = append(scopeKeys, name)
		}
		subscopeBucket.mu.RUnlock()

		// Process each scope without holding the lock
		for i, s := range scopesToReport {
			safeReportScope(s, reporter)

			// Check if scope is closed after reporting
			if atomic.LoadInt32(&s.closed) != 0 {
				subscopeBucket.mu.Lock()
				delete(subscopeBucket.s, scopeKeys[i])
				subscopeBucket.mu.Unlock()
				s.clearMetrics()
			}
		}
	}
}

// safeReportScope wraps the scope.report call with panic recovery.
func safeReportScope(s *scope, reporter StatsReporter) {
	defer func() {
		if err := recover(); err != nil {
			// TODO: Consider making this logging configurable or sending to an internal metric
			fmt.Printf("tally: panic during scope report for scope %s: %v\n", s.fullyQualifiedName("."), err)
			// Potentially re-panic if it's a critical error, or ensure the reporter's state is clean.
			// For now, we just log and continue, assuming the reporter can handle individual metric failures.
		}
	}()
	s.report(reporter)
}

// parallelReport processes scopes in parallel with one goroutine per shard for high throughput
func (r *scopeRegistry) parallelReport(reporter StatsReporter) {
	var wg sync.WaitGroup
	type closedScopeInfo struct {
		bucket *scopeBucket
		name   string
		scope  *scope
	}

	// Buffer for closed scopes info
	closedScopes := make(chan closedScopeInfo, 100)

	// Process each shard concurrently
	for _, subscopeBucket := range r.subscopes {
		wg.Add(1)
		go func(bucket *scopeBucket) {
			defer wg.Done()

			// Copy scope references under lock to minimize lock duration
			scopesToReport := make([]*scope, 0, len(bucket.s))
			scopeKeys := make([]string, 0, len(bucket.s))

			bucket.mu.RLock()
			for name, s := range bucket.s {
				scopesToReport = append(scopesToReport, s)
				scopeKeys = append(scopeKeys, name)
			}
			bucket.mu.RUnlock()

			// Process each scope without holding the lock
			for i, s := range scopesToReport {
				safeReportScope(s, reporter)

				// Check if scope is closed after reporting
				if atomic.LoadInt32(&s.closed) != 0 {
					closedScopes <- closedScopeInfo{
						bucket: bucket,
						name:   scopeKeys[i],
						scope:  s,
					}
				}
			}
		}(subscopeBucket)
	}

	// Close channel when all reporting is done
	go func() {
		wg.Wait()
		close(closedScopes)
	}()

	// Process closed scopes outside the main reporting path
	for info := range closedScopes {
		info.bucket.mu.Lock()
		delete(info.bucket.s, info.name)
		info.bucket.mu.Unlock()
		info.scope.clearMetrics()
	}
}

// workerPoolReport processes scopes using a fixed-size worker pool for better resource control
func (r *scopeRegistry) workerPoolReport(reporter StatsReporter, numWorkers int) {
	var wg sync.WaitGroup

	// First gather all scope references under minimal lock contention
	type scopeBatch struct {
		bucket    *scopeBucket
		scopes    []*scope
		scopeKeys []string
	}

	// Collect all scopes across all buckets
	var batches []scopeBatch

	for _, subscopeBucket := range r.subscopes {
		batch := scopeBatch{
			bucket: subscopeBucket,
		}

		subscopeBucket.mu.RLock()
		batch.scopes = make([]*scope, 0, len(subscopeBucket.s))
		batch.scopeKeys = make([]string, 0, len(subscopeBucket.s))

		for name, s := range subscopeBucket.s {
			batch.scopes = append(batch.scopes, s)
			batch.scopeKeys = append(batch.scopeKeys, name)
		}
		subscopeBucket.mu.RUnlock()

		if len(batch.scopes) > 0 {
			batches = append(batches, batch)
		}
	}

	// If we have no scopes to process, return early
	if len(batches) == 0 {
		return
	}

	// Split the work evenly among workers
	type workItem struct {
		scope    *scope
		scopeKey string
		bucket   *scopeBucket
	}

	// Create work queue - estimate size based on average batch
	var totalScopes int
	for _, batch := range batches {
		totalScopes += len(batch.scopes)
	}

	workChan := make(chan workItem, totalScopes)
	closedScopesChan := make(chan workItem, totalScopes) // Channel for scopes found to be closed

	// Fill work queue
	for _, batch := range batches {
		for scopeIdx, s := range batch.scopes {
			workChan <- workItem{
				scope:    s,
				scopeKey: batch.scopeKeys[scopeIdx],
				bucket:   batch.bucket,
			}
		}
	}
	close(workChan)

	// Start worker pool
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			for item := range workChan {
				safeReportScope(item.scope, reporter)

				// Check if scope is closed
				if atomic.LoadInt32(&item.scope.closed) != 0 {
					closedScopesChan <- item
				}
			}
		}()
	}

	// Close result channel when all workers are done
	go func() {
		wg.Wait()
		close(closedScopesChan)
	}()

	// Process closed scopes
	for item := range closedScopesChan {
		item.bucket.mu.Lock()
		delete(item.bucket.s, item.scopeKey)
		item.bucket.mu.Unlock()
		item.scope.clearMetrics()
	}
}

// CachedReport processes all scopes and reports their metrics via the cached reporter
func (r *scopeRegistry) CachedReport() {
	defer r.purgeIfRootClosed()
	r.reportInternalMetrics()

	// Check if we should run eviction
	if r.enableEviction {
		r.evictInactiveScopes()
	}

	// Cleanup the scope pool
	if r.scopePool.New != nil {
		r.cleanupScopePool()
	}

	// Choose between sequential or parallel processing based on the feature flag
	if EnableParallelFlush.Load() {
		if MaxParallelFlushGoroutines > 0 {
			r.workerPoolCachedReport(MaxParallelFlushGoroutines)
		} else {
			r.parallelCachedReport()
		}
	} else {
		r.sequentialCachedReport()
	}
}

// sequentialCachedReport processes all scopes sequentially to avoid waitgroup issues
func (r *scopeRegistry) sequentialCachedReport() {
	for _, subscopeBucket := range r.subscopes {
		// Copy out scope references under lock to minimize lock duration
		scopesToReport := make([]*scope, 0, len(subscopeBucket.s))
		scopeKeys := make([]string, 0, len(subscopeBucket.s))

		subscopeBucket.mu.RLock()
		for name, s := range subscopeBucket.s {
			scopesToReport = append(scopesToReport, s)
			scopeKeys = append(scopeKeys, name)
		}
		subscopeBucket.mu.RUnlock()

		// Process each scope without holding the lock
		for i, s := range scopesToReport {
			safeCachedReportScope(s)

			// Check if scope is closed after reporting
			if atomic.LoadInt32(&s.closed) != 0 {
				subscopeBucket.mu.Lock()
				delete(subscopeBucket.s, scopeKeys[i])
				subscopeBucket.mu.Unlock()
				s.clearMetrics()
			}
		}
	}
}

// safeCachedReportScope wraps the scope.cachedReport call with panic recovery.
func safeCachedReportScope(s *scope) {
	defer func() {
		if err := recover(); err != nil {
			// TODO: Consider making this logging configurable or sending to an internal metric
			fmt.Printf("tally: panic during scope cachedReport for scope %s: %v\n", s.fullyQualifiedName("."), err)
		}
	}()
	s.cachedReport()
}

// parallelCachedReport processes scopes in parallel with one goroutine per shard
func (r *scopeRegistry) parallelCachedReport() {
	var wg sync.WaitGroup
	type closedScopeInfo struct {
		bucket *scopeBucket
		name   string
		scope  *scope
	}

	// Buffer for closed scopes info
	closedScopes := make(chan closedScopeInfo, 100)

	// Process each shard concurrently
	for _, subscopeBucket := range r.subscopes {
		wg.Add(1)
		go func(bucket *scopeBucket) {
			defer wg.Done()

			// Copy scope references under lock to minimize lock duration
			scopesToReport := make([]*scope, 0, len(bucket.s))
			scopeKeys := make([]string, 0, len(bucket.s))

			bucket.mu.RLock()
			for name, s := range bucket.s {
				scopesToReport = append(scopesToReport, s)
				scopeKeys = append(scopeKeys, name)
			}
			bucket.mu.RUnlock()

			// Process each scope without holding the lock
			for i, s := range scopesToReport {
				safeCachedReportScope(s)

				// Check if scope is closed after reporting
				if atomic.LoadInt32(&s.closed) != 0 {
					closedScopes <- closedScopeInfo{
						bucket: bucket,
						name:   scopeKeys[i],
						scope:  s,
					}
				}
			}
		}(subscopeBucket)
	}

	// Close channel when all reporting is done
	go func() {
		wg.Wait()
		close(closedScopes)
	}()

	// Process closed scopes outside the main reporting path
	for info := range closedScopes {
		info.bucket.mu.Lock()
		delete(info.bucket.s, info.name)
		info.bucket.mu.Unlock()
		info.scope.clearMetrics()
	}
}

// workerPoolCachedReport processes scopes using a fixed-size worker pool
func (r *scopeRegistry) workerPoolCachedReport(numWorkers int) {
	var wg sync.WaitGroup

	// First gather all scope references under minimal lock contention
	type scopeBatch struct {
		bucket    *scopeBucket
		scopes    []*scope
		scopeKeys []string
	}

	// Collect all scopes across all buckets
	var batches []scopeBatch

	for _, subscopeBucket := range r.subscopes {
		batch := scopeBatch{
			bucket: subscopeBucket,
		}

		subscopeBucket.mu.RLock()
		batch.scopes = make([]*scope, 0, len(subscopeBucket.s))
		batch.scopeKeys = make([]string, 0, len(subscopeBucket.s))

		for name, s := range subscopeBucket.s {
			batch.scopes = append(batch.scopes, s)
			batch.scopeKeys = append(batch.scopeKeys, name)
		}
		subscopeBucket.mu.RUnlock()

		if len(batch.scopes) > 0 {
			batches = append(batches, batch)
		}
	}

	// If we have no scopes to process, return early
	if len(batches) == 0 {
		return
	}

	// Split the work evenly among workers
	type workItem struct {
		scope    *scope
		scopeKey string
		bucket   *scopeBucket
	}

	// Create work queue
	var totalScopes int
	for _, batch := range batches {
		totalScopes += len(batch.scopes)
	}
	workChan := make(chan workItem, totalScopes)
	closedScopesChan := make(chan workItem, totalScopes) // Channel for scopes found to be closed

	// Fill work queue
	for _, batch := range batches {
		for scopeIdx, s := range batch.scopes {
			workChan <- workItem{
				scope:    s,
				scopeKey: batch.scopeKeys[scopeIdx],
				bucket:   batch.bucket,
			}
		}
	}
	close(workChan)

	// Start worker pool
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			for item := range workChan {
				safeCachedReportScope(item.scope)

				if atomic.LoadInt32(&item.scope.closed) != 0 {
					closedScopesChan <- item
				}
			}
		}()
	}

	// Close result channel when all workers are done
	go func() {
		wg.Wait()
		close(closedScopesChan)
	}()

	// Process closed scopes
	for item := range closedScopesChan {
		item.bucket.mu.Lock()
		delete(item.bucket.s, item.scopeKey)
		item.bucket.mu.Unlock()
		item.scope.clearMetrics()
	}
}

func (r *scopeRegistry) ForEachScope(f func(*scope)) {
	// Process each shard sequentially, but minimize lock hold time
	for _, subscopeBucket := range r.subscopes {
		// Copy scope references under lock to minimize lock duration
		var scopesToProcess []*scope

		subscopeBucket.mu.RLock()
		scopesToProcess = make([]*scope, 0, len(subscopeBucket.s))
		for _, s := range subscopeBucket.s {
			scopesToProcess = append(scopesToProcess, s)
		}
		subscopeBucket.mu.RUnlock()

		// Process the scopes outside the lock
		for _, s := range scopesToProcess {
			f(s)
		}
	}
}

func (r *scopeRegistry) Subscope(parent *scope, prefix string, tags map[string]string) *scope {
	if atomic.LoadInt32(&parent.closed) != 0 || atomic.LoadInt32(&r.root.closed) != 0 {
		return NoopScope.(*scope)
	}

	// If NoCacheSubscopes is enabled on parent scope, create an ephemeral scope
	if parent.noCacheSubscopes || atomic.LoadInt32(&r.adaptiveMode) == 1 {
		allTags := mergeRightTags(parent.tags, tags)

		// For ephemeral scopes, we'll generate a key for pooling purposes only
		rawEphemeralKey := poolKey(prefix, allTags)
		ephemeralKey := r.internString(rawEphemeralKey)

		// Get a scope from the pool if available
		if r.scopePool.New != nil {
			// First, try to find an existing scope in our pool map
			r.pooledScopesMtx.Lock()
			existingScope, exists := r.pooledScopes[ephemeralKey]
			if exists {
				// Remove it from the map but keep the scope
				delete(r.pooledScopes, ephemeralKey)

				// Remove from LRU list
				for i, key := range r.pooledScopesLRU {
					if key == ephemeralKey {
						// Remove the key by replacing with the last element and truncating
						lastIdx := len(r.pooledScopesLRU) - 1
						if i < lastIdx {
							r.pooledScopesLRU[i] = r.pooledScopesLRU[lastIdx]
						}
						r.pooledScopesLRU = r.pooledScopesLRU[:lastIdx]
						break
					}
				}

				// Delete from access times
				delete(r.poolAccessTimes, ephemeralKey)

				// Update pool size
				atomic.AddInt64(&r.poolSize, -1)

				// Track pool hit
				atomic.AddInt64(&r.poolHits, 1)

				r.pooledScopesMtx.Unlock()

				// Make sure it's not marked as closed
				atomic.StoreInt32(&existingScope.closed, 0)
				// Update activity timestamp
				atomic.StoreInt64(&existingScope.lastActivity, time.Now().Unix())

				return existingScope
			}

			// Track pool miss
			atomic.AddInt64(&r.poolMisses, 1)
			r.pooledScopesMtx.Unlock()

			// Get a new scope from the pool
			newScope := r.scopePool.Get().(*scope)

			// Reset all maps to be safe
			newScope.resetForPool()

			// Initialize the pooled scope with the new values
			newScope.separator = parent.separator
			newScope.prefix = prefix
			newScope.tags = allTags
			newScope.reporter = parent.reporter
			newScope.cachedReporter = parent.cachedReporter
			newScope.baseReporter = parent.baseReporter
			newScope.defaultBuckets = parent.defaultBuckets
			newScope.sanitizer = parent.sanitizer
			newScope.registry = r
			newScope.bucketCache = parent.bucketCache
			newScope.testScope = parent.testScope
			newScope.noCacheSubscopes = parent.noCacheSubscopes
			newScope.ephemeralKey = ephemeralKey

			// Make sure lastActivity is current
			atomic.StoreInt64(&newScope.lastActivity, time.Now().Unix())

			// Ensure the scope is marked as not closed
			atomic.StoreInt32(&newScope.closed, 0)

			return newScope
		}

		// If pooling is not enabled, create a new scope
		return &scope{
			separator:      parent.separator,
			prefix:         prefix,
			tags:           allTags,
			reporter:       parent.reporter,
			cachedReporter: parent.cachedReporter,
			baseReporter:   parent.baseReporter,
			defaultBuckets: parent.defaultBuckets,
			sanitizer:      parent.sanitizer,
			registry:       r,
			bucketCache:    parent.bucketCache,
			ephemeralKey:   ephemeralKey,

			// sync.Map is zero-value ready and doesn't need initialization
			countersSlice:      make([]*counter, 0, _defaultInitialSliceSize),
			countersSliceMux:   sync.Mutex{},
			gaugesSlice:        make([]*gauge, 0, _defaultInitialSliceSize),
			gaugesSliceMux:     sync.Mutex{},
			histogramsSlice:    make([]*histogram, 0, _defaultInitialSliceSize),
			histogramsSliceMux: sync.Mutex{},
			done:               make(chan struct{}),
			testScope:          parent.testScope,
			noCacheSubscopes:   parent.noCacheSubscopes,
		}
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
		// If this subscope isn't closed or is a test scope, return it.
		// Otherwise, report it immediately and delete it so that a new
		// (functional) scope can be returned instead.
		if atomic.LoadInt32(&s.closed) == 0 || s.testScope {
			subscopeBucket.mu.RUnlock()
			return s
		}

		// This scope is closed; if it can still report, flush it in place.
		if s.baseReporter != nil {
			// If this scope can report, do so in place.
			if s.reporter != nil {
				s.report(s.reporter)
			} else if s.cachedReporter != nil {
				s.cachedReport()
			}
		}

		// Recycle the scope so that we can replace it with a new one.
		// We perform this operation while holding the registry lock to
		// emulate the semantics of a cache eviction policy.
		r.removeWithRLock(subscopeBucket, unsanitizedKey)
		s.clearMetrics()
		subscopeBucket.mu.RUnlock()
	} else {
		subscopeBucket.mu.RUnlock()
		// Sanitize the key before adding it to the bucket.
		// This is safe as the scope key is stored only to help avoid
		// recreating the sanitized key if we don't have to.
		keyCandidate := string(buf)
		sanitizedKey = r.internString(keyCandidate)
	}

	// A sanitized key is a new heap allocation, so it's safe to put
	// it into a map.
	s = &scope{
		separator:      parent.separator,
		prefix:         prefix,
		tags:           mergeRightTags(parent.tags, tags),
		reporter:       parent.reporter,
		cachedReporter: parent.cachedReporter,
		baseReporter:   parent.baseReporter,
		defaultBuckets: parent.defaultBuckets,
		sanitizer:      parent.sanitizer,
		registry:       r,
		bucketCache:    parent.bucketCache,

		// sync.Map is zero-value ready and doesn't need initialization
		countersSlice:      make([]*counter, 0, _defaultInitialSliceSize),
		countersSliceMux:   sync.Mutex{},
		gaugesSlice:        make([]*gauge, 0, _defaultInitialSliceSize),
		gaugesSliceMux:     sync.Mutex{},
		histogramsSlice:    make([]*histogram, 0, _defaultInitialSliceSize),
		histogramsSliceMux: sync.Mutex{},
		done:               make(chan struct{}),
		testScope:          parent.testScope,
		noCacheSubscopes:   parent.noCacheSubscopes,
	}

	// Initialize lastActivity timestamp
	atomic.StoreInt64(&s.lastActivity, time.Now().Unix())

	subscopeBucket.mu.Lock()
	// Since this is a heap allocation, re-examine the cache first. We may have evicted it during a reporting cycle.
	s2, ok := subscopeBucket.s[sanitizedKey]
	if ok {
		subscopeBucket.mu.Unlock()
		return s2
	}
	subscopeBucket.s[sanitizedKey] = s
	subscopeBucket.mu.Unlock()

	// Increment the total subscopes counter for adaptive monitoring
	atomic.AddInt64(&r.totalSubScopes, 1)

	// If we've exceeded the high cardinality threshold, switch to adaptive mode
	if atomic.LoadInt32(&r.adaptiveMode) == 0 && atomic.LoadInt64(&r.totalSubScopes) > HighCardinalityThreshold {
		atomic.StoreInt32(&r.adaptiveMode, 1)
	}

	return s
}

func (r *scopeRegistry) lockedLookup(subscopeBucket *scopeBucket, key string) (*scope, bool) {
	ss, ok := subscopeBucket.s[key]
	return ss, ok
}

func (r *scopeRegistry) purgeIfRootClosed() {
	if atomic.LoadInt32(&r.root.closed) == 0 {
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
	if r.omitCardinalityMetrics {
		return
	}

	counters, gauges, histograms, scopes := atomic.Int64{}, atomic.Int64{}, atomic.Int64{}, atomic.Int64{}
	rootCounters, rootGauges, rootHistograms := atomic.Int64{}, atomic.Int64{}, atomic.Int64{}
	scopes.Inc() // Account for root scope.
	r.ForEachScope(
		func(ss *scope) {
			ss.countersSliceMux.Lock()
			counterSliceLen := int64(len(ss.countersSlice))
			ss.countersSliceMux.Unlock()

			ss.gaugesSliceMux.Lock()
			gaugeSliceLen := int64(len(ss.gaugesSlice))
			ss.gaugesSliceMux.Unlock()

			ss.histogramsSliceMux.Lock()
			histogramSliceLen := int64(len(ss.histogramsSlice))
			ss.histogramsSliceMux.Unlock()

			if ss.root { // Root scope is referenced across all buckets.
				rootCounters.Store(counterSliceLen)
				rootGauges.Store(gaugeSliceLen)
				rootHistograms.Store(histogramSliceLen)
				return
			}
			counters.Add(counterSliceLen)
			gauges.Add(gaugeSliceLen)
			histograms.Add(histogramSliceLen)
			scopes.Inc()
		},
	)

	counters.Add(rootCounters.Load())
	gauges.Add(rootGauges.Load())
	histograms.Add(rootHistograms.Load())
	if r.root.reporter != nil {
		r.root.reporter.ReportGauge(r.sanitizedCounterCardinalityName, r.cardinalityMetricsTags, float64(counters.Load()))
		r.root.reporter.ReportGauge(r.sanitizedGaugeCardinalityName, r.cardinalityMetricsTags, float64(gauges.Load()))
		r.root.reporter.ReportGauge(r.sanitizedHistogramCardinalityName, r.cardinalityMetricsTags, float64(histograms.Load()))
		r.root.reporter.ReportGauge(r.sanitizedScopeCardinalityName, r.cardinalityMetricsTags, float64(scopes.Load()))
	}

	if r.root.cachedReporter != nil {
		r.cachedCounterCardinalityGauge.ReportGauge(float64(counters.Load()))
		r.cachedGaugeCardinalityGauge.ReportGauge(float64(gauges.Load()))
		r.cachedHistogramCardinalityGauge.ReportGauge(float64(histograms.Load()))
		r.cachedScopeCardinalityGauge.ReportGauge(float64(scopes.Load()))
	}
}

// preAllocateEmptyMaps creates empty maps with preallocated capacity
// to avoid frequent reallocations when adding metrics to scopes
func preAllocateEmptyMaps() (
	counters map[string]*counter,
	countersSlice []*counter,
	gauges map[string]*gauge,
	gaugesSlice []*gauge,
	histograms map[string]*histogram,
	histogramsSlice []*histogram,
	timers map[string]*timer,
) {
	counters = make(map[string]*counter, _defaultInitialSliceSize)
	countersSlice = make([]*counter, 0, _defaultInitialSliceSize)
	gauges = make(map[string]*gauge, _defaultInitialSliceSize)
	gaugesSlice = make([]*gauge, 0, _defaultInitialSliceSize)
	histograms = make(map[string]*histogram, _defaultInitialSliceSize)
	histogramsSlice = make([]*histogram, 0, _defaultInitialSliceSize)
	timers = make(map[string]*timer, _defaultInitialSliceSize)

	return
}

func newScopeRegistryWithEvictionOptions(
	root *scope,
	shardCount uint,
	omitCardinalityMetrics bool,
	cardinalityMetricsTags map[string]string,
	enableEviction bool,
	maxInactivity time.Duration,
	maxSubscopesBeforeEviction int,
	enableScopePooling bool,
) *scopeRegistry {
	r := newScopeRegistryWithShardCount(root, shardCount, omitCardinalityMetrics, cardinalityMetricsTags)

	// Set eviction options
	r.enableEviction = enableEviction
	r.maxInactivity = maxInactivity
	r.maxSubscopesBeforeEviction = maxSubscopesBeforeEviction
	atomic.StoreInt64(&r.lastEvictionCheck, time.Now().Unix())

	// Initialize scope pool if pooling is enabled
	if enableScopePooling {
		r.pooledScopes = make(map[string]*scope)
		r.pooledScopesLRU = make([]string, 0, 128)
		r.poolAccessTimes = make(map[string]int64)
		r.maxPoolSize = 1000 // Cap the pool size to prevent memory issues
		r.scopePool = sync.Pool{
			New: func() interface{} {
				// Get pre-allocated slice capacities from the helper but discard the maps
				_, countersSlice, _, gaugesSlice,
					_, histogramsSlice, _ := preAllocateEmptyMaps()

				return &scope{
					// sync.Map is zero-value ready and doesn't need initialization
					countersSlice:      countersSlice,
					countersSliceMux:   sync.Mutex{},
					gaugesSlice:        gaugesSlice,
					gaugesSliceMux:     sync.Mutex{},
					histogramsSlice:    histogramsSlice,
					histogramsSliceMux: sync.Mutex{},
					done:               make(chan struct{}),
					registry:           r,
				}
			},
		}
	}

	return r
}

// evictInactiveScopes removes scopes that have been inactive for too long
func (r *scopeRegistry) evictInactiveScopes() {
	now := time.Now().Unix()

	// Only run eviction check periodically (every 60 seconds)
	lastCheck := atomic.LoadInt64(&r.lastEvictionCheck)
	if now-lastCheck < 60 {
		return
	}
	atomic.StoreInt64(&r.lastEvictionCheck, now)

	// Check if we have exceeded the max number of subscopes
	if r.maxSubscopesBeforeEviction > 0 && atomic.LoadInt64(&r.totalSubScopes) > int64(r.maxSubscopesBeforeEviction) {
		// We have too many subscopes, enforce eviction
		r.forceEviction(now)
		return
	}

	// If time-based eviction is disabled, return
	if r.maxInactivity <= 0 {
		return
	}

	// Time-based eviction: Check each subscope for inactivity
	cutoffTime := now - int64(r.maxInactivity.Seconds())

	for _, subscopeBucket := range r.subscopes {
		subscopeBucket.mu.RLock()

		var toEvict []string
		var scopes []*scope
		for name, s := range subscopeBucket.s {
			// Skip the root scope
			if s.root {
				continue
			}

			lastActivity := atomic.LoadInt64(&s.lastActivity)
			if lastActivity < cutoffTime {
				toEvict = append(toEvict, name)
				scopes = append(scopes, s)
			}
		}

		subscopeBucket.mu.RUnlock()

		// Evict inactive scopes
		if len(toEvict) > 0 {
			subscopeBucket.mu.Lock()
			for _, name := range toEvict {
				if s, ok := subscopeBucket.s[name]; ok {
					// Report metrics before evicting
					if r.root.reporter != nil {
						s.report(r.root.reporter)
					} else if r.root.cachedReporter != nil {
						s.cachedReport()
					}
					s.clearMetrics()
					delete(subscopeBucket.s, name)
					atomic.AddInt64(&r.totalSubScopes, -1)

					// Return the scope to the pool for reuse
					s.resetForPool()
					// Only return to pool if it exists
					if r.scopePool.New != nil {
						r.scopePool.Put(s)
					}
				}
			}
			subscopeBucket.mu.Unlock()
		}
	}
}

// forceEviction evicts a percentage of the least recently used scopes
func (r *scopeRegistry) forceEviction(now int64) {
	// Structure to track scopes for eviction
	type scopeToEvict struct {
		bucket *scopeBucket
		name   string
		scope  *scope
	}

	// Collect all non-root scopes with their activity time
	var allScopes []scopeToEvict

	// First pass: collect scopes
	for _, subscopeBucket := range r.subscopes {
		subscopeBucket.mu.RLock()
		for name, s := range subscopeBucket.s {
			if !s.root {
				allScopes = append(allScopes, scopeToEvict{
					bucket: subscopeBucket,
					name:   name,
					scope:  s,
				})
			}
		}
		subscopeBucket.mu.RUnlock()
	}

	// Sort by activity time (oldest first)
	sort.Slice(allScopes, func(i, j int) bool {
		return atomic.LoadInt64(&allScopes[i].scope.lastActivity) < atomic.LoadInt64(&allScopes[j].scope.lastActivity)
	})

	// Determine how many to evict - aim to evict 25% of scopes beyond the threshold
	overageCount := int(atomic.LoadInt64(&r.totalSubScopes)) - r.maxSubscopesBeforeEviction
	if overageCount <= 0 {
		return
	}

	evictionCount := overageCount / 4
	if evictionCount < 10 {
		evictionCount = 10 // Always evict at least 10 to avoid frequent eviction cycles
	}
	if evictionCount > len(allScopes)/2 {
		evictionCount = len(allScopes) / 2 // Don't evict more than half the scopes at once
	}

	// Only consider up to evictionCount oldest scopes
	if evictionCount > len(allScopes) {
		evictionCount = len(allScopes)
	}

	// Second pass: evict the selected scopes
	for i := 0; i < evictionCount; i++ {
		toEvict := allScopes[i]
		bucket := toEvict.bucket
		bucket.mu.Lock()

		// Check if the scope still exists (it might have been removed by another operation)
		if s, ok := bucket.s[toEvict.name]; ok {
			// Report metrics before evicting
			if r.root.reporter != nil {
				s.report(r.root.reporter)
			} else if r.root.cachedReporter != nil {
				s.cachedReport()
			}
			s.clearMetrics()
			delete(bucket.s, toEvict.name)
			atomic.AddInt64(&r.totalSubScopes, -1)

			// Return the scope to the pool for reuse
			s.resetForPool()
			// Only return to pool if it exists
			if r.scopePool.New != nil {
				r.scopePool.Put(s)
			}
		}

		bucket.mu.Unlock()
	}
}

// Custom key generator for pooled scopes
func poolKey(prefix string, tags map[string]string) string {
	// Use a faster key generation to avoid string concatenation costs
	buf := bytesBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bytesBufferPool.Put(buf)

	buf.WriteString(prefix)
	buf.WriteByte(':')

	// Sort keys for deterministic ordering
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.WriteString(tags[k])
		buf.WriteByte(',')
	}

	return buf.String()
}

// cleanupScopePool removes stale scopes from the pool that haven't been used recently
func (r *scopeRegistry) cleanupScopePool() {
	// Only run cleanup periodically
	now := time.Now().Unix()
	lastCheck := atomic.LoadInt64(&r.lastEvictionCheck)
	if now-lastCheck < 60 { // Only check once per minute
		return
	}

	// Update last check time
	atomic.StoreInt64(&r.lastEvictionCheck, now)

	r.pooledScopesMtx.Lock()
	defer r.pooledScopesMtx.Unlock()

	// Nothing to clean if pool is empty
	if len(r.pooledScopes) == 0 {
		return
	}

	// Calculate pooling statistics
	hitRate := float64(0)
	if totalOps := atomic.LoadInt64(&r.poolHits) + atomic.LoadInt64(&r.poolMisses); totalOps > 0 {
		hitRate = float64(atomic.LoadInt64(&r.poolHits)) / float64(totalOps) * 100
	}

	// Adjust pool size based on hit rate
	// If hit rate is high, we can increase max pool size
	if hitRate > 90 && r.maxPoolSize < 2000 {
		r.maxPoolSize += 100
	} else if hitRate < 30 && r.maxPoolSize > 100 {
		r.maxPoolSize -= 50
	}

	// Remove old scopes - anything not accessed in the last 5 minutes
	cutoffTime := now - 300 // 5 minutes
	var removedCount int

	// Start with the oldest scopes first (front of LRU list)
	i := 0
	for i < len(r.pooledScopesLRU) {
		key := r.pooledScopesLRU[i]
		accessTime, exists := r.poolAccessTimes[key]

		// If too old or we need to reduce pool size
		if !exists || accessTime < cutoffTime || len(r.pooledScopes) > r.maxPoolSize {
			// Get the scope and remove it from all tracking
			scope := r.pooledScopes[key]
			delete(r.pooledScopes, key)
			delete(r.poolAccessTimes, key)

			// Remove from LRU by swapping with last element and truncating
			lastIdx := len(r.pooledScopesLRU) - 1
			if i < lastIdx {
				r.pooledScopesLRU[i] = r.pooledScopesLRU[lastIdx]
			}
			r.pooledScopesLRU = r.pooledScopesLRU[:lastIdx]

			// Return to generic pool
			if scope != nil {
				r.scopePool.Put(scope)
			}

			// Update counter
			removedCount++
		} else {
			// Only increment if we didn't remove this entry
			i++
		}
	}

	// Update pool size counter
	atomic.StoreInt64(&r.poolSize, int64(len(r.pooledScopes)))

	// Reset hit/miss counters periodically
	if now%3600 == 0 { // Reset every hour
		atomic.StoreInt64(&r.poolHits, 0)
		atomic.StoreInt64(&r.poolMisses, 0)
	}
}

// scopeClosureInfo holds the information needed to clean up a closed scope
type scopeClosureInfo struct {
	bucket *scopeBucket
	name   string
	scope  *scope
}

// EnableOptimizedFlush configures the flush mechanism for optimal performance
// in production environments. This reduces lock contention during flush operations
// by using a worker pool approach with the recommended number of workers.
//
// This is particularly beneficial for high-cardinality metric workloads
// where many metrics need to be reported concurrently.
func EnableOptimizedFlush() {
	// Enable parallel processing
	EnableParallelFlush.Store(true)

	// Use a number of workers based on CPU cores, but capped to avoid contention
	numCPU := runtime.GOMAXPROCS(-1)
	if numCPU <= 4 {
		MaxParallelFlushGoroutines = numCPU
	} else {
		// Use 75% of available CPUs, minimum 4, maximum 8
		workers := (numCPU * 3) / 4
		if workers < 4 {
			workers = 4
		}
		if workers > 8 {
			workers = 8
		}
		MaxParallelFlushGoroutines = workers
	}
}

// DisableOptimizedFlush sets the flush settings back to sequential.
// Useful for testing or environments where parallelism is not desired.
func DisableOptimizedFlush() {
	EnableParallelFlush.Store(false)
	MaxParallelFlushGoroutines = 0
}
