package datadog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/uber-go/tally"
)

const (
	// DefaultBufferSize contains the number of metrics the datadog reporter
	// will capture before forcing a flush
	DefaultBufferSize = 512
)

const (
	typeGauge   = "gauge"
	typeRate    = "rate"
	typeCounter = "counter"
	typeTimer   = "timer"
)

var (
	poolMetric = &sync.Pool{
		New: func() interface{} {
			return &metric{}
		},
	}
)

type metric struct {
	Name   string      `json:"metric"`
	Points [][]float64 `json:"points"`
	Type   string      `json:"type"`
	Host   string      `json:"host,omitempty"`
	Tags   []string    `json:"tags,omitempty"`
}

func (m *metric) Reset() {
	m.Name = ""
	m.Points = nil
	m.Type = ""
	m.Host = ""
	m.Tags = nil
}

type series struct {
	Metrics []*metric `json:"series"`
}

type Reporter struct {
	metrics     []*metric
	bufSize     int
	offset      int
	handlerFunc func(*http.Request) (*http.Response, error)
	output      io.Writer
	mux         *sync.Mutex
	endpoint    string
}

// Reporting returns whether the reporter has the ability to actively report.
func (r *Reporter) Reporting() bool {
	return false
}

// Tagging returns whether the reporter has the capability for tagged metrics.
func (r *Reporter) Tagging() bool {
	return true
}

// Capabilities returns the capabilities description of the reporter.
func (r *Reporter) Capabilities() tally.Capabilities {
	return r
}

// post encoded metrics to datadog
func (r *Reporter) post(data []byte) error {
	req, err := http.NewRequest(http.MethodPost, r.endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.handlerFunc(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	io.Copy(r.output, resp.Body)
	io.WriteString(r.output, "\n")

	return nil
}

// submit the set of metrics to datadog. submit will retry the operation if
// unsuccessful
func (r *Reporter) submit(metrics []*metric) {
	s := series{Metrics: metrics}

	data, err := json.Marshal(s)
	if err != nil {
		return
	}

	for attempts := 0; attempts < 3; attempts++ {
		if err := r.post(data); err != nil {
			fmt.Fprintln(os.Stderr, "failed to connect to host")
			fmt.Fprintln(r.output, "failed to connect to host")
			time.Sleep(time.Second * 15)
			continue
		}

		break
	}

	for _, m := range metrics {
		m.Reset()
		poolMetric.Put(m)
	}
}

// flush is a thread-unsafe flush. assumes caller has already obtained lock
func (r *Reporter) flush() {
	if r.offset == 0 {
		return
	}

	var metrics []*metric
	metrics = append(metrics, r.metrics[0:r.offset]...)
	go r.submit(metrics)

	for i := 0; i < r.offset; i++ {
		r.metrics[i] = nil
	}

	r.offset = 0
}

// pushMetric provides a helper to push a metric onto the stack.  captures lock
func (r *Reporter) pushMetric(m *metric) {
	r.mux.Lock()
	defer r.mux.Unlock()

	r.metrics[r.offset] = m
	r.offset++

	if r.offset == r.bufSize {
		r.flush()
	}
}

// push is a helper function to push metrics onto the queue
func (r *Reporter) push(name, metricType string, tags map[string]string, value float64) {
	m := poolMetric.Get().(*metric)
	m.Name = name
	m.Type = metricType
	m.Points = [][]float64{
		{
			float64(time.Now().Unix()),
			value,
		},
	}
	for k, v := range tags {
		m.Tags = append(m.Tags, k+":"+v)
	}

	r.pushMetric(m)
}

// Flush asks the reporter to flush all reported values.
func (r *Reporter) Flush() {
	r.mux.Lock()
	defer r.mux.Unlock()

	r.flush()
}

// ReportCounter reports a counter value
func (r *Reporter) ReportCounter(
	name string,
	tags map[string]string,
	value int64,
) {
	r.push(name, typeCounter, tags, float64(value))
}

// ReportGauge reports a gauge value
func (r *Reporter) ReportGauge(
	name string,
	tags map[string]string,
	value float64,
) {
	r.push(name, typeGauge, tags, value)
}

// ReportTimer reports a timer value
func (r *Reporter) ReportTimer(
	name string,
	tags map[string]string,
	interval time.Duration,
) {
	r.push(name, typeTimer, tags, float64(interval)/float64(time.Second))
}

// ReportHistogramValueSamples reports histogram samples for a bucket
func (r *Reporter) ReportHistogramValueSamples(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
	bucketLowerBound,
	bucketUpperBound float64,
	samples int64,
) {
	r.push(name+".min", typeGauge, tags, bucketLowerBound)
	r.push(name+".max", typeGauge, tags, bucketUpperBound)
	r.push(name+".count", typeRate, tags, float64(samples))
}

// ReportHistogramDurationSamples reports histogram samples for a bucket
func (r *Reporter) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
	bucketLowerBound,
	bucketUpperBound time.Duration,
	samples int64,
) {
	r.push(name+".min", typeGauge, tags, float64(bucketLowerBound)/float64(time.Second))
	r.push(name+".max", typeGauge, tags, float64(bucketUpperBound)/float64(time.Second))
	r.push(name+".count", typeRate, tags, float64(samples))
}

// Options holds the option values
type options struct {
	bufferSize  int
	handlerFunc func(req *http.Request) (*http.Response, error)
	writer      io.Writer
}

// Option provides functional arguments to datadog
type Option func(*options)

// BufferSize specifies the size of the internal datadog buffer.  Once the
// buffer number of metrics is reached, metrics will be posted to datadog
func BufferSize(n int) Option {
	return func(o *options) {
		o.bufferSize = n
	}
}

// HandlerFunc allows the http transport to be overridden; useful for testing
func HandlerFunc(h func(req *http.Request) (*http.Response, error)) Option {
	return func(o *options) {
		o.handlerFunc = h
	}
}

// Debug writes the datadog response to the provided writer
func Debug(w io.Writer) Option {
	return func(o *options) {
		o.writer = w
	}
}

// New returns a new datadog reporter
func New(apiKey string, opts ...Option) (*Reporter, error) {
	endpoint := "https://app.datadoghq.com/api/v1/series?api_key=" + apiKey

	options := options{
		bufferSize:  DefaultBufferSize,
		handlerFunc: http.DefaultTransport.RoundTrip,
		writer:      ioutil.Discard,
	}
	for _, opt := range opts {
		opt(&options)
	}

	r := &Reporter{
		metrics:     make([]*metric, options.bufferSize),
		bufSize:     options.bufferSize,
		mux:         &sync.Mutex{},
		endpoint:    endpoint,
		handlerFunc: options.handlerFunc,
		output:      options.writer,
	}

	return r, nil
}
