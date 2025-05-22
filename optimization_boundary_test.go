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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestSanitizationPerformance tests that sanitization doesn't become a bottleneck
func TestSanitizationPerformance(t *testing.T) {
	testCases := []struct {
		name  string
		input string
	}{
		{"Clean", "clean_metric_name"},
		{"NeedsSanitization", "metric-with.special!chars@"},
		{"Unicode", "métric_with_ünïcode"},
		{"Long", strings.Repeat("very_long_metric_name_", 20)},
		{"Empty", ""},
		{"OnlySpecialChars", "!@#$%^&*()"},
	}

	scope := NewTestScope("sanitize", nil)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()

			// Create many metrics with the same pattern to test caching
			for i := 0; i < 1000; i++ {
				name := fmt.Sprintf("%s_%d", tc.input, i)
				scope.Counter(name).Inc(1)
			}

			elapsed := time.Since(start)

			// Should complete within reasonable time
			maxDuration := 100 * time.Millisecond
			assert.Less(t, elapsed, maxDuration,
				"Sanitization for %s should be fast: %v", tc.name, elapsed)
		})
	}
}

// TestKeyGenerationConsistency tests that key generation is consistent
func TestKeyGenerationConsistency(t *testing.T) {
	scope := NewTestScope("keygen", map[string]string{"service": "test"})

	// Test that identical scopes produce identical keys
	scope1 := scope.Tagged(map[string]string{"env": "prod", "version": "1.0"})
	scope2 := scope.Tagged(map[string]string{"version": "1.0", "env": "prod"}) // Different order

	// Create metrics with same names
	counter1 := scope1.Counter("test_counter")
	counter2 := scope2.Counter("test_counter")

	// Should be the same metric (scopes with same tags should be identical)
	assert.True(t, counter1 == counter2 || reflect.DeepEqual(counter1, counter2),
		"Scopes with identical tags (different order) should produce same metrics")
}

// TestRegistryScaling tests registry performance at different scales
func TestRegistryScaling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scaling test in short mode")
	}

	scales := []struct {
		name        string
		numScopes   int
		numMetrics  int
		maxDuration time.Duration
	}{
		{"Small", 100, 10, 50 * time.Millisecond},
		{"Medium", 1000, 100, 200 * time.Millisecond},
		{"Large", 5000, 1000, 2 * time.Second},
	}

	for _, scale := range scales {
		t.Run(scale.name, func(t *testing.T) {
			rootScope := NewTestScope("scale", nil)
			start := time.Now()

			// Create many scopes and metrics
			for i := 0; i < scale.numScopes; i++ {
				scope := rootScope.Tagged(map[string]string{
					"scope_id": fmt.Sprintf("%d", i),
					"batch":    fmt.Sprintf("%d", i/100),
				})

				for j := 0; j < scale.numMetrics; j++ {
					scope.Counter(fmt.Sprintf("metric_%d", j)).Inc(1)
				}
			}

			elapsed := time.Since(start)
			assert.Less(t, elapsed, scale.maxDuration,
				"Registry scaling for %s should complete within %v, took %v",
				scale.name, scale.maxDuration, elapsed)

			t.Logf("%s scale: %d scopes × %d metrics in %v",
				scale.name, scale.numScopes, scale.numMetrics, elapsed)
		})
	}
}

// TestTagOptimizationBoundaries tests the boundaries of tag optimization
func TestTagOptimizationBoundaries(t *testing.T) {
	scope := NewTestScope("tag_opt", nil)

	testCases := []struct {
		name            string
		tags            map[string]string
		expectOptimized bool
	}{
		{"NoTags", nil, true},
		{"EmptyTags", map[string]string{}, true},
		{"SingleTag", map[string]string{"key": "value"}, false},
		{"CommonPattern", map[string]string{"env": "prod", "service": "test"}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()

			// Create many scopes with same tag pattern
			var scopes []Scope
			for i := 0; i < 1000; i++ {
				scope := scope.Tagged(tc.tags)
				scopes = append(scopes, scope)
			}

			elapsed := time.Since(start)

			// Optimized cases should be faster
			if tc.expectOptimized {
				maxDuration := 10 * time.Millisecond
				assert.Less(t, elapsed, maxDuration,
					"Optimized tag case %s should be very fast: %v", tc.name, elapsed)
			}

			t.Logf("%s: %v for 1000 scopes (optimized: %v)",
				tc.name, elapsed, tc.expectOptimized)
		})
	}
}

// TestMetricNamePatterns tests different metric naming patterns
func TestMetricNamePatterns(t *testing.T) {
	scope := NewTestScope("patterns", nil)

	patterns := []struct {
		name    string
		pattern func(int) string
	}{
		{"Short", func(i int) string { return fmt.Sprintf("m%d", i) }},
		{"Medium", func(i int) string { return fmt.Sprintf("metric_%d", i) }},
		{"Long", func(i int) string { return fmt.Sprintf("very_long_metric_name_with_lots_of_details_%d", i) }},
		{"Hierarchical", func(i int) string { return fmt.Sprintf("service.component.method.%d", i) }},
		{"WithSpecialChars", func(i int) string { return fmt.Sprintf("metric-with.special@chars_%d", i) }},
	}

	for _, pattern := range patterns {
		t.Run(pattern.name, func(t *testing.T) {
			start := time.Now()

			// Create metrics with this pattern
			for i := 0; i < 1000; i++ {
				name := pattern.pattern(i)
				scope.Counter(name).Inc(1)
			}

			elapsed := time.Since(start)
			t.Logf("%s pattern: %v for 1000 metrics", pattern.name, elapsed)

			// All patterns should complete within reasonable time
			maxDuration := 200 * time.Millisecond
			assert.Less(t, elapsed, maxDuration,
				"Pattern %s should be efficient: %v", pattern.name, elapsed)
		})
	}
}

// TestScopeHierarchyDepthLimits tests scope hierarchy at various depths
func TestScopeHierarchyDepthLimits(t *testing.T) {
	rootScope := NewTestScope("hierarchy", nil)

	depths := []int{1, 5, 10, 20, 50}

	for _, depth := range depths {
		t.Run(fmt.Sprintf("Depth%d", depth), func(t *testing.T) {
			start := time.Now()

			// Create deep hierarchy
			currentScope := Scope(rootScope)
			for i := 0; i < depth; i++ {
				currentScope = currentScope.SubScope(fmt.Sprintf("level_%d", i))
			}

			// Create metric at leaf
			currentScope.Counter("leaf_metric").Inc(1)

			elapsed := time.Since(start)
			t.Logf("Depth %d: %v", depth, elapsed)

			// Should be efficient even at reasonable depths
			if depth <= 20 {
				maxDuration := 10 * time.Millisecond
				assert.Less(t, elapsed, maxDuration,
					"Hierarchy depth %d should be efficient: %v", depth, elapsed)
			}
		})
	}
}

// TestConcurrentRegistryAccess tests registry behavior under concurrent access
func TestConcurrentRegistryAccess(t *testing.T) {
	scope := NewTestScope("concurrent", nil)

	// Pre-warm with some metrics
	for i := 0; i < 100; i++ {
		scope.Counter(fmt.Sprintf("existing_%d", i))
	}

	start := time.Now()

	// Concurrent access patterns
	done := make(chan bool, 3)

	// Reader goroutine - accesses existing metrics
	go func() {
		for i := 0; i < 10000; i++ {
			scope.Counter(fmt.Sprintf("existing_%d", i%100)).Inc(1)
		}
		done <- true
	}()

	// Writer goroutine - creates new metrics
	go func() {
		for i := 0; i < 5000; i++ {
			scope.Counter(fmt.Sprintf("new_%d", i)).Inc(1)
		}
		done <- true
	}()

	// Mixed goroutine - mix of new and existing
	go func() {
		for i := 0; i < 7500; i++ {
			if i%2 == 0 {
				scope.Counter(fmt.Sprintf("existing_%d", i%100)).Inc(1)
			} else {
				scope.Counter(fmt.Sprintf("mixed_%d", i)).Inc(1)
			}
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}

	elapsed := time.Since(start)

	// Should handle concurrent access efficiently
	maxDuration := 2 * time.Second
	assert.Less(t, elapsed, maxDuration,
		"Concurrent registry access should be efficient: %v", elapsed)

	t.Logf("Concurrent access completed in %v", elapsed)
}

// TestMetricTypeCoexistence tests different metric types with same names
func TestMetricTypeCoexistence(t *testing.T) {
	scope := NewTestScope("coexist", nil)

	// These should be different metrics even with same base name
	counter := scope.Counter("metric")
	gauge := scope.Gauge("metric")
	timer := scope.Timer("metric")
	histogram := scope.Histogram("metric", DefaultBuckets)

	// Verify they're different objects
	assert.NotEqual(t, counter, gauge)
	assert.NotEqual(t, counter, timer)
	assert.NotEqual(t, counter, histogram)
	assert.NotEqual(t, gauge, timer)

	// Should be able to use them independently
	counter.Inc(1)
	gauge.Update(42.0)
	timer.Record(time.Millisecond)
	histogram.RecordValue(3.14)

	// Verify values
	snapshot := scope.Snapshot()

	foundCounter := false
	foundGauge := false
	foundTimer := false
	foundHistogram := false

	for _, c := range snapshot.Counters() {
		if strings.Contains(c.Name(), "metric") {
			foundCounter = true
		}
	}

	for _, g := range snapshot.Gauges() {
		if strings.Contains(g.Name(), "metric") {
			foundGauge = true
		}
	}

	for _, t := range snapshot.Timers() {
		if strings.Contains(t.Name(), "metric") {
			foundTimer = true
		}
	}

	for _, h := range snapshot.Histograms() {
		if strings.Contains(h.Name(), "metric") {
			foundHistogram = true
		}
	}

	assert.True(t, foundCounter, "Should find counter metric")
	assert.True(t, foundGauge, "Should find gauge metric")
	assert.True(t, foundTimer, "Should find timer metric")
	assert.True(t, foundHistogram, "Should find histogram metric")
}

// BenchmarkOptimizationBoundaries benchmarks various optimization scenarios
func BenchmarkOptimizationBoundaries(b *testing.B) {
	scope := NewTestScope("bench", nil)

	b.Run("EmptyTagsVsNil", func(b *testing.B) {
		b.Run("EmptyMap", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = scope.Tagged(map[string]string{})
			}
		})

		b.Run("NilMap", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = scope.Tagged(nil)
			}
		})
	})

	b.Run("MetricNameLengths", func(b *testing.B) {
		names := []string{
			"short",
			"medium_length_name",
			"very_long_metric_name_that_has_many_components_and_details",
			strings.Repeat("extremely_long_", 10),
		}

		for _, name := range names {
			b.Run(fmt.Sprintf("Len%d", len(name)), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					scope.Counter(fmt.Sprintf("%s_%d", name, i%1000)).Inc(1)
				}
			})
		}
	})

	b.Run("TagCounts", func(b *testing.B) {
		tagCounts := []int{0, 1, 3, 5, 10}

		for _, count := range tagCounts {
			b.Run(fmt.Sprintf("Tags%d", count), func(b *testing.B) {
				tags := make(map[string]string)
				for i := 0; i < count; i++ {
					tags[fmt.Sprintf("key_%d", i)] = fmt.Sprintf("value_%d", i)
				}

				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					scope.Tagged(tags).Counter("test").Inc(1)
				}
			})
		}
	})
}
