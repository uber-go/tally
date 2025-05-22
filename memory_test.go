// Copyright (c) 2025 Uber Technologies, Inc.
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
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestScopeRegistryMemoryLeak tests that scope registry doesn't leak memory
func TestScopeRegistryMemoryLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory leak test in short mode")
	}

	// Force GC and get baseline
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)

	// Create many scopes and let them go out of scope
	for iteration := 0; iteration < 10; iteration++ {
		var scopes []Scope
		rootScope := NewTestScope(fmt.Sprintf("root_%d", iteration), nil)

		// Create many subscopes
		for i := 0; i < 1000; i++ {
			scope := rootScope.Tagged(map[string]string{
				"iteration": fmt.Sprintf("%d", iteration),
				"id":        fmt.Sprintf("%d", i),
				"service":   "test-service",
			})
			scopes = append(scopes, scope)
		}

		// Use the scopes briefly
		for _, scope := range scopes {
			scope.Counter("test").Inc(1)
		}

		// Clear references
		scopes = nil
		rootScope = nil

		// Force GC
		runtime.GC()
	}

	// Final GC and memory check
	runtime.GC()
	var final runtime.MemStats
	runtime.ReadMemStats(&final)

	var memGrowth uint64
	if final.Alloc > baseline.Alloc {
		memGrowth = final.Alloc - baseline.Alloc
	} else {
		memGrowth = 0 // Memory actually decreased
	}

	// Memory growth should be minimal after GC
	maxGrowth := uint64(5 * 1024 * 1024) // 5MB tolerance
	assert.Less(t, memGrowth, maxGrowth,
		"Memory growth after scope cleanup should be minimal: %d bytes", memGrowth)

	t.Logf("Memory growth after scope lifecycle: %d bytes", memGrowth)
}

// TestMetricCacheMemoryBounds tests memory bounds of metric caching
func TestMetricCacheMemoryBounds(t *testing.T) {
	scope := NewTestScope("cache_test", nil)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	// Create many metrics with predictable memory usage
	const numMetrics = 10000
	metrics := make(map[string]interface{}, numMetrics)

	for i := 0; i < numMetrics; i++ {
		name := fmt.Sprintf("metric_%d", i)

		switch i % 4 {
		case 0:
			metrics[name] = scope.Counter(name)
		case 1:
			metrics[name] = scope.Gauge(name)
		case 2:
			metrics[name] = scope.Timer(name)
		case 3:
			metrics[name] = scope.Histogram(name, DefaultBuckets)
		}
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	var memGrowth uint64
	if after.Alloc > before.Alloc {
		memGrowth = after.Alloc - before.Alloc
	} else {
		memGrowth = 0
	}

	var avgBytesPerMetric uint64
	if memGrowth > 0 && numMetrics > 0 {
		avgBytesPerMetric = memGrowth / numMetrics
	} else {
		avgBytesPerMetric = 0
	}

	// Each metric should use reasonable memory (less than 1KB on average)
	maxBytesPerMetric := uint64(1024)
	assert.Less(t, avgBytesPerMetric, maxBytesPerMetric,
		"Average memory per metric should be reasonable: %d bytes/metric", avgBytesPerMetric)

	t.Logf("Memory usage: %d bytes for %d metrics (%.1f bytes/metric)",
		memGrowth, numMetrics, float64(avgBytesPerMetric))

	// Verify metrics are cached (second access shouldn't increase memory significantly)
	runtime.GC()
	var beforeReaccess runtime.MemStats
	runtime.ReadMemStats(&beforeReaccess)

	// Access same metrics again
	for i := 0; i < numMetrics; i++ {
		name := fmt.Sprintf("metric_%d", i)
		switch i % 4 {
		case 0:
			scope.Counter(name).Inc(1)
		case 1:
			scope.Gauge(name).Update(float64(i))
		case 2:
			scope.Timer(name).Record(time.Microsecond)
		case 3:
			scope.Histogram(name, DefaultBuckets).RecordValue(float64(i))
		}
	}

	runtime.GC()
	var afterReaccess runtime.MemStats
	runtime.ReadMemStats(&afterReaccess)

	var reaccessGrowth uint64
	if afterReaccess.Alloc > beforeReaccess.Alloc {
		reaccessGrowth = afterReaccess.Alloc - beforeReaccess.Alloc
	} else {
		reaccessGrowth = 0 // Memory usage decreased (GC occurred)
	}

	// Reaccess should use minimal additional memory (metrics should be cached)
	maxReaccessGrowth := memGrowth / 10 // Allow 10% of original growth
	assert.Less(t, reaccessGrowth, maxReaccessGrowth,
		"Memory growth on metric reaccess should be minimal: %d bytes", reaccessGrowth)
}

// TestLongRunningMemoryStability tests memory stability over extended operations
func TestLongRunningMemoryStability(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping long-running memory test in short mode")
	}

	scope := NewTestScope("stability", nil)
	counter := scope.Counter("operations")
	gauge := scope.Gauge("memory_gauge")
	timer := scope.Timer("operation_time")

	samples := make([]uint64, 0, 20)

	// Sample memory usage over time with continuous operations
	for i := 0; i < 20; i++ {
		// Perform operations
		for j := 0; j < 5000; j++ {
			counter.Inc(1)
			gauge.Update(float64(j))
			timer.Record(time.Microsecond)

			// Create some temporary scopes
			if j%100 == 0 {
				tempScope := scope.Tagged(map[string]string{
					"temp": fmt.Sprintf("%d_%d", i, j),
				})
				tempScope.Counter("temp_counter").Inc(1)
			}
		}

		// Force GC and sample memory
		runtime.GC()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		samples = append(samples, mem.Alloc)

		time.Sleep(10 * time.Millisecond) // Brief pause
	}

	// Analyze memory trend
	if len(samples) < 10 {
		t.Skip("Not enough memory samples")
	}

	// Calculate trend over latter half (after warmup)
	start := len(samples) / 2
	firstHalf := samples[start]
	lastSample := samples[len(samples)-1]

	// Memory should be stable (not growing unboundedly)
	growth := float64(lastSample) / float64(firstHalf)
	maxGrowthRatio := 2.0 // Allow 100% growth max in test environment

	assert.Less(t, growth, maxGrowthRatio,
		"Memory should remain stable over time. Growth ratio: %.2f", growth)

	t.Logf("Memory stability test: %.2f growth ratio over %d samples", growth, len(samples))
	t.Logf("Memory samples (KB): %v", convertToKB(samples))
}

// TestScopeHierarchyMemoryEfficiency tests memory efficiency of scope hierarchies
func TestScopeHierarchyMemoryEfficiency(t *testing.T) {
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)

	rootScope := NewTestScope("hierarchy", map[string]string{"service": "test"})

	// Create deep hierarchy
	const depth = 10
	const width = 100

	var leafScopes []Scope

	for w := 0; w < width; w++ {
		currentScope := Scope(rootScope)

		for d := 0; d < depth; d++ {
			currentScope = currentScope.SubScope(fmt.Sprintf("level_%d_%d", d, w))
		}

		leafScopes = append(leafScopes, currentScope)
	}

	// Use leaf scopes
	for i, scope := range leafScopes {
		scope.Counter("leaf_counter").Inc(int64(i))
		scope.Gauge("leaf_gauge").Update(float64(i))
	}

	runtime.GC()
	var final runtime.MemStats
	runtime.ReadMemStats(&final)

	var memGrowth uint64
	if final.Alloc > baseline.Alloc {
		memGrowth = final.Alloc - baseline.Alloc
	} else {
		memGrowth = 0
	}

	var avgBytesPerLeaf uint64
	if memGrowth > 0 && len(leafScopes) > 0 {
		avgBytesPerLeaf = memGrowth / uint64(len(leafScopes))
	} else {
		avgBytesPerLeaf = 0
	}

	// Each leaf scope (including its hierarchy) should be memory efficient
	maxBytesPerLeaf := uint64(5 * 1024) // 5KB per leaf scope max
	assert.Less(t, avgBytesPerLeaf, maxBytesPerLeaf,
		"Memory per scope hierarchy should be efficient: %d bytes/leaf", avgBytesPerLeaf)

	t.Logf("Hierarchy memory usage: %d bytes for %d leaf scopes (%.1f bytes/leaf)",
		memGrowth, len(leafScopes), float64(avgBytesPerLeaf))
}

// TestTagMapMemoryOptimization tests memory optimization for tag maps
func TestTagMapMemoryOptimization(t *testing.T) {
	scope := NewTestScope("tags", nil)

	// Test different tag patterns
	testCases := []struct {
		name     string
		tagCount int
		repeat   int
	}{
		{"NoTags", 0, 1000},
		{"SingleTag", 1, 1000},
		{"FewTags", 3, 1000},
		{"ManyTags", 10, 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			var scopes []Scope
			for i := 0; i < tc.repeat; i++ {
				tags := make(map[string]string)
				for j := 0; j < tc.tagCount; j++ {
					tags[fmt.Sprintf("key_%d", j)] = fmt.Sprintf("value_%d_%d", j, i)
				}

				taggedScope := scope.Tagged(tags)
				taggedScope.Counter("test").Inc(1)
				scopes = append(scopes, taggedScope)
			}

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			var memGrowth uint64
			if after.Alloc > before.Alloc {
				memGrowth = after.Alloc - before.Alloc
			} else {
				memGrowth = 0
			}

			var avgBytesPerScope uint64
			if memGrowth > 0 && tc.repeat > 0 {
				avgBytesPerScope = memGrowth / uint64(tc.repeat)
			} else {
				avgBytesPerScope = 0
			}

			t.Logf("%s: %d bytes total, %.1f bytes/scope",
				tc.name, memGrowth, float64(avgBytesPerScope))

			// Memory should scale reasonably with tag count
			if tc.tagCount == 0 {
				// No tags should use minimal memory
				maxBytes := uint64(200) // 200 bytes per scope max for no tags
				assert.Less(t, avgBytesPerScope, maxBytes,
					"No-tag scopes should use minimal memory")
			}
		})
	}
}

// Helper function to convert memory samples to KB for logging
func convertToKB(samples []uint64) []uint64 {
	kb := make([]uint64, len(samples))
	for i, sample := range samples {
		kb[i] = sample / 1024
	}
	return kb
}

// BenchmarkMemoryAllocations benchmarks memory allocation patterns
func BenchmarkMemoryAllocations(b *testing.B) {
	scope := NewTestScope("bench", nil)

	b.Run("ScopeCreation", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = scope.Tagged(map[string]string{
				"iteration": fmt.Sprintf("%d", i),
			})
		}
	})

	b.Run("MetricCreation", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			scope.Counter(fmt.Sprintf("counter_%d", i%1000)).Inc(1)
		}
	})

	b.Run("MetricAccess", func(b *testing.B) {
		// Pre-create metrics
		for i := 0; i < 1000; i++ {
			scope.Counter(fmt.Sprintf("counter_%d", i))
		}

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			scope.Counter(fmt.Sprintf("counter_%d", i%1000)).Inc(1)
		}
	})
}
