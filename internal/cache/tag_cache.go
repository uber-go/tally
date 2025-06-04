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
	"time"

	"github.com/uber-go/tally/v4/internal/identity"
	m3thrift "github.com/uber-go/tally/v4/m3/thrift/v2"
)

// LRU node for the tag cache
type tagCacheNode struct {
	key        uint64
	value      []m3thrift.MetricTag
	prev       *tagCacheNode
	next       *tagCacheNode
	lastAccess int64 // Unix timestamp for TTL
}

// TagCache is an identity.Accumulator-based tag cache with LRU eviction.
type TagCache struct {
	entries    map[uint64]*tagCacheNode
	head       *tagCacheNode // Most recently used
	tail       *tagCacheNode // Least recently used
	mtx        sync.RWMutex
	maxSize    int
	ttlSeconds int64 // Time-to-live in seconds, 0 means no TTL

	// Metrics
	hits      int64
	misses    int64
	evictions int64
}

// TagCacheOptions configures the TagCache behavior
type TagCacheOptions struct {
	MaxSize    int   // Maximum number of entries (default: 10000)
	TTLSeconds int64 // TTL in seconds, 0 means no TTL (default: 3600 = 1 hour)
}

// NewTagCache creates a new TagCache with default settings.
func NewTagCache() *TagCache {
	return NewTagCacheWithOptions(TagCacheOptions{
		MaxSize:    10000, // Default size limit
		TTLSeconds: 3600,  // 1 hour default TTL
	})
}

// NewTagCacheWithOptions creates a new TagCache with specified options.
func NewTagCacheWithOptions(opts TagCacheOptions) *TagCache {
	if opts.MaxSize <= 0 {
		opts.MaxSize = 10000
	}

	// Create sentinel nodes for the doubly-linked list
	head := &tagCacheNode{}
	tail := &tagCacheNode{}
	head.next = tail
	tail.prev = head

	return &TagCache{
		entries:    make(map[uint64]*tagCacheNode, opts.MaxSize),
		head:       head,
		tail:       tail,
		maxSize:    opts.MaxSize,
		ttlSeconds: opts.TTLSeconds,
	}
}

// Get returns the cached value for key.
func (c *TagCache) Get(key uint64) ([]m3thrift.MetricTag, bool) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	node, ok := c.entries[key]
	if !ok {
		c.misses++
		return nil, false
	}

	// Check TTL if enabled
	if c.ttlSeconds > 0 {
		now := time.Now().Unix()
		if now-node.lastAccess > c.ttlSeconds {
			// Entry has expired, remove it
			c.removeNode(node)
			delete(c.entries, key)
			c.misses++
			return nil, false
		}
		node.lastAccess = now
	}

	// Move to front (most recently used)
	c.moveToFront(node)
	c.hits++
	return node.value, true
}

// Set attempts to set the value of key as tslice, returning either tslice or
// the pre-existing value if found.
func (c *TagCache) Set(key uint64, tslice []m3thrift.MetricTag) []m3thrift.MetricTag {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	// Check if already exists
	if node, ok := c.entries[key]; ok {
		// Move to front and update access time
		c.moveToFront(node)
		if c.ttlSeconds > 0 {
			node.lastAccess = time.Now().Unix()
		}
		return node.value
	}

	// Evict if necessary
	if len(c.entries) >= c.maxSize {
		c.evictLRU()
	}

	// Create new node
	now := time.Now().Unix()
	node := &tagCacheNode{
		key:        key,
		value:      tslice,
		lastAccess: now,
	}

	// Add to front of list
	c.addToFront(node)
	c.entries[key] = node

	return tslice
}

// Len returns the size of the cache.
func (c *TagCache) Len() int {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return len(c.entries)
}

// Stats returns cache statistics.
func (c *TagCache) Stats() (hits, misses, evictions int64, hitRate float64) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	hits = c.hits
	misses = c.misses
	evictions = c.evictions

	total := hits + misses
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return
}

// Clear removes all entries from the cache.
func (c *TagCache) Clear() {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.entries = make(map[uint64]*tagCacheNode, c.maxSize)
	c.head.next = c.tail
	c.tail.prev = c.head
	c.hits = 0
	c.misses = 0
	c.evictions = 0
}

// CleanupExpired removes expired entries (useful for periodic cleanup).
func (c *TagCache) CleanupExpired() int {
	if c.ttlSeconds <= 0 {
		return 0 // TTL disabled
	}

	c.mtx.Lock()
	defer c.mtx.Unlock()

	now := time.Now().Unix()
	removed := 0

	// Walk from tail (oldest) towards head
	current := c.tail.prev
	for current != c.head {
		prev := current.prev // Save before potential removal

		if now-current.lastAccess > c.ttlSeconds {
			c.removeNode(current)
			delete(c.entries, current.key)
			removed++
		} else {
			// Since list is ordered by access time, we can stop here
			break
		}

		current = prev
	}

	return removed
}

// moveToFront moves a node to the front of the LRU list
func (c *TagCache) moveToFront(node *tagCacheNode) {
	c.removeNode(node)
	c.addToFront(node)
}

// addToFront adds a node to the front of the LRU list
func (c *TagCache) addToFront(node *tagCacheNode) {
	node.prev = c.head
	node.next = c.head.next
	c.head.next.prev = node
	c.head.next = node
}

// removeNode removes a node from the LRU list
func (c *TagCache) removeNode(node *tagCacheNode) {
	node.prev.next = node.next
	node.next.prev = node.prev
}

// evictLRU removes the least recently used entry
func (c *TagCache) evictLRU() {
	if c.tail.prev == c.head {
		return // Empty list
	}

	lru := c.tail.prev
	c.removeNode(lru)
	delete(c.entries, lru.key)
	c.evictions++
}

// TagMapKey generates a new key based on tags.
func TagMapKey(tags map[string]string) uint64 {
	return identity.StringStringMap(tags)
}
