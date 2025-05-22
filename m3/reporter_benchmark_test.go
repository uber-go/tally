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
	"testing"
	"time"

	m3thrift "github.com/uber-go/tally/v4/m3/thrift/v2"
	"github.com/uber-go/tally/v4/thirdparty/github.com/apache/thrift/lib/go/thrift"
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
		internedName := benchReporter.stringInterner.Intern("benchmark.metric")
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

func BenchmarkStringInterning(b *testing.B) {
	metricNames := []string{
		"benchmark.metric.1", "benchmark.metric.2", "benchmark.metric.3",
		"benchmark.metric.4", "benchmark.metric.5", "benchmark.metric.6",
	}

	b.Run("WithInterning", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			name := metricNames[i%len(metricNames)]
			_ = benchReporter.stringInterner.Intern(name)
		}
	})

	b.Run("WithoutInterning", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			name := metricNames[i%len(metricNames)]
			_ = name
		}
	})
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

	// Start consumer to prevent channel blocking
	go func() {
		for range benchReporter.metCh {
			// Consume reports
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		report := pendingReport{
			cached:    cachedMet,
			valueType: counterType,
			countVal:  int64(i + 1),
		}
		benchReporter.metCh <- report
	}
}
