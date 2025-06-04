package m3

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tally "github.com/uber-go/tally/v6"
)

// TestTimerLimiting tests that the M3 reporter correctly limits timer metrics per metricId per batch
func TestTimerLimiting(t *testing.T) {
	// Create a reporter with a low timer limit for testing
	r, err := NewReporter(Options{
		HostPorts:                  []string{"localhost:9999"}, // fake address
		Service:                    "test-service",
		Env:                        "test",
		MaxTimersPerMetricPerBatch: 3, // Set limit to 3 for easy testing
	})
	require.NoError(t, err)
	defer r.Close()

	// Cast to internal reporter to access internal fields
	internalReporter := r.(*reporter)

	// Create a cached timer metric
	timer := internalReporter.AllocateTimer("test.timer", map[string]string{"tag1": "value1"})

	// Record more timer values than the limit
	for i := 0; i < 5; i++ {
		timer.ReportTimer(time.Duration(i) * time.Millisecond)
	}

	// Allow some time for processing
	time.Sleep(10 * time.Millisecond)

	// Check that the timers dropped counter increased
	droppedCount := internalReporter.numTimersDroppedLimit.Load()
	assert.True(t, droppedCount >= 2, "Expected at least 2 timers to be dropped (5 sent - 3 limit), got %d", droppedCount)
}

// TestTimerLimitingDifferentMetrics tests that the limit applies per metricId
func TestTimerLimitingDifferentMetrics(t *testing.T) {
	// Create a reporter with a low timer limit for testing
	r, err := NewReporter(Options{
		HostPorts:                  []string{"localhost:9999"}, // fake address
		Service:                    "test-service",
		Env:                        "test",
		MaxTimersPerMetricPerBatch: 2, // Set limit to 2 for easy testing
	})
	require.NoError(t, err)
	defer r.Close()

	// Cast to internal reporter to access internal fields
	internalReporter := r.(*reporter)

	// Create two different timer metrics
	timer1 := internalReporter.AllocateTimer("test.timer1", map[string]string{"tag1": "value1"})
	timer2 := internalReporter.AllocateTimer("test.timer2", map[string]string{"tag1": "value1"})

	// Record more timer values than the limit for each metric
	for i := 0; i < 3; i++ {
		timer1.ReportTimer(time.Duration(i) * time.Millisecond)
		timer2.ReportTimer(time.Duration(i+10) * time.Millisecond)
	}

	// Allow some time for processing
	time.Sleep(10 * time.Millisecond)

	// Check that timers were dropped (should be 2 total: 1 from each metric)
	droppedCount := internalReporter.numTimersDroppedLimit.Load()
	assert.True(t, droppedCount >= 2, "Expected at least 2 timers to be dropped (1 from each metric), got %d", droppedCount)
}

// TestTimerLimitingWithDifferentTags tests that different tag combinations create different metricIds
func TestTimerLimitingWithDifferentTags(t *testing.T) {
	// Create a reporter with a low timer limit for testing
	r, err := NewReporter(Options{
		HostPorts:                  []string{"localhost:9999"}, // fake address
		Service:                    "test-service",
		Env:                        "test",
		MaxTimersPerMetricPerBatch: 2, // Set limit to 2 for easy testing
	})
	require.NoError(t, err)
	defer r.Close()

	// Cast to internal reporter to access internal fields
	internalReporter := r.(*reporter)

	// Create timer metrics with same name but different tags (should be different metricIds)
	timer1 := internalReporter.AllocateTimer("test.timer", map[string]string{"tag1": "value1"})
	timer2 := internalReporter.AllocateTimer("test.timer", map[string]string{"tag1": "value2"})

	// Record more timer values than the limit for each metric
	for i := 0; i < 3; i++ {
		timer1.ReportTimer(time.Duration(i) * time.Millisecond)
		timer2.ReportTimer(time.Duration(i+10) * time.Millisecond)
	}

	// Allow some time for processing
	time.Sleep(10 * time.Millisecond)

	// Check that timers were dropped (should be 2 total: 1 from each metricId)
	droppedCount := internalReporter.numTimersDroppedLimit.Load()
	assert.True(t, droppedCount >= 2, "Expected at least 2 timers to be dropped (1 from each metricId), got %d", droppedCount)
}
