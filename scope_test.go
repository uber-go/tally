// Copyright (c) 2016 Uber Technologies, Inc.
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWriteOnce(t *testing.T) {
	r := NewTestStatsReporter()
	scope := NewScope("", r)
	scope.Counter("bar").Inc(1)
	scope.Gauge("zed").Update(1)
	scope.Timer("ticky").Record(time.Millisecond * 175)

	scope.report(r)
	assert.EqualValues(t, 1, r.counters["bar"])
	assert.EqualValues(t, 1, r.gauges["zed"])
	assert.EqualValues(t, time.Millisecond*175, r.timers["ticky"])

	r = NewTestStatsReporter()
	scope.report(r)
	assert.EqualValues(t, 0, r.counters["bar"])
	assert.EqualValues(t, 0, r.gauges["zed"])
	assert.EqualValues(t, 0, r.timers["ticky"])
}

func TestRootScopeWithoutPrefix(t *testing.T) {
	r := NewTestStatsReporter()
	scope := NewScope("", r)
	scope.Counter("bar").Inc(1)
	scope.Counter("bar").Inc(20)
	scope.Gauge("zed").Update(1)
	scope.Timer("blork").Record(time.Millisecond * 175)

	scope.report(r)
	assert.EqualValues(t, 21, r.counters["bar"])
	assert.EqualValues(t, 1, r.gauges["zed"])
	assert.EqualValues(t, time.Millisecond*175, r.timers["blork"])
}

func TestRootScopeWithPrefix(t *testing.T) {
	r := NewTestStatsReporter()
	scope := NewScope("foo", r)
	scope.Counter("bar").Inc(1)
	scope.Counter("bar").Inc(20)
	scope.Gauge("zed").Update(1)
	scope.Timer("blork").Record(time.Millisecond * 175)

	scope.report(r)
	assert.EqualValues(t, 21, r.counters["foo.bar"])
	assert.EqualValues(t, 1, r.gauges["foo.zed"])
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.blork"])
}

func TestSubScope(t *testing.T) {
	r := NewTestStatsReporter()
	scope := NewScope("foo", r).SubScope("mork")
	scope.Counter("bar").Inc(1)
	scope.Counter("bar").Inc(20)
	scope.Gauge("zed").Update(1)
	scope.Timer("blork").Record(time.Millisecond * 175)

	scope.report(r)
	assert.EqualValues(t, 21, r.counters["foo.mork.bar"])
	assert.EqualValues(t, 1, r.gauges["foo.mork.zed"])
	assert.EqualValues(t, time.Millisecond*175, r.timers["foo.mork.blork"])
}

// func TestTaggedScope(t *testing.T) {
// 	l1Tags := map[string]string{
// 		"my-key":    "my-val",
// 		"other-key": "to-replace",
// 	}

// 	l2Tags := map[string]string{
// 		"other-key": "override",
// 	}

// 	tags := map[string]string{
// 		"my-key":    "my-val",
// 		"other-key": "override",
// 	}

// 	stats := NewTestStatsReporter()
// 	scope := NewScope("foo", stats).Tagged(l1Tags).Tagged(l2Tags)
// 	scope.IncCounter("bar", 1)
// 	scope.IncCounter("bar", 20)
// 	scope.UpdateGauge("zed", 1)
// 	scope.RecordTimer("blork", time.Millisecond*175)

// 	assert.EqualValues(t, 21, stats.Counter("foo.bar", tags))
// 	assert.EqualValues(t, 1, stats.Gauge("foo.zed", tags))
// 	assert.EqualValues(t, 175.0, stats.Timer("foo.blork", tags).Quantile(0.5))
// 	assert.Nil(t, stats.Timer("foo.blork", nil))
// }

// func TestScopeCall(t *testing.T) {
// 	clock := clock.NewMock()
// 	stats := NewTestStatsReporter()
// 	scope := NewScope("", stats)

// 	call := scope.StartCall("my-call", clock)
// 	clock.Add(time.Millisecond * 10)
// 	call(true)

// 	call = scope.StartCall("my-call", clock)
// 	clock.Add(time.Second * 1000)
// 	call(false) // did not succeed

// 	assert.EqualValues(t, 1, stats.Counter("my-call.success", nil))
// 	assert.EqualValues(t, 1, stats.Counter("my-call.errors", nil))
// 	assert.EqualValues(t, 10, stats.Timer("my-call", nil).Quantile(0.999)) // only success is tracked
// }
