package metrics

// Copied from code.uber.internal:go-common.git at version 139e3b5b4b4b775ff9ed8abb2a9f31b7bd1aad58

import (
	"github.com/apache/thrift/lib/go/thrift"
	"github.com/uber-go/tally/m3/customtransports"
	"github.com/uber-go/tally/m3/pool"
	"github.com/uber-go/tally/m3/thrift"
)

const (
	batchPoolSize       = 10
	metricPoolSize      = 300
	valuePoolSize       = 300
	timerPoolSize       = 100
	sizedMetricPoolSize = 100
	timerMetricPoolSize = 100
	tagPoolSize         = 100
	counterPoolSize     = 100
	gaugePoolSize       = 100
	protoPoolSize       = 10
	metIDPoolSize       = 300
)

type m3ResourcePool struct {
	batchPool       pool.ObjectPool
	metricPool      pool.ObjectPool
	tagPool         pool.ObjectPool
	valuePool       pool.ObjectPool
	counterPool     pool.ObjectPool
	gaugePool       pool.ObjectPool
	timerPool       pool.ObjectPool
	sizedMetricPool pool.ObjectPool
	timerMetricPool pool.ObjectPool
	counterIDPool   pool.ObjectPool
	gaugeIDPool     pool.ObjectPool
	timerIDPool     pool.ObjectPool
	protoPool       pool.ObjectPool
}

func newM3ResourcePool(protoFac thrift.TProtocolFactory) *m3ResourcePool {
	batchPool, _ := pool.NewStandardObjectPool(batchPoolSize,
		func() (interface{}, error) {
			return m3.NewMetricBatch(), nil
		}, nil,
	)

	metricPool, _ := pool.NewStandardObjectPool(metricPoolSize,
		func() (interface{}, error) {
			return m3.NewMetric(), nil
		}, nil,
	)

	tagPool, _ := pool.NewStandardObjectPool(tagPoolSize,
		func() (interface{}, error) {
			return m3.NewMetricTag(), nil
		}, nil,
	)

	valuePool, _ := pool.NewStandardObjectPool(valuePoolSize,
		func() (interface{}, error) {
			return m3.NewMetricValue(), nil
		}, nil,
	)

	counterPool, _ := pool.NewStandardObjectPool(counterPoolSize,
		func() (interface{}, error) {
			return m3.NewCountValue(), nil
		}, nil,
	)

	gaugePool, _ := pool.NewStandardObjectPool(gaugePoolSize,
		func() (interface{}, error) {
			return m3.NewGaugeValue(), nil
		}, nil,
	)

	timerPool, _ := pool.NewStandardObjectPool(timerPoolSize,
		func() (interface{}, error) {
			return m3.NewTimerValue(), nil
		}, nil,
	)

	sizedMetricPool, _ := pool.NewStandardObjectPool(sizedMetricPoolSize,
		func() (interface{}, error) {
			return &sizedMetric{}, nil
		}, nil,
	)

	timerMetricPool, _ := pool.NewStandardObjectPool(timerMetricPoolSize,
		func() (interface{}, error) {
			return &timerMetric{}, nil
		}, nil,
	)

	counterIDPool, _ := pool.NewStandardObjectPool(metIDPoolSize,
		func() (interface{}, error) {
			return &counterMetricID{}, nil
		}, nil,
	)

	gaugeIDPool, _ := pool.NewStandardObjectPool(metIDPoolSize,
		func() (interface{}, error) {
			return &gaugeMetricID{}, nil
		}, nil,
	)

	timerIDPool, _ := pool.NewStandardObjectPool(metIDPoolSize,
		func() (interface{}, error) {
			return &timerMetricID{}, nil
		}, nil,
	)

	protoPool, _ := pool.NewStandardObjectPool(protoPoolSize,
		func() (interface{}, error) {
			return protoFac.GetProtocol(&customtransport.TCalcTransport{}), nil
		}, nil,
	)

	return &m3ResourcePool{batchPool: batchPool,
		metricPool:      metricPool,
		tagPool:         tagPool,
		valuePool:       valuePool,
		counterPool:     counterPool,
		gaugePool:       gaugePool,
		timerPool:       timerPool,
		sizedMetricPool: sizedMetricPool,
		timerMetricPool: timerMetricPool,
		counterIDPool:   counterIDPool,
		gaugeIDPool:     gaugeIDPool,
		timerIDPool:     timerIDPool,
		protoPool:       protoPool}
}

func (r *m3ResourcePool) getBatch() *m3.MetricBatch {
	o, _ := r.batchPool.GetOrAlloc()
	return o.(*m3.MetricBatch)
}

func (r *m3ResourcePool) getMetric() *m3.Metric {
	o, _ := r.metricPool.GetOrAlloc()
	return o.(*m3.Metric)
}

func (r *m3ResourcePool) getTagList() map[*m3.MetricTag]bool {
	return map[*m3.MetricTag]bool{}
}

func (r *m3ResourcePool) getTag() *m3.MetricTag {
	o, _ := r.tagPool.GetOrAlloc()
	return o.(*m3.MetricTag)
}

func (r *m3ResourcePool) getValue() *m3.MetricValue {
	o, _ := r.valuePool.GetOrAlloc()
	return o.(*m3.MetricValue)
}

func (r *m3ResourcePool) getCount() *m3.CountValue {
	o, _ := r.counterPool.GetOrAlloc()
	return o.(*m3.CountValue)
}

func (r *m3ResourcePool) getGauge() *m3.GaugeValue {
	o, _ := r.gaugePool.GetOrAlloc()
	return o.(*m3.GaugeValue)
}

func (r *m3ResourcePool) getTimer() *m3.TimerValue {
	o, _ := r.timerPool.GetOrAlloc()
	return o.(*m3.TimerValue)
}

func (r *m3ResourcePool) getSizedMetric() *sizedMetric {
	o, _ := r.sizedMetricPool.GetOrAlloc()
	return o.(*sizedMetric)
}

func (r *m3ResourcePool) getTimerMetric() *timerMetric {
	o, _ := r.timerMetricPool.GetOrAlloc()
	return o.(*timerMetric)
}

func (r *m3ResourcePool) getCounterID(met *m3.Metric, size int32) *counterMetricID {
	o, _ := r.counterIDPool.GetOrAlloc()
	c := o.(*counterMetricID)
	c.met = met
	c.registered = 1
	c.prev = 0
	c.val = 0
	c.size = size
	return c
}

func (r *m3ResourcePool) getGaugeID(met *m3.Metric, size int32) *gaugeMetricID {
	o, _ := r.gaugeIDPool.GetOrAlloc()
	g := o.(*gaugeMetricID)
	g.met = met
	g.size = size
	g.registered = 1
	g.updated = 0
	g.val = 0
	return g
}

func (r *m3ResourcePool) getTimerID(met *m3.Metric, size int32) *timerMetricID {
	o, _ := r.timerIDPool.GetOrAlloc()
	t := o.(*timerMetricID)
	t.met = met
	t.registered = 1
	t.size = size
	return t
}

func (r *m3ResourcePool) getProto() thrift.TProtocol {
	o, _ := r.protoPool.GetOrAlloc()
	return o.(thrift.TProtocol)
}

func (r *m3ResourcePool) releaseProto(proto thrift.TProtocol) {
	calc := proto.Transport().(*customtransport.TCalcTransport)
	calc.ResetCount()
	r.protoPool.Release(proto)
}

func (r *m3ResourcePool) releaseSizedMetric(t *sizedMetric) {
	t.m = nil
	t.size = 0
	t.releaseShallow = false
	r.sizedMetricPool.Release(t)
}

func (r *m3ResourcePool) releaseTimerMetric(t *timerMetric) {
	r.timerMetricPool.Release(t)
}

func (r *m3ResourcePool) releaseAggMet(m aggMet) {
	if m.getMet() != nil {
		r.releaseMetric(m.getMet())
	}

	if _, ok := m.(*counterMetricID); ok {
		r.counterIDPool.Release(m)
	} else if _, ok := m.(*gaugeMetricID); ok {
		r.gaugeIDPool.Release(m)
	} else if _, ok := m.(*timerMetricID); ok {
		r.timerIDPool.Release(m)
	}
}

func (r *m3ResourcePool) releaseBatch(batch *m3.MetricBatch) {
	batch.CommonTags = nil
	for _, metric := range batch.Metrics {
		r.releaseMetric(metric)
	}
	batch.Metrics = nil
	r.batchPool.Release(batch)
}

func (r *m3ResourcePool) releaseMetricValue(metVal *m3.MetricValue) {
	if metVal.IsSetCount() {
		metVal.Count.I64Value = nil
		r.counterPool.Release(metVal.Count)
		metVal.Count = nil
	} else if metVal.IsSetGauge() {
		metVal.Gauge.I64Value = nil
		metVal.Gauge.DValue = nil
		r.gaugePool.Release(metVal.Gauge)
		metVal.Gauge = nil
	} else if metVal.IsSetTimer() {
		metVal.Timer.I64Value = nil
		metVal.Timer.DValue = nil
		r.timerPool.Release(metVal.Timer)
		metVal.Timer = nil
	}
	r.valuePool.Release(metVal)
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
	//Release Tags
	for tag := range metric.GetTags() {
		tag.TagName = ""
		tag.TagValue = nil
		r.tagPool.Release(tag)
	}
	metric.Tags = nil

	r.releaseShallowMetric(metric)
}

func (r *m3ResourcePool) releaseShallowMetric(metric *m3.Metric) {
	metric.Timestamp = nil

	metVal := metric.GetMetricValue()
	if metVal == nil {
		r.metricPool.Release(metric)
		return
	}

	//Release values
	if metVal.IsSetCount() {
		metVal.Count.I64Value = nil
		r.counterPool.Release(metVal.Count)
		metVal.Count = nil
	} else if metVal.IsSetGauge() {
		metVal.Gauge.I64Value = nil
		metVal.Gauge.DValue = nil
		r.gaugePool.Release(metVal.Gauge)
		metVal.Gauge = nil
	} else if metVal.IsSetTimer() {
		metVal.Timer.I64Value = nil
		metVal.Timer.DValue = nil
		r.timerPool.Release(metVal.Timer)
		metVal.Timer = nil
	}

	r.valuePool.Release(metVal)
	r.metricPool.Release(metric)
}
