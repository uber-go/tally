package m3

import (
	"testing"
	"time"

	"github.com/uber-go/tally/v6"
)

// BenchmarkAsyncVsSyncTransport compares performance of async vs sync transport
func BenchmarkAsyncVsSyncTransport(b *testing.B) {
	scenarios := []struct {
		name         string
		asyncEnabled bool
	}{
		{"AsyncTransport", false}, // false = DisableAsyncTransport: false (async enabled)
		{"SyncTransport", true},   // true = DisableAsyncTransport: true (sync/blocking)
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			// Use localhost - network errors are expected and OK for this benchmark
			// We're measuring internal processing overhead, not network transmission success
			r, err := NewReporter(Options{
				HostPorts:             []string{"127.0.0.1:9999"}, // Non-existent port is fine for this test
				Service:               "benchmark-service",
				CommonTags:            map[string]string{"env": "test"},
				MaxQueueSize:          1000,
				MaxPacketSizeBytes:    1440,
				DisableAsyncTransport: scenario.asyncEnabled,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer r.Close()

			// Pre-allocate metrics to avoid allocation overhead in benchmark
			counter := r.AllocateCounter("test-counter", map[string]string{"type": "benchmark"})
			gauge := r.AllocateGauge("test-gauge", map[string]string{"type": "benchmark"})
			timer := r.AllocateTimer("test-timer", map[string]string{"type": "benchmark"})

			b.ResetTimer()
			b.ReportAllocs()

			// Benchmark metric reporting (network transmission will fail, but that's OK)
			for i := 0; i < b.N; i++ {
				counter.ReportCount(1)
				gauge.ReportGauge(float64(i))
				timer.ReportTimer(time.Microsecond * time.Duration(i%1000))
			}
		})
	}
}

// BenchmarkTransportAllocation compares allocation overhead between async and sync transport
func BenchmarkTransportAllocation(b *testing.B) {
	scenarios := []struct {
		name         string
		asyncEnabled bool
	}{
		{"AsyncAllocation", false},
		{"SyncAllocation", true},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			r, err := NewReporter(Options{
				HostPorts:             []string{"127.0.0.1:9999"},
				Service:               "benchmark-service",
				CommonTags:            map[string]string{"env": "test"},
				MaxQueueSize:          1000,
				MaxPacketSizeBytes:    1440,
				DisableAsyncTransport: scenario.asyncEnabled,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer r.Close()

			b.ResetTimer()
			b.ReportAllocs()

			// Benchmark metric allocation and reporting
			for i := 0; i < b.N; i++ {
				counter := r.AllocateCounter("alloc-counter", map[string]string{"idx": "test"})
				counter.ReportCount(1)
			}
		})
	}
}

// BenchmarkTransportBatchFlush compares flush behavior between async and sync transport
func BenchmarkTransportBatchFlush(b *testing.B) {
	scenarios := []struct {
		name         string
		asyncEnabled bool
	}{
		{"AsyncFlush", false},
		{"SyncFlush", true},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			r, err := NewReporter(Options{
				HostPorts:             []string{"127.0.0.1:9999"},
				Service:               "benchmark-service",
				CommonTags:            map[string]string{"env": "test"},
				MaxQueueSize:          2000,
				MaxPacketSizeBytes:    1440,
				DisableAsyncTransport: scenario.asyncEnabled,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer r.Close()

			// Pre-allocate metrics
			counter := r.AllocateCounter("flush-counter", map[string]string{"test": "flush"})

			b.ResetTimer()
			b.ReportAllocs()

			// Benchmark batch reporting with periodic flushes
			for i := 0; i < b.N; i++ {
				counter.ReportCount(1)

				// Force flush every 10 operations to trigger batching behavior
				if i%10 == 0 {
					r.Flush()
				}
			}
		})
	}
}

// BenchmarkTransportThroughput measures sustained throughput differences
func BenchmarkTransportThroughput(b *testing.B) {
	scenarios := []struct {
		name         string
		asyncEnabled bool
	}{
		{"AsyncThroughput", false},
		{"SyncThroughput", true},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			r, err := NewReporter(Options{
				HostPorts:             []string{"127.0.0.1:9999"},
				Service:               "benchmark-service",
				CommonTags:            map[string]string{"env": "test"},
				MaxQueueSize:          5000,
				MaxPacketSizeBytes:    1440,
				DisableAsyncTransport: scenario.asyncEnabled,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer r.Close()

			// Create multiple metric types to simulate realistic load
			counters := make([]tally.CachedCount, 10)
			gauges := make([]tally.CachedGauge, 10)
			timers := make([]tally.CachedTimer, 10)

			for i := 0; i < 10; i++ {
				counters[i] = r.AllocateCounter("throughput-counter", map[string]string{"shard": string(rune('0' + i))})
				gauges[i] = r.AllocateGauge("throughput-gauge", map[string]string{"shard": string(rune('0' + i))})
				timers[i] = r.AllocateTimer("throughput-timer", map[string]string{"shard": string(rune('0' + i))})
			}

			b.ResetTimer()
			b.ReportAllocs()

			// Simulate sustained load
			for i := 0; i < b.N; i++ {
				shard := i % 10
				counters[shard].ReportCount(int64(i))
				gauges[shard].ReportGauge(float64(i * 100))
				timers[shard].ReportTimer(time.Millisecond * time.Duration(i%100))

				// Periodic flush to simulate realistic batching
				if i%100 == 0 {
					r.Flush()
				}
			}
		})
	}
}

// BenchmarkBatchedVsIndividualReports compares batched vs individual metric transmission
func BenchmarkBatchedVsIndividualReports(b *testing.B) {
	scenarios := []struct {
		name         string
		batchMode    bool
		asyncEnabled bool
	}{
		{"BatchedAsync", true, false},
		{"BatchedSync", true, true},
		{"IndividualAsync", false, false},
		{"IndividualSync", false, true},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			r, err := NewReporter(Options{
				HostPorts:             []string{"127.0.0.1:9999"},
				Service:               "benchmark-service",
				CommonTags:            map[string]string{"env": "test"},
				MaxQueueSize:          1000,
				MaxPacketSizeBytes:    1440,
				DisableAsyncTransport: scenario.asyncEnabled,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer r.Close()

			counter := r.AllocateCounter("report-counter", map[string]string{"mode": scenario.name})

			b.ResetTimer()
			b.ReportAllocs()

			if scenario.batchMode {
				// Batched mode: accumulate multiple metrics before flushing
				for i := 0; i < b.N; i++ {
					counter.ReportCount(1)
					// Only flush every 50 operations to create batches
					if i%50 == 0 {
						r.Flush()
					}
				}
				// Final flush for remaining metrics
				r.Flush()
			} else {
				// Individual mode: flush after every metric
				for i := 0; i < b.N; i++ {
					counter.ReportCount(1)
					r.Flush() // Force individual transmission
				}
			}
		})
	}
}

// BenchmarkLowFrequencyUsage simulates real low-frequency application patterns
func BenchmarkLowFrequencyUsage(b *testing.B) {
	scenarios := []struct {
		name         string
		asyncEnabled bool
	}{
		{"LowFreqAsync", false},
		{"LowFreqSync", true},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			r, err := NewReporter(Options{
				HostPorts:             []string{"127.0.0.1:9999"},
				Service:               "benchmark-service",
				CommonTags:            map[string]string{"env": "test"},
				MaxQueueSize:          100,
				MaxPacketSizeBytes:    1440,
				DisableAsyncTransport: scenario.asyncEnabled,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer r.Close()

			// Simulate different metric types that would be used in low-frequency apps
			jobCounter := r.AllocateCounter("job.completed", map[string]string{"type": "batch"})
			errorCounter := r.AllocateCounter("job.errors", map[string]string{"type": "critical"})
			durationTimer := r.AllocateTimer("job.duration", map[string]string{"type": "performance"})

			b.ResetTimer()
			b.ReportAllocs()

			// Simulate a low-frequency job reporting pattern
			for i := 0; i < b.N; i++ {
				jobCounter.ReportCount(1)

				// Occasionally report errors (10% of the time)
				if i%10 == 0 {
					errorCounter.ReportCount(1)
				}

				// Always report duration
				durationTimer.ReportTimer(time.Duration(i%1000) * time.Millisecond)

				// Immediate flush - simulating low-frequency job completion
				r.Flush()
			}
		})
	}
}
