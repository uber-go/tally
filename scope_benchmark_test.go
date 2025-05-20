// Copyright (c) 2021 Uber Technologies, Inc.
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
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkNameGeneration(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
	}, 0)
	s := root.(*scope)
	for n := 0; n < b.N; n++ {
		s.fullyQualifiedName("take.me.to")
	}
}

func BenchmarkCounterAllocation(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
	}, 0)
	s := root.(*scope)

	ids := make([]string, 0, b.N)
	for i := 0; i < b.N; i++ {
		ids = append(ids, fmt.Sprintf("take.me.to.%d", i))
	}
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		s.Counter(ids[n])
	}
}

func BenchmarkSanitizedCounterAllocation(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:          "funkytown",
		Reporter:        NullStatsReporter,
		SanitizeOptions: &alphanumericSanitizerOpts,
	}, 0)
	s := root.(*scope)

	ids := make([]string, 0, b.N)
	for i := 0; i < b.N; i++ {
		ids = append(ids, fmt.Sprintf("take.me.to.%d", i))
	}
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		s.Counter(ids[n])
	}
}

func BenchmarkNameGenerationTagged(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix: "funkytown",
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
		Reporter: NullStatsReporter,
	}, 0)
	s := root.(*scope)
	for n := 0; n < b.N; n++ {
		s.fullyQualifiedName("take.me.to")
	}
}

func BenchmarkScopeTaggedCachedSubscopes(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
	}, 0)
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		root.Tagged(map[string]string{
			"foo": "bar",
			"baz": "qux",
			"qux": "quux",
		})
	}
}

func BenchmarkScopeTaggedNoCachedSubscopes(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
	}, 0)

	values := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		values[i] = strconv.Itoa(i)
	}

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		root.Tagged(map[string]string{
			"foo": values[n],
			"baz": values[n],
			"qux": values[n],
		})
	}
}

func BenchmarkScopeTaggedNoCachedSubscopesParallel(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
	}, 0)

	b.ResetTimer()

	index := int64(0)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := atomic.AddInt64(&index, 1)
			value := strconv.Itoa(int(n))

			// Validated that the compiler is not optimizing this with a cpu profiler.
			// Check https://github.com/uber-go/tally/pull/184 for more details
			root.Tagged(map[string]string{
				"foo": value,
				"baz": value,
				"qux": value,
			})
		}
	})
}

func BenchmarkScopeTaggedNoCachedSubscopesParallelPercentageCached(b *testing.B) {
	percentageCached := int64(5)
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
	}, 0)

	cachedMap := map[string]string{
		"foo": "any",
		"baz": "any",
		"qux": "any",
	}
	root.Tagged(cachedMap)

	b.ResetTimer()

	index := int64(-1)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := atomic.AddInt64(&index, 1)

			if (n % 100) < percentageCached {
				root.Tagged(cachedMap)
				continue
			}

			value := strconv.Itoa(int(n))

			// Validated that the compiler is not optimizing this with a cpu profiler.
			// Check https://github.com/uber-go/tally/pull/184 for more details
			root.Tagged(map[string]string{
				"foo": value,
				"baz": value,
				"qux": value,
			})
		}
	})
}

func BenchmarkNameGenerationNoPrefix(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Reporter: NullStatsReporter,
	}, 0)
	s := root.(*scope)
	for n := 0; n < b.N; n++ {
		s.fullyQualifiedName("im.all.alone")
	}
}

func BenchmarkHistogramAllocation(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Reporter: NullStatsReporter,
	}, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root.Histogram("foo"+strconv.Itoa(i), DefaultBuckets)
	}
}

func BenchmarkHistogramExisting(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Reporter: NullStatsReporter,
	}, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root.Histogram("foo", DefaultBuckets)
	}
}

func benchmarkScopeReportingN(b *testing.B, numElems int) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:          "funkytown",
		CachedReporter:  noopCachedReporter{},
		SanitizeOptions: &alphanumericSanitizerOpts,
	}, 0)
	s := root.(*scope)

	ids := make([]string, 0, numElems)
	for i := 0; i < numElems; i++ {
		id := fmt.Sprintf("take.me.to.%d", i)
		ids = append(ids, id)
		s.Counter(id)
	}
	_ = ids
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		s.cachedReport()
	}
}

func BenchmarkScopeReporting(b *testing.B) {
	for i := 1; i <= 1000000; i *= 10 {
		size := fmt.Sprintf("size%d", i)
		b.Run(size, func(b *testing.B) {
			benchmarkScopeReportingN(b, i)
		})
	}
}

type noopStat struct{}

func (s noopStat) ReportCount(value int64)            {}
func (s noopStat) ReportGauge(value float64)          {}
func (s noopStat) ReportTimer(interval time.Duration) {}
func (s noopStat) ValueBucket(bucketLowerBound, bucketUpperBound float64) CachedHistogramBucket {
	return s
}
func (s noopStat) DurationBucket(bucketLowerBound, bucketUpperBound time.Duration) CachedHistogramBucket {
	return s
}
func (s noopStat) ReportSamples(value int64) {}

type noopCachedReporter struct{}

func (n noopCachedReporter) Capabilities() Capabilities {
	return n
}

func (n noopCachedReporter) Reporting() bool { return true }
func (n noopCachedReporter) Tagging() bool   { return true }
func (n noopCachedReporter) Flush()          {}

func (n noopCachedReporter) ReportCounter(name string, tags map[string]string, value int64) {}
func (n noopCachedReporter) ReportGauge(name string, tags map[string]string, value float64) {}

func (n noopCachedReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
}

func (n noopCachedReporter) ReportHistogramValueSamples(name string, tags map[string]string, buckets Buckets, bucketLowerBound float64, bucketUpperBound float64, samples int64) {
}
func (n noopCachedReporter) ReportHistogramDurationSamples(name string, tags map[string]string, buckets Buckets, bucketLowerBound time.Duration, bucketUpperBound time.Duration, samples int64) {
}

func (n noopCachedReporter) AllocateCounter(name string, tags map[string]string) CachedCount {
	return noopStat{}
}

func (n noopCachedReporter) AllocateGauge(name string, tags map[string]string) CachedGauge {
	return noopStat{}
}

func (n noopCachedReporter) AllocateTimer(name string, tags map[string]string) CachedTimer {
	return noopStat{}
}
func (n noopCachedReporter) AllocateHistogram(name string, tags map[string]string, buckets Buckets) CachedHistogram {
	return noopStat{}
}

func BenchmarkScopePooling(b *testing.B) {
	// Test cases for with and without pooling
	testCases := []struct {
		name        string
		withPooling bool
	}{
		{
			name:        "WithoutPooling",
			withPooling: false,
		},
		{
			name:        "WithPooling",
			withPooling: true,
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			reporter := &noopCachedReporter{}
			root, closer := NewRootScope(ScopeOptions{
				CachedReporter:         reporter,
				Prefix:                 "benchmark",
				NoCacheSubscopes:       true, // Force ephemeral scopes
				EnableScopePooling:     tc.withPooling,
				OmitCardinalityMetrics: true,
			}, 0)
			defer closer.Close()

			// Reset the benchmark timer to exclude setup time
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				// Create and use 100 subscopes per iteration
				for j := 0; j < 100; j++ {
					// Create a unique tag to force new scope creation
					scope := root.Tagged(map[string]string{
						"iteration": strconv.Itoa(i),
						"subscope":  strconv.Itoa(j),
					})

					// Use the scope by recording some metrics
					scope.Counter("counter").Inc(1)
					scope.Gauge("gauge").Update(float64(j))
					scope.Timer("timer").Record(time.Millisecond * time.Duration(j))

					// Close the scope to return it to the pool (if pooling is enabled)
					scope.(io.Closer).Close()
				}
			}
		})
	}
}

func BenchmarkScopePoolingRealistic(b *testing.B) {
	// Just a few unique tag combinations that will be heavily reused
	tagSets := []map[string]string{
		{"service": "api", "endpoint": "users"},
		{"service": "api", "endpoint": "products"},
		{"service": "database", "operation": "read"},
	}

	runBench := func(name string, enablePooling bool) {
		b.Run(name, func(b *testing.B) {
			r := &noopCachedReporter{}
			root, closer := NewRootScope(ScopeOptions{
				CachedReporter:         r,
				NoCacheSubscopes:       true,
				EnableScopePooling:     enablePooling,
				OmitCardinalityMetrics: true,
			}, 0)
			defer closer.Close()

			// Pre-warm to ensure fair comparison
			for i := 0; i < 1000; i++ {
				tagSet := tagSets[i%len(tagSets)]
				s := root.Tagged(tagSet)
				s.(io.Closer).Close()
			}

			b.ResetTimer()

			// Force garbage collection before starting
			runtime.GC()

			// Track memory stats
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			allocStart := m.TotalAlloc

			for i := 0; i < b.N; i++ {
				// Create 10,000 scopes with just 3 unique tag combinations
				// This should heavily benefit from pooling
				for j := 0; j < 10000; j++ {
					tagSet := tagSets[j%len(tagSets)]
					s := root.Tagged(tagSet)

					// Do some work with the scope (increment counters, update gauges)
					s.Counter("counter").Inc(1)
					s.Gauge("gauge").Update(42.0)
					s.Histogram("hist", ValueBuckets{0, 10, 100}).RecordValue(50.0)

					// Close the scope to return it to the pool
					s.(io.Closer).Close()
				}
			}

			// Record final memory stats
			runtime.ReadMemStats(&m)
			totalAlloc := m.TotalAlloc - allocStart

			b.ReportMetric(float64(totalAlloc)/float64(b.N), "B/op-total")
		})
	}

	// Run with and without pooling for comparison
	runBench("WithPooling", true)
	runBench("WithoutPooling", false)
}

func BenchmarkScopePoolingHighReuse(b *testing.B) {
	// Test cases for with and without pooling
	testCases := []struct {
		name        string
		withPooling bool
	}{
		{
			name:        "WithoutPooling",
			withPooling: false,
		},
		{
			name:        "WithPooling",
			withPooling: true,
		},
	}

	// Create just 10 unique tag combinations
	uniqueTags := make([]map[string]string, 10)
	for i := 0; i < 10; i++ {
		uniqueTags[i] = map[string]string{
			"service":  fmt.Sprintf("service-%d", i%3),
			"endpoint": fmt.Sprintf("endpoint-%d", i%3),
			"id":       fmt.Sprintf("id-%d", i),
		}
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			reporter := &noopCachedReporter{}
			root, closer := NewRootScope(ScopeOptions{
				CachedReporter:         reporter,
				Prefix:                 "benchmark",
				NoCacheSubscopes:       true, // Force ephemeral scopes
				EnableScopePooling:     tc.withPooling,
				OmitCardinalityMetrics: true,
			}, 0)
			defer closer.Close()

			// Reset the benchmark timer to exclude setup time
			b.ResetTimer()

			// Force garbage collection before starting
			runtime.GC()

			// Track memory stats
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			allocStart := m.TotalAlloc

			for i := 0; i < b.N; i++ {
				// Create thousands of scopes with a few unique tag combinations
				// Heavily exercising the pool reuse pattern
				for j := 0; j < 20000; j++ {
					// Use just the small set of unique tags to ensure high reuse rate
					scope := root.Tagged(uniqueTags[j%len(uniqueTags)])

					// Perform actual work with the scope to simulate real usage
					counter := scope.Counter("requests")
					counter.Inc(1)
					scope.Gauge("latency").Update(float64(j % 100))
					scope.(io.Closer).Close()
				}
			}

			// Record final memory stats
			runtime.ReadMemStats(&m)
			totalAlloc := m.TotalAlloc - allocStart

			b.ReportMetric(float64(totalAlloc)/float64(b.N), "B/op-total")
		})
	}
}

func BenchmarkScopePoolingIntensive(b *testing.B) {
	// Create a small set of unique tag combinations that will be reused heavily
	uniqueTags := make([]map[string]string, 5)
	for i := 0; i < 5; i++ {
		uniqueTags[i] = map[string]string{
			"service":  fmt.Sprintf("service-%d", i%2),
			"endpoint": fmt.Sprintf("endpoint-%d", i%2),
			"env":      "production",
		}
	}

	// Set up a profiler to track memory allocations
	var memProfiler runtime.MemStats
	var totalAllocs, totalBytes uint64

	// Test cases for with and without pooling
	testCases := []struct {
		name        string
		withPooling bool
	}{
		{
			name:        "WithoutPooling",
			withPooling: false,
		},
		{
			name:        "WithPooling",
			withPooling: true,
		},
	}

	// Use much larger numbers to stress the system more
	const (
		iterationsPerRun   = 5_000 // How many scopes to create per b.N
		operationsPerScope = 10    // How many operations to do with each scope
	)

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Reset memory profiler
			runtime.ReadMemStats(&memProfiler)
			before := memProfiler.TotalAlloc
			beforeObjs := memProfiler.Mallocs

			// Create a reporter
			reporter := &noopCachedReporter{}

			// Create root scope with appropriate options
			root, closer := NewRootScope(ScopeOptions{
				Prefix:             "benchmark",
				Reporter:           reporter,
				NoCacheSubscopes:   true, // Force ephemeral scopes
				EnableScopePooling: tc.withPooling,
			}, time.Hour)
			defer closer.Close()

			// Run the benchmark
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Create many subscopes with recycling
				for j := 0; j < iterationsPerRun; j++ {
					// Use one of our 5 tag combinations, causing high reuse with pooling
					tagIdx := j % 5

					// Create and use a scope
					scope := root.Tagged(uniqueTags[tagIdx])

					// Perform multiple operations with this scope to increase contention
					scope.Counter("requests").Inc(1)
					scope.Gauge("latency").Update(float64(j))
					scope.Timer("duration").Record(time.Millisecond * time.Duration(j%100))

					// Add more metric operations to increase work per scope
					for op := 0; op < operationsPerScope; op++ {
						// Different types of operations
						counter := scope.Counter(fmt.Sprintf("counter-%d", op))
						counter.Inc(int64(op + 1))

						gauge := scope.Gauge(fmt.Sprintf("gauge-%d", op))
						gauge.Update(float64(op * 10))

						if op%2 == 0 {
							timer := scope.Timer(fmt.Sprintf("timer-%d", op))
							timer.Record(time.Millisecond * time.Duration(op))
						}
					}

					// Close explicitly to return to pool (in the pooling case)
					scope.(io.Closer).Close()
				}
			}
			b.StopTimer()

			// Measure memory after benchmark
			runtime.GC() // Force GC
			runtime.ReadMemStats(&memProfiler)
			after := memProfiler.TotalAlloc
			afterObjs := memProfiler.Mallocs

			// Calculate total allocations during the test
			totalBytes = after - before
			totalAllocs = afterObjs - beforeObjs

			// Report custom metrics
			b.ReportMetric(float64(totalBytes)/float64(b.N), "B/op-total")
			b.ReportMetric(float64(totalAllocs)/float64(b.N), "allocs/op")
		})
	}
}

// BenchmarkScopePoolingExtreme creates a benchmark with extreme reuse conditions
// to demonstrate the memory advantages of pooling
func BenchmarkScopePoolingExtreme(b *testing.B) {
	// Just a single tag combination to maximize reuse
	tags := map[string]string{
		"service": "benchmark-service",
		"env":     "production",
	}

	// Test cases for with and without pooling
	testCases := []struct {
		name        string
		withPooling bool
	}{
		{
			name:        "WithoutPooling",
			withPooling: false,
		},
		{
			name:        "WithPooling",
			withPooling: true,
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Create a reporter
			reporter := &noopCachedReporter{}

			// Create root scope with appropriate options
			root, closer := NewRootScope(ScopeOptions{
				Prefix:             "benchmark",
				Reporter:           reporter,
				NoCacheSubscopes:   true, // Force ephemeral scopes
				EnableScopePooling: tc.withPooling,
			}, time.Hour)
			defer closer.Close()

			// Warmup phase - very important to establish the pool
			// For pooled implementation, this will populate the pool
			// For non-pooled, this is just extra work
			if tc.withPooling {
				for i := 0; i < 1000; i++ {
					scope := root.Tagged(tags)
					scope.Counter("warmup").Inc(1)
					scope.(io.Closer).Close()
				}
			}

			// Run the benchmark with memory tracking
			var m runtime.MemStats
			runtime.GC() // Force GC before measuring
			runtime.ReadMemStats(&m)
			beforeAlloc := m.TotalAlloc
			beforeMallocs := m.Mallocs

			b.ResetTimer()

			// Create many scopes but with the same tags to maximize pooling benefits
			for i := 0; i < b.N; i++ {
				// Just reuse the same tags thousands of times
				for j := 0; j < 10_000; j++ {
					// Get a scope from pool (with pooling) or create new (without pooling)
					scope := root.Tagged(tags)

					// Record just one metric to reduce noise
					scope.Counter("requests").Inc(1)

					// Return to pool immediately
					scope.(io.Closer).Close()
				}
			}

			b.StopTimer()

			// Measure memory after benchmark
			runtime.GC() // Force GC
			runtime.ReadMemStats(&m)

			// Calculate and report memory usage
			totalAlloc := m.TotalAlloc - beforeAlloc
			mallocs := m.Mallocs - beforeMallocs

			b.ReportMetric(float64(totalAlloc)/float64(b.N), "B/op-total")
			b.ReportMetric(float64(mallocs)/float64(b.N), "allocs/op")
		})
	}
}

// BenchmarkScopePoolingAllocations specifically focuses on memory allocations
// with minimal other operations to isolate the impact of pooling
func BenchmarkScopePoolingAllocations(b *testing.B) {
	// Use a single tag set for all tests
	tags := map[string]string{"service": "test-service"}

	b.Run("WithoutPooling", func(b *testing.B) {
		root, closer := NewRootScope(ScopeOptions{
			NoCacheSubscopes:   true,
			EnableScopePooling: false,
		}, 0)
		defer closer.Close()

		// Warm up
		runtime.GC()

		// Count allocations
		var afterAllocs uint64
		// Discard initial allocation counts during warmup
		_ = testing.AllocsPerRun(100, func() {
			// Just create and close scopes to count allocations
			for i := 0; i < 10; i++ {
				scope := root.Tagged(tags)
				scope.(io.Closer).Close()
			}
		})

		b.ResetTimer()
		allocsPerLoop := uint64(0)

		// Run in smaller batches to prevent integer overflow
		const batchSize = 10000
		for i := 0; i < b.N; i += batchSize {
			count := batchSize
			if i+count > b.N {
				count = b.N - i
			}

			afterAllocs = uint64(testing.AllocsPerRun(1, func() {
				for j := 0; j < count; j++ {
					scope := root.Tagged(tags)
					scope.(io.Closer).Close()
				}
			}))
			allocsPerLoop += afterAllocs
		}

		// Report direct allocation counts
		b.ReportMetric(float64(allocsPerLoop)/float64(b.N), "allocs/op")
	})

	b.Run("WithPooling", func(b *testing.B) {
		root, closer := NewRootScope(ScopeOptions{
			NoCacheSubscopes:   true,
			EnableScopePooling: true,
		}, 0)
		defer closer.Close()

		// Create a warm pool
		for i := 0; i < 100; i++ {
			scope := root.Tagged(tags)
			scope.(io.Closer).Close()
		}

		runtime.GC()

		// Count allocations
		var afterAllocs uint64
		// Discard initial allocation counts during warmup
		_ = testing.AllocsPerRun(100, func() {
			// Just create and close scopes to count allocations
			for i := 0; i < 10; i++ {
				scope := root.Tagged(tags)
				scope.(io.Closer).Close()
			}
		})

		b.ResetTimer()
		allocsPerLoop := uint64(0)

		// Run in smaller batches to prevent integer overflow
		const batchSize = 10000
		for i := 0; i < b.N; i += batchSize {
			count := batchSize
			if i+count > b.N {
				count = b.N - i
			}

			afterAllocs = uint64(testing.AllocsPerRun(1, func() {
				for j := 0; j < count; j++ {
					scope := root.Tagged(tags)
					scope.(io.Closer).Close()
				}
			}))
			allocsPerLoop += afterAllocs
		}

		// Report direct allocation counts
		b.ReportMetric(float64(allocsPerLoop)/float64(b.N), "allocs/op")
	})
}

// BenchmarkSyncMapConcurrent benchmarks the sync.Map implementation for metric access with concurrent goroutines
func BenchmarkSyncMapConcurrent(b *testing.B) {
	root, closer := NewRootScope(ScopeOptions{}, 0)
	defer closer.Close()

	// First, create a fixed set of metrics that will be accessed
	metricNames := []string{
		"requests.count",
		"requests.latency",
		"requests.errors",
		"requests.success",
		"db.queries",
		"db.latency",
		"cache.hits",
		"cache.misses",
		"memory.allocated",
		"memory.freed",
	}

	// Pre-create all metrics
	for _, name := range metricNames {
		root.Counter(name)
	}

	b.ResetTimer()

	// Run with different levels of concurrency
	for _, numGoroutines := range []int{1, 4, 8, 16, 32, 64} {
		b.Run(fmt.Sprintf("Goroutines-%d", numGoroutines), func(b *testing.B) {
			var wg sync.WaitGroup

			// Launch goroutines
			for g := 0; g < numGoroutines; g++ {
				wg.Add(1)
				go func(goroutineNum int) {
					defer wg.Done()

					// Each goroutine processes its share of operations
					iterations := b.N / numGoroutines
					if goroutineNum == 0 {
						// First goroutine does any remainder operations
						iterations += b.N % numGoroutines
					}

					// Repeatedly access metrics
					for i := 0; i < iterations; i++ {
						// Use a different metric for each iteration to simulate real-world distribution
						name := metricNames[i%len(metricNames)]
						counter := root.Counter(name)
						counter.Inc(1)
					}
				}(g)
			}

			wg.Wait()
		})
	}
}
