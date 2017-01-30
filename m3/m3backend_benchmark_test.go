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

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uber-go/tally/m3/thrift"
	"github.com/uber-go/tally/m3/thriftudp"

	"github.com/apache/thrift/lib/go/thrift"
)

const (
	updaters = 10
	updates  = 1000
	numIds   = 10

	testID = "stats.$dc.gauges.m3+servers.my-internal-server-$dc.network.eth0_tx_colls+dc=$dc,domain=production.$zone,env=production,pipe=$pipe,service=servers,type=gauge"
)

func BenchmarkDirectID(b *testing.B) {
	for n := 0; n < b.N; n++ {
		benchUpdateDirect(numIds, updaters, updates)
	}
}

func BenchmarkMapAccess(b *testing.B) {
	for n := 0; n < b.N; n++ {
		benchMapAccess(numIds, updaters, updates)
	}
}

func benchUpdateDirect(numIds, numUpdaters, numUpdates int) {
	mids := make([]*counterMetricID, numIds)
	for i := 0; i < numIds; i++ {
		mids[i] = &counterMetricID{}
	}

	var wg sync.WaitGroup
	wg.Add(numUpdaters * numIds)
	for i := 0; i < numIds; i++ {
		mid := mids[i]
		for j := 0; j < numUpdaters; j++ {
			mid := mid
			go func() {
				for k := 0; k < numUpdates; k++ {
					atomic.AddInt64(&mid.val, 1)
				}
				wg.Done()
			}()
		}
	}

	wg.Wait()
}

func BenchmarkNewMetric(b *testing.B) {
	protocolFactory := thrift.NewTCompactProtocolFactory()
	resourcePool := newM3ResourcePool(protocolFactory)
	benchAggMets := newAggMets(resourcePool, updateCounterVal)

	for n := 0; n < b.N; n++ {
		benchAggMets.newMetric("foo", nil, CounterType)
	}
}

func BenchmarkCalulateSize(b *testing.B) {
	protocolFactory := thrift.NewTCompactProtocolFactory()
	resourcePool := newM3ResourcePool(protocolFactory)
	benchAggMets := newAggMets(resourcePool, updateCounterVal)

	val := int64(123456)
	met := benchAggMets.newMetric("foo", nil, CounterType)
	met.MetricValue.Count.I64Value = &val

	for n := 0; n < b.N; n++ {
		benchAggMets.calculateSize(met)
	}
}

func benchmarkM3Backend() *m3Backend {
	protocolFactory := thrift.NewTCompactProtocolFactory()
	trans, _ := thriftudp.NewTUDPClientTransport("localhost:4444", "")
	client := m3.NewM3ClientFactory(trans, protocolFactory)
	resourcePool := newM3ResourcePool(protocolFactory)

	batch := resourcePool.getBatch()

	m3 := &m3Backend{
		client:       client,
		curBatch:     batch,
		commonTags:   nil,
		freeBytes:    maxPacketSize,
		resourcePool: resourcePool,
		counters:     newAggMets(resourcePool, updateCounterVal),
		gauges:       newAggMets(resourcePool, updateGaugeVal),
		timers:       newAggMets(resourcePool, noopVal),
		metCh:        make(chan *sizedMetric, 0), // drop these on the floor
		timerCh:      make(chan *timerMetric, 0),
		closeChan:    make(chan struct{}),
	}
	return m3
}

func BenchmarkReportCounter(b *testing.B) {
	m3 := benchmarkM3Backend()
	for n := 0; n < b.N; n++ {
		m3.ReportCounter("foo", nil, 1234)
	}
}

func BenchmarkReportGauge(b *testing.B) {
	m3 := benchmarkM3Backend()
	for n := 0; n < b.N; n++ {
		m3.ReportGauge("bar", nil, 1234)
	}
}

func BenchmarkReportTimer(b *testing.B) {
	m3 := benchmarkM3Backend()
	t := time.Millisecond * 175
	for n := 0; n < b.N; n++ {
		m3.ReportTimer("foo", nil, t)
	}
}

func benchMapAccess(numIds, numUpdaters, numUpdates int) {
	idMap := make(map[string]*int64, numIds)
	ids := make([]string, numIds)
	for i := 0; i < numIds; i++ {
		id := testID + strconv.Itoa(i)
		var val int64
		idMap[id] = &val
		ids[i] = id
	}

	var lock sync.RWMutex
	var wg sync.WaitGroup
	wg.Add(numUpdaters * numIds)

	for i := 0; i < numIds; i++ {
		id := ids[i]
		for j := 0; j < numUpdaters; j++ {
			id := id
			go func() {
				for k := 0; k < numUpdates; k++ {
					lock.RLock()
					val := idMap[id]
					lock.RUnlock()
					atomic.AddInt64(val, 1)
				}
				wg.Done()
			}()
		}
	}

	wg.Wait()
}
