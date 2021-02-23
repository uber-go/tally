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
	"time"
)

var scopeRegistryKey = keyForPrefixedStringMaps

type scopeRegistry struct {
	mu        sync.RWMutex
	subscopes map[string]*scope
	ttl       time.Duration
	deep      bool
}

func newScopeRegistry(root *scope, opts ScopeOptions) *scopeRegistry {
	r := &scopeRegistry{
		subscopes: make(map[string]*scope),
		ttl:       opts.UnusedScopeTTL,
		deep:      opts.UnusedScopeDeepEviction,
	}
	r.subscopes[scopeRegistryKey(root.prefix, root.tags)] = root
	return r
}

func (r *scopeRegistry) Report(reporter StatsReporter) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	for key, s := range r.subscopes {
		if s.report(reporter) {
			s.lastReport = now
			continue
		}

		if r.ttl > 0 && now.Sub(s.lastReport) > r.ttl {
			s.release(r.deep)

			if r.deep {
				delete(r.subscopes, key)
				for k := range s.tags {
					delete(s.tags, k)
				}
			}
		}
	}
}

func (r *scopeRegistry) CachedReport() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	for key, s := range r.subscopes {
		if s.cachedReport() {
			s.lastReport = now
			continue
		}

		if r.ttl > 0 && now.Sub(s.lastReport) > r.ttl {
			s.release(r.deep)

			if r.deep {
				delete(r.subscopes, key)
				for k := range s.tags {
					delete(s.tags, k)
				}
			}
		}
	}
}

func (r *scopeRegistry) ForEachScope(f func(*scope)) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, s := range r.subscopes {
		f(s)
	}
}

func (r *scopeRegistry) Subscope(parent *scope, prefix string, tags map[string]string) *scope {
	key := scopeRegistryKey(prefix, parent.tags, tags)

	r.mu.RLock()
	if s, ok := r.lockedLookup(key); ok {
		r.mu.RUnlock()
		return s
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if s, ok := r.lockedLookup(key); ok {
		return s
	}

	allTags := mergeRightTags(parent.tags, tags)
	subscope := &scope{
		separator: parent.separator,
		prefix:    prefix,
		// NB(prateek): don't need to copy the tags here,
		// we assume the map provided is immutable.
		tags:           allTags,
		reporter:       parent.reporter,
		cachedReporter: parent.cachedReporter,
		baseReporter:   parent.baseReporter,
		defaultBuckets: parent.defaultBuckets,
		sanitizer:      parent.sanitizer,
		registry:       parent.registry,

		counters:        make(map[string]*counter),
		countersSlice:   make([]*counter, 0, _defaultInitialSliceSize),
		gauges:          make(map[string]*gauge),
		gaugesSlice:     make([]*gauge, 0, _defaultInitialSliceSize),
		histograms:      make(map[string]*histogram),
		histogramsSlice: make([]*histogram, 0, _defaultInitialSliceSize),
		timers:          make(map[string]*timer),
		bucketCache:     parent.bucketCache,
	}
	r.subscopes[key] = subscope
	return subscope
}

func (r *scopeRegistry) lockedLookup(key string) (*scope, bool) {
	ss, ok := r.subscopes[key]
	return ss, ok
}
