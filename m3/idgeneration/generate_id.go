package idgeneration

// Copied from code.uber.internal:go-common.git at version 139e3b5b4b4b775ff9ed8abb2a9f31b7bd1aad58

import (
	"bytes"
	"sort"

	"code.uber.internal/rt/go-common-pool.git"

	"github.com/uber-common/bark"
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
