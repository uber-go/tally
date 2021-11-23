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
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestVerifyCachedTaggedScopesAlloc(t *testing.T) {
	root, _ := NewRootScope(ScopeOptions{
		Prefix:   "funkytown",
		Reporter: NullStatsReporter,
		Tags: map[string]string{
			"style":     "funky",
			"hair":      "wavy",
			"jefferson": "starship",
		},
	}, 0)

	allocs := testing.AllocsPerRun(1000, func() {
		root.Tagged(map[string]string{
			"foo": "bar",
			"baz": "qux",
			"qux": "quux",
		})
	})
	expected := 2.0
	assert.True(t, allocs <= expected, "the cached tagged scopes should allocate at most %.0f allocations, but did allocate %.0f", expected, allocs)
}
