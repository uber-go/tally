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
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
)

func TestStringInterning(t *testing.T) {
	// Reset stats before test
	ResetStringInterningStats()

	str1 := "test.metric.name"
	str2 := "test.metric.name"

	interned1 := InternString(str1)
	interned2 := InternString(str2)

	// Test that same content gives same pointer
	assert.True(t, unsafe.Pointer(&interned1) != unsafe.Pointer(&interned2), "Pointers to variables should be different")
	assert.Equal(t, interned1, interned2, "Interned strings should have same content")

	// Verify the actual string data pointers are the same
	h1 := (*reflect.StringHeader)(unsafe.Pointer(&interned1))
	h2 := (*reflect.StringHeader)(unsafe.Pointer(&interned2))
	assert.True(t, h1.Data == h2.Data, "Interned strings should have same underlying data pointer")
}

func TestStringInterningDifferentStrings(t *testing.T) {
	ResetStringInterningStats()

	str1 := "metric.one"
	str2 := "metric.two"

	interned1 := InternString(str1)
	interned2 := InternString(str2)

	// Different strings should have different data pointers
	h1 := (*reflect.StringHeader)(unsafe.Pointer(&interned1))
	h2 := (*reflect.StringHeader)(unsafe.Pointer(&interned2))
	assert.False(t, h1.Data == h2.Data, "Different strings should have different data pointers")
	assert.NotEqual(t, interned1, interned2, "Different strings should have different content")
}

func TestStringInterningLongStrings(t *testing.T) {
	ResetStringInterningStats()

	// Create a string longer than MaxInternedStringLength
	longStr := make([]byte, MaxInternedStringLength+100)
	for i := range longStr {
		longStr[i] = 'a'
	}

	str1 := string(longStr)
	str2 := string(longStr)

	interned1 := InternString(str1)
	interned2 := InternString(str2)

	// Long strings should not be interned, so they should have different data pointers
	h1 := (*reflect.StringHeader)(unsafe.Pointer(&interned1))
	h2 := (*reflect.StringHeader)(unsafe.Pointer(&interned2))
	assert.False(t, h1.Data == h2.Data, "Long strings should not be interned")
}

func TestStringInterningStats(t *testing.T) {
	ResetStringInterningStats()

	str1 := "stats.test.metric"
	str2 := "stats.test.metric"

	// First call should be a miss
	InternString(str1)
	stats := GetStringInterningStats()
	assert.Equal(t, int64(1), stats.Misses, "First intern should be a miss")
	assert.Equal(t, int64(0), stats.Hits, "First intern should not be a hit")

	// Second call should be a hit
	InternString(str2)
	stats = GetStringInterningStats()
	assert.Equal(t, int64(1), stats.Misses, "Should still be 1 miss")
	assert.Equal(t, int64(1), stats.Hits, "Second intern should be a hit")
}

func TestStringInterningConcurrency(t *testing.T) {
	ResetStringInterningStats()

	const numGoroutines = 50
	const numOperations = 100
	const testString = "concurrent.test.metric"

	var wg sync.WaitGroup
	results := make([]string, numGoroutines*numOperations)

	// Launch goroutines that all intern the same string
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				interned := InternString(testString)
				idx := int64(goroutineID*numOperations + j)
				results[idx] = interned
			}
		}(i)
	}

	wg.Wait()

	// Verify all results have the same data pointer
	h1 := (*reflect.StringHeader)(unsafe.Pointer(&results[0]))
	for i := 1; i < len(results); i++ {
		hi := (*reflect.StringHeader)(unsafe.Pointer(&results[i]))
		assert.True(t, h1.Data == hi.Data,
			"Same string content should have same data pointer across goroutines")
	}

	// Verify stats
	stats := GetStringInterningStats()
	assert.Equal(t, int64(1), stats.Misses, "Should have exactly 1 miss for the first intern")
	assert.Equal(t, int64(numGoroutines*numOperations-1), stats.Hits,
		"Should have hits for all subsequent interns")
}

func TestBuildInternedMetricName(t *testing.T) {
	ResetStringInterningStats()

	prefix := "app"
	separator := "."
	name := "requests"

	result1 := BuildInternedMetricName(prefix, separator, name)
	result2 := BuildInternedMetricName(prefix, separator, name)

	assert.Equal(t, "app.requests", result1)
	assert.Equal(t, "app.requests", result2)

	// Should have same data pointer
	h1 := (*reflect.StringHeader)(unsafe.Pointer(&result1))
	h2 := (*reflect.StringHeader)(unsafe.Pointer(&result2))
	assert.True(t, h1.Data == h2.Data, "Built metric names should be interned")
}

func TestBuildInternedMetricNameEmptyPrefix(t *testing.T) {
	ResetStringInterningStats()

	name := "requests"

	result1 := BuildInternedMetricName("", ".", name)
	result2 := InternString(name)

	assert.Equal(t, name, result1)

	// Should have same data pointer as direct interning
	h1 := (*reflect.StringHeader)(unsafe.Pointer(&result1))
	h2 := (*reflect.StringHeader)(unsafe.Pointer(&result2))
	assert.True(t, h1.Data == h2.Data, "Should intern just the name when prefix is empty")
}

func TestBuildInternedMetricID(t *testing.T) {
	ResetStringInterningStats()

	metricName := "requests"
	tags := map[string]string{
		"method": "GET",
		"status": "200",
	}

	result1 := BuildInternedMetricID(metricName, tags)
	result2 := BuildInternedMetricID(metricName, tags)

	assert.Equal(t, result1, result2)

	// Should have same data pointer
	h1 := (*reflect.StringHeader)(unsafe.Pointer(&result1))
	h2 := (*reflect.StringHeader)(unsafe.Pointer(&result2))
	assert.True(t, h1.Data == h2.Data, "Built metric IDs should be interned")
}

func TestBuildInternedMetricIDEmptyTags(t *testing.T) {
	ResetStringInterningStats()

	metricName := "requests"
	emptyTags := map[string]string{}

	result1 := BuildInternedMetricID(metricName, emptyTags)
	result2 := InternString(metricName)

	assert.Equal(t, metricName, result1)

	// Should have same data pointer as direct interning
	h1 := (*reflect.StringHeader)(unsafe.Pointer(&result1))
	h2 := (*reflect.StringHeader)(unsafe.Pointer(&result2))
	assert.True(t, h1.Data == h2.Data, "Should intern just the metric name when tags are empty")
}

func TestStringInterningCleanup(t *testing.T) {
	// Create a new interning system for this test to avoid interference
	system := newStringInterningSystemWithCleanup(2, true)
	defer system.Shutdown() // Properly shut down to avoid goroutine leak

	// Fill up the system beyond the limit to trigger cleanup
	for i := 0; i < InternPoolSizeLimit+100; i++ {
		system.internString(fmt.Sprintf("metric.%d", i))
	}

	// Give cleanup a chance to run
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	stats := system.GetStringInterningStats()
	// We don't assert exact numbers since cleanup is async,
	// but we verify the system is functional
	assert.True(t, stats.TotalEntries > 0, "Should have some entries after cleanup")
}

func BenchmarkStringInterning(b *testing.B) {
	const testString = "benchmark.test.metric.name"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		InternString(testString)
	}
}

func BenchmarkStringInterningConcurrent(b *testing.B) {
	const testString = "concurrent.benchmark.test.metric.name"

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			InternString(testString)
		}
	})
}

func BenchmarkStringInterningDifferentStrings(b *testing.B) {
	strings := make([]string, 1000)
	for i := range strings {
		strings[i] = fmt.Sprintf("metric.%d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		InternString(strings[i%len(strings)])
	}
}
