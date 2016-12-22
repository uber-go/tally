package prometheus

import (
	"crypto/sha1"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/byxorna/tally"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	errorAlreadyRegistered = fmt.Errorf("Metric already registered")
)

type metricID string

// Reporter is a prometheus backed tally reporter
type Reporter struct {
	counters  map[metricID]*prom.CounterVec
	gauges    map[metricID]*prom.GaugeVec
	summaries map[metricID]*prom.SummaryVec
	mtx       sync.RWMutex
}

// HTTPHandler returns the prometheus HTTP handler for serving metrics
func (r *Reporter) HTTPHandler() http.Handler {
	return promhttp.Handler()
}

// NewReporter returns a new Reporter for Prometheus client backed metrics
func NewReporter() Reporter {
	counters := map[metricID]*prom.CounterVec{}
	gauges := map[metricID]*prom.GaugeVec{}
	summaries := map[metricID]*prom.SummaryVec{}
	reporter := Reporter{
		counters:  counters,
		gauges:    gauges,
		summaries: summaries,
		mtx:       sync.RWMutex{},
	}

	return reporter
}

// RegisterCounter is a helper method to initialize a counter in the prometheus backend with a given help text.
// If not called explicitly, the Reporter will create one for you on first use, with a not super helpful HELP string
func (r *Reporter) RegisterCounter(name string, tags map[string]string, desc string) (*prom.CounterVec, error) {
	ctr := &prom.CounterVec{}
	id := hashMetricLabelsToID(name, tags)
	exists := r.hasCounter(id)
	if exists {
		return ctr, errorAlreadyRegistered
	}
	labelKeys := keysFromMap(tags)
	ctr = prom.NewCounterVec(
		prom.CounterOpts{
			Name: name,
			Help: desc,
		},
		labelKeys,
	)
	err := prom.Register(ctr)
	if err != nil {
		return ctr, err
	}
	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.counters[id] = ctr
	return ctr, nil
}

// ReportCounter reports a counter value
func (r *Reporter) ReportCounter(name string, tags map[string]string, value int64) {
	id := hashMetricLabelsToID(name, tags)

	r.mtx.RLock()
	ctr, ok := r.counters[id]
	r.mtx.RUnlock()

	if !ok {
		var err error
		ctr, err = r.RegisterCounter(name, tags, name+" counter")
		if err != nil {
			panic(err)
		}
	}
	ctr.With(tags).Add(float64(value))
}

// RegisterGauge is a helper method to initialize a gauge in the prometheus backend with a given help text.
// If not called explicitly, the Reporter will create one for you on first use, with a not super helpful HELP string
func (r *Reporter) RegisterGauge(name string, tags map[string]string, desc string) (*prom.GaugeVec, error) {
	g := &prom.GaugeVec{}
	id := hashMetricLabelsToID(name, tags)
	exists := r.hasGauge(id)
	if exists {
		return g, errorAlreadyRegistered
	}
	labelKeys := keysFromMap(tags)

	g = prom.NewGaugeVec(
		prom.GaugeOpts{
			Name: name,
			Help: desc,
		},
		labelKeys,
	)
	err := prom.Register(g)
	if err != nil {
		return g, err
	}
	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.gauges[id] = g
	return g, nil
}

// ReportGauge reports a gauge value
func (r *Reporter) ReportGauge(name string, tags map[string]string, value float64) {
	id := hashMetricLabelsToID(name, tags)

	r.mtx.RLock()
	g, ok := r.gauges[id]
	r.mtx.RUnlock()

	if !ok {
		var err error
		g, err = r.RegisterGauge(name, tags, name+" gauge")
		if err != nil {
			panic(err)
		}
	}
	g.With(tags).Set(value)
}

// RegisterTimer is a helper method to initialize a Timer histogram vector in the prometheus backend with a given help text.
// If not called explicitly, the Reporter will create one for you on first use, with a not super helpful HELP string
// objectives is the prometheus.SummaryVec objectives. Default, if nil passed is {0.5: 0.05, 0.9: 0.01, 0.99: 0.001, 0.999: 0.0001}
// See https://godoc.org/github.com/prometheus/client_golang/prometheus#SummaryOpts
func (r *Reporter) RegisterTimer(name string, tags map[string]string, desc string, objectives map[float64]float64) (*prom.SummaryVec, error) {
	h := &prom.SummaryVec{}
	id := hashMetricLabelsToID(name, tags)
	exists := r.hasSummary(id)
	if exists {
		return h, errorAlreadyRegistered
	}
	labelKeys := keysFromMap(tags)

	if objectives == nil {
		objectives = map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001, 0.999: 0.0001}
	}
	h = prom.NewSummaryVec(
		prom.SummaryOpts{
			Name:       name,
			Help:       desc,
			Objectives: objectives,
		},
		labelKeys,
	)
	err := prom.Register(h)
	if err != nil {
		return h, err
	}
	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.summaries[id] = h
	return h, nil
}

// ReportTimer reports a timer value into the Summary histogram
func (r *Reporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	id := hashMetricLabelsToID(name, tags)

	r.mtx.RLock()
	h, ok := r.summaries[id]
	r.mtx.RUnlock()

	if !ok {
		var err error
		h, err = r.RegisterTimer(name, tags, name+" histogram in seconds", nil)
		if err != nil {
			panic(err)
		}
	}
	h.With(tags).Observe(float64(interval))
}

// Capabilities ...
func (r *Reporter) Capabilities() tally.Capabilities {
	return r
}

// Reporting ...
func (r *Reporter) Reporting() bool {
	return false
}

// Tagging indicates prometheus supports tagged metrics
func (r *Reporter) Tagging() bool {
	return true
}

// Flush does nothing for prometheus
func (r *Reporter) Flush() {}

// NOTE: this hashes name+label keys, not values, as we track metrics as Vectors, to support on-the-fly label changes
func hashMetricLabelsToID(name string, tags map[string]string) metricID {
	str := name + "{"
	hasher := sha1.New()
	ts := keysFromMap(tags)
	sort.Strings(ts)
	for _, k := range ts {
		str = str + k + ","
	}
	str = str + "}"

	return metricID(string(hasher.Sum([]byte(str))))
}

func (r *Reporter) hasCounter(id metricID) (exists bool) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	_, exists = r.counters[id]
	return
}

func (r *Reporter) hasGauge(id metricID) (exists bool) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	_, exists = r.gauges[id]
	return
}

func (r *Reporter) hasSummary(id metricID) (exists bool) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	_, exists = r.summaries[id]
	return
}

func keysFromMap(m map[string]string) []string {
	labelKeys := make([]string, len(m))
	i := 0
	for k := range m {
		labelKeys[i] = k
		i++
	}
	return labelKeys
}
