package tally

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// BenchmarkConcurrentFlush measures the performance of concurrent metric registration
// and reporting with different lock scoping strategies
func BenchmarkConcurrentFlush(b *testing.B) {
	// Test configurations
	configs := []struct {
		name            string
		numScopes       int
		metricsPerScope int
		reportInterval  time.Duration
	}{
		{
			name:            "Small",
			numScopes:       1000,
			metricsPerScope: 10,
			reportInterval:  time.Millisecond * 100,
		},
		{
			name:            "Medium",
			numScopes:       1000,
			metricsPerScope: 50,
			reportInterval:  time.Millisecond * 100,
		},
		{
			name:            "Large",
			numScopes:       1000,
			metricsPerScope: 100,
			reportInterval:  time.Millisecond * 100,
		},
	}

	for _, cfg := range configs {
		b.Run(cfg.name, func(b *testing.B) {
			// Create a test reporter that does minimal work
			reporter := newTestStatsReporter()
			reporter.noWait = true       // Disable wait groups for benchmarking
			reporter.suppressLogs = true // Suppress debug logs

			// Create root scope with the test reporter
			root, closer := NewRootScope(ScopeOptions{
				Reporter:               reporter,
				OmitCardinalityMetrics: true,
			}, cfg.reportInterval)
			defer closer.Close()

			// Create scopes and metrics
			scopes := make([]Scope, cfg.numScopes)
			for i := 0; i < cfg.numScopes; i++ {
				scopes[i] = root.Tagged(map[string]string{
					"scope_id": string(rune('a' + i%26)),
				})
			}

			// Create metrics for each scope
			counters := make([][]Counter, cfg.numScopes)
			gauges := make([][]Gauge, cfg.numScopes)
			for i := 0; i < cfg.numScopes; i++ {
				counters[i] = make([]Counter, cfg.metricsPerScope)
				gauges[i] = make([]Gauge, cfg.metricsPerScope)
				for j := 0; j < cfg.metricsPerScope; j++ {
					counters[i][j] = scopes[i].Counter("counter_" + string(rune('a'+j)))
					gauges[i][j] = scopes[i].Gauge("gauge_" + string(rune('a'+j)))
				}
			}

			// Start concurrent metric updates
			var wg sync.WaitGroup
			stop := make(chan struct{})

			// Start metric update goroutines
			for i := 0; i < cfg.numScopes; i++ {
				wg.Add(1)
				go func(scopeIdx int) {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
							// Update metrics
							for j := 0; j < cfg.metricsPerScope; j++ {
								counters[scopeIdx][j].Inc(1)
								gauges[scopeIdx][j].Update(float64(j))
							}
							time.Sleep(time.Microsecond) // Small delay to simulate real-world usage
						}
					}
				}(i)
			}

			// Benchmark reporting
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Force a report
				root.(*scope).reportLoopRun()
			}
			b.StopTimer()

			// Cleanup
			close(stop)
			wg.Wait()
		})
	}
}

// BenchmarkFlushLockContention measures the lock contention during flush operations
func BenchmarkFlushLockContention(b *testing.B) {
	// Create a test reporter
	reporter := newTestStatsReporter()
	reporter.noWait = true
	reporter.suppressLogs = true // Suppress debug logs

	// Create root scope
	root, closer := NewRootScope(ScopeOptions{
		Reporter:               reporter,
		OmitCardinalityMetrics: true,
	}, time.Millisecond*100)
	defer closer.Close()

	// Create a large number of scopes with metrics
	const numScopes = 1000
	const metricsPerScope = 50

	// Create scopes and metrics
	scopes := make([]Scope, numScopes)
	for i := 0; i < numScopes; i++ {
		scopes[i] = root.Tagged(map[string]string{
			"scope_id": string(rune('a' + i%26)),
		})
	}

	// Create metrics for each scope
	counters := make([][]Counter, numScopes)
	gauges := make([][]Gauge, numScopes)
	for i := 0; i < numScopes; i++ {
		counters[i] = make([]Counter, metricsPerScope)
		gauges[i] = make([]Gauge, metricsPerScope)
		for j := 0; j < metricsPerScope; j++ {
			counters[i][j] = scopes[i].Counter("counter_" + string(rune('a'+j)))
			gauges[i][j] = scopes[i].Gauge("gauge_" + string(rune('a'+j)))
		}
	}

	// Start concurrent metric updates
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Start metric update goroutines
	for i := 0; i < numScopes; i++ {
		wg.Add(1)
		go func(scopeIdx int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					// Update metrics
					for j := 0; j < metricsPerScope; j++ {
						counters[scopeIdx][j].Inc(1)
						gauges[scopeIdx][j].Update(float64(j))
					}
					time.Sleep(time.Microsecond)
				}
			}
		}(i)
	}

	// Benchmark reporting with different concurrency levels
	concurrencyLevels := []int{1, 2, 4, 8}
	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("Concurrency_%d", concurrency), func(b *testing.B) {
			var reportWg sync.WaitGroup
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				reportWg.Add(concurrency)
				for j := 0; j < concurrency; j++ {
					go func() {
						defer reportWg.Done()
						root.(*scope).reportLoopRun()
					}()
				}
				reportWg.Wait()
			}
			b.StopTimer()
		})
	}

	// Cleanup
	close(stop)
	wg.Wait()
}

// BenchmarkMetricRegistrationDuringFlush measures the performance of metric registration
// while flush operations are ongoing
func BenchmarkMetricRegistrationDuringFlush(b *testing.B) {
	// Create a test reporter
	reporter := newTestStatsReporter()
	reporter.noWait = true
	reporter.suppressLogs = true // Suppress debug logs

	// Create root scope
	root, closer := NewRootScope(ScopeOptions{
		Reporter:               reporter,
		OmitCardinalityMetrics: true,
	}, time.Millisecond*100)
	defer closer.Close()

	// Start continuous reporting in background
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				root.(*scope).reportLoopRun()
				time.Sleep(time.Millisecond * 10)
			}
		}
	}()

	// Benchmark metric registration
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create a new scope with metrics
		scope := root.Tagged(map[string]string{
			"benchmark_id": fmt.Sprintf("%d", i%1000), // Ensure we stay within 1000 scopes
		})

		// Register multiple metrics
		for j := 0; j < 100; j++ {
			scope.Counter(fmt.Sprintf("counter_%d", j)).Inc(1)
			scope.Gauge(fmt.Sprintf("gauge_%d", j)).Update(float64(j))
		}
	}
	b.StopTimer()

	// Cleanup
	close(stop)
}

// BenchmarkConcurrentFlushWithLowContention measures how the reduced lock scope
// during flush impacts performance with concurrent metric operations
func BenchmarkConcurrentFlushWithLowContention(b *testing.B) {
	b.Run("Sequential", func(b *testing.B) {
		// Make sure parallel flush is disabled
		EnableParallelFlush.Store(false)
		MaxParallelFlushGoroutines = 0
		runConcurrentFlushBenchmark(b)
	})

	b.Run("Parallel_PerShard", func(b *testing.B) {
		// Enable parallel flush for this test with one goroutine per shard
		EnableParallelFlush.Store(true)
		MaxParallelFlushGoroutines = 0
		runConcurrentFlushBenchmark(b)
	})

	b.Run("WorkerPool_4", func(b *testing.B) {
		// Enable parallel flush with a fixed worker pool size of 4
		EnableParallelFlush.Store(true)
		MaxParallelFlushGoroutines = 4
		runConcurrentFlushBenchmark(b)
	})

	b.Run("WorkerPool_8", func(b *testing.B) {
		// Enable parallel flush with a fixed worker pool size of 8
		EnableParallelFlush.Store(true)
		MaxParallelFlushGoroutines = 8
		runConcurrentFlushBenchmark(b)
	})

	b.Run("WorkerPool_16", func(b *testing.B) {
		// Enable parallel flush with a fixed worker pool size of 16
		EnableParallelFlush.Store(true)
		MaxParallelFlushGoroutines = 16
		runConcurrentFlushBenchmark(b)
	})
}

// runConcurrentFlushBenchmark is a helper function that runs the actual benchmark logic
func runConcurrentFlushBenchmark(b *testing.B) {
	// Create a test reporter
	reporter := newTestStatsReporter()
	reporter.noWait = true
	reporter.suppressLogs = true

	// Create root scope with a high number of shards to reduce lock contention
	root, closer := NewRootScope(ScopeOptions{
		Reporter:               reporter,
		OmitCardinalityMetrics: true,
		// Set a higher number of shards than default (which is GOMAXPROCS)
		registryShardCount: 32,
	}, time.Millisecond*100)
	defer closer.Close()

	// Configuration for test
	const numScopes = 5000
	const metricsPerScope = 20
	const numUpdateGoroutines = 50 // Number of goroutines that will update metrics
	const numReportGoroutines = 8  // Number of concurrent reporting goroutines

	// Create scopes and metrics (distributed across shards)
	var allScopes []Scope
	var allCounters []Counter
	var allGauges []Gauge

	for i := 0; i < numScopes; i++ {
		// Ensure scopes are distributed across different shards by using varied tags
		s := root.Tagged(map[string]string{
			"shard": fmt.Sprintf("shard-%d", i%32),
			"id":    fmt.Sprintf("scope-%d", i),
		})

		// Create metrics for each scope
		counter := s.Counter(fmt.Sprintf("counter-%d", i))
		gauge := s.Gauge(fmt.Sprintf("gauge-%d", i))

		allScopes = append(allScopes, s)
		allCounters = append(allCounters, counter)
		allGauges = append(allGauges, gauge)
	}

	// Setup channels and wait groups
	var wg sync.WaitGroup
	stopUpdate := make(chan struct{})
	stopReport := make(chan struct{})

	// Start goroutines to continuously update metrics
	for i := 0; i < numUpdateGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Each goroutine updates different metrics to distribute the work
			offset := (idx * numScopes) / numUpdateGoroutines
			batchSize := numScopes / numUpdateGoroutines

			// If we're the last goroutine, make sure we get all remaining metrics
			if idx == numUpdateGoroutines-1 {
				batchSize = numScopes - offset
			}

			for {
				select {
				case <-stopUpdate:
					return
				default:
					// Update metrics in our assigned range
					for j := 0; j < batchSize; j++ {
						metricIdx := offset + j
						if metricIdx < len(allCounters) {
							allCounters[metricIdx].Inc(1)
							allGauges[metricIdx].Update(float64(idx))
						}
					}
					time.Sleep(time.Microsecond) // Small delay to simulate real-world usage
				}
			}
		}(i)
	}

	// Let metrics stabilize before benchmarking
	time.Sleep(10 * time.Millisecond)

	// Run the benchmark - measure repeated flush operations under load
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var reportWg sync.WaitGroup
		reportWg.Add(numReportGoroutines)

		// Start concurrent reporters
		for j := 0; j < numReportGoroutines; j++ {
			go func() {
				defer reportWg.Done()
				// Force a report
				root.(*scope).reportLoopRun()
			}()
		}

		// Wait for all reports to complete
		reportWg.Wait()
	}
	b.StopTimer()

	// Clean up
	close(stopUpdate)
	close(stopReport)
	wg.Wait()
}

// testStatsReporterWithDelay simulates a slow backend by adding a delay to each report
// (for benchmarking realistic flush scenarios)
type testStatsReporterWithDelay struct {
	testStatsReporter
	delay time.Duration
}

func (r *testStatsReporterWithDelay) ReportCounter(name string, tags map[string]string, value int64) {
	time.Sleep(r.delay)
	r.testStatsReporter.ReportCounter(name, tags, value)
}
func (r *testStatsReporterWithDelay) ReportGauge(name string, tags map[string]string, value float64) {
	time.Sleep(r.delay)
	r.testStatsReporter.ReportGauge(name, tags, value)
}
func (r *testStatsReporterWithDelay) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	time.Sleep(r.delay)
	r.testStatsReporter.ReportTimer(name, tags, interval)
}
func (r *testStatsReporterWithDelay) ReportHistogramValueSamples(name string, tags map[string]string, buckets Buckets, bucketLowerBound, bucketUpperBound float64, samples int64) {
	time.Sleep(r.delay)
	r.testStatsReporter.ReportHistogramValueSamples(name, tags, buckets, bucketLowerBound, bucketUpperBound, samples)
}
func (r *testStatsReporterWithDelay) ReportHistogramDurationSamples(name string, tags map[string]string, buckets Buckets, bucketLowerBound, bucketUpperBound time.Duration, samples int64) {
	time.Sleep(r.delay)
	r.testStatsReporter.ReportHistogramDurationSamples(name, tags, buckets, bucketLowerBound, bucketUpperBound, samples)
}

// BenchmarkRealisticFlush simulates a real-world scenario with moderate cardinality and backend latency
func BenchmarkRealisticFlush(b *testing.B) {
	const numScopes = 1000
	const numUpdateGoroutines = 8
	const numReportGoroutines = 4
	const backendDelay = 200 * time.Microsecond

	b.Run("Sequential", func(b *testing.B) {
		EnableParallelFlush.Store(false)
		MaxParallelFlushGoroutines = 0
		runRealisticFlushBenchmark(b, numScopes, numUpdateGoroutines, numReportGoroutines, backendDelay)
	})
	b.Run("WorkerPool_4", func(b *testing.B) {
		EnableParallelFlush.Store(true)
		MaxParallelFlushGoroutines = 4
		runRealisticFlushBenchmark(b, numScopes, numUpdateGoroutines, numReportGoroutines, backendDelay)
	})
}

func runRealisticFlushBenchmark(b *testing.B, numScopes, numUpdateGoroutines, numReportGoroutines int, backendDelay time.Duration) {
	reporter := &testStatsReporterWithDelay{
		testStatsReporter: *newTestStatsReporter(),
		delay:             backendDelay,
	}
	reporter.noWait = true
	reporter.suppressLogs = true

	root, closer := NewRootScope(ScopeOptions{
		Reporter:               reporter,
		OmitCardinalityMetrics: true,
		registryShardCount:     8, // typical for 4-8 CPU systems
	}, time.Millisecond*100)
	defer closer.Close()

	// Create scopes and metrics
	scopes := make([]Scope, numScopes)
	counters := make([]Counter, numScopes)
	gauges := make([]Gauge, numScopes)
	for i := 0; i < numScopes; i++ {
		s := root.Tagged(map[string]string{"id": fmt.Sprintf("scope-%d", i)})
		scopes[i] = s
		counters[i] = s.Counter("counter")
		gauges[i] = s.Gauge("gauge")
	}

	// Start update goroutines
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < numUpdateGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					for j := idx; j < numScopes; j += numUpdateGoroutines {
						counters[j].Inc(1)
						gauges[j].Update(float64(idx))
					}
					time.Sleep(time.Microsecond)
				}
			}
		}(i)
	}

	// Let metrics stabilize
	time.Sleep(10 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var reportWg sync.WaitGroup
		reportWg.Add(numReportGoroutines)
		for j := 0; j < numReportGoroutines; j++ {
			go func() {
				defer reportWg.Done()
				root.(*scope).reportLoopRun()
			}()
		}
		reportWg.Wait()
	}
	b.StopTimer()

	close(stop)
	wg.Wait()
}
