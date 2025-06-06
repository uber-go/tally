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

	uberatomic "go.uber.org/atomic"
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

	// Specialized pools for tag serialization buffers
	// These are optimized for different key building scenarios
	keySerializationPools = struct {
		// Small buffer pool for simple scope keys (typically < 128 bytes)
		small sync.Pool
		// Medium buffer pool for moderate scope keys (typically < 512 bytes)
		medium sync.Pool
		// Large buffer pool for complex scope keys (typically < 1024 bytes)
		large sync.Pool
	}{
		small: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 0, 128)
				return &buf
			},
		},
		medium: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 0, 512)
				return &buf
			},
		},
		large: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 0, 1024)
				return &buf
			},
		},
	}

	// String slice pools for key generation and sorting
	// These eliminate allocations during tag key collection and sorting
	stringSlicePools = struct {
		small  sync.Pool // For small tag collections (< 8 keys)
		medium sync.Pool // For medium tag collections (< 16 keys)
		large  sync.Pool // For large tag collections (< 32 keys)
	}{
		small: sync.Pool{
			New: func() interface{} {
				slice := make([]string, 0, 8)
				return &slice
			},
		},
		medium: sync.Pool{
			New: func() interface{} {
				slice := make([]string, 0, 16)
				return &slice
			},
		},
		large: sync.Pool{
			New: func() interface{} {
				slice := make([]string, 0, 32)
				return &slice
			},
		},
	}

	// Phase 4: Advanced metric slice and tag map pools
	// These eliminate the frequent allocation of metric tracking slices and tag maps
	metricSlicePools = struct {
		// Counter slice pools
		counterSmall  sync.Pool // []*counter with cap 16
		counterMedium sync.Pool // []*counter with cap 64
		counterLarge  sync.Pool // []*counter with cap 256

		// Gauge slice pools
		gaugeSmall  sync.Pool // []*gauge with cap 16
		gaugeMedium sync.Pool // []*gauge with cap 64
		gaugeLarge  sync.Pool // []*gauge with cap 256

		// Histogram slice pools
		histogramSmall  sync.Pool // []*histogram with cap 16
		histogramMedium sync.Pool // []*histogram with cap 64
		histogramLarge  sync.Pool // []*histogram with cap 256
	}{
		counterSmall: sync.Pool{
			New: func() interface{} {
				slice := make([]*counter, 0, 16)
				return &slice
			},
		},
		counterMedium: sync.Pool{
			New: func() interface{} {
				slice := make([]*counter, 0, 64)
				return &slice
			},
		},
		counterLarge: sync.Pool{
			New: func() interface{} {
				slice := make([]*counter, 0, 256)
				return &slice
			},
		},
		gaugeSmall: sync.Pool{
			New: func() interface{} {
				slice := make([]*gauge, 0, 16)
				return &slice
			},
		},
		gaugeMedium: sync.Pool{
			New: func() interface{} {
				slice := make([]*gauge, 0, 64)
				return &slice
			},
		},
		gaugeLarge: sync.Pool{
			New: func() interface{} {
				slice := make([]*gauge, 0, 256)
				return &slice
			},
		},
		histogramSmall: sync.Pool{
			New: func() interface{} {
				slice := make([]*histogram, 0, 16)
				return &slice
			},
		},
		histogramMedium: sync.Pool{
			New: func() interface{} {
				slice := make([]*histogram, 0, 64)
				return &slice
			},
		},
		histogramLarge: sync.Pool{
			New: func() interface{} {
				slice := make([]*histogram, 0, 256)
				return &slice
			},
		},
	}

	// Tag map pools for efficient tag merging operations
	// These eliminate allocations during tag map merging in subscope creation
	tagMapPools = struct {
		small  sync.Pool // map[string]string with initial capacity 8
		medium sync.Pool // map[string]string with initial capacity 16
		large  sync.Pool // map[string]string with initial capacity 32
	}{
		small: sync.Pool{
			New: func() interface{} {
				m := make(map[string]string, 8)
				return &m
			},
		},
		medium: sync.Pool{
			New: func() interface{} {
				m := make(map[string]string, 16)
				return &m
			},
		},
		large: sync.Pool{
			New: func() interface{} {
				m := make(map[string]string, 32)
				return &m
			},
		},
	}

	// Snapshot map pools for efficient snapshot creation
	// These eliminate allocations during Snapshot() operations
	snapshotMapPools = struct {
		counterMaps   sync.Pool // map[string]CounterSnapshot
		gaugeMaps     sync.Pool // map[string]GaugeSnapshot
		timerMaps     sync.Pool // map[string]TimerSnapshot
		histogramMaps sync.Pool // map[string]HistogramSnapshot
	}{
		counterMaps: sync.Pool{
			New: func() interface{} {
				m := make(map[string]CounterSnapshot, 16)
				return &m
			},
		},
		gaugeMaps: sync.Pool{
			New: func() interface{} {
				m := make(map[string]GaugeSnapshot, 16)
				return &m
			},
		},
		timerMaps: sync.Pool{
			New: func() interface{} {
				m := make(map[string]TimerSnapshot, 16)
				return &m
			},
		},
		histogramMaps: sync.Pool{
			New: func() interface{} {
				m := make(map[string]HistogramSnapshot, 16)
				return &m
			},
		},
	}

	// Buffer size thresholds for pool management
	smallBufferThreshold  = 128
	mediumBufferThreshold = 512
	largeBufferThreshold  = 1024

	// Metric slice size thresholds
	smallMetricThreshold  = 16
	mediumMetricThreshold = 64
	largeMetricThreshold  = 256

	// Tag map size thresholds
	smallTagThreshold  = 8
	mediumTagThreshold = 16
	largeTagThreshold  = 32
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
var EnableParallelFlush uberatomic.Bool

// MaxParallelFlushGoroutines controls the maximum number of goroutines to use for parallel flushing.
// Default value of 0 means use one goroutine per shard. Setting this to a positive number
// will use the specified number of worker goroutines for optimal resource usage.
//
// Recommended values:
// - 0: Use one goroutine per shard (not recommended for high-cardinality cases)
// - 4-8: Provides good performance balance for most workloads
// - 16+: May cause degraded performance due to contention
var MaxParallelFlushGoroutines int

// getTagSerializationBuffer returns an appropriately sized buffer for tag serialization
// based on an estimated size of the final key
func getTagSerializationBuffer(estimatedSize int) *[]byte {
	if estimatedSize <= smallBufferThreshold {
		return keySerializationPools.small.Get().(*[]byte)
	} else if estimatedSize <= mediumBufferThreshold {
		return keySerializationPools.medium.Get().(*[]byte)
	} else {
		return keySerializationPools.large.Get().(*[]byte)
	}
}

// releaseTagSerializationBuffer returns a buffer to the appropriate pool
// based on its capacity, or lets it be GC'd if it's oversized
func releaseTagSerializationBuffer(buf *[]byte) {
	// Reset length but keep capacity
	*buf = (*buf)[:0]

	capacity := cap(*buf)
	if capacity <= smallBufferThreshold {
		keySerializationPools.small.Put(buf)
	} else if capacity <= mediumBufferThreshold {
		keySerializationPools.medium.Put(buf)
	} else if capacity <= largeBufferThreshold {
		keySerializationPools.large.Put(buf)
	}
	// If capacity > largeBufferThreshold, let it be GC'd to prevent memory bloat
}

// estimateKeySize provides a rough estimate of the key size based on prefix and tag counts
// This helps choose the right buffer pool size
func estimateKeySize(prefix string, tagMaps ...map[string]string) int {
	size := len(prefix) + 8 // Base size + some overhead

	for _, tags := range tagMaps {
		for k, v := range tags {
			size += len(k) + len(v) + 3 // key=value, separators
		}
	}

	return size
}

// getStringSlice returns an appropriately sized string slice for tag key collection
func getStringSlice(estimatedKeyCount int) *[]string {
	if estimatedKeyCount <= 8 {
		return stringSlicePools.small.Get().(*[]string)
	} else if estimatedKeyCount <= 16 {
		return stringSlicePools.medium.Get().(*[]string)
	} else {
		return stringSlicePools.large.Get().(*[]string)
	}
}

// releaseStringSlice returns a string slice to the appropriate pool
func releaseStringSlice(slice *[]string) {
	// Reset length but keep capacity
	*slice = (*slice)[:0]

	capacity := cap(*slice)
	if capacity <= 8 {
		stringSlicePools.small.Put(slice)
	} else if capacity <= 16 {
		stringSlicePools.medium.Put(slice)
	} else if capacity <= 32 {
		stringSlicePools.large.Put(slice)
	}
	// If capacity > 32, let it be GC'd to prevent memory bloat
}

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
	r.performMaintenance()
	r.processScopes(func(s *scope) { safeReportScope(s, reporter) })
}

// CachedReport processes all scopes and reports their metrics via the cached reporter
func (r *scopeRegistry) CachedReport() {
	defer r.purgeIfRootClosed()
	r.reportInternalMetrics()
	r.performMaintenance()
	r.processScopes(func(s *scope) { safeCachedReportScope(s) })
}

// performMaintenance consolidates maintenance tasks
func (r *scopeRegistry) performMaintenance() {
	if r.enableEviction {
		r.evictInactiveScopes()
	}
	if r.scopePool.New != nil {
		r.cleanupScopePool()
	}
}

// processScopes handles scope processing with the appropriate concurrency strategy
func (r *scopeRegistry) processScopes(processFn func(*scope)) {
	if EnableParallelFlush.Load() {
		if MaxParallelFlushGoroutines > 0 {
			r.workerPoolProcess(processFn, MaxParallelFlushGoroutines)
		} else {
			r.parallelProcess(processFn)
		}
	} else {
		r.sequentialProcess(processFn)
	}
}

// sequentialProcess processes all scopes sequentially
func (r *scopeRegistry) sequentialProcess(processFn func(*scope)) {
	for _, subscopeBucket := range r.subscopes {
		r.processBucket(subscopeBucket, processFn)
	}
}

// parallelProcess processes scopes in parallel with one goroutine per shard
func (r *scopeRegistry) parallelProcess(processFn func(*scope)) {
	var wg sync.WaitGroup
	closedScopes := make(chan scopeClosureInfo, 100)

	for _, subscopeBucket := range r.subscopes {
		wg.Add(1)
		go func(bucket *scopeBucket) {
			defer wg.Done()
			r.processBucketWithClosures(bucket, processFn, closedScopes)
		}(subscopeBucket)
	}

	go func() {
		wg.Wait()
		close(closedScopes)
	}()

	r.handleClosedScopes(closedScopes)
}

// workerPoolProcess processes scopes using a fixed-size worker pool
func (r *scopeRegistry) workerPoolProcess(processFn func(*scope), numWorkers int) {
	var wg sync.WaitGroup

	// Collect all work items synchronously to avoid races
	var allWorkItems []workItem
	for _, subscopeBucket := range r.subscopes {
		scopesToProcess := r.copyBucketScopes(subscopeBucket)
		for i, s := range scopesToProcess.scopes {
			allWorkItems = append(allWorkItems, workItem{
				scope:    s,
				scopeKey: scopesToProcess.keys[i],
				bucket:   subscopeBucket,
			})
		}
	}

	// Create channels with exact capacity
	workChan := make(chan workItem, len(allWorkItems))
	closedScopesChan := make(chan workItem, len(allWorkItems))

	// Fill work queue synchronously
	for _, item := range allWorkItems {
		workChan <- item
	}
	close(workChan)

	// Start worker goroutines
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			for item := range workChan {
				processFn(item.scope)
				if atomic.LoadInt32(&item.scope.closed) != 0 {
					closedScopesChan <- item
				}
			}
		}()
	}

	// Wait for workers and close the closed scopes channel
	go func() {
		wg.Wait()
		close(closedScopesChan)
	}()

	// Handle closed scopes
	for item := range closedScopesChan {
		item.bucket.mu.Lock()
		delete(item.bucket.s, item.scopeKey)
		item.bucket.mu.Unlock()
		item.scope.clearMetrics()
	}
}

// Helper methods for scope processing
func (r *scopeRegistry) processBucket(bucket *scopeBucket, processFn func(*scope)) {
	scopesToProcess := r.copyBucketScopes(bucket)

	for i, s := range scopesToProcess.scopes {
		processFn(s)
		if atomic.LoadInt32(&s.closed) != 0 {
			bucket.mu.Lock()
			delete(bucket.s, scopesToProcess.keys[i])
			bucket.mu.Unlock()
			s.clearMetrics()
		}
	}
}

func (r *scopeRegistry) processBucketWithClosures(bucket *scopeBucket, processFn func(*scope), closedChan chan<- scopeClosureInfo) {
	scopesToProcess := r.copyBucketScopes(bucket)

	for i, s := range scopesToProcess.scopes {
		processFn(s)
		if atomic.LoadInt32(&s.closed) != 0 {
			closedChan <- scopeClosureInfo{bucket: bucket, name: scopesToProcess.keys[i], scope: s}
		}
	}
}

type scopesToProcess struct {
	scopes []*scope
	keys   []string
}

func (r *scopeRegistry) copyBucketScopes(bucket *scopeBucket) scopesToProcess {
	bucket.mu.RLock()
	defer bucket.mu.RUnlock()

	result := scopesToProcess{
		scopes: make([]*scope, 0, len(bucket.s)),
		keys:   make([]string, 0, len(bucket.s)),
	}

	for name, s := range bucket.s {
		result.scopes = append(result.scopes, s)
		result.keys = append(result.keys, name)
	}

	return result
}

func (r *scopeRegistry) estimateTotalScopes() int {
	total := 0
	for _, bucket := range r.subscopes {
		bucket.mu.RLock()
		total += len(bucket.s)
		bucket.mu.RUnlock()
	}
	return total
}

func (r *scopeRegistry) handleClosedScopes(closedScopes <-chan scopeClosureInfo) {
	for info := range closedScopes {
		info.bucket.mu.Lock()
		delete(info.bucket.s, info.name)
		info.bucket.mu.Unlock()
		info.scope.clearMetrics()
	}
}

type workItem struct {
	scope    *scope
	scopeKey string
	bucket   *scopeBucket
}

// safeReportScope wraps the scope.report call with panic recovery
func safeReportScope(s *scope, reporter StatsReporter) {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("tally: panic during scope report for scope %s: %v\n", s.fullyQualifiedName("."), err)
		}
	}()
	s.report(reporter)
}

// safeCachedReportScope wraps the scope.cachedReport call with panic recovery
func safeCachedReportScope(s *scope) {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("tally: panic during scope cachedReport for scope %s: %v\n", s.fullyQualifiedName("."), err)
		}
	}()
	s.cachedReport()
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
		allTags := mergeRightTagsPooled(parent.tags, tags)

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
			countersSlice:      *getCounterSlice(_defaultInitialSliceSize),
			countersSliceMux:   sync.Mutex{},
			gaugesSlice:        *getGaugeSlice(_defaultInitialSliceSize),
			gaugesSliceMux:     sync.Mutex{},
			histogramsSlice:    *getHistogramSlice(_defaultInitialSliceSize),
			histogramsSliceMux: sync.Mutex{},
			done:               make(chan struct{}),
			testScope:          parent.testScope,
			noCacheSubscopes:   parent.noCacheSubscopes,
		}
	}

	var (
		estimatedSize = estimateKeySize(prefix, parent.tags, tags)
		buf           *[]byte
		h             maphash.Hash
	)

	// Get appropriately sized buffer from pool
	buf = getTagSerializationBuffer(estimatedSize)
	defer releaseTagSerializationBuffer(buf)

	// Generate the key using the optimized pooled buffer and string slice method
	keyBytes := keyForPrefixedStringMapsAsKeyWithPooledSlice(*buf, prefix, parent.tags, tags)

	h.SetSeed(r.seed)
	_, _ = h.Write(keyBytes)
	subscopeBucket := r.subscopes[h.Sum64()%uint64(len(r.subscopes))]

	subscopeBucket.mu.RLock()
	// keyBytes is the actual byte slice and casting it to a string for lookup from the cache
	// as the memory layout of []byte is a superset of string the below casting is safe and does not do any alloc
	// However it cannot be used outside of the stack; a heap allocation is needed if that string needs to be stored
	// in the map as a key
	var (
		unsanitizedKey = *(*string)(unsafe.Pointer(&keyBytes))
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
		keyCandidate := string(keyBytes)
		sanitizedKey = r.internString(keyCandidate)
	}

	// A sanitized key is a new heap allocation, so it's safe to put
	// it into a map.
	s = &scope{
		separator:      parent.separator,
		prefix:         prefix,
		tags:           mergeRightTagsPooled(parent.tags, tags),
		reporter:       parent.reporter,
		cachedReporter: parent.cachedReporter,
		baseReporter:   parent.baseReporter,
		defaultBuckets: parent.defaultBuckets,
		sanitizer:      parent.sanitizer,
		registry:       r,
		bucketCache:    parent.bucketCache,

		// sync.Map is zero-value ready and doesn't need initialization
		countersSlice:      *getCounterSlice(_defaultInitialSliceSize),
		countersSliceMux:   sync.Mutex{},
		gaugesSlice:        *getGaugeSlice(_defaultInitialSliceSize),
		gaugesSliceMux:     sync.Mutex{},
		histogramsSlice:    *getHistogramSlice(_defaultInitialSliceSize),
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

	var counters, gauges, histograms int64
	var rootCounters, rootGauges, rootHistograms int64
	scopes := 1 // Account for root scope.
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
				rootCounters = counterSliceLen
				rootGauges = gaugeSliceLen
				rootHistograms = histogramSliceLen
				return
			}
			counters += counterSliceLen
			gauges += gaugeSliceLen
			histograms += histogramSliceLen
			scopes++
		},
	)

	counters += rootCounters
	gauges += rootGauges
	histograms += rootHistograms
	if r.root.reporter != nil {
		r.root.reporter.ReportGauge(r.sanitizedCounterCardinalityName, r.cardinalityMetricsTags, float64(counters))
		r.root.reporter.ReportGauge(r.sanitizedGaugeCardinalityName, r.cardinalityMetricsTags, float64(gauges))
		r.root.reporter.ReportGauge(r.sanitizedHistogramCardinalityName, r.cardinalityMetricsTags, float64(histograms))
		r.root.reporter.ReportGauge(r.sanitizedScopeCardinalityName, r.cardinalityMetricsTags, float64(scopes))
	}

	if r.root.cachedReporter != nil {
		r.cachedCounterCardinalityGauge.ReportGauge(float64(counters))
		r.cachedGaugeCardinalityGauge.ReportGauge(float64(gauges))
		r.cachedHistogramCardinalityGauge.ReportGauge(float64(histograms))
		r.cachedScopeCardinalityGauge.ReportGauge(float64(scopes))
	}
}

// preAllocateEmptyMaps creates empty maps with preallocated capacity
// to avoid frequent reallocations when adding metrics to scopes
// Phase 4: Now uses pooled slices for better memory efficiency
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
	gauges = make(map[string]*gauge, _defaultInitialSliceSize)
	histograms = make(map[string]*histogram, _defaultInitialSliceSize)
	timers = make(map[string]*timer, _defaultInitialSliceSize)

	// Use pooled slices instead of allocating new ones
	countersSlicePtr := getCounterSlice(_defaultInitialSliceSize)
	countersSlice = *countersSlicePtr

	gaugesSlicePtr := getGaugeSlice(_defaultInitialSliceSize)
	gaugesSlice = *gaugesSlicePtr

	histogramsSlicePtr := getHistogramSlice(_defaultInitialSliceSize)
	histogramsSlice = *histogramsSlicePtr

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

// Phase 4: Metric slice pool management functions
// getCounterSlice returns an appropriately sized counter slice from pools
func getCounterSlice(estimatedSize int) *[]*counter {
	if estimatedSize <= smallMetricThreshold {
		return metricSlicePools.counterSmall.Get().(*[]*counter)
	} else if estimatedSize <= mediumMetricThreshold {
		return metricSlicePools.counterMedium.Get().(*[]*counter)
	} else {
		return metricSlicePools.counterLarge.Get().(*[]*counter)
	}
}

// releaseCounterSlice returns a counter slice to the appropriate pool
func releaseCounterSlice(slice *[]*counter) {
	// Reset length but keep capacity
	*slice = (*slice)[:0]

	capacity := cap(*slice)
	if capacity <= smallMetricThreshold {
		metricSlicePools.counterSmall.Put(slice)
	} else if capacity <= mediumMetricThreshold {
		metricSlicePools.counterMedium.Put(slice)
	} else if capacity <= largeMetricThreshold {
		metricSlicePools.counterLarge.Put(slice)
	}
	// If capacity > largeMetricThreshold, let it be GC'd
}

// getGaugeSlice returns an appropriately sized gauge slice from pools
func getGaugeSlice(estimatedSize int) *[]*gauge {
	if estimatedSize <= smallMetricThreshold {
		return metricSlicePools.gaugeSmall.Get().(*[]*gauge)
	} else if estimatedSize <= mediumMetricThreshold {
		return metricSlicePools.gaugeMedium.Get().(*[]*gauge)
	} else {
		return metricSlicePools.gaugeLarge.Get().(*[]*gauge)
	}
}

// releaseGaugeSlice returns a gauge slice to the appropriate pool
func releaseGaugeSlice(slice *[]*gauge) {
	// Reset length but keep capacity
	*slice = (*slice)[:0]

	capacity := cap(*slice)
	if capacity <= smallMetricThreshold {
		metricSlicePools.gaugeSmall.Put(slice)
	} else if capacity <= mediumMetricThreshold {
		metricSlicePools.gaugeMedium.Put(slice)
	} else if capacity <= largeMetricThreshold {
		metricSlicePools.gaugeLarge.Put(slice)
	}
	// If capacity > largeMetricThreshold, let it be GC'd
}

// getHistogramSlice returns an appropriately sized histogram slice from pools
func getHistogramSlice(estimatedSize int) *[]*histogram {
	if estimatedSize <= smallMetricThreshold {
		return metricSlicePools.histogramSmall.Get().(*[]*histogram)
	} else if estimatedSize <= mediumMetricThreshold {
		return metricSlicePools.histogramMedium.Get().(*[]*histogram)
	} else {
		return metricSlicePools.histogramLarge.Get().(*[]*histogram)
	}
}

// releaseHistogramSlice returns a histogram slice to the appropriate pool
func releaseHistogramSlice(slice *[]*histogram) {
	// Reset length but keep capacity
	*slice = (*slice)[:0]

	capacity := cap(*slice)
	if capacity <= smallMetricThreshold {
		metricSlicePools.histogramSmall.Put(slice)
	} else if capacity <= mediumMetricThreshold {
		metricSlicePools.histogramMedium.Put(slice)
	} else if capacity <= largeMetricThreshold {
		metricSlicePools.histogramLarge.Put(slice)
	}
	// If capacity > largeMetricThreshold, let it be GC'd
}

// getTagMap returns an appropriately sized tag map from pools
func getTagMap(estimatedSize int) *map[string]string {
	if estimatedSize <= smallTagThreshold {
		return tagMapPools.small.Get().(*map[string]string)
	} else if estimatedSize <= mediumTagThreshold {
		return tagMapPools.medium.Get().(*map[string]string)
	} else {
		return tagMapPools.large.Get().(*map[string]string)
	}
}

// releaseTagMap returns a tag map to the appropriate pool after clearing it
func releaseTagMap(m *map[string]string) {
	// Clear the map but keep capacity
	for k := range *m {
		delete(*m, k)
	}

	// Return to appropriate pool based on capacity
	// Note: Go maps don't expose capacity directly, so we estimate based on length it had
	if len(*m) <= smallTagThreshold {
		tagMapPools.small.Put(m)
	} else if len(*m) <= mediumTagThreshold {
		tagMapPools.medium.Put(m)
	} else if len(*m) <= largeTagThreshold {
		tagMapPools.large.Put(m)
	}
	// If too large, let it be GC'd
}

// Snapshot map pool management functions
func getCounterSnapshotMap() *map[string]CounterSnapshot {
	return snapshotMapPools.counterMaps.Get().(*map[string]CounterSnapshot)
}

func releaseCounterSnapshotMap(m *map[string]CounterSnapshot) {
	// Clear the map
	for k := range *m {
		delete(*m, k)
	}
	snapshotMapPools.counterMaps.Put(m)
}

func getGaugeSnapshotMap() *map[string]GaugeSnapshot {
	return snapshotMapPools.gaugeMaps.Get().(*map[string]GaugeSnapshot)
}

func releaseGaugeSnapshotMap(m *map[string]GaugeSnapshot) {
	// Clear the map
	for k := range *m {
		delete(*m, k)
	}
	snapshotMapPools.gaugeMaps.Put(m)
}

func getTimerSnapshotMap() *map[string]TimerSnapshot {
	return snapshotMapPools.timerMaps.Get().(*map[string]TimerSnapshot)
}

func releaseTimerSnapshotMap(m *map[string]TimerSnapshot) {
	// Clear the map
	for k := range *m {
		delete(*m, k)
	}
	snapshotMapPools.timerMaps.Put(m)
}

func getHistogramSnapshotMap() *map[string]HistogramSnapshot {
	return snapshotMapPools.histogramMaps.Get().(*map[string]HistogramSnapshot)
}

func releaseHistogramSnapshotMap(m *map[string]HistogramSnapshot) {
	// Clear the map
	for k := range *m {
		delete(*m, k)
	}
	snapshotMapPools.histogramMaps.Put(m)
}
