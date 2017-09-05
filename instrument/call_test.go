// Copyright (c) 2017 Uber Technologies, Inc.
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

package instrument

import (
	"errors"
	"testing"

	"github.com/uber-go/tally"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallSuccess(t *testing.T) {
	s := tally.NewTestScope("", nil)

	err := NewCall(s, "test_call").Exec(func() error {
		return nil
	})
	assert.Nil(t, err)

	snapshot := s.Snapshot()
	counters := snapshot.Counters()
	timers := snapshot.Timers()

	require.NotNil(t, counters["test_call+result_type=success"])
	require.NotNil(t, timers["test_call.latency+"])

	assert.Equal(t, int64(1), counters["test_call+result_type=success"].Value())
}

func TestCallFail(t *testing.T) {
	s := tally.NewTestScope("", nil)

	expected := errors.New("an error")
	err := NewCall(s, "test_call").Exec(func() error {
		return expected
	})
	assert.NotNil(t, err)
	assert.Equal(t, expected, err)

	snapshot := s.Snapshot()
	counters := snapshot.Counters()
	timers := snapshot.Timers()

	require.NotNil(t, counters["test_call+result_type=error"])
	require.NotNil(t, timers["test_call.latency+"])

	assert.Equal(t, int64(1), counters["test_call+result_type=error"].Value())
}
