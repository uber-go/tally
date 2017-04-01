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

package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/uber-go/tally"
)

func TestHTTPHandler(t *testing.T) {
	opts := tally.ScopeOptions{
		Tags: map[string]string{"env": "test", "service": "fizz"},
	}
	s, closer := tally.NewRootScope(opts, 0)
	defer closer.Close()

	s.Counter("doe").Inc(1)
	s.Gauge("ray").Update(2)
	s.Timer("me").Record(1 * time.Second)

	vh := s.Histogram("fa", tally.ValueBuckets{0, 2, 4})
	vh.RecordValue(1)
	vh.RecordValue(5)
	dh := s.Histogram("sew", tally.DurationBuckets{time.Second * 2, time.Second * 4})
	dh.RecordDuration(time.Second)

	handler := Handler(s)

	writer := httptest.NewRecorder()
	request, _ := http.NewRequest("GET", "/", nil)
	request.Header.Add("Accept", "test/plain")

	handler.ServeHTTP(writer, request)
	assert.Equal(t, http.StatusOK, writer.Code)

	expectedResponse := `# TYPE doe counter
doe{env="test",service="fizz"} 1
# TYPE ray gauge
ray{env="test",service="fizz"} 2
# TYPE me timer
me{env="test",service="fizz"} [1s]
# TYPE fa histogram
fa{env="test",service="fizz",le="0"} 0
fa{env="test",service="fizz",le="2"} 1
fa{env="test",service="fizz",le="4"} 0
fa{env="test",service="fizz",le="+Inf"} 1
# TYPE sew histogram
sew{env="test",service="fizz",le="0s"} 0
sew{env="test",service="fizz",le="2s"} 1
sew{env="test",service="fizz",le="4s"} 0
sew{env="test",service="fizz",le="+Inf"} 0
`
	assert.Equal(t, expectedResponse, writer.Body.String())
}
