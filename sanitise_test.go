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

package tally

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestSanitiser() SanitiseFn {
	c := &ValidCharacters{
		Ranges:     AlphanumericRange,
		Characters: UnderscoreDashCharacters,
	}
	return c.sanitiseFn(DefaultReplacementCharacter)
}

func TestSanitiseIdentifierAllValidCharacters(t *testing.T) {
	allValidChars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	fn := newTestSanitiser()
	require.Equal(t, allValidChars, fn(allValidChars))
}

func TestSanitiseTestCases(t *testing.T) {
	fn := newTestSanitiser()
	type testCase struct {
		input  string
		output string
	}

	testCases := []testCase{
		{"abcdef0AxS-s_Z", "abcdef0AxS-s_Z"},
		{"a:b", "a_b"},
		{"a! b", "a__b"},
		{"?bZ", "_bZ"},
	}

	for _, tc := range testCases {
		require.Equal(t, tc.output, fn(tc.input))
	}
}
