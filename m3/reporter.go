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

// Package m3 implements a Tally reporter for M3 metrics.
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

// Protocol describes an M3 Thrift transport protocol.
type Protocol int

// Compact and Binary represent the compact and
// binary thrift protocols respectively.
const (
	Compact Protocol = iota
	Binary
)

const (
	// ServiceTag is the M3 service tag name.
	ServiceTag = "service"
	// EnvTag is the M3 env tag name.
	EnvTag = "env"
	// HostTag is the M3 host tag name.
	HostTag = "host"
	// DefaultMaxQueueSize is the default reporter queue size for backward compatibility,
	// mapping to transport buffer size in the current implementation.
	DefaultMaxQueueSize = 4096
	// DefaultMaxPacketSize is the default reporter max packet size.
	DefaultMaxPacketSize = int32(32768)
	// DefaultHistogramBucketIDName is the default histogram bucket ID tag name.
	DefaultHistogramBucketIDName = "bucketid"
	// DefaultHistogramBucketName is the default histogram bucket name tag name.
	DefaultHistogramBucketName = "bucket"
	// DefaultHistogramBucketTagPrecision is the default precision for formatting
	// histogram bucket bound values in metric tags.
	DefaultHistogramBucketTagPrecision = uint(6)
	// DefaultTagCacheSize is the default size for the tag map to canonical []MetricTag cache.
	DefaultTagCacheSize = 10000
	// DefaultTagCacheTTLSeconds is the default TTL for tag cache entries (1 hour).
	DefaultTagCacheTTLSeconds = int64(3600)
	// DefaultTransportBufferSize is the default buffer size for the asynchronous metric transport channel.
	DefaultTransportBufferSize = 1000
	// DefaultFlushInterval is the interval at which to flush metric batches.
	DefaultFlushInterval = 1 * time.Second
	// DefaultMaxTimersPerMetricPerBatch is the maximum number of timer samples allowed
	// per metric ID per batch. Samples exceeding this limit are dropped.
	DefaultMaxTimersPerMetricPerBatch = 1000

	_minMetricBucketIDTagLength = 4 // Minimum length for bucket ID strings.
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

// Reporter is a Tally CachedStatsReporter that sends metrics to M3.
// Metrics are batched and emitted via UDP using
// either Thrift compact or binary protocol.
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

type pendingMetric struct {
	cached     *cachedMetric
	metricType metricType
	value      interface{}
}

type batchOperationType int

const (
	opMetric batchOperationType = iota
	opFlush
	opShutdown
)

type batchOperation struct {
	opType batchOperationType
	metric *pendingMetric // Only used for opMetric
	done   chan struct{}  // Only used for opFlush
}

// reporter is a metrics backend that reports metrics to a local or
// remote M3 collector, metrics are batched together and emitted
// via either thrift compact or binary protocol in batch UDP packets.
type reporter struct {
	bucketIDTagName string               // Tag name for histogram bucket ID.
	bucketTagName   string               // Tag name for histogram bucket name/bound.
	bucketValFmt    string               // Format string for histogram bucket float values.
	buckets         []tally.BucketPair   // Pre-calculated bucket pairs for internal batch size histogram.
	client          *m3thrift.M3Client   // M3 Thrift client.
	commonTags      []m3thrift.MetricTag // Pre-serialized common tags for all metrics.
	commonTagsBytes []byte               // Serialized common tags part of the batch prefix.
	done            atomic.Bool          // Indicates if the reporter has been closed.
	donech          chan struct{}        // Signals goroutines to stop.
	freeBytes       int32                // Remaining bytes available in a packet after common tags and batch overhead.
	now             atomic.Int64         // Cached current time in nanoseconds, updated periodically.
	overheadBytes   int32                // Size of common tags and basic batch overhead in bytes.
	// resourcePool    *resourcePool // This field was part of a previous implementation and is no longer used.
	stringInterner *cache.StringInterner // Interner for tag keys and values to reduce allocations.
	tagCache       *cache.TagCache       // Cache for tag map to []m3thrift.MetricTag conversion.
	wg             sync.WaitGroup        // Coordinates goroutine shutdown.

	// Simplified single-level batching
	currentBatch      []m3thrift.Metric      // Current batch of metrics being built.
	currentBatchBytes int32                  // Current size of metrics in currentBatch (serialized size, excluding common tags).
	maxBatchBytes     int32                  // Maximum size of metrics in a batch (derived from freeBytes).
	sendCh            chan []m3thrift.Metric // Channel for sending batches to the asyncSender.
	operationCh       chan batchOperation    // Single channel for all batch operations (metrics, flushes, shutdown).
	dropCount         atomic.Int64           // Atomic counter for batches dropped by asyncSender (channel full).
	inputQueueDrops   atomic.Int64           // Atomic counter for operations dropped if operationCh is full.
	successCount      atomic.Int64           // Atomic counter for batches successfully sent by asyncSender.
	asyncEnabled      bool                   // True if asynchronous sending via sendCh is enabled.

	// Timer limiting per batch
	currentBatchTimerCounts    map[string]int // metricId -> count of timer samples in current batch.
	maxTimersPerMetricPerBatch int            // Configurable limit for timer samples per metric ID per batch.

	// Internal metrics reported by this library
	batchSizeHistogram    tally.CachedHistogram // Histogram of batch sizes sent.
	numBatches            atomic.Int64          // Total batches processed, periodically reported.
	numBatchesCounter     tally.CachedCount
	numMetrics            atomic.Int64 // Total metrics processed, periodically reported.
	numMetricsCounter     tally.CachedCount
	numWriteErrors        atomic.Int64 // Total M3 write errors, periodically reported.
	numWriteErrorsCounter tally.CachedCount
	numTagCacheCounter    tally.CachedCount // Current size of the tag cache, periodically reported.
	numDropped            atomic.Int64      // Metrics dropped due to reporter being closed, periodically reported.

	// Consolidated Drop Counters with reason tag
	dropsReporterDoneCounter       tally.CachedCount // Metric for numDropped (reason="reporter_done").
	dropsTransportFullCounter      tally.CachedCount // Metric for dropCount (reason="transport_full").
	dropsTimerLimitExceededCounter tally.CachedCount // Metric for numTimersDroppedLimit (reason="timer_limit_exceeded").
	dropsInputQueueFullCounter     tally.CachedCount // Metric for inputQueueDrops (reason="input_queue_full")

	// Atomics for accumulating counts
	// dropCount (defined above) accumulates batches dropped by asyncSender (channel full).
	// numDropped (defined above) accumulates metrics dropped due to reporter being closed.
	numTimersDroppedLimit atomic.Int64 // Accumulates timers dropped due to per-metric limits per batch.

	// Transport success
	numTransportSuccess        atomic.Int64      // Batches successfully sent by asyncSender, periodically reported.
	numTransportSuccessCounter tally.CachedCount // Metric for numTransportSuccess.

	protocolFactory thrift.TProtocolFactory // Thrift protocol factory for serialization.
}

// Options configures an M3 reporter.
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
	MaxTimersPerMetricPerBatch int // Maximum timer samples per metricId per batch.
}

// NewReporter creates and starts a new M3 reporter.
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
	// rPool := newResourcePool(pFactory) // Part of a previous implementation, no longer used.

	tagm := make(map[string]string)
	for k, v := range opts.CommonTags {
		tagm[k] = v
	}

	if tagm[ServiceTag] == "" {
		if opts.Service == "" {
			return nil, fmt.Errorf("%s common tag is required", ServiceTag)
		}
		tagm[ServiceTag] = opts.Service
	}
	if tagm[EnvTag] == "" {
		if opts.Env == "" {
			return nil, fmt.Errorf("%s common tag is required", EnvTag)
		}
		tagm[EnvTag] = opts.Env
	}
	if opts.IncludeHost {
		if tagm[HostTag] == "" {
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
		stringInterner:  tempInterner, // Use the interner created for common tags
		tagCache: cache.NewTagCacheWithOptions(cache.TagCacheOptions{
			MaxSize:    DefaultTagCacheSize,
			TTLSeconds: DefaultTagCacheTTLSeconds,
		}),
		protocolFactory:            pFactory,
		currentBatchTimerCounts:    make(map[string]int),
		maxTimersPerMetricPerBatch: opts.MaxTimersPerMetricPerBatch,
		asyncEnabled:               !opts.DisableAsyncTransport,
		operationCh:                make(chan batchOperation, opts.TransportBufferSize),
	}

	// Initialize async sending if enabled
	if r.asyncEnabled {
		r.sendCh = make(chan []m3thrift.Metric, opts.TransportBufferSize)
		r.wg.Add(1)
		go r.asyncSender()
	}

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

	// Allocate consolidated drop counters
	r.dropsReporterDoneCounter = r.AllocateCounter("tally.internal.drops", addReasonTag(internalScopeTags, "reporter_done"))
	r.dropsTransportFullCounter = r.AllocateCounter("tally.internal.drops", addReasonTag(internalScopeTags, "transport_full"))
	r.dropsTimerLimitExceededCounter = r.AllocateCounter("tally.internal.drops", addReasonTag(internalScopeTags, "timer_limit_exceeded"))
	r.dropsInputQueueFullCounter = r.AllocateCounter("tally.internal.drops", addReasonTag(internalScopeTags, "input_queue_full"))

	// Keep non-drop specific counters like transport success
	r.numTransportSuccessCounter = r.AllocateCounter("tally.internal.num-transport-success", internalScopeTags)

	// Start maintenance goroutine (handles now, flush, internal metrics reporting)
	r.wg.Add(1)
	go r.maintenanceLoop()

	// Start the batch builder goroutine
	r.wg.Add(1)
	go r.batchBuilderLoop()

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

// allocateMetric pre-serializes metric name and tags, preparing a cachedMetric.
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

	metricID := r.getMetricID(canonicalTags)

	return &cachedMetric{
		reporter:          r,
		metricName:        internedName,
		metricTags:        canonicalTags,
		precomputedHeader: precomputedData,
		size:              int32(len(precomputedData)) + estimatedValueAndTimestampSize,
		metricType:        mType,
		isNoop:            false,
		metricID:          metricID,
	}
}

// AllocateHistogram pre-allocates histogram buckets as individual counters.
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

// convertTags converts a tag map to a canonical, sorted slice of M3 MetricTags,
// utilizing a cache for performance.
func (r *reporter) convertTags(tags map[string]string) []m3thrift.MetricTag {
	tagCount := len(tags)
	if tagCount == 0 {
		return nil
	}

	// Key for the cache
	key := cache.TagMapKey(tags)
	if cachedTags, ok := r.tagCache.Get(key); ok {
		return cachedTags
	}

	// If it's a cache miss, convertManyTags will handle it and update the cache.
	// convertManyTags is capable of handling all tag counts (1 to many).
	return r.convertManyTags(tags)
}

// convertManyTags handles tag map to []MetricTag conversion for any number of tags.
// It sorts tags by name and interns strings for efficiency, caching the result.
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

// asyncSender processes metric batches from sendCh for asynchronous transmission.
func (r *reporter) asyncSender() {
	defer r.wg.Done()
	// This loop will naturally terminate when sendCh is closed by batchBuilderLoop.
	for metrics := range r.sendCh {
		r.sendBatch(metrics)
	}
}

// sendBatch transmits a single M3 MetricBatch to the configured collector.
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

// addToBatchWithSize adds a metric with a pre-calculated size to the current batch,
// flushing the batch if it would exceed maxBatchBytes.
func (r *reporter) addToBatchWithSize(metric m3thrift.Metric, metricSize int32) {
	// Check if we need to flush before adding this metric
	if len(r.currentBatch) > 0 && (r.currentBatchBytes+metricSize > r.maxBatchBytes) {
		r.flushBatch()
	}

	// Add metric to current batch
	r.currentBatch = append(r.currentBatch, metric)
	r.currentBatchBytes += metricSize
	r.numMetrics.Inc()

	// Flush if batch is full
	if r.currentBatchBytes >= r.maxBatchBytes {
		r.flushBatch()
	}
}

// flushBatch sends the current batch and resets batching state.
// It is called ONLY by batchBuilderLoop. It does not use batchMu.
func (r *reporter) flushBatch() {
	if len(r.currentBatch) == 0 {
		return
	}

	metrics := make([]m3thrift.Metric, len(r.currentBatch))
	copy(metrics, r.currentBatch)

	if r.asyncEnabled {
		select {
		case r.sendCh <- metrics:
			// Sent successfully
		default:
			// Transport is full. Drop the batch.
			r.dropCount.Add(1)
		}
	} else {
		go r.sendBatch(metrics)
	}

	// Reset batch state
	r.currentBatch = r.currentBatch[:0]
	r.currentBatchBytes = 0
	for k := range r.currentBatchTimerCounts {
		delete(r.currentBatchTimerCounts, k)
	}
}

// This is a non-blocking operation.
func (r *reporter) forceFlush() {
	if r.done.Load() {
		return
	}
	select {
	case r.operationCh <- batchOperation{opType: opFlush}:
		// Signal sent successfully.
	default:
		// Do nothing. If the batcher is busy, we don't block.
		// The periodic flush or shutdown sequence will handle flushing.
	}
}

// Flush ensures any buffered metrics are sent. It waits for the flush to complete synchronously.
func (r *reporter) Flush() {
	if r.done.Load() {
		return
	}

	// Create a completion channel for this flush
	done := make(chan struct{})

	// Send the completion channel to the batch builder
	select {
	case r.operationCh <- batchOperation{opType: opFlush, done: done}:
		// Wait for flush completion
		<-done
	default:
		// Channel is full, fallback to async flush
		r.forceFlush()
	}
}

// Close flushes any pending metrics, closes channels, and releases resources.
func (r *reporter) Close() error {
	if !r.done.CAS(false, true) {
		return errAlreadyClosed
	}

	// 1. Signal all goroutines to stop their loops and begin shutdown.
	close(r.donech)

	// 2. Close the operation channel. Since donech is closed, no new metrics
	// will be produced by maintenanceLoop, making this safe. batchBuilderLoop
	// will see this closure as it drains.
	close(r.operationCh)

	// 3. Wait for all goroutines to finish.
	// - maintenanceLoop exits because donech is closed.
	// - batchBuilderLoop drains operationCh, flushes, closes sendCh, and exits.
	// - asyncSender sees sendCh is closed, drains it, and exits.
	r.wg.Wait()

	// 4. Close underlying transport.
	if r.client != nil && r.client.Transport != nil {
		if cl, ok := r.client.Transport.(io.Closer); ok {
			return cl.Close()
		}
	}
	return nil
}

// reportMetric is the internal entry point for recording any metric type.
// With the single-writer goroutine, this now sends the metric to operationCh.
func (r *reporter) reportMetric(mType metricType, cachedMetric *cachedMetric, value interface{}) {
	if r.done.Load() {
		r.numDropped.Inc() // Drops due to reporter already closed
		return
	}

	pMetric := pendingMetric{
		cached:     cachedMetric,
		metricType: mType,
		value:      value,
	}

	select {
	case r.operationCh <- batchOperation{opType: opMetric, metric: &pMetric}:
		// Metric successfully enqueued for batching
	default:
		// operationCh is full, drop the metric
		r.inputQueueDrops.Inc()
	}
}

// buildMetric constructs an M3 Metric from cached data and a reported value.
// This function will be called by the batchBuilderLoop.
// It also enforces timer sample limits.
func (r *reporter) buildMetric(mType metricType, cachedMetric *cachedMetric, value interface{}) *m3thrift.Metric {
	// Create metric value
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
			metricID := cachedMetric.metricID
			currentCount := r.currentBatchTimerCounts[metricID] // Accessed only by batchBuilderLoop
			if currentCount >= r.maxTimersPerMetricPerBatch {
				r.numTimersDroppedLimit.Inc()
				return nil
			}
			r.currentBatchTimerCounts[metricID] = currentCount + 1 // Accessed only by batchBuilderLoop

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

// getMetricID generates a unique string ID for a metric based on its canonical tags.
// This ID is used for per-metric timer sample limiting.
func (r *reporter) getMetricID(tags []m3thrift.MetricTag) string {
	// Use a simple approach - combine all tag key-value pairs
	var parts []string
	for _, tag := range tags {
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

// reportInternalMetrics sends metrics about the reporter's own operational state.
// It directly calls ReportCount/ReportSamples on the pre-allocated cached metric handles.
func (r *reporter) reportInternalMetrics() {
	// Check if reporter is closed before trying to report internal metrics
	// This prevents deadlocks during shutdown
	if r.done.Load() {
		return
	}

	numBatches := r.numBatches.Swap(0)
	numMetricsProcessedByBatcher := r.numMetrics.Swap(0)
	numWriteErrors := r.numWriteErrors.Swap(0)
	tagCacheLen := int64(r.tagCache.Len())

	reporterDoneDrops := r.numDropped.Swap(0)
	transportFullDrops := r.dropCount.Swap(0)
	timerLimitExceededDrops := r.numTimersDroppedLimit.Swap(0)
	inputQueueFullDrops := r.inputQueueDrops.Swap(0)
	transportSuccesses := r.successCount.Swap(0)

	if r.batchSizeHistogram != nil {
		var avgBatchSize float64
		if numBatches > 0 {
			avgBatchSize = float64(numMetricsProcessedByBatcher) / float64(numBatches)
		}
		// Corrected bucket reporting logic
		var bucketValueToReport float64
		if len(r.buckets) > 0 {
			idx := sort.Search(len(r.buckets), func(i int) bool { return r.buckets[i].UpperBoundValue() >= avgBatchSize })
			if idx < len(r.buckets) {
				bucketValueToReport = r.buckets[idx].UpperBoundValue()
			} else {
				bucketValueToReport = r.buckets[len(r.buckets)-1].UpperBoundValue()
			}
		} // If r.buckets is empty, bucketValueToReport remains 0.0, which is acceptable.
		r.batchSizeHistogram.ValueBucket(0, bucketValueToReport).ReportSamples(1)
	}

	if r.numBatchesCounter != nil {
		r.numBatchesCounter.ReportCount(numBatches)
	}
	if r.numMetricsCounter != nil {
		r.numMetricsCounter.ReportCount(numMetricsProcessedByBatcher)
	}
	if r.numWriteErrorsCounter != nil {
		r.numWriteErrorsCounter.ReportCount(numWriteErrors)
	}
	if r.numTagCacheCounter != nil {
		r.numTagCacheCounter.ReportCount(tagCacheLen)
	}

	if r.dropsReporterDoneCounter != nil {
		r.dropsReporterDoneCounter.ReportCount(reporterDoneDrops)
	}
	if r.dropsTransportFullCounter != nil {
		r.dropsTransportFullCounter.ReportCount(transportFullDrops)
	}
	if r.dropsTimerLimitExceededCounter != nil {
		r.dropsTimerLimitExceededCounter.ReportCount(timerLimitExceededDrops)
	}
	if r.dropsInputQueueFullCounter != nil {
		r.dropsInputQueueFullCounter.ReportCount(inputQueueFullDrops)
	}

	if r.numTransportSuccessCounter != nil {
		r.numTransportSuccessCounter.ReportCount(transportSuccesses)
	}
}

// maintenanceLoop periodically updates the reporter's notion of the current time,
// reports internal metrics, and signals for a time-based batch flush.
func (r *reporter) maintenanceLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(DefaultFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.now.Store(time.Now().UnixNano())
			r.forceFlush()            // Flush any pending user metrics first
			r.reportInternalMetrics() // Report internal metrics (will be in separate batch)
			r.forceFlush()            // Flush internal metrics
		case <-r.donech:
			return
		}
	}
}

// batchBuilderLoop is the single writer goroutine responsible for building and flushing batches.
func (r *reporter) batchBuilderLoop() {
	defer r.wg.Done()
	// As the sole producer for sendCh, batchBuilderLoop is responsible for closing it.
	if r.asyncEnabled {
		defer close(r.sendCh)
	}

	for {
		select {
		case op, ok := <-r.operationCh:
			if !ok { // operationCh has been closed
				r.flushBatch() // Final flush
				return
			}

			switch op.opType {
			case opMetric:
				metric := r.buildMetric(op.metric.metricType, op.metric.cached, op.metric.value)
				if metric == nil {
					continue
				}

				metricSize := op.metric.cached.size
				if len(r.currentBatch) > 0 && (r.currentBatchBytes+metricSize > r.maxBatchBytes) {
					r.flushBatch()
				}

				r.currentBatch = append(r.currentBatch, *metric)
				r.currentBatchBytes += metricSize
				r.numMetrics.Inc()

				if r.currentBatchBytes >= r.maxBatchBytes {
					r.flushBatch()
				}

			case opFlush:
				r.flushBatch()
				// Signal completion if this was a synchronous flush
				if op.done != nil {
					close(op.done)
				}

			}
		}
	}
}

// addReasonTag is a helper to create a new tag map with an added 'reason' tag.
func addReasonTag(baseTags map[string]string, reason string) map[string]string {
	newTags := make(map[string]string, len(baseTags)+1)
	for k, v := range baseTags {
		newTags[k] = v
	}
	newTags["reason"] = reason
	return newTags
}

//
// Definitions restored from previous state
//

type cachedMetric struct {
	reporter          *reporter
	precomputedHeader []byte
	size              int32
	metricName        string
	metricTags        []m3thrift.MetricTag
	metricType        metricType
	isNoop            bool
	metricID          string
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
