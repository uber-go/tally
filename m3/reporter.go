// Copyright (c) 2024 Uber Technologies, Inc.
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
	"github.com/uber-go/tally/internal/cache"
	customtransport "github.com/uber-go/tally/m3/customtransports"
	m3thrift "github.com/uber-go/tally/m3/thrift/v2"
	"github.com/uber-go/tally/m3/thriftudp"
	"github.com/uber-go/tally/thirdparty/github.com/apache/thrift/lib/go/thrift"
	"go.uber.org/atomic"
)

// pendingReport is used to send metric data to the processing goroutine.
type pendingReport struct {
	cached    *cachedMetric
	valueType metricType
	countVal  int64
	gaugeVal  float64
	timerVal  time.Duration
	bucket    string
	bucketID  string
}

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
	client          *m3thrift.M3Client
	commonTags      []m3thrift.MetricTag
	commonTagsBytes []byte
	done            atomic.Bool
	donech          chan struct{}
	freeBytes       int32
	metCh           chan pendingReport
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
	numTagCacheCounter    tally.CachedCount
	protocolFactory       thrift.TProtocolFactory
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
	InternalTags                map[string]string
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

	var pFactory thrift.TProtocolFactory
	if opts.Protocol == Compact {
		pFactory = thrift.NewTCompactProtocolFactory()
	} else {
		pFactory = thrift.NewTBinaryProtocolFactoryDefault()
	}

	client := m3thrift.NewM3ClientFactory(trans, pFactory)
	rPool := newResourcePool(pFactory)

	tagm := make(map[string]string)
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
			hostname, hostErr := os.Hostname()
			if hostErr != nil {
				return nil, errors.WithMessage(hostErr, "error resolving host tag")
			}
			tagm[HostTag] = hostname
		}
	}

	tempInterner := cache.NewStringInterner()
	resolvedCommonTags := make([]m3thrift.MetricTag, 0, len(tagm))
	for k, v := range tagm {
		resolvedCommonTags = append(resolvedCommonTags, m3thrift.MetricTag{
			Name:  tempInterner.Intern(k),
			Value: tempInterner.Intern(v),
		})
	}
	sort.Slice(resolvedCommonTags, func(i, j int) bool {
		return resolvedCommonTags[i].Name < resolvedCommonTags[j].Name
	})

	r := &reporter{
		bucketIDTagName: opts.HistogramBucketIDName,
		bucketTagName:   opts.HistogramBucketName,
		bucketValFmt:    "%." + strconv.Itoa(int(opts.HistogramBucketTagPrecision)) + "f",
		metCh:           make(chan pendingReport, opts.MaxQueueSize),
		donech:          make(chan struct{}),
		client:          client,
		commonTags:      resolvedCommonTags,
		resourcePool:    rPool,
		stringInterner:  cache.NewStringInterner(),
		tagCache:        cache.NewTagCache(),
		protocolFactory: pFactory,
	}

	r.now.Store(time.Now().UnixNano())

	tempBatchForOverhead := m3thrift.NewMetricBatch()
	tempBatchForOverhead.CommonTags = r.commonTags

	memBufForOverhead := thrift.NewTMemoryBuffer()
	protoForOverhead := r.protocolFactory.GetProtocol(memBufForOverhead)
	if err := tempBatchForOverhead.Write(protoForOverhead); err != nil {
		return nil, errors.WithMessage(err, "failed to write common tags for overhead calculation")
	}
	r.commonTagsBytes = append([]byte(nil), memBufForOverhead.Bytes()...)
	r.overheadBytes = int32(len(r.commonTagsBytes))

	if r.overheadBytes >= opts.MaxPacketSizeBytes {
		return nil, errCommonTagSize
	}
	r.freeBytes = opts.MaxPacketSizeBytes - r.overheadBytes

	internalMetricBuckets := tally.ValueBuckets(append(
		[]float64{0.0},
		tally.MustMakeExponentialValueBuckets(2.0, 2.0, 11)...,
	))
	r.buckets = tally.BucketPairs(internalMetricBuckets)

	internalScopeTags := opts.InternalTags
	if internalScopeTags == nil {
		internalScopeTags = make(map[string]string)
	}
	if _, ok := internalScopeTags["version"]; !ok {
		internalScopeTags["version"] = tally.Version
	}

	r.batchSizeHistogram = r.AllocateHistogram("tally.internal.batch-size", internalScopeTags, internalMetricBuckets)
	r.numBatchesCounter = r.AllocateCounter("tally.internal.num-batches", internalScopeTags)
	r.numMetricsCounter = r.AllocateCounter("tally.internal.num-metrics", internalScopeTags)
	r.numWriteErrorsCounter = r.AllocateCounter("tally.internal.num-write-errors", internalScopeTags)
	r.numTagCacheCounter = r.AllocateCounter("tally.internal.num-tag-cache", internalScopeTags)

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
	return r.allocateMetric(name, tags, counterType)
}

// AllocateGauge implements tally.CachedStatsReporter.
func (r *reporter) AllocateGauge(
	name string,
	tags map[string]string,
) tally.CachedGauge {
	return r.allocateMetric(name, tags, gaugeType)
}

// AllocateTimer implements tally.CachedStatsReporter.
func (r *reporter) AllocateTimer(
	name string,
	tags map[string]string,
) tally.CachedTimer {
	return r.allocateMetric(name, tags, timerType)
}

// allocateMetric is the new central allocation function.
// It pre-serializes the metric name and tags.
func (r *reporter) allocateMetric(
	name string,
	tagMap map[string]string,
	mType metricType,
) *cachedMetric {
	internedName := r.stringInterner.Intern(name)
	canonicalTags := r.convertTags(tagMap)

	headerMetric := m3thrift.NewMetric()
	headerMetric.Name = internedName
	headerMetric.Tags = canonicalTags

	memBuf := thrift.NewTMemoryBuffer()
	proto := r.protocolFactory.GetProtocol(memBuf)

	var precomputedData []byte
	var estimatedValueAndTimestampSize int32

	if err := headerMetric.Write(proto); err != nil {
		return &cachedMetric{reporter: r, isNoop: true}
	}
	precomputedData = append([]byte(nil), memBuf.Bytes()...)

	switch mType {
	case counterType, timerType:
		estimatedValueAndTimestampSize = 15
	case gaugeType:
		estimatedValueAndTimestampSize = 13
	}

	return &cachedMetric{
		reporter:          r,
		metricName:        internedName,
		metricTags:        canonicalTags,
		precomputedHeader: precomputedData,
		size:              int32(len(precomputedData)) + estimatedValueAndTimestampSize,
		metricType:        mType,
		isNoop:            false,
	}
}

// AllocateHistogram implements tally.CachedStatsReporter.
func (r *reporter) AllocateHistogram(
	name string,
	tags map[string]string,
	bucketsTally tally.Buckets,
) tally.CachedHistogram {
	internedBaseName := r.stringInterner.Intern(name)
	baseTags := r.convertTags(tags)

	_, isDuration := bucketsTally.(tally.DurationBuckets)
	numBuckets := bucketsTally.Len()
	bucketIDLen := int(math.Max(float64(ndigits(numBuckets)), float64(_minMetricBucketIDTagLength)))
	bucketIDFmt := "%0" + strconv.Itoa(bucketIDLen) + "d"

	ch := &cachedHistogram{
		r:                     r,
		name:                  internedBaseName,
		baseTags:              baseTags,
		cachedValueBuckets:    make([]cachedHistogramBucket, 0, numBuckets),
		cachedDurationBuckets: make([]cachedHistogramBucket, 0, numBuckets),
	}

	var (
		prevDuration = time.Duration(math.MinInt64)
		prevValue    = -math.MaxFloat64
	)

	for i, pair := range tally.BucketPairs(bucketsTally) {
		bucketSpecificTagMap := make(map[string]string)
		for _, baseTag := range baseTags {
			bucketSpecificTagMap[baseTag.Name] = baseTag.Value
		}

		bucketIDStr := r.stringInterner.Intern(fmt.Sprintf(bucketIDFmt, i))
		bucketSpecificTagMap[r.bucketIDTagName] = bucketIDStr

		var boundValueStr string
		if isDuration {
			boundValueStr = r.stringInterner.Intern(r.durationBucketString(prevDuration) + "-" + r.durationBucketString(pair.UpperBoundDuration()))
		} else {
			boundValueStr = r.stringInterner.Intern(r.valueBucketString(prevValue) + "-" + r.valueBucketString(pair.UpperBoundValue()))
		}
		bucketSpecificTagMap[r.bucketTagName] = boundValueStr

		cm := r.allocateMetric(internedBaseName, bucketSpecificTagMap, counterType)

		histBucket := cachedHistogramBucket{
			metric:             cm,
			valueUpperBound:    pair.UpperBoundValue(),
			durationUpperBound: pair.UpperBoundDuration(),
			bucket:             boundValueStr,
			bucketID:           bucketIDStr,
		}

		if isDuration {
			ch.cachedDurationBuckets = append(ch.cachedDurationBuckets, histBucket)
		} else {
			ch.cachedValueBuckets = append(ch.cachedValueBuckets, histBucket)
		}

		prevDuration = pair.UpperBoundDuration()
		prevValue = pair.UpperBoundValue()
	}
	return ch
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

func (r *reporter) convertTags(tags map[string]string) []m3thrift.MetricTag {
	if len(tags) == 0 {
		return nil
	}
	key := cache.TagMapKey(tags)

	cachedTags, ok := r.tagCache.Get(key)
	if ok {
		return cachedTags
	}

	newTags := r.resourcePool.getMetricTagSlice()
	for k, v := range tags {
		newTags = append(newTags, m3thrift.MetricTag{
			Name:  r.stringInterner.Intern(k),
			Value: r.stringInterner.Intern(v),
		})
	}
	sort.Slice(newTags, func(i, j int) bool {
		return newTags[i].Name < newTags[j].Name
	})

	cachedResult := r.tagCache.Set(key, newTags)
	return cachedResult
}

func (r *reporter) Flush() {
	if r.done.Load() {
		return
	}

	r.pending.Inc()

	select {
	case r.metCh <- pendingReport{cached: nil}:
		// Successfully sent flush signal. process() loop will Dec() r.pending.
	case <-r.donech:
		// Reporter is closing, this flush signal won't be processed normally.
		if r.pending.Load() > 0 {
			r.pending.Dec()
		}
		return
	}

	timeout := time.After(2 * time.Second) // Overall timeout for flush completion

	for {
		select {
		case <-r.donech:
			return // Reporter is closing
		case <-timeout:
			return // Flush wait timed out
		default:
			// Fall through to check pending
		}

		if r.pending.Load() == 0 {
			return // All pending operations (including this flush) likely processed.
		}

		time.Sleep(1 * time.Millisecond) // Yield to allow other goroutines to run.
	}
}

func (r *reporter) Close() (err error) {
	if !r.done.CAS(false, true) {
		return errAlreadyClosed
	}

	// Signal the goroutines to stop first
	close(r.donech)

	// Report any final internal metrics. This will queue them into metCh.
	r.reportInternalMetrics()

	// Attempt a final flush for all queued metrics (user + internal).
	r.tryFinalFlush()

	// Wait for process() goroutine to finish (which includes draining metCh).
	r.wg.Wait()

	// Now that process() is done and metCh is drained, it's safe to close metCh.
	close(r.metCh)

	if r.client != nil && r.client.Transport != nil {
		if cl, ok := r.client.Transport.(io.Closer); ok {
			return cl.Close()
		}
	}
	return nil
}

// tryFinalFlush sends a flush signal without blocking indefinitely.
// It's a helper for Close to ensure pending metrics are processed.
func (r *reporter) tryFinalFlush() {
	r.pending.Inc()
	defer func() {
		if r.pending.Load() > 0 {
			r.pending.Dec()
		}
	}()

	select {
	case r.metCh <- pendingReport{cached: nil}:
		// Successfully sent flush signal. The process() loop's drain will handle it.
	case <-r.donech:
		// This flush signal may not be processed if already shutting down hard.
		return
	}
	// No explicit wait. Relies on Close() waiting for r.wg.Wait(), which ensures process() drains metCh.
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
	mets := r.resourcePool.getMetricSlice()
	var currentBytesInBatch int32

	// Setup a ticker for periodic flushes
	ticker := time.NewTicker(100 * time.Millisecond) // Flush at least every 100ms
	defer ticker.Stop()

	for {
		select {
		case report, ok := <-r.metCh:
			if !ok {
				// Channel closed, perform final flush if needed
				if len(mets) > 0 {
					mets = r.internalFlush(mets, &currentBytesInBatch)
				}
				r.resourcePool.releaseMetricSlice(mets)
				return
			}

			// Safely decrement pending counter if it was incremented for a flush signal
			if report.cached == nil { // This was a flush signal from Flush() or tryFinalFlush()
				if r.pending.Load() > 0 {
					r.pending.Dec() // This is the authoritative Dec for the flush signal
				}
			}

			if report.cached == nil {
				// Explicit flush request from metCh (already accounted for in pending.Dec above)
				if len(mets) > 0 {
					mets = r.internalFlush(mets, &currentBytesInBatch)
				}
				continue
			}

			if report.cached.isNoop {
				continue
			}

			metricToAddSize := report.cached.size

			if len(mets) > 0 && currentBytesInBatch+metricToAddSize > r.freeBytes {
				mets = r.internalFlush(mets, &currentBytesInBatch)
			}

			finalMetric := m3thrift.NewMetric()

			hdrMemBuf := thrift.NewTMemoryBuffer()
			if _, err := hdrMemBuf.Write(report.cached.precomputedHeader); err != nil {
				// TODO: Log or handle error (e.g., increment a counter)
				continue
			}

			hdrProto := r.protocolFactory.GetProtocol(hdrMemBuf)
			if err := finalMetric.Read(hdrProto); err != nil {
				// TODO: Log or handle error
				continue
			}

			nowVal := r.now.Load()
			finalMetric.Timestamp = nowVal

			metricVal := m3thrift.NewMetricValue()
			switch report.valueType {
			case counterType:
				metricVal.MetricType = m3thrift.MetricType_COUNTER
				countVal := report.countVal
				metricVal.Count = countVal
			case gaugeType:
				metricVal.MetricType = m3thrift.MetricType_GAUGE
				gaugeVal := report.gaugeVal
				metricVal.Gauge = gaugeVal
			case timerType:
				metricVal.MetricType = m3thrift.MetricType_TIMER
				timerVal := int64(report.timerVal)
				metricVal.Timer = timerVal
			}
			finalMetric.Value = *metricVal

			mets = append(mets, *finalMetric)
			currentBytesInBatch += metricToAddSize
		case <-ticker.C:
			// Periodic flush for any accumulated metrics
			if len(mets) > 0 {
				mets = r.internalFlush(mets, &currentBytesInBatch)
			}
		case <-r.donech:
			// Drain metCh before exiting
			for len(r.metCh) > 0 {
				report, ok := <-r.metCh
				if !ok {
					break // Should not happen if donech is the primary signal
				}
				if report.cached == nil { // Flush signal
					if r.pending.Load() > 0 {
						r.pending.Dec()
					}
					if len(mets) > 0 { // Flush any accumulated before this signal
						mets = r.internalFlush(mets, &currentBytesInBatch)
					}
					continue
				}
				if report.cached.isNoop {
					continue
				}
				// Simplified metric processing for draining phase
				metricToAddSize := report.cached.size
				if len(mets) > 0 && currentBytesInBatch+metricToAddSize > r.freeBytes {
					mets = r.internalFlush(mets, &currentBytesInBatch)
				}
				// (Code to prepare and append finalMetric as above)
				// For brevity, assuming a function prepareAndAppendMetric(&mets, report, &currentBytesInBatch)
				// This part needs to be filled in similar to the main select case for r.metCh
				// Reconstruct the metric addition logic here:
				finalMetric := m3thrift.NewMetric()
				hdrMemBuf := thrift.NewTMemoryBuffer()
				if _, err := hdrMemBuf.Write(report.cached.precomputedHeader); err != nil {
					continue
				}
				hdrProto := r.protocolFactory.GetProtocol(hdrMemBuf)
				if err := finalMetric.Read(hdrProto); err != nil {
					continue
				}
				nowVal := r.now.Load()
				finalMetric.Timestamp = nowVal
				metricVal := m3thrift.NewMetricValue()
				switch report.valueType {
				case counterType:
					metricVal.MetricType = m3thrift.MetricType_COUNTER
					metricVal.Count = report.countVal
				case gaugeType:
					metricVal.MetricType = m3thrift.MetricType_GAUGE
					metricVal.Gauge = report.gaugeVal
				case timerType:
					metricVal.MetricType = m3thrift.MetricType_TIMER
					metricVal.Timer = int64(report.timerVal)
				}
				finalMetric.Value = *metricVal
				mets = append(mets, *finalMetric)
				currentBytesInBatch += metricToAddSize
			}
			// Final flush of any remaining metrics
			if len(mets) > 0 {
				mets = r.internalFlush(mets, &currentBytesInBatch)
			}
			r.resourcePool.releaseMetricSlice(mets)
			return
		}
	}
}

func (r *reporter) internalFlush(mets []m3thrift.Metric, currentBytesInBatch *int32) []m3thrift.Metric {
	if len(mets) == 0 {
		return mets
	}

	r.numBatchesCounter.ReportCount(1)
	r.numMetricsCounter.ReportCount(int64(len(mets)))
	r.numBatches.Add(1)
	r.numMetrics.Add(int64(len(mets)))

	batch := m3thrift.NewMetricBatch()
	batch.Metrics = mets
	batch.CommonTags = r.commonTags

	err := r.client.EmitMetricBatchV2(*batch)
	if err != nil {
		r.numWriteErrorsCounter.ReportCount(1)
		r.numWriteErrors.Add(1)
	}

	*currentBytesInBatch = 0
	r.resourcePool.releaseMetricSlice(mets)
	return r.resourcePool.getMetricSlice()
}

func (r *reporter) reportInternalMetrics() {
	numBatches := r.numBatches.Swap(0)
	numMetrics := r.numMetrics.Swap(0)
	numWriteErrors := r.numWriteErrors.Swap(0)
	tagCacheLen := int64(r.tagCache.Len())

	var avgBatchSize float64
	if numBatches > 0 {
		avgBatchSize = float64(numMetrics) / float64(numBatches)
	}

	idx := sort.Search(len(r.buckets), func(i int) bool {
		return r.buckets[i].UpperBoundValue() >= avgBatchSize
	})

	var bucketValueToReport float64
	if idx < len(r.buckets) {
		bucketValueToReport = r.buckets[idx].UpperBoundValue()
	} else if len(r.buckets) > 0 {
		bucketValueToReport = r.buckets[len(r.buckets)-1].UpperBoundValue()
	} else {
		bucketValueToReport = 0
	}

	if r.batchSizeHistogram != nil {
		r.batchSizeHistogram.ValueBucket(0, bucketValueToReport).ReportSamples(1)
	}
	if r.numBatchesCounter != nil {
		r.numBatchesCounter.ReportCount(numBatches)
	}
	if r.numMetricsCounter != nil {
		r.numMetricsCounter.ReportCount(numMetrics)
	}
	if r.numWriteErrorsCounter != nil {
		r.numWriteErrorsCounter.ReportCount(numWriteErrors)
	}
	if r.numTagCacheCounter != nil {
		r.numTagCacheCounter.ReportCount(tagCacheLen)
	}
}

func (r *reporter) timeLoop() {
	ticker := time.NewTicker(_timeResolution)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.now.Store(time.Now().UnixNano())
		case <-r.donech:
			return
		}
	}
}

type cachedMetric struct {
	reporter          *reporter
	precomputedHeader []byte
	size              int32
	metricName        string
	metricTags        []m3thrift.MetricTag
	metricType        metricType
	isNoop            bool
}

func (c *cachedMetric) ReportCount(value int64) {
	if c.isNoop || c.reporter == nil || c.reporter.done.Load() {
		return
	}
	if c.metricType != counterType {
		return
	}

	c.reporter.pending.Inc()
	select {
	case c.reporter.metCh <- pendingReport{
		cached:    c,
		valueType: counterType,
		countVal:  value,
	}:
		// Successfully queued
	case <-c.reporter.donech:
		// Reporter is shutting down, decrement counter
		c.reporter.pending.Dec()
	default:
		// Channel full, drop metric to avoid blocking
		c.reporter.pending.Dec()
	}
}

func (c *cachedMetric) ReportGauge(value float64) {
	if c.isNoop || c.reporter == nil || c.reporter.done.Load() {
		return
	}
	if c.metricType != gaugeType {
		return
	}

	c.reporter.pending.Inc()
	select {
	case c.reporter.metCh <- pendingReport{
		cached:    c,
		valueType: gaugeType,
		gaugeVal:  value,
	}:
		// Successfully queued
	case <-c.reporter.donech:
		// Reporter is shutting down, decrement counter
		c.reporter.pending.Dec()
	default:
		// Channel full, drop metric to avoid blocking
		c.reporter.pending.Dec()
	}
}

func (c *cachedMetric) ReportTimer(interval time.Duration) {
	if c.isNoop || c.reporter == nil || c.reporter.done.Load() {
		return
	}
	if c.metricType != timerType {
		return
	}

	c.reporter.pending.Inc()
	select {
	case c.reporter.metCh <- pendingReport{
		cached:    c,
		valueType: timerType,
		timerVal:  interval,
	}:
		// Successfully queued
	case <-c.reporter.donech:
		// Reporter is shutting down, decrement counter
		c.reporter.pending.Dec()
	default:
		// Channel full, drop metric to avoid blocking
		c.reporter.pending.Dec()
	}
}

type cachedHistogram struct {
	r                     *reporter
	name                  string
	baseTags              []m3thrift.MetricTag
	cachedValueBuckets    []cachedHistogramBucket
	cachedDurationBuckets []cachedHistogramBucket
}

func (h *cachedHistogram) ValueBucket(
	_ float64,
	bucketUpperBound float64,
) tally.CachedHistogramBucket {
	if len(h.cachedValueBuckets) == 0 {
		return noopCachedHistogramBucket{}
	}
	idx := sort.Search(len(h.cachedValueBuckets), func(i int) bool {
		return h.cachedValueBuckets[i].valueUpperBound >= bucketUpperBound
	})
	if idx == len(h.cachedValueBuckets) {
		idx = len(h.cachedValueBuckets) - 1
	}
	return &h.cachedValueBuckets[idx]
}

func (h *cachedHistogram) DurationBucket(
	_ time.Duration,
	bucketUpperBound time.Duration,
) tally.CachedHistogramBucket {
	if len(h.cachedDurationBuckets) == 0 {
		return noopCachedHistogramBucket{}
	}
	idx := sort.Search(len(h.cachedDurationBuckets), func(i int) bool {
		return h.cachedDurationBuckets[i].durationUpperBound >= bucketUpperBound
	})
	if idx == len(h.cachedDurationBuckets) {
		idx = len(h.cachedDurationBuckets) - 1
	}
	return &h.cachedDurationBuckets[idx]
}

type cachedHistogramBucket struct {
	metric             *cachedMetric
	durationUpperBound time.Duration
	valueUpperBound    float64
	bucket             string
	bucketID           string
}

func (chb *cachedHistogramBucket) ReportSamples(value int64) {
	if chb.metric == nil || chb.metric.isNoop || chb.metric.reporter == nil || chb.metric.reporter.done.Load() {
		return
	}

	chb.metric.reporter.pending.Inc()
	select {
	case chb.metric.reporter.metCh <- pendingReport{
		cached:    chb.metric,
		valueType: counterType,
		countVal:  value,
	}:
		// Successfully queued
	case <-chb.metric.reporter.donech:
		// Reporter is shutting down, decrement counter
		chb.metric.reporter.pending.Dec()
	default:
		// Channel full, drop metric to avoid blocking
		chb.metric.reporter.pending.Dec()
	}
}

type noopCachedHistogramBucket struct{}

func (n noopCachedHistogramBucket) ReportSamples(value int64) {}

func ndigits(i int) int {
	n := 1
	if i < 0 {
		i = -i
	}
	for i/10 != 0 {
		n++
		i /= 10
	}
	return n
}
