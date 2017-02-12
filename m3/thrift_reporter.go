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

package m3

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	"github.com/uber-go/tally"
	"github.com/uber-go/tally/m3/customtransports"
	m3thrift "github.com/uber-go/tally/m3/thrift"
	"github.com/uber-go/tally/m3/thriftudp"

	"github.com/apache/thrift/lib/go/thrift"
)

// Protocol describes a M3 thrift transport protocol.
type Protocol int

// Compact and Binary represent the compact and
// binary thrift protocols respectively.
const (
	Compact Protocol = iota
	Binary
)

const (
	emitMetricBatchOverhead = 19
)

// Initialize max vars in init function to avoid lint error.
var (
	maxInt64   int64
	maxFloat64 float64
)

func init() {
	maxInt64 = math.MaxInt64
	maxFloat64 = math.MaxFloat64
}

type metricType int

const (
	counterType metricType = iota + 1
	timerType
	gaugeType
)

var (
	errCommonTagSize = errors.New("common tags serialized size exceeds packet size")
)

// reporter is a metrics backend that reports metrics to a local or
// remote M3 collector, metrics are batched together and emitted
// via either thrift compact or binary protocol in batch UDP packets.
type thriftReporter struct {
	client       *m3thrift.M3Client
	curBatch     *m3thrift.MetricBatch
	curBatchLock sync.Mutex
	calc         *customtransport.TCalcTransport
	calcProto    thrift.TProtocol
	calcLock     sync.Mutex
	commonTags   map[*m3thrift.MetricTag]bool
	freeBytes    int32
	processors   sync.WaitGroup
	resourcePool *resourcePool
	closeChan    chan struct{}

	metCh chan sizedMetric
}

// newThriftReporter creates a new M3 reporter which emits metrics using a Thrift encoding.
func newThriftReporter(opts Options) (Reporter, error) {
	// Create M3 thrift client
	var trans thrift.TTransport
	var err error
	if len(opts.HostPorts) == 0 {
		err = errNoHostPorts
	} else if len(opts.HostPorts) == 1 {
		trans, err = thriftudp.NewTUDPClientTransport(opts.HostPorts[0], "")
	} else {
		trans, err = thriftudp.NewTMultiUDPClientTransport(opts.HostPorts, "")
	}
	if err != nil {
		return nil, err
	}

	var protocolFactory thrift.TProtocolFactory
	if opts.Protocol == Compact {
		protocolFactory = thrift.NewTCompactProtocolFactory()
	} else {
		protocolFactory = thrift.NewTBinaryProtocolFactoryDefault()
	}

	client := m3thrift.NewM3ClientFactory(trans, protocolFactory)
	resourcePool := newResourcePool(protocolFactory)

	// Create common tags
	tags := resourcePool.getTagList()
	for k, v := range opts.CommonTags {
		tags[createTag(resourcePool, k, v)] = true
	}
	if opts.CommonTags[ServiceTag] == "" {
		if opts.Service == "" {
			return nil, fmt.Errorf("%s common tag is required", ServiceTag)
		}
		tags[createTag(resourcePool, ServiceTag, opts.Service)] = true
	}
	if opts.CommonTags[EnvTag] == "" {
		if opts.Env == "" {
			return nil, fmt.Errorf("%s common tag is required", EnvTag)
		}
		tags[createTag(resourcePool, EnvTag, opts.Env)] = true
	}
	if opts.IncludeHost {
		if opts.CommonTags[HostTag] == "" {
			hostname, err := os.Hostname()
			if err != nil {
				return nil, fmt.Errorf("error resolving host tag: %v", err)
			}
			tags[createTag(resourcePool, HostTag, hostname)] = true
		}
	}

	// Calculate size of common tags
	batch := resourcePool.getBatch()
	batch.CommonTags = tags
	batch.Metrics = []*m3thrift.Metric{}
	proto := resourcePool.getProto()
	batch.Write(proto)
	calc := proto.Transport().(*customtransport.TCalcTransport)
	numOverheadBytes := emitMetricBatchOverhead + calc.GetCount()
	calc.ResetCount()

	freeBytes := opts.MaxPacketSizeBytes - numOverheadBytes
	if freeBytes <= 0 {
		return nil, errCommonTagSize
	}

	r := &thriftReporter{
		client:       client,
		curBatch:     batch,
		calc:         calc,
		calcProto:    proto,
		commonTags:   tags,
		freeBytes:    freeBytes,
		resourcePool: resourcePool,
		metCh:        make(chan sizedMetric, opts.MaxQueueSize),
	}

	r.processors.Add(1)
	go r.process()

	return r, nil
}

// AllocateCounter implements tally.CachedStatsReporter.
func (r *thriftReporter) AllocateCounter(
	name string, tags map[string]string,
) tally.CachedCount {
	counter := r.newMetric(name, tags, counterType)
	size := r.calculateSize(counter)
	return cachedMetric{counter, r, size}
}

// AllocateGauge implements tally.CachedStatsReporter.
func (r *thriftReporter) AllocateGauge(
	name string, tags map[string]string,
) tally.CachedGauge {
	gauge := r.newMetric(name, tags, gaugeType)
	size := r.calculateSize(gauge)
	return cachedMetric{gauge, r, size}
}

// AllocateTimer implements tally.CachedStatsReporter.
func (r *thriftReporter) AllocateTimer(
	name string, tags map[string]string,
) tally.CachedTimer {
	timer := r.newMetric(name, tags, timerType)
	size := r.calculateSize(timer)
	return cachedMetric{timer, r, size}
}

func (r *thriftReporter) newMetric(
	name string,
	tags map[string]string,
	t metricType,
) *m3thrift.Metric {
	var (
		m      = r.resourcePool.getMetric()
		metVal = r.resourcePool.getValue()
	)
	m.Name = name
	if tags != nil {
		metTags := r.resourcePool.getTagList()
		for k, v := range tags {
			val := v
			metTag := r.resourcePool.getTag()
			metTag.TagName = k
			metTag.TagValue = &val
			metTags[metTag] = true
		}
		m.Tags = metTags
	}
	m.Timestamp = &maxInt64

	switch t {
	case counterType:
		c := r.resourcePool.getCount()
		c.I64Value = &maxInt64
		metVal.Count = c
	case gaugeType:
		g := r.resourcePool.getGauge()
		g.DValue = &maxFloat64
		metVal.Gauge = g
	case timerType:
		t := r.resourcePool.getTimer()
		t.I64Value = &maxInt64
		metVal.Timer = t
	}
	m.MetricValue = metVal

	return m
}

func (r *thriftReporter) calculateSize(m *m3thrift.Metric) int32 {
	r.calcLock.Lock()
	m.Write(r.calcProto)
	size := r.calc.GetCount()
	r.calc.ResetCount()
	r.calcLock.Unlock()
	return size
}

func (r *thriftReporter) reportCopyMetric(
	m *m3thrift.Metric,
	size int32,
	t metricType,
	iValue int64,
	dValue float64,
) {
	copy := r.resourcePool.getMetric()
	copy.Name = m.Name
	copy.Tags = m.Tags
	timestampNano := time.Now().UnixNano()
	copy.Timestamp = &timestampNano
	copy.MetricValue = r.resourcePool.getValue()

	switch t {
	case counterType:
		c := r.resourcePool.getCount()
		c.I64Value = &iValue
		copy.MetricValue.Count = c
	case gaugeType:
		g := r.resourcePool.getGauge()
		g.DValue = &dValue
		copy.MetricValue.Gauge = g
	case timerType:
		t := r.resourcePool.getTimer()
		t.I64Value = &iValue
		copy.MetricValue.Timer = t
	}

	select {
	case r.metCh <- sizedMetric{copy, size}:
	default:
	}
}

// Flush implements tally.CachedStatsReporter.
func (r *thriftReporter) Flush() {
	r.metCh <- sizedMetric{}
}

// Close waits for metrics to be flushed before closing the backend.
func (r *thriftReporter) Close() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("close error occurred: %v", r)
		}
	}()

	close(r.metCh)
	r.processors.Wait()
	return
}

func (r *thriftReporter) Capabilities() tally.Capabilities {
	return r
}

func (r *thriftReporter) Reporting() bool {
	return true
}

func (r *thriftReporter) Tagging() bool {
	return true
}

func (r *thriftReporter) process() {
	mets := make([]*m3thrift.Metric, 0, (r.freeBytes / 10))
	bytes := int32(0)

	for smet := range r.metCh {
		if smet.m == nil {
			// Explicit flush requested
			if len(mets) > 0 {
				mets = r.flush(mets)
				bytes = 0
			}
			continue
		}

		if bytes+smet.size > r.freeBytes {
			mets = r.flush(mets)
			bytes = 0
		}

		mets = append(mets, smet.m)
		bytes += smet.size
	}

	if len(mets) > 0 {
		// Final flush
		r.flush(mets)
	}

	r.processors.Done()
}

func (r *thriftReporter) flush(
	mets []*m3thrift.Metric,
) []*m3thrift.Metric {
	r.curBatchLock.Lock()
	r.curBatch.Metrics = mets
	r.client.EmitMetricBatch(r.curBatch)
	r.curBatch.Metrics = nil
	r.curBatchLock.Unlock()

	r.resourcePool.releaseShallowMetrics(mets)

	for i := range mets {
		mets[i] = nil
	}
	return mets[:0]
}

func createTag(
	pool *resourcePool,
	tagName, tagValue string,
) *m3thrift.MetricTag {
	tag := pool.getTag()
	tag.TagName = tagName
	if tagValue != "" {
		tag.TagValue = &tagValue
	}

	return tag
}

type cachedMetric struct {
	metric   *m3thrift.Metric
	reporter *thriftReporter
	size     int32
}

func (c cachedMetric) ReportCount(value int64) {
	c.reporter.reportCopyMetric(c.metric, c.size, counterType, value, 0)
}

func (c cachedMetric) ReportGauge(value float64) {
	c.reporter.reportCopyMetric(c.metric, c.size, gaugeType, 0, value)
}

func (c cachedMetric) ReportTimer(interval time.Duration) {
	val := int64(interval)
	c.reporter.reportCopyMetric(c.metric, c.size, timerType, val, 0)
}

type sizedMetric struct {
	m    *m3thrift.Metric
	size int32
}
