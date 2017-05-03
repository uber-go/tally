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
	"errors"
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

var (
	errScopeNotSnapshotResetProvider = errors.New(
		"scope provided is not a snapshot reset provider")
)

const (
	contentTypeHeader   = "Content-Type"
	contentLengthHeader = "Content-Length"
)

var (
	bufferPoolSize = 8
	bufferPoolLen  = 65536
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

// TryMakeHandler tries to create a handler from a Scope. If
// using a Scope constructed from tally then this call will always
// succeed as it implements SnapshotResetProvider.
func TryMakeHandler(s tally.Scope) (http.Handler, error) {
	provider, ok := s.(tally.SnapshotResetProvider)
	if !ok {
		return nil, errScopeNotSnapshotResetProvider
	}
	return Handler(provider), nil
}

// MustMakeHandler will create a handler from a Scope or panics. If
// using a Scope constructed from tally then this call will always
// succeed as it implements SnapshotResetProvider.
func MustMakeHandler(s tally.Scope) http.Handler {
	handler, err := TryMakeHandler(s)
	if err != nil {
		panic(err)
	}
	return handler
}

// Handler returns an http.Handler for the provided SnapshotResetProvider.
func Handler(s tally.SnapshotResetProvider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseForm(); err != nil {
			httpError(w, err)
			return
		}

		tagsFmt := DefaultTagsFormat
		if arg := req.Form.Get("tags_format"); arg != "" {
			value, err := ParseTagsFormat(arg)
			if err != nil {
				httpError(w, err)
				return
			}
			tagsFmt = value
		}

		opts := tally.ResetOptions{
			ResetCounters:   false,
			ResetTimers:     true,
			ResetHistograms: false,
		}

		for _, param := range []struct {
			name  string
			value *bool
		}{
			{"reset_counters", &opts.ResetCounters},
			{"reset_timers", &opts.ResetTimers},
			{"reset_histograms", &opts.ResetHistograms},
		} {
			if arg := req.Form.Get(param.name); arg != "" {
				value, err := parseBool(param.name, arg)
				if err != nil {
					httpError(w, err)
					return
				}
				*param.value = value
			}
		}

		snapshot := s.SnapshotReset(opts)

		buf := bufferPool.Get().(*bytes.Buffer)
		defer release(buf)

		enc := NewEncoder(buf, Options{
			TagsFormat: tagsFmt,
		})

		if err := enc.Encode(snapshot); err != nil {
			httpError(w, err)
			return
		}

		header := w.Header()
		header.Set(contentTypeHeader, string(TextFormat))
		header.Set(contentLengthHeader, fmt.Sprint(buf.Len()))
		w.Write(buf.Bytes())
	})
}

func parseBool(param, value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("not a bool %s, was %s: must be true or false",
		param, value)
}

func httpError(w http.ResponseWriter, err error) {
	http.Error(w, "error: "+err.Error(), http.StatusInternalServerError)
}
