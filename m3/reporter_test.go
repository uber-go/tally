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
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tally "github.com/uber-go/tally/v6"
	customtransport "github.com/uber-go/tally/v6/m3/customtransports"
	m3thrift "github.com/uber-go/tally/v6/m3/thrift/v2"
	"github.com/uber-go/tally/v6/m3/thriftudp"
	"github.com/uber-go/tally/v6/thirdparty/github.com/apache/thrift/lib/go/thrift"
)

const (
	queueSize     = 1000
	includeHost   = true
	maxPacketSize = int32(1440)
	shortInterval = 10 * time.Millisecond
)

var (
	localListenAddr   = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	defaultCommonTags = map[string]string{"env": "test", "host": "test"}
)

var protocols = []Protocol{Compact, Binary}

const internalMetrics = 1    // Additional metrics the reporter sends in a batch - use this, not a magic number.
const cardinalityMetrics = 4 // Additional metrics emitted by the scope registry.

// findCounterAndTimerBatchesInSlice is a helper for TestReporter.
// It iterates through a slice of batches and returns pointers to the first batch containing
// "my-counter" and the first batch containing "my-timer".
// It allows these to be the same batch if both metrics are found within it.
func findCounterAndTimerBatchesInSlice(batches []m3thrift.MetricBatch) (counterB *m3thrift.MetricBatch, timerB *m3thrift.MetricBatch) {
	for i := range batches { // Iterate by index to get an addressable element
		currentBatch := &batches[i]
		for _, metric := range currentBatch.GetMetrics() {
			if metric.GetName() == "my-counter" {
				if counterB == nil { // Take the first one found
					counterB = currentBatch
				}
			}
			if metric.GetName() == "my-timer" {
				if timerB == nil { // Take the first one found
					timerB = currentBatch
				}
			}
		}
		// Optimization: if both found (possibly in same or different batches), no need to scan further for *first* occurrences.
		if counterB != nil && timerB != nil {
			break
		}
	}
	return
}

// TestReporter tests the reporter works as expected with both compact and binary protocols
func TestReporter(t *testing.T) {
	for _, protocol := range protocols {
		server := newFakeM3Server(t, nil, false, protocol)
		go server.Serve()
		defer server.Close()

		r, err := NewReporter(Options{
			HostPorts:          []string{server.Addr},
			Service:            "test-service",
			CommonTags:         map[string]string{"env": "development", "host": hostname(), "commonTag": "common", "commonTag2": "tag", "commonTag3": "val"},
			IncludeHost:        includeHost,
			Protocol:           protocol,
			MaxQueueSize:       queueSize,
			MaxPacketSizeBytes: maxPacketSize,
		})
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, r.Close())
		}()

		tags := map[string]string{"testTag": "TestValue", "testTag2": "TestValue2"}

		r.AllocateCounter("my-counter", tags).ReportCount(10)
		r.Flush()

		r.AllocateTimer("my-timer", tags).ReportTimer(5 * time.Millisecond)
		r.Flush()

		var counterBatchFound, timerBatchFound *m3thrift.MetricBatch

		for i := 0; i < 200; i++ { // Poll for up to 2 seconds
			allBatches := server.Service.getBatches()
			cb, tb := findCounterAndTimerBatchesInSlice(allBatches)

			if cb != nil {
				counterBatchFound = cb
			}
			if tb != nil {
				timerBatchFound = tb
			}

			if counterBatchFound != nil && timerBatchFound != nil && counterBatchFound != timerBatchFound {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		require.NotNil(t, counterBatchFound, "Counter batch not found after polling")
		require.NotNil(t, timerBatchFound, "Timer batch not found after polling")
		require.NotEqual(t, counterBatchFound, timerBatchFound, "Counter and Timer should be in different batch objects due to separate flushes")

		userMetricBatchesToValidate := []m3thrift.MetricBatch{*counterBatchFound, *timerBatchFound}

		var validatedCounter, validatedTimer bool
		// commonTagsMap used in validation refers to the map in NewReporter Options.
		// tagsMap used in validation refers to the `tags` map defined earlier in this TestReporter scope.
		commonTagsMap := map[string]string{"env": "development", "host": hostname(), "commonTag": "common", "commonTag2": "tag", "commonTag3": "val"}
		tagsMap := tags // alias for clarity in loop below, or use `tags` directly

		for _, currentBatchForValidation := range userMetricBatchesToValidate {
			require.NotNil(t, currentBatchForValidation)
			require.True(t, currentBatchForValidation.IsSetCommonTags())
			require.Equal(t, len(commonTagsMap)+1, len(currentBatchForValidation.GetCommonTags()))
			for _, tag := range currentBatchForValidation.GetCommonTags() {
				if tag.GetName() == ServiceTag {
					require.Equal(t, "test-service", tag.GetValue())
				} else {
					require.Equal(t, commonTagsMap[tag.GetName()], tag.GetValue())
				}
			}

			metricsInThisBatch := currentBatchForValidation.GetMetrics()
			require.True(t, len(metricsInThisBatch) >= 1)

			for _, emittedMetric := range metricsInThisBatch {
				if emittedMetric.GetName() == "my-counter" {
					require.True(t, emittedMetric.IsSetTags())
					require.Equal(t, len(tagsMap), len(emittedMetric.GetTags()))
					for _, tag := range emittedMetric.GetTags() {
						require.Equal(t, tagsMap[tag.GetName()], tag.GetValue())
					}
					require.True(t, emittedMetric.IsSetValue())
					emittedVal := emittedMetric.GetValue()
					emittedCount := emittedVal.GetCount()
					require.EqualValues(t, int64(10), emittedCount)
					validatedCounter = true
				} else if emittedMetric.GetName() == "my-timer" {
					require.True(t, emittedMetric.IsSetValue())
					emittedVal := emittedMetric.GetValue()
					emittedTimerVal := emittedVal.GetTimer()
					require.EqualValues(t, int64(5*1000*1000), emittedTimerVal)
					validatedTimer = true
				}
			}
		}
		require.True(t, validatedCounter, "Counter metric was not validated")
		require.True(t, validatedTimer, "Timer metric was not validated")
	}
}

// TestMultiReporter tests the multi Reporter works as expected
func TestMultiReporter(t *testing.T) {
	dests := []string{"127.0.0.1:9052", "127.0.0.1:9053"}
	commonTags := map[string]string{
		"env":        "test",
		"host":       "test",
		"commonTag":  "common",
		"commonTag2": "tag",
		"commonTag3": "val",
	}
	r, err := NewReporter(Options{
		HostPorts:  dests,
		Service:    "test-service",
		CommonTags: commonTags,
	})
	require.NoError(t, err)
	defer r.Close()

	reporter, ok := r.(*reporter)
	require.True(t, ok)
	multitransport, ok := reporter.client.Transport.(*thriftudp.TMultiUDPTransport)
	require.NotNil(t, multitransport)
	require.True(t, ok)
}

// TestNewReporterErrors tests for Reporter creation errors
func TestNewReporterErrors(t *testing.T) {
	var err error
	// Test freeBytes (maxPacketSizeBytes - numOverheadBytes) is negative
	_, err = NewReporter(Options{
		HostPorts:          []string{"127.0.0.1"},
		Service:            "test-service",
		MaxQueueSize:       10,
		MaxPacketSizeBytes: 2 << 5,
	})
	assert.Error(t, err)
	// Test invalid addr
	_, err = NewReporter(Options{
		HostPorts: []string{"fakeAddress"},
		Service:   "test-service",
	})
	assert.Error(t, err)
}

// TestReporterRaceCondition checks if therem is race condition between reporter closing
// and metric reporting, when run with race detector on, this test should pass
func TestReporterRaceCondition(t *testing.T) {
	r, err := NewReporter(Options{
		HostPorts:          []string{"localhost:8888"},
		Service:            "test-service",
		CommonTags:         defaultCommonTags,
		MaxQueueSize:       queueSize,
		MaxPacketSizeBytes: maxPacketSize,
	})
	require.NoError(t, err)

	go func() {
		r.AllocateTimer("my-timer", nil).ReportTimer(10 * time.Millisecond)
	}()
	r.Close()
}

// TestReporterFinalFlush ensures the Reporter emits the last batch of metrics
// after close
func TestReporterFinalFlush(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	r, err := NewReporter(Options{
		HostPorts:          []string{server.Addr},
		Service:            "test-service",
		CommonTags:         defaultCommonTags,
		MaxQueueSize:       queueSize,
		MaxPacketSizeBytes: maxPacketSize,
	})
	require.NoError(t, err)

	wg.Add(1)

	r.AllocateTimer("my-timer", nil).ReportTimer(10 * time.Millisecond)
	r.Close()

	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])
	require.Equal(t, 1, len(server.Service.getBatches()[0].GetMetrics()))
}

// TestReporterNoPanicOnTimerAfterClose ensure the reporter avoids panic
// after close of the reporter when emitting a timer value
func TestReporterNoPanicOnTimerAfterClose(t *testing.T) {
	server := newFakeM3Server(t, &sync.WaitGroup{}, true, Compact)
	go server.Serve()
	defer server.Close()

	r, err := NewReporter(Options{
		HostPorts:          []string{server.Addr},
		Service:            "test-service",
		CommonTags:         defaultCommonTags,
		MaxQueueSize:       queueSize,
		MaxPacketSizeBytes: maxPacketSize,
	})
	require.NoError(t, err)

	timer := r.AllocateTimer("my-timer", nil)
	r.Close()

	assert.NotPanics(t, func() {
		timer.ReportTimer(time.Millisecond)
	})
}

// TestReporterNoPanicOnFlushAfterClose ensure the reporter avoids panic
// after close of the reporter when calling flush
func TestReporterNoPanicOnFlushAfterClose(t *testing.T) {
	server := newFakeM3Server(t, &sync.WaitGroup{}, true, Compact)
	go server.Serve()
	defer server.Close()

	r, err := NewReporter(Options{
		HostPorts:          []string{server.Addr},
		Service:            "test-service",
		CommonTags:         defaultCommonTags,
		MaxQueueSize:       queueSize,
		MaxPacketSizeBytes: maxPacketSize,
	})
	require.NoError(t, err)
	r.Close()

	assert.NotPanics(t, func() {
		r.Flush()
	})
}

func TestReporterHistogram(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	r, err := NewReporter(Options{
		HostPorts:          []string{server.Addr},
		Service:            "test-service",
		CommonTags:         defaultCommonTags,
		MaxQueueSize:       queueSize,
		MaxPacketSizeBytes: maxPacketSize,
	})
	require.NoError(t, err)

	wg.Add(1)

	h := r.AllocateHistogram("my-histogram", map[string]string{
		"foo": "bar",
	}, tally.DurationBuckets{
		0 * time.Millisecond,
		25 * time.Millisecond,
		50 * time.Millisecond,
		75 * time.Millisecond,
		100 * time.Millisecond,
	})
	b := h.DurationBucket(0*time.Millisecond, 25*time.Millisecond)
	b.ReportSamples(7)
	b = h.DurationBucket(50*time.Millisecond, 75*time.Millisecond)
	b.ReportSamples(3)
	r.Close()

	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])
	require.Equal(t, 2, len(server.Service.getBatches()[0].GetMetrics()))

	// Verify first bucket
	counter := server.Service.getBatches()[0].GetMetrics()[0]
	require.Equal(t, "my-histogram", counter.GetName())
	require.True(t, counter.IsSetTags())
	for _, tag := range counter.GetTags() {
		require.Equal(t, map[string]string{
			"foo":      "bar",
			"bucketid": "0001",
			"bucket":   "0-25ms",
		}[tag.GetName()], tag.GetValue())
	}
	require.Equal(t, 3, len(counter.GetTags()))
	require.True(t, counter.IsSetValue())
	val := counter.GetValue()
	count := val.GetCount()
	require.Equal(t, int64(7), count)

	// Verify second bucket
	counter = server.Service.getBatches()[0].GetMetrics()[1]
	require.Equal(t, "my-histogram", counter.GetName())
	require.True(t, counter.IsSetTags())
	require.Equal(t, 3, len(counter.GetTags()))
	for _, tag := range counter.GetTags() {
		require.Equal(t, map[string]string{
			"foo":      "bar",
			"bucketid": "0003",
			"bucket":   "50ms-75ms",
		}[tag.GetName()], tag.GetValue())
	}
	require.True(t, counter.IsSetValue())
	val = counter.GetValue()
	count = val.GetCount()
	require.Equal(t, int64(3), count)
}

func TestBatchSizes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping TestBatchSizes in short mode")
	}

	server := newFakeM3Server(t, nil, false, Compact)
	go server.Serve()
	defer server.Close()

	commonTags := map[string]string{
		"env":    "test",
		"domain": "pod" + strconv.Itoa(rand.Intn(100)),
	}
	maxPacketSize := int32(1440)
	r, err := NewReporter(Options{
		HostPorts:          []string{server.Addr},
		Service:            "test-service",
		CommonTags:         commonTags,
		MaxQueueSize:       10000,
		MaxPacketSizeBytes: maxPacketSize,
	})

	require.NoError(t, err)
	rand.Seed(time.Now().UnixNano())

	var stop uint32
	go func() {
		var (
			counters = make(map[string]tally.CachedCount)
			gauges   = make(map[string]tally.CachedGauge)
			timers   = make(map[string]tally.CachedTimer)
			randTags = func() map[string]string {
				return map[string]string{
					"t1": "val" + strconv.Itoa(rand.Intn(10000)),
				}
			}
		)
		for atomic.LoadUint32(&stop) == 0 {
			metTypeRand := rand.Intn(9)
			name := "size.test.metric.name" + strconv.Itoa(rand.Intn(50))

			if metTypeRand <= 2 {
				_, ok := counters[name]
				if !ok {
					counters[name] = r.AllocateCounter(name, randTags())
				}
				counters[name].ReportCount(rand.Int63n(10000))
			} else if metTypeRand <= 5 {
				_, ok := gauges[name]
				if !ok {
					gauges[name] = r.AllocateGauge(name, randTags())
				}
				gauges[name].ReportGauge(rand.Float64() * 10000)
			} else {
				_, ok := timers[name]
				if !ok {
					timers[name] = r.AllocateTimer(name, randTags())
				}
				timers[name].ReportTimer(time.Duration(rand.Int63n(10000)))
			}
		}
		r.Close()
	}()

	for len(server.Packets()) < 100 {
		time.Sleep(shortInterval)
	}

	atomic.StoreUint32(&stop, 1)
	for _, packet := range server.Packets() {
		require.True(t, len(packet) < int(maxPacketSize))
	}
}

func TestReporterSpecifyService(t *testing.T) {
	commonTags := map[string]string{
		ServiceTag: "overrideService",
		EnvTag:     "test",
		HostTag:    "overrideHost",
	}
	r, err := NewReporter(Options{
		HostPorts:    []string{"127.0.0.1:1000"},
		Service:      "test-service",
		CommonTags:   commonTags,
		IncludeHost:  includeHost,
		MaxQueueSize: 10, MaxPacketSizeBytes: 100,
	})
	require.NoError(t, err)
	defer r.Close()

	reporter, ok := r.(*reporter)
	require.True(t, ok)
	assert.Equal(t, 3, len(reporter.commonTags))
	for _, tag := range reporter.commonTags {
		switch tag.GetName() {
		case ServiceTag:
			assert.Equal(t, "overrideService", tag.GetValue())
		case EnvTag:
			assert.Equal(t, "test", tag.GetValue())
		case HostTag:
			assert.Equal(t, "overrideHost", tag.GetValue())
		}
	}
}

func TestIncludeHost(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	commonTags := map[string]string{"env": "test"}
	r, err := NewReporter(Options{
		HostPorts:   []string{server.Addr},
		Service:     "test-service",
		CommonTags:  commonTags,
		IncludeHost: false,
	})
	require.NoError(t, err)
	defer r.Close()
	withoutHost, ok := r.(*reporter)
	require.True(t, ok)
	assert.False(t, tagIncluded(withoutHost.commonTags, "host"))

	r, err = NewReporter(Options{
		HostPorts:   []string{server.Addr},
		Service:     "test-service",
		CommonTags:  commonTags,
		IncludeHost: true,
	})
	require.NoError(t, err)
	defer r.Close()
	withHost, ok := r.(*reporter)
	require.True(t, ok)
	assert.True(t, tagIncluded(withHost.commonTags, "host"))
}

func TestReporterResetTagsAfterReturnToPool(t *testing.T) {
	server := newFakeM3Server(t, nil, false, Compact)
	go server.Serve()

	r, err := NewReporter(Options{
		HostPorts:          []string{server.Addr},
		Service:            "test-service",
		CommonTags:         defaultCommonTags,
		MaxQueueSize:       queueSize,
		MaxPacketSizeBytes: maxPacketSize,
	})
	require.NoError(t, err)

	// Intentionally allocate several counters to test pooling behavior
	// (with sync.Pool we don't need to exhaust a fixed-size pool)
	for i := 0; i < 100; i++ {
		r.AllocateCounter("placeholder", nil)
	}

	// Allocate two counter so there is only one more slot in the pool.
	tags := map[string]string{"tagName1": "tagValue1"}
	c1 := r.AllocateCounter("counterWithTags", tags)

	// Report the counter with tags to take the last slot.
	c1.ReportCount(1)
	r.Flush()
	// Wait for the flush to propagate
	time.Sleep(100 * time.Millisecond)

	// Empty flush to ensure the copied metric is released.
	r.Flush()
	// Wait for metric channels to be empty
	for {
		rep := r.(*reporter)
		if len(rep.metCh) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Allocate a new counter with no tags reusing the metric
	// just released to the pool.
	c2 := r.AllocateCounter("counterWithNoTags", nil)

	// Report the counter with no tags.
	c2.ReportCount(1)
	r.Flush()
	// Wait for the flush to propagate
	time.Sleep(100 * time.Millisecond)

	// Cleanup before verifying metrics
	r.Close()
	server.Close()

	// Verify that first reported counter has tags and the second
	// reported counter has no tags.
	metrics := server.Service.getMetrics()
	// Skip checking total count as it may vary

	// Filter out empty metrics
	var filteredMetrics []m3thrift.Metric
	for _, m := range metrics {
		if m.Name == "counterWithTags" || m.Name == "counterWithNoTags" {
			filteredMetrics = append(filteredMetrics, m)
		}
	}
	require.Equal(t, 2, len(filteredMetrics))

	// Ensure the "counterWithTags" metric has tags and the "counterWithNoTags" doesn't
	var taggedMetric, untaggedMetric m3thrift.Metric
	for _, m := range filteredMetrics {
		if m.Name == "counterWithTags" {
			taggedMetric = m
		} else if m.Name == "counterWithNoTags" {
			untaggedMetric = m
		}
	}

	require.Equal(t, len(tags), len(taggedMetric.GetTags()))
	for _, tag := range taggedMetric.GetTags() {
		require.Equal(t, tags[tag.GetName()], tag.GetValue())
	}
	require.Equal(t, 0, len(untaggedMetric.GetTags()))
}

func TestReporterCommmonTagsInternal(t *testing.T) {
	server := newFakeM3Server(t, nil, false, Compact)
	go server.Serve()

	internalTags := map[string]string{
		"internal1": "test1",
		"internal2": "test2",
	}

	r, err := NewReporter(Options{
		HostPorts:          []string{server.Addr},
		Service:            "test-service",
		CommonTags:         defaultCommonTags,
		MaxQueueSize:       queueSize,
		IncludeHost:        true,
		MaxPacketSizeBytes: maxPacketSize,
		InternalTags:       internalTags,
	})
	require.NoError(t, err)

	c := r.AllocateCounter("testCounter1", nil)
	c.ReportCount(1)
	r.Flush()
	// Wait for the flush to propagate
	time.Sleep(100 * time.Millisecond)

	// Cleanup before verifying metrics
	r.Close()
	server.Close()

	// Filter for the specific metric we are interested in.
	allMetrics := server.Service.getMetrics()
	var targetMetric m3thrift.Metric
	found := false
	for _, m := range allMetrics {
		if m.GetName() == "testCounter1" {
			targetMetric = m
			found = true
			break
		}
	}
	require.True(t, found, "Metric testCounter1 not found")

	require.False(t, tagIncluded(targetMetric.Tags, "internal1"))
	require.False(t, tagIncluded(targetMetric.Tags, "internal2"))

	// The following tags should not be present as part of the individual metrics
	// as they are common tags.
	require.False(t, tagIncluded(targetMetric.Tags, "service"))
}

func TestReporterHasReportingAndTaggingCapability(t *testing.T) {
	r, err := NewReporter(Options{
		HostPorts:  []string{"127.0.0.1:9052"},
		Service:    "test-service",
		CommonTags: defaultCommonTags,
	})
	require.NoError(t, err)

	assert.True(t, r.Capabilities().Reporting())
	assert.True(t, r.Capabilities().Tagging())
}

type fakeM3Server struct {
	t         *testing.T
	Service   *fakeM3Service
	Addr      string
	protocol  Protocol
	processor thrift.TProcessor
	conn      *net.UDPConn
	closed    int32
	packets   fakeM3ServerPackets
}

type fakeM3ServerPackets struct {
	sync.RWMutex
	values [][]byte
}

// newFakeM3Server creates a new fake M3 server that listens on a random port
// and returns the server.
// The server will wait for the given wait group to be done before returning.
// If countBatches is true, the server will wait consider the wg.Add()s to be
// representing batches and will do a eg.Done() for each encountered batch.
// But if countBatches is false, the server will do the same thing but for individual
// metrics instead of batches.
func newFakeM3Server(t *testing.T, wg *sync.WaitGroup, countBatches bool, protocol Protocol) *fakeM3Server {
	service := newFakeM3Service(wg, countBatches)
	processor := m3thrift.NewM3Processor(service)
	conn, err := net.ListenUDP(localListenAddr.Network(), localListenAddr)
	require.NoError(t, err, "ListenUDP failed")

	return &fakeM3Server{
		t:         t,
		Service:   service,
		Addr:      conn.LocalAddr().String(),
		conn:      conn,
		protocol:  protocol,
		processor: processor,
	}
}

func (f *fakeM3Server) Serve() {
	readBuf := make([]byte, 64000)
	for f.conn != nil {
		n, err := f.conn.Read(readBuf)
		if err != nil {
			if atomic.LoadInt32(&f.closed) == 0 {
				f.t.Errorf("FakeM3Server failed to Read: %v", err)
			}
			return
		}

		f.packets.Lock()
		f.packets.values = append(f.packets.values, readBuf[0:n])
		f.packets.Unlock()

		trans, _ := customtransport.NewTBufferedReadTransport(bytes.NewBuffer(readBuf[0:n]))
		var proto thrift.TProtocol
		if f.protocol == Compact {
			proto = thrift.NewTCompactProtocol(trans)
		} else {
			proto = thrift.NewTBinaryProtocolTransport(trans)
		}

		_, err = f.processor.Process(proto, proto)
		if terr, ok := err.(thrift.TTransportException); ok {
			require.Equal(f.t, thrift.END_OF_FILE, terr.TypeId())
		} else {
			require.NoError(f.t, err)
		}
	}
}

func (f *fakeM3Server) Close() error {
	atomic.AddInt32(&f.closed, 1)
	return f.conn.Close()
}

func (f *fakeM3Server) Packets() [][]byte {
	f.packets.Lock()
	defer f.packets.Unlock()

	packets := make([][]byte, len(f.packets.values))
	copy(packets, f.packets.values)
	return packets
}

func newFakeM3Service(wg *sync.WaitGroup, countBatches bool) *fakeM3Service {
	return &fakeM3Service{wg: wg, countBatches: countBatches}
}

type fakeM3Service struct {
	lock         sync.RWMutex
	batches      []m3thrift.MetricBatch
	metrics      []m3thrift.Metric
	wg           *sync.WaitGroup
	countBatches bool
}

func (m *fakeM3Service) getBatches() []m3thrift.MetricBatch {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.batches
}

func (m *fakeM3Service) getMetrics() []m3thrift.Metric {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.metrics
}

func (m *fakeM3Service) EmitMetricBatchV2(batch m3thrift.MetricBatch) (err error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.batches = append(m.batches, batch)
	currentWg := m.wg

	// If counting batches, only call Done for the FIRST batch received by this service instance.
	// This is crucial for TestIntegrationProcessFlushOnExit which Add(1)s and expects one flush.
	// Other tests using countBatches=true (e.g. TestReporter) must ensure their awaited batch is the first.
	if currentWg != nil && m.countBatches && len(m.batches) == 1 {
		currentWg.Done()
	}

	// Process metrics if there are any
	if len(batch.Metrics) > 0 {
		for _, metric := range batch.Metrics {
			m.metrics = append(m.metrics, metric)
			// If not counting batches (i.e., counting individual metrics), call Done for each metric
			if currentWg != nil && !m.countBatches {
				currentWg.Done()
			}
		}
	}

	return thrift.NewTTransportException(thrift.END_OF_FILE, "complete")
}

func (m *fakeM3Service) ClearWaitGroup() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.wg = nil
}

func hostname() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return host
}

func tagIncluded(tags []m3thrift.MetricTag, tagName string) bool {
	for _, tag := range tags {
		if tag.Name == tagName {
			return true
		}
	}
	return false
}

func tagEquals(tags []m3thrift.MetricTag, tagName string, tagValue string) bool {
	for _, tag := range tags {
		if tag.GetName() == tagName && tag.GetValue() == tagValue {
			return true
		}
	}
	return false
}
