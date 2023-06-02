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
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally"
	"github.com/uber-go/tally/tallymock"
)

func TestNoDefunctSubscopes(t *testing.T) {
	var (
		tags = map[string]string{
			"hello": "world",
		}
		cases = map[string]struct {
			reportOnClose bool
			wantReportsA  int
			wantReportsB  int
		}{
			"ReportOnSubscopeClose=true": {
				reportOnClose: true,
				wantReportsA:  1,
				wantReportsB:  1,
			},
			"ReportOnSubscopeClose=false": {
				reportOnClose: false,
				wantReportsA:  0,
				wantReportsB:  1,
			},
		}
	)

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			var (
				mockreporter = tallymock.NewMockStatsReporter(ctrl)
				ready        = make(chan struct{})
				closed       atomic.Bool
				wg           sync.WaitGroup
			)
			wg.Add(tt.wantReportsA + tt.wantReportsB)

			// Expect counter A to be reported when ReportOnSubscopeClose is
			// true. Otherwise, the counter will belong to a closed scope and
			// will not be reported.
			mockreporter.EXPECT().
				ReportCounter("a", gomock.Any(), int64(123)).
				Do(func(_ string, _ map[string]string, _ int64) {
					wg.Done()
				}).
				Times(tt.wantReportsA)

			// Expect counter B to be reported regardless of whether
			// ReportOnSubscopeClose is true, because the scope is acquired
			// after the flush loop has happened (and the scope has been
			// removed from the registry's cache).
			mockreporter.EXPECT().
				ReportCounter("b", gomock.Any(), int64(456)).
				Do(func(_ string, _ map[string]string, _ int64) {
					wg.Done()
				}).
				Times(tt.wantReportsB)

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
				Reporter:              mockreporter,
				ReportOnSubscopeClose: tt.reportOnClose,
			}, time.Millisecond)

			subscope := root.Tagged(tags)
			requireClose(t, subscope)
			subscope = root.Tagged(tags)

			// Signal and wait for the next flush to ensure that subscope can
			// be a closed scope.
			closed.Store(true)
			<-ready

			// Use the maybe-closed (if not reporting on close) subscope for
			// counter A.
			subscope.Counter("a").Inc(123)

			// Guarantee that counter B will not use a closed subscope.
			subscope = root.Tagged(tags)
			subscope.Counter("b").Inc(456)

			requireClose(t, root)
			wg.Wait()
		})
	}
}

func requireClose(t *testing.T, scope tally.Scope) {
	x, ok := scope.(io.Closer)
	require.True(t, ok)
	require.NoError(t, x.Close())
}
