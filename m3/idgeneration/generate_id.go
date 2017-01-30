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

package idgeneration

// Copied from code.uber.internal:go-common.git at version 139e3b5b4b4b775ff9ed8abb2a9f31b7bd1aad58

import (
	"bytes"
	"sort"

	"github.com/uber-common/bark"
	"github.com/uber-go/tally/m3/pool"
)

const (
	// nameSplitter is the separator for different compoenents of the path
	nameSplitter = '+'
	// tagPairSplitter is the separator for tag pairs
	tagPairSplitter = ','
	// tagNameSplitter is the separator for tag name and values
	tagNameSplitter = '='
)

var sPool = newScopePool(1000, 1000, 10)

type scopePool struct {
	bytesPool   pool.ObjectPool
	stringsPool pool.ObjectPool
}

// Get generates a unique string id for a name and tag set combination
func Get(name string, tags bark.Tags) string {
	if len(tags) == 0 {
		return name
	}

	s, _ := sPool.stringsPool.GetOrAlloc()
	sortedKeys := s.([]string)
	for k := range tags {
		sortedKeys = append(sortedKeys, k)
	}

	sort.Strings(sortedKeys)
	b, _ := sPool.bytesPool.GetOrAlloc()
	buf := b.(*bytes.Buffer)
	buf.WriteString(name)
	buf.WriteByte(nameSplitter)
	i := 0
	for i = 0; i < len(sortedKeys)-1; i++ {
		buf.WriteString(sortedKeys[i])
		buf.WriteByte(tagNameSplitter)
		buf.WriteString(tags[sortedKeys[i]])
		buf.WriteByte(tagPairSplitter)
	}

	buf.WriteString(sortedKeys[i])
	buf.WriteByte(tagNameSplitter)
	buf.WriteString(tags[sortedKeys[i]])

	id := buf.String()
	sPool.release(buf, sortedKeys)
	return id
}

func newScopePool(size, blen, slen int) *scopePool {
	b, _ := pool.NewStandardObjectPool(size, func() (interface{}, error) {
		return bytes.NewBuffer(make([]byte, 0, blen)), nil
	}, nil)

	s, _ := pool.NewStandardObjectPool(size, func() (interface{}, error) {
		return make([]string, 0, slen), nil
	}, nil)

	return &scopePool{
		bytesPool:   b,
		stringsPool: s,
	}
}

func (s *scopePool) release(b *bytes.Buffer, strs []string) {
	b.Reset()
	s.bytesPool.Release(b)
	s.stringsPool.Release(strs[:0])
}
