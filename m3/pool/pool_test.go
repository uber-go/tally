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

package pool

// Copied from code.uber.internal:go-common.git at version 2581320e78e1574e31e581fb32498c19c40acd66

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetReleaseDestroy(t *testing.T) {
	n := int32(0)

	alloc := func() (interface{}, error) {
		return atomic.AddInt32(&n, 1), nil
	}

	p, err := NewStandardObjectPool(5, alloc, nil)
	require.NoError(t, err)

	// First 5 should be fine
	var returned []int32
	for i := 0; i < 5; i++ {
		returned = append(returned, p.Get().(int32))
	}

	assert.Equal(t, []int32{1, 2, 3, 4, 5}, returned)

	// Next should block until one of the first 5 are released
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		returned = append(returned, p.Get().(int32))
		wg.Done()
	}()

	// Let the goroutine kick
	time.Sleep(time.Millisecond * 100)

	// Should still be blocked in pool
	assert.Equal(t, []int32{1, 2, 3, 4, 5}, returned)

	// Return #3 to the pool and wait for goroutine to complete
	p.Release(returned[2])

	wg.Wait()
	assert.Equal(t, []int32{1, 2, 3, 4, 5, 3}, returned)

	// Destroy #4, next call should alloc a new value
	p.Destroy(returned[3])
	returned = append(returned, p.Get().(int32))
	assert.Equal(t, []int32{1, 2, 3, 4, 5, 3, 6}, returned)
}

func TestGetWithTimeout(t *testing.T) {
	n := int32(0)

	alloc := func() (interface{}, error) {
		return atomic.AddInt32(&n, 1), nil
	}

	p, err := NewStandardObjectPool(5, alloc, nil)
	require.NoError(t, err)

	// First 5 should be fine
	for i := 0; i < 5; i++ {
		p.Get()
	}

	// Next should block until timeout, and not return a value
	deadline := time.Now().Add(time.Millisecond * 100)
	o := p.GetWithDeadline(deadline)
	assert.Nil(t, o)
	assert.True(t, time.Now().After(deadline))

	// Make sure we properly handle deadlines in the past
	o = p.GetWithDeadline(deadline)
	assert.Nil(t, o)

	// But if there is an object available, we'll return it even if the deadline is in the past
	p.Release(int32(1))
	o = p.GetWithDeadline(deadline)
	assert.EqualValues(t, 1, o)
}

func TestTestOnRelease(t *testing.T) {
	n := int32(0)
	alloc := func() (interface{}, error) {
		return atomic.AddInt32(&n, 1), nil
	}

	// TestOnRelease should be asynchronous - tested by virtue of the test being blocked
	// by a wait group triggered after the initial release
	var wgTestStarted sync.WaitGroup
	wgTestStarted.Add(1)

	var wgAllowTest sync.WaitGroup
	wgAllowTest.Add(1)

	testOnRelease := func(o interface{}) bool {
		wgTestStarted.Done()
		wgAllowTest.Wait()
		return false // always fail and realloc
	}

	p, err := NewStandardObjectPool(1, alloc, &StandardObjectPoolOptions{
		TestOnRelease: testOnRelease,
	})
	require.NoError(t, err)

	o := p.Get()
	require.EqualValues(t, 1, o)

	// Release, test function should fire, opening wgTestStarted and blocking on wgAllowTest
	p.Release(o)
	wgTestStarted.Wait()
	wgAllowTest.Done()

	// Test should have failed forcing a new allocation
	o = p.Get()
	require.EqualValues(t, 2, o)
}

func TestTestOnGet(t *testing.T) {
	n := int32(0)
	alloc := func() (interface{}, error) {
		return atomic.AddInt32(&n, 1), nil
	}

	callsToTest := int32(0)
	testOnGet := func(o interface{}) bool {
		// Fail the second and third tests
		tests := atomic.AddInt32(&callsToTest, 1)
		return tests != 2 && tests != 3
	}

	p, err := NewStandardObjectPool(1, alloc, &StandardObjectPoolOptions{
		TestOnGet: testOnGet,
	})
	require.NoError(t, err)

	// test on get should be synchronous - confirmed by ensuring that the tested bool is triggered
	// after the call to Get()
	o := p.Get()
	assert.EqualValues(t, 1, o)
	assert.EqualValues(t, 1, callsToTest)
	p.Release(o)

	// test on get will now fail, and we should go into a reallocation loop
	o = p.Get()

	// Should have required 2 allocations - test 2 (on value 1) failed
	// forcing a realloc, and test 3 (on value 2) failed forcing another
	// realloc, resulting in a final value of 3
	assert.EqualValues(t, 3, o)
	assert.EqualValues(t, 4, callsToTest)
}

func TestGetWithAlloc(t *testing.T) {
	n := int32(0)
	alloc := func() (interface{}, error) {
		return atomic.AddInt32(&n, 1), nil
	}

	p, err := NewStandardObjectPool(1, alloc, nil)
	require.NoError(t, err)

	o, err := p.GetOrAlloc()

	require.NoError(t, err)
	assert.EqualValues(t, 1, o)

	x, err := p.GetOrAlloc()
	require.NoError(t, err)
	assert.EqualValues(t, 2, x)

	p.Release(o)
	p.Release(x)
}

func TestGetImmediate(t *testing.T) {
	alloc := func() (interface{}, error) {
		return true, nil
	}

	p, err := NewStandardObjectPool(1, alloc, nil)
	require.NoError(t, err)

	assert.Equal(t, true, p.GetImmediate(), "")
	assert.Nil(t, p.GetImmediate(), "")
	p.Release(true)
	assert.Equal(t, true, p.GetImmediate(), "")
}
