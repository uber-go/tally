package prometheus

import (
	"crypto/sha1"
	"fmt"
	"net/http"
	"time"

	"github.com/byxorna/tally"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	errorAlreadyRegistered = fmt.Errorf("Metric already registered")
)

type metricID string
type PromReporter struct {
	counters  map[metricID]*prom.CounterVec
	gauges    map[metricID]*prom.GaugeVec
	summaries map[metricID]*prom.SummaryVec
}

func NewPrometheusReporter() PromReporter {
	counters := map[metricID]*prom.CounterVec{}
	gauges := map[metricID]*prom.GaugeVec{}
	summaries := map[metricID]*prom.SummaryVec{}
	reporter := PromReporter{
		counters:  counters,
		gauges:    gauges,
		summaries: summaries,
	}

	http.Handle("/metrics", promhttp.Handler())
	return reporter
}

func (r *PromReporter) RegisterCounter(name string, tags map[string]string, desc string) (*prom.CounterVec, error) {
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
	r.counters[id] = ctr
	return ctr, nil
}

func (r *PromReporter) ReportCounter(name string, tags map[string]string, value int64) {
	id := hashMetricLabelsToID(name, tags)
	ctr, ok := r.counters[id]
	if !ok {
		ctr, _ = r.RegisterCounter(name, tags, name+" counter")
	}
	ctr.With(tags).Add(float64(value))
}

func (r *PromReporter) RegisterGauge(name string, tags map[string]string, desc string) (*prom.GaugeVec, error) {
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
	r.gauges[id] = g
	return g, nil
}

func (r *PromReporter) ReportGauge(name string, tags map[string]string, value float64) {
	id := hashMetricLabelsToID(name, tags)
	g, ok := r.gauges[id]
	if !ok {
		g, _ = r.RegisterGauge(name, tags, name+" gauge")
	}
	g.With(tags).Set(value)
}

// RegisterTimer registers a timer with the given Summary Objectives. See https://godoc.org/github.com/prometheus/client_golang/prometheus#SummaryOpts
// an empty slice for objectives uses {0.5: 0.05, 0.9: 0.01, 0.99: 0.001, 0.999: 0.0001}
func (r *PromReporter) RegisterTimer(name string, tags map[string]string, desc string, objectives map[float64]float64) (*prom.SummaryVec, error) {
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
	r.summaries[id] = h
	return h, nil
}

func (r *PromReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	id := hashMetricLabelsToID(name, tags)
	h, ok := r.summaries[id]
	if !ok {
		h, _ = r.RegisterTimer(name, tags, name+" histogram in seconds", nil)
	}
	h.With(tags).Observe(float64(interval))
}

func (r *PromReporter) Capabilities() tally.Capabilities {
	return r
}

func (r *PromReporter) Reporting() bool {
	return false
}

func (r *PromReporter) Tagging() bool {
	return true
}

func (r *PromReporter) Flush() {
	// no-op
}

// NOTE: this hashes name+label keys, not values, as we track everything as a *Vec
func hashMetricLabelsToID(name string, tags map[string]string) metricID {
	hasher := sha1.New()
	hasher.Write([]byte(name + "{"))
	for k, _ := range tags {
		hasher.Write([]byte(k + ","))
	}
	hasher.Write([]byte("}"))
	return metricID(string(hasher.Sum(nil)))
}

func (r *PromReporter) hasCounter(id metricID) (exists bool) {
	_, exists = r.counters[id]
	return
}

func (r *PromReporter) hasGauge(id metricID) (exists bool) {
	_, exists = r.gauges[id]
	return
}

func (r *PromReporter) hasSummary(id metricID) (exists bool) {
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
