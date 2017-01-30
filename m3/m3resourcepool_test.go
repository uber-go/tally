package metrics

// Copied from code.uber.internal:go-common.git at version 139e3b5b4b4b775ff9ed8abb2a9f31b7bd1aad58

import (
	"testing"

	"github.com/uber-go/tally/m3/thrift"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/stretchr/testify/require"
)

func TestM3ResourcePoolMetric(t *testing.T) {
	p := newM3ResourcePool(thrift.NewTCompactProtocolFactory())

	var v int64
	cm := p.getMetric()
	cmv := p.getValue()
	cm.MetricValue = cmv
	cv := p.getCount()
	cmv.Count = cv
	cv.I64Value = &v
	cm.Tags = map[*m3.MetricTag]bool{createTag(p, "t1", "v1"): true}

	gm := p.getMetric()
	gmv := p.getValue()
	gm.MetricValue = gmv
	gv := p.getGauge()
	gmv.Gauge = gv
	gv.I64Value = &v

	tm := p.getMetric()
	tmv := p.getValue()
	tm.MetricValue = tmv
	tv := p.getTimer()
	tmv.Timer = tv
	tv.I64Value = &v

	p.releaseMetric(tm)
	p.releaseMetric(gm)
	p.releaseMetric(cm)

	cm2 := p.getMetric()
	gm2 := p.getMetric()
	tm2 := p.getMetric()

	require.Nil(t, cm2.MetricValue)
	require.Nil(t, gm2.MetricValue)
	require.Nil(t, tm2.MetricValue)
}

func TestM3ResourcePoolMetricValue(t *testing.T) {
	p := newM3ResourcePool(thrift.NewTCompactProtocolFactory())
	var v int64
	cmv := p.getValue()
	cv := p.getCount()
	cmv.Count = cv
	cv.I64Value = &v

	gmv := p.getValue()
	gv := p.getGauge()
	gmv.Gauge = gv
	gv.I64Value = &v

	tmv := p.getValue()
	tv := p.getTimer()
	tmv.Timer = tv
	tv.I64Value = &v

	p.releaseMetricValue(tmv)
	p.releaseMetricValue(gmv)
	p.releaseMetricValue(cmv)

	cmv2 := p.getValue()
	gmv2 := p.getValue()
	tmv2 := p.getValue()

	require.Nil(t, cmv2.Count)
	require.Nil(t, gmv2.Gauge)
	require.Nil(t, tmv2.Timer)
}

func TestM3ResourcePoolBatch(t *testing.T) {
	p := newM3ResourcePool(thrift.NewTCompactProtocolFactory())
	b := p.getBatch()
	b.Metrics = append(b.Metrics, p.getMetric())
	p.releaseBatch(b)
	b2 := p.getBatch()
	require.Equal(t, 0, len(b2.Metrics))
}
