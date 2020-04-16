// Copyright (c) 2020 Uber Technologies, Inc.
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
	"io/ioutil"
	"net"
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
	dir, err := ioutil.TempDir("", "tally-test-prometheus")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	uds := path.Join(dir, "test-metrics.sock")
	cfg := Configuration{
		ListenAddress: fmt.Sprintf("unix://%s", uds),
		OnError:       "log",
	}

	go func() {
		_, _ = cfg.NewReporter(ConfigurationOptions{})
	}()

	time.Sleep(time.Second)
	_, err = os.Stat(uds)
	require.NoError(t, err)
}
