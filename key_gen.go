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
	"bytes"
	"sort"
	"sync"
	"unsafe"
)

const (
	prefixSplitter  = '+'
	keyPairSplitter = ','
	keyNameSplitter = '='
)

var (
	nilString  = ""
	bufferPool = &sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(make([]byte, 0, 1024))
		},
	}
	stringsPool = sync.Pool{
		New: func() interface{} {
			ss := make([]string, 0, 32)
			return &ss
		},
	}
)

// key helps to reduce allocations when the Tagged scope is already cached
// key is a struct to reduce allocation that would happen with interface as interface escapes to heap
type key struct {
	bufferPool *sync.Pool
	buffer     *bytes.Buffer
}

func (k key) CastAsString() string {
	b := k.buffer.Bytes()
	return *(*string)(unsafe.Pointer(&b))
}

func (k key) CopyAsString() string {
	return k.buffer.String()
}

func (k key) Release() {
	k.buffer.Reset()
	k.bufferPool.Put(k.buffer)
}

// KeyForStringMap generates a unique key for a map string set combination.
func KeyForStringMap(
	stringMap map[string]string,
) string {
	return KeyForPrefixedStringMap(nilString, stringMap)
}

// KeyForPrefixedStringMap generates a unique key for a
// a prefix and a map string set combination.
func KeyForPrefixedStringMap(
	prefix string,
	stringMap map[string]string,
) string {
	return keyForPrefixedStringMaps(prefix, stringMap)
}

func keyForPrefixedStringMapsAsKey(prefix string, maps ...map[string]string) key {
	keys := stringsPool.Get().(*[]string)
	for _, m := range maps {
		for k := range m {
			*keys = append(*keys, k)
		}
	}
	// 1 allocation as sort.Interface escapes to heap
	sort.Strings(*keys)

	buf := bufferPool.Get().(*bytes.Buffer)

	if prefix != nilString {
		buf.WriteString(prefix)
		buf.WriteByte(prefixSplitter)
	}

	var lastKey string // last key written to the buffer
	for _, k := range *keys {
		if len(lastKey) > 0 {
			if k == lastKey {
				// Already wrote this key.
				continue
			}
			buf.WriteByte(keyPairSplitter)
		}
		lastKey = k

		buf.WriteString(k)
		buf.WriteByte(keyNameSplitter)

		// Find and write the value for this key. Rightmost map takes
		// precedence.
		for j := len(maps) - 1; j >= 0; j-- {
			if v, ok := maps[j][k]; ok {
				buf.WriteString(v)
				break
			}
		}
	}

	release(keys)
	return key{
		bufferPool: bufferPool,
		buffer:     buf,
	}
}

// keyForPrefixedStringMaps generates a unique key for a prefix and a series
// of maps containing tags.
//
// If a key occurs in multiple maps, keys on the right take precedence.
func keyForPrefixedStringMaps(prefix string, maps ...map[string]string) string {
	key := keyForPrefixedStringMapsAsKey(prefix, maps...)
	defer key.Release()
	return key.CopyAsString()
}

func release(strs *[]string) {
	for i := range *strs {
		(*strs)[i] = nilString
	}
	*strs = (*strs)[:0]
	stringsPool.Put(strs)
}
