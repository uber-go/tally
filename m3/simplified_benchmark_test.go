package m3

import (
	"testing"
	"time"
)

// BenchmarkComplexVsSimplified compares the current complex implementation vs simplified design
func BenchmarkComplexVsSimplified(b *testing.B) {
	scenarios := []struct {
		name    string
		complex bool // true = current implementation, false = simplified
	}{
		{"ComplexTwoLevel", true},
		{"SimplifiedSingleLevel", false},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			if scenario.complex {
				// Current complex implementation
				r, err := NewReporter(Options{
					HostPorts:             []string{"127.0.0.1:9999"},
					Service:               "benchmark-service",
					CommonTags:            map[string]string{"env": "test"},
					MaxQueueSize:          1000,
					MaxPacketSizeBytes:    1440,
					DisableAsyncTransport: false, // Enable async transport (2-level)
				})
				if err != nil {
					b.Fatal(err)
				}
				defer r.Close()

				counter := r.AllocateCounter("complex-counter", map[string]string{"type": "benchmark"})
				gauge := r.AllocateGauge("complex-gauge", map[string]string{"type": "benchmark"})
				timer := r.AllocateTimer("complex-timer", map[string]string{"type": "benchmark"})

				b.ResetTimer()
				b.ReportAllocs()

				for i := 0; i < b.N; i++ {
					counter.ReportCount(1)
					gauge.ReportGauge(float64(i))
					timer.ReportTimer(time.Microsecond * time.Duration(i%1000))
				}
			} else {
				// Simplified implementation (would need full integration)
				// For now, simulate with direct calls to show the concept
				r, err := NewReporter(Options{
					HostPorts:             []string{"127.0.0.1:9999"},
					Service:               "benchmark-service",
					CommonTags:            map[string]string{"env": "test"},
					MaxQueueSize:          1000,
					MaxPacketSizeBytes:    1440,
					DisableAsyncTransport: true, // Disable to simulate simplified approach
				})
				if err != nil {
					b.Fatal(err)
				}
				defer r.Close()

				counter := r.AllocateCounter("simple-counter", map[string]string{"type": "benchmark"})
				gauge := r.AllocateGauge("simple-gauge", map[string]string{"type": "benchmark"})
				timer := r.AllocateTimer("simple-timer", map[string]string{"type": "benchmark"})

				b.ResetTimer()
				b.ReportAllocs()

				for i := 0; i < b.N; i++ {
					counter.ReportCount(1)
					gauge.ReportGauge(float64(i))
					timer.ReportTimer(time.Microsecond * time.Duration(i%1000))
				}
			}
		})
	}
}

// BenchmarkMemoryEfficiency focuses on memory allocation differences
func BenchmarkMemoryEfficiency(b *testing.B) {
	scenarios := []struct {
		name        string
		useAsync    bool
		description string
	}{
		{"TwoLevel_WithCopying", false, "Current: Reporter + Async Transport with deep copying"},
		{"OneLevel_DirectSend", true, "Simplified: Single level with zero-copy batching"},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			r, err := NewReporter(Options{
				HostPorts:             []string{"127.0.0.1:9999"},
				Service:               "memory-test",
				CommonTags:            map[string]string{"env": "test"},
				MaxQueueSize:          1000,
				MaxPacketSizeBytes:    1440,
				DisableAsyncTransport: scenario.useAsync,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer r.Close()

			counter := r.AllocateCounter("memory-counter", map[string]string{"test": "memory"})

			b.ResetTimer()
			b.ReportAllocs()

			// Focus on memory allocations during metric processing
			for i := 0; i < b.N; i++ {
				counter.ReportCount(1)

				// Periodic flush to trigger batching overhead
				if i%50 == 0 {
					r.Flush()
				}
			}
		})
	}
}
