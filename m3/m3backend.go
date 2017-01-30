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
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/uber-common/bark"
	"github.com/uber-go/tally"
	"github.com/uber-go/tally/m3/customtransports"
	"github.com/uber-go/tally/m3/idgeneration"
	"github.com/uber-go/tally/m3/thrift"
	"github.com/uber-go/tally/m3/thriftudp"
)

// Protocol represent a thrift transport protocol
type Protocol int

// Compact and Binary represent the compact and binary thrift protocols respectively
const (
	Compact Protocol = iota
	Binary
)

const (
	// MaxTags is the max number of tags per metric
	MaxTags                 = 10
	emitMetricBatchOverhead = 19
	serviceTag              = "service"
	envTag                  = "env"
	hostTag                 = "host"
)

// initializng this in this file's init function to get around lint error
var maxInt64 int64

func init() {
	maxInt64 = math.MaxInt64
}

var (
	errMaxQueueSize       = errors.New("maxQueueSize must be greater than zero")
	errMaxPacketSizeBytes = errors.New("maxPacketSizeBytes must be greater than zero")
	errCommonTagSize      = errors.New("common tag sizes exceeds packet size")
)

// m3Backend is a metrics backend that reports metrics to the local M3 collector
// Metrics are buffered, batched up and emitted via the Thrift TCompact protocol over UDP
type m3Backend struct {
	client       *m3.M3Client
	curBatch     *m3.MetricBatch
	curBatchLock sync.Mutex
	commonTags   map[*m3.MetricTag]bool
	freeBytes    int32
	processors   sync.WaitGroup
	resourcePool *m3ResourcePool
	closeChan    chan struct{}

	counters *aggMets
	gauges   *aggMets
	timers   *aggMets

	metCh   chan *sizedMetric
	timerCh chan *timerMetric
}

// NewM3Backend creates a new M3Backend
func NewM3Backend(
	hostPort string,
	service string,
	commonTags map[string]string,
	includeHost bool,
	maxQueueSize int,
	maxPacketSizeBytes int32,
	interval time.Duration,
) (BufferedBackend, error) {
	return newMultiM3BackendWithProtocol(backendOptions{
		hostPorts:          []string{hostPort},
		service:            service,
		commonTags:         commonTags,
		includeHost:        includeHost,
		maxQueueSize:       maxQueueSize,
		maxPacketSizeBytes: maxPacketSizeBytes,
		interval:           interval,
		protocol:           Compact,
	})
}

// NewMultiM3Backend creates a new M3Backend with multiple destinations
func NewMultiM3Backend(
	hostPorts []string,
	service string,
	commonTags map[string]string,
	includeHost bool,
	maxQueueSize int,
	maxPacketSizeBytes int32,
	interval time.Duration,
) (BufferedBackend, error) {
	return newMultiM3BackendWithProtocol(backendOptions{
		hostPorts:          hostPorts,
		service:            service,
		commonTags:         commonTags,
		includeHost:        includeHost,
		maxQueueSize:       maxQueueSize,
		maxPacketSizeBytes: maxPacketSizeBytes,
		interval:           interval,
		protocol:           Compact,
	})
}

// NewMultiM3BackendWithProtocol creates a new M3Backend with multiple destinations using the
// given protocol
func NewMultiM3BackendWithProtocol(
	hostPorts []string,
	service string,
	commonTags map[string]string,
	includeHost bool,
	maxQueueSize int,
	maxPacketSizeBytes int32,
	interval time.Duration,
	protocol Protocol,
) (BufferedBackend, error) {
	return newMultiM3BackendWithProtocol(backendOptions{
		hostPorts:          hostPorts,
		service:            service,
		commonTags:         commonTags,
		includeHost:        includeHost,
		maxQueueSize:       maxQueueSize,
		maxPacketSizeBytes: maxPacketSizeBytes,
		interval:           interval,
		protocol:           protocol,
	})
}

type backendOptions struct {
	hostPorts          []string
	service            string
	commonTags         map[string]string
	includeHost        bool
	maxQueueSize       int
	maxPacketSizeBytes int32
	interval           time.Duration
	protocol           Protocol
}

func newMultiM3BackendWithProtocol(opts backendOptions) (*m3Backend, error) {
	if opts.maxQueueSize <= 0 {
		return nil, errMaxQueueSize
	}
	if opts.maxPacketSizeBytes <= 0 {
		return nil, errMaxPacketSizeBytes
	}

	// M3 thrift client
	var trans thrift.TTransport
	var err error
	if len(opts.hostPorts) == 1 {
		trans, err = thriftudp.NewTUDPClientTransport(opts.hostPorts[0], "")
	} else {
		trans, err = thriftudp.NewTMultiUDPClientTransport(opts.hostPorts, "")
	}
	if err != nil {
		return nil, err
	}

	var protocolFactory thrift.TProtocolFactory
	if opts.protocol == Compact {
		protocolFactory = thrift.NewTCompactProtocolFactory()
	} else {
		protocolFactory = thrift.NewTBinaryProtocolFactoryDefault()
	}

	client := m3.NewM3ClientFactory(trans, protocolFactory)
	resourcePool := newM3ResourcePool(protocolFactory)

	//create common tags
	tags := resourcePool.getTagList()
	for k, v := range opts.commonTags {
		tags[createTag(resourcePool, k, v)] = true
	}
	if opts.commonTags[serviceTag] == "" {
		tags[createTag(resourcePool, serviceTag, opts.service)] = true
	}
	if opts.commonTags[envTag] == "" {
		panic("m3backend: commontags[env] is required")
		// tags[createTag(resourcePool, envTag, config.GetEnvironment())] = true
	}
	if opts.includeHost {
		if opts.commonTags[hostTag] == "" {
			panic("m3backend: commonTags[host] is required")
		}
		// tags[createTag(resourcePool, hostTag, config.GetHostname())] = true
	}

	//calculate size of common tags
	batch := resourcePool.getBatch()
	batch.CommonTags = tags
	batch.Metrics = []*m3.Metric{}
	proto := resourcePool.getProto()
	batch.Write(proto)
	calc := proto.Transport().(*customtransport.TCalcTransport)
	numOverheadBytes := emitMetricBatchOverhead + calc.GetCount()
	calc.ResetCount()
	resourcePool.releaseProto(proto)

	freeBytes := opts.maxPacketSizeBytes - numOverheadBytes
	if freeBytes <= 0 {
		return nil, errCommonTagSize
	}

	m3 := &m3Backend{client: client,
		curBatch:     batch,
		commonTags:   tags,
		freeBytes:    freeBytes,
		resourcePool: resourcePool,
		counters:     newAggMets(resourcePool, updateCounterVal),
		gauges:       newAggMets(resourcePool, updateGaugeVal),
		timers:       newAggMets(resourcePool, noopVal),
		metCh:        make(chan *sizedMetric, opts.maxQueueSize),
		timerCh:      make(chan *timerMetric, opts.maxQueueSize),
		closeChan:    make(chan struct{}),
	}

	go m3.processTallyMetrics()
	m3.processors.Add(1)

	if opts.interval > 0 {
		go m3.reportEvery(opts.interval)
		go m3.processTimers(opts.interval)
		m3.processors.Add(2)
	}

	return m3, nil
}

// RegisterForID registers a metric name and tag combination with the M3 backend
func (b *m3Backend) RegisterForID(name string, tags bark.Tags, t MetricType) MetricID {
	if len(tags) > MaxTags {
		return nil
	}

	switch t {
	case CounterType:
		return b.counters.getOrAddID(name, tags, t)
	case GaugeType:
		return b.gauges.getOrAddID(name, tags, t)
	case TimerType:
		return b.timers.getOrAddID(name, tags, t)
	default:
		panic("Unknown MetricType")
	}
}

// GetForID returns the metric if it's present otherwise nil
func (b *m3Backend) GetForID(name string, tags bark.Tags, t MetricType) (MetricID, bool) {
	if len(tags) > MaxTags {
		return nil, false
	}
	switch t {
	case CounterType:
		return b.counters.getID(name, tags)
	case GaugeType:
		return b.gauges.getID(name, tags)
	case TimerType:
		return b.timers.getID(name, tags)
	default:
		panic("Unknown MetricType")
	}
}

// IncCounter increments a counter value
func (b *m3Backend) IncCounter(id MetricID, value int64) {
	c, ok := id.(*counterMetricID)
	if !ok {
		return
	}

	atomic.AddInt64(&c.val, value)
}

// UpdateGauge updates the value of a gauge
func (b *m3Backend) UpdateGauge(id MetricID, value int64) {
	g, ok := id.(*gaugeMetricID)
	if !ok {
		return
	}

	atomic.StoreInt64(&g.val, value)
	atomic.StoreUint32(&g.updated, 1)
}

// RecordTimer records a timing duration
func (b *m3Backend) RecordTimer(id MetricID, d time.Duration) {
	t, ok := id.(*timerMetricID)
	if !ok {
		return
	}

	timerMet := b.resourcePool.getTimerMetric()
	timerMet.id = t
	timerMet.val = d.Nanoseconds()

	select {
	case b.timerCh <- timerMet:
	default:
	}
}

// ReportCounter for compatability with tally metrics
func (b *m3Backend) ReportCounter(name string, tags map[string]string, value int64) {
	counter := b.counters.newMetric(name, tags, CounterType)
	counter.MetricValue.Count.I64Value = &value
	b.counters.Lock()
	size := b.counters.calculateSize(counter)
	b.counters.Unlock()
	b.reportMetric(counter, size)
}

// AllocateCounter for compatibility with tally metrics
func (b *m3Backend) AllocateCounter(
	name string, tags map[string]string,
) tally.CachedCount {
	counter := b.counters.newMetric(name, tags, CounterType)
	b.counters.Lock()
	size := b.counters.calculateSize(counter)
	b.counters.Unlock()
	return cachedMetric{counter, b, size}
}

type cachedMetric struct {
	metric  *m3.Metric
	backend *m3Backend
	size    int32
}

func (c cachedMetric) ReportCount(value int64) {
	c.backend.reportCopyMetric(c.metric, c.size, CounterType, value, 0)
}

func (c cachedMetric) ReportGauge(value float64) {
	c.backend.reportCopyMetric(c.metric, c.size, GaugeType, 0, value)
}

func (c cachedMetric) ReportTimer(interval time.Duration) {
	val := interval.Nanoseconds()
	c.backend.reportCopyMetric(c.metric, c.size, TimerType, val, 0)
}

// ReportGauge for compatability with tally metrics
func (b *m3Backend) ReportGauge(name string, tags map[string]string, value float64) {
	gauge := b.gauges.newMetric(name, tags, GaugeType)
	gauge.MetricValue.Gauge.DValue = &value
	b.gauges.Lock()
	size := b.gauges.calculateSize(gauge)
	b.gauges.Unlock()
	b.reportMetric(gauge, size)
}

// AllocateGauge for compatability with tally metrics
func (b *m3Backend) AllocateGauge(
	name string, tags map[string]string,
) tally.CachedGauge {
	gauge := b.gauges.newMetric(name, tags, GaugeType)
	b.gauges.Lock()
	size := b.gauges.calculateSize(gauge)
	b.gauges.Unlock()
	return cachedMetric{gauge, b, size}
}

// ReportTimer for compatability with tally metrics
func (b *m3Backend) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	timer := b.timers.newMetric(name, tags, TimerType)
	val := interval.Nanoseconds()
	timer.MetricValue.Timer.I64Value = &val
	b.timers.Lock()
	size := b.timers.calculateSize(timer)
	b.timers.Unlock()
	b.reportMetric(timer, size)
}

// AllocateTimer for compatability with tally metrics
func (b *m3Backend) AllocateTimer(
	name string, tags map[string]string,
) tally.CachedTimer {
	timer := b.timers.newMetric(name, tags, TimerType)
	b.timers.Lock()
	size := b.timers.calculateSize(timer)
	b.timers.Unlock()
	return cachedMetric{timer, b, size}
}

func (b *m3Backend) reportCopyMetric(
	m *m3.Metric, size int32, t MetricType, iValue int64, dValue float64,
) {
	copy := b.resourcePool.getMetric()
	copy.Name = m.Name
	copy.Tags = m.Tags
	timestampNano := time.Now().UnixNano()
	copy.Timestamp = &timestampNano
	copy.MetricValue = b.resourcePool.getValue()

	switch t {
	case CounterType:
		c := b.resourcePool.getCount()
		c.I64Value = &iValue
		copy.MetricValue.Count = c
	case GaugeType:
		g := b.resourcePool.getGauge()
		g.DValue = &dValue
		copy.MetricValue.Gauge = g
	case TimerType:
		t := b.resourcePool.getTimer()
		t.I64Value = &iValue
		copy.MetricValue.Timer = t
	}

	sizedMet := b.resourcePool.getSizedMetric()
	sizedMet.m = copy
	sizedMet.size = size
	sizedMet.releaseShallow = true

	select {
	case b.metCh <- sizedMet:
	default:
		// TODO(jra3): Record/Log this
	}
}

func (b *m3Backend) reportMetric(m *m3.Metric, size int32) {
	timestampNano := time.Now().UnixNano()
	m.Timestamp = &timestampNano

	sizedMet := b.resourcePool.getSizedMetric()
	sizedMet.m = m
	sizedMet.size = size
	sizedMet.releaseShallow = false

	select {
	case b.metCh <- sizedMet:
	default:
		// TODO(jra3): Record/Log this
	}
}

func (b *m3Backend) Flush() {
	b.metCh <- nil
}

// Close waits for metrics to be flushed before closing the backend
func (b *m3Backend) Close() {
	close(b.closeChan)
	close(b.timerCh)
	close(b.metCh)
	b.processors.Wait()
}

func (b *m3Backend) Capabilities() tally.Capabilities {
	return taggingCapabilities
}

// emitAggregations flushes the counters and gauges that the client
// has been buffering
func (b *m3Backend) emitAggregations() {
	var wg sync.WaitGroup

	for _, agg := range []*aggMets{b.counters, b.gauges} {
		wg.Add(1)
		curAgg := agg
		go func() {
			mets := make([]*m3.Metric, 0, (b.freeBytes / 10))
			var bytes int32

			mids, cleanup := curAgg.getAllUpdates()
			for i := range mids {
				mid := mids[i]

				if bytes+mid.getSize() > b.freeBytes {
					mets = b.flush(mets, false, false)
					bytes = 0
				}

				mets = append(mets, mid.getMet())
				bytes += mid.getSize()

			}

			if len(mets) > 0 {
				b.flush(mets, false, false)
			}

			if cleanup {
				go curAgg.clearUnregistered()
			}

			wg.Done()
		}()
	}

	wg.Wait()
}

type tallyMetricsInfo struct {
	mets               []*m3.Metric
	lastReleaseShallow bool
}

func (b *m3Backend) processTallyMetrics() {

	mets := make([]*m3.Metric, 0, (b.freeBytes / 10))
	info := &tallyMetricsInfo{
		mets:               mets,
		lastReleaseShallow: false,
	}
	bytes := int32(0)

	for {
		select {
		case smet, ok := <-b.metCh:
			info, bytes = b.processTallyMetric(info, bytes, smet)
			if !ok {
				b.processors.Done()
				return
			}
		}
	}
}

func (b *m3Backend) processTallyMetric(
	info *tallyMetricsInfo,
	bytes int32,
	smet *sizedMetric,
) (*tallyMetricsInfo, int32) {

	if smet == nil {
		if len(info.mets) > 0 {
			// timer ticked, or channel was closed
			info.mets = b.flush(info.mets, true, info.lastReleaseShallow)
			bytes = 0
		}
	} else {

		if bytes+smet.size > b.freeBytes {
			info.mets = b.flush(info.mets, true, info.lastReleaseShallow)
			bytes = 0
		}

		info.mets = append(info.mets, smet.m)
		info.lastReleaseShallow = smet.releaseShallow
		bytes += smet.size

		b.resourcePool.releaseSizedMetric(smet)
	}
	return info, bytes
}

// ReportEvery flushes aggregated metrics periodically
func (b *m3Backend) reportEvery(interval time.Duration) {
	reportTicker := time.Tick(interval)

	for {
		select {
		case <-reportTicker:
			b.emitAggregations()
		case <-b.closeChan:
			b.emitAggregations()
			b.processors.Done()
			return
		}
	}

}

func (b *m3Backend) processTimers(interval time.Duration) {
	reportTicker := time.Tick(interval)
	var bytes int32
	timerBatch := make([]*m3.Metric, 0, (b.freeBytes / 10))

	for {
		select {
		case timerMet := <-b.timerCh:
			if timerMet == nil {
				if len(timerBatch) > 0 {
					// channel closed
					b.flush(timerBatch, true, false)
				}
				b.processors.Done()
				return
			}

			if bytes+timerMet.id.size > b.freeBytes {
				timerBatch = b.flush(timerBatch, true, false)
				bytes = 0
			}

			copyMet := b.copyTimerMetric(timerMet.id.met)
			val := timerMet.val
			copyMet.MetricValue.Timer.I64Value = &val
			timerBatch = append(timerBatch, copyMet)
			bytes += timerMet.id.size
			b.resourcePool.releaseTimerMetric(timerMet)

		case <-reportTicker:
			if len(timerBatch) > 0 {
				timerBatch = b.flush(timerBatch, true, false)
				bytes = 0
			}
		}
	}
}

func (b *m3Backend) flush(
	mets []*m3.Metric,
	release bool,
	releaseShallow bool,
) []*m3.Metric {
	b.curBatchLock.Lock()
	b.curBatch.Metrics = mets
	b.client.EmitMetricBatch(b.curBatch)
	b.curBatch.Metrics = nil
	b.curBatchLock.Unlock()

	if release && releaseShallow {
		b.resourcePool.releaseShallowMetrics(mets)
	} else if release {
		b.resourcePool.releaseMetrics(mets)
	}
	return mets[:0]
}

// copyTimerMetric copies a timer metric object and sets its
// value to the value provided
func (b *m3Backend) copyTimerMetric(met *m3.Metric) *m3.Metric {
	copyMet := b.resourcePool.getMetric()
	copyMet.Name = met.Name
	if met.Tags != nil {
		copyTags := b.resourcePool.getTagList()
		for tag := range met.Tags {
			copyTag := b.resourcePool.getTag()
			copyTag.TagName = tag.TagName
			copyTag.TagValue = tag.TagValue
			copyTags[copyTag] = true
		}
		copyMet.Tags = copyTags
	}

	copyMet.MetricValue = b.resourcePool.getValue()
	copyMet.MetricValue.Timer = b.resourcePool.getTimer()

	return copyMet
}

func createTag(pool *m3ResourcePool, tagName string, tagValue string) *m3.MetricTag {
	tag := pool.getTag()
	tag.TagName = tagName
	if tagValue != "" {
		tag.TagValue = &tagValue
	}

	return tag
}

type sizedMetric struct {
	m              *m3.Metric
	size           int32
	releaseShallow bool
}

type timerMetric struct {
	id  *timerMetricID
	val int64
}

type aggMet interface {
	getMet() *m3.Metric
	getSize() int32
	isRegistered() bool
	UnregisterID()
}

type counterMetricID struct {
	val        int64
	prev       int64
	registered uint32
	met        *m3.Metric
	size       int32
}

func (c *counterMetricID) getMet() *m3.Metric {
	return c.met
}

func (c *counterMetricID) getSize() int32 {
	return c.size
}

func (c *counterMetricID) isRegistered() bool {
	return atomic.LoadUint32(&c.registered) == 1
}

func (c *counterMetricID) UnregisterID() {
	atomic.StoreUint32(&c.registered, 0)
}

type gaugeMetricID struct {
	val        int64
	updated    uint32
	registered uint32
	met        *m3.Metric
	size       int32
}

func (g *gaugeMetricID) getMet() *m3.Metric {
	return g.met
}

func (g *gaugeMetricID) getSize() int32 {
	return g.size
}

func (g *gaugeMetricID) isRegistered() bool {
	return atomic.LoadUint32(&g.registered) == 1
}

func (g *gaugeMetricID) UnregisterID() {
	atomic.StoreUint32(&g.registered, 0)
}

type timerMetricID struct {
	registered uint32
	met        *m3.Metric
	size       int32
}

func (t *timerMetricID) getMet() *m3.Metric {
	return t.met
}

func (t *timerMetricID) getSize() int32 {
	return t.size
}

func (t *timerMetricID) isRegistered() bool {
	return atomic.LoadUint32(&t.registered) == 1
}

func (t *timerMetricID) UnregisterID() {
	atomic.StoreUint32(&t.registered, 0)
}

type aggMets struct {
	mets         map[string]aggMet
	res          *m3ResourcePool
	calc         *customtransport.TCalcTransport
	calcProto    thrift.TProtocol
	updateMetVal updateMetricValFunc
	sync.RWMutex
}

func newAggMets(res *m3ResourcePool, uf updateMetricValFunc) *aggMets {
	proto := res.getProto()
	return &aggMets{
		mets:         map[string]aggMet{},
		res:          res,
		calc:         proto.Transport().(*customtransport.TCalcTransport),
		calcProto:    proto,
		updateMetVal: uf,
	}
}

// newMetric returns a new, naked m3.Metric for use in either metric reporting path
func (a *aggMets) newMetric(name string, tags map[string]string, t MetricType) *m3.Metric {

	var (
		m      = a.res.getMetric()
		metVal = a.res.getValue()
	)

	m.Name = name
	if tags != nil {
		metTags := a.res.getTagList()
		for k, v := range tags {
			val := v
			metTag := a.res.getTag()
			metTag.TagName = k
			metTag.TagValue = &val
			metTags[metTag] = true
		}
		m.Tags = metTags
	}

	switch t {
	case CounterType:
		c := a.res.getCount()
		metVal.Count = c
	case GaugeType:
		g := a.res.getGauge()
		metVal.Gauge = g
	case TimerType:
		t := a.res.getTimer()
		metVal.Timer = t
	}
	m.MetricValue = metVal

	return m
}

func (a *aggMets) calculateSize(m *m3.Metric) int32 {
	m.Write(a.calcProto)
	size := a.calc.GetCount()
	a.calc.ResetCount()
	return size
}

func (a *aggMets) getOrAddID(name string, tags bark.Tags, t MetricType) MetricID {
	key := idgeneration.Get(name, tags)
	a.RLock()
	mid := a.mets[key]
	a.RUnlock()

	if mid != nil {
		return mid
	}

	a.Lock()
	defer a.Unlock()

	mid = a.mets[key]
	if mid != nil {
		return mid
	}

	m := a.newMetric(name, tags, t)
	val := int64(0)

	switch t {
	case CounterType:
		m.MetricValue.Count.I64Value = &maxInt64
		size := a.calculateSize(m)
		m.MetricValue.Count.I64Value = &val
		mid = a.res.getCounterID(m, size)
	case GaugeType:
		m.MetricValue.Gauge.I64Value = &maxInt64
		size := a.calculateSize(m)
		m.MetricValue.Gauge.I64Value = &val
		mid = a.res.getGaugeID(m, size)
	case TimerType:
		m.MetricValue.Timer.I64Value = &maxInt64
		size := a.calculateSize(m)
		m.MetricValue.Timer.I64Value = &val
		mid = a.res.getTimerID(m, size)
	}

	a.mets[key] = mid
	return mid
}

func (a *aggMets) getID(name string, tags bark.Tags) (MetricID, bool) {
	key := idgeneration.Get(name, tags)
	a.RLock()
	mid, ok := a.mets[key]
	a.RUnlock()

	return mid, ok
}

func (a *aggMets) getAllUpdates() ([]aggMet, bool) {
	itemsToRemove := false
	a.RLock()
	mets := make([]aggMet, 0, len(a.mets))
	for _, met := range a.mets {
		if a.updateMetVal(met) {
			mets = append(mets, met)
		} else if !met.isRegistered() {
			itemsToRemove = true
		}
	}
	a.RUnlock()

	return mets, itemsToRemove
}

func (a *aggMets) clearUnregistered() {
	a.Lock()
	for key, met := range a.mets {
		if !met.isRegistered() {
			delete(a.mets, key)
			a.res.releaseAggMet(met)
		}
	}
	a.Unlock()
}

type updateMetricValFunc func(m aggMet) bool

func updateCounterVal(m aggMet) bool {
	c := m.(*counterMetricID)
	cur := atomic.LoadInt64(&c.val)
	if cur-c.prev > 0 {
		*c.met.MetricValue.Count.I64Value = cur - c.prev
		c.prev = cur
		return true
	}

	return false
}

func updateGaugeVal(m aggMet) bool {
	g := m.(*gaugeMetricID)
	if atomic.SwapUint32(&g.updated, 0) == 1 {
		*g.met.MetricValue.Gauge.I64Value = atomic.LoadInt64(&g.val)
		return true
	}

	return false
}

func noopVal(m aggMet) bool { return false }
