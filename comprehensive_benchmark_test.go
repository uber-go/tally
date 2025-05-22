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
	"strings"
	"testing"
	"time"
)

// BenchmarkRealWorldPatterns benchmarks realistic usage patterns
func BenchmarkRealWorldPatterns(b *testing.B) {
	scope := NewTestScope("service", map[string]string{
		"service": "my-service",
		"version": "1.0.0",
		"env":     "prod",
	})

	b.Run("WebServerPattern", func(b *testing.B) {
		// Simulate web server metrics
		endpoints := []string{"users", "orders", "products", "auth", "metrics"}
		methods := []string{"GET", "POST", "PUT", "DELETE"}
		statuses := []string{"200", "400", "404", "500"}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			endpoint := endpoints[i%len(endpoints)]
			method := methods[i%len(methods)]
			status := statuses[i%len(statuses)]

			requestScope := scope.Tagged(map[string]string{
				"endpoint": endpoint,
				"method":   method,
				"status":   status,
			})

			requestScope.Counter("requests").Inc(1)
			requestScope.Timer("response_time").Record(time.Duration(i%1000) * time.Microsecond)
			requestScope.Histogram("response_size", DefaultBuckets).RecordValue(float64(i % 10000))
		}
	})

	b.Run("DatabasePattern", func(b *testing.B) {
		// Simulate database client metrics
		tables := []string{"users", "orders", "products", "sessions"}
		operations := []string{"select", "insert", "update", "delete"}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			table := tables[i%len(tables)]
			operation := operations[i%len(operations)]

			dbScope := scope.Tagged(map[string]string{
				"table":     table,
				"operation": operation,
			})

			dbScope.Counter("db_queries").Inc(1)
			dbScope.Timer("db_duration").Record(time.Duration(i%5000) * time.Microsecond)
			dbScope.Gauge("connection_pool_size").Update(float64(i%100) + 10)
		}
	})

	b.Run("BackgroundJobPattern", func(b *testing.B) {
		// Simulate background job processor metrics
		jobTypes := []string{"email", "notification", "data_sync", "cleanup"}
		queues := []string{"high", "normal", "low"}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			jobType := jobTypes[i%len(jobTypes)]
			queue := queues[i%len(queues)]

			jobScope := scope.Tagged(map[string]string{
				"job_type": jobType,
				"queue":    queue,
			})

			jobScope.Counter("jobs_processed").Inc(1)
			jobScope.Timer("job_duration").Record(time.Duration(i%10000) * time.Microsecond)
			jobScope.Gauge("queue_depth").Update(float64(i % 500))
		}
	})
}

// BenchmarkHighCardinalityScenarios tests performance under high cardinality
func BenchmarkHighCardinalityScenarios(b *testing.B) {
	scope := NewTestScope("high_cardinality", nil)

	b.Run("UserScopedMetrics", func(b *testing.B) {
		// Simulate per-user metrics (high cardinality)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			userScope := scope.Tagged(map[string]string{
				"user_id":     fmt.Sprintf("user_%d", i%10000), // 10k unique users
				"user_tier":   fmt.Sprintf("tier_%d", i%5),     // 5 tiers
				"user_region": fmt.Sprintf("region_%d", i%10),  // 10 regions
			})

			userScope.Counter("user_actions").Inc(1)
			userScope.Gauge("user_session_duration").Update(float64(i % 3600))
		}
	})

	b.Run("TimeSeriesMetrics", func(b *testing.B) {
		// Simulate time-series metrics with temporal tags
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			timeScope := scope.Tagged(map[string]string{
				"hour":   fmt.Sprintf("%02d", i%24),
				"minute": fmt.Sprintf("%02d", (i/24)%60),
				"day":    fmt.Sprintf("%d", i%7),
			})

			timeScope.Counter("events").Inc(1)
			timeScope.Gauge("cpu_usage").Update(float64(i%100) / 100.0)
		}
	})

	b.Run("DynamicTags", func(b *testing.B) {
		// Simulate metrics with dynamic tag values
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			dynamicScope := scope.Tagged(map[string]string{
				"request_id": fmt.Sprintf("req_%d", i),
				"trace_id":   fmt.Sprintf("trace_%d", i/100), // Groups of 100
				"span_id":    fmt.Sprintf("span_%d", i%50),   // 50 unique spans
			})

			dynamicScope.Counter("trace_spans").Inc(1)
			dynamicScope.Timer("span_duration").Record(time.Duration(i%1000) * time.Microsecond)
		}
	})
}

// BenchmarkOptimizationCandidates identifies areas for optimization
func BenchmarkOptimizationCandidates(b *testing.B) {
	scope := NewTestScope("optimization", nil)

	b.Run("StringConcatenation", func(b *testing.B) {
		// Test string building performance
		parts := []string{"service", "component", "method", "status"}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Simulate building metric names
			name := strings.Join(parts, ".") + fmt.Sprintf(".%d", i%1000)
			scope.Counter(name).Inc(1)
		}
	})

	b.Run("MapAllocation", func(b *testing.B) {
		// Test map allocation overhead
		keys := []string{"env", "service", "version", "region"}
		values := []string{"prod", "test-service", "1.0", "us-east-1"}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tags := make(map[string]string, len(keys))
			for j, key := range keys {
				tags[key] = values[j]
			}
			tags["instance"] = fmt.Sprintf("i-%d", i%100)

			scope.Tagged(tags).Counter("requests").Inc(1)
		}
	})

	b.Run("TagSorting", func(b *testing.B) {
		// Test tag sorting overhead
		baseTags := map[string]string{
			"zzz_last":   "last",
			"aaa_first":  "first",
			"mmm_middle": "middle",
			"service":    "test",
			"env":        "prod",
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Create new map to force sorting
			tags := make(map[string]string)
			for k, v := range baseTags {
				tags[k] = v
			}
			tags["dynamic"] = fmt.Sprintf("value_%d", i%100)

			scope.Tagged(tags).Counter("sorted_tags").Inc(1)
		}
	})

	b.Run("MetricLookup", func(b *testing.B) {
		// Pre-create many metrics to test lookup performance
		for i := 0; i < 10000; i++ {
			scope.Counter(fmt.Sprintf("metric_%d", i))
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Access existing metrics
			scope.Counter(fmt.Sprintf("metric_%d", i%10000)).Inc(1)
		}
	})
}

// BenchmarkMemoryIntensiveOperations tests memory-heavy scenarios
func BenchmarkMemoryIntensiveOperations(b *testing.B) {
	scope := NewTestScope("memory", nil)

	b.Run("ManyUniqueMetrics", func(b *testing.B) {
		// Create many unique metrics
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			name := fmt.Sprintf("unique_metric_%d", i)
			scope.Counter(name).Inc(1)
		}
	})

	b.Run("DeepScopeHierarchy", func(b *testing.B) {
		// Create deep scope hierarchies
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			currentScope := Scope(scope)
			depth := i%10 + 1 // 1-10 levels deep

			for j := 0; j < depth; j++ {
				currentScope = currentScope.SubScope(fmt.Sprintf("level_%d", j))
			}

			currentScope.Counter("deep_metric").Inc(1)
		}
	})

	b.Run("LargeTagValues", func(b *testing.B) {
		// Test with large tag values
		largeValue := strings.Repeat("large_tag_value_", 100) // ~1.6KB per tag

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tags := map[string]string{
				"large_tag_1": largeValue + fmt.Sprintf("_%d", i%10),
				"large_tag_2": largeValue + fmt.Sprintf("_%d", (i+1)%10),
				"small_tag":   fmt.Sprintf("small_%d", i%5),
			}

			scope.Tagged(tags).Counter("large_tag_metric").Inc(1)
		}
	})
}

// BenchmarkConcurrentPatterns tests concurrent usage patterns
func BenchmarkConcurrentPatterns(b *testing.B) {
	scope := NewTestScope("concurrent", nil)

	b.Run("SharedMetrics", func(b *testing.B) {
		// Pre-create shared metrics
		counter := scope.Counter("shared_counter")
		gauge := scope.Gauge("shared_gauge")
		timer := scope.Timer("shared_timer")

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				switch i % 3 {
				case 0:
					counter.Inc(1)
				case 1:
					gauge.Update(float64(i))
				case 2:
					timer.Record(time.Microsecond)
				}
				i++
			}
		})
	})

	b.Run("UniqueMetricsPerGoroutine", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			goroutineID := 0 // Would be set to actual goroutine ID in real scenario
			i := 0
			for pb.Next() {
				name := fmt.Sprintf("goroutine_%d_metric_%d", goroutineID, i)
				scope.Counter(name).Inc(1)
				i++
			}
		})
	})

	b.Run("ScopeCreationRace", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				// Different goroutines creating scopes with overlapping tags
				tags := map[string]string{
					"goroutine": fmt.Sprintf("g_%d", i%10),
					"batch":     fmt.Sprintf("b_%d", i%5),
					"iteration": fmt.Sprintf("i_%d", i),
				}

				scope.Tagged(tags).Counter("race_metric").Inc(1)
				i++
			}
		})
	})
}

// BenchmarkReporterIntegration tests with actual reporters
func BenchmarkReporterIntegration(b *testing.B) {
	// Test with null reporter (fastest)
	b.Run("NullReporter", func(b *testing.B) {
		scope, _ := NewRootScope(ScopeOptions{
			Reporter: NullStatsReporter,
		}, time.Second)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			scope.Counter("null_counter").Inc(1)
			scope.Gauge("null_gauge").Update(float64(i))
			scope.Timer("null_timer").Record(time.Microsecond)
		}
	})

	// Test with test reporter (captures metrics)
	b.Run("TestReporter", func(b *testing.B) {
		scope := NewTestScope("bench", nil)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			scope.Counter("test_counter").Inc(1)
			scope.Gauge("test_gauge").Update(float64(i))
			scope.Timer("test_timer").Record(time.Microsecond)
		}
	})
}

// BenchmarkEdgeCases tests edge cases and boundary conditions
func BenchmarkEdgeCases(b *testing.B) {
	scope := NewTestScope("edge", nil)

	b.Run("EmptyStrings", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Test with empty metric names and tag values
			tags := map[string]string{
				"empty_value": "",
				"normal":      "value",
			}

			taggedScope := scope.Tagged(tags)
			taggedScope.Counter("empty_test").Inc(1)
		}
	})

	b.Run("SpecialCharacters", func(b *testing.B) {
		specialChars := "!@#$%^&*(){}[]|\\:;\"'<>,.?/~`"

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Test metric names requiring sanitization
			name := fmt.Sprintf("special_%s_%d", string(specialChars[i%len(specialChars)]), i)
			scope.Counter(name).Inc(1)
		}
	})

	b.Run("VeryLongNames", func(b *testing.B) {
		longName := strings.Repeat("very_long_metric_name_part_", 50) // ~1.35KB

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			name := fmt.Sprintf("%s_%d", longName, i%100)
			scope.Counter(name).Inc(1)
		}
	})

	b.Run("ManyTags", func(b *testing.B) {
		// Create base tags
		baseTags := make(map[string]string)
		for i := 0; i < 50; i++ { // 50 tags per metric
			baseTags[fmt.Sprintf("tag_%d", i)] = fmt.Sprintf("value_%d", i)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Add one dynamic tag
			tags := make(map[string]string)
			for k, v := range baseTags {
				tags[k] = v
			}
			tags["dynamic"] = fmt.Sprintf("dynamic_%d", i%10)

			scope.Tagged(tags).Counter("many_tags").Inc(1)
		}
	})
}
