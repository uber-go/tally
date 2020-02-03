package tally

import "sync"

var scopeRegistryKey = KeyForPrefixedStringMap

type scopeRegistry struct {
	mu        sync.RWMutex
	subscopes map[string]*scope
}

func newScopeRegistry(root *scope) *scopeRegistry {
	r := &scopeRegistry{
		subscopes: make(map[string]*scope),
	}
	r.subscopes[scopeRegistryKey(root.prefix, root.tags)] = root
	return r
}

func (r *scopeRegistry) Report(reporter StatsReporter) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, s := range r.subscopes {
		s.report(reporter)
	}
}

func (r *scopeRegistry) CachedReport() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, s := range r.subscopes {
		s.cachedReport()
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
	allTags := mergeRightTags(parent.tags, tags)
	key := scopeRegistryKey(prefix, allTags)

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
	}
	r.subscopes[key] = subscope
	return subscope
}

func (r *scopeRegistry) lockedLookup(key string) (*scope, bool) {
	ss, ok := r.subscopes[key]
	return ss, ok
}
