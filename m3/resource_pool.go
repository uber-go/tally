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
	tally "github.com/uber-go/tally/v4"
	customtransport "github.com/uber-go/tally/v4/m3/customtransports"
	m3thrift "github.com/uber-go/tally/v4/m3/thrift/v2"
	"github.com/uber-go/tally/v4/thirdparty/github.com/apache/thrift/lib/go/thrift"
)

const (
	batchPoolSize  = 10
	metricPoolSize = DefaultMaxQueueSize
	protoPoolSize  = 10
)

type resourcePool struct {
	metricSlicePool    *tally.ObjectPool
	metricTagSlicePool *tally.ObjectPool
	protoPool          *tally.ObjectPool
}

func newResourcePool(protoFac thrift.TProtocolFactory) *resourcePool {
	metricSlicePool := tally.NewObjectPool(batchPoolSize)
	metricSlicePool.Init(func() interface{} {
		return make([]m3thrift.Metric, 0, batchPoolSize)
	})

	metricTagSlicePool := tally.NewObjectPool(DefaultMaxQueueSize)
	metricTagSlicePool.Init(func() interface{} {
		return make([]m3thrift.MetricTag, 0, batchPoolSize)
	})

	protoPool := tally.NewObjectPool(protoPoolSize)
	protoPool.Init(func() interface{} {
		return protoFac.GetProtocol(&customtransport.TCalcTransport{})
	})

	return &resourcePool{
		metricSlicePool:    metricSlicePool,
		metricTagSlicePool: metricTagSlicePool,
		protoPool:          protoPool,
	}
}

func (r *resourcePool) getMetricSlice() []m3thrift.Metric {
	return r.metricSlicePool.Get().([]m3thrift.Metric)
}

func (r *resourcePool) getMetricTagSlice() []m3thrift.MetricTag {
	return r.metricTagSlicePool.Get().([]m3thrift.MetricTag)
}

func (r *resourcePool) getProto() thrift.TProtocol {
	o := r.protoPool.Get()
	return o.(thrift.TProtocol)
}

//nolint:unused
func (r *resourcePool) releaseProto(proto thrift.TProtocol) {
	calc := proto.Transport().(*customtransport.TCalcTransport)
	calc.ResetCount()
	r.protoPool.Put(proto)
}

func (r *resourcePool) releaseMetricSlice(metrics []m3thrift.Metric) {
	for i := 0; i < len(metrics); i++ {
		metrics[i].Tags = nil
	}

	r.metricSlicePool.Put(metrics[:0])
}

//nolint:unused
func (r *resourcePool) releaseMetricTagSlice(tags []m3thrift.MetricTag) {
	r.metricSlicePool.Put(tags[:0])
}
