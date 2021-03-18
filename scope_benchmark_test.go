// Copyright (c) 2021 Uber Technologies, Inc.
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
	"testing"
	"time"
)

func BenchmarkNameGeneration(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
	}, 0)
	s := root.(*scope)
	for n := 0; n < b.N; n++ {
		s.fullyQualifiedName("take.me.to")
	}
}

func BenchmarkCounterAllocation(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
	}, 0)
	s := root.(*scope)

	ids := make([]string, 0, b.N)
	for i := 0; i < b.N; i++ {
		ids = append(ids, fmt.Sprintf("take.me.to.%d", i))
	}
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		s.Counter(ids[n])
	}
}

func BenchmarkSanitizedCounterAllocation(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:          "funkytown",
		Reporter:        NullStatsReporter,
		SanitizeOptions: &alphanumericSanitizerOpts,
	}, 0)
	s := root.(*scope)

	ids := make([]string, 0, b.N)
	for i := 0; i < b.N; i++ {
		ids = append(ids, fmt.Sprintf("take.me.to.%d", i))
	}
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		s.Counter(ids[n])
	}
}

func BenchmarkNameGenerationTagged(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix: "funkytown",
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
		Reporter: NullStatsReporter,
	}, 0)
	s := root.(*scope)
	for n := 0; n < b.N; n++ {
		s.fullyQualifiedName("take.me.to")
	}
}

func BenchmarkScopeTaggedCachedSubscopes(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
	}, 0)
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		root.Tagged(map[string]string{
			"foo": "bar",
			"baz": "qux",
			"qux": "quux",
		})
	}
}

func BenchmarkScopeTaggedNoCachedSubscopes(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
	}, 0)

	values := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		values[i] = strconv.Itoa(i)
	}

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		root.Tagged(map[string]string{
			"foo": values[n],
			"baz": values[n],
			"qux": values[n],
		})
	}
}

func BenchmarkNameGenerationNoPrefix(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Reporter: NullStatsReporter,
	}, 0)
	s := root.(*scope)
	for n := 0; n < b.N; n++ {
		s.fullyQualifiedName("im.all.alone")
	}
}

func BenchmarkHistogramAllocation(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Reporter: NullStatsReporter,
	}, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root.Histogram("foo"+strconv.Itoa(i), DefaultBuckets)
	}
}

func BenchmarkHistogramExisting(b *testing.B) {
	root, _ := NewRootScope(ScopeOptions{
		Reporter: NullStatsReporter,
	}, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root.Histogram("foo", DefaultBuckets)
	}
}

func benchmarkScopeReportingN(b *testing.B, numElems int) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:          "funkytown",
		CachedReporter:  noopCachedReporter{},
		SanitizeOptions: &alphanumericSanitizerOpts,
	}, 0)
	s := root.(*scope)

	ids := make([]string, 0, numElems)
	for i := 0; i < numElems; i++ {
		id := fmt.Sprintf("take.me.to.%d", i)
		ids = append(ids, id)
		s.Counter(id)
	}
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		s.cachedReport()
	}
}

func BenchmarkScopeReporting(b *testing.B) {
	for i := 1; i <= 1000000; i *= 10 {
		size := fmt.Sprintf("size%d", i)
		b.Run(size, func(b *testing.B) {
			benchmarkScopeReportingN(b, i)
		})
	}
}

type noopStat struct{}

func (s noopStat) ReportCount(value int64)            {}
func (s noopStat) ReportGauge(value float64)          {}
func (s noopStat) ReportTimer(interval time.Duration) {}
func (s noopStat) ValueBucket(bucketLowerBound, bucketUpperBound float64) CachedHistogramBucket {
	return s
}
func (s noopStat) DurationBucket(bucketLowerBound, bucketUpperBound time.Duration) CachedHistogramBucket {
	return s
}
func (s noopStat) ReportSamples(value int64) {}

type noopCachedReporter struct{}

func (n noopCachedReporter) Capabilities() Capabilities {
	return n
}

func (n noopCachedReporter) Reporting() bool { return true }
func (n noopCachedReporter) Tagging() bool   { return true }
func (n noopCachedReporter) Flush()          {}

func (n noopCachedReporter) ReportCounter(name string, tags map[string]string, value int64) {}
func (n noopCachedReporter) ReportGauge(name string, tags map[string]string, value float64) {}

func (n noopCachedReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
}

func (n noopCachedReporter) ReportHistogramValueSamples(name string, tags map[string]string, buckets Buckets, bucketLowerBound float64, bucketUpperBound float64, samples int64) {
}
func (n noopCachedReporter) ReportHistogramDurationSamples(name string, tags map[string]string, buckets Buckets, bucketLowerBound time.Duration, bucketUpperBound time.Duration, samples int64) {
}

func (n noopCachedReporter) AllocateCounter(name string, tags map[string]string) CachedCount {
	return noopStat{}
}

func (n noopCachedReporter) DeallocateCounter(CachedCount) {}

func (n noopCachedReporter) AllocateGauge(name string, tags map[string]string) CachedGauge {
	return noopStat{}
}

func (n noopCachedReporter) DeallocateGauge(CachedGauge) {}

func (n noopCachedReporter) AllocateTimer(name string, tags map[string]string) CachedTimer {
	return noopStat{}
}

func (n noopCachedReporter) DeallocateTimer(CachedTimer) {}

func (n noopCachedReporter) AllocateHistogram(name string, tags map[string]string, buckets Buckets) CachedHistogram {
	return noopStat{}
}

func (n noopCachedReporter) DeallocateHistogram(CachedHistogram) {}
