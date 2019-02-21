// Copyright (c) 2019 Uber Technologies, Inc.
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
