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

package prometheus

import (
	"net"
	"net/http"
	"os"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenErrorCallsOnRegisterError(t *testing.T) {
	// Ensure that Listen error calls default OnRegisterError to panic
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer func() { _ = listener.Close() }()

	assert.NotPanics(t, func() {
		cfg := Configuration{
			ListenAddress: listener.Addr().String(),
			OnError:       "log",
		}
		_, _ = cfg.NewReporter(ConfigurationOptions{})
		time.Sleep(time.Second)
	})
}

func TestUnixDomainSocketListener(t *testing.T) {
	dir, err := os.MkdirTemp("", "tally-test-prometheus")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	uds := path.Join(dir, "test-metrics.sock")
	cfg := Configuration{ListenNetwork: "unix", ListenAddress: uds}
	_, _ = cfg.NewReporter(ConfigurationOptions{})

	time.Sleep(time.Second)
	_, err = os.Stat(uds)
	require.NoError(t, err)
	require.NoError(t, os.Remove(uds))
}

func TestTcpListener(t *testing.T) {
	cases := map[string]Configuration{
		"127.0.0.1:0":        {ListenAddress: "127.0.0.1:0"},
		"tcp://127.0.0.1:0":  {ListenNetwork: "tcp", ListenAddress: "127.0.0.1:0"},
		"tcp4://127.0.0.1:0": {ListenNetwork: "tcp4", ListenAddress: "127.0.0.1:0"},
	}

	for cn, cc := range cases {
		t.Run(cn, func(t *testing.T) {
			assert.NotPanics(t, func() {
				_, _ = cc.NewReporter(ConfigurationOptions{})
				time.Sleep(time.Second)
			})
		})
	}
}

func TestOptionsServeMux(t *testing.T) {
	t.Run("option ServerMux is nil", func(t *testing.T) {
		cfg := Configuration{
			ListenAddress: "127.0.0.1:8081",
		}
		_, err := cfg.NewReporter(ConfigurationOptions{})
		require.NoError(t, err)

		time.Sleep(time.Second)

		res, err := http.Get("http://127.0.0.1:8081/metrics")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, err = http.Get("http://127.0.0.1:8081/debug/pprof")
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	})

	t.Run("option ServerMux is not nil", func(t *testing.T) {
		cfg := Configuration{
			ListenAddress: "127.0.0.1:8082",
		}

		// an empty HandleFunc responds HTTP 200
		http.DefaultServeMux.HandleFunc("/debug/pprof", func(w http.ResponseWriter, r *http.Request) {})

		_, err := cfg.NewReporter(ConfigurationOptions{ServerMux: http.DefaultServeMux})
		require.NoError(t, err)

		time.Sleep(time.Second)

		res, err := http.Get("http://127.0.0.1:8082/metrics")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, err = http.Get("http://127.0.0.1:8082/debug/pprof")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)
	})

}
