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

	"github.com/uber-go/tally"
)

func BenchmarkNewMetric(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: defaultCommonTags,
	})
	defer r.Close()
	benchReporter := r.(*reporter)
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		benchReporter.newMetric("foo", nil, counterType)
	}
}

func BenchmarkEmitMetrics(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: defaultCommonTags,
	})
	defer r.Close()

	benchReporter := r.(*reporter)

	counters := make([]tally.CachedCount, 100)
	for i := range counters {
		counters[i] = r.AllocateCounter(fmt.Sprintf("foo-%v", i), nil /* tags */)
	}

	for n := 0; n < b.N; n++ {
		for _, c := range counters {
			c.ReportCount(1)
		}

		benchReporter.Flush()
	}
}

func BenchmarkCalulateSize(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: defaultCommonTags,
	})
	defer r.Close()
	benchReporter := r.(*reporter)

	met := benchReporter.newMetric("foo", map[string]string{"domain": "foo"}, counterType)
	met.Value.Count = 123456

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		benchReporter.calculateSize(met)
	}
}

func BenchmarkTimer(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:    []string{"127.0.0.1:9052"},
		Service:      "test-service",
		CommonTags:   defaultCommonTags,
		MaxQueueSize: DefaultMaxQueueSize,
	})

	defer r.Close()

	benchReporter := r.(*reporter)

	go func() {
		// Blindly consume metrics
		for range benchReporter.metCh {
			// nop
		}
	}()

	timer := benchReporter.AllocateTimer("foo", nil)

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		timer.ReportTimer(time.Duration(n) * time.Millisecond)
	}

	b.StopTimer()
}
