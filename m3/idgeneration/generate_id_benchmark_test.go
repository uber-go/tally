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

package idgeneration

import (
	"strconv"
	"testing"
)

func BenchmarkGenerateID0(b *testing.B) {
	benchmarkGenerateID(b, 0)
}

func BenchmarkGenerateID2(b *testing.B) {
	benchmarkGenerateID(b, 2)
}

func BenchmarkGenerateID8(b *testing.B) {
	benchmarkGenerateID(b, 8)
}

func benchmarkGenerateID(b *testing.B, numTags int) {
	tags := make(map[string]string, numTags)
	for i := numTags; i > 0; i-- {
		tags["tagName"+strconv.Itoa(i)] = "value" + strconv.Itoa(i)
	}

	for n := 0; n < b.N; n++ {
		Get("random.name.for.testing.purposes", tags)
	}
}
