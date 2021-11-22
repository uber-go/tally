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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStringSlicePool(t *testing.T) {
	size := 10
	pool := NewStringSlicePool(size)
	b := true
	init := &b
	pool.Init(func() []string {
		ss := make([]string, 0, 1)
		if *init {
			ss = append(ss, "init")
		} else {
			ss = append(ss, "adhoc")
		}
		return ss
	})
	*init = false

	// first time init is expected
	verifyPoolUsage(t, pool, size, []string{"init"}, []string{})
	// next run, empty slice is expected and adhoc should not be present
	verifyPoolUsage(t, pool, size, []string{}, nil)

	for i := 0; i < size; i++ {
		pool.Get()
	}

	// when pool is exhausted a new instance should be allocated
	assert.Equal(t, []string{"adhoc"}, pool.Get())
}

func verifyPoolUsage(t *testing.T, pool *StringSlicePool, size int, expected []string, put []string) {
	var g1, g2 sync.WaitGroup
	g1.Add(size)
	g2.Add(size)
	for i := 0; i < size; i++ {
		go func() {
			ss := pool.Get()
			assert.Equal(t, expected, ss)
			g1.Add(-1)
			g1.Wait()
			pool.Put(put)
			g2.Add(-1)
		}()
	}
	g2.Wait()
}
