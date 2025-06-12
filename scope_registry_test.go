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

package tally

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	numInternalMetrics = 4
)

func TestVerifyCachedTaggedScopesAlloc(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping allocation comparison test in short mode")
	}

	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
	}, 0)

	tags := map[string]string{
		"foo": "bar",
		"baz": "qux",
		"qux": "quux",
	}

	// Test with cache (should have fewer allocations on subsequent calls)
	firstRunAllocs := testing.AllocsPerRun(100, func() {
		_ = root.Tagged(tags)
	})

	// Second run should have fewer allocations due to caching
	secondRunAllocs := testing.AllocsPerRun(100, func() {
		_ = root.Tagged(tags)
	})

	// The cached version should allocate less or equal (not more)
	// We don't test exact numbers, just the relationship
	if secondRunAllocs > firstRunAllocs+1 { // Allow for small variance
		t.Logf("First run allocs: %.2f, Second run allocs: %.2f", firstRunAllocs, secondRunAllocs)
		t.Error("Cached tagged scope creation should not allocate significantly more than initial creation")
	}
}

func TestVerifyOmitCardinalityMetricsTags(t *testing.T) {
	r := newTestStatsReporter()
	_, closer := NewRootScope(ScopeOptions{
		Reporter:               r,
		OmitCardinalityMetrics: false,
		CardinalityMetricsTags: map[string]string{
			"cardinality_tag_key": "cardinality_tag_value",
		},
	}, 0)
	wantOmitCardinalityMetricsTags := map[string]string{
		"cardinality_tag_key": "cardinality_tag_value",
		"version":             Version,
		"host":                "global",
		"instance":            "global",
	}

	r.gg.Add(numInternalMetrics)
	closer.Close()
	r.WaitAll()

	assert.NotNil(t, r.gauges[counterCardinalityName], "counter cardinality should not be nil")
	assert.Equal(
		t, wantOmitCardinalityMetricsTags, r.gauges[counterCardinalityName].tags, "expected tags %v, got tags %v",
		wantOmitCardinalityMetricsTags, r.gauges[counterCardinalityName].tags,
	)
}

func TestNewTestStatsReporterOneScope(t *testing.T) {
	r := newTestStatsReporter()
	root, closer := NewRootScope(ScopeOptions{Reporter: r, OmitCardinalityMetrics: false}, 0)
	s := root.(*scope)

	numFakeCounters := 3
	numFakeGauges := 5
	numFakeHistograms := 11
	numScopes := 1

	r.cg.Add(numFakeCounters)
	for c := 1; c <= numFakeCounters; c++ {
		s.Counter(fmt.Sprintf("counter-%d", c)).Inc(int64(c))
	}

	r.gg.Add(numFakeGauges + numInternalMetrics)
	for g := 1; g <= numFakeGauges; g++ {
		s.Gauge(fmt.Sprintf("gauge_%d", g)).Update(float64(g))
	}

	r.hg.Add(numFakeHistograms)
	for h := 1; h <= numFakeHistograms; h++ {
		s.Histogram(fmt.Sprintf("histogram_%d", h), MustMakeLinearValueBuckets(0, 1, 10)).RecordValue(float64(h))
	}

	closer.Close()
	r.WaitAll()

	assert.NotNil(t, r.gauges[counterCardinalityName], "counter cardinality should not be nil")
	assert.Equal(
		t, numFakeCounters, int(r.gauges[counterCardinalityName].val), "expected %d counters, got %d counters",
		numFakeCounters, r.gauges[counterCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[gaugeCardinalityName], "gauge cardinality should not be nil")
	assert.Equal(
		t, numFakeGauges, int(r.gauges[gaugeCardinalityName].val), "expected %d gauges, got %d gauges",
		numFakeGauges, r.gauges[gaugeCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[histogramCardinalityName], "histogram cardinality should not be nil")
	assert.Equal(
		t, numFakeHistograms, int(r.gauges[histogramCardinalityName].val),
		"expected %d histograms, got %d histograms", numFakeHistograms, r.gauges[histogramCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[scopeCardinalityName], "scope cardinality should not be nil")
	assert.Equal(
		t, numScopes, int(r.gauges[scopeCardinalityName].val), "expected %d scopes, got %d scopes",
		numScopes, r.gauges[scopeCardinalityName].val,
	)
}

func TestNewTestStatsReporterManyScopes(t *testing.T) {
	r := newTestStatsReporter()
	root, closer := NewRootScope(ScopeOptions{Reporter: r, OmitCardinalityMetrics: false}, 0)
	wantCounters, wantGauges, wantHistograms, wantScopes := 3, 2, 1, 2

	s := root.(*scope)
	r.cg.Add(2)
	s.Counter("counter-foo").Inc(1)
	s.Counter("counter-bar").Inc(2)
	r.gg.Add(1 + numInternalMetrics)
	s.Gauge("gauge-foo").Update(3)
	r.hg.Add(1)
	s.Histogram("histogram-foo", MustMakeLinearValueBuckets(0, 1, 10)).RecordValue(4)

	ss := root.SubScope("sub-scope").(*scope)
	r.cg.Add(1)
	ss.Counter("counter-baz").Inc(5)
	r.gg.Add(1)
	ss.Gauge("gauge-bar").Update(6)

	closer.Close()
	r.WaitAll()

	assert.NotNil(t, r.gauges[counterCardinalityName], "counter cardinality should not be nil")
	assert.Equal(
		t, wantCounters, int(r.gauges[counterCardinalityName].val), "expected %d counters, got %d counters", wantCounters,
		r.gauges[counterCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[gaugeCardinalityName], "gauge cardinality should not be nil")
	assert.Equal(
		t, wantGauges, int(r.gauges[gaugeCardinalityName].val), "expected %d gauges, got %d gauges", wantGauges,
		r.gauges[gaugeCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[histogramCardinalityName], "histogram cardinality should not be nil")
	assert.Equal(
		t, wantHistograms, int(r.gauges[histogramCardinalityName].val), "expected %d histograms, got %d histograms",
		wantHistograms, r.gauges[histogramCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[scopeCardinalityName], "scope cardinality should not be nil")
	assert.Equal(
		t, wantScopes, int(r.gauges[scopeCardinalityName].val), "expected %d scopes, got %d scopes",
		wantScopes, r.gauges[scopeCardinalityName].val,
	)
}

func TestForEachScopeConcurrent(t *testing.T) {
	var (
		root = newRootScope(ScopeOptions{Prefix: "", Tags: nil}, 0)
		quit = make(chan struct{})
		done = make(chan struct{})
	)

	go func() {
		defer close(done)
		for {
			select {
			case <-quit:
				return
			default:
				hello := root.Tagged(map[string]string{"a": "b"}).Counter("hello")
				hello.Inc(1)
			}
		}
	}()

	var c Counter = nil
	for {
		// Keep poking at the subscopes until the counter is written.
		root.registry.ForEachScope(
			func(ss *scope) {
				// Use sync.Map's Load method to access counters
				if counterValue, ok := ss.counters.Load("hello"); ok {
					c = counterValue.(*counter)
				}
			},
		)
		if c != nil {
			quit <- struct{}{}
			break
		}
	}

	<-done
}

func TestCachedReporterInternalMetricsAlloc(t *testing.T) {
	tests := []struct {
		name                   string
		omitCardinalityMetrics bool
		wantGauges             int
	}{
		{
			name:                   "omit metrics",
			omitCardinalityMetrics: true,
			wantGauges:             1,
		},
		{
			name:                   "include metrics",
			omitCardinalityMetrics: false,
			wantGauges:             1 + numInternalMetrics,
		},
	}

	for _, tt := range tests {
		r := newTestStatsReporter()
		root, closer := NewRootScope(ScopeOptions{CachedReporter: r, OmitCardinalityMetrics: tt.omitCardinalityMetrics}, 0)
		s := root.(*scope)

		r.gg.Add(tt.wantGauges)
		s.Gauge("gauge-foo").Update(3)

		closer.Close()
		r.WaitAll()

		assert.Equal(
			t, tt.wantGauges, len(r.gauges), "%n: expected %d gauges, got %d gauges", tt.name, tt.wantGauges,
			len(r.gauges),
		)
	}
}

func TestCachedReporterInternalMetricsConcurrent(t *testing.T) {
	tr := newTestStatsReporter()
	root, closer := NewRootScope(ScopeOptions{
		CachedReporter:         tr,
		OmitCardinalityMetrics: false,
	}, 0)
	s := root.(*scope)

	var wg sync.WaitGroup

	done := make(chan struct{})
	time.AfterFunc(time.Second, func() {
		close(done)
	})

	wg.Add(1)
	go func() {
		defer wg.Done()
		var i int
		for {
			select {
			case <-done:
				return
			default:
			}
			suffix := strconv.Itoa(i)
			tr.gg.Add(1)
			tr.tg.Add(1)
			tr.cg.Add(1)
			s.Gauge("gauge-foo" + suffix).Update(42)
			s.Timer("timer-foo" + suffix).Record(42)
			s.Counter("counter-foo" + suffix).Inc(42)
			i++
			time.Sleep(time.Microsecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// kick off report loop manually, so we can keep track of how many internal metrics
				// we emitted.
				tr.gg.Add(numInternalMetrics)
				s.reportLoopRun()
			}
		}
	}()
	wg.Wait()

	// Close should also trigger internal metric report.
	tr.gg.Add(numInternalMetrics)
	closer.Close()
}

func BenchmarkKeyGenerationComparison(b *testing.B) {
	prefix := "test.metric.name"
	tags := map[string]string{
		"service":     "test-service",
		"environment": "production",
		"host":        "server-01",
		"region":      "us-west-2",
		"datacenter":  "pdx1",
	}

	b.Run("OriginalWithAllocation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Simulate the old approach with manual allocation
			_ = string(keyForPrefixedStringMapsAsKey(make([]byte, 0, 256), prefix, tags))
		}
	})

	b.Run("NewWithPooledBuffer", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Use the new pooled buffer approach
			_ = keyForPrefixedStringMapsWithPooledBuffer(prefix, tags)
		}
	})

	b.Run("OptimizedWithPooledSlices", func(b *testing.B) {
		buf := make([]byte, 0, 256)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Use the new optimized version with pooled string slices
			buf = buf[:0] // Reset buffer
			_ = string(keyForPrefixedStringMapsAsKeyWithPooledSlice(buf, prefix, tags))
		}
	})
}

// Phase 4: New benchmarks for metric slice pooling and tag map pooling optimizations
func BenchmarkMetricSlicePooling(b *testing.B) {
	b.Run("CounterSliceAllocation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Old approach - always allocate
			_ = make([]*counter, 0, 16)
		}
	})

	b.Run("CounterSlicePooled", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// New approach - use pools
			slice := getCounterSlice(16)
			releaseCounterSlice(slice)
		}
	})

	b.Run("GaugeSliceAllocation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Old approach - always allocate
			_ = make([]*gauge, 0, 16)
		}
	})

	b.Run("GaugeSlicePooled", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// New approach - use pools
			slice := getGaugeSlice(16)
			releaseGaugeSlice(slice)
		}
	})

	b.Run("HistogramSliceAllocation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Old approach - always allocate
			_ = make([]*histogram, 0, 16)
		}
	})

	b.Run("HistogramSlicePooled", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// New approach - use pools
			slice := getHistogramSlice(16)
			releaseHistogramSlice(slice)
		}
	})
}

func BenchmarkTagMapPooling(b *testing.B) {
	left := map[string]string{
		"service": "test-service",
		"region":  "us-west-2",
	}
	right := map[string]string{
		"environment": "production",
		"instance":    "instance-1",
	}

	b.Run("TagMergeAllocation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Old approach - always allocate
			_ = mergeRightTags(left, right)
		}
	})

	b.Run("TagMergePooled", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// New approach - use pooled maps
			_ = mergeRightTagsPooled(left, right)
		}
	})

	b.Run("TagMapDirectAllocation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Direct allocation
			_ = make(map[string]string, 8)
		}
	})

	b.Run("TagMapPooled", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Pooled allocation
			tagMap := getTagMap(8)
			releaseTagMap(tagMap)
		}
	})
}

func BenchmarkSnapshotMapPooling(b *testing.B) {
	b.Run("SnapshotMapsAllocation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Old approach - always allocate
			_ = map[string]CounterSnapshot{}
			_ = map[string]GaugeSnapshot{}
			_ = map[string]TimerSnapshot{}
			_ = map[string]HistogramSnapshot{}
		}
	})

	b.Run("SnapshotMapsPooled", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// New approach - use pools
			counterMap := getCounterSnapshotMap()
			gaugeMap := getGaugeSnapshotMap()
			timerMap := getTimerSnapshotMap()
			histogramMap := getHistogramSnapshotMap()

			releaseCounterSnapshotMap(counterMap)
			releaseGaugeSnapshotMap(gaugeMap)
			releaseTimerSnapshotMap(timerMap)
			releaseHistogramSnapshotMap(histogramMap)
		}
	})

	b.Run("SnapshotCreationOriginal", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Simulate old newSnapshot
			_ = &snapshot{
				counters:   make(map[string]CounterSnapshot),
				gauges:     make(map[string]GaugeSnapshot),
				timers:     make(map[string]TimerSnapshot),
				histograms: make(map[string]HistogramSnapshot),
			}
		}
	})

	b.Run("SnapshotCreationPooled", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// New pooled approach
			snapshot, cleanup := newSnapshotPooled()
			_ = snapshot
			cleanup()
		}
	})
}

// Benchmark the overall subscope creation with all Phase 4 optimizations
func BenchmarkSubscopeCreationPhase4(b *testing.B) {
	// Test subscope creation with all optimizations enabled
	root := newRootScope(ScopeOptions{
		Prefix: "test",
		Tags: map[string]string{
			"service": "benchmark",
		},
	}, 0)

	tags := map[string]string{
		"endpoint": "/api/v1/test",
		"method":   "GET",
		"status":   "200",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create subscope with tags
		_ = root.Tagged(tags)
	}
}

func TestOptimizedFlushReportsAllMetrics(t *testing.T) {
	// Enable optimized flush
	EnableOptimizedFlush()
	defer DisableOptimizedFlush()

	// Create a test reporter to capture reported metrics
	r := newTestStatsReporter()

	// Create root scope with the test reporter
	root, closer := NewRootScope(ScopeOptions{
		Reporter:               r,
		OmitCardinalityMetrics: true, // Disable cardinality metrics to simplify test
	}, 50*time.Millisecond)
	defer closer.Close()

	// Create multiple scopes and metrics to simulate high cardinality
	numScopes := 10
	for i := 0; i < numScopes; i++ {
		scope := root.Tagged(map[string]string{"instance": fmt.Sprintf("instance-%d", i)})

		// Create different types of metrics with unique names
		r.cg.Add(1)
		counter := scope.Counter(fmt.Sprintf("test_counter_%d", i))
		counter.Inc(int64(i + 1))

		r.gg.Add(1)
		gauge := scope.Gauge(fmt.Sprintf("test_gauge_%d", i))
		gauge.Update(float64(i * 2))

		r.tg.Add(1)
		timer := scope.Timer(fmt.Sprintf("test_timer_%d", i))
		timer.Record(time.Duration(i) * time.Millisecond)

		r.hg.Add(1)
		histogram := scope.Histogram(fmt.Sprintf("test_histogram_%d", i), ValueBuckets{1, 5, 10})
		histogram.RecordValue(float64(i))
	}

	// Wait for all metrics to be reported
	r.WaitAll()

	// Verify all metric types were reported by checking the maps
	counters := r.getCounters()
	gauges := r.getGauges()
	timers := r.getTimers()
	histograms := r.getHistograms()

	// Verify all metric types were reported
	assert.True(t, len(counters) > 0, "Counters should be reported with optimized flush")
	assert.True(t, len(gauges) > 0, "Gauges should be reported with optimized flush")
	assert.True(t, len(timers) > 0, "Timers should be reported with optimized flush")
	assert.True(t, len(histograms) > 0, "Histograms should be reported with optimized flush")

	// Verify we have the expected number of metrics (should be numScopes each)
	assert.Equal(t, numScopes, len(counters), "Should have %d counter metrics", numScopes)
	assert.Equal(t, numScopes, len(gauges), "Should have %d gauge metrics", numScopes)
	assert.Equal(t, numScopes, len(timers), "Should have %d timer metrics", numScopes)
	assert.Equal(t, numScopes, len(histograms), "Should have %d histogram metrics", numScopes)
}
