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
	// Channel for flush acknowledgment - nil for regular metrics
	flushAck chan struct{}
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
	// DefaultTagCacheSize is the default size for the tag cache
	DefaultTagCacheSize = 10000
	// DefaultTagCacheTTLSeconds is the default TTL for tag cache entries (1 hour)
	DefaultTagCacheTTLSeconds = int64(3600)
	// DefaultTransportBufferSize is the default buffer size for async transport
	DefaultTransportBufferSize = 1000
	// DefaultFlushInterval is how often to flush transport batches (500ms for balanced performance)
	DefaultFlushInterval = 1 * time.Second
	// DefaultMaxTimersPerMetricPerBatch is the maximum number of timer metrics allowed per metricId per batch (this is set to 1000 by default to avoid overwhelming the server)
	// the timer metrics with over 1000 samples will be dropped by the server
	DefaultMaxTimersPerMetricPerBatch = 1000

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

// AsyncTransport provides non-blocking metric transmission
//
// LATENCY CHARACTERISTICS:
//
// For All Metrics (Uniform Treatment):
//
//	Best Case:  ~1-10ms (immediate sending when batch full)
//	Normal Case: ~1s (flush timeout when batch not full)
//	Worst Case: ~1s + network latency
//	Drop Scenario: Transport channel full (configurable capacity)
type AsyncTransport interface {
	SendAsync(batch *m3thrift.MetricBatch) error
	Close() error
	DropCount() int64    // Returns number of dropped batches
	SuccessCount() int64 // Returns number of successfully sent batches
}

// bufferedTransport wraps the underlying transport with async sending
type bufferedTransport struct {
	underlying    *m3thrift.M3Client
	batchCh       chan *m3thrift.MetricBatch
	dropCount     atomic.Int64
	successCount  atomic.Int64 // Track successful transmissions
	done          chan struct{}
	wg            sync.WaitGroup
	flushInterval time.Duration // How often to flush batches (500ms default)
}

// newBufferedTransport creates a non-blocking transport wrapper
func newBufferedTransport(client *m3thrift.M3Client, bufferSize int) *bufferedTransport {
	if bufferSize <= 0 {
		bufferSize = DefaultTransportBufferSize
	}

	bt := &bufferedTransport{
		underlying:    client,
		batchCh:       make(chan *m3thrift.MetricBatch, bufferSize),
		done:          make(chan struct{}),
		flushInterval: DefaultFlushInterval, // 500ms
	}

	// Start async sender goroutine
	bt.wg.Add(1)
	go bt.sendLoop()

	return bt
}

// SendAsync sends a batch without blocking on network I/O
func (bt *bufferedTransport) SendAsync(batch *m3thrift.MetricBatch) error {
	select {
	case bt.batchCh <- batch:
		return nil
	case <-bt.done:
		return fmt.Errorf("transport closed")
	default:
		// Channel full, drop the batch
		bt.dropCount.Add(1)
		return nil
	}
}

// DropCount returns the number of dropped batches
func (bt *bufferedTransport) DropCount() int64 {
	return bt.dropCount.Load()
}

// SuccessCount returns the number of successfully sent batches
func (bt *bufferedTransport) SuccessCount() int64 {
	return bt.successCount.Load()
}

// sendLoop processes batches with timeout-based flushing
func (bt *bufferedTransport) sendLoop() {
	defer bt.wg.Done()

	ticker := time.NewTicker(bt.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case batch, ok := <-bt.batchCh:
			if !ok {
				return
			}
			// Send metrics immediately when received
			err := bt.underlying.EmitMetricBatchV2(*batch)
			if err == nil {
				// Track successful transmissions (could be extended to count actual metrics in batch)
				atomic.AddInt64(&bt.successCount, 1)
			}

		case <-ticker.C:
			// Periodic flush tick - currently no local batching to flush
			// This could be extended to batch multiple small batches together
			continue

		case <-bt.done:
			// Drain remaining batches on shutdown
			bt.drainChannels()
			return
		}
	}
}

// drainChannels drains the batch channel on shutdown
func (bt *bufferedTransport) drainChannels() {
	for {
		select {
		case batch, ok := <-bt.batchCh:
			if !ok {
				return
			}
			_ = bt.underlying.EmitMetricBatchV2(*batch)
		default:
			return
		}
	}
}

// Close shuts down the async transport
func (bt *bufferedTransport) Close() error {
	close(bt.done)
	bt.wg.Wait()
	close(bt.batchCh)

	if bt.underlying != nil && bt.underlying.Transport != nil {
		if cl, ok := bt.underlying.Transport.(io.Closer); ok {
			return cl.Close()
		}
	}
	return nil
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
	asyncTransport  *bufferedTransport // Non-blocking transport
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

	// Timer limiting per batch
	currentBatchTimerCounts    map[string]int // metricId -> count of timers in current batch
	maxTimersPerMetricPerBatch int            // configurable limit (default 10000)

	batchSizeHistogram           tally.CachedHistogram
	numBatches                   atomic.Int64
	numBatchesCounter            tally.CachedCount
	numMetrics                   atomic.Int64
	numMetricsCounter            tally.CachedCount
	numWriteErrors               atomic.Int64
	numWriteErrorsCounter        tally.CachedCount
	numTagCacheCounter           tally.CachedCount
	numDropped                   atomic.Int64      // Track dropped metrics
	numDroppedCounter            tally.CachedCount // Counter for dropped metrics
	numTransportDropped          atomic.Int64      // Track transport-level drops
	numTransportDroppedCounter   tally.CachedCount // Counter for transport drops
	numTransportSuccess          atomic.Int64      // Track successful transmissions
	numTransportSuccessCounter   tally.CachedCount // Counter for successful transmissions
	numTimersDroppedLimit        atomic.Int64      // Track timers dropped due to per-metric limits
	numTimersDroppedLimitCounter tally.CachedCount // Counter for timer limit drops
	protocolFactory              thrift.TProtocolFactory
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
	// Transport configuration
	TransportBufferSize   int  // Buffer size for async transport (default: DefaultTransportBufferSize)
	DisableAsyncTransport bool // Disable async transport (default: false)
	// Timer limiting configuration
	MaxTimersPerMetricPerBatch int // Maximum timers per metricId per batch (default: DefaultMaxTimersPerMetricPerBatch)
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
	if opts.TransportBufferSize <= 0 {
		opts.TransportBufferSize = DefaultTransportBufferSize
	}
	if opts.MaxTimersPerMetricPerBatch <= 0 {
		opts.MaxTimersPerMetricPerBatch = DefaultMaxTimersPerMetricPerBatch
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

	// Initialize async transport if not disabled
	var asyncTransport *bufferedTransport
	if !opts.DisableAsyncTransport {
		asyncTransport = newBufferedTransport(client, opts.TransportBufferSize)
	}

	r := &reporter{
		bucketIDTagName: opts.HistogramBucketIDName,
		bucketTagName:   opts.HistogramBucketName,
		bucketValFmt:    "%." + strconv.Itoa(int(opts.HistogramBucketTagPrecision)) + "f",
		metCh:           make(chan pendingReport, opts.MaxQueueSize),
		donech:          make(chan struct{}),
		client:          client,
		asyncTransport:  asyncTransport,
		commonTags:      resolvedCommonTags,
		resourcePool:    rPool,
		stringInterner:  cache.NewStringInterner(),
		tagCache: cache.NewTagCacheWithOptions(cache.TagCacheOptions{
			MaxSize:    DefaultTagCacheSize,
			TTLSeconds: DefaultTagCacheTTLSeconds,
		}),
		protocolFactory: pFactory,
		// Initialize timer limiting
		currentBatchTimerCounts:    make(map[string]int),
		maxTimersPerMetricPerBatch: opts.MaxTimersPerMetricPerBatch,
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
	r.numDroppedCounter = r.AllocateCounter("tally.internal.num-dropped", internalScopeTags)
	r.numTransportDroppedCounter = r.AllocateCounter("tally.internal.num-transport-dropped", internalScopeTags)
	r.numTransportSuccessCounter = r.AllocateCounter("tally.internal.num-transport-success", internalScopeTags)
	r.numTimersDroppedLimitCounter = r.AllocateCounter("tally.internal.num-timers-dropped-limit", internalScopeTags)

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

// optimizedConvertTags provides specialized conversion with minimal allocations
func (r *reporter) optimizedConvertTags(tags map[string]string) []m3thrift.MetricTag {
	tagCount := len(tags)
	if tagCount == 0 {
		return nil
	}

	// Fast path for empty or single common tags
	if tagCount == 1 {
		return r.convertSingleTag(tags)
	}

	// Fast path for <= 3 tags
	// todo: figure out the sweet spot for this just a random guess for now
	if tagCount <= 3 {
		return r.convertFewTags(tags)
	}

	// General case for many tags
	return r.convertManyTags(tags)
}

// convertSingleTag handles the very common single tag case
func (r *reporter) convertSingleTag(tags map[string]string) []m3thrift.MetricTag {
	for k, v := range tags {
		// Use a simplified cache key for single tags to reduce hashing overhead
		cacheKey := uint64(len(k))<<32 | uint64(len(v)) // Simple hash based on lengths
		if k != "" && len(k) < 64 && v != "" && len(v) < 64 {
			// For short strings, create a fast composite key
			var keyBuf [128]byte
			copy(keyBuf[:], k)
			copy(keyBuf[len(k):], v)
			cacheKey = cache.TagMapKey(map[string]string{string(keyBuf[:len(k)+len(v)]): ""})
		} else {
			cacheKey = cache.TagMapKey(tags)
		}

		if cachedTags, ok := r.tagCache.Get(cacheKey); ok {
			return cachedTags
		}

		// Create result with exact capacity
		result := make([]m3thrift.MetricTag, 1)
		result[0] = m3thrift.MetricTag{
			Name:  r.stringInterner.Intern(k),
			Value: r.stringInterner.Intern(v),
		}

		r.tagCache.Set(cacheKey, result)
		return result
	}
	return nil
}

// convertFewTags handles 2-3 tags efficiently
func (r *reporter) convertFewTags(tags map[string]string) []m3thrift.MetricTag {
	key := cache.TagMapKey(tags)
	if cachedTags, ok := r.tagCache.Get(key); ok {
		return cachedTags
	}

	tagCount := len(tags)
	result := make([]m3thrift.MetricTag, 0, tagCount)

	// For small maps, direct iteration with sort is faster than building intermediate slice
	type tagPair struct {
		key, value string
	}

	// Use stack-allocated array for small tag counts
	var pairs [3]tagPair
	i := 0
	for k, v := range tags {
		pairs[i] = tagPair{k, v}
		i++
	}

	// Simple in-place sort for 2-3 elements
	if tagCount == 2 {
		if pairs[0].key > pairs[1].key {
			pairs[0], pairs[1] = pairs[1], pairs[0]
		}
	} else if tagCount == 3 {
		// Simple bubble sort for 3 elements
		if pairs[0].key > pairs[1].key {
			pairs[0], pairs[1] = pairs[1], pairs[0]
		}
		if pairs[1].key > pairs[2].key {
			pairs[1], pairs[2] = pairs[2], pairs[1]
		}
		if pairs[0].key > pairs[1].key {
			pairs[0], pairs[1] = pairs[1], pairs[0]
		}
	}

	// Convert sorted pairs to metric tags
	for j := 0; j < tagCount; j++ {
		result = append(result, m3thrift.MetricTag{
			Name:  r.stringInterner.Intern(pairs[j].key),
			Value: r.stringInterner.Intern(pairs[j].value),
		})
	}

	r.tagCache.Set(key, result)
	return result
}

// convertManyTags handles the general case with many tags
func (r *reporter) convertManyTags(tags map[string]string) []m3thrift.MetricTag {
	key := cache.TagMapKey(tags)
	if cachedTags, ok := r.tagCache.Get(key); ok {
		return cachedTags
	}

	tagCount := len(tags)
	result := make([]m3thrift.MetricTag, 0, tagCount)

	// Use stack buffer for moderate number of tags
	var keysBuf [16]string
	var keys []string
	if tagCount <= 16 {
		keys = keysBuf[:0]
	} else {
		keys = make([]string, 0, tagCount)
	}

	// Collect keys
	for k := range tags {
		keys = append(keys, k)
	}

	// Sort keys efficiently
	sort.Strings(keys)

	// Build result in sorted order
	for _, k := range keys {
		result = append(result, m3thrift.MetricTag{
			Name:  r.stringInterner.Intern(k),
			Value: r.stringInterner.Intern(tags[k]),
		})
	}

	r.tagCache.Set(key, result)
	return result
}

// Update the main convertTags function to use the optimized version
func (r *reporter) convertTags(tags map[string]string) []m3thrift.MetricTag {
	return r.optimizedConvertTags(tags)
}

func (r *reporter) Flush() {
	if r.done.Load() {
		return
	}

	// Create acknowledgment channel for this flush
	flushAck := make(chan struct{})

	r.pending.Inc()

	select {
	case r.metCh <- pendingReport{cached: nil, flushAck: flushAck}:
		// Successfully sent flush signal with ack channel
	case <-r.donech:
		// Reporter is closing, this flush signal won't be processed normally.
		if r.pending.Load() > 0 {
			r.pending.Dec()
		}
		return
	}

	// Wait for flush acknowledgment or timeout
	select {
	case <-flushAck:
		// Flush completed successfully
		return
	case <-r.donech:
		// Reporter is closing
		return
	case <-time.After(2 * time.Second):
		// Flush timeout
		return
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

	// Close async transport if it exists
	if r.asyncTransport != nil {
		if closeErr := r.asyncTransport.Close(); closeErr != nil {
			err = closeErr
		}
	} else if r.client != nil && r.client.Transport != nil {
		if cl, ok := r.client.Transport.(io.Closer); ok {
			if closeErr := cl.Close(); closeErr != nil {
				err = closeErr
			}
		}
	}
	return err
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

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case report, ok := <-r.metCh:
			if !ok {
				r.finalFlush(mets, &currentBytesInBatch)
				return
			}
			mets = r.processReport(report, mets, &currentBytesInBatch)

		case <-ticker.C:
			if len(mets) > 0 {
				mets = r.internalFlush(mets, &currentBytesInBatch)
			}

		case <-r.donech:
			r.drainAndFlush(mets, &currentBytesInBatch)
			return
		}
	}
}

// generateMetricId creates a unique identifier for a metric based on its name and tags
func (r *reporter) generateMetricId(cached *cachedMetric) string {
	if cached == nil {
		return ""
	}

	// Use metric name as base
	metricId := cached.metricName

	// Append sorted tags to ensure consistent IDs
	for _, tag := range cached.metricTags {
		metricId += "|" + tag.Name + "=" + tag.Value
	}

	return metricId
}

func (r *reporter) processReport(report pendingReport, mets []m3thrift.Metric, currentBytesInBatch *int32) []m3thrift.Metric {
	if report.cached == nil {
		r.decrementPending()

		// Send flush acknowledgment if this was a flush request
		if report.flushAck != nil {
			close(report.flushAck)
		}

		if len(mets) > 0 {
			return r.internalFlush(mets, currentBytesInBatch)
		}
		return mets
	}

	if report.cached.isNoop {
		return mets
	}

	// Check timer limits before processing
	if report.valueType == timerType {
		metricId := r.generateMetricId(report.cached)
		currentCount := r.currentBatchTimerCounts[metricId]

		if currentCount >= r.maxTimersPerMetricPerBatch {
			// Drop this timer - exceeded limit for this metricId
			r.numTimersDroppedLimit.Add(1)
			return mets
		}

		// Increment count for this metricId
		r.currentBatchTimerCounts[metricId] = currentCount + 1
	}

	if len(mets) > 0 && *currentBytesInBatch+report.cached.size > r.freeBytes {
		mets = r.internalFlush(mets, currentBytesInBatch)
	}

	finalMetric := r.buildMetric(report)
	if finalMetric != nil {
		mets = append(mets, *finalMetric)
		*currentBytesInBatch += report.cached.size
	}

	return mets
}

func (r *reporter) buildMetric(report pendingReport) *m3thrift.Metric {
	finalMetric := m3thrift.NewMetric()

	hdrMemBuf := thrift.NewTMemoryBuffer()
	if _, err := hdrMemBuf.Write(report.cached.precomputedHeader); err != nil {
		return nil
	}

	hdrProto := r.protocolFactory.GetProtocol(hdrMemBuf)
	if err := finalMetric.Read(hdrProto); err != nil {
		return nil
	}

	finalMetric.Timestamp = r.now.Load()
	finalMetric.Value = r.buildMetricValue(report)
	return finalMetric
}

func (r *reporter) buildMetricValue(report pendingReport) m3thrift.MetricValue {
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
	return *metricVal
}

func (r *reporter) drainAndFlush(mets []m3thrift.Metric, currentBytesInBatch *int32) {
	for len(r.metCh) > 0 {
		report, ok := <-r.metCh
		if !ok {
			break
		}
		mets = r.processReport(report, mets, currentBytesInBatch)
	}
	r.finalFlush(mets, currentBytesInBatch)
}

func (r *reporter) finalFlush(mets []m3thrift.Metric, currentBytesInBatch *int32) {
	if len(mets) > 0 {
		r.internalFlush(mets, currentBytesInBatch)
	}
	r.resourcePool.releaseMetricSlice(mets)
}

func (r *reporter) decrementPending() {
	if r.pending.Load() > 0 {
		r.pending.Dec()
	}
}

func (r *reporter) estimateValueSize(mType metricType) int32 {
	switch mType {
	case counterType, timerType:
		return 15
	case gaugeType:
		return 13
	default:
		return 15
	}
}

// reportMetric consolidates the common reporting pattern
func (r *reporter) reportMetric(c *cachedMetric, valueType metricType, countVal int64, gaugeVal float64, timerVal time.Duration) {
	if c.isNoop || c.reporter == nil || c.reporter.done.Load() || c.metricType != valueType {
		return
	}

	c.reporter.pending.Inc()
	report := pendingReport{
		cached:    c,
		valueType: valueType,
		countVal:  countVal,
		gaugeVal:  gaugeVal,
		timerVal:  timerVal,
	}

	select {
	case c.reporter.metCh <- report:
		// Successfully queued
	case <-c.reporter.donech:
		// Reporter is shutting down
		c.reporter.pending.Dec()
	default:
		// Channel full, drop metric but track it
		c.reporter.pending.Dec()
		c.reporter.numDropped.Add(1)
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

	// Use async transport if available, otherwise fallback to blocking client
	if r.asyncTransport != nil {
		// All metrics use the same transport path now
		err := r.asyncTransport.SendAsync(batch)
		if err != nil {
			r.numWriteErrorsCounter.ReportCount(1)
			r.numWriteErrors.Add(1)
		}
	} else {
		err := r.client.EmitMetricBatchV2(*batch)
		if err != nil {
			r.numWriteErrorsCounter.ReportCount(1)
			r.numWriteErrors.Add(1)
		}
	}

	*currentBytesInBatch = 0

	// Reset timer counts for the new batch
	r.currentBatchTimerCounts = make(map[string]int)

	r.resourcePool.releaseMetricSlice(mets)
	return r.resourcePool.getMetricSlice()
}

func (r *reporter) reportInternalMetrics() {
	numBatches := r.numBatches.Swap(0)
	numMetrics := r.numMetrics.Swap(0)
	numWriteErrors := r.numWriteErrors.Swap(0)
	numDropped := r.numDropped.Swap(0)
	numTimersDroppedLimit := r.numTimersDroppedLimit.Swap(0)
	tagCacheLen := int64(r.tagCache.Len())

	// Get transport drop count if async transport is enabled
	var numTransportDropped int64
	if r.asyncTransport != nil {
		numTransportDropped = r.asyncTransport.DropCount()
		r.numTransportDropped.Store(numTransportDropped)
	}

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
	if r.numDroppedCounter != nil {
		r.numDroppedCounter.ReportCount(numDropped)
	}
	if r.numTransportDroppedCounter != nil {
		r.numTransportDroppedCounter.ReportCount(numTransportDropped)
	}
	if r.numTransportSuccessCounter != nil {
		r.numTransportSuccessCounter.ReportCount(numMetrics - numTransportDropped)
	}
	if r.numTimersDroppedLimitCounter != nil {
		r.numTimersDroppedLimitCounter.ReportCount(numTimersDroppedLimit)
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
	c.reporter.reportMetric(c, counterType, value, 0, 0)
}

func (c *cachedMetric) ReportGauge(value float64) {
	c.reporter.reportMetric(c, gaugeType, 0, value, 0)
}

func (c *cachedMetric) ReportTimer(interval time.Duration) {
	c.reporter.reportMetric(c, timerType, 0, 0, interval)
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
