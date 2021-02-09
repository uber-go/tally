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
	"fmt"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/uber-go/tally"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NB(r): If a test is failing, you can debug what is being
// gathered from Prometheus by printing the following:
// proto.MarshalTextString(gather(t, registry)[0])

func TestCounter(t *testing.T) {
	registry := prom.NewRegistry()
	r := NewReporter(Options{Registerer: registry})
	name := "test_counter"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	tags2 := map[string]string{
		"foo":  "baz",
		"test": "something",
	}

	count := r.AllocateCounter(name, tags)
	count.ReportCount(1)
	count.ReportCount(2)

	count = r.AllocateCounter(name, tags2)
	count.ReportCount(2)

	assertMetric(t, gather(t, registry), metric{
		name:  name,
		mtype: dto.MetricType_COUNTER,
		instances: []instance{
			{
				labels:  tags,
				counter: counterValue(3),
			},
			{
				labels:  tags2,
				counter: counterValue(2),
			},
		},
	})
}

func TestGauge(t *testing.T) {
	registry := prom.NewRegistry()
	r := NewReporter(Options{Registerer: registry})
	name := "test_gauge"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}

	gauge := r.AllocateGauge(name, tags)
	gauge.ReportGauge(15)
	gauge.ReportGauge(30)

	assertMetric(t, gather(t, registry), metric{
		name:  name,
		mtype: dto.MetricType_GAUGE,
		instances: []instance{
			{
				labels: tags,
				gauge:  gaugeValue(30),
			},
		},
	})
}

func TestTimerHistogram(t *testing.T) {
	registry := prom.NewRegistry()
	r := NewReporter(Options{
		Registerer:       registry,
		DefaultTimerType: HistogramTimerType,
		DefaultHistogramBuckets: []float64{
			50 * ms,
			250 * ms,
			1000 * ms,
			2500 * ms,
			10000 * ms,
		},
	})

	name := "test_timer"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	tags2 := map[string]string{
		"foo":  "baz",
		"test": "something",
	}
	vals := []time.Duration{
		23 * time.Millisecond,
		223 * time.Millisecond,
		320 * time.Millisecond,
	}
	vals2 := []time.Duration{
		1742 * time.Millisecond,
		3232 * time.Millisecond,
	}

	timer := r.AllocateTimer(name, tags)
	for _, v := range vals {
		timer.ReportTimer(v)
	}

	timer = r.AllocateTimer(name, tags2)
	for _, v := range vals2 {
		timer.ReportTimer(v)
	}

	assertMetric(t, gather(t, registry), metric{
		name:  name,
		mtype: dto.MetricType_HISTOGRAM,
		instances: []instance{
			{
				labels: tags,
				histogram: histogramValue(histogramVal{
					sampleCount: uint64(len(vals)),
					sampleSum:   durationFloatSum(vals),
					buckets: []histogramValBucket{
						{upperBound: 0.05, count: 1},
						{upperBound: 0.25, count: 2},
						{upperBound: 1.00, count: 3},
						{upperBound: 2.50, count: 3},
						{upperBound: 10.00, count: 3},
					},
				}),
			},
			{
				labels: tags2,
				histogram: histogramValue(histogramVal{
					sampleCount: uint64(len(vals2)),
					sampleSum:   durationFloatSum(vals2),
					buckets: []histogramValBucket{
						{upperBound: 0.05, count: 0},
						{upperBound: 0.25, count: 0},
						{upperBound: 1.00, count: 0},
						{upperBound: 2.50, count: 1},
						{upperBound: 10.00, count: 2},
					},
				}),
			},
		},
	})
}

func TestTimerSummary(t *testing.T) {
	registry := prom.NewRegistry()
	r := NewReporter(Options{
		Registerer:       registry,
		DefaultTimerType: SummaryTimerType,
		DefaultSummaryObjectives: map[float64]float64{
			0.5:   0.01,
			0.75:  0.001,
			0.95:  0.001,
			0.99:  0.001,
			0.999: 0.0001,
		},
	})

	name := "test_timer"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	tags2 := map[string]string{
		"foo":  "baz",
		"test": "something",
	}
	vals := []time.Duration{
		23 * time.Millisecond,
		223 * time.Millisecond,
		320 * time.Millisecond,
	}
	vals2 := []time.Duration{
		1742 * time.Millisecond,
		3232 * time.Millisecond,
	}

	timer := r.AllocateTimer(name, tags)
	for _, v := range vals {
		timer.ReportTimer(v)
	}

	timer = r.AllocateTimer(name, tags2)
	for _, v := range vals2 {
		timer.ReportTimer(v)
	}

	assertMetric(t, gather(t, registry), metric{
		name:  name,
		mtype: dto.MetricType_SUMMARY,
		instances: []instance{
			{
				labels: tags,
				summary: summaryValue(summaryVal{
					sampleCount: uint64(len(vals)),
					sampleSum:   durationFloatSum(vals),
					quantiles: []summaryValQuantile{
						{quantile: 0.50, value: 0.223},
						{quantile: 0.75, value: 0.32},
						{quantile: 0.95, value: 0.32},
						{quantile: 0.99, value: 0.32},
						{quantile: 0.999, value: 0.32},
					},
				}),
			},
			{
				labels: tags2,
				summary: summaryValue(summaryVal{
					sampleCount: uint64(len(vals2)),
					sampleSum:   durationFloatSum(vals2),
					quantiles: []summaryValQuantile{
						{quantile: 0.50, value: 1.742},
						{quantile: 0.75, value: 3.232},
						{quantile: 0.95, value: 3.232},
						{quantile: 0.99, value: 3.232},
						{quantile: 0.999, value: 3.232},
					},
				}),
			},
		},
	})
}

func TestHistogramBucketValues(t *testing.T) {
	registry := prom.NewRegistry()
	r := NewReporter(Options{
		Registerer: registry,
	})

	buckets := tally.DurationBuckets{
		0 * time.Millisecond,
		50 * time.Millisecond,
		250 * time.Millisecond,
		1000 * time.Millisecond,
		2500 * time.Millisecond,
		10000 * time.Millisecond,
	}

	name := "test_histogram"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	tags2 := map[string]string{
		"foo":  "baz",
		"test": "something",
	}
	vals := []time.Duration{
		23 * time.Millisecond,
		223 * time.Millisecond,
		320 * time.Millisecond,
	}
	vals2 := []time.Duration{
		1742 * time.Millisecond,
		3232 * time.Millisecond,
	}

	histogram := r.AllocateHistogram(name, tags, buckets)
	histogram.DurationBucket(0, 50*time.Millisecond).ReportSamples(1)
	histogram.DurationBucket(0, 250*time.Millisecond).ReportSamples(1)
	histogram.DurationBucket(0, 1000*time.Millisecond).ReportSamples(1)

	histogram = r.AllocateHistogram(name, tags2, buckets)
	histogram.DurationBucket(0, 2500*time.Millisecond).ReportSamples(1)
	histogram.DurationBucket(0, 10000*time.Millisecond).ReportSamples(1)

	assertMetric(t, gather(t, registry), metric{
		name:  name,
		mtype: dto.MetricType_HISTOGRAM,
		instances: []instance{
			{
				labels: tags,
				histogram: histogramValue(histogramVal{
					sampleCount: uint64(len(vals)),
					sampleSum:   1.3,
					buckets: []histogramValBucket{
						{upperBound: 0.00, count: 0},
						{upperBound: 0.05, count: 1},
						{upperBound: 0.25, count: 2},
						{upperBound: 1.00, count: 3},
						{upperBound: 2.50, count: 3},
						{upperBound: 10.00, count: 3},
					},
				}),
			},
			{
				labels: tags2,
				histogram: histogramValue(histogramVal{
					sampleCount: uint64(len(vals2)),
					sampleSum:   12.5,
					buckets: []histogramValBucket{
						{upperBound: 0.00, count: 0},
						{upperBound: 0.05, count: 0},
						{upperBound: 0.25, count: 0},
						{upperBound: 1.00, count: 0},
						{upperBound: 2.50, count: 1},
						{upperBound: 10.00, count: 2},
					},
				}),
			},
		},
	})
}

func TestOnRegisterError(t *testing.T) {
	var captured []error

	registry := prom.NewRegistry()
	r := NewReporter(Options{
		Registerer: registry,
		OnRegisterError: func(err error) {
			captured = append(captured, err)
		},
	})

	c := r.AllocateCounter("bad-name", nil)
	c.ReportCount(2)
	c.ReportCount(4)
	c = r.AllocateCounter("bad.name", nil)
	c.ReportCount(42)
	c.ReportCount(84)

	assert.Equal(t, 2, len(captured))
}

func TestAlreadyRegisteredCounter(t *testing.T) {
	var captured []error

	registry := prom.NewRegistry()
	r := NewReporter(Options{
		Registerer: registry,
		OnRegisterError: func(err error) {
			captured = append(captured, err)
		},
	})

	// n.b. Prometheus metrics are different from M3 metrics in that they are
	//      uniquely identified as "metric_name+label_name+label_name+...";
	//      additionally, given that Prometheus ingestion is pull-based, there
	//      is only ever one reporter used regardless of tally.Scope hierarchy.
	//
	//      Because of this, for a given metric "foo", only the first-registered
	//      permutation of metric and label names will succeed because the same
	//      registry is being used, and because Prometheus asserts that a
	//      registered metric name has the same corresponding label names.
	//      Subsequent registrations - such as adding or removing tags - will
	//      return an error.
	//
	//      This is a problem because Tally's API does not apply semantics or
	//      restrictions to the combinatorics (or descendant mutations of)
	//      metric tags. As such, we must assert that Tally's Prometheus
	//      reporter does the right thing and indicates an error when this
	//      happens.
	//
	// The first allocation call will succeed. This establishes the required
	// label names (["foo"]) for metric "foo".
	r.AllocateCounter("foo", map[string]string{"foo": "bar"})

	// The second allocation call is okay, as it has the same label names (["foo"]).
	r.AllocateCounter("foo", map[string]string{"foo": "baz"})

	// The third allocation call fails, because it has different label names
	// (["bar"], vs previously ["foo"]) for the same metric name "foo".
	r.AllocateCounter("foo", map[string]string{"bar": "qux"})

	// The fourth allocation call fails, because while it has one of the same
	// label names ("foo") as was previously registered for metric "foo", it
	// also has an additional label name (["foo", "zork"] != ["foo"]).
	r.AllocateCounter("foo", map[string]string{
		"foo":  "bar",
		"zork": "derp",
	})

	// The fifth allocation call fails, because it has no label names for the
	// metric "foo", which expects the label names it was originally registered
	// with (["foo"]).
	r.AllocateCounter("foo", nil)

	require.Equal(t, 3, len(captured))
	for _, err := range captured {
		require.Contains(t, err.Error(), "same fully-qualified name")
	}
}

func gather(t *testing.T, r prom.Gatherer) []*dto.MetricFamily {
	metrics, err := r.Gather()
	require.NoError(t, err)
	return metrics
}

func counterValue(v float64) *dto.Counter {
	return &dto.Counter{Value: &v}
}

func gaugeValue(v float64) *dto.Gauge {
	return &dto.Gauge{Value: &v}
}

type histogramVal struct {
	sampleCount uint64
	sampleSum   float64
	buckets     []histogramValBucket
}

type histogramValBucket struct {
	count      uint64
	upperBound float64
}

func histogramValue(v histogramVal) *dto.Histogram {
	r := &dto.Histogram{
		SampleCount: &v.sampleCount,
		SampleSum:   &v.sampleSum,
	}
	for _, b := range v.buckets {
		b := b // or else the addresses we take will be static
		r.Bucket = append(r.Bucket, &dto.Bucket{
			CumulativeCount: &b.count,
			UpperBound:      &b.upperBound,
		})
	}
	return r
}

type summaryVal struct {
	sampleCount uint64
	sampleSum   float64
	quantiles   []summaryValQuantile
}

type summaryValQuantile struct {
	quantile float64
	value    float64
}

func summaryValue(v summaryVal) *dto.Summary {
	r := &dto.Summary{
		SampleCount: &v.sampleCount,
		SampleSum:   &v.sampleSum,
	}
	for _, q := range v.quantiles {
		q := q // or else the addresses we take will be static
		r.Quantile = append(r.Quantile, &dto.Quantile{
			Quantile: &q.quantile,
			Value:    &q.value,
		})
	}
	return r
}

func durationFloatSum(v []time.Duration) float64 {
	var sum float64
	for _, d := range v {
		sum += durationFloat(d)
	}
	return sum
}

func durationFloat(d time.Duration) float64 {
	return float64(d) / float64(time.Second)
}

type metric struct {
	name      string
	mtype     dto.MetricType
	instances []instance
}

type instance struct {
	labels    map[string]string
	counter   *dto.Counter
	gauge     *dto.Gauge
	histogram *dto.Histogram
	summary   *dto.Summary
}

func assertMetric(
	t *testing.T,
	metrics []*dto.MetricFamily,
	query metric,
) {
	q := query
	msgFmt := func(msg string, v ...interface{}) string {
		prefix := fmt.Sprintf("assert fail for metric name=%s, type=%s: ",
			q.name, q.mtype.String())
		return fmt.Sprintf(prefix+msg, v...)
	}
	for _, m := range metrics {
		if m.GetName() != q.name || m.GetType() != q.mtype {
			continue
		}
		if len(q.instances) == 0 {
			require.Fail(t, msgFmt("no instances to assert"))
		}
		for _, i := range q.instances {
			found := false
			for _, j := range m.GetMetric() {
				if len(i.labels) != len(j.GetLabel()) {
					continue
				}

				notMatched := make(map[string]string, len(i.labels))
				for k, v := range i.labels {
					notMatched[k] = v
				}

				for _, pair := range j.GetLabel() {
					notMatchedValue, matches := notMatched[pair.GetName()]
					if matches && pair.GetValue() == notMatchedValue {
						delete(notMatched, pair.GetName())
					}
				}

				if len(notMatched) != 0 {
					continue
				}

				found = true

				switch {
				case i.counter != nil:
					require.NotNil(t, j.GetCounter())
					assert.Equal(t, i.counter.GetValue(), j.GetCounter().GetValue())
				case i.gauge != nil:
					require.NotNil(t, j.GetGauge())
					assert.Equal(t, i.gauge.GetValue(), j.GetGauge().GetValue())
				case i.histogram != nil:
					require.NotNil(t, j.GetHistogram())
					assert.Equal(t, i.histogram.GetSampleCount(), j.GetHistogram().GetSampleCount())
					assert.Equal(t, i.histogram.GetSampleSum(), j.GetHistogram().GetSampleSum())
					require.Equal(t, len(i.histogram.GetBucket()), len(j.GetHistogram().GetBucket()))
					for idx, b := range i.histogram.GetBucket() {
						actual := j.GetHistogram().GetBucket()[idx]
						assert.Equal(t, b.GetCumulativeCount(), actual.GetCumulativeCount())
						assert.Equal(t, b.GetUpperBound(), actual.GetUpperBound())
					}
				case i.summary != nil:
					require.NotNil(t, j.GetSummary())
					assert.Equal(t, i.summary.GetSampleCount(), j.GetSummary().GetSampleCount())
					assert.Equal(t, i.summary.GetSampleSum(), j.GetSummary().GetSampleSum())
					require.Equal(t, len(i.summary.GetQuantile()), len(j.GetSummary().GetQuantile()))
					for idx, q := range i.summary.GetQuantile() {
						actual := j.GetSummary().GetQuantile()[idx]
						assert.Equal(t, q.GetQuantile(), actual.GetQuantile())
						assert.Equal(t, q.GetValue(), actual.GetValue())
					}
				}
			}
			if !found {
				require.Fail(t, msgFmt("instance not found labels=%v", i.labels))
			}
		}
		return
	}
	require.Fail(t, msgFmt("metric not found"))
}
