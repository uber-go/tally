// Copyright (c) 2023 Uber Technologies, Inc.
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

package tally_test

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally/v4"
	"github.com/uber-go/tally/v4/tallymock"
	"go.uber.org/atomic"
)

func TestTestScopesNotPruned(t *testing.T) {
	var (
		root     = tally.NewTestScope("", nil)
		subscope = root.SubScope("foo")
		counter  = subscope.Counter("bar")
	)

	counter.Inc(123)

	closer, ok := subscope.(io.Closer)
	require.True(t, ok)
	require.NoError(t, closer.Close())

	subscope = root.SubScope("foo")
	counter = subscope.Counter("bar")
	counter.Inc(123)

	var (
		snapshot = root.Snapshot()
		counters = snapshot.Counters()
	)
	require.Len(t, counters, 1)
	require.Len(t, snapshot.Gauges(), 0)
	require.Len(t, snapshot.Timers(), 0)
	require.Len(t, snapshot.Histograms(), 0)

	val, ok := counters["foo.bar+"]
	require.True(t, ok)
	require.Equal(t, "foo.bar", val.Name())
	require.EqualValues(t, 246, val.Value())
}

func TestNoDefunctSubscopes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	var (
		tags = map[string]string{
			"hello": "world",
		}
		mockreporter = tallymock.NewMockStatsReporter(ctrl)
		ready        = make(chan struct{})
		closed       atomic.Bool
		wg           sync.WaitGroup
	)
	wg.Add(2)

	mockreporter.EXPECT().
		ReportCounter("a", gomock.Any(), int64(123)).
		Do(func(_ string, _ map[string]string, _ int64) {
			wg.Done()
		}).
		Times(1)
	mockreporter.EXPECT().
		ReportCounter("b", gomock.Any(), int64(456)).
		Do(func(_ string, _ map[string]string, _ int64) {
			wg.Done()
		}).
		Times(1)

	// Use flushing as a signal to determine if/when a closed scope
	// would be removed from the registry's cache.
	mockreporter.EXPECT().
		Flush().
		Do(func() {
			// Don't unblock the ready channel until we've explicitly
			// closed the scope.
			if !closed.Load() {
				return
			}

			select {
			case <-ready:
			default:
				close(ready)
			}
		}).
		MinTimes(1)

	root, _ := tally.NewRootScope(tally.ScopeOptions{
		Reporter:               mockreporter,
		OmitCardinalityMetrics: true,
	}, time.Millisecond)

	subscope := root.Tagged(tags)
	requireClose(t, subscope)
	subscope = root.Tagged(tags)

	// Signal and wait for the next flush to ensure that subscope can
	// be a closed scope.
	closed.Store(true)
	<-ready

	// Use the maybe-closed subscope for counter A.
	subscope.Counter("a").Inc(123)

	// Guarantee that counter B will not use a closed subscope.
	subscope = root.Tagged(tags)
	subscope.Counter("b").Inc(456)

	requireClose(t, root)
	wg.Wait()
}

func requireClose(t *testing.T, scope tally.Scope) {
	x, ok := scope.(io.Closer)
	require.True(t, ok)
	require.NoError(t, x.Close())
}
