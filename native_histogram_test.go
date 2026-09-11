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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testNativeHistogramData is a NativeHistogramData that records what it is
// given so tests can assert on the payload and on drain semantics.
type testNativeHistogramData struct {
	values     []float64
	marshalErr error
	marshals   int
	clears     int
}

func newTestNativeHistogramData() *testNativeHistogramData {
	return &testNativeHistogramData{}
}

func (d *testNativeHistogramData) Update(value float64) {
	d.values = append(d.values, value)
}

func (d *testNativeHistogramData) Count() uint64 {
	return uint64(len(d.values))
}

func (d *testNativeHistogramData) Clear() {
	d.values = nil
	d.clears++
}

func (d *testNativeHistogramData) MarshalBinary() ([]byte, error) {
	d.marshals++
	if d.marshalErr != nil {
		return nil, d.marshalErr
	}
	return []byte(fmt.Sprint(d.values)), nil
}

type testCachedNativeHistogram struct {
	payloads [][]byte
	samples  []uint64
}

func (c *testCachedNativeHistogram) ReportNativeHistogram(payload []byte, samples uint64) {
	c.payloads = append(c.payloads, payload)
	c.samples = append(c.samples, samples)
}

func TestNativeHistogramRecordValue(t *testing.T) {
	data := newTestNativeHistogramData()
	h := newNativeHistogram(data, nil)

	h.RecordValue(1.5)
	h.RecordValue(2.5)

	assert.Equal(t, []float64{1.5, 2.5}, data.values)
	assert.Equal(t, uint64(2), h.snapshot())
}

// RecordDuration must convert to seconds: the same units that
// DurationBuckets.AsValues() uses. Getting this wrong silently misaligns
// percentiles against tally's own bucketed histograms.
func TestNativeHistogramRecordDurationRecordsSeconds(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected float64
	}{
		{duration: 0, expected: 0},
		{duration: time.Nanosecond, expected: 1e-9},
		{duration: time.Microsecond, expected: 1e-6},
		{duration: time.Millisecond, expected: 0.001},
		{duration: 250 * time.Millisecond, expected: 0.25},
		{duration: time.Second, expected: 1},
		{duration: 90 * time.Second, expected: 90},
		{duration: -time.Second, expected: -1},
	}

	for _, tt := range tests {
		t.Run(tt.duration.String(), func(t *testing.T) {
			data := newTestNativeHistogramData()
			h := newNativeHistogram(data, nil)

			h.RecordDuration(tt.duration)

			require.Equal(t, 1, len(data.values))
			assert.Equal(t, tt.expected, data.values[0])
		})
	}
}

// A duration must land on the same value the equivalent bucketed histogram
// would have used for it.
func TestNativeHistogramDurationUnitsMatchDurationBuckets(t *testing.T) {
	buckets := DurationBuckets{
		10 * time.Millisecond,
		250 * time.Millisecond,
		3 * time.Second,
	}

	data := newTestNativeHistogramData()
	h := newNativeHistogram(data, nil)
	for _, d := range buckets {
		h.RecordDuration(d)
	}

	assert.Equal(t, buckets.AsValues(), data.values)
}

func TestNativeHistogramStopwatch(t *testing.T) {
	now := time.Now()
	restore := globalNow
	globalNow = func() time.Time { return now }
	defer func() { globalNow = restore }()

	data := newTestNativeHistogramData()
	h := newNativeHistogram(data, nil)

	sw := h.Start()
	now = now.Add(1500 * time.Millisecond)
	sw.Stop()

	require.Equal(t, 1, len(data.values))
	assert.Equal(t, 1.5, data.values[0])
}

func TestNativeHistogramReport(t *testing.T) {
	data := newTestNativeHistogramData()
	h := newNativeHistogram(data, nil)
	r := newStatsTestReporter()

	h.RecordValue(1)
	h.RecordValue(2)
	h.report("nh", nil, r)

	assert.Equal(t, []byte("[1 2]"), r.nativeHistogramPayload)
	assert.Equal(t, uint64(2), r.nativeHistogramSamples)
}

func TestNativeHistogramReportNothingWithoutSamples(t *testing.T) {
	data := newTestNativeHistogramData()
	h := newNativeHistogram(data, nil)
	r := newStatsTestReporter()

	h.report("nh", nil, r)

	assert.Nil(t, r.nativeHistogramPayload)
	assert.Equal(t, uint64(0), r.nativeHistogramSamples)
	assert.Equal(t, 0, data.marshals, "should not marshal an empty accumulator")
}

// Each report covers only the interval since the previous one.
func TestNativeHistogramReportIsDelta(t *testing.T) {
	data := newTestNativeHistogramData()
	h := newNativeHistogram(data, nil)
	r := newStatsTestReporter()

	h.RecordValue(1)
	h.report("nh", nil, r)
	assert.Equal(t, []byte("[1]"), r.nativeHistogramPayload)
	assert.Equal(t, uint64(1), r.nativeHistogramSamples)

	h.RecordValue(2)
	h.report("nh", nil, r)
	assert.Equal(t, []byte("[2]"), r.nativeHistogramPayload)
	assert.Equal(t, uint64(1), r.nativeHistogramSamples)

	assert.Equal(t, 2, data.clears)
}

// A failed marshal must keep the samples for the next cycle rather than
// dropping them on the floor.
func TestNativeHistogramReportRetainsSamplesOnMarshalError(t *testing.T) {
	data := newTestNativeHistogramData()
	data.marshalErr = errors.New("boom")
	h := newNativeHistogram(data, nil)
	r := newStatsTestReporter()

	h.RecordValue(1)
	h.report("nh", nil, r)

	assert.Nil(t, r.nativeHistogramPayload, "nothing should be reported")
	assert.Equal(t, 0, data.clears, "accumulator must not be cleared")
	assert.Equal(t, uint64(1), h.snapshot())

	// Once marshalling recovers, the retained sample is reported alongside
	// the new one.
	data.marshalErr = nil
	h.RecordValue(2)
	h.report("nh", nil, r)

	assert.Equal(t, []byte("[1 2]"), r.nativeHistogramPayload)
	assert.Equal(t, uint64(2), r.nativeHistogramSamples)
	assert.Equal(t, uint64(0), h.snapshot())
}

func TestNativeHistogramCachedReport(t *testing.T) {
	data := newTestNativeHistogramData()
	cached := &testCachedNativeHistogram{}
	h := newNativeHistogram(data, cached)

	h.RecordValue(1)
	h.RecordValue(2)
	h.cachedReport()

	require.Equal(t, 1, len(cached.payloads))
	assert.Equal(t, []byte("[1 2]"), cached.payloads[0])
	assert.Equal(t, uint64(2), cached.samples[0])

	// No samples, no report.
	h.cachedReport()
	assert.Equal(t, 1, len(cached.payloads))
}

func TestNativeHistogramSnapshot(t *testing.T) {
	data := newTestNativeHistogramData()
	h := newNativeHistogram(data, nil)

	assert.Equal(t, uint64(0), h.snapshot())

	h.RecordValue(1)
	h.RecordValue(2)
	h.RecordValue(3)
	assert.Equal(t, uint64(3), h.snapshot())

	// Snapshot does not drain.
	assert.Equal(t, uint64(3), h.snapshot())
}

func TestDefaultNativeHistogramData(t *testing.T) {
	data := defaultNativeHistogramFactory(DefaultNativeHistogramMaxBuckets)

	assert.Equal(t, uint64(0), data.Count())

	data.Update(1)
	data.Update(2)
	assert.Equal(t, uint64(2), data.Count())

	payload, err := data.MarshalBinary()
	assert.Nil(t, payload)
	assert.ErrorIs(t, err, ErrNativeHistogramBackendNotConfigured)

	data.Clear()
	assert.Equal(t, uint64(0), data.Count())
}

// Without a NativeHistogramFactory a scope's native histograms must accumulate
// harmlessly and never report, rather than panicking or emitting nonsense.
func TestDefaultNativeHistogramDataIsInert(t *testing.T) {
	h := newNativeHistogram(
		defaultNativeHistogramFactory(DefaultNativeHistogramMaxBuckets),
		nil,
	)
	r := newStatsTestReporter()

	h.RecordValue(1)
	h.RecordDuration(time.Second)
	h.report("nh", nil, r)

	assert.Nil(t, r.nativeHistogramPayload)
	assert.Equal(t, uint64(2), h.snapshot(), "values are still counted")
}

func TestDefaultNativeHistogramMaxBucketsValue(t *testing.T) {
	assert.Equal(t, 160, DefaultNativeHistogramMaxBuckets)
}

// testNativeHistogramFactory records the bucket budget it is handed for each
// native histogram a scope creates, and hands back accumulators the test can
// inspect.
type testNativeHistogramFactory struct {
	maxBuckets []int
	data       []*testNativeHistogramData
}

func (f *testNativeHistogramFactory) New(maxBuckets int) NativeHistogramData {
	d := newTestNativeHistogramData()
	f.maxBuckets = append(f.maxBuckets, maxBuckets)
	f.data = append(f.data, d)
	return d
}

// getNativeHistograms mirrors testStatsReporter's other accessors, keying by
// bare metric name.
func (r *testStatsReporter) getNativeHistograms() map[string]*testNativeHistogramValue {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	dst := make(map[string]*testNativeHistogramValue, len(r.nativeHistograms))
	for k, v := range r.nativeHistograms {
		var (
			parts = strings.Split(k, "+")
			name  string
		)
		if len(parts) > 0 {
			name = parts[0]
		}

		dst[name] = v
	}

	return dst
}

func TestScopeNativeHistogram(t *testing.T) {
	r := newTestStatsReporter()
	f := &testNativeHistogramFactory{}

	root, closer := NewRootScope(ScopeOptions{
		Reporter:               r,
		NativeHistogramFactory: f.New,
		OmitCardinalityMetrics: true,
	}, 0)
	defer closer.Close()

	s := root.(*scope)

	r.nhg.Add(1)
	h := s.NativeHistogram("latency", 0)
	h.RecordValue(1)
	h.RecordDuration(2 * time.Second)

	s.report(r)
	r.WaitAll()

	nh := r.getNativeHistograms()["latency"]
	require.NotNil(t, nh)
	assert.Equal(t, []byte("[1 2]"), nh.payload)
	assert.Equal(t, uint64(2), nh.samples)
}

func TestScopeNativeHistogramCachedReporter(t *testing.T) {
	r := newTestStatsReporter()
	f := &testNativeHistogramFactory{}

	root, closer := NewRootScope(ScopeOptions{
		CachedReporter:         r,
		NativeHistogramFactory: f.New,
		OmitCardinalityMetrics: true,
	}, 0)
	defer closer.Close()

	s := root.(*scope)

	r.nhg.Add(1)
	s.NativeHistogram("latency", 0).RecordValue(42)

	s.cachedReport()
	r.WaitAll()

	nh := r.getNativeHistograms()["latency"]
	require.NotNil(t, nh)
	assert.Equal(t, []byte("[42]"), nh.payload)
	assert.Equal(t, uint64(1), nh.samples)
}

func TestScopeNativeHistogramMaxBuckets(t *testing.T) {
	tests := []struct {
		name       string
		scopeMax   int
		callMax    int
		expectedTo int
	}{
		{
			name:       "zero uses the scope default",
			callMax:    0,
			expectedTo: DefaultNativeHistogramMaxBuckets,
		},
		{
			name:       "explicit budget is passed through",
			callMax:    32,
			expectedTo: 32,
		},
		{
			name:       "zero uses a configured scope default",
			scopeMax:   64,
			callMax:    0,
			expectedTo: 64,
		},
		{
			name:       "explicit budget overrides the scope default",
			scopeMax:   64,
			callMax:    32,
			expectedTo: 32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &testNativeHistogramFactory{}

			root, closer := NewRootScope(ScopeOptions{
				Reporter:                         NullStatsReporter,
				NativeHistogramFactory:           f.New,
				DefaultNativeHistogramMaxBuckets: tt.scopeMax,
				OmitCardinalityMetrics:           true,
			}, 0)
			defer closer.Close()

			root.NativeHistogram("latency", tt.callMax)

			require.Equal(t, 1, len(f.maxBuckets))
			assert.Equal(t, tt.expectedTo, f.maxBuckets[0])
		})
	}
}

// A scope with no NativeHistogramFactory must stay usable: values are counted
// but nothing is ever reported.
func TestScopeNativeHistogramWithoutFactory(t *testing.T) {
	r := newTestStatsReporter()

	root, closer := NewRootScope(ScopeOptions{
		Reporter:               r,
		OmitCardinalityMetrics: true,
	}, 0)
	defer closer.Close()

	s := root.(*scope)

	h := s.NativeHistogram("latency", 0)
	h.RecordValue(1)
	h.RecordDuration(time.Second)

	s.report(r)
	r.WaitAll()

	assert.Empty(t, r.getNativeHistograms())
	assert.Equal(t, uint64(2), h.(*nativeHistogram).snapshot())
}

func TestScopeNativeHistogramSubscopesInheritFactory(t *testing.T) {
	r := newTestStatsReporter()
	f := &testNativeHistogramFactory{}

	root, closer := NewRootScope(ScopeOptions{
		Reporter:                         r,
		NativeHistogramFactory:           f.New,
		DefaultNativeHistogramMaxBuckets: 64,
		OmitCardinalityMetrics:           true,
	}, 0)
	defer closer.Close()

	sub := root.SubScope("requests").Tagged(map[string]string{"env": "test"})

	r.nhg.Add(1)
	sub.NativeHistogram("latency", 0).RecordValue(7)

	root.(*scope).registry.Report(r)
	r.WaitAll()

	require.Equal(t, 1, len(f.maxBuckets))
	assert.Equal(t, 64, f.maxBuckets[0], "subscope inherits the scope default")

	nh := r.getNativeHistograms()["requests.latency"]
	require.NotNil(t, nh)
	assert.Equal(t, []byte("[7]"), nh.payload)
	assert.Equal(t, uint64(1), nh.samples)
	assert.Equal(t, map[string]string{"env": "test"}, nh.tags)
}

func TestScopeNativeHistogramReturnsSameInstance(t *testing.T) {
	f := &testNativeHistogramFactory{}

	root, closer := NewRootScope(ScopeOptions{
		Reporter:               NullStatsReporter,
		NativeHistogramFactory: f.New,
		OmitCardinalityMetrics: true,
	}, 0)
	defer closer.Close()

	first := root.NativeHistogram("latency", 0)
	second := root.NativeHistogram("latency", 0)

	assert.Equal(t, first, second)
	assert.Equal(t, 1, len(f.maxBuckets), "the accumulator is only built once")
}

func TestScopeNativeHistogramSnapshot(t *testing.T) {
	s := NewTestScope("prefix", map[string]string{"env": "test"})

	s.NativeHistogram("latency", 0).RecordValue(1)
	s.NativeHistogram("latency", 0).RecordDuration(time.Second)
	s.NativeHistogram("other", 0).RecordValue(3)

	snap := s.Snapshot().NativeHistograms()
	require.Equal(t, 2, len(snap))

	latency := snap[KeyForPrefixedStringMap("prefix.latency", map[string]string{"env": "test"})]
	require.NotNil(t, latency)
	assert.Equal(t, "prefix.latency", latency.Name())
	assert.Equal(t, map[string]string{"env": "test"}, latency.Tags())
	assert.Equal(t, uint64(2), latency.Samples())

	other := snap[KeyForPrefixedStringMap("prefix.other", map[string]string{"env": "test"})]
	require.NotNil(t, other)
	assert.Equal(t, uint64(1), other.Samples())
}
