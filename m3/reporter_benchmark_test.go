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

package m3

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/uber-go/tally/v6/internal/cache"
	m3thrift "github.com/uber-go/tally/v6/m3/thrift/v2"
	"github.com/uber-go/tally/v6/thirdparty/github.com/apache/thrift/lib/go/thrift"
)

// Common test data
var (
	benchReporter *reporter
	testTags      = map[string]string{"env": "test", "service": "benchmark"}
)

func init() {
	r, _ := NewReporter(Options{
		HostPorts:    []string{"127.0.0.1:9052"},
		Service:      "test-service",
		CommonTags:   testTags,
		Env:          "test",
		MaxQueueSize: 100000,
	})
	benchReporter = r.(*reporter)
}

func BenchmarkMetricAllocation(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = benchReporter.allocateMetric("benchmark.counter", testTags, counterType)
	}
}

func BenchmarkMetricAllocationWithTags(b *testing.B) {
	testCases := []struct {
		name string
		tags map[string]string
	}{
		{"NoTags", nil},
		{"FewTags", map[string]string{"tag1": "value1", "tag2": "value2"}},
		{"ManyTags", map[string]string{
			"tag1": "value1", "tag2": "value2", "tag3": "value3", "tag4": "value4",
			"tag5": "value5", "tag6": "value6", "tag7": "value7", "tag8": "value8",
		}},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = benchReporter.allocateMetric("benchmark.counter", tc.tags, counterType)
			}
		})
	}
}

func BenchmarkPrecomputedHeaderAccess(b *testing.B) {
	cachedMet := benchReporter.allocateMetric("benchmark.metric", testTags, counterType)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = len(cachedMet.precomputedHeader)
		_ = cachedMet.size
	}
}

func BenchmarkThriftSerialization(b *testing.B) {
	b.Run("PrecomputedHeader", func(b *testing.B) {
		cachedMet := benchReporter.allocateMetric("benchmark.metric", testTags, counterType)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = len(cachedMet.precomputedHeader)
			_ = cachedMet.size
		}
	})

	b.Run("OnDemandSerialization", func(b *testing.B) {
		internedName := "benchmark.metric"
		canonicalTags := benchReporter.convertTags(testTags)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			headerMetric := &m3thrift.Metric{
				Name: internedName,
				Tags: canonicalTags,
			}

			memBuf := thrift.NewTMemoryBuffer()
			proto := benchReporter.protocolFactory.GetProtocol(memBuf)
			headerMetric.Write(proto)
			_ = memBuf.Bytes()
		}
	})
}

func BenchmarkTimerReporting(b *testing.B) {
	timer := benchReporter.AllocateTimer("benchmark.timer", testTags)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		timer.ReportTimer(time.Millisecond)
	}
}

func BenchmarkFullSerializationCycle(b *testing.B) {
	cachedMet := benchReporter.allocateMetric("benchmark.metric", testTags, counterType)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate the full process() cycle
		finalMetric := &m3thrift.Metric{}

		// Deserialize precomputed header
		hdrMemBuf := thrift.NewTMemoryBuffer()
		hdrMemBuf.Write(cachedMet.precomputedHeader)
		hdrProto := benchReporter.protocolFactory.GetProtocol(hdrMemBuf)
		finalMetric.Read(hdrProto)

		// Add timestamp and value
		finalMetric.Timestamp = time.Now().UnixNano()
		finalMetric.Value = m3thrift.MetricValue{
			MetricType: m3thrift.MetricType_COUNTER,
			Count:      int64(i + 1),
		}

		// Serialize complete metric
		outBuf := thrift.NewTMemoryBuffer()
		outProto := benchReporter.protocolFactory.GetProtocol(outBuf)
		finalMetric.Write(outProto)
		_ = outBuf.Bytes()
	}
}

func BenchmarkTagConversion(b *testing.B) {
	tagMaps := []map[string]string{
		{"tag1": "value1"},
		{"tag1": "value1", "tag2": "value2"},
		{"tag1": "value1", "tag2": "value2", "tag3": "value3", "tag4": "value4"},
	}

	for _, tags := range tagMaps {
		b.Run(fmt.Sprintf("%dTags", len(tags)), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = benchReporter.convertTags(tags)
			}
		})
	}
}

func BenchmarkMetricEmission(b *testing.B) {
	cachedMet := benchReporter.allocateMetric("benchmark.metric", testTags, counterType)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Use the simplified batching approach directly
		cachedMet.ReportCount(int64(i + 1))
	}
}

// BenchmarkConvertTagsOptimizations tests the performance improvements of the optimized convertTags
func BenchmarkConvertTagsOptimizations(b *testing.B) {
	// Use the existing benchReporter instead of creating a new one
	r := benchReporter

	// Test cases for different tag scenarios
	testCases := []struct {
		name string
		tags map[string]string
	}{
		{
			name: "NoTags",
			tags: map[string]string{},
		},
		{
			name: "SingleTag",
			tags: map[string]string{"service": "test"},
		},
		{
			name: "TwoTags",
			tags: map[string]string{"service": "test", "env": "prod"},
		},
		{
			name: "ThreeTags",
			tags: map[string]string{"service": "test", "env": "prod", "region": "us-west-2"},
		},
		{
			name: "FiveTags",
			tags: map[string]string{
				"service": "test", "env": "prod", "region": "us-west-2",
				"instance": "i-12345", "version": "1.0.0",
			},
		},
		{
			name: "TenTags",
			tags: map[string]string{
				"service": "test", "env": "prod", "region": "us-west-2",
				"instance": "i-12345", "version": "1.0.0", "team": "platform",
				"component": "api", "datacenter": "us-west-2a", "cluster": "main",
				"deployment": "blue",
			},
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Clear cache before each test
			r.tagCache = cache.NewTagCache()

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				result := r.convertTags(tc.tags)
				_ = result // Prevent optimization
			}
		})

		// Test cache hit performance
		b.Run(tc.name+"_Cached", func(b *testing.B) {
			// Warm up cache
			r.convertTags(tc.tags)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				result := r.convertTags(tc.tags)
				_ = result // Prevent optimization
			}
		})
	}
}

// BenchmarkGCPressure measures GC pressure under high load
func BenchmarkGCPressure(b *testing.B) {
	r := benchReporter

	// Mix of different tag patterns that are common in practice
	tagVariations := []map[string]string{
		{"service": "api"},
		{"service": "api", "env": "prod"},
		{"service": "api", "env": "prod", "region": "us-west"},
		{"service": "db", "env": "staging"},
		{"service": "cache", "env": "prod", "instance": "primary"},
	}

	b.Run("HighThroughput", func(b *testing.B) {
		// Measure GC stats
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			// Simulate high-throughput conversion with realistic tag patterns
			for j := 0; j < 100; j++ {
				tags := tagVariations[j%len(tagVariations)]
				result := r.convertTags(tags)
				_ = result
			}
		}

		b.StopTimer()
		runtime.GC()
		runtime.ReadMemStats(&after)

		// Report GC statistics
		gcCycles := after.NumGC - before.NumGC
		allocRate := float64(after.TotalAlloc-before.TotalAlloc) / float64(b.N) / 100.0

		b.ReportMetric(float64(gcCycles), "gc-cycles")
		b.ReportMetric(allocRate, "allocs-per-op")
	})
}

// BenchmarkTagInterningOptimized tests string interning efficiency with new optimizations
func BenchmarkTagInterningOptimized(b *testing.B) {
	r := benchReporter

	// Common strings that should be interned
	commonKeys := []string{"service", "env", "region", "instance", "version"}
	commonValues := []string{"api", "prod", "us-west-2", "primary", "1.0.0"}

	b.Run("RepeatedStrings", func(b *testing.B) {
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			// Create tags with repeated string values
			tags := map[string]string{
				commonKeys[i%len(commonKeys)]:     commonValues[i%len(commonValues)],
				commonKeys[(i+1)%len(commonKeys)]: commonValues[(i+1)%len(commonValues)],
			}
			result := r.convertTags(tags)
			_ = result
		}
	})
}

// BenchmarkMemoryFootprint measures memory usage patterns
func BenchmarkMemoryFootprint(b *testing.B) {
	r := benchReporter

	b.Run("LargeTagSets", func(b *testing.B) {
		// Create large tag sets to test worst-case scenarios
		largeTags := make(map[string]string)
		for i := 0; i < 20; i++ {
			largeTags[fmt.Sprintf("key_%d", i)] = fmt.Sprintf("value_%d", i)
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			result := r.convertTags(largeTags)
			_ = result
		}
	})

	b.Run("ManySmallTagSets", func(b *testing.B) {
		// Test many small tag sets (more realistic scenario)
		smallTagSets := make([]map[string]string, 100)
		for i := 0; i < 100; i++ {
			smallTagSets[i] = map[string]string{
				"service":  fmt.Sprintf("svc_%d", i%10),
				"instance": fmt.Sprintf("inst_%d", i%20),
			}
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			for j := 0; j < 10; j++ {
				result := r.convertTags(smallTagSets[(i+j)%100])
				_ = result
			}
		}
	})
}
