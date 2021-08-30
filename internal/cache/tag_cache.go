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

	"github.com/uber-go/tally/v4/internal/identity"
	m3thrift "github.com/uber-go/tally/v4/m3/thrift/v2"
)

// TagCache is an identity.Accumulator-based tag cache.
type TagCache struct {
	entries map[uint64][]m3thrift.MetricTag
	mtx     sync.RWMutex
}

// NewTagCache creates a new TagCache.
func NewTagCache() *TagCache {
	return &TagCache{
		entries: make(map[uint64][]m3thrift.MetricTag),
	}
}

// Get returns the cached value for key.
func (c *TagCache) Get(key uint64) ([]m3thrift.MetricTag, bool) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	entry, ok := c.entries[key]
	return entry, ok
}

// Set attempts to set the value of key as tslice, returning either tslice or
// the pre-existing value if found.
func (c *TagCache) Set(key uint64, tslice []m3thrift.MetricTag) []m3thrift.MetricTag {
	c.mtx.RLock()
	existing, ok := c.entries[key]
	c.mtx.RUnlock()

	if ok {
		return existing
	}

	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.entries[key] = tslice
	return tslice
}

// TagMapKey generates a new key based on tags.
func TagMapKey(tags map[string]string) uint64 {
	return identity.StringStringMap(tags)
}
