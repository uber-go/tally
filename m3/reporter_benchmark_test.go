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
	"strconv"
	"testing"
	"time"

	"github.com/uber-go/tally"
	m3thrift "github.com/uber-go/tally/m3/thrift/v2"
	"github.com/uber-go/tally/thirdparty/github.com/apache/thrift/lib/go/thrift"
)

var (
	// volatileMet is used to ensure benchmarked allocations are not optimized away.
	volatileMet *cachedMetric
	// benchmarkCommonTags are common tags for benchmarks in this file.
	benchmarkCommonTags = map[string]string{"env": "test", "host": "benchmark_host"}
)

func BenchmarkNewMetric(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: benchmarkCommonTags,
		Env:        "test",
	})
	defer r.Close()
	benchReporter := r.(*reporter)

	tags := map[string]string{"testTag": "TestValue"}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		volatileMet = benchReporter.allocateMetric("my-counter", tags, counterType)
	}
}

func BenchmarkEmitMetrics(b *testing.B) {
	r, err := NewReporter(Options{
		HostPorts:    []string{"127.0.0.1:9052"},
		Service:      "test-service",
		CommonTags:   benchmarkCommonTags,
		Env:          "test",
		MaxQueueSize: 1000000, // Keep a large queue
	})
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()

	benchReporter := r.(*reporter)
	cachedMet := benchReporter.allocateMetric("benchmark.metric", nil, counterType)
	if cachedMet.isNoop {
		b.Error("allocateMetric returned a noop metric unexpectedly")
	}

	const maxIterations = 10000 // Proper benchmark size

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < maxIterations; j++ {
			val := int64(j + 1)
			report := pendingReport{
				cached:    cachedMet,
				valueType: counterType,
				countVal:  val,
			}
			benchReporter.metCh <- report
		}
	}
	b.StopTimer()

	benchReporter.Flush()
}

func BenchmarkAccessPrecalculatedSize(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: benchmarkCommonTags,
		Env:        "test",
	})
	defer r.Close()
	benchReporter := r.(*reporter)

	// Allocate a metric to get access to its precalculated size
	cachedMet := benchReporter.allocateMetric("my-counter", map[string]string{"testTag": "TestValue"}, counterType)
	if cachedMet.isNoop {
		b.Fatal("allocateMetric returned a noop metric")
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = cachedMet.size // Access the precalculated size
	}
}

func BenchmarkTimer(b *testing.B) {
	r, err := NewReporter(Options{
		HostPorts:    []string{"127.0.0.1:9052"},
		Service:      "test-service",
		CommonTags:   benchmarkCommonTags,
		Env:          "test",
		MaxQueueSize: 1000000,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()

	benchReporter := r.(*reporter)

	go func() {
		for range benchReporter.metCh {
		}
	}()

	timer := r.AllocateTimer("foo.timer", nil)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		timer.ReportTimer(time.Millisecond)
	}
	b.StopTimer()
}

// BenchmarkMetricAllocation tests the performance of allocateMetric with different tag counts
func BenchmarkMetricAllocation(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: benchmarkCommonTags,
		Env:        "test",
	})
	defer r.Close()
	benchReporter := r.(*reporter)

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
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				volatileMet = benchReporter.allocateMetric("benchmark.counter", tc.tags, counterType)
			}
		})
	}
}

// BenchmarkThriftSerialization compares precomputed header performance vs on-demand serialization
func BenchmarkThriftSerialization(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: benchmarkCommonTags,
		Env:        "test",
	})
	defer r.Close()
	benchReporter := r.(*reporter)

	tags := map[string]string{"testTag": "TestValue", "envTag": "benchmark"}

	b.Run("PrecomputedHeader", func(b *testing.B) {
		// Test using the current precomputed approach
		cachedMet := benchReporter.allocateMetric("benchmark.metric", tags, counterType)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Simulate the current process() logic that reuses precomputed headers
			_ = len(cachedMet.precomputedHeader) // Access precomputed bytes
			_ = cachedMet.size                   // Access precomputed size
		}
	})

	b.Run("OnDemandSerialization", func(b *testing.B) {
		// Simulate what it would be like without precomputation
		internedName := benchReporter.stringInterner.Intern("benchmark.metric")
		canonicalTags := benchReporter.convertTags(tags)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Simulate creating and serializing the metric header each time
			headerMetric := &m3thrift.Metric{
				Name: internedName,
				Tags: canonicalTags,
			}

			memBuf := thrift.NewTMemoryBuffer()
			proto := benchReporter.protocolFactory.GetProtocol(memBuf)
			headerMetric.Write(proto)
			_ = memBuf.Bytes() // Get the serialized bytes
		}
	})
}

// BenchmarkFlushCycle tests the end-to-end performance of the flush cycle
func BenchmarkFlushCycle(b *testing.B) {
	r, err := NewReporter(Options{
		HostPorts:    []string{"127.0.0.1:9052"},
		Service:      "test-service",
		CommonTags:   benchmarkCommonTags,
		Env:          "test",
		MaxQueueSize: 1000000,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()

	benchReporter := r.(*reporter)

	// Pre-allocate metrics with different tag cardinalities
	metrics := make([]*cachedMetric, 100)
	for i := 0; i < 100; i++ {
		tags := map[string]string{
			"metric_id": strconv.Itoa(i),
			"service":   "benchmark",
		}
		metrics[i] = benchReporter.allocateMetric(fmt.Sprintf("benchmark.metric.%d", i), tags, counterType)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Queue metrics for flushing
		for j, metric := range metrics {
			report := pendingReport{
				cached:    metric,
				valueType: counterType,
				countVal:  int64(j + 1),
			}
			select {
			case benchReporter.metCh <- report:
			default:
				// Channel full, skip this metric
			}
		}

		// Trigger flush
		benchReporter.Flush()
	}
}

// BenchmarkDeserializeModifySerialize tests the current process() loop's Thrift handling
func BenchmarkDeserializeModifySerialize(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: benchmarkCommonTags,
		Env:        "test",
	})
	defer r.Close()
	benchReporter := r.(*reporter)

	tags := map[string]string{"testTag": "TestValue"}
	cachedMet := benchReporter.allocateMetric("benchmark.metric", tags, counterType)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Simulate the current process() logic
		finalMetric := &m3thrift.Metric{}

		// Deserialize precomputed header
		hdrMemBuf := thrift.NewTMemoryBuffer()
		hdrMemBuf.Write(cachedMet.precomputedHeader)
		hdrProto := benchReporter.protocolFactory.GetProtocol(hdrMemBuf)
		finalMetric.Read(hdrProto)

		// Add timestamp and value (simulate process() loop)
		nowVal := time.Now().UnixNano()
		finalMetric.Timestamp = nowVal

		metricVal := &m3thrift.MetricValue{
			MetricType: m3thrift.MetricType_COUNTER,
			Count:      int64(i + 1),
		}
		finalMetric.Value = *metricVal

		// Serialize complete metric (what actually gets sent)
		outBuf := thrift.NewTMemoryBuffer()
		outProto := benchReporter.protocolFactory.GetProtocol(outBuf)
		finalMetric.Write(outProto)
		_ = outBuf.Bytes() // Get final serialized metric
	}
}

// BenchmarkStringInterning tests the performance impact of string interning
func BenchmarkStringInterning(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: benchmarkCommonTags,
		Env:        "test",
	})
	defer r.Close()
	benchReporter := r.(*reporter)

	metricNames := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		metricNames[i] = fmt.Sprintf("benchmark.metric.%d", i)
	}

	b.Run("WithInterning", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			name := metricNames[i%len(metricNames)]
			_ = benchReporter.stringInterner.Intern(name)
		}
	})

	b.Run("WithoutInterning", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			name := metricNames[i%len(metricNames)]
			_ = name // Just access the string without interning
		}
	})
}

// BenchmarkTagConversion tests tag processing and caching performance
func BenchmarkTagConversion(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: benchmarkCommonTags,
		Env:        "test",
	})
	defer r.Close()
	benchReporter := r.(*reporter)

	// Create various tag maps to test
	tagMaps := []map[string]string{
		{"tag1": "value1"},
		{"tag1": "value1", "tag2": "value2"},
		{"tag1": "value1", "tag2": "value2", "tag3": "value3", "tag4": "value4"},
	}

	for _, tags := range tagMaps {
		name := fmt.Sprintf("%dTags", len(tags))
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = benchReporter.convertTags(tags) // Test tag conversion with caching
			}
		})
	}
}
