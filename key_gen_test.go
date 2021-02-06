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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeyForPrefixedStringMaps(t *testing.T) {
	tests := []struct {
		desc   string
		prefix string
		maps   []map[string]string
		want   string
	}{
		{
			desc:   "no maps",
			prefix: "foo",
			want:   "foo+",
		},
		{
			desc:   "disjoint maps",
			prefix: "foo",
			maps: []map[string]string{
				{
					"a": "foo",
					"b": "bar",
				},
				{
					"c": "baz",
					"d": "qux",
				},
			},
			want: "foo+a=foo,b=bar,c=baz,d=qux",
		},
		{
			desc:   "map overlap",
			prefix: "foo",
			maps: []map[string]string{
				{
					"a": "1",
					"b": "1",
					"c": "1",
					"d": "1",
				},
				{"b": "2"},
				{"c": "3"},
				{"d": "4"},
			},
			want: "foo+a=1,b=2,c=3,d=4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := keyForPrefixedStringMaps(tt.prefix, tt.maps...)
			assert.Equal(t, tt.want, got)
		})
	}
}
