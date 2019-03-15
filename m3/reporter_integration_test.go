// Copyright (c) 2019 Uber Technologies, Inc.
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
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	mainFileFmt = `
package main

import (
	"time"

	"github.com/uber-go/tally"
	"github.com/uber-go/tally/m3"
)

func main() {
	r, err := m3.NewReporter(m3.Options{
		HostPorts: []string{"%s"},
		Service:   "test-service",
		Env:       "test",
	})
	if err != nil {
		panic(err)
	}

	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		CachedReporter: r,
	}, 5 * time.Second)
	defer closer.Close()

	scope.Counter("my-counter").Inc(42)
	scope.Gauge("my-gauge").Update(123)
	scope.Timer("my-timer").Record(456 * time.Millisecond)
}
	`
)

// TestIntegrationProcessFlushOnExit tests whether data is correctly flushed
// when the scope is closed for shortly lived programs
func TestIntegrationProcessFlushOnExit(t *testing.T) {
	for i := 0; i < 5; i++ {
		testProcessFlushOnExit(t, i)
	}
}

func testProcessFlushOnExit(t *testing.T, i int) {
	dir, err := ioutil.TempDir("", "foo")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	var wg sync.WaitGroup
	server := newFakeM3Server(t, &wg, true, Compact)
	go server.Serve()
	defer server.Close()

	mainFile := path.Join(dir, "main.go")
	mainFileContents := fmt.Sprintf(mainFileFmt, server.Addr)

	fileErr := ioutil.WriteFile(mainFile, []byte(mainFileContents), 0666)
	require.NoError(t, fileErr)

	binary := path.Join(dir, "m3testemit")

	// build
	cmd := exec.Command("go", "build", "-o", binary, mainFile)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, fmt.Sprintf("output:\n\n%s", output))

	// run, do not sleep at end of the main program as per
	// main program source code
	wg.Add(1)
	require.NoError(t, exec.Command(binary).Run())

	// Wait for fake M3 server to receive the batch
	wg.Wait()

	require.Equal(t, 1, len(server.Service.getBatches()))
	require.NotNil(t, server.Service.getBatches()[0])
	require.Equal(t, 3, len(server.Service.getBatches()[0].GetMetrics()))
	metrics := server.Service.getBatches()[0].GetMetrics()
	fmt.Printf("Test %d emitted:\n%v\n", i, metrics)
}
