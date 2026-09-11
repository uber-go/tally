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

// The two variants are views over one accumulator, exactly as the scope
// builds them.
func newTestValueHistogram(
	data NativeHistogramData,
	cached CachedNativeHistogram,
) nativeValueHistogram {
	return nativeValueHistogram{newNativeHistogram(data, cached)}
}

func newTestDurationHistogram(
	data NativeHistogramData,
	cached CachedNativeHistogram,
) nativeDurationHistogram {
	return nativeDurationHistogram{newNativeHistogram(data, cached)}
}

func TestNativeHistogramRecordValue(t *testing.T) {
	data := newTestNativeHistogramData()
	h := newTestValueHistogram(data, nil)

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
			h := newTestDurationHistogram(data, nil)

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
	h := newTestDurationHistogram(data, nil)
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
	h := newTestDurationHistogram(data, nil)

	sw := h.Start()
	now = now.Add(1500 * time.Millisecond)
	sw.Stop()

	require.Equal(t, 1, len(data.values))
	assert.Equal(t, 1.5, data.values[0])
}

func TestNativeHistogramReport(t *testing.T) {
	data := newTestNativeHistogramData()
	h := newTestValueHistogram(data, nil)
	r := newStatsTestReporter()

	h.RecordValue(1)
	h.RecordValue(2)
	h.report("nh", nil, r)

	assert.Equal(t, []byte("[1 2]"), r.nativeHistogramPayload)
	assert.Equal(t, uint64(2), r.nativeHistogramSamples)
}

func TestNativeHistogramReportNothingWithoutSamples(t *testing.T) {
	data := newTestNativeHistogramData()
	h := newTestValueHistogram(data, nil)
	r := newStatsTestReporter()

	h.report("nh", nil, r)

	assert.Nil(t, r.nativeHistogramPayload)
	assert.Equal(t, uint64(0), r.nativeHistogramSamples)
	assert.Equal(t, 0, data.marshals, "should not marshal an empty accumulator")
}

// Each report covers only the interval since the previous one.
func TestNativeHistogramReportIsDelta(t *testing.T) {
	data := newTestNativeHistogramData()
	h := newTestValueHistogram(data, nil)
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

// A failed marshal costs that interval's samples. Marshalling is
// deterministic, so keeping them would fail identically next cycle and leave
// the histogram reporting nothing for good.
func TestNativeHistogramReportDiscardsSamplesOnMarshalError(t *testing.T) {
	data := newTestNativeHistogramData()
	data.marshalErr = errors.New("boom")
	h := newTestValueHistogram(data, nil)
	r := newStatsTestReporter()

	h.RecordValue(1)
	h.report("nh", nil, r)

	assert.Nil(t, r.nativeHistogramPayload, "nothing should be reported")
	assert.Equal(t, 1, data.clears, "accumulator must be cleared anyway")
	assert.Equal(t, uint64(0), h.snapshot())

	// The next interval reports on its own, carrying none of the lost samples.
	data.marshalErr = nil
	h.RecordValue(2)
	h.report("nh", nil, r)

	assert.Equal(t, []byte("[2]"), r.nativeHistogramPayload)
	assert.Equal(t, uint64(1), r.nativeHistogramSamples)
	assert.Equal(t, uint64(0), h.snapshot())
}

// The reason the type is split in two. A histogram that accepts both APIs lets
// two call sites feed one distribution in different units -- 250 and 0.25 for
// the same quarter second -- with nothing to catch it.
func TestNativeHistogramVariantsWithholdEachOthersAPIs(t *testing.T) {
	acc := newNativeHistogram(newTestNativeHistogramData(), nil)

	var value interface{} = nativeValueHistogram{acc}
	var duration interface{} = nativeDurationHistogram{acc}

	_, ok := value.(interface{ RecordDuration(time.Duration) })
	assert.False(t, ok, "a value histogram must not accept durations")

	_, ok = value.(interface{ Start() Stopwatch })
	assert.False(t, ok, "a value histogram must not hand out a stopwatch")

	_, ok = duration.(interface{ RecordValue(float64) })
	assert.False(t, ok, "a duration histogram must not accept raw values")
}

func TestNativeHistogramCachedReport(t *testing.T) {
	data := newTestNativeHistogramData()
	cached := &testCachedNativeHistogram{}
	h := newTestValueHistogram(data, cached)

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
	h := newTestValueHistogram(data, nil)

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
	acc := newNativeHistogram(
		defaultNativeHistogramFactory(DefaultNativeHistogramMaxBuckets),
		nil,
	)
	r := newStatsTestReporter()

	nativeValueHistogram{acc}.RecordValue(1)
	nativeDurationHistogram{acc}.RecordDuration(time.Second)
	assert.Equal(t, uint64(2), acc.snapshot(), "values are still counted")

	acc.report("nh", nil, r)

	assert.Nil(t, r.nativeHistogramPayload, "but nothing is reported")
	assert.Equal(t, uint64(0), acc.snapshot(), "and the failed marshal clears")
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

	r.nhg.Add(2)
	s.NativeValueHistogram("payload_bytes").RecordValue(1)
	s.NativeDurationHistogram("latency").RecordDuration(2 * time.Second)

	s.report(r)
	r.WaitAll()

	bytes := r.getNativeHistograms()["payload_bytes"]
	require.NotNil(t, bytes)
	assert.Equal(t, []byte("[1]"), bytes.payload)
	assert.Equal(t, uint64(1), bytes.samples)

	latency := r.getNativeHistograms()["latency"]
	require.NotNil(t, latency)
	assert.Equal(t, []byte("[2]"), latency.payload, "durations report as seconds")
	assert.Equal(t, uint64(1), latency.samples)
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
	s.NativeValueHistogram("latency").RecordValue(42)

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
		expectedTo int
	}{
		{
			name:       "an unset scope default falls back to the package default",
			expectedTo: DefaultNativeHistogramMaxBuckets,
		},
		{
			name:       "a configured scope default is honoured",
			scopeMax:   64,
			expectedTo: 64,
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

			root.NativeValueHistogram("payload_bytes")
			root.NativeDurationHistogram("latency")

			require.Equal(t, 2, len(f.maxBuckets))
			assert.Equal(t, tt.expectedTo, f.maxBuckets[0])
			assert.Equal(t, tt.expectedTo, f.maxBuckets[1],
				"both variants honour the same budget")
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

	values := s.NativeValueHistogram("payload_bytes").(nativeValueHistogram)
	durations := s.NativeDurationHistogram("latency").(nativeDurationHistogram)
	values.RecordValue(1)
	durations.RecordDuration(time.Second)

	require.Equal(t, uint64(1), values.snapshot(), "values are counted")
	require.Equal(t, uint64(1), durations.snapshot())

	s.report(r)
	r.WaitAll()

	assert.Empty(t, r.getNativeHistograms(), "but nothing is reported")
	assert.Equal(t, uint64(0), values.snapshot(), "and the failed marshal clears")
	assert.Equal(t, uint64(0), durations.snapshot())
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
	sub.NativeValueHistogram("latency").RecordValue(7)

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

	first := root.NativeValueHistogram("latency")
	second := root.NativeValueHistogram("latency")

	assert.Equal(t, first, second)
	assert.Equal(t, 1, len(f.maxBuckets), "the accumulator is only built once")
}

// Values and durations are separate metrics, so one name in both yields two
// accumulators -- and two payloads reported under that name, the same way
// Counter("x") and Gauge("x") already both report as x.
func TestScopeNativeHistogramVariantsAreSeparateMetrics(t *testing.T) {
	f := &testNativeHistogramFactory{}

	root, closer := NewRootScope(ScopeOptions{
		Reporter:               NullStatsReporter,
		NativeHistogramFactory: f.New,
		OmitCardinalityMetrics: true,
	}, 0)
	defer closer.Close()

	root.NativeValueHistogram("latency").RecordValue(1)
	root.NativeDurationHistogram("latency").RecordDuration(time.Second)

	require.Equal(t, 2, len(f.maxBuckets), "one accumulator per variant")
	require.Equal(t, 2, len(f.data))
	assert.Equal(t, []float64{1}, f.data[0].values)
	assert.Equal(t, []float64{1}, f.data[1].values,
		"a second of duration, not the raw value")
}

func TestScopeNativeHistogramSnapshot(t *testing.T) {
	s := NewTestScope("prefix", map[string]string{"env": "test"})

	s.NativeDurationHistogram("latency").RecordDuration(time.Second)
	s.NativeDurationHistogram("latency").RecordDuration(2 * time.Second)
	s.NativeValueHistogram("other").RecordValue(3)

	snap := s.Snapshot().NativeHistograms()
	require.Equal(t, 2, len(snap), "both variants land in the one map")

	latency := snap[KeyForPrefixedStringMap("prefix.latency", map[string]string{"env": "test"})]
	require.NotNil(t, latency)
	assert.Equal(t, "prefix.latency", latency.Name())
	assert.Equal(t, map[string]string{"env": "test"}, latency.Tags())
	assert.Equal(t, uint64(2), latency.Samples())

	other := snap[KeyForPrefixedStringMap("prefix.other", map[string]string{"env": "test"})]
	require.NotNil(t, other)
	assert.Equal(t, uint64(1), other.Samples())
}
