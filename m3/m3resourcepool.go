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

package metrics

import (
	"github.com/uber-go/tally/m3/customtransports"
	"github.com/uber-go/tally/m3/thrift"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/m3db/m3x/pool"
)

const (
	batchPoolSize   = 10
	metricPoolSize  = DefaultMaxQueueSize
	valuePoolSize   = DefaultMaxQueueSize
	timerPoolSize   = DefaultMaxQueueSize
	tagPoolSize     = DefaultMaxQueueSize
	counterPoolSize = DefaultMaxQueueSize
	gaugePoolSize   = DefaultMaxQueueSize
	protoPoolSize   = 10
)

type m3ResourcePool struct {
	batchPool   pool.ObjectPool
	metricPool  pool.ObjectPool
	tagPool     pool.ObjectPool
	valuePool   pool.ObjectPool
	counterPool pool.ObjectPool
	gaugePool   pool.ObjectPool
	timerPool   pool.ObjectPool
	protoPool   pool.ObjectPool
}

func newM3ResourcePool(protoFac thrift.TProtocolFactory) *m3ResourcePool {
	batchPool := pool.NewObjectPool(pool.NewObjectPoolOptions().SetSize(batchPoolSize))
	batchPool.Init(func() interface{} {
		return m3.NewMetricBatch()
	})

	metricPool := pool.NewObjectPool(pool.NewObjectPoolOptions().SetSize(metricPoolSize))
	metricPool.Init(func() interface{} {
		return m3.NewMetric()
	})

	tagPool := pool.NewObjectPool(pool.NewObjectPoolOptions().SetSize(tagPoolSize))
	tagPool.Init(func() interface{} {
		return m3.NewMetricTag()
	})

	valuePool := pool.NewObjectPool(pool.NewObjectPoolOptions().SetSize(valuePoolSize))
	valuePool.Init(func() interface{} {
		return m3.NewMetricValue()
	})

	counterPool := pool.NewObjectPool(pool.NewObjectPoolOptions().SetSize(counterPoolSize))
	counterPool.Init(func() interface{} {
		return m3.NewCountValue()
	})

	gaugePool := pool.NewObjectPool(pool.NewObjectPoolOptions().SetSize(gaugePoolSize))
	gaugePool.Init(func() interface{} {
		return m3.NewGaugeValue()
	})

	timerPool := pool.NewObjectPool(pool.NewObjectPoolOptions().SetSize(timerPoolSize))
	timerPool.Init(func() interface{} {
		return m3.NewTimerValue()
	})

	protoPool := pool.NewObjectPool(pool.NewObjectPoolOptions().SetSize(protoPoolSize))
	protoPool.Init(func() interface{} {
		return protoFac.GetProtocol(&customtransport.TCalcTransport{})
	})

	return &m3ResourcePool{
		batchPool:   batchPool,
		metricPool:  metricPool,
		tagPool:     tagPool,
		valuePool:   valuePool,
		counterPool: counterPool,
		gaugePool:   gaugePool,
		timerPool:   timerPool,
		protoPool:   protoPool,
	}
}

func (r *m3ResourcePool) getBatch() *m3.MetricBatch {
	o := r.batchPool.Get()
	return o.(*m3.MetricBatch)
}

func (r *m3ResourcePool) getMetric() *m3.Metric {
	o := r.metricPool.Get()
	return o.(*m3.Metric)
}

func (r *m3ResourcePool) getTagList() map[*m3.MetricTag]bool {
	return map[*m3.MetricTag]bool{}
}

func (r *m3ResourcePool) getTag() *m3.MetricTag {
	o := r.tagPool.Get()
	return o.(*m3.MetricTag)
}

func (r *m3ResourcePool) getValue() *m3.MetricValue {
	o := r.valuePool.Get()
	return o.(*m3.MetricValue)
}

func (r *m3ResourcePool) getCount() *m3.CountValue {
	o := r.counterPool.Get()
	return o.(*m3.CountValue)
}

func (r *m3ResourcePool) getGauge() *m3.GaugeValue {
	o := r.gaugePool.Get()
	return o.(*m3.GaugeValue)
}

func (r *m3ResourcePool) getTimer() *m3.TimerValue {
	o := r.timerPool.Get()
	return o.(*m3.TimerValue)
}

func (r *m3ResourcePool) getProto() thrift.TProtocol {
	o := r.protoPool.Get()
	return o.(thrift.TProtocol)
}

func (r *m3ResourcePool) releaseProto(proto thrift.TProtocol) {
	calc := proto.Transport().(*customtransport.TCalcTransport)
	calc.ResetCount()
	r.protoPool.Put(proto)
}

func (r *m3ResourcePool) releaseBatch(batch *m3.MetricBatch) {
	batch.CommonTags = nil
	for _, metric := range batch.Metrics {
		r.releaseMetric(metric)
	}
	batch.Metrics = nil
	r.batchPool.Put(batch)
}

func (r *m3ResourcePool) releaseMetricValue(metVal *m3.MetricValue) {
	if metVal.IsSetCount() {
		metVal.Count.I64Value = nil
		r.counterPool.Put(metVal.Count)
		metVal.Count = nil
	} else if metVal.IsSetGauge() {
		metVal.Gauge.I64Value = nil
		metVal.Gauge.DValue = nil
		r.gaugePool.Put(metVal.Gauge)
		metVal.Gauge = nil
	} else if metVal.IsSetTimer() {
		metVal.Timer.I64Value = nil
		metVal.Timer.DValue = nil
		r.timerPool.Put(metVal.Timer)
		metVal.Timer = nil
	}
	r.valuePool.Put(metVal)
}

func (r *m3ResourcePool) releaseMetrics(mets []*m3.Metric) {
	for _, m := range mets {
		r.releaseMetric(m)
	}
}

func (r *m3ResourcePool) releaseShallowMetrics(mets []*m3.Metric) {
	for _, m := range mets {
		r.releaseShallowMetric(m)
	}
}

func (r *m3ResourcePool) releaseMetric(metric *m3.Metric) {
	metric.Name = ""
	// Release Tags
	for tag := range metric.Tags {
		tag.TagName = ""
		tag.TagValue = nil
		r.tagPool.Put(tag)
	}
	metric.Tags = nil

	r.releaseShallowMetric(metric)
}

func (r *m3ResourcePool) releaseShallowMetric(metric *m3.Metric) {
	metric.Timestamp = nil

	metVal := metric.MetricValue
	if metVal == nil {
		r.metricPool.Put(metric)
		return
	}

	r.releaseMetricValue(metVal)
	metric.MetricValue = nil

	r.metricPool.Put(metric)
}
