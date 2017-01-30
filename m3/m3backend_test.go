// Copyright (c) 2017 Uber Technologies, Inc.
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

package metrics

// Copied from code.uber.internal:go-common.git at version 139e3b5b4b4b775ff9ed8abb2a9f31b7bd1aad58

import (
	"bytes"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uber-go/tally/m3/customtransports"
	"github.com/uber-go/tally/m3/thrift"
	"github.com/uber-go/tally/m3/thriftudp"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/stretchr/testify/require"
)

const (
	numReaders    = 10
	queueSize     = 1000
	includeHost   = true
	maxPacketSize = int32(1440)
	noInterval    = time.Duration(0)
	shortInterval = 10 * time.Millisecond
	longInterval  = 100 * time.Millisecond
)

var localListenAddr = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
var defaultCommonTags = map[string]string{"env": "test", "host": "test"}

var protocols = []Protocol{Compact, Binary}

// TestM3Backend tests the M3Backend works as expected with both compact and binary protocols
func TestM3Backend(t *testing.T) {
	for _, protocol := range protocols {
		var wg sync.WaitGroup
		server := newFakeM3Server(t, &wg, true, protocol)
		go server.Serve()
		defer server.Close()

		commonTags := map[string]string{"env": "development", "host": hostname(), "commonTag": "common", "commonTag2": "tag", "commonTag3": "val"}
		var m3be BufferedBackend
		var err error
		if protocol == Compact {
			m3be, err = NewM3Backend(server.Addr, "testService", commonTags, includeHost, queueSize,
				maxPacketSize, shortInterval)
		} else {
			m3be, err = NewMultiM3BackendWithProtocol([]string{server.Addr}, "testService", commonTags,
				includeHost, queueSize, maxPacketSize, shortInterval, Binary)
		}
		require.Nil(t, err)
		defer m3be.Close()
		tags := map[string]string{"testTag": "TestValue", "testTag2": "TestValue2"}
		wg.Add(2)
		id1 := m3be.RegisterForID("my-counter", tags, CounterType)
		m3be.IncCounter(id1, 10)
		id2 := m3be.RegisterForID("my-timer", tags, TimerType)
		m3be.RecordTimer(id2, 5*time.Millisecond)
		wg.Wait()

		//Fill in service and env tags
		commonTags["service"] = "testService"
		commonTags["env"] = "development"
		commonTags["host"] = hostname()

		batches := server.Service.getBatches()
		require.Equal(t, 2, len(batches))

		//Validate common tags
		for _, batch := range batches {
			require.NotNil(t, batch)
			require.True(t, batch.IsSetCommonTags())
			require.Equal(t, len(commonTags), len(batch.GetCommonTags()))
			for tag := range batch.GetCommonTags() {
				require.Equal(t, commonTags[tag.GetTagName()], tag.GetTagValue())
			}
		}

		//Validate metrics
		emittedCounters := batches[0].GetMetrics()
		require.Equal(t, 1, len(emittedCounters))
		emittedTimers := batches[1].GetMetrics()
		require.Equal(t, 1, len(emittedTimers))

		emittedCounter, emittedTimer := emittedCounters[0], emittedTimers[0]
		if emittedCounter.GetName() == "my-timer" {
			emittedCounter, emittedTimer = emittedTimer, emittedCounter
		}

		require.Equal(t, "my-counter", emittedCounter.GetName())
		require.True(t, emittedCounter.IsSetTags())
		require.Equal(t, len(tags), len(emittedCounter.GetTags()))
		for tag := range emittedCounter.GetTags() {
			require.Equal(t, tags[tag.GetTagName()], tag.GetTagValue())
		}
		require.True(t, emittedCounter.IsSetMetricValue())
		emittedVal := emittedCounter.GetMetricValue()
		require.True(t, emittedVal.IsSetCount())
		require.False(t, emittedVal.IsSetGauge())
		require.False(t, emittedVal.IsSetTimer())
		emittedCount := emittedVal.GetCount()
		require.True(t, emittedCount.IsSetI64Value())
		require.EqualValues(t, int64(10), emittedCount.GetI64Value())

		require.True(t, emittedTimer.IsSetMetricValue())
		emittedVal = emittedTimer.GetMetricValue()
		require.False(t, emittedVal.IsSetCount())
		require.False(t, emittedVal.IsSetGauge())
		require.True(t, emittedVal.IsSetTimer())
		emittedTimerVal := emittedVal.GetTimer()
		require.True(t, emittedTimerVal.IsSetI64Value())
		require.EqualValues(t, int64(5*1000*1000), emittedTimerVal.GetI64Value())
	}
}

// TestMultiM3Backend tests the multi M3Backend works as expected
func TestMultiM3Backend(t *testing.T) {
	dests := []string{"127.0.0.1:9052", "127.0.0.1:9053"}
	commonTags := map[string]string{"env": "test", "host": "test", "commonTag": "common", "commonTag2": "tag", "commonTag3": "val"}
	m, err := NewMultiM3Backend(dests, "testService", commonTags, includeHost, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	defer m.Close()
	m3be, ok := m.(*m3Backend)
	require.True(t, ok)
	multitransport, ok := m3be.client.Transport.(*thriftudp.TMultiUDPTransport)
	require.NotNil(t, multitransport)
	require.True(t, ok)
}

// TestNewM3BackendErrors tests for M3Backend creation errors
func TestNewM3BackendErrors(t *testing.T) {
	var err error
	// Test 0 queue size
	_, err = NewM3Backend("127.0.0.1", "testService", nil, false, 0, maxPacketSize, noInterval)
	require.NotNil(t, err)
	// Test 0 max packet size
	_, err = NewM3Backend("127.0.0.1", "testService", nil, false, queueSize, 0, noInterval)
	require.NotNil(t, err)
	// Test freeBytes (maxPacketSizeBytes - numOverheadBytes) is negative
	_, err = NewM3Backend("127.0.0.1", "testService", nil, false, 10, 2<<5, time.Minute)
	require.NotNil(t, err)
	// Test invalid addr
	_, err = NewM3Backend("fakeAddress", "testService", nil, false, queueSize, maxPacketSize, noInterval)
	require.NotNil(t, err)
}

// TestM3BackendInterval ensures the M3Backend emits metrics on the set interval
func TestM3BackendInterval(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	m3be, err := NewM3Backend(server.Addr, "testService", defaultCommonTags, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	defer m3be.Close()
	wg.Add(1)
	id := m3be.RegisterForID("my-counter", nil, CounterType)
	m3be.IncCounter(id, 10)
	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])
	require.Equal(t, 1, len(server.Service.getBatches()[0].GetMetrics()))
}

// TestM3BackendInterval ensures the M3Backend tallies counters as expected
func TestM3BackendTally(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	m, err := NewM3Backend(server.Addr, "testService", defaultCommonTags, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	defer m.Close()

	m3be, ok := m.(*m3Backend)
	require.True(t, ok)

	value := int64(1234)
	counter := m3be.counters.newMetric("direct", nil, CounterType)
	counter.MetricValue.Count.I64Value = &value
	m3be.counters.Lock()
	size := m3be.counters.calculateSize(counter)
	m3be.counters.Unlock()

	mets := make([]*m3.Metric, 0, (m3be.freeBytes / 10))
	info := &tallyMetricsInfo{mets, false}
	bytes := int32(0)

	sizedMet := m3be.resourcePool.getSizedMetric()
	sizedMet.m = counter
	sizedMet.size = size

	info, bytes = m3be.processTallyMetric(info, bytes, sizedMet)
	require.Equal(t, 1, len(info.mets))
	require.True(t, bytes > 0)

	wg.Add(1)
	info, bytes = m3be.processTallyMetric(info, bytes, nil)
	require.Equal(t, 0, len(info.mets))
	require.True(t, bytes == 0)
}

// TestM3BackendBuffering ensures the M3Backend buffers values
func TestM3BackendBuffering(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, false, Compact)
	go server.Serve()
	defer server.Close()

	m3be, err := NewM3Backend(server.Addr, "testService", defaultCommonTags, false, queueSize, maxPacketSize, longInterval)
	require.Nil(t, err)
	defer m3be.Close()
	numCounters := 40
	incPerCounter := 2
	wg.Add(numCounters)
	for i := 0; i < numCounters; i++ {
		id := m3be.RegisterForID("testing.my-counter.for.backend.buffering"+strconv.Itoa(i), nil, CounterType)
		for j := 0; j < incPerCounter; j++ {
			m3be.IncCounter(id, 1)
		}
	}

	wg.Wait()

	mets := server.Service.getMetrics()
	require.Equal(t, numCounters, len(mets))
	for _, met := range mets {
		require.Equal(t, int64(2), *met.MetricValue.Count.I64Value)
	}
}

// TestM3BackendFinalFlush ensures the M3Backend emits the last batch of metrics
// after close
func TestM3BackendFinalFlush(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	m3be, err := NewM3Backend(server.Addr, "testService", defaultCommonTags, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	wg.Add(1)
	id := m3be.RegisterForID("my-timer", nil, TimerType)
	m3be.RecordTimer(id, 10)
	m3be.Close()
	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])
	require.Equal(t, 1, len(server.Service.getBatches()[0].GetMetrics()))
}

// TestM3BackendCache ensures the M3Backend caches results
func TestM3BackendCache(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	m3be, err := NewM3Backend(server.Addr, "testService", defaultCommonTags, false, 10, 1440, shortInterval)
	require.Nil(t, err)
	defer m3be.Close()

	server.Service.wg.Add(2)
	gID := m3be.RegisterForID("gauge", map[string]string{"t1": "v1"}, GaugeType)
	m3be.UpdateGauge(gID, 1)
	m3be.UpdateGauge(gID, 2)
	m3be.UpdateGauge(gID, 3)
	cID := m3be.RegisterForID("counter", nil, CounterType)
	m3be.IncCounter(cID, 1)
	m3be.IncCounter(cID, 2)
	m3be.IncCounter(cID, 3)

	server.Service.wg.Wait()

	batches := server.Service.getBatches()
	require.Equal(t, 2, len(batches))
	m1 := batches[0].GetMetrics()[0]
	m2 := batches[1].GetMetrics()[0]

	if m1.GetName() == "gauge" {
		tmp := m1
		m1 = m2
		m2 = tmp
	}

	require.True(t, m1.MetricValue.IsSetCount())
	require.Equal(t, int64(6), m1.MetricValue.Count.GetI64Value())
	require.True(t, m2.MetricValue.IsSetGauge())
	require.Equal(t, int64(3), m2.MetricValue.Gauge.GetI64Value())
}

func TestBatchSizes(t *testing.T) {
	server := newSimpleServer(t)
	go server.serve()
	defer server.close()

	commonTags := map[string]string{"env": "test", "domain": "pod" + strconv.Itoa(rand.Intn(100))}
	maxPacketSize := int32(1440)
	m3be, err := NewM3Backend(server.addr(), "testService", commonTags, false, 10, maxPacketSize, shortInterval)

	require.NoError(t, err)
	rand.Seed(time.Now().UnixNano())
	var stop uint32

	go func() {
		for atomic.LoadUint32(&stop) == 0 {
			metTypeRand := rand.Intn(9)
			name := "size.test.metric.name" + strconv.Itoa(rand.Intn(50))
			var tags map[string]string
			if rand.Intn(2) == 0 {
				tags = map[string]string{"t1": "val" + strconv.Itoa(rand.Intn(10000))}
			}

			cid := m3be.RegisterForID(name, tags, CounterType)
			gid := m3be.RegisterForID(name, tags, GaugeType)
			tid := m3be.RegisterForID(name, tags, TimerType)
			if metTypeRand <= 2 {
				m3be.IncCounter(cid, rand.Int63n(10000))
			} else if metTypeRand <= 5 {
				m3be.UpdateGauge(gid, rand.Int63n(10000))
			} else {
				m3be.RecordTimer(tid, time.Duration(rand.Int63n(10000)))
			}
		}
		m3be.Close()
	}()

	packets := server.getPackets()
	for len(packets) < 100 {
		time.Sleep(shortInterval)
		packets = server.getPackets()
	}

	atomic.StoreUint32(&stop, 1)
	for _, packet := range packets {
		require.True(t, len(packet) < int(maxPacketSize))
	}
}

func TestInvalidIds(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	m3be, err := NewM3Backend(server.Addr, "testService", defaultCommonTags, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	m3be.RecordTimer(nil, 10)
	m3be.IncCounter(nil, 20)
	m3be.UpdateGauge(nil, 20)
	m3be.Close()
	time.Sleep(2 * shortInterval)

	require.Equal(t, 0, len(server.Service.getBatches()))
	require.Panics(t, func() { m3be.RegisterForID("test", nil, 0) }, "Unknown MetricType")
}

func TestUnregister(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	server := newFakeM3Server(t, &wg, false, Compact)
	go server.Serve()
	defer server.Close()

	m3be, err := NewM3Backend(server.Addr, "testService", defaultCommonTags, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)
	id := m3be.RegisterForID("my-counter", nil, CounterType)
	m3be.IncCounter(id, 1)
	id.UnregisterID()
	wg.Wait()

	time.Sleep(10 * shortInterval) // give some time for async cleanup to run
	m3be.IncCounter(id, 2)
	m3be.Close()
	time.Sleep(10 * shortInterval) // give some time for flushed value that we do not expect

	mets := server.Service.getMetrics()
	require.Equal(t, 1, len(mets))
	require.Equal(t, int64(1), *mets[0].MetricValue.Count.I64Value)
}

func TestM3BackendSpecifyService(t *testing.T) {
	commonTags := map[string]string{
		serviceTag: "overrideService",
		envTag:     "test",
		hostTag:    "overrideHost",
	}
	m, err := NewM3Backend("127.0.0.1:1000", "testService", commonTags, includeHost, 10, 100, noInterval)
	require.Nil(t, err)
	defer m.Close()

	m3be, ok := m.(*m3Backend)
	require.True(t, ok)
	require.Equal(t, 3, len(m3be.commonTags))
	for tag := range m3be.commonTags {
		if tag.GetTagName() == serviceTag {
			require.Equal(t, "overrideService", tag.GetTagValue())
		}
		if tag.GetTagName() == hostTag {
			require.Equal(t, "overrideHost", tag.GetTagValue())
		}
	}
}

func TestM3BackendMaxTags(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	m, err := NewM3Backend(server.Addr, "testService", defaultCommonTags, includeHost, 10, 100, shortInterval)
	require.Nil(t, err)
	defer m.Close()
	m3be, ok := m.(*m3Backend)
	require.True(t, ok)

	tags := make(map[string]string, 11)
	for i := 0; i <= MaxTags; i++ {
		tags["tagName"+strconv.Itoa(i)] = "tagValue"
	}

	id := m3be.RegisterForID("gauge", tags, GaugeType)
	m3be.UpdateGauge(id, 10)
	time.Sleep(5 * shortInterval)

	require.Equal(t, 0, len(server.Service.getBatches()))
}

func TestIncludeHost(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	tagIncluded := func(tags map[*m3.MetricTag]bool, tagName string) bool {
		for k, v := range tags {
			if v && k.TagName == tagName {
				return true
			}
		}
		return false
	}

	commonTags := map[string]string{"env": "test"}
	m, err := NewM3Backend(server.Addr, "testService", commonTags, false, queueSize, maxPacketSize, noInterval)
	require.NoError(t, err)
	defer m.Close()
	m3WithoutHost, ok := m.(*m3Backend)
	require.True(t, ok)
	require.False(t, tagIncluded(m3WithoutHost.commonTags, "host"))

	m, err = NewM3Backend(server.Addr, "testService", defaultCommonTags, true, queueSize, maxPacketSize, noInterval)
	require.NoError(t, err)
	defer m.Close()
	m3WithHost, ok := m.(*m3Backend)
	require.True(t, ok)
	require.True(t, tagIncluded(m3WithHost.commonTags, "host"))
}

func TestM3BackendHasTaggingCapability(t *testing.T) {
	m, err := NewM3Backend("127.0.0.1:9052", "testService", defaultCommonTags, false, queueSize, maxPacketSize, noInterval)
	require.Nil(t, err)

	require.True(t, m.Capabilities().Tagging())
}

func TestM3GetForID(t *testing.T) {
	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	m3be, err := NewM3Backend(server.Addr, "testService", defaultCommonTags, false, queueSize, maxPacketSize, shortInterval)
	require.Nil(t, err)

	tags := map[string]string{"testTag": "TestValue", "testTag2": "TestValue2"}

	id1 := m3be.RegisterForID("my-counter", tags, CounterType)
	id2 := m3be.RegisterForID("my-timer", tags, TimerType)
	id3 := m3be.RegisterForID("my-gauge", tags, GaugeType)

	// Verify that the created IDs exist
	r1, e1 := m3be.GetForID("my-counter", tags, CounterType)
	require.Equal(t, id1, r1)
	require.True(t, e1)

	r2, e2 := m3be.GetForID("my-timer", tags, TimerType)
	require.Equal(t, id2, r2)
	require.True(t, e2)

	r3, e3 := m3be.GetForID("my-gauge", tags, GaugeType)
	require.Equal(t, id3, r3)
	require.True(t, e3)

	// Check for a metric that has not been created
	r, e := m3be.GetForID("my-ctr", tags, CounterType)
	require.Nil(t, r)
	require.False(t, e)
}

type simpleServer struct {
	conn    *net.UDPConn
	t       *testing.T
	packets [][]byte
	sync.Mutex
	closed int32
}

func newSimpleServer(t *testing.T) *simpleServer {
	addr, err := net.ResolveUDPAddr("udp", ":0")
	require.NoError(t, err)

	conn, err := net.ListenUDP(addr.Network(), addr)
	require.NoError(t, err)

	return &simpleServer{conn: conn, t: t}
}

func (s *simpleServer) serve() {
	readBuf := make([]byte, 64000)
	for atomic.LoadInt32(&s.closed) == 0 {
		n, err := s.conn.Read(readBuf)
		if err != nil {
			if atomic.LoadInt32(&s.closed) == 0 {
				s.t.Errorf("FakeM3Server failed to Read: %v", err)
			}
			return
		}
		s.Lock()
		s.packets = append(s.packets, readBuf[0:n])
		s.Unlock()
		readBuf = make([]byte, 64000)
	}
}

func (s *simpleServer) getPackets() [][]byte {
	s.Lock()
	defer s.Unlock()
	copy := make([][]byte, len(s.packets))
	for i, packet := range s.packets {
		copy[i] = packet
	}

	return copy
}

func (s *simpleServer) close() error {
	atomic.AddInt32(&s.closed, 1)
	return s.conn.Close()
}

func (s *simpleServer) addr() string {
	return s.conn.LocalAddr().String()
}

type fakeM3Server struct {
	t         *testing.T
	Service   *fakeM3Service
	Addr      string
	protocol  Protocol
	processor thrift.TProcessor
	conn      *net.UDPConn
	closed    int32
}

func newFakeM3Server(t *testing.T, wg *sync.WaitGroup, countBatches bool, protocol Protocol) *fakeM3Server {
	service := newFakeM3Service(wg, countBatches)
	processor := m3.NewM3Processor(service)
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
		trans, _ := customtransport.NewTBufferedReadTransport(bytes.NewBuffer(readBuf[0:n]))
		var proto thrift.TProtocol
		if f.protocol == Compact {
			proto = thrift.NewTCompactProtocol(trans)
		} else {
			proto = thrift.NewTBinaryProtocolTransport(trans)
		}
		f.processor.Process(proto, proto)
	}
}

func (f *fakeM3Server) Close() error {
	atomic.AddInt32(&f.closed, 1)
	return f.conn.Close()
}

func newFakeM3Service(wg *sync.WaitGroup, countBatches bool) *fakeM3Service {
	return &fakeM3Service{wg: wg, countBatches: countBatches}
}

type fakeM3Service struct {
	lock         sync.RWMutex
	batches      []*m3.MetricBatch
	metrics      []*m3.Metric
	wg           *sync.WaitGroup
	countBatches bool
}

func (m *fakeM3Service) getBatches() []*m3.MetricBatch {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.batches
}

func (m *fakeM3Service) getMetrics() []*m3.Metric {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.metrics
}

func (m *fakeM3Service) EmitMetricBatch(batch *m3.MetricBatch) (err error) {
	m.lock.Lock()
	m.batches = append(m.batches, batch)
	if m.countBatches {
		m.wg.Done()
	}

	for _, metric := range batch.Metrics {
		m.metrics = append(m.metrics, metric)
		if !m.countBatches {
			m.wg.Done()
		}
	}

	m.lock.Unlock()
	return thrift.NewTTransportException(thrift.END_OF_FILE, "complete")
}

func hostname() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return host
}
