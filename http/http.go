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
	"bytes"
	"fmt"
	"net/http"

	"github.com/uber-go/tally"
)

// NB(jeromefroe): We follow the model of Prometheus here and define different Content-Type
// values in case we want to support other content types in the future.

// Format specifies the HTTP content type of the different wire protocols.
type Format string

// Constants to assemble the Content-Type values for the different wire protocols.
const (
	TextVersion = "0.0.1"

	// The Content-Type values for the different wire protocols.
	TextFormat Format = `text/plain; version=` + TextVersion
)

const (
	contentTypeHeader   = "Content-Type"
	contentLengthHeader = "Content-Length"
)

var (
	bufferPoolSize = 128
	bufferPoolLen  = 8192
	bufferPool     = tally.NewObjectPool(bufferPoolSize)
)

func init() {
	bufferPool.Init(func() interface{} {
		return bytes.NewBuffer(make([]byte, 0, bufferPoolLen))
	})
}

func release(b *bytes.Buffer) {
	b.Reset()
	bufferPool.Put(b)
}

// Handler returns an http.Handler for the provided SnapshotScope.
func Handler(s tally.SnapshotScope) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		snapshot := s.Snapshot()

		buf := bufferPool.Get().(*bytes.Buffer)
		defer release(buf)

		enc := NewEncoder(buf)

		if err := enc.Encode(snapshot); err != nil {
			http.Error(w, "An error has occurred during metrics encoding:\n\n"+err.Error(), http.StatusInternalServerError)
			return
		}

		header := w.Header()
		header.Set(contentTypeHeader, string(TextFormat))
		header.Set(contentLengthHeader, fmt.Sprint(buf.Len()))
		w.Write(buf.Bytes())
	})
}
