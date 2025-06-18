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
	"hash/maphash"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultInternShards is the default number of shards for the string interning system
	DefaultInternShards = 16
	// InternCleanupInterval is how often we run cleanup operations on the intern pools
	InternCleanupInterval = 5 * time.Minute
	// MaxInternedStringLength limits the maximum length of strings we'll intern to prevent memory bloat
	MaxInternedStringLength = 1024
	// InternPoolSizeLimit is the maximum number of strings per shard before we start evicting
	InternPoolSizeLimit = 10000
)

// StringInterningStats tracks statistics for the string interning system
type StringInterningStats struct {
	Hits         int64 // Number of cache hits
	Misses       int64 // Number of cache misses
	Evictions    int64 // Number of evicted entries
	TotalEntries int64 // Current total entries across all shards
}

// stringInternShard represents a single shard of the string interning system
type stringInternShard struct {
	m        sync.Map
	size     int64    // Approximate size, updated atomically
	lastUsed sync.Map // tracks last used time for eviction
}

// stringInterningSystem manages interned strings across multiple shards for concurrency
type stringInterningSystem struct {
	shards     []*stringInternShard
	shardCount uint64
	seed       maphash.Seed
	stats      StringInterningStats

	// Cleanup management
	cleanupOnce sync.Once
	stopCleanup chan struct{}
	shutdownWg  sync.WaitGroup
}

// Global string interning system
var globalStringInterning = newStringInterningSystem(DefaultInternShards)

// newStringInterningSystem creates a new string interning system with the specified number of shards
func newStringInterningSystem(shardCount int) *stringInterningSystem {
	return newStringInterningSystemWithCleanup(shardCount, true)
}

// newStringInterningSystemWithCleanup creates a new string interning system with optional cleanup
func newStringInterningSystemWithCleanup(shardCount int, startCleanup bool) *stringInterningSystem {
	if shardCount <= 0 {
		shardCount = runtime.GOMAXPROCS(-1)
		if shardCount <= 0 {
			shardCount = DefaultInternShards
		}
	}

	system := &stringInterningSystem{
		shards:      make([]*stringInternShard, shardCount),
		shardCount:  uint64(shardCount),
		seed:        maphash.MakeSeed(),
		stopCleanup: make(chan struct{}),
	}

	for i := 0; i < shardCount; i++ {
		system.shards[i] = &stringInternShard{}
	}

	// Start background cleanup goroutine if requested
	if startCleanup {
		system.startCleanup()
	}

	return system
}

// startCleanup starts the background cleanup goroutine
func (s *stringInterningSystem) startCleanup() {
	s.cleanupOnce.Do(func() {
		s.shutdownWg.Add(1)
		go s.cleanupLoop()
	})
}

// cleanupLoop runs periodic cleanup operations
func (s *stringInterningSystem) cleanupLoop() {
	defer s.shutdownWg.Done()
	ticker := time.NewTicker(InternCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.performCleanup()
		case <-s.stopCleanup:
			return
		}
	}
}

// performCleanup performs maintenance operations on all shards
func (s *stringInterningSystem) performCleanup() {
	now := time.Now()
	cutoff := now.Add(-2 * InternCleanupInterval) // Evict entries older than 10 minutes

	var totalEntries int64
	var evictions int64

	for _, shard := range s.shards {
		shardSize := atomic.LoadInt64(&shard.size)
		if shardSize <= InternPoolSizeLimit {
			totalEntries += shardSize
			continue
		}

		// Shard is oversized, perform eviction
		var keysToEvict []string
		var count int

		// Collect keys to evict based on age and size limit
		shard.lastUsed.Range(func(key, value interface{}) bool {
			if count >= int(shardSize-InternPoolSizeLimit/2) {
				return false // Stop collecting, we have enough to evict
			}

			keyStr := key.(string)
			lastUsed := value.(time.Time)

			if lastUsed.Before(cutoff) {
				keysToEvict = append(keysToEvict, keyStr)
				count++
			}
			return true
		})

		// Evict collected keys
		for _, key := range keysToEvict {
			shard.m.Delete(key)
			shard.lastUsed.Delete(key)
			atomic.AddInt64(&shard.size, -1)
			evictions++
		}

		totalEntries += atomic.LoadInt64(&shard.size)
	}

	atomic.StoreInt64(&s.stats.Evictions, atomic.LoadInt64(&s.stats.Evictions)+evictions)
	atomic.StoreInt64(&s.stats.TotalEntries, totalEntries)
}

// getShard returns the appropriate shard for the given string
func (s *stringInterningSystem) getShard(str string) *stringInternShard {
	var h maphash.Hash
	h.SetSeed(s.seed)
	h.WriteString(str)
	return s.shards[h.Sum64()%s.shardCount]
}

// internString interns a string and returns the canonical version
func (s *stringInterningSystem) internString(str string) string {
	// Don't intern extremely long strings to prevent memory bloat
	if len(str) > MaxInternedStringLength {
		return str
	}

	shard := s.getShard(str)

	// Try to load existing interned string
	if value, ok := shard.m.Load(str); ok {
		atomic.AddInt64(&s.stats.Hits, 1)
		// Update last used time using the stored string as key for consistency
		if storedStr, ok := value.(string); ok {
			shard.lastUsed.Store(storedStr, time.Now())
		}
		return value.(string)
	}

	// String not found, intern it
	atomic.AddInt64(&s.stats.Misses, 1)

	// Create a copy of the string to ensure it's not referencing a larger backing array
	interned := string([]byte(str))

	// Use the original string as key but store the interned copy as value
	// This ensures consistent key usage between Load and LoadOrStore
	actual, loaded := shard.m.LoadOrStore(str, interned)
	if !loaded {
		// We were first to store it
		atomic.AddInt64(&shard.size, 1)
		shard.lastUsed.Store(interned, time.Now())
		return interned
	}

	// Someone else beat us to it, return the actual stored value
	return actual.(string)
}

// InternString interns a string using the global interning system
func InternString(str string) string {
	return globalStringInterning.internString(str)
}

// GetStringInterningStats returns current statistics for the string interning system
func GetStringInterningStats() StringInterningStats {
	stats := StringInterningStats{
		Hits:      atomic.LoadInt64(&globalStringInterning.stats.Hits),
		Misses:    atomic.LoadInt64(&globalStringInterning.stats.Misses),
		Evictions: atomic.LoadInt64(&globalStringInterning.stats.Evictions),
	}

	// Calculate total entries across all shards
	var totalEntries int64
	for _, shard := range globalStringInterning.shards {
		totalEntries += atomic.LoadInt64(&shard.size)
	}
	stats.TotalEntries = totalEntries

	return stats
}

// GetStringInterningStats returns current statistics for this specific string interning system
func (s *stringInterningSystem) GetStringInterningStats() StringInterningStats {
	stats := StringInterningStats{
		Hits:      atomic.LoadInt64(&s.stats.Hits),
		Misses:    atomic.LoadInt64(&s.stats.Misses),
		Evictions: atomic.LoadInt64(&s.stats.Evictions),
	}

	// Calculate total entries across all shards
	var totalEntries int64
	for _, shard := range s.shards {
		totalEntries += atomic.LoadInt64(&shard.size)
	}
	stats.TotalEntries = totalEntries

	return stats
}

// ResetStringInterningStats resets the statistics counters
func ResetStringInterningStats() {
	atomic.StoreInt64(&globalStringInterning.stats.Hits, 0)
	atomic.StoreInt64(&globalStringInterning.stats.Misses, 0)
	atomic.StoreInt64(&globalStringInterning.stats.Evictions, 0)
}

// ShutdownGlobalStringInterning shuts down the global string interning system (for tests)
func ShutdownGlobalStringInterning() {
	globalStringInterning.Shutdown()
}

// BuildInternedMetricName constructs and interns a fully qualified metric name
func BuildInternedMetricName(prefix, separator, name string) string {
	if len(prefix) == 0 {
		return InternString(name)
	}

	// Fast path for common case - estimate total length and use pre-sized builder
	totalLen := len(prefix) + len(separator) + len(name)
	if totalLen > MaxInternedStringLength {
		// Don't intern very long names
		return prefix + separator + name
	}

	// Use a bytes buffer approach for efficient concatenation
	buf := make([]byte, 0, totalLen)
	buf = append(buf, prefix...)
	buf = append(buf, separator...)
	buf = append(buf, name...)

	// Convert to string properly and intern
	return InternString(string(buf))
}

// BuildInternedTagKey constructs and interns a tag key
func BuildInternedTagKey(key string) string {
	return InternString(key)
}

// BuildInternedTagValue constructs and interns a tag value
func BuildInternedTagValue(value string) string {
	return InternString(value)
}

// BuildInternedMetricID constructs and interns a metric ID from name and tags
func BuildInternedMetricID(metricName string, tags map[string]string) string {
	if len(tags) == 0 {
		return InternString(metricName)
	}

	// Estimate total size for efficient buffer allocation
	totalSize := len(metricName) + 1 // name + separator
	for k, v := range tags {
		totalSize += len(k) + len(v) + 2 // key=value,
	}

	if totalSize > MaxInternedStringLength {
		// Don't intern very long IDs, just build them normally
		return buildMetricIDNonInterned(metricName, tags)
	}

	// Use efficient building with pre-sized buffer
	buf := make([]byte, 0, totalSize)
	buf = append(buf, metricName...)
	buf = append(buf, '|')

	// Sort tags for deterministic ordering
	keys := getStringSlice(len(tags))
	defer releaseStringSlice(keys)
	*keys = (*keys)[:0]

	for k := range tags {
		*keys = append(*keys, k)
	}

	// Sort for deterministic ordering (simple sort for small slices)
	if len(*keys) <= 8 {
		internInsertionSort(*keys)
	} else {
		internQuickSort(*keys, 0, len(*keys)-1)
	}

	// Build the ID string
	for i, k := range *keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, k...)
		buf = append(buf, '=')
		buf = append(buf, tags[k]...)
	}

	// Convert to string properly and intern
	return InternString(string(buf))
}

// buildMetricIDNonInterned builds a metric ID without interning for very long strings
func buildMetricIDNonInterned(metricName string, tags map[string]string) string {
	// Get a string slice for sorting keys
	keys := getStringSlice(len(tags))
	defer releaseStringSlice(keys)
	*keys = (*keys)[:0]

	for k := range tags {
		*keys = append(*keys, k)
	}

	// Sort for deterministic ordering
	if len(*keys) <= 8 {
		internInsertionSort(*keys)
	} else {
		internQuickSort(*keys, 0, len(*keys)-1)
	}

	// Build the ID string using a simple approach
	result := metricName + "|"
	for i, k := range *keys {
		if i > 0 {
			result += ","
		}
		result += k + "=" + tags[k]
	}

	return result
}

// Simple sorting algorithms for small slices (more efficient than sort.Strings for tiny slices)
func internInsertionSort(slice []string) {
	for i := 1; i < len(slice); i++ {
		key := slice[i]
		j := i - 1
		for j >= 0 && slice[j] > key {
			slice[j+1] = slice[j]
			j--
		}
		slice[j+1] = key
	}
}

func internQuickSort(slice []string, low, high int) {
	if low < high {
		pi := internPartition(slice, low, high)
		internQuickSort(slice, low, pi-1)
		internQuickSort(slice, pi+1, high)
	}
}

func internPartition(slice []string, low, high int) int {
	pivot := slice[high]
	i := low - 1

	for j := low; j < high; j++ {
		if slice[j] <= pivot {
			i++
			slice[i], slice[j] = slice[j], slice[i]
		}
	}
	slice[i+1], slice[high] = slice[high], slice[i+1]
	return i + 1
}

// Shutdown stops the cleanup goroutine and waits for it to complete
func (s *stringInterningSystem) Shutdown() {
	close(s.stopCleanup)
	s.shutdownWg.Wait()
}
