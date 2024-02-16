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
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	numInternalMetrics = 4
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
	expected := 3.0
	assert.True(t, allocs <= expected, "the cached tagged scopes should allocate at most %.0f allocations, but did allocate %.0f", expected, allocs)
}

func TestVerifyOmitCardinalityMetricsTags(t *testing.T) {
	r := newTestStatsReporter()
	_, closer := NewRootScope(ScopeOptions{
		Reporter:               r,
		OmitCardinalityMetrics: false,
		CardinalityMetricsTags: map[string]string{
			"cardinality_tag_key": "cardinality_tag_value",
		},
	}, 0)
	wantOmitCardinalityMetricsTags := map[string]string{
		"cardinality_tag_key": "cardinality_tag_value",
		"version":             Version,
		"host":                "global",
		"instance":            "global",
	}

	r.gg.Add(numInternalMetrics)
	closer.Close()
	r.WaitAll()

	assert.NotNil(t, r.gauges[counterCardinalityName], "counter cardinality should not be nil")
	assert.Equal(
		t, wantOmitCardinalityMetricsTags, r.gauges[counterCardinalityName].tags, "expected tags %v, got tags %v",
		wantOmitCardinalityMetricsTags, r.gauges[counterCardinalityName].tags,
	)
}

func TestNewTestStatsReporterOneScope(t *testing.T) {
	r := newTestStatsReporter()
	root, closer := NewRootScope(ScopeOptions{Reporter: r, OmitCardinalityMetrics: false}, 0)
	s := root.(*scope)

	numFakeCounters := 3
	numFakeGauges := 5
	numFakeHistograms := 11
	numScopes := 1

	r.cg.Add(numFakeCounters)
	for c := 1; c <= numFakeCounters; c++ {
		s.Counter(fmt.Sprintf("counter-%d", c)).Inc(int64(c))
	}

	r.gg.Add(numFakeGauges + numInternalMetrics)
	for g := 1; g <= numFakeGauges; g++ {
		s.Gauge(fmt.Sprintf("gauge_%d", g)).Update(float64(g))
	}

	r.hg.Add(numFakeHistograms)
	for h := 1; h <= numFakeHistograms; h++ {
		s.Histogram(fmt.Sprintf("histogram_%d", h), MustMakeLinearValueBuckets(0, 1, 10)).RecordValue(float64(h))
	}

	closer.Close()
	r.WaitAll()

	assert.NotNil(t, r.gauges[counterCardinalityName], "counter cardinality should not be nil")
	assert.Equal(
		t, numFakeCounters, int(r.gauges[counterCardinalityName].val), "expected %d counters, got %d counters",
		numFakeCounters, r.gauges[counterCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[gaugeCardinalityName], "gauge cardinality should not be nil")
	assert.Equal(
		t, numFakeGauges, int(r.gauges[gaugeCardinalityName].val), "expected %d gauges, got %d gauges",
		numFakeGauges, r.gauges[gaugeCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[histogramCardinalityName], "histogram cardinality should not be nil")
	assert.Equal(
		t, numFakeHistograms, int(r.gauges[histogramCardinalityName].val),
		"expected %d histograms, got %d histograms", numFakeHistograms, r.gauges[histogramCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[scopeCardinalityName], "scope cardinality should not be nil")
	assert.Equal(
		t, numScopes, int(r.gauges[scopeCardinalityName].val), "expected %d scopes, got %d scopes",
		numScopes, r.gauges[scopeCardinalityName].val,
	)
}

func TestNewTestStatsReporterManyScopes(t *testing.T) {
	r := newTestStatsReporter()
	root, closer := NewRootScope(ScopeOptions{Reporter: r, OmitCardinalityMetrics: false}, 0)
	wantCounters, wantGauges, wantHistograms, wantScopes := 3, 2, 1, 2

	s := root.(*scope)
	r.cg.Add(2)
	s.Counter("counter-foo").Inc(1)
	s.Counter("counter-bar").Inc(2)
	r.gg.Add(1 + numInternalMetrics)
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

	assert.NotNil(t, r.gauges[counterCardinalityName], "counter cardinality should not be nil")
	assert.Equal(
		t, wantCounters, int(r.gauges[counterCardinalityName].val), "expected %d counters, got %d counters", wantCounters,
		r.gauges[counterCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[gaugeCardinalityName], "gauge cardinality should not be nil")
	assert.Equal(
		t, wantGauges, int(r.gauges[gaugeCardinalityName].val), "expected %d gauges, got %d gauges", wantGauges,
		r.gauges[gaugeCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[histogramCardinalityName], "histogram cardinality should not be nil")
	assert.Equal(
		t, wantHistograms, int(r.gauges[histogramCardinalityName].val), "expected %d histograms, got %d histograms",
		wantHistograms, r.gauges[histogramCardinalityName].val,
	)

	assert.NotNil(t, r.gauges[scopeCardinalityName], "scope cardinality should not be nil")
	assert.Equal(
		t, wantScopes, int(r.gauges[scopeCardinalityName].val), "expected %d scopes, got %d scopes",
		wantScopes, r.gauges[scopeCardinalityName].val,
	)
}

func TestForEachScopeConcurrent(t *testing.T) {
	var (
		root = newRootScope(ScopeOptions{Prefix: "", Tags: nil}, 0)
		quit = make(chan struct{})
		done = make(chan struct{})
	)

	go func() {
		defer close(done)
		for {
			select {
			case <-quit:
				return
			default:
				hello := root.Tagged(map[string]string{"a": "b"}).Counter("hello")
				hello.Inc(1)
			}
		}
	}()

	var c Counter = nil
	for {
		// Keep poking at the subscopes until the counter is written.
		root.registry.ForEachScope(
			func(ss *scope) {
				ss.cm.RLock()
				if ss.counters["hello"] != nil {
					c = ss.counters["hello"]
				}
				ss.cm.RUnlock()
			},
		)
		if c != nil {
			quit <- struct{}{}
			break
		}
	}

	<-done
}

func TestCachedReporterInternalMetricsAlloc(t *testing.T) {
	tests := []struct {
		name                   string
		omitCardinalityMetrics bool
		wantGauges             int
	}{
		{
			name:                   "omit metrics",
			omitCardinalityMetrics: true,
			wantGauges:             1,
		},
		{
			name:                   "include metrics",
			omitCardinalityMetrics: false,
			wantGauges:             1 + numInternalMetrics,
		},
	}

	for _, tt := range tests {
		r := newTestStatsReporter()
		root, closer := NewRootScope(ScopeOptions{CachedReporter: r, OmitCardinalityMetrics: tt.omitCardinalityMetrics}, 0)
		s := root.(*scope)

		r.gg.Add(tt.wantGauges)
		s.Gauge("gauge-foo").Update(3)

		closer.Close()
		r.WaitAll()

		assert.Equal(
			t, tt.wantGauges, len(r.gauges), "%n: expected %d gauges, got %d gauges", tt.name, tt.wantGauges,
			len(r.gauges),
		)
	}
}
