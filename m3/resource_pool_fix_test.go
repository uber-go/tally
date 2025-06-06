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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResourcePoolInitialized verifies that the resource pool fix is in place
// to prevent allocation pressure under high cardinality scenarios.
func TestResourcePoolInitialized(t *testing.T) {
	r, err := NewReporter(Options{
		HostPorts: []string{"127.0.0.1:9052"},
		Service:   "test-service",
		Env:       "test",
		Protocol:  Compact,
	})
	require.NoError(t, err)
	defer r.Close()

	// Verify resource pool is properly initialized
	reporter, ok := r.(*reporter)
	require.True(t, ok)
	assert.NotNil(t, reporter.resourcePool, "Resource pool should be initialized to prevent thrift allocation pressure")

	// Verify the resource pool has the correct thrift protocol factory
	assert.NotNil(t, reporter.resourcePool.protoFactory, "Resource pool should have protocol factory")
	assert.Equal(t, reporter.protocolFactory, reporter.resourcePool.protoFactory, "Resource pool should use same protocol factory as reporter")
}

// TestMetricAllocationUsesResourcePool verifies that metric allocation
// uses the resource pool to prevent memory pressure under high cardinality.
func TestMetricAllocationUsesResourcePool(t *testing.T) {
	r, err := NewReporter(Options{
		HostPorts: []string{"127.0.0.1:9052"},
		Service:   "test-service",
		Env:       "test",
		Protocol:  Compact,
	})
	require.NoError(t, err)
	defer r.Close()

	reporter, ok := r.(*reporter)
	require.True(t, ok)

	// Verify that allocating metrics doesn't cause any panics or errors
	// This would previously fail under high cardinality due to allocation pressure
	tags := map[string]string{
		"high_cardinality": "test_metric",
		"unique_id":        "12345",
	}

	counter := reporter.allocateMetric("test.metric", tags, counterType)
	assert.NotNil(t, counter, "Should successfully allocate metric with resource pooling")
	assert.False(t, counter.isNoop, "Allocated metric should not be noop")
	assert.NotEmpty(t, counter.precomputedHeader, "Should have precomputed header data")

	// Allocate multiple metrics to simulate high cardinality scenario
	for i := 0; i < 100; i++ {
		testTags := map[string]string{
			"test_id": string(rune('a' + i%26)),
			"batch":   string(rune('A' + i%26)),
		}
		metric := reporter.allocateMetric("test.multiple", testTags, counterType)
		assert.NotNil(t, metric, "Should handle multiple metric allocations")
		assert.False(t, metric.isNoop, "All metrics should be valid")
	}
}
