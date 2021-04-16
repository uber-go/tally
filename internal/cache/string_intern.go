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

package cache

import (
	"sync"
)

// StringInterner interns strings.
type StringInterner struct {
	entries map[string]string
	mtx     sync.RWMutex
}

// NewStringInterner creates a new StringInterner.
func NewStringInterner() *StringInterner {
	return &StringInterner{
		entries: make(map[string]string),
	}
}

// Intern interns s.
func (i *StringInterner) Intern(s string) string {
	i.mtx.RLock()
	x, ok := i.entries[s]
	i.mtx.RUnlock()

	if ok {
		return x
	}

	i.mtx.Lock()
	i.entries[s] = s
	i.mtx.Unlock()

	return s
}
