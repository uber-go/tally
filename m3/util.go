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
)

const (
	defaultReplacementCharacter = '_'
)

var (
	validRangeCharacters  = [][2]int{{int('a'), int('z')}, {int('A'), int('Z')}, {int('0'), int('9')}}
	validSingleCharacters = []rune{'-', '_'}
)

func sanitizeMetricIdentifiers(value string) string {
	buf := bytes.NewBuffer(make([]byte, 0, len(value)))
	for _, ch := range value {
		validCurr := false
		for i := 0; !validCurr && i < len(validRangeCharacters); i++ {
			for j := validRangeCharacters[i][0]; j <= validRangeCharacters[i][1]; j++ {
				if rune(j) == ch {
					validCurr = true
					break
				}
			}
		}
		for i := 0; !validCurr && i < len(validSingleCharacters); i++ {
			if validSingleCharacters[i] == ch {
				validCurr = true
				break
			}
		}
		if validCurr {
			buf.WriteRune(ch)
		} else {
			buf.WriteRune(defaultReplacementCharacter)
		}
	}
	return buf.String()
}
