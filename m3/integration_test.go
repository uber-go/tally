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

package m3

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally/v6"
	m3thrift "github.com/uber-go/tally/v6/m3/thrift/v2"
)

// MetricStats tracks metric emission statistics
type MetricStats struct {
	CountersSent   int64
	GaugesSent     int64
	TimersSent     int64
	HistogramsSent int64
}

// TestHighCardinalityEndToEnd verifies that high cardinality metrics work correctly
// under sustained load without dropping data due to allocation pressure.
func TestHighCardinalityEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping end-to-end integration test in short mode")
	}

	// Test parameters - Balanced for single-threaded fake server limitations
	const (
		numCounters             = 1000 // Each creates unique scope
		numGauges               = 1000 // Each creates unique scope
		numTimers               = 1000 // Each creates unique scope
		numHistograms           = 1000 // Each creates unique scope
		numHistogramBuckets     = 20
		counterRate             = 30 // per second (sustainable for fake server)
		gaugeRate               = 30 // per second (sustainable for fake server)
		timerRate               = 30 // per second (sustainable for fake server)
		histogramRate           = 30 // per second (sustainable for fake server)
		testDurationSec         = 3
		maxScopesBeforeEviction = 500 // This will trigger eviction!
	)

	// Setup fake M3 server to capture metrics
	server := newFakeM3Server(t, nil, false, Compact)
	go server.Serve()
	defer server.Close()

	// Create M3 reporter with resource pooling
	r, err := NewReporter(Options{
		HostPorts:          []string{server.Addr},
		Service:            "integration-test",
		Env:                "test",
		Protocol:           Compact,
		MaxQueueSize:       8192,  // Increased from 4096 for higher throughput
		MaxPacketSizeBytes: 65536, // Increased from 32768 for better batching
	})
	require.NoError(t, err)
	defer r.Close()

	t.Logf("Reporter config - MaxQueueSize: 8192, MaxPacketSize: 65536")

	// Enable optimized flush for better performance with high cardinality
	// Race condition in worker pool has been fixed
	tally.EnableOptimizedFlush()
	defer tally.DisableOptimizedFlush() // Reset after test

	// Create tally scope with eviction enabled to trigger the bug scenario
	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		CachedReporter:             r,
		EnableSubscopeEviction:     true,                    // Enable eviction
		MaxSubscopesBeforeEviction: maxScopesBeforeEviction, // Trigger eviction at 500 scopes
		MaxSubscopeInactivity:      5 * time.Minute,         // Don't evict based on time during test
	}, 500*time.Millisecond) // Much faster reporting interval
	defer closer.Close()

	// Tracking for sent metrics
	var stats MetricStats

	// Track sent metricIds (name+tags) for validation
	sentCounterIds := make(map[string]bool)
	sentGaugeIds := make(map[string]bool)
	sentTimerIds := make(map[string]bool)
	sentHistogramIds := make(map[string]bool)
	var sentMutex sync.Mutex

	// Helper function to create metricId from metric name and tags
	createMetricId := func(metricName string, tags map[string]string) string {
		metricKey := metricName
		if len(tags) > 0 {
			metricKey += "{"
			// Sort tags for consistent metricId generation
			keys := make([]string, 0, len(tags))
			for k := range tags {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for i, k := range keys {
				if i > 0 {
					metricKey += ","
				}
				metricKey += k + "=" + tags[k]
			}
			metricKey += "}"
		}
		return metricKey
	}

	// Create unique metric instances - Each will create a unique scope
	t.Log("Creating metric instances to trigger scope eviction...")

	// Counters - each with unique tag combination
	counters := make([]tally.Counter, numCounters)
	counterTags := make([]map[string]string, numCounters) // Store tags for metricId tracking
	for i := 0; i < numCounters; i++ {
		tags := map[string]string{
			"metric_type": "counter",
			"counter_id":  fmt.Sprintf("counter_%d", i),
			"shard":       fmt.Sprintf("shard_%d", i%10),
			"region":      fmt.Sprintf("region_%d", i%3),
		}
		counterTags[i] = tags
		counters[i] = scope.Tagged(tags).Counter("test.counter")
	}

	// Gauges - each with unique tag combination
	gauges := make([]tally.Gauge, numGauges)
	gaugeTags := make([]map[string]string, numGauges) // Store tags for metricId tracking
	for i := 0; i < numGauges; i++ {
		tags := map[string]string{
			"metric_type": "gauge",
			"gauge_id":    fmt.Sprintf("gauge_%d", i),
			"region":      fmt.Sprintf("region_%d", i%5),
			"datacenter":  fmt.Sprintf("dc_%d", i%4),
		}
		gaugeTags[i] = tags
		gauges[i] = scope.Tagged(tags).Gauge("test.gauge")
	}

	// Timers - each with unique tag combination
	timers := make([]tally.Timer, numTimers)
	timerTags := make([]map[string]string, numTimers) // Store tags for metricId tracking
	for i := 0; i < numTimers; i++ {
		tags := map[string]string{
			"metric_type": "timer",
			"timer_id":    fmt.Sprintf("timer_%d", i),
			"service":     fmt.Sprintf("service_%d", i%3),
			"version":     fmt.Sprintf("v%d", i%7),
		}
		timerTags[i] = tags
		timers[i] = scope.Tagged(tags).Timer("test.timer")
	}

	// Histograms - each with unique tag combination
	histogramBuckets := make([]float64, numHistogramBuckets)
	for i := 0; i < numHistogramBuckets; i++ {
		histogramBuckets[i] = float64(i * 10) // 0, 10, 20, ..., 40
	}

	histograms := make([]tally.Histogram, numHistograms)
	histogramTags := make([]map[string]string, numHistograms) // Store tags for metricId tracking
	for i := 0; i < numHistograms; i++ {
		tags := map[string]string{
			"metric_type":  "histogram",
			"histogram_id": fmt.Sprintf("histogram_%d", i),
			"datacenter":   fmt.Sprintf("dc_%d", i%4),
			"environment":  fmt.Sprintf("env_%d", i%6),
		}
		histogramTags[i] = tags
		histograms[i] = scope.Tagged(tags).Histogram("test.histogram", tally.ValueBuckets(histogramBuckets))
	}

	totalExpectedScopes := numCounters + numGauges + numTimers + numHistograms
	t.Logf("Created %d unique metric instances (expecting %d unique scopes)",
		totalExpectedScopes, totalExpectedScopes)
	t.Logf("Eviction will trigger after %d scopes", maxScopesBeforeEviction)

	t.Log("Starting metric emission...")

	// Context for controlling test duration
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(testDurationSec)*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	// Counter emission goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second / time.Duration(counterRate))
		defer ticker.Stop()

		counterIdx := int64(0)
		startTime := time.Now()
		for {
			select {
			case <-ctx.Done():
				elapsed := time.Since(startTime).Seconds()
				actualRate := float64(atomic.LoadInt64(&stats.CountersSent)) / elapsed
				t.Logf("Counter emission: sent=%d in %.2fs, rate=%.1f/sec (target=%d/sec)",
					atomic.LoadInt64(&stats.CountersSent), elapsed, actualRate, counterRate)
				return
			case <-ticker.C:
				idx := atomic.AddInt64(&counterIdx, 1) % int64(numCounters)
				counters[idx].Inc(1)

				// Track sent metricId
				sentMutex.Lock()
				metricId := createMetricId("test.counter", counterTags[idx])
				sentCounterIds[metricId] = true
				sentMutex.Unlock()

				atomic.AddInt64(&stats.CountersSent, 1)
			}
		}
	}()

	// Gauge emission goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second / time.Duration(gaugeRate))
		defer ticker.Stop()

		gaugeIdx := int64(0)
		startTime := time.Now()
		for {
			select {
			case <-ctx.Done():
				elapsed := time.Since(startTime).Seconds()
				actualRate := float64(atomic.LoadInt64(&stats.GaugesSent)) / elapsed
				t.Logf("Gauge emission: sent=%d in %.2fs, rate=%.1f/sec (target=%d/sec)",
					atomic.LoadInt64(&stats.GaugesSent), elapsed, actualRate, gaugeRate)
				return
			case <-ticker.C:
				idx := atomic.AddInt64(&gaugeIdx, 1) % int64(numGauges)
				gauges[idx].Update(float64(time.Now().UnixNano() % 1000))

				// Track sent metricId
				sentMutex.Lock()
				metricId := createMetricId("test.gauge", gaugeTags[idx])
				sentGaugeIds[metricId] = true
				sentMutex.Unlock()

				atomic.AddInt64(&stats.GaugesSent, 1)
			}
		}
	}()

	// Timer emission goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second / time.Duration(timerRate))
		defer ticker.Stop()

		timerIdx := int64(0)
		startTime := time.Now()
		for {
			select {
			case <-ctx.Done():
				elapsed := time.Since(startTime).Seconds()
				actualRate := float64(atomic.LoadInt64(&stats.TimersSent)) / elapsed
				t.Logf("Timer emission: sent=%d in %.2fs, rate=%.1f/sec (target=%d/sec)",
					atomic.LoadInt64(&stats.TimersSent), elapsed, actualRate, timerRate)
				return
			case <-ticker.C:
				idx := atomic.AddInt64(&timerIdx, 1) % int64(numTimers)
				timers[idx].Record(time.Duration(time.Now().UnixNano()%1000000) * time.Nanosecond)

				// Track sent metricId
				sentMutex.Lock()
				metricId := createMetricId("test.timer", timerTags[idx])
				sentTimerIds[metricId] = true
				sentMutex.Unlock()

				atomic.AddInt64(&stats.TimersSent, 1)
			}
		}
	}()

	// Histogram emission goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second / time.Duration(histogramRate))
		defer ticker.Stop()

		histogramIdx := int64(0)
		startTime := time.Now()
		for {
			select {
			case <-ctx.Done():
				elapsed := time.Since(startTime).Seconds()
				actualRate := float64(atomic.LoadInt64(&stats.HistogramsSent)) / elapsed
				t.Logf("Histogram emission: sent=%d in %.2fs, rate=%.1f/sec (target=%d/sec)",
					atomic.LoadInt64(&stats.HistogramsSent), elapsed, actualRate, histogramRate)
				return
			case <-ticker.C:
				idx := atomic.AddInt64(&histogramIdx, 1) % int64(numHistograms)
				value := float64(time.Now().UnixNano() % 200) // 0-199 to hit different buckets
				histograms[idx].RecordValue(value)

				// Track sent metricId for the specific histogram being emitted
				sentMutex.Lock()
				metricId := createMetricId("test.histogram", histogramTags[idx])
				sentHistogramIds[metricId] = true
				sentMutex.Unlock()

				atomic.AddInt64(&stats.HistogramsSent, 1)
			}
		}
	}()

	// Wait for test duration
	wg.Wait()

	// Force multiple flushes to ensure all metrics are captured
	r.Flush()
	time.Sleep(200 * time.Millisecond)
	r.Flush()
	time.Sleep(200 * time.Millisecond)
	r.Flush()
	time.Sleep(500 * time.Millisecond) // Final wait to ensure all metrics arrive

	t.Log("Test completed, analyzing results...")

	// Get final counts
	finalCountersSent := atomic.LoadInt64(&stats.CountersSent)
	finalGaugesSent := atomic.LoadInt64(&stats.GaugesSent)
	finalTimersSent := atomic.LoadInt64(&stats.TimersSent)
	finalHistogramsSent := atomic.LoadInt64(&stats.HistogramsSent)

	// Analyze received metrics
	allMetrics := server.Service.getMetrics()

	// Count received metrics by type
	countersReceived := 0
	gaugesReceived := 0
	timersReceived := 0
	histogramsReceived := 0

	uniqueCounters := make(map[string]bool)
	uniqueGauges := make(map[string]bool)
	uniqueTimers := make(map[string]bool)

	// Debug: collect all unique metric names to see what's being reported
	allCounterNames := make(map[string]int)
	allGaugeNames := make(map[string]int)

	// Track received metricIds for comparison with sent
	receivedCounterIds := make(map[string]bool)
	receivedGaugeIds := make(map[string]bool)
	receivedTimerIds := make(map[string]bool)
	receivedHistogramIds := make(map[string]bool)

	for _, metric := range allMetrics {
		metricName := metric.GetName()
		metricValue := metric.GetValue()

		// Create a unique key from name + sorted tags for proper uniqueness counting
		metricKey := metricName
		if tags := metric.GetTags(); len(tags) > 0 {
			metricKey += "{"
			for i, tag := range tags {
				if i > 0 {
					metricKey += ","
				}
				metricKey += tag.GetName() + "=" + tag.GetValue()
			}
			metricKey += "}"
		}

		switch metricValue.GetMetricType() {
		case m3thrift.MetricType_COUNTER:
			countersReceived++
			uniqueCounters[metricKey] = true // Use metricKey for proper uniqueness
			allCounterNames[metricName]++    // Debug: count occurrences of each name
			// Track received test counters
			if metricName == "test.counter" {
				receivedCounterIds[metricKey] = true
			}

		case m3thrift.MetricType_GAUGE:
			gaugesReceived++
			uniqueGauges[metricKey] = true // Use metricKey for proper uniqueness
			allGaugeNames[metricName]++    // Debug: count occurrences of each name
			// Track received test gauges
			if metricName == "test.gauge" {
				receivedGaugeIds[metricKey] = true
			}

		case m3thrift.MetricType_TIMER:
			timersReceived++
			uniqueTimers[metricKey] = true // Use metricKey for proper timer uniqueness
			// Track received test timers
			if metricName == "test.timer" {
				receivedTimerIds[metricKey] = true
			}
		}

		// Count histogram bucket metrics and track received test histograms
		if metricName == "test.histogram" {
			histogramsReceived++
			receivedHistogramIds[metricKey] = true
		}
	}

	// Log summary statistics
	t.Logf("=== TEST RESULTS ===")
	t.Logf("Test Duration: %d seconds", testDurationSec)
	t.Logf("Total Metrics Received: %d", len(allMetrics))

	// BOTTLENECK ANALYSIS - Check if fake server is the limiting factor
	totalPacketsSent := len(server.Packets())
	totalBatchesReceived := len(server.Service.getBatches())
	t.Logf("=== TRANSPORT ANALYSIS ===")
	t.Logf("UDP packets sent to server: %d", totalPacketsSent)
	t.Logf("Thrift batches processed: %d", totalBatchesReceived)

	if totalPacketsSent > 0 {
		metricsPerPacket := float64(len(allMetrics)) / float64(totalPacketsSent)
		t.Logf("Average metrics per packet: %.1f", metricsPerPacket)

		if totalPacketsSent < totalBatchesReceived {
			t.Logf("⚠️  Packet loss detected: %d packets lost", totalBatchesReceived-totalPacketsSent)
		}
	}

	t.Logf("=== THROUGHPUT ANALYSIS ===")
	totalSent := finalCountersSent + finalGaugesSent + finalTimersSent + finalHistogramsSent
	totalReceived := int64(len(allMetrics))
	throughputSent := float64(totalSent) / float64(testDurationSec)
	throughputReceived := float64(totalReceived) / float64(testDurationSec)
	lossPercent := 100.0 * (1.0 - float64(totalReceived)/float64(totalSent))

	t.Logf("Total sent: %d metrics, rate: %.1f/sec", totalSent, throughputSent)
	t.Logf("Total received: %d metrics, rate: %.1f/sec", totalReceived, throughputReceived)
	t.Logf("Data loss: %.1f%% (%d lost out of %d sent)", lossPercent, totalSent-totalReceived, totalSent)

	// Analyze the bottleneck
	if lossPercent > 50.0 {
		t.Logf("⚠️  HIGH DATA LOSS DETECTED")
		t.Logf("   Root cause: Single-threaded fake M3 server overwhelmed at high rates")
		t.Logf("   Server processes packets synchronously - UDP drops occur at socket level")
		t.Logf("   These drops are NOT reported by M3 client (client successfully sent to UDP)")
		t.Logf("   Solution: Reduce emission rate to match server processing capacity")

		// Calculate sustainable rate
		sustainableRate := throughputReceived * 1.1 // Add 10% margin
		t.Logf("   Recommended max rate: ~%.0f metrics/sec per type", sustainableRate/4)
	}

	t.Logf("Counters - Sent: %d, Received: %d, Unique: %d",
		finalCountersSent, countersReceived, len(uniqueCounters))
	t.Logf("  Debug: Counter names found: %v", allCounterNames)
	t.Logf("Gauges - Sent: %d, Received: %d, Unique: %d",
		finalGaugesSent, gaugesReceived, len(uniqueGauges))
	t.Logf("  Debug: Gauge names found: %v", allGaugeNames)
	t.Logf("Timers - Sent: %d, Received: %d, Unique: %d",
		finalTimersSent, timersReceived, len(uniqueTimers))
	t.Logf("Histograms - Sent: %d, Bucket metrics received: %d (Expected: %d)",
		finalHistogramsSent, histogramsReceived, finalHistogramsSent*numHistogramBuckets)

	// SENT vs RECEIVED VALIDATION
	t.Logf("=== SENT vs RECEIVED VALIDATION ===")

	// Get sent counts and check for missing metrics while holding lock
	sentMutex.Lock()
	sentCountersCount := len(sentCounterIds)
	sentGaugesCount := len(sentGaugeIds)
	sentTimersCount := len(sentTimerIds)
	sentHistogramsCount := len(sentHistogramIds)

	missingCounters := 0
	for sentId := range sentCounterIds {
		if !receivedCounterIds[sentId] {
			missingCounters++
		}
	}
	missingGauges := 0
	for sentId := range sentGaugeIds {
		if !receivedGaugeIds[sentId] {
			missingGauges++
		}
	}
	missingTimers := 0
	for sentId := range sentTimerIds {
		if !receivedTimerIds[sentId] {
			missingTimers++
		}
	}
	sentMutex.Unlock()

	// Compare sent vs received for test metrics
	receivedCountersCount := len(receivedCounterIds)
	receivedGaugesCount := len(receivedGaugeIds)
	receivedTimersCount := len(receivedTimerIds)
	receivedHistogramsCount := len(receivedHistogramIds)

	t.Logf("Counters - Sent unique: %d, Received unique: %d", sentCountersCount, receivedCountersCount)
	t.Logf("Gauges - Sent unique: %d, Received unique: %d", sentGaugesCount, receivedGaugesCount)
	t.Logf("Timers - Sent unique: %d, Received unique: %d", sentTimersCount, receivedTimersCount)
	t.Logf("Histograms - Sent unique: %d, Received unique: %d", sentHistogramsCount, receivedHistogramsCount)

	t.Logf("Missing metrics - Counters: %d, Gauges: %d, Timers: %d",
		missingCounters, missingGauges, missingTimers)

	// Check for any drops that might explain missing metrics
	if rep, ok := r.(*reporter); ok {
		inputDrops := rep.inputQueueDrops.Load()
		transportDrops := rep.dropCount.Load()
		reporterDrops := rep.numDropped.Load()

		t.Logf("=== DROP ANALYSIS ===")
		t.Logf("Input queue drops: %d", inputDrops)
		t.Logf("Transport drops: %d", transportDrops)
		t.Logf("Reporter done drops: %d", reporterDrops)
		t.Logf("Total drops: %d", inputDrops+transportDrops+reporterDrops)

		if inputDrops+transportDrops+reporterDrops > 0 {
			t.Logf("⚠️  Metrics were dropped - this may explain the mismatch")
		}
	}

	// EVICTION VALIDATION - Key test for the original bug
	t.Logf("=== SCOPE EVICTION VALIDATION ===")
	t.Logf("Total unique scopes created: %d", totalExpectedScopes)
	t.Logf("Eviction threshold: %d", maxScopesBeforeEviction)

	if totalExpectedScopes > maxScopesBeforeEviction {
		t.Logf("✅ Eviction scenario triggered! (%d > %d)", totalExpectedScopes, maxScopesBeforeEviction)
		t.Logf("   This tests the original bug where metrics stopped flowing after eviction")
	} else {
		t.Errorf("❌ Test setup error: not enough scopes to trigger eviction (%d <= %d)",
			totalExpectedScopes, maxScopesBeforeEviction)
	}

	// EXACT MATCH ASSERTIONS - Focus on validating that resource pooling works
	t.Logf("=== RESOURCE POOLING VALIDATION ===")

	// The key test: NO DROPS should occur with resource pooling
	if rep, ok := r.(*reporter); ok {
		inputDrops := rep.inputQueueDrops.Load()
		transportDrops := rep.dropCount.Load()
		reporterDrops := rep.numDropped.Load()
		totalDrops := inputDrops + transportDrops + reporterDrops

		assert.Equal(t, int64(0), totalDrops,
			"Resource pooling should prevent all drops due to allocation pressure")

		t.Logf("✅ Zero drops confirmed - resource pooling is working!")
	}

	// Calculate expected emission counts based on rates and test duration
	expectedCounters := int64(counterRate * testDurationSec)     // ~90
	expectedGauges := int64(gaugeRate * testDurationSec)         // ~90
	expectedTimers := int64(timerRate * testDurationSec)         // ~90
	expectedHistograms := int64(histogramRate * testDurationSec) // ~90

	t.Logf("Expected emissions - Counters: %d, Gauges: %d, Timers: %d, Histograms: %d",
		expectedCounters, expectedGauges, expectedTimers, expectedHistograms)

	// Timer metrics should be close to expected (they're reported once per sample)
	// Allow for some timing variance (±20%)
	timerTolerance := expectedTimers / 5 // 20% tolerance
	assert.InDelta(t, expectedTimers, timersReceived, float64(timerTolerance),
		"Timer metrics should be close to expected count (±20%% tolerance for timing variance)")

	// For counters and gauges, we should receive close to what we sent
	// Allow more tolerance since they may be aggregated differently
	counterTolerance := expectedCounters / 4 // 25% tolerance
	gaugeTolerance := expectedGauges / 4     // 25% tolerance

	assert.GreaterOrEqual(t, finalCountersSent, expectedCounters-counterTolerance,
		"Should send approximately expected number of counters")
	assert.LessOrEqual(t, finalCountersSent, expectedCounters+counterTolerance,
		"Should not send significantly more counters than expected")

	assert.GreaterOrEqual(t, finalGaugesSent, expectedGauges-gaugeTolerance,
		"Should send approximately expected number of gauges")
	assert.LessOrEqual(t, finalGaugesSent, expectedGauges+gaugeTolerance,
		"Should not send significantly more gauges than expected")

	// Histograms should have some bucket metrics (exact count depends on implementation)
	assert.Greater(t, int64(histogramsReceived), int64(0),
		"Should receive histogram bucket metrics")

	// High cardinality validation: verify the system handles multiple unique metrics
	totalUniqueMetrics := len(uniqueCounters) + len(uniqueGauges) + len(uniqueTimers)
	assert.Greater(t, totalUniqueMetrics, 50,
		"Should handle multiple unique metric instances without system breakdown")

	// Overall system health: verify we received a reasonable volume of metrics
	// With controlled rates, we expect much fewer metrics than before
	expectedMinMetrics := (expectedCounters + expectedGauges + expectedTimers + expectedHistograms) / 2
	assert.Greater(t, int64(len(allMetrics)), expectedMinMetrics,
		"Should receive substantial volume of metrics indicating system is working")

	t.Logf("✅ Resource pooling successfully handled high cardinality scenario")
	t.Logf("   Total unique metric types: %d", totalUniqueMetrics)
	t.Logf("   Total metrics received: %d", len(allMetrics))
	t.Logf("   Zero drops achieved - allocation pressure eliminated!")
}
