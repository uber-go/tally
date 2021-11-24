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
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/errors"
	tally "github.com/uber-go/tally/v4"
	"github.com/uber-go/tally/v4/internal/cache"
	customtransport "github.com/uber-go/tally/v4/m3/customtransports"
	m3thrift "github.com/uber-go/tally/v4/m3/thrift/v2"
	"github.com/uber-go/tally/v4/m3/thriftudp"
	"github.com/uber-go/tally/v4/thirdparty/github.com/apache/thrift/lib/go/thrift"
	"go.uber.org/atomic"
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
	DefaultMaxPacketSize = int32(32768)
	// DefaultHistogramBucketIDName is the default histogram bucket ID tag name
	DefaultHistogramBucketIDName = "bucketid"
	// DefaultHistogramBucketName is the default histogram bucket name tag name
	DefaultHistogramBucketName = "bucket"
	// DefaultHistogramBucketTagPrecision is the default
	// precision to use when formatting the metric tag
	// with the histogram bucket bound values.
	DefaultHistogramBucketTagPrecision = uint(6)

	_emitMetricBatchOverhead    = 19
	_minMetricBucketIDTagLength = 4
	_timeResolution             = 100 * time.Millisecond
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
	buckets         []tally.BucketPair
	calc            *customtransport.TCalcTransport
	calcLock        sync.Mutex
	calcProto       thrift.TProtocol
	client          *m3thrift.M3Client
	commonTags      []m3thrift.MetricTag
	done            atomic.Bool
	donech          chan struct{}
	freeBytes       int32
	metCh           chan sizedMetric
	now             atomic.Int64
	overheadBytes   int32
	pending         atomic.Uint64
	resourcePool    *resourcePool
	stringInterner  *cache.StringInterner
	tagCache        *cache.TagCache
	wg              sync.WaitGroup

	batchSizeHistogram    tally.CachedHistogram
	numBatches            atomic.Int64
	numBatchesCounter     tally.CachedCount
	numMetrics            atomic.Int64
	numMetricsCounter     tally.CachedCount
	numWriteErrors        atomic.Int64
	numWriteErrorsCounter tally.CachedCount
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
		numOverheadBytes = _emitMetricBatchOverhead + calc.GetCount()
		freeBytes        = opts.MaxPacketSizeBytes - numOverheadBytes
	)
	calc.ResetCount()

	if freeBytes <= 0 {
		return nil, errCommonTagSize
	}

	buckets := tally.ValueBuckets(append(
		[]float64{0.0},
		tally.MustMakeExponentialValueBuckets(2.0, 2.0, 11)...,
	))

	r := &reporter{
		buckets:         tally.BucketPairs(buckets),
		bucketIDTagName: opts.HistogramBucketIDName,
		bucketTagName:   opts.HistogramBucketName,
		bucketValFmt:    "%." + strconv.Itoa(int(opts.HistogramBucketTagPrecision)) + "f",
		calc:            calc,
		calcProto:       proto,
		client:          client,
		commonTags:      tags,
		donech:          make(chan struct{}),
		freeBytes:       freeBytes,
		metCh:           make(chan sizedMetric, opts.MaxQueueSize),
		overheadBytes:   numOverheadBytes,
		resourcePool:    resourcePool,
		stringInterner:  cache.NewStringInterner(),
		tagCache:        cache.NewTagCache(),
	}

	internalTags := map[string]string{
		"version": tally.Version,
	}
	r.batchSizeHistogram = r.AllocateHistogram("tally.internal.batch-size", internalTags, buckets)
	r.numBatchesCounter = r.AllocateCounter("tally.internal.num-batches", internalTags)
	r.numMetricsCounter = r.AllocateCounter("tally.internal.num-metrics", internalTags)
	r.numWriteErrorsCounter = r.AllocateCounter("tally.internal.num-write-errors", internalTags)

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.process()
	}()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.timeLoop()
	}()

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

// AllocateHistogram implements tally.CachedStatsReporter.
func (r *reporter) AllocateHistogram(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
) tally.CachedHistogram {
	var (
		_, isDuration = buckets.(tally.DurationBuckets)
		bucketIDLen   = int(math.Max(
			float64(ndigits(buckets.Len())),
			float64(_minMetricBucketIDTagLength),
		))
		bucketIDFmt           = "%0" + strconv.Itoa(bucketIDLen) + "d"
		cachedValueBuckets    []cachedHistogramBucket
		cachedDurationBuckets []cachedHistogramBucket
	)

	var (
		mtags        = r.convertTags(tags)
		prevDuration = time.Duration(math.MinInt64)
		prevValue    = -math.MaxFloat64
	)
	for i, pair := range tally.BucketPairs(buckets) {
		var (
			counter = r.allocateCounter(name, nil)
			hbucket = cachedHistogramBucket{
				bucketID:           r.stringInterner.Intern(fmt.Sprintf(bucketIDFmt, i)),
				valueUpperBound:    pair.UpperBoundValue(),
				durationUpperBound: pair.UpperBoundDuration(),
				metric:             &counter,
			}
			delta = len(r.bucketIDTagName) + len(r.bucketTagName) + len(hbucket.bucketID)
		)

		hbucket.metric.metric.Tags = mtags
		hbucket.metric.size = r.calculateSize(hbucket.metric.metric)

		if isDuration {
			bname := r.stringInterner.Intern(
				r.durationBucketString(prevDuration) + "-" +
					r.durationBucketString(pair.UpperBoundDuration()),
			)
			hbucket.bucket = bname
			hbucket.metric.size += int32(delta + len(bname))
			cachedDurationBuckets = append(cachedDurationBuckets, hbucket)
		} else {
			bname := r.stringInterner.Intern(
				r.valueBucketString(prevValue) + "-" +
					r.valueBucketString(pair.UpperBoundValue()),
			)
			hbucket.bucket = bname
			hbucket.metric.size += int32(delta + len(bname))
			cachedValueBuckets = append(cachedValueBuckets, hbucket)
		}

		prevDuration = pair.UpperBoundDuration()
		prevValue = pair.UpperBoundValue()
	}

	return cachedHistogram{
		r:                     r,
		name:                  name,
		cachedValueBuckets:    cachedValueBuckets,
		cachedDurationBuckets: cachedDurationBuckets,
	}
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
		Name:      r.stringInterner.Intern(name),
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

	m.Tags = r.convertTags(tags)
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

func (r *reporter) reportCopyMetric(
	m m3thrift.Metric,
	size int32,
	bucket string,
	bucketID string,
) {
	r.pending.Inc()
	defer r.pending.Dec()

	if r.done.Load() {
		return
	}

	m.Timestamp = r.now.Load()

	sm := sizedMetric{
		m:        m,
		size:     size,
		set:      true,
		bucket:   bucket,
		bucketID: bucketID,
	}

	select {
	case r.metCh <- sm:
	case <-r.donech:
	}
}

// Flush sends an empty sizedMetric to signal a flush.
func (r *reporter) Flush() {
	r.pending.Inc()
	defer r.pending.Dec()

	if r.done.Load() {
		return
	}

	r.reportInternalMetrics()
	r.metCh <- sizedMetric{}
}

// Close waits for metrics to be flushed before closing the backend.
func (r *reporter) Close() (err error) {
	if !r.done.CAS(false, true) {
		return errAlreadyClosed
	}

	// Wait for any pending reports to complete.
	for r.pending.Load() > 0 {
		runtime.Gosched()
	}

	close(r.donech)
	close(r.metCh)
	r.wg.Wait()

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
		extraTags = sync.Pool{
			New: func() interface{} {
				return make([]m3thrift.MetricTag, 0, 8)
			},
		}
		borrowedTags = make([][]m3thrift.MetricTag, 0, 128)
		mets         = make([]m3thrift.Metric, 0, r.freeBytes/10)
		bytes        int32
	)

	for smet := range r.metCh {
		flush := !smet.set && len(mets) > 0
		if flush || bytes+smet.size > r.freeBytes {
			r.numMetrics.Add(int64(len(mets)))
			mets = r.flush(mets)
			bytes = 0

			if len(borrowedTags) > 0 {
				for i := range borrowedTags {
					extraTags.Put(borrowedTags[i][:0])
				}
				borrowedTags = borrowedTags[:0]
			}
		}

		if !smet.set {
			continue
		}

		m := smet.m
		if len(smet.bucket) > 0 {
			tags := extraTags.Get().([]m3thrift.MetricTag)
			tags = append(tags, m.Tags...)
			tags = append(
				tags,
				m3thrift.MetricTag{
					Name:  r.bucketIDTagName,
					Value: smet.bucketID,
				},
				m3thrift.MetricTag{
					Name:  r.bucketTagName,
					Value: smet.bucket,
				},
			)
			borrowedTags = append(borrowedTags, tags)
			m.Tags = tags
		}

		mets = append(mets, m)
		bytes += smet.size
	}

	// Final flush
	r.flush(mets)
}

func (r *reporter) flush(mets []m3thrift.Metric) []m3thrift.Metric {
	if len(mets) == 0 {
		return mets
	}

	r.numBatches.Inc()

	err := r.client.EmitMetricBatchV2(m3thrift.MetricBatch{
		Metrics:    mets,
		CommonTags: r.commonTags,
	})
	if err != nil {
		r.numWriteErrors.Inc()
	}

	// n.b. In the event that we had allocated additional tag storage in
	//      process(), clear it so that it can be reclaimed. This does not
	//      affect allocated metrics' tags.
	for i := range mets {
		mets[i].Tags = nil
	}
	return mets[:0]
}

func (r *reporter) convertTags(tags map[string]string) []m3thrift.MetricTag {
	key := cache.TagMapKey(tags)

	mtags, ok := r.tagCache.Get(key)
	if !ok {
		mtags = r.resourcePool.getMetricTagSlice()
		for k, v := range tags {
			mtags = append(mtags, m3thrift.MetricTag{
				Name:  r.stringInterner.Intern(k),
				Value: r.stringInterner.Intern(v),
			})
		}
		mtags = r.tagCache.Set(key, mtags)
	}

	return mtags
}

func (r *reporter) reportInternalMetrics() {
	var (
		batches     = r.numBatches.Swap(0)
		metrics     = r.numMetrics.Swap(0)
		writeErrors = r.numWriteErrors.Swap(0)
		batchSize   = float64(metrics) / float64(batches)
	)

	bucket := sort.Search(len(r.buckets), func(i int) bool {
		return r.buckets[i].UpperBoundValue() >= batchSize
	})

	var value float64
	if bucket < len(r.buckets) {
		value = r.buckets[bucket].UpperBoundValue()
	} else {
		value = math.MaxFloat64
	}

	r.batchSizeHistogram.ValueBucket(0, value).ReportSamples(1)
	r.numBatchesCounter.ReportCount(batches)
	r.numMetricsCounter.ReportCount(metrics)
	r.numWriteErrorsCounter.ReportCount(writeErrors)
}

func (r *reporter) timeLoop() {
	for !r.done.Load() {
		r.now.Store(time.Now().UnixNano())
		time.Sleep(_timeResolution)
	}
}

type cachedMetric struct {
	metric   m3thrift.Metric
	reporter *reporter
	size     int32
}

func (c cachedMetric) ReportCount(value int64) {
	c.metric.Value.Count = value
	c.reporter.reportCopyMetric(c.metric, c.size, "", "")
}

func (c cachedMetric) ReportGauge(value float64) {
	c.metric.Value.Gauge = value
	c.reporter.reportCopyMetric(c.metric, c.size, "", "")
}

func (c cachedMetric) ReportTimer(interval time.Duration) {
	c.metric.Value.Timer = int64(interval)
	c.reporter.reportCopyMetric(c.metric, c.size, "", "")
}

type noopMetric struct{}

func (c noopMetric) ReportCount(value int64)            {}
func (c noopMetric) ReportGauge(value float64)          {}
func (c noopMetric) ReportTimer(interval time.Duration) {}
func (c noopMetric) ReportSamples(value int64)          {}

type cachedHistogram struct {
	r                     *reporter
	name                  string
	cachedValueBuckets    []cachedHistogramBucket
	cachedDurationBuckets []cachedHistogramBucket
}

func (h cachedHistogram) ValueBucket(
	_ float64,
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

	var (
		b        = h.cachedValueBuckets[idx]
		cm       = b.metric
		m        = cm.metric
		size     = cm.size
		bucket   = b.bucket
		bucketID = b.bucketID
		rep      = cm.reporter
	)

	return reportSamplesFunc(func(value int64) {
		m.Value.Count = value
		rep.reportCopyMetric(m, size, bucket, bucketID)
	})
}

func (h cachedHistogram) DurationBucket(
	_ time.Duration,
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

	var (
		b        = h.cachedDurationBuckets[idx]
		cm       = b.metric
		m        = cm.metric
		size     = cm.size
		bucket   = b.bucket
		bucketID = b.bucketID
		rep      = cm.reporter
	)

	return reportSamplesFunc(func(value int64) {
		m.Value.Count = value
		rep.reportCopyMetric(m, size, bucket, bucketID)
	})
}

type cachedHistogramBucket struct {
	metric             *cachedMetric
	durationUpperBound time.Duration
	valueUpperBound    float64
	bucket             string
	bucketID           string
}

type reportSamplesFunc func(value int64)

func (f reportSamplesFunc) ReportSamples(value int64) {
	f(value)
}

type sizedMetric struct {
	m        m3thrift.Metric
	size     int32
	set      bool
	bucket   string
	bucketID string
}

func ndigits(i int) int {
	n := 1
	for i/10 != 0 {
		n++
		i /= 10
	}
	return n
}
