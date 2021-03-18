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
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/uber-go/tally"
	customtransport "github.com/uber-go/tally/m3/customtransports"
	m3thrift "github.com/uber-go/tally/m3/thrift/v2"
	"github.com/uber-go/tally/m3/thriftudp"
	"github.com/uber-go/tally/thirdparty/github.com/apache/thrift/lib/go/thrift"
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
	// ServiceTag is the name of the M3 service tag.
	ServiceTag = "service"
	// EnvTag is the name of the M3 env tag.
	EnvTag = "env"
	// HostTag is the name of the M3 host tag.
	HostTag = "host"
	// DefaultMaxQueueSize is the default M3 reporter queue size.
	DefaultMaxQueueSize = 4096
	// DefaultMaxPacketSize is the default M3 reporter max packet size.
	DefaultMaxPacketSize = int32(1440)
	// DefaultHistogramBucketIDName is the default histogram bucket ID tag name
	DefaultHistogramBucketIDName = "bucketid"
	// DefaultHistogramBucketName is the default histogram bucket name tag name
	DefaultHistogramBucketName = "bucket"
	// DefaultHistogramBucketTagPrecision is the default
	// precision to use when formatting the metric tag
	// with the histogram bucket bound values.
	DefaultHistogramBucketTagPrecision = uint(6)

	emitMetricBatchOverhead    = 19
	minMetricBucketIDTagLength = 4
)

var (
	_maxInt64   = int64(math.MaxInt64)
	_maxFloat64 = math.MaxFloat64
)

type metricType int

const (
	counterType metricType = iota + 1
	timerType
	gaugeType
)

var (
	errNoHostPorts   = errors.New("at least one entry for HostPorts is required")
	errCommonTagSize = errors.New("common tags serialized size exceeds packet size")
	errAlreadyClosed = errors.New("reporter already closed")
)

// Reporter is an M3 reporter.
type Reporter interface {
	tally.CachedStatsReporter
	io.Closer
}

// reporter is a metrics backend that reports metrics to a local or
// remote M3 collector, metrics are batched together and emitted
// via either thrift compact or binary protocol in batch UDP packets.
type reporter struct {
	bucketIDTagName string
	bucketTagName   string
	bucketValFmt    string
	calc            *customtransport.TCalcTransport
	calcLock        sync.Mutex
	calcProto       thrift.TProtocol
	client          *m3thrift.M3Client
	commonTags      []m3thrift.MetricTag
	freeBytes       int32
	metCh           chan sizedMetric
	processors      sync.WaitGroup
	resourcePool    *resourcePool
	status          reporterStatus
}

type reporterStatus struct {
	sync.RWMutex
	closed bool
}

// Options is a set of options for the M3 reporter.
type Options struct {
	HostPorts                   []string
	Service                     string
	Env                         string
	CommonTags                  map[string]string
	IncludeHost                 bool
	Protocol                    Protocol
	MaxQueueSize                int
	MaxPacketSizeBytes          int32
	HistogramBucketIDName       string
	HistogramBucketName         string
	HistogramBucketTagPrecision uint
}

// NewReporter creates a new M3 reporter.
func NewReporter(opts Options) (Reporter, error) {
	if opts.MaxQueueSize <= 0 {
		opts.MaxQueueSize = DefaultMaxQueueSize
	}
	if opts.MaxPacketSizeBytes <= 0 {
		opts.MaxPacketSizeBytes = DefaultMaxPacketSize
	}
	if opts.HistogramBucketIDName == "" {
		opts.HistogramBucketIDName = DefaultHistogramBucketIDName
	}
	if opts.HistogramBucketName == "" {
		opts.HistogramBucketName = DefaultHistogramBucketName
	}
	if opts.HistogramBucketTagPrecision == 0 {
		opts.HistogramBucketTagPrecision = DefaultHistogramBucketTagPrecision
	}

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

	var (
		client       = m3thrift.NewM3ClientFactory(trans, protocolFactory)
		resourcePool = newResourcePool(protocolFactory)
		tagm         = make(map[string]string)
		tags         = resourcePool.getMetricTagSlice()
	)

	// Create common tags
	for k, v := range opts.CommonTags {
		tagm[k] = v
	}

	if opts.CommonTags[ServiceTag] == "" {
		if opts.Service == "" {
			return nil, fmt.Errorf("%s common tag is required", ServiceTag)
		}
		tagm[ServiceTag] = opts.Service
	}

	if opts.CommonTags[EnvTag] == "" {
		if opts.Env == "" {
			return nil, fmt.Errorf("%s common tag is required", EnvTag)
		}
		tagm[EnvTag] = opts.Env
	}

	if opts.IncludeHost {
		if opts.CommonTags[HostTag] == "" {
			hostname, err := os.Hostname()
			if err != nil {
				return nil, errors.WithMessage(err, "error resolving host tag")
			}
			tagm[HostTag] = hostname
		}
	}

	for k, v := range tagm {
		tags = append(tags, m3thrift.MetricTag{
			Name:  k,
			Value: v,
		})
	}

	// Calculate size of common tags
	var (
		batch = m3thrift.MetricBatch{
			Metrics:    resourcePool.getMetricSlice(),
			CommonTags: tags,
		}
		proto = resourcePool.getProto()
	)

	if err := batch.Write(proto); err != nil {
		return nil, errors.WithMessage(
			err,
			"failed to write to proto for size calculation",
		)
	}

	resourcePool.releaseMetricSlice(batch.Metrics)

	var (
		calc             = proto.Transport().(*customtransport.TCalcTransport)
		numOverheadBytes = emitMetricBatchOverhead + calc.GetCount()
		freeBytes        = opts.MaxPacketSizeBytes - numOverheadBytes
	)
	calc.ResetCount()

	if freeBytes <= 0 {
		return nil, errCommonTagSize
	}

	r := &reporter{
		bucketIDTagName: opts.HistogramBucketIDName,
		bucketTagName:   opts.HistogramBucketName,
		bucketValFmt:    "%." + strconv.Itoa(int(opts.HistogramBucketTagPrecision)) + "f",
		calc:            calc,
		calcProto:       proto,
		client:          client,
		commonTags:      tags,
		freeBytes:       freeBytes,
		metCh:           make(chan sizedMetric, opts.MaxQueueSize),
		resourcePool:    resourcePool,
	}

	r.processors.Add(1)
	go r.process()

	return r, nil
}

// AllocateCounter implements tally.CachedStatsReporter.
func (r *reporter) AllocateCounter(
	name string,
	tags map[string]string,
) tally.CachedCount {
	return r.allocateCounter(name, tags)
}

func (r *reporter) allocateCounter(
	name string,
	tags map[string]string,
) cachedMetric {
	var (
		counter = r.newMetric(name, tags, counterType)
		size    = r.calculateSize(counter)
	)

	return cachedMetric{
		metric:   counter,
		reporter: r,
		size:     size,
	}
}

// DeallocateCounter implements tally.CachedStatsReporter.
func (r *reporter) DeallocateCounter(counter tally.CachedCount) {
	if counter == nil {
		return
	}

	c, ok := counter.(cachedMetric)
	if !ok {
		return
	}

	r.resourcePool.releaseMetricTagSlice(c.metric.Tags)
	c.metric.Tags = nil
}

// AllocateGauge implements tally.CachedStatsReporter.
func (r *reporter) AllocateGauge(
	name string,
	tags map[string]string,
) tally.CachedGauge {
	var (
		gauge = r.newMetric(name, tags, gaugeType)
		size  = r.calculateSize(gauge)
	)

	return cachedMetric{
		metric:   gauge,
		reporter: r,
		size:     size,
	}
}

// DeallocateGauge implements tally.CachedStatsReporter.
func (r *reporter) DeallocateGauge(gauge tally.CachedGauge) {
	if gauge == nil {
		return
	}

	g, ok := gauge.(cachedMetric)
	if !ok {
		return
	}

	r.resourcePool.releaseMetricTagSlice(g.metric.Tags)
	g.metric.Tags = nil
}

// AllocateTimer implements tally.CachedStatsReporter.
func (r *reporter) AllocateTimer(
	name string,
	tags map[string]string,
) tally.CachedTimer {
	var (
		timer = r.newMetric(name, tags, timerType)
		size  = r.calculateSize(timer)
	)

	return cachedMetric{
		metric:   timer,
		reporter: r,
		size:     size,
	}
}

// DeallocateTimer implements tally.CachedStatsReporter.
func (r *reporter) DeallocateTimer(timer tally.CachedTimer) {
	if timer == nil {
		return
	}

	t, ok := timer.(cachedMetric)
	if !ok {
		return
	}

	r.resourcePool.releaseMetricTagSlice(t.metric.Tags)
	t.metric.Tags = nil
}

// AllocateHistogram implements tally.CachedStatsReporter.
func (r *reporter) AllocateHistogram(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
) tally.CachedHistogram {
	var (
		_, isDuration = buckets.(tally.DurationBuckets)
		bucketIDLen   = int(math.Max(
			float64(len(strconv.Itoa(buckets.Len()))),
			float64(minMetricBucketIDTagLength),
		))
		bucketIDLenStr        = strconv.Itoa(bucketIDLen)
		bucketIDFmt           = "%0" + bucketIDLenStr + "d"
		htags                 = make(map[string]string, len(tags))
		cachedValueBuckets    []cachedHistogramBucket
		cachedDurationBuckets []cachedHistogramBucket
	)

	for k, v := range tags {
		htags[k] = v
	}

	for i, pair := range tally.BucketPairs(buckets) {
		var (
			counter    = r.allocateCounter(name, htags)
			idTagValue = fmt.Sprintf(bucketIDFmt, i)
			hbucket    = cachedHistogramBucket{
				valueUpperBound:    pair.UpperBoundValue(),
				durationUpperBound: pair.UpperBoundDuration(),
				metric:             counter,
			}
		)

		hbucket.metric.metric.Tags = append(
			hbucket.metric.metric.Tags,
			m3thrift.MetricTag{
				Name:  r.bucketIDTagName,
				Value: idTagValue,
			},
			m3thrift.MetricTag{
				Name: r.bucketTagName,
			},
		)

		bucketIdx := len(hbucket.metric.metric.Tags) - 1
		if isDuration {
			hbucket.metric.metric.Tags[bucketIdx].Value =
				r.durationBucketString(pair.LowerBoundDuration()) +
					"-" + r.durationBucketString(pair.UpperBoundDuration())
			cachedDurationBuckets = append(cachedDurationBuckets, hbucket)
		} else {
			hbucket.metric.metric.Tags[bucketIdx].Value =
				r.valueBucketString(pair.LowerBoundValue()) +
					"-" + r.valueBucketString(pair.UpperBoundValue())
			cachedValueBuckets = append(cachedValueBuckets, hbucket)
		}
	}

	return cachedHistogram{
		r:                     r,
		name:                  name,
		tags:                  tags,
		cachedValueBuckets:    cachedValueBuckets,
		cachedDurationBuckets: cachedDurationBuckets,
	}
}

// DeallocateHistogram implements tally.CachedStatsReporter.
func (r *reporter) DeallocateHistogram(histogram tally.CachedHistogram) {
	if histogram == nil {
		return
	}

	h, ok := histogram.(cachedHistogram)
	if !ok {
		return
	}

	for _, hbucket := range h.cachedDurationBuckets {
		r.DeallocateCounter(hbucket.metric)
	}
	h.cachedDurationBuckets = nil

	for _, hbucket := range h.cachedValueBuckets {
		r.DeallocateCounter(hbucket.metric)
	}
	h.cachedValueBuckets = nil
}

func (r *reporter) valueBucketString(v float64) string {
	if v == math.MaxFloat64 {
		return "infinity"
	}
	if v == -math.MaxFloat64 {
		return "-infinity"
	}
	return fmt.Sprintf(r.bucketValFmt, v)
}

func (r *reporter) durationBucketString(d time.Duration) string {
	if d == 0 {
		return "0"
	}
	if d == time.Duration(math.MaxInt64) {
		return "infinity"
	}
	if d == time.Duration(math.MinInt64) {
		return "-infinity"
	}
	return d.String()
}

func (r *reporter) newMetric(
	name string,
	tags map[string]string,
	t metricType,
) m3thrift.Metric {
	m := m3thrift.Metric{
		Name:      name,
		Timestamp: _maxInt64,
	}

	switch t {
	case counterType:
		m.Value.MetricType = m3thrift.MetricType_COUNTER
		m.Value.Count = _maxInt64
	case gaugeType:
		m.Value.MetricType = m3thrift.MetricType_GAUGE
		m.Value.Gauge = _maxFloat64
	case timerType:
		m.Value.MetricType = m3thrift.MetricType_TIMER
		m.Value.Timer = _maxInt64
	}

	if len(tags) == 0 {
		return m
	}

	m.Tags = r.resourcePool.getMetricTagSlice()
	for k, v := range tags {
		m.Tags = append(m.Tags, m3thrift.MetricTag{
			Name:  k,
			Value: v,
		})
	}

	return m
}

func (r *reporter) calculateSize(m m3thrift.Metric) int32 {
	r.calcLock.Lock()
	m.Write(r.calcProto) //nolint:errcheck
	size := r.calc.GetCount()
	r.calc.ResetCount()
	r.calcLock.Unlock()
	return size
}

func (r *reporter) reportCopyMetric(m m3thrift.Metric, size int32) {
	m.Timestamp = time.Now().UnixNano()

	sm := sizedMetric{
		m:    m,
		size: size,
		set:  true,
	}

	r.status.RLock()
	if !r.status.closed {
		select {
		case r.metCh <- sm:
		default:
			// TODO: don't drop when full, or add metric to track
		}
	}
	r.status.RUnlock()
}

// Flush sends an empty sizedMetric to signal a flush.
func (r *reporter) Flush() {
	r.status.RLock()
	if !r.status.closed {
		r.metCh <- sizedMetric{}
	}
	r.status.RUnlock()
}

// Close waits for metrics to be flushed before closing the backend.
func (r *reporter) Close() (err error) {
	r.status.Lock()
	if r.status.closed {
		r.status.Unlock()
		return errAlreadyClosed
	}

	r.status.closed = true
	close(r.metCh)
	r.status.Unlock()

	r.processors.Wait()

	return nil
}

func (r *reporter) Capabilities() tally.Capabilities {
	return r
}

func (r *reporter) Reporting() bool {
	return true
}

func (r *reporter) Tagging() bool {
	return true
}

func (r *reporter) process() {
	var (
		mets  = make([]m3thrift.Metric, 0, (r.freeBytes / 10))
		bytes int32
	)

	for smet := range r.metCh {
		if !smet.set {
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

func (r *reporter) flush(
	mets []m3thrift.Metric,
) []m3thrift.Metric {
	//nolint:errcheck
	r.client.EmitMetricBatchV2(m3thrift.MetricBatch{
		Metrics:    mets,
		CommonTags: r.commonTags,
	})

	return mets[:0]
}

type cachedMetric struct {
	metric   m3thrift.Metric
	reporter *reporter
	size     int32
}

func (c cachedMetric) ReportCount(value int64) {
	c.metric.Value.Count = value
	c.reporter.reportCopyMetric(c.metric, c.size)
}

func (c cachedMetric) ReportGauge(value float64) {
	c.metric.Value.Gauge = value
	c.reporter.reportCopyMetric(c.metric, c.size)
}

func (c cachedMetric) ReportTimer(interval time.Duration) {
	c.metric.Value.Timer = int64(interval)
	c.reporter.reportCopyMetric(c.metric, c.size)
}

func (c cachedMetric) ReportSamples(value int64) {
	c.metric.Value.Count = value
	c.reporter.reportCopyMetric(c.metric, c.size)
}

type noopMetric struct {
}

func (c noopMetric) ReportCount(value int64) {
}

func (c noopMetric) ReportGauge(value float64) {
}

func (c noopMetric) ReportTimer(interval time.Duration) {
}

func (c noopMetric) ReportSamples(value int64) {
}

type cachedHistogram struct {
	r                     *reporter
	name                  string
	tags                  map[string]string
	cachedValueBuckets    []cachedHistogramBucket
	cachedDurationBuckets []cachedHistogramBucket
}

type cachedHistogramBucket struct {
	valueUpperBound    float64
	durationUpperBound time.Duration
	metric             cachedMetric
}

func (h cachedHistogram) ValueBucket(
	bucketLowerBound float64,
	bucketUpperBound float64,
) tally.CachedHistogramBucket {
	var (
		n   = len(h.cachedValueBuckets)
		idx = sort.Search(n, func(i int) bool {
			return h.cachedValueBuckets[i].valueUpperBound >= bucketUpperBound
		})
	)

	if idx == n {
		return noopMetric{}
	}

	return h.cachedValueBuckets[idx].metric
}

func (h cachedHistogram) DurationBucket(
	bucketLowerBound time.Duration,
	bucketUpperBound time.Duration,
) tally.CachedHistogramBucket {
	var (
		n   = len(h.cachedDurationBuckets)
		idx = sort.Search(n, func(i int) bool {
			return h.cachedDurationBuckets[i].durationUpperBound >= bucketUpperBound
		})
	)

	if idx == n {
		return noopMetric{}
	}

	return h.cachedDurationBuckets[idx].metric
}

type sizedMetric struct {
	m    m3thrift.Metric
	size int32
	set  bool
}
