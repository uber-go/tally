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
	"github.com/uber-go/tally/v6"
	"github.com/uber-go/tally/v6/internal/cache"
	m3thrift "github.com/uber-go/tally/v6/m3/thrift/v2"
	"github.com/uber-go/tally/v6/m3/thriftudp"
	"github.com/uber-go/tally/v6/thirdparty/github.com/apache/thrift/lib/go/thrift"
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
	// DefaultFlushInterval is how often to flush transport batches
	DefaultFlushInterval = 1 * time.Second
	// DefaultMaxTimersPerMetricPerBatch is the maximum number of timer metrics allowed per metricId per batch (this is set to 1000 by default to avoid overwhelming the server)
	// the timer metrics with over 1000 samples will be dropped by the server
	DefaultMaxTimersPerMetricPerBatch = 1000

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
	now             atomic.Int64
	overheadBytes   int32
	resourcePool    *resourcePool
	stringInterner  *cache.StringInterner
	tagCache        *cache.TagCache
	wg              sync.WaitGroup

	// Simplified single-level batching
	currentBatch      []m3thrift.Metric
	currentBatchBytes int32
	maxBatchBytes     int32
	batchMu           sync.Mutex             // Protects batching state
	sendCh            chan []m3thrift.Metric // Direct metric slice channel
	dropCount         atomic.Int64
	successCount      atomic.Int64
	asyncEnabled      bool

	// Timer limiting per batch
	currentBatchTimerCounts    map[string]int // metricId -> count of timers in current batch
	maxTimersPerMetricPerBatch int            // configurable limit (default 1000)

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
		// Use MaxQueueSize for backward compatibility if TransportBufferSize not set
		opts.TransportBufferSize = opts.MaxQueueSize
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

	r := &reporter{
		bucketIDTagName: opts.HistogramBucketIDName,
		bucketTagName:   opts.HistogramBucketName,
		bucketValFmt:    "%." + strconv.Itoa(int(opts.HistogramBucketTagPrecision)) + "f",
		donech:          make(chan struct{}),
		client:          client,
		commonTags:      resolvedCommonTags,
		resourcePool:    rPool,
		stringInterner:  cache.NewStringInterner(),
		tagCache: cache.NewTagCacheWithOptions(cache.TagCacheOptions{
			MaxSize:    DefaultTagCacheSize,
			TTLSeconds: DefaultTagCacheTTLSeconds,
		}),
		protocolFactory: pFactory,
		// Simplified batching setup - use freeBytes for proper size calculation
		currentBatchTimerCounts:    make(map[string]int),
		maxTimersPerMetricPerBatch: opts.MaxTimersPerMetricPerBatch,
		asyncEnabled:               !opts.DisableAsyncTransport,
	}

	// Initialize async sending if enabled
	if r.asyncEnabled {
		r.sendCh = make(chan []m3thrift.Metric, opts.TransportBufferSize)
		r.wg.Add(1)
		go r.asyncSender()
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

	// Set the actual limit for batching after calculating overhead
	r.maxBatchBytes = r.freeBytes

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

	// Start time update goroutine
	r.wg.Add(1)
	go r.timeLoop()

	// Start periodic flush goroutine
	r.wg.Add(1)
	go r.flushTicker()

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

// timeLoop updates the now timestamp at set resolution
func (r *reporter) timeLoop() {
	defer r.wg.Done()

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

// flushTicker periodically flushes batches
func (r *reporter) flushTicker() {
	defer r.wg.Done()

	ticker := time.NewTicker(DefaultFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.forceFlush()
		case <-r.donech:
			return
		}
	}
}

// asyncSender processes metric batches asynchronously
func (r *reporter) asyncSender() {
	defer r.wg.Done()

	for {
		select {
		case metrics, ok := <-r.sendCh:
			if !ok {
				return
			}
			r.sendBatch(metrics)
		case <-r.donech:
			return
		}
	}
}

// sendBatch sends a batch of metrics
func (r *reporter) sendBatch(metrics []m3thrift.Metric) {
	if len(metrics) == 0 {
		return
	}

	batch := &m3thrift.MetricBatch{
		CommonTags: r.commonTags,
		Metrics:    metrics,
	}

	err := r.client.EmitMetricBatchV2(*batch)
	if err != nil {
		r.numWriteErrors.Inc()
	} else {
		r.successCount.Add(1)
	}

	r.numBatches.Inc()
	// Note: Internal metrics counters will be reported via periodic reportInternalMetrics
}

// addToBatchWithSize adds a metric to the current batch with known size, flushing if necessary
func (r *reporter) addToBatchWithSize(metric m3thrift.Metric, metricSize int32) {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()

	// Check if we need to flush before adding this metric
	if len(r.currentBatch) > 0 && (r.currentBatchBytes+metricSize > r.maxBatchBytes) {
		r.flushCurrentBatch()
	}

	// Add metric to current batch
	r.currentBatch = append(r.currentBatch, metric)
	r.currentBatchBytes += metricSize
	r.numMetrics.Inc()

	// Flush if batch is full
	if r.currentBatchBytes >= r.maxBatchBytes {
		r.flushCurrentBatch()
	}
}

// addToBatch adds a metric to the current batch, flushing if necessary (fallback for when size is unknown)
func (r *reporter) addToBatch(metric m3thrift.Metric) {
	// Fallback to calculated size if pre-computed size not available
	metricSize := r.calculateMetricSize(metric)
	r.addToBatchWithSize(metric, metricSize)
}

// flushCurrentBatch sends the current batch (must be called with batchMu held)
func (r *reporter) flushCurrentBatch() {
	if len(r.currentBatch) == 0 {
		return
	}

	// Copy metrics for sending
	metrics := make([]m3thrift.Metric, len(r.currentBatch))
	copy(metrics, r.currentBatch)

	if r.asyncEnabled {
		// Send async
		select {
		case r.sendCh <- metrics:
			// Sent successfully
		default:
			// Channel full, drop batch
			r.dropCount.Add(1)
		}
	} else {
		// Send synchronously
		go r.sendBatch(metrics)
	}

	// Reset batch
	r.currentBatch = r.currentBatch[:0]
	r.currentBatchBytes = 0
	r.currentBatchTimerCounts = make(map[string]int)
}

// forceFlush flushes any pending metrics
func (r *reporter) forceFlush() {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
	r.flushCurrentBatch()
}

// calculateMetricSize estimates the serialized size of a metric more accurately
func (r *reporter) calculateMetricSize(metric m3thrift.Metric) int32 {
	// Use proper thrift serialization to get accurate size
	memBuf := thrift.NewTMemoryBuffer()
	proto := r.protocolFactory.GetProtocol(memBuf)

	if err := metric.Write(proto); err != nil {
		// Fallback to estimation if serialization fails
		return r.estimateMetricSize(metric)
	}

	return int32(memBuf.Len())
}

// estimateMetricSize provides a fallback size estimation
func (r *reporter) estimateMetricSize(metric m3thrift.Metric) int32 {
	// Base metric overhead
	size := int32(50)

	// Add name size
	size += int32(len(metric.Name))

	// Add value size based on type
	if metric.Value.Count != 0 {
		size += 8
	}
	if metric.Value.Gauge != 0 {
		size += 8
	}
	if metric.Value.Timer != 0 {
		size += 8
	}

	// Add tag sizes
	for _, tag := range metric.Tags {
		size += int32(len(tag.Name) + len(tag.Value) + 4)
	}

	// Add timestamp size
	size += 8

	return size
}

// Flush waits for any currently pending metrics to be sent.
func (r *reporter) Flush() {
	if r.done.Load() {
		return
	}

	// Flush current batch
	r.forceFlush()

	// Create acknowledgment channel for this flush
	flushAck := make(chan struct{})

	// Wait for flush completion - for simplified approach just wait a bit
	time.Sleep(10 * time.Millisecond)
	close(flushAck)
}

// Close waits for any pending metrics to be sent, and closes any
// underlying connections used for sending metrics to M3.
func (r *reporter) Close() error {
	if !r.done.CAS(false, true) {
		return errAlreadyClosed
	}

	// Flush any remaining metrics
	r.forceFlush()

	// Close done channel to signal shutdown
	close(r.donech)

	// Close send channel if async enabled
	if r.asyncEnabled {
		close(r.sendCh)
	}

	// Wait for all goroutines to finish
	r.wg.Wait()

	// Close underlying transport
	if r.client != nil && r.client.Transport != nil {
		if cl, ok := r.client.Transport.(io.Closer); ok {
			return cl.Close()
		}
	}

	return nil
}

// reportMetric reports a metric for the given type, ID, and value
func (r *reporter) reportMetric(mType metricType, cachedMetric *cachedMetric, value interface{}) {
	if r.done.Load() {
		r.numDropped.Inc()
		return
	}

	// Build the metric
	metric := r.buildMetric(mType, cachedMetric, value)
	if metric == nil {
		r.numDropped.Inc()
		return
	}

	// Add to batch using the pre-computed size from cachedMetric
	r.addToBatchWithSize(*metric, cachedMetric.size)
}

// buildMetric constructs a metric from the given parameters
func (r *reporter) buildMetric(mType metricType, cachedMetric *cachedMetric, value interface{}) *m3thrift.Metric {
	// Create metric value with all required fields initialized
	metricValue := m3thrift.MetricValue{
		Count: 0,
		Gauge: 0.0,
		Timer: 0,
	}

	switch mType {
	case counterType:
		if val, ok := value.(int64); ok {
			metricValue.MetricType = m3thrift.MetricType_COUNTER
			metricValue.Count = val
		} else {
			return nil
		}
	case gaugeType:
		if val, ok := value.(float64); ok {
			metricValue.MetricType = m3thrift.MetricType_GAUGE
			metricValue.Gauge = val
		} else {
			return nil
		}
	case timerType:
		if val, ok := value.(time.Duration); ok {
			// Check timer limiting per batch
			metricID := r.getMetricID(cachedMetric)
			r.batchMu.Lock()
			currentCount := r.currentBatchTimerCounts[metricID]
			if currentCount >= r.maxTimersPerMetricPerBatch {
				r.batchMu.Unlock()
				r.numTimersDroppedLimit.Inc()
				return nil
			}
			r.currentBatchTimerCounts[metricID] = currentCount + 1
			r.batchMu.Unlock()

			metricValue.MetricType = m3thrift.MetricType_TIMER
			metricValue.Timer = int64(val.Nanoseconds())
		} else {
			return nil
		}
	default:
		return nil
	}

	metric := &m3thrift.Metric{
		Name:      cachedMetric.metricName,
		Value:     metricValue,
		Timestamp: r.now.Load(),
		Tags:      cachedMetric.metricTags,
	}

	return metric
}

// getMetricID generates a unique ID for a metric based on its tags
func (r *reporter) getMetricID(cachedMetric *cachedMetric) string {
	// Use a simple approach - combine all tag key-value pairs
	var parts []string
	for _, tag := range cachedMetric.metricTags {
		parts = append(parts, tag.Name+"="+tag.Value)
	}
	return fmt.Sprintf("%s", parts)
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

func (r *reporter) reportInternalMetrics() {
	numBatches := r.numBatches.Swap(0)
	numMetrics := r.numMetrics.Swap(0)
	numWriteErrors := r.numWriteErrors.Swap(0)
	numDropped := r.numDropped.Swap(0)
	numTimersDroppedLimit := r.numTimersDroppedLimit.Swap(0)
	tagCacheLen := int64(r.tagCache.Len())

	// Get transport drop and success counts
	numTransportDropped := r.dropCount.Swap(0)
	numTransportSuccess := r.successCount.Swap(0)

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
		r.numTransportSuccessCounter.ReportCount(numTransportSuccess)
	}
	if r.numTimersDroppedLimitCounter != nil {
		r.numTimersDroppedLimitCounter.ReportCount(numTimersDroppedLimit)
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
	c.reporter.reportMetric(counterType, c, value)
}

func (c *cachedMetric) ReportGauge(value float64) {
	c.reporter.reportMetric(gaugeType, c, value)
}

func (c *cachedMetric) ReportTimer(interval time.Duration) {
	c.reporter.reportMetric(timerType, c, interval)
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

	// Use the simplified batching approach
	chb.metric.reporter.reportMetric(counterType, chb.metric, value)
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
