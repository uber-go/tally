package http

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/uber-go/tally"
)

type mockMetadata struct {
	mockName func() string
	mockTags func() map[string]string
}

func (m *mockMetadata) Name() string {
	if m.mockName != nil {
		return m.mockName()
	}

	return ""
}

func (m *mockMetadata) Tags() map[string]string {
	if m.mockTags != nil {
		return m.mockTags()
	}

	return nil
}

var metadata = &mockMetadata{
	mockName: func() string { return "requests" },
	mockTags: func() map[string]string {
		return map[string]string{"status_code": "200", "request_type": "GET"}
	},
}

func TestEncodeComment(t *testing.T) {
	var buf bytes.Buffer
	e := &encoder{w: &buf}

	metricTypes := []string{counterType, gaugeType, timerType, histogramType}
	for _, metricType := range metricTypes {
		n, err := e.encodeComments(metadata, metricType)
		actual := buf.String()
		expected := "# TYPE requests " + metricType + "\n"
		assert.Nil(t, err)
		assert.Equal(t, n, len(expected))
		assert.Equal(t, expected, actual)

		buf.Reset()
	}
}

func TestEncodeNameAndTags(t *testing.T) {
	var buf bytes.Buffer
	e := &encoder{w: &buf}

	n, err := e.encodeNameAndTags(metadata, "", "")
	actual := buf.String()
	expected := `requests{request_type="GET",status_code="200"}`
	assert.Nil(t, err)
	assert.Equal(t, n, len(expected))
	assert.Equal(t, expected, actual)

	buf.Reset()

	n, err = e.encodeNameAndTags(metadata, "quantile", "75")
	actual = buf.String()
	expected = `requests{request_type="GET",status_code="200",quantile="75"}`
	assert.Nil(t, err)
	assert.Equal(t, n, len(expected))
	assert.Equal(t, expected, actual)
}

type mockCounterSnapshot struct {
	*mockMetadata
	mockValue func() int64
}

func (m *mockCounterSnapshot) Value() int64 {
	if m.mockValue != nil {
		return m.mockValue()
	}

	return -1
}

var counterSnapshot = &mockCounterSnapshot{
	mockMetadata: metadata,
	mockValue:    func() int64 { return 42 },
}

func TestEncodeCounter(t *testing.T) {
	var buf bytes.Buffer
	e := &encoder{w: &buf}

	n, err := e.encodeCounter(counterSnapshot)
	actual := buf.String()
	expected := "# TYPE requests counter\n" +
		`requests{request_type="GET",status_code="200"} 42` + "\n"
	assert.Nil(t, err)
	assert.Equal(t, n, len(expected))
	assert.Equal(t, expected, actual)
}

type mockGaugeSnapshot struct {
	*mockMetadata
	mockValue func() float64
}

func (m *mockGaugeSnapshot) Value() float64 {
	if m.mockValue != nil {
		return m.mockValue()
	}

	return -1
}

var gaugeSnapshot = &mockGaugeSnapshot{
	mockMetadata: metadata,
	mockValue:    func() float64 { return 21.5 },
}

func TestEncodeGauge(t *testing.T) {
	var buf bytes.Buffer
	e := &encoder{w: &buf}

	n, err := e.encodeGauge(gaugeSnapshot)
	actual := buf.String()
	expected := "# TYPE requests gauge\n" +
		`requests{request_type="GET",status_code="200"} 21.5` + "\n"
	assert.Nil(t, err)
	assert.Equal(t, n, len(expected))
	assert.Equal(t, expected, actual)
}

type mockTimerSnapshot struct {
	*mockMetadata
	mockValues func() []time.Duration
}

func (m *mockTimerSnapshot) Values() []time.Duration {
	if m.mockValues != nil {
		return m.mockValues()
	}

	return nil
}

var timerSnapshot = &mockTimerSnapshot{
	mockMetadata: metadata,
	mockValues: func() []time.Duration {
		return []time.Duration{time.Second, time.Minute, 5 * time.Second}
	},
}

func TestEncodeTimer(t *testing.T) {
	var buf bytes.Buffer
	e := &encoder{w: &buf}

	n, err := e.encodeTimer(timerSnapshot)
	actual := buf.String()
	expected := "# TYPE requests timer\n" +
		`requests{request_type="GET",status_code="200"} [1s 1m0s 5s]` + "\n"
	assert.Nil(t, err)
	assert.Equal(t, n, len(expected))
	assert.Equal(t, expected, actual)
}

type mockHistogramSnapshot struct {
	*mockMetadata
	mockValues    func() map[float64]int64
	mockDurations func() map[time.Duration]int64
}

func (m *mockHistogramSnapshot) Values() map[float64]int64 {
	if m.mockValues != nil {
		return m.mockValues()
	}

	return nil
}

func (m *mockHistogramSnapshot) Durations() map[time.Duration]int64 {
	if m.mockDurations != nil {
		return m.mockDurations()
	}

	return nil
}

var valuesHistogramSnapshot = &mockHistogramSnapshot{
	mockMetadata: metadata,
	mockValues: func() map[float64]int64 {
		return map[float64]int64{
			0:               0,
			2:               1,
			4:               0,
			math.MaxFloat64: 1,
		}
	},
}

var durationsHistogramSnapshot = &mockHistogramSnapshot{
	mockMetadata: metadata,
	mockDurations: func() map[time.Duration]int64 {
		return map[time.Duration]int64{
			0:               0,
			time.Second * 2: 1,
			time.Second * 4: 0,
			math.MaxInt64:   1,
		}
	},
}

func TestEncodeHistogram(t *testing.T) {
	var buf bytes.Buffer
	e := &encoder{w: &buf}

	n, err := e.encodeHistogram(valuesHistogramSnapshot)
	actual := buf.String()
	expected := "# TYPE requests histogram\n" +
		`requests{request_type="GET",status_code="200",le="0"} 0` + "\n" +
		`requests{request_type="GET",status_code="200",le="2"} 1` + "\n" +
		`requests{request_type="GET",status_code="200",le="4"} 0` + "\n" +
		`requests{request_type="GET",status_code="200",le="+Inf"} 1` + "\n"
	assert.Nil(t, err)
	assert.Equal(t, n, len(expected))
	assert.Equal(t, expected, actual)

	buf.Reset()

	n, err = e.encodeHistogram(durationsHistogramSnapshot)
	actual = buf.String()
	expected = "# TYPE requests histogram\n" +
		`requests{request_type="GET",status_code="200",le="0s"} 0` + "\n" +
		`requests{request_type="GET",status_code="200",le="2s"} 1` + "\n" +
		`requests{request_type="GET",status_code="200",le="4s"} 0` + "\n" +
		`requests{request_type="GET",status_code="200",le="+Inf"} 1` + "\n"
	assert.Nil(t, err)
	assert.Equal(t, n, len(expected))
	assert.Equal(t, expected, actual)
}

type mockSnapshot struct {
	mockCounters   func() map[string]tally.CounterSnapshot
	mockGauges     func() map[string]tally.GaugeSnapshot
	mockTimers     func() map[string]tally.TimerSnapshot
	mockHistograms func() map[string]tally.HistogramSnapshot
}

func (m *mockSnapshot) Counters() map[string]tally.CounterSnapshot {
	if m.mockCounters != nil {
		return m.mockCounters()
	}

	return nil
}

func (m *mockSnapshot) Gauges() map[string]tally.GaugeSnapshot {
	if m.mockGauges != nil {
		return m.mockGauges()
	}

	return nil
}

func (m *mockSnapshot) Timers() map[string]tally.TimerSnapshot {
	if m.mockTimers != nil {
		return m.mockTimers()
	}

	return nil
}

func (m *mockSnapshot) Histograms() map[string]tally.HistogramSnapshot {
	if m.mockHistograms != nil {
		return m.mockHistograms()
	}

	return nil
}

var (
	id       = tally.KeyForPrefixedStringMap(metadata.Name(), metadata.Tags())
	snapshot = &mockSnapshot{
		mockCounters: func() map[string]tally.CounterSnapshot {
			return map[string]tally.CounterSnapshot{id: counterSnapshot}
		},
		mockGauges: func() map[string]tally.GaugeSnapshot {
			return map[string]tally.GaugeSnapshot{id: gaugeSnapshot}
		},
		mockTimers: func() map[string]tally.TimerSnapshot {
			return map[string]tally.TimerSnapshot{id: timerSnapshot}
		},
		mockHistograms: func() map[string]tally.HistogramSnapshot {
			return map[string]tally.HistogramSnapshot{id: valuesHistogramSnapshot}
		},
	}
)

func TestEncode(t *testing.T) {
	var buf bytes.Buffer
	e := &encoder{w: &buf}

	n, err := e.encode(snapshot)
	actual := buf.String()
	expected := "# TYPE requests counter\n" +
		`requests{request_type="GET",status_code="200"} 42` + "\n" +
		"# TYPE requests gauge\n" +
		`requests{request_type="GET",status_code="200"} 21.5` + "\n" +
		"# TYPE requests timer\n" +
		`requests{request_type="GET",status_code="200"} [1s 1m0s 5s]` + "\n" +
		"# TYPE requests histogram\n" +
		`requests{request_type="GET",status_code="200",le="0"} 0` + "\n" +
		`requests{request_type="GET",status_code="200",le="2"} 1` + "\n" +
		`requests{request_type="GET",status_code="200",le="4"} 0` + "\n" +
		`requests{request_type="GET",status_code="200",le="+Inf"} 1` + "\n"
	assert.Nil(t, err)
	assert.Equal(t, n, len(expected))
	assert.Equal(t, expected, actual)

	buf.Reset()
}
