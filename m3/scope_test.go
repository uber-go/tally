// Copyright (c) 2023 Uber Technologies, Inc.
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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tally "github.com/uber-go/tally/v4"
)

var commonTags = map[string]string{"env": "test"}

type doneFn func()

func newTestReporterScope(
	t *testing.T,
	addr string,
	scopePrefix string,
	scopeTags map[string]string,
) (Reporter, tally.Scope, doneFn) {
	r, err := NewReporter(Options{
		HostPorts:          []string{addr},
		Service:            "testService",
		CommonTags:         commonTags,
		IncludeHost:        includeHost,
		MaxQueueSize:       queueSize,
		MaxPacketSizeBytes: maxPacketSize,
	})
	require.NoError(t, err)

	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		Prefix:                 scopePrefix,
		Tags:                   scopeTags,
		CachedReporter:         r,
		OmitCardinalityMetrics: false,
	}, shortInterval)

	return r, scope, func() {
		require.NoError(t, closer.Close())
		require.True(t, r.(*reporter).done.Load())
	}
}

// TestScope tests that scope emits expected metrics.
func TestScope(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact) // countBatches=true, server calls wg.Done() on first batch
	go server.Serve()

	r, err := NewReporter(Options{
		HostPorts:  []string{server.Addr},
		Service:    "test",
		CommonTags: map[string]string{"env": "test"},
	})
	require.NoError(t, err)

	scopeOpts := tally.ScopeOptions{CachedReporter: r}
	scope, closer := tally.NewRootScope(scopeOpts, time.Second)

	wg.Add(1) // Expecting one primary flush from closer.Close()
	scope.Counter("testCounter").Inc(1)
	scope.Gauge("testGauge").Update(1)
	scope.Timer("testTimer").Record(time.Millisecond)

	require.NoError(t, closer.Close())
	wg.Wait() // Wait for the batch from closer.Close() to be processed
	server.Close()
	r.Close()

	allMetrics := server.Service.getMetrics()
	var foundCounter, foundGauge, foundTimer bool
	for _, m := range allMetrics {
		switch m.GetName() {
		case "testCounter":
			foundCounter = true
		case "testGauge":
			foundGauge = true
		case "testTimer":
			foundTimer = true
		}
	}
	assert.True(t, foundCounter, "testCounter not found")
	assert.True(t, foundGauge, "testGauge not found")
	assert.True(t, foundTimer, "testTimer not found")
}

// TestScopeCounter tests counter through scope.
func TestScopeCounter(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()

	r, err := NewReporter(Options{
		HostPorts:  []string{server.Addr},
		Service:    "test",
		CommonTags: map[string]string{"env": "test"},
	})
	require.NoError(t, err)

	scopeOpts := tally.ScopeOptions{CachedReporter: r, Prefix: "honk"}
	scope, closer := tally.NewRootScope(scopeOpts, time.Second)

	wg.Add(1)
	scope.Counter("foobar").Inc(42)

	require.NoError(t, closer.Close())
	wg.Wait()
	server.Close()
	r.Close()

	allMetrics := server.Service.getMetrics()
	var found bool
	for _, m := range allMetrics {
		if m.GetName() == "honk.foobar" {
			assert.EqualValues(t, 42, m.Value.GetCount())
			found = true
			break
		}
	}
	assert.True(t, found, "Metric honk.foobar not found")
}

// TestScopeGauge tests gauge through scope.
func TestScopeGauge(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()

	r, err := NewReporter(Options{
		HostPorts:  []string{server.Addr},
		Service:    "test",
		CommonTags: map[string]string{"env": "test"},
	})
	require.NoError(t, err)

	scopeOpts := tally.ScopeOptions{CachedReporter: r, Prefix: "honk"}
	scope, closer := tally.NewRootScope(scopeOpts, time.Second)

	wg.Add(1)
	scope.Gauge("foobaz").Update(42)

	require.NoError(t, closer.Close())
	wg.Wait()
	server.Close()
	r.Close()

	allMetrics := server.Service.getMetrics()
	var found bool
	for _, m := range allMetrics {
		if m.GetName() == "honk.foobaz" {
			assert.EqualValues(t, float64(42), m.Value.GetGauge())
			found = true
			break
		}
	}
	assert.True(t, found, "Metric honk.foobaz not found")
}

func BenchmarkScopeReportTimer(b *testing.B) {
	backend, err := NewReporter(Options{
		HostPorts:          []string{"127.0.0.1:4444"},
		Service:            "my-service",
		MaxQueueSize:       10000,
		MaxPacketSizeBytes: maxPacketSize,
		Env:                "test",
	})
	if err != nil {
		b.Error(err.Error())
		return
	}

	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		Prefix:         "bench",
		CachedReporter: backend,
	}, 1*time.Second)

	perEndpointScope := scope.Tagged(
		map[string]string{
			"endpointid": "health",
			"handlerid":  "health",
		},
	)
	timer := perEndpointScope.Timer("inbound.latency")
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			timer.Record(500)
		}
	})

	b.StopTimer()
	closer.Close()
	b.StartTimer()
}

func BenchmarkScopeReportHistogram(b *testing.B) {
	backend, err := NewReporter(Options{
		HostPorts:          []string{"127.0.0.1:4444"},
		Service:            "my-service",
		MaxQueueSize:       10000,
		MaxPacketSizeBytes: maxPacketSize,
		Env:                "test",
	})
	if err != nil {
		b.Error(err.Error())
		return
	}

	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		Prefix:         "bench",
		CachedReporter: backend,
	}, 1*time.Second)

	perEndpointScope := scope.Tagged(
		map[string]string{
			"endpointid": "health",
			"handlerid":  "health",
		},
	)
	buckets := tally.DurationBuckets{
		0 * time.Millisecond,
		10 * time.Millisecond,
		25 * time.Millisecond,
		50 * time.Millisecond,
		75 * time.Millisecond,
		100 * time.Millisecond,
		200 * time.Millisecond,
		300 * time.Millisecond,
		400 * time.Millisecond,
		500 * time.Millisecond,
		600 * time.Millisecond,
		800 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		5 * time.Second,
	}

	histogram := perEndpointScope.Histogram("inbound.latency", buckets)
	b.ReportAllocs()
	b.ResetTimer()

	bucketsLen := len(buckets)
	for i := 0; i < b.N; i++ {
		histogram.RecordDuration(buckets[i%bucketsLen])
	}

	b.StopTimer()
	closer.Close()
}
