// Copyright (c) 2025 Uber Technologies, Inc.
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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestBackwardCompatibility ensures all existing functionality still works
func TestBackwardCompatibility(t *testing.T) {
	scope := NewTestScope("compat", map[string]string{"service": "test"})

	// Test basic counter functionality
	t.Run("Counter", func(t *testing.T) {
		counter := scope.Counter("test_counter")

		// Test increment by 1
		counter.Inc(1)
		snapshot := scope.Snapshot()
		counters := snapshot.Counters()
		found := false
		for _, c := range counters {
			if containsSubstring(c.Name(), "test_counter") {
				assert.Equal(t, int64(1), c.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find test_counter")

		// Test increment by larger amount
		counter.Inc(42)
		snapshot = scope.Snapshot()
		counters = snapshot.Counters()
		found = false
		for _, c := range counters {
			if containsSubstring(c.Name(), "test_counter") {
				assert.Equal(t, int64(43), c.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find test_counter")

		// Test multiple calls accumulate
		counter.Inc(7)
		snapshot = scope.Snapshot()
		counters = snapshot.Counters()
		found = false
		for _, c := range counters {
			if containsSubstring(c.Name(), "test_counter") {
				assert.Equal(t, int64(50), c.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find test_counter")
	})

	// Test basic gauge functionality
	t.Run("Gauge", func(t *testing.T) {
		gauge := scope.Gauge("test_gauge")

		// Test setting value
		gauge.Update(3.14)
		snapshot := scope.Snapshot()
		gauges := snapshot.Gauges()
		found := false
		for _, g := range gauges {
			if containsSubstring(g.Name(), "test_gauge") {
				assert.Equal(t, 3.14, g.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find test_gauge")

		// Test updating value
		gauge.Update(2.71)
		snapshot = scope.Snapshot()
		gauges = snapshot.Gauges()
		found = false
		for _, g := range gauges {
			if containsSubstring(g.Name(), "test_gauge") {
				assert.Equal(t, 2.71, g.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find test_gauge")

		// Test negative values
		gauge.Update(-1.0)
		snapshot = scope.Snapshot()
		gauges = snapshot.Gauges()
		found = false
		for _, g := range gauges {
			if containsSubstring(g.Name(), "test_gauge") {
				assert.Equal(t, -1.0, g.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find test_gauge")
	})

	// Test basic timer functionality
	t.Run("Timer", func(t *testing.T) {
		timer := scope.Timer("test_timer")

		// Test recording duration
		timer.Record(100 * time.Millisecond)
		timer.Record(200 * time.Millisecond)
		timer.Record(50 * time.Millisecond)

		// Verify timer recorded values
		snapshot := scope.Snapshot()
		timers := snapshot.Timers()

		found := false
		for _, timer := range timers {
			if containsSubstring(timer.Name(), "test_timer") {
				found = true
				values := timer.Values()
				assert.Len(t, values, 3)
				// Values should be in nanoseconds
				assert.Contains(t, values, 100*time.Millisecond)
				assert.Contains(t, values, 200*time.Millisecond)
				assert.Contains(t, values, 50*time.Millisecond)
			}
		}
		assert.True(t, found, "Timer should be found in snapshot")
	})

	// Test basic histogram functionality
	t.Run("Histogram", func(t *testing.T) {
		buckets := ValueBuckets{0, 10, 50, 100, 500, 1000}
		histogram := scope.Histogram("test_histogram", buckets)

		// Record some values
		histogram.RecordValue(5)    // bucket 0-10
		histogram.RecordValue(25)   // bucket 10-50
		histogram.RecordValue(75)   // bucket 50-100
		histogram.RecordValue(150)  // bucket 100-500
		histogram.RecordValue(2000) // bucket 500+

		// Verify histogram values
		snapshot := scope.Snapshot()
		histograms := snapshot.Histograms()

		found := false
		for _, h := range histograms {
			if containsSubstring(h.Name(), "test_histogram") {
				found = true
				// Should have recorded values in the histogram buckets
				values := h.Values()
				assert.NotEmpty(t, values, "Histogram should have recorded values")

				// Sum up total samples recorded
				totalSamples := int64(0)
				for _, count := range values {
					totalSamples += count
				}
				assert.Equal(t, int64(5), totalSamples, "Should have recorded 5 total samples")
			}
		}
		assert.True(t, found, "Histogram should be found in snapshot")
	})
}

// TestScopeHierarchy ensures scope hierarchy works correctly
func TestScopeHierarchy(t *testing.T) {
	rootScope := NewTestScope("root", map[string]string{"app": "test"})

	// Test SubScope functionality
	t.Run("SubScope", func(t *testing.T) {
		level1 := rootScope.SubScope("level1")
		level2 := level1.SubScope("level2")

		// Each level should work independently
		rootScope.Counter("root_counter").Inc(1)
		level1.Counter("level1_counter").Inc(2)
		level2.Counter("level2_counter").Inc(3)

		snapshot := rootScope.Snapshot()
		counters := snapshot.Counters()

		// Should find all counters with correct names
		counterMap := make(map[string]int64)
		for _, c := range counters {
			counterMap[c.Name()] = c.Value()
		}

		// Root scope metric - should be prefixed with scope name
		assert.Contains(t, counterMap, "root.root_counter")
		assert.Equal(t, int64(1), counterMap["root.root_counter"])

		// Level 1 metric should include hierarchy
		found := false
		for name, value := range counterMap {
			if value == 2 && containsSubstring(name, "level1") {
				found = true
				break
			}
		}
		assert.True(t, found, "Should find level1 counter")

		// Level 2 metric should include full hierarchy
		found = false
		for name, value := range counterMap {
			if value == 3 && containsSubstring(name, "level2") {
				found = true
				break
			}
		}
		assert.True(t, found, "Should find level2 counter")
	})

	// Test Tagged functionality
	t.Run("Tagged", func(t *testing.T) {
		tagged1 := rootScope.Tagged(map[string]string{"env": "prod"})
		tagged2 := rootScope.Tagged(map[string]string{"env": "dev", "version": "1.0"})

		tagged1.Counter("requests").Inc(10)
		tagged2.Counter("requests").Inc(20)

		snapshot := rootScope.Snapshot()
		counters := snapshot.Counters()

		// Should have two separate request counters
		requestCounters := 0
		for _, c := range counters {
			if containsSubstring(c.Name(), "requests") {
				requestCounters++
				// Value should be either 10 or 20
				assert.True(t, c.Value() == 10 || c.Value() == 20)
			}
		}
		assert.Equal(t, 2, requestCounters, "Should have two separate request counters")
	})
}

// TestMetricNaming ensures metric naming works correctly
func TestMetricNaming(t *testing.T) {
	scope := NewTestScope("naming", nil)

	testCases := []struct {
		name     string
		input    string
		expected bool // whether it should work without error
	}{
		{"Normal", "normal_metric_name", true},
		{"WithDashes", "metric-with-dashes", true},
		{"WithDots", "metric.with.dots", true},
		{"WithNumbers", "metric123", true},
		{"WithSpecialChars", "metric!@#$%^&*()", true},
		{"Empty", "", true},
		{"OnlySpecialChars", "!@#$%", true},
		{"Unicode", "métric_ünïcode", true},
		{"VeryLong", fmt.Sprintf("very_long_%s", repeatString("name_", 100)), true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.expected {
				// Should not panic
				assert.NotPanics(t, func() {
					counter := scope.Counter(tc.input)
					counter.Inc(1)

					gauge := scope.Gauge(tc.input + "_gauge")
					gauge.Update(1.0)

					timer := scope.Timer(tc.input + "_timer")
					timer.Record(time.Millisecond)

					histogram := scope.Histogram(tc.input+"_hist", ValueBuckets{0, 1, 2})
					histogram.RecordValue(1.0)
				})
			}
		})
	}
}

// TestTagHandling ensures tag handling works correctly
func TestTagHandling(t *testing.T) {
	scope := NewTestScope("tags", nil)

	t.Run("TagOrder", func(t *testing.T) {
		// Same tags in different order should produce same scope
		tags1 := map[string]string{"b": "2", "a": "1", "c": "3"}
		tags2 := map[string]string{"a": "1", "c": "3", "b": "2"}

		scope1 := scope.Tagged(tags1)
		scope2 := scope.Tagged(tags2)

		// Should be equivalent scopes
		scope1.Counter("test").Inc(5)
		scope2.Counter("test").Inc(3)

		// Final value should be 8 (same counter)
		snapshot := scope.Snapshot()
		counters := snapshot.Counters()

		found := false
		for _, c := range counters {
			if containsSubstring(c.Name(), "test") {
				// Both increments should have gone to the same counter
				assert.Equal(t, int64(8), c.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find the test counter")
	})

	t.Run("EmptyTags", func(t *testing.T) {
		// Different empty tag scenarios should work
		scope1 := scope.Tagged(nil)
		scope2 := scope.Tagged(map[string]string{})

		// Should work without panics
		assert.NotPanics(t, func() {
			scope1.Counter("nil_tags").Inc(1)
			scope2.Counter("empty_tags").Inc(1)
		})
	})

	t.Run("SpecialTagValues", func(t *testing.T) {
		tags := map[string]string{
			"empty":   "",
			"spaces":  "value with spaces",
			"special": "value!@#$%^&*()",
			"unicode": "valüe_ünïcode",
			"long":    repeatString("long_value_", 50),
		}

		assert.NotPanics(t, func() {
			tagged := scope.Tagged(tags)
			tagged.Counter("special_values").Inc(1)
		})
	})
}

// TestValuePrecision ensures numeric precision is maintained
func TestValuePrecision(t *testing.T) {
	scope := NewTestScope("precision", nil)

	t.Run("LargeCounters", func(t *testing.T) {
		counter := scope.Counter("large_counter")

		// Test large values
		largeValue := int64(math.MaxInt64 - 1000)
		counter.Inc(largeValue)

		snapshot := scope.Snapshot()
		counters := snapshot.Counters()
		found := false
		for _, c := range counters {
			if containsSubstring(c.Name(), "large_counter") {
				assert.Equal(t, largeValue, c.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find large_counter")

		// Test increment doesn't overflow
		counter.Inc(500)
		snapshot = scope.Snapshot()
		counters = snapshot.Counters()
		found = false
		for _, c := range counters {
			if containsSubstring(c.Name(), "large_counter") {
				assert.Equal(t, largeValue+500, c.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find large_counter after increment")
	})

	t.Run("PreciseGauges", func(t *testing.T) {
		gauge := scope.Gauge("precise_gauge")

		// Test very small values
		gauge.Update(1e-10)
		snapshot := scope.Snapshot()
		gauges := snapshot.Gauges()
		found := false
		for _, g := range gauges {
			if containsSubstring(g.Name(), "precise_gauge") {
				assert.Equal(t, 1e-10, g.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find precise_gauge with small value")

		// Test very large values
		gauge.Update(1e10)
		snapshot = scope.Snapshot()
		gauges = snapshot.Gauges()
		found = false
		for _, g := range gauges {
			if containsSubstring(g.Name(), "precise_gauge") {
				assert.Equal(t, 1e10, g.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find precise_gauge with large value")

		// Test precise decimal values
		gauge.Update(math.Pi)
		snapshot = scope.Snapshot()
		gauges = snapshot.Gauges()
		found = false
		for _, g := range gauges {
			if containsSubstring(g.Name(), "precise_gauge") {
				assert.Equal(t, math.Pi, g.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find precise_gauge with Pi value")
	})
}

// TestConcurrencySafety ensures thread safety is maintained
func TestConcurrencySafety(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrency test in short mode")
	}

	scope := NewTestScope("concurrency", nil)

	// Test concurrent access to same counter
	t.Run("SharedCounter", func(t *testing.T) {
		counter := scope.Counter("shared_counter")

		const numGoroutines = 100
		const incrementsPerGoroutine = 1000

		done := make(chan bool, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func() {
				for j := 0; j < incrementsPerGoroutine; j++ {
					counter.Inc(1)
				}
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < numGoroutines; i++ {
			<-done
		}

		// Should have exact count
		expected := int64(numGoroutines * incrementsPerGoroutine)
		snapshot := scope.Snapshot()
		counters := snapshot.Counters()
		found := false
		for _, c := range counters {
			if containsSubstring(c.Name(), "shared_counter") {
				assert.Equal(t, expected, c.Value())
				found = true
				break
			}
		}
		assert.True(t, found, "Should find shared_counter")
	})
}

// TestSnapshotConsistency ensures snapshots remain consistent
func TestSnapshotConsistency(t *testing.T) {
	scope := NewTestScope("snapshot", nil)

	// Create some metrics
	scope.Counter("counter1").Inc(10)
	scope.Counter("counter2").Inc(20)
	scope.Gauge("gauge1").Update(3.14)
	scope.Gauge("gauge2").Update(2.71)
	scope.Timer("timer1").Record(100 * time.Millisecond)

	snapshot1 := scope.Snapshot()

	// Modify metrics
	scope.Counter("counter1").Inc(5)                     // now 15
	scope.Gauge("gauge1").Update(1.41)                   // now 1.41
	scope.Timer("timer1").Record(200 * time.Millisecond) // add another value

	snapshot2 := scope.Snapshot()

	// First snapshot should be unchanged
	counters1 := snapshot1.Counters()
	counter1Map := make(map[string]int64)
	for _, c := range counters1 {
		counter1Map[c.Name()] = c.Value()
	}

	gauges1 := snapshot1.Gauges()
	gauge1Map := make(map[string]float64)
	for _, g := range gauges1 {
		gauge1Map[g.Name()] = g.Value()
	}

	// Second snapshot should have new values
	counters2 := snapshot2.Counters()
	counter2Map := make(map[string]int64)
	for _, c := range counters2 {
		counter2Map[c.Name()] = c.Value()
	}

	gauges2 := snapshot2.Gauges()
	gauge2Map := make(map[string]float64)
	for _, g := range gauges2 {
		gauge2Map[g.Name()] = g.Value()
	}

	// Verify snapshots are different
	assert.NotEqual(t, counter1Map, counter2Map, "Counter snapshots should differ")
	assert.NotEqual(t, gauge1Map, gauge2Map, "Gauge snapshots should differ")
}

// Helper functions
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func repeatString(s string, count int) string {
	result := make([]string, count)
	for i := range result {
		result[i] = s
	}
	return fmt.Sprintf("%s", result)
}

// BenchmarkRegressionBaseline provides baseline performance measurements
func BenchmarkRegressionBaseline(b *testing.B) {
	scope := NewTestScope("baseline", nil)

	b.Run("CounterBaseline", func(b *testing.B) {
		counter := scope.Counter("baseline_counter")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			counter.Inc(1)
		}
	})

	b.Run("GaugeBaseline", func(b *testing.B) {
		gauge := scope.Gauge("baseline_gauge")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			gauge.Update(float64(i))
		}
	})

	b.Run("TimerBaseline", func(b *testing.B) {
		timer := scope.Timer("baseline_timer")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			timer.Record(time.Microsecond)
		}
	})

	b.Run("ScopeCreationBaseline", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tagged := scope.Tagged(map[string]string{
				"iteration": fmt.Sprintf("%d", i%1000),
			})
			tagged.Counter("test").Inc(1)
		}
	})

	b.Run("SnapshotBaseline", func(b *testing.B) {
		// Create some metrics first
		for i := 0; i < 100; i++ {
			scope.Counter(fmt.Sprintf("counter_%d", i)).Inc(int64(i))
			scope.Gauge(fmt.Sprintf("gauge_%d", i)).Update(float64(i))
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = scope.Snapshot()
		}
	})
}
