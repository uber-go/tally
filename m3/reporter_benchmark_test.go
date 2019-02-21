// Copyright (c) 2019 Uber Technologies, Inc.
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
	"testing"
	"time"
)

const (
	updaters = 10
	updates  = 1000
	numIds   = 10

	testID = "stats.$dc.gauges.m3+" +
		"servers.my-internal-server-$dc.network.eth0_tx_colls+" +
		"dc=$dc,domain=production.$zone,env=production,pipe=$pipe,service=servers,type=gauge"
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

func BenchmarkCalulateSize(b *testing.B) {
	r, _ := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: defaultCommonTags,
	})
	defer r.Close()
	benchReporter := r.(*reporter)

	val := int64(123456)
	met := benchReporter.newMetric("foo", nil, counterType)
	met.MetricValue.Count.I64Value = &val

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
		resourcePool := benchReporter.resourcePool
		// Blindly consume metrics
		for met := range benchReporter.metCh {
			resourcePool.releaseShallowMetric(met.m)
		}
	}()

	timer := benchReporter.AllocateTimer("foo", nil)

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		timer.ReportTimer(time.Duration(n) * time.Millisecond)
	}

	b.StopTimer()
}
