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

// Handler returns an http.Handler for the provided Scope.
func Handler(s tally.Scope) http.Handler {
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
