package datadog

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tj/assert"
	"github.com/uber-go/tally"
)

func newHandler(t *testing.T, ch chan []*metric) func(req *http.Request) (*http.Response, error) {
	return func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		data, err := ioutil.ReadAll(req.Body)
		assert.Nil(t, err)

		s := series{}
		assert.Nil(t, json.Unmarshal(data, &s))
		ch <- s.Metrics

		w := httptest.NewRecorder()
		w.WriteHeader(http.StatusOK)
		return w.Result(), nil
	}
}

func TestCapabilities(t *testing.T) {
	reporter, err := New("blah", HandlerFunc(func(req *http.Request) (*http.Response, error) {
		return httptest.NewRecorder().Result(), nil
	}))
	assert.Nil(t, err)

	scope, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: reporter}, time.Second)
	defer closer.Close()

	c := scope.Capabilities()
	assert.NotNil(t, c)
	assert.False(t, c.Reporting())
	assert.True(t, c.Tagging())
}

func TestDatadog(t *testing.T) {
	apiKey := "blah"

	t.Run("gauge", func(t *testing.T) {
		ch := make(chan []*metric)
		defer close(ch)

		reporter, err := New(apiKey, HandlerFunc(newHandler(t, ch)))
		assert.Nil(t, err)

		scope, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: reporter}, time.Second)
		defer closer.Close()
		scope = scope.Tagged(map[string]string{"hello": "world"})

		// Given
		name := "sample"
		value := 1.0

		// When
		g := scope.Gauge(name)
		g.Update(value)

		// Then
		assert.Nil(t, closer.Close())

		metrics := <-ch // wait for content to arrive

		assert.Len(t, metrics, 1)
		assertMetric(t, metrics[0], name, typeGauge, value, "hello:world")
	})

	t.Run("counter", func(t *testing.T) {
		ch := make(chan []*metric)
		defer close(ch)

		reporter, err := New(apiKey, HandlerFunc(newHandler(t, ch)))
		assert.Nil(t, err)

		scope, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: reporter}, time.Second)
		defer closer.Close()
		scope = scope.Tagged(map[string]string{"hello": "world"})

		// Given
		name := "sample"
		value := int64(2)

		// When
		g := scope.Counter(name)
		g.Inc(value)

		// Then
		assert.Nil(t, closer.Close())

		metrics := <-ch // wait for content to arrive

		assert.Len(t, metrics, 1)
		assertMetric(t, metrics[0], name, typeCounter, float64(value), "hello:world")
	})

	t.Run("timer", func(t *testing.T) {
		ch := make(chan []*metric)
		defer close(ch)

		reporter, err := New(apiKey, HandlerFunc(newHandler(t, ch)))
		assert.Nil(t, err)

		scope, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: reporter}, time.Second)
		defer closer.Close()
		scope = scope.Tagged(map[string]string{"hello": "world"})

		// Given
		name := "sample"
		value := time.Duration(time.Second)

		// When
		g := scope.Timer(name)
		g.Record(value)

		// Then
		assert.Nil(t, closer.Close())

		metrics := <-ch // wait for content to arrive

		assert.Len(t, metrics, 1)
		assertMetric(t, metrics[0], name, typeTimer, 1, "hello:world")
	})

	t.Run("histogram value", func(t *testing.T) {
		ch := make(chan []*metric)
		defer close(ch)

		reporter, err := New(apiKey, HandlerFunc(newHandler(t, ch)))
		assert.Nil(t, err)

		scope, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: reporter}, time.Second)
		defer closer.Close()
		scope = scope.Tagged(map[string]string{"hello": "world"})

		// Given
		name := "sample"
		value := 1.0

		// When
		g := scope.Histogram(name, tally.ValueBuckets{0, 1.0, 2.0})
		g.RecordValue(value)
		g.RecordValue(value)

		// Then
		assert.Nil(t, closer.Close())

		metrics := <-ch // wait for content to arrive

		assert.Len(t, metrics, 3)
		assertMetric(t, metrics[0], name+".min", typeGauge, 0, "hello:world")
		assertMetric(t, metrics[1], name+".max", typeGauge, value, "hello:world")
		assertMetric(t, metrics[2], name+".count", typeRate, 2, "hello:world")
	})

	t.Run("histogram duration", func(t *testing.T) {
		ch := make(chan []*metric)
		defer close(ch)

		reporter, err := New(apiKey, HandlerFunc(newHandler(t, ch)))
		assert.Nil(t, err)

		scope, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: reporter}, time.Second)
		defer closer.Close()
		scope = scope.Tagged(map[string]string{"hello": "world"})

		// Given
		name := "sample"
		value := time.Second

		// When
		g := scope.Histogram(name, tally.DurationBuckets{0, time.Second, time.Minute})
		g.RecordDuration(value)
		g.RecordDuration(value)

		// Then
		assert.Nil(t, closer.Close())

		metrics := <-ch // wait for content to arrive

		assert.Len(t, metrics, 3)
		assertMetric(t, metrics[0], name+".min", typeGauge, 0, "hello:world")
		assertMetric(t, metrics[1], name+".max", typeGauge, 1, "hello:world")
		assertMetric(t, metrics[2], name+".count", typeRate, 2, "hello:world")
	})
}

func TestBufferSize(t *testing.T) {
	ch := make(chan []*metric)
	defer close(ch)

	reporter, err := New("blah", HandlerFunc(newHandler(t, ch)), BufferSize(1))
	assert.Nil(t, err)

	scope, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: reporter}, time.Hour)
	defer closer.Close()

	// When
	timer := scope.Timer("name")
	timer.Record(time.Second)

	// Then
	metrics := <-ch
	assert.Len(t, metrics, 1)
	assertMetric(t, metrics[0], "name", typeTimer, 1.0)
}

func TestOptions(t *testing.T) {
	t.Run("BufferSize", func(t *testing.T) {
		bufSize := 123
		opts := options{}
		BufferSize(bufSize)(&opts)
		assert.EqualValues(t, bufSize, opts.bufferSize)
	})

	t.Run("Output", func(t *testing.T) {
		opts := options{}
		Debug(os.Stdout)(&opts)
		assert.EqualValues(t, os.Stdout, opts.writer)
	})
}

func assertMetric(t *testing.T, m *metric, name, metricType string, value float64, tags ...string) {
	assert.EqualValues(t, name, m.Name)
	assert.EqualValues(t, metricType, m.Type)

	if len(tags) > 0 {
		assert.EqualValues(t, tags, m.Tags)
	}
	assert.Len(t, m.Points, 1)
	assert.Len(t, m.Points[0], 2)
	assert.NotZero(t, m.Points[0][0])
	assert.EqualValues(t, value, m.Points[0][1])
}

func TestLive(t *testing.T) {
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		t.SkipNow()
	}

	reporter, err := New(apiKey)
	assert.Nil(t, err)

	scope, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: reporter}, time.Second)
	defer closer.Close()

	gauge := scope.Gauge("test.gauge")
	gauge.Update(1)

	counter := scope.Counter("test.counter")
	counter.Inc(2)

	timer := scope.Timer("test.timer")
	timer.Record(time.Second * 3)

	value := scope.Histogram("test.histogram.value", tally.ValueBuckets{0, 5, 10})
	value.RecordValue(4)

	duration := scope.Histogram("test.histogram.duration", tally.DurationBuckets{0, time.Second * 10, time.Second * 20})
	duration.RecordDuration(5 * time.Second)

	time.Sleep(time.Second * 5)
}

func BenchmarkDatadog(t *testing.B) {
	resp := &http.Response{
		Body: ioutil.NopCloser(strings.NewReader("")),
	}

	reporter, err := New("blah", HandlerFunc(func(req *http.Request) (*http.Response, error) {
		return resp, nil
	}))
	assert.Nil(t, err)

	scope, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: reporter}, time.Millisecond*250)
	defer closer.Close()

	timer := scope.Timer("test.timer")

	for i := 0; i < t.N; i++ {
		timer.Record(time.Second)
	}
}
