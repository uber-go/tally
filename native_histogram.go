// Copyright (c) 2026 Uber Technologies, Inc.
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

package tally

import (
	"errors"
	"sync"
	"time"
)

// DefaultNativeHistogramMaxBuckets is the bucket budget used when a scope
// does not configure one. Matches the OpenTelemetry exponential histogram
// default max size.
const DefaultNativeHistogramMaxBuckets = 160

// ErrNativeHistogramBackendNotConfigured is returned by MarshalBinary when a
// scope has no ScopeOptions.NativeHistogramFactory. Values are still counted,
// but there is no encoder to serialize them with, so nothing is reported.
var ErrNativeHistogramBackendNotConfigured = errors.New(
	"native histogram backend not configured: " +
		"set ScopeOptions.NativeHistogramFactory",
)

// NativeHistogramData is a mergeable accumulator backing a NativeHistogram,
// supplied by the application through ScopeOptions.NativeHistogramFactory.
//
// Implementations need not be safe for concurrent use; the owning scope
// serializes every call. MarshalBinary is expected to produce a self-describing
// encoding of everything accumulated since the last Clear, in whatever format
// the configured reporter understands.
type NativeHistogramData interface {
	// Update records a single observation.
	Update(value float64)

	// Count returns the number of observations accumulated since the last
	// Clear.
	Count() uint64

	// Clear discards everything accumulated so far.
	Clear()

	// MarshalBinary serializes everything accumulated since the last Clear.
	MarshalBinary() ([]byte, error)
}

// NativeHistogram is the interface for emitting native histogram metrics: a
// distribution whose buckets are derived from the observed values rather than
// declared up front, reported as one serialized payload per interval instead
// of as a counter per bucket.
type NativeHistogram interface {
	// RecordValue records a specific value directly.
	RecordValue(value float64)

	// RecordDuration records a specific duration directly, as seconds.
	RecordDuration(value time.Duration)

	// Start gives you a specific point in time to then record a duration.
	Start() Stopwatch
}

type nativeHistogram struct {
	// mtx guards data, whose implementations are supplied by the application
	// and are not required to be safe for concurrent use.
	mtx  sync.Mutex
	data NativeHistogramData

	cachedNativeHistogram CachedNativeHistogram
}

func newNativeHistogram(
	data NativeHistogramData,
	cachedNativeHistogram CachedNativeHistogram,
) *nativeHistogram {
	return &nativeHistogram{
		data:                  data,
		cachedNativeHistogram: cachedNativeHistogram,
	}
}

func (h *nativeHistogram) RecordValue(value float64) {
	h.mtx.Lock()
	defer h.mtx.Unlock()

	h.data.Update(value)
}

// RecordDuration records value in SECONDS, matching the units that
// DurationBuckets.AsValues() reports durations in. Reporters that need another
// unit on the wire convert on the way out.
func (h *nativeHistogram) RecordDuration(value time.Duration) {
	h.RecordValue(value.Seconds())
}

func (h *nativeHistogram) Start() Stopwatch {
	return NewStopwatch(globalNow(), h)
}

func (h *nativeHistogram) RecordStopwatch(stopwatchStart time.Time) {
	d := globalNow().Sub(stopwatchStart)
	h.RecordDuration(d)
}

// drain marshals everything accumulated since the last drain and resets the
// accumulator, so each payload is a delta covering one reporting interval
// rather than the distribution's whole history.
//
// A marshal error leaves the accumulator intact, so the samples are retried on
// the next cycle instead of being dropped. That is also what makes a scope
// without a NativeHistogramFactory inert rather than broken:
// defaultNativeHistogramData never marshals, so every cycle is skipped.
func (h *nativeHistogram) drain() ([]byte, uint64, bool) {
	h.mtx.Lock()
	defer h.mtx.Unlock()

	samples := h.data.Count()
	if samples == 0 {
		return nil, 0, false
	}

	payload, err := h.data.MarshalBinary()
	if err != nil {
		return nil, 0, false
	}

	h.data.Clear()

	return payload, samples, true
}

func (h *nativeHistogram) report(name string, tags map[string]string, r StatsReporter) {
	payload, samples, ok := h.drain()
	if !ok {
		return
	}

	r.ReportNativeHistogram(name, tags, payload, samples)
}

func (h *nativeHistogram) cachedReport() {
	payload, samples, ok := h.drain()
	if !ok {
		return
	}

	h.cachedNativeHistogram.ReportNativeHistogram(payload, samples)
}

func (h *nativeHistogram) snapshot() uint64 {
	h.mtx.Lock()
	defer h.mtx.Unlock()

	return h.data.Count()
}

// defaultNativeHistogramData counts observations but cannot serialize them.
// It is what a scope falls back to when no NativeHistogramFactory is set, so
// that NativeHistogram is safe to call but emits nothing.
type defaultNativeHistogramData struct {
	count uint64
}

func defaultNativeHistogramFactory(maxBuckets int) NativeHistogramData {
	return &defaultNativeHistogramData{}
}

func (d *defaultNativeHistogramData) Update(value float64) {
	d.count++
}

func (d *defaultNativeHistogramData) Count() uint64 {
	return d.count
}

func (d *defaultNativeHistogramData) Clear() {
	d.count = 0
}

func (d *defaultNativeHistogramData) MarshalBinary() ([]byte, error) {
	return nil, ErrNativeHistogramBackendNotConfigured
}
