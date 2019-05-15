// Copyright (c) 2018 Uber Technologies, Inc.
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
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOldStyleListenAddressConfig(t *testing.T) {
	go func() {
		if err := http.ListenAndServe(":8080", nil); err != nil {
			panic(err)
		}
	}()

	config := Configuration{}
	_, err := config.NewReporter(ConfigurationOptions{})
	require.NoError(t, err)

	start := time.Now()
	testTimeout := time.Second
	timeCheckInterval := 50 * time.Millisecond
	getSuccess := false
	for time.Since(start) < testTimeout {
		resp, err := http.Get("http://0.0.0.0:8080/metrics") // default test address
		if err == nil && resp.StatusCode == 200 {
			getSuccess = true
			break
		}
		time.Sleep(timeCheckInterval)
	}

	require.True(t, getSuccess)
}

func TestNewStyleListenAddressConfig(t *testing.T) {
	port := 12321
	config := Configuration{
		DynamicListenAddress: &ListenAddressConfiguration{
			Hostname: "0.0.0.0",
			Port: &PortConfiguration{
				PortType: ConfigResolver,
				Value:    &port,
			},
		},
	}
	_, err := config.NewReporter(ConfigurationOptions{})
	require.NoError(t, err)

	start := time.Now()
	testTimeout := time.Second
	timeCheckInterval := 50 * time.Millisecond
	getSuccess := false
	for time.Since(start) < testTimeout {
		resp, err := http.Get("http://0.0.0.0:12321/metrics")
		if err == nil && resp.StatusCode == 200 {
			getSuccess = true
			break
		}
		time.Sleep(timeCheckInterval)
	}

	require.True(t, getSuccess)
}

func TestNewStyleEnvVarBasedListenAddressConfig(t *testing.T) {
	port := 13331
	envVarName := "SOMETHINGLONGANDABSURD"
	require.NoError(t, os.Setenv(envVarName, fmt.Sprintf("%d", port)))

	config := Configuration{
		DynamicListenAddress: &ListenAddressConfiguration{
			Hostname: "0.0.0.0",
			Port: &PortConfiguration{
				PortType:   EnvironmentResolver,
				EnvVarName: &envVarName,
			},
		},
	}
	_, err := config.NewReporter(ConfigurationOptions{})
	require.NoError(t, err)

	start := time.Now()
	testTimeout := time.Second
	timeCheckInterval := 50 * time.Millisecond
	getSuccess := false
	for time.Since(start) < testTimeout {
		resp, err := http.Get("http://0.0.0.0:13331/metrics")
		if err == nil && resp.StatusCode == 200 {
			getSuccess = true
			break
		}
		time.Sleep(timeCheckInterval)
	}

	require.True(t, getSuccess)
}
