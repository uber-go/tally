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

package prometheus

import (
	"fmt"
	"testing"
	"time"
)

func TestCounter(t *testing.T) {
	r := NewReporter(nil)
	name := "test_counter"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	tags2 := map[string]string{
		"foo":  "baz",
		"test": "*",
	}
	r.ReportCounter(name, tags, 1)
	r.ReportCounter(name, tags, 1)
	r.ReportCounter(name, tags, 1)
	for i := 0; i < 5; i++ {
		fmt.Printf("%d adding 1\n", i)
		r.ReportCounter(name, tags, 1)
	}
	r.ReportCounter(name, tags2, 1)
	r.ReportCounter(name, tags2, 1)
}

func TestGauge(t *testing.T) {
	r := NewReporter(nil)
	name := "test_gauge"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	r.ReportGauge(name, tags, 15)
	r.ReportGauge(name, tags, 30)
}

func TestTimer(t *testing.T) {
	r := NewReporter(nil)
	name := "test_timer"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	tags2 := map[string]string{
		"foo":  "baz",
		"test": "*",
	}
	r.ReportTimer(name, tags, 10*time.Second)
	r.ReportTimer(name, tags, 320*time.Millisecond)
	r.ReportTimer(name, tags2, 223*time.Millisecond)
	r.ReportTimer(name, tags, 4*time.Second)
}
