// Copyright (c) 2022 Uber Technologies, Inc.
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
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	numInternalMetrics = 3
)

func TestVerifyCachedTaggedScopesAlloc(t *testing.T) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
	}, 0)

	allocs := testing.AllocsPerRun(1000, func() {
		root.Tagged(map[string]string{
			"foo": "bar",
			"baz": "qux",
			"qux": "quux",
		})
	})
	expected := 2.0
	assert.True(t, allocs <= expected, "the cached tagged scopes should allocate at most %.0f allocations, but did allocate %.0f", expected, allocs)
}

func TestNewTestStatsReporterOneScope(t *testing.T) {
	r := newTestStatsReporter()
	root, closer := NewRootScope(ScopeOptions{Reporter: r, skipInternalMetrics: false}, 0)
	s := root.(*scope)

	numFakeCounters := 3
	numFakeGauges := 5
	numFakeHistograms := 11

	r.cg.Add(numFakeCounters + numInternalMetrics)
	for c := 1; c <= numFakeCounters; c++ {
		s.Counter(fmt.Sprintf("counter-%d", c)).Inc(int64(c))
	}

	r.gg.Add(numFakeGauges)
	for g := 1; g <= numFakeGauges; g++ {
		s.Gauge(fmt.Sprintf("gauge_%d", g)).Update(float64(g))
	}

	r.hg.Add(numFakeHistograms)
	for h := 1; h <= numFakeHistograms; h++ {
		s.Histogram(fmt.Sprintf("histogram_%d", h), MustMakeLinearValueBuckets(0, 1, 10)).RecordValue(float64(h))
	}

	closer.Close()
	r.WaitAll()

	assert.NotNil(t, r.counters[counterCardinalityName], "counter cardinality should not be nil")
	assert.Equal(
		t, int64(numFakeCounters), r.counters[counterCardinalityName].val, "expected %d counters, got %d counters",
		numFakeCounters, r.counters[counterCardinalityName].val,
	)

	assert.NotNil(t, r.counters[gaugeCardinalityName], "gauge cardinality should not be nil")
	assert.Equal(
		t, int64(numFakeGauges), r.counters[gaugeCardinalityName].val, "expected %d gauges, got %d gauges",
		numFakeGauges, r.counters[gaugeCardinalityName].val,
	)

	assert.NotNil(t, r.counters[histogramCardinalityName], "histogram cardinality should not be nil")
	assert.Equal(
		t, int64(numFakeHistograms), r.counters[histogramCardinalityName].val,
		"expected %d histograms, got %d histograms", numFakeHistograms, r.counters[histogramCardinalityName].val,
	)
}

func TestNewTestStatsReporterManyScopes(t *testing.T) {
	r := newTestStatsReporter()
	root, closer := NewRootScope(ScopeOptions{Reporter: r, skipInternalMetrics: false}, 0)
	wantCounters, wantGauges, wantHistograms := int64(3), int64(2), int64(1)

	s := root.(*scope)
	r.cg.Add(2 + numInternalMetrics)
	s.Counter("counter-foo").Inc(1)
	s.Counter("counter-bar").Inc(2)
	r.gg.Add(1)
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

	assert.NotNil(t, r.counters[counterCardinalityName], "counter cardinality should not be nil")
	assert.Equal(
		t, wantCounters, r.counters[counterCardinalityName].val, "expected %d counters, got %d counters", wantCounters,
		r.counters[counterCardinalityName].val,
	)

	assert.NotNil(t, r.counters[gaugeCardinalityName], "gauge cardinality should not be nil")
	assert.Equal(
		t, wantGauges, r.counters[gaugeCardinalityName].val, "expected %d counters, got %d gauges", wantGauges,
		r.counters[gaugeCardinalityName].val,
	)

	assert.NotNil(t, r.counters[histogramCardinalityName], "histogram cardinality should not be nil")
	assert.Equal(
		t, wantHistograms, r.counters[histogramCardinalityName].val, "expected %d counters, got %d histograms",
		wantHistograms, r.counters[histogramCardinalityName].val,
	)
}
