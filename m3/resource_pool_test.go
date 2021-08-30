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

package m3

import (
	"testing"

	"github.com/stretchr/testify/require"
	m3thrift "github.com/uber-go/tally/v4/m3/thrift/v2"
	"github.com/uber-go/tally/v4/thirdparty/github.com/apache/thrift/lib/go/thrift"
)

func TestM3ResourcePoolMetric(t *testing.T) {
	p := newResourcePool(thrift.NewTCompactProtocolFactory())

	metrics := p.getMetricSlice()
	metrics = append(metrics, m3thrift.Metric{})
	require.Equal(t, 1, len(metrics))
	p.releaseMetricSlice(metrics)
	metrics = p.getMetricSlice()
	require.Equal(t, 0, len(metrics))

	tags := p.getMetricTagSlice()
	tags = append(tags, m3thrift.MetricTag{})
	require.Equal(t, 1, len(tags))
	p.releaseMetricTagSlice(tags)
	tags = p.getMetricTagSlice()
	require.Equal(t, 0, len(tags))
}
