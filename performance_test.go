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
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestConcurrentMetricCreation verifies that concurrent metric creation
// doesn't cause race conditions or panics
func TestConcurrentMetricCreation(t *testing.T) {
	scope := NewTestScope("test", nil)

	const numGoroutines = 100
	const metricsPerGoroutine = 1000

	var wg sync.WaitGroup
	var panics int32

	// Panic recovery function
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Panic during concurrent metric creation: %v", r)
		}
	}()

	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt32(&panics, 1)
				}
			}()

			for j := 0; j < metricsPerGoroutine; j++ {
				// Create different types of metrics concurrently
				scope.Counter(fmt.Sprintf("counter_%d_%d", id, j)).Inc(1)
				scope.Gauge(fmt.Sprintf("gauge_%d_%d", id, j)).Update(float64(j))
				scope.Timer(fmt.Sprintf("timer_%d_%d", id, j)).Record(time.Millisecond)

				// Create histogram with random bucket
				if j%10 == 0 {
					scope.Histogram(fmt.Sprintf("hist_%d_%d", id, j), DefaultBuckets).RecordValue(float64(j))
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify no panics occurred
	assert.Equal(t, int32(0), atomic.LoadInt32(&panics), "No panics should occur during concurrent metric creation")

	// Verify all metrics were created
	snapshot := scope.Snapshot()
	expectedCounters := numGoroutines * metricsPerGoroutine
	expectedGauges := numGoroutines * metricsPerGoroutine
	expectedTimers := numGoroutines * metricsPerGoroutine
	expectedHistograms := numGoroutines * (metricsPerGoroutine / 10)

	assert.Equal(t, expectedCounters, len(snapshot.Counters()), "All counters should be created")
	assert.Equal(t, expectedGauges, len(snapshot.Gauges()), "All gauges should be created")
	assert.Equal(t, expectedTimers, len(snapshot.Timers()), "All timers should be created")
	assert.Equal(t, expectedHistograms, len(snapshot.Histograms()), "All histograms should be created")
}

// TestConcurrentScopeCreation tests concurrent subscope creation and access
func TestConcurrentScopeCreation(t *testing.T) {
	rootScope := NewTestScope("root", map[string]string{"service": "test"})

	const numGoroutines = 50
	const scopesPerGoroutine = 100

	var wg sync.WaitGroup
	var createdScopes int32
	var panics int32

	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt32(&panics, 1)
				}
			}()

			for j := 0; j < scopesPerGoroutine; j++ {
				// Create scopes with different patterns
				scope1 := rootScope.SubScope(fmt.Sprintf("subsope_%d_%d", id, j))
				scope2 := rootScope.Tagged(map[string]string{
					"worker":    strconv.Itoa(id),
					"iteration": strconv.Itoa(j),
				})

				// Use the scopes to create metrics
				scope1.Counter("test_counter").Inc(1)
				scope2.Gauge("test_gauge").Update(float64(j))

				atomic.AddInt32(&createdScopes, 2)
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int32(0), atomic.LoadInt32(&panics), "No panics should occur during concurrent scope creation")
	expectedScopes := int32(numGoroutines * scopesPerGoroutine * 2)
	assert.Equal(t, expectedScopes, atomic.LoadInt32(&createdScopes), "All scopes should be created successfully")
}

// TestHighCardinalityPerformance tests performance under high cardinality conditions
func TestHighCardinalityPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping high cardinality test in short mode")
	}

	scope := NewTestScope("perf", nil)

	const numUniqueMetrics = 10000
	start := time.Now()

	// Create many unique metrics to simulate high cardinality
	for i := 0; i < numUniqueMetrics; i++ {
		tags := map[string]string{
			"endpoint": fmt.Sprintf("endpoint_%d", i%100),
			"method":   fmt.Sprintf("method_%d", i%10),
			"status":   fmt.Sprintf("status_%d", i%5),
			"unique":   strconv.Itoa(i),
		}

		taggedScope := scope.Tagged(tags)
		taggedScope.Counter("requests").Inc(1)
		taggedScope.Timer("latency").Record(time.Duration(i) * time.Microsecond)
	}

	elapsed := time.Since(start)

	// Performance should be reasonable even with high cardinality
	maxDuration := 5 * time.Second
	assert.Less(t, elapsed, maxDuration,
		"High cardinality metric creation should complete within %v, took %v", maxDuration, elapsed)

	// Verify metrics were created
	snapshot := scope.Snapshot()
	assert.Greater(t, len(snapshot.Counters()), numUniqueMetrics/2,
		"Should have created significant number of unique counters")
}

// TestMemoryGrowthBounds ensures memory growth stays within reasonable bounds
func TestMemoryGrowthBounds(t *testing.T) {
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	scope := NewTestScope("memory", nil)

	// Create and use many metrics
	const numMetrics = 5000
	for i := 0; i < numMetrics; i++ {
		counter := scope.Counter(fmt.Sprintf("metric_%d", i))
		counter.Inc(int64(i))

		gauge := scope.Gauge(fmt.Sprintf("gauge_%d", i))
		gauge.Update(float64(i))

		if i%10 == 0 {
			timer := scope.Timer(fmt.Sprintf("timer_%d", i))
			timer.Record(time.Duration(i) * time.Microsecond)
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)

	var memGrowth uint64
	if m2.Alloc > m1.Alloc {
		memGrowth = m2.Alloc - m1.Alloc
	} else {
		memGrowth = 0 // Memory usage decreased (GC occurred)
	}

	// Memory growth should be reasonable (less than 10MB for this test)
	maxMemoryGrowth := uint64(10 * 1024 * 1024) // 10MB
	assert.Less(t, memGrowth, maxMemoryGrowth,
		"Memory growth (%d bytes) should be less than %d bytes", memGrowth, maxMemoryGrowth)

	t.Logf("Memory growth: %d bytes for %d metrics", memGrowth, numMetrics)
}

// TestReporterStress tests the reporter under stress conditions
func TestReporterStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	scope := NewTestScope("stress", nil)

	const numWorkers = 20
	const operationsPerWorker = 1000
	const testDuration = 2 * time.Second

	var wg sync.WaitGroup
	var operations int64
	var errors int32

	stopChan := make(chan struct{})

	// Start workers
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < operationsPerWorker; j++ {
				select {
				case <-stopChan:
					return
				default:
				}

				func() {
					defer func() {
						if r := recover(); r != nil {
							atomic.AddInt32(&errors, 1)
							return
						}
					}()

					// Mix of operations
					switch j % 4 {
					case 0:
						scope.Counter(fmt.Sprintf("counter_%d_%d", workerID, j%100)).Inc(1)
					case 1:
						scope.Gauge(fmt.Sprintf("gauge_%d_%d", workerID, j%100)).Update(float64(j))
					case 2:
						scope.Timer(fmt.Sprintf("timer_%d_%d", workerID, j%100)).Record(time.Microsecond)
					case 3:
						taggedScope := scope.Tagged(map[string]string{
							"worker": strconv.Itoa(workerID),
							"batch":  strconv.Itoa(j / 100),
						})
						taggedScope.Counter("tagged_counter").Inc(1)
					}

					atomic.AddInt64(&operations, 1)
				}()
			}
		}(i)
	}

	// Stop after duration
	time.AfterFunc(testDuration, func() {
		close(stopChan)
	})

	wg.Wait()

	totalOps := atomic.LoadInt64(&operations)
	totalErrors := atomic.LoadInt32(&errors)

	assert.Equal(t, int32(0), totalErrors, "No errors should occur during stress test")
	assert.Greater(t, totalOps, int64(numWorkers*operationsPerWorker/2),
		"Should complete significant number of operations")

	opsPerSecond := float64(totalOps) / testDuration.Seconds()
	t.Logf("Completed %d operations in %v (%.0f ops/sec)", totalOps, testDuration, opsPerSecond)
}

// BenchmarkScopeCreationPatterns benchmarks different scope creation patterns
func BenchmarkScopeCreationPatterns(b *testing.B) {
	rootScope := NewTestScope("bench", map[string]string{"service": "test"})

	testCases := []struct {
		name string
		fn   func() Scope
	}{
		{"EmptyTags", func() Scope {
			return rootScope.Tagged(map[string]string{})
		}},
		{"NilTags", func() Scope {
			return rootScope.Tagged(nil)
		}},
		{"SingleTag", func() Scope {
			return rootScope.Tagged(map[string]string{"key": "value"})
		}},
		{"FewTags", func() Scope {
			return rootScope.Tagged(map[string]string{
				"env": "prod", "version": "1.0",
			})
		}},
		{"ManyTags", func() Scope {
			return rootScope.Tagged(map[string]string{
				"service": "test", "env": "prod", "version": "1.0",
				"region": "us-east-1", "az": "us-east-1a", "instance": "i-1234567890abcdef0",
			})
		}},
		{"SubScope", func() Scope {
			return rootScope.SubScope("subsystem")
		}},
		{"NestedSubScope", func() Scope {
			return rootScope.SubScope("level1").SubScope("level2")
		}},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tc.fn()
			}
		})
	}
}

// BenchmarkMetricCreation benchmarks metric creation performance
func BenchmarkMetricCreation(b *testing.B) {
	scope := NewTestScope("bench", nil)

	b.Run("Counter", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			scope.Counter("test_counter").Inc(1)
		}
	})

	b.Run("Gauge", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			scope.Gauge("test_gauge").Update(float64(i))
		}
	})

	b.Run("Timer", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			scope.Timer("test_timer").Record(time.Microsecond)
		}
	})

	b.Run("Histogram", func(b *testing.B) {
		buckets := DefaultBuckets
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			scope.Histogram("test_histogram", buckets).RecordValue(float64(i))
		}
	})
}

// BenchmarkConcurrentAccess benchmarks concurrent access to the same metrics
func BenchmarkConcurrentAccess(b *testing.B) {
	scope := NewTestScope("bench", nil)
	counter := scope.Counter("shared_counter")
	gauge := scope.Gauge("shared_gauge")
	timer := scope.Timer("shared_timer")

	b.Run("Counter", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				counter.Inc(1)
			}
		})
	})

	b.Run("Gauge", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				gauge.Update(float64(i))
				i++
			}
		})
	})

	b.Run("Timer", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				timer.Record(time.Microsecond)
			}
		})
	})
}
