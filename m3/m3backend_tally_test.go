package metrics

// Copied from code.uber.internal:go-common.git at version 139e3b5b4b4b775ff9ed8abb2a9f31b7bd1aad58

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally"
)

// TestM3Scope tests that scope works as expected
func TestM3Scope(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	tags := map[string]string{"testTag": "TestValue", "testTag2": "TestValue2"}

	m3be, err := NewM3Backend(server.Addr, "testService", nil, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	wg.Add(1)

	tallybackend := m3be.(tally.StatsReporter)
	scope, closer := tally.NewRootScope("honk", tags, tallybackend, shortInterval, tally.DefaultSeparator)
	defer closer.Close()

	timer := scope.Timer("dazzle")
	report := timer.Start()
	time.Sleep(shortInterval)
	report.Stop()

	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])

	emittedTimers := server.Service.getBatches()[0].GetMetrics()
	require.Equal(t, 1, len(emittedTimers))
	require.Equal(t, "honk.dazzle", emittedTimers[0].GetName())
}

// TestM3ScopeCounter tests that scope works as expected
func TestM3ScopeCounter(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	tags := map[string]string{"testTag": "TestValue", "testTag2": "TestValue2"}

	m3be, err := NewM3Backend(server.Addr, "testService", nil, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	wg.Add(1)

	tallybackend := m3be.(tally.StatsReporter)
	scope, closer := tally.NewRootScope("honk", tags, tallybackend, shortInterval, tally.DefaultSeparator)
	defer closer.Close()

	counter := scope.Counter("foobar")
	counter.Inc(42)

	time.Sleep(shortInterval)

	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])

	emittedTimers := server.Service.getBatches()[0].GetMetrics()
	require.Equal(t, 1, len(emittedTimers))
	require.Equal(t, "honk.foobar", emittedTimers[0].GetName())
}

// TestM3ScopeGauge tests that scope works as expected
func TestM3ScopeGauge(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	tags := map[string]string{"testTag": "TestValue", "testTag2": "TestValue2"}

	m3be, err := NewM3Backend(server.Addr, "testService", nil, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	wg.Add(1)

	tallybackend := m3be.(tally.StatsReporter)
	scope, closer := tally.NewRootScope("honk", tags, tallybackend, shortInterval, tally.DefaultSeparator)
	defer closer.Close()

	gauge := scope.Gauge("foobar")
	gauge.Update(42)

	time.Sleep(shortInterval)

	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])

	emittedTimers := server.Service.getBatches()[0].GetMetrics()
	require.Equal(t, 1, len(emittedTimers))
	require.Equal(t, "honk.foobar", emittedTimers[0].GetName())
}

// TestM3CachedScope tests that scope works as expected
func TestM3CachedScope(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	tags := map[string]string{"testTag": "TestValue", "testTag2": "TestValue2"}

	m3be, err := NewM3Backend(server.Addr, "testService", nil, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	wg.Add(1)

	scope, closer := tally.NewCachedRootScope("honk", tags, m3be, shortInterval, tally.DefaultSeparator)
	defer closer.Close()

	timer := scope.Timer("dazzle")
	report := timer.Start()
	time.Sleep(shortInterval)
	report.Stop()

	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])

	emittedTimers := server.Service.getBatches()[0].GetMetrics()
	require.Equal(t, 1, len(emittedTimers))
	require.Equal(t, "honk.dazzle", emittedTimers[0].GetName())
}

// TestM3CachedScopeCounter tests that scope works as expected
func TestM3CachedScopeCounter(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	tags := map[string]string{"testTag": "TestValue", "testTag2": "TestValue2"}

	m3be, err := NewM3Backend(server.Addr, "testService", nil, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	wg.Add(1)

	scope, closer := tally.NewCachedRootScope("honk", tags, m3be, shortInterval, tally.DefaultSeparator)
	defer closer.Close()

	counter := scope.Counter("foobar")
	counter.Inc(42)

	time.Sleep(shortInterval)

	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])

	emittedTimers := server.Service.getBatches()[0].GetMetrics()
	require.Equal(t, 1, len(emittedTimers))
	require.Equal(t, "honk.foobar", emittedTimers[0].GetName())
}

// TestM3CachedScopeGauge tests that scope works as expected
func TestM3CachedScopeGauge(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	tags := map[string]string{"testTag": "TestValue", "testTag2": "TestValue2"}

	m3be, err := NewM3Backend(server.Addr, "testService", nil, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	wg.Add(1)

	scope, closer := tally.NewCachedRootScope("honk", tags, m3be, shortInterval, tally.DefaultSeparator)
	defer closer.Close()

	gauge := scope.Gauge("foobar")
	gauge.Update(42)

	time.Sleep(shortInterval)

	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])

	emittedTimers := server.Service.getBatches()[0].GetMetrics()
	require.Equal(t, 1, len(emittedTimers))
	require.Equal(t, "honk.foobar", emittedTimers[0].GetName())
}

func BenchmarkTallyReportTimer(b *testing.B) {
	backend, err := NewM3Backend(
		"127.0.0.1:4444",
		"my-service",
		nil,
		false,
		10000,
		maxPacketSize,
		500*time.Millisecond,
	)
	if err != nil {
		b.Error(err.Error())
		return
	}

	tallybackend := backend.(tally.StatsReporter)
	scope, closer := tally.NewRootScope(
		"my-service-name.production",
		nil,
		tallybackend,
		1*time.Second,
		tally.DefaultSeparator,
	)

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

func BenchmarkTallyCachedReportTimer(b *testing.B) {
	backend, err := NewM3Backend(
		"127.0.0.1:4444",
		"my-service",
		nil,
		false,
		10000,
		maxPacketSize,
		500*time.Millisecond,
	)
	if err != nil {
		b.Error(err.Error())
		return
	}

	scope, closer := tally.NewCachedRootScope(
		"my-service-name.production",
		nil,
		backend,
		1*time.Second,
		tally.DefaultSeparator,
	)

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
