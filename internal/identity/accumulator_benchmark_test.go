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

package identity_test

import (
	"fmt"
	"testing"

	"github.com/uber-go/tally/v4/internal/identity"
)

func BenchmarkAccumulator_StringStringMap(b *testing.B) {
	cases := []struct {
		keys int
	}{
		{keys: 0},
		{keys: 1},
		{keys: 2},
		{keys: 4},
		{keys: 8},
		{keys: 16},
		{keys: 32},
	}

	bench := func(b *testing.B, m map[string]string) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			identity.StringStringMap(m)
		}
	}

	for _, tt := range cases {
		b.Run(fmt.Sprintf("%d keys", tt.keys), func(b *testing.B) {
			m := make(map[string]string)
			for i := 0; i < tt.keys; i++ {
				s := fmt.Sprintf("abcdefghij%d", i)
				m[s] = s
			}

			bench(b, m)
		})
	}
}
