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

package m3

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeIdentifierBasic(t *testing.T) {
	validID := "abcdef0AxS-s_Z"
	require.Equal(t, validID, sanitizeMetricIdentifiers(validID))
}

func TestSanitizeIdentifierAllValidCharacters(t *testing.T) {
	var buf bytes.Buffer
	for _, chars := range validRangeCharacters {
		for i := chars[0]; i <= chars[1]; i++ {
			_, err := buf.WriteRune(rune(i))
			require.NoError(t, err)
		}
	}
	for _, i := range validSingleCharacters {
		_, err := buf.WriteRune(rune(i))
		require.NoError(t, err)
	}
	id := buf.String()
	require.Equal(t, id, sanitizeMetricIdentifiers(id))
}

func TestSanitizeIdentifierInvalid(t *testing.T) {
	type testCase struct {
		input  string
		output string
	}

	testCases := []testCase{
		{"a:b", "a_b"},
		{"a! b", "a__b"},
		{"?bZ", "_bZ"},
	}

	for _, tc := range testCases {
		require.Equal(t, tc.output, sanitizeMetricIdentifiers(tc.input))
	}
}
