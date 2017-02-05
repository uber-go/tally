package prometheus

import (
	"errors"
	"net/http"
	"sync"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/uber-go/tally"
)

var (
	errorAlreadyRegistered = errors.New("metric already registered")
	// DefaultHistogramObjectives is the default objectives used when creating a new Summary histogram
	// in the prometheus registry.
	// See https://godoc.org/github.com/prometheus/client_golang/prometheus#SummaryOpts
	DefaultHistogramObjectives = map[float64]float64{
		0.5:   0.05,
		0.9:   0.01,
		0.99:  0.001,
		0.999: 0.0001,
	}
)

type metricID string

// Reporter is a Prometheus backed tally reporter.
type Reporter interface {
	tally.StatsReporter

	// HTTPHandler provides the Prometheus HTTP scrape handler.
	HTTPHandler() http.Handler

	// RegisterCounter is a helper method to initialize a counter
	// in the prometheus backend with a given help text.
	// If not called explicitly, the Reporter will create one for
	// you on first use, with a not super helpful HELP string.
	RegisterCounter(
		name string,
		tags map[string]string,
		desc string,
	) (*prom.CounterVec, error)

	// RegisterGauge is a helper method to initialize a gauge
	// in the prometheus backend with a given help text.
	// If not called explicitly, the Reporter will create one for
	// you on first use, with a not super helpful HELP string.
	RegisterGauge(
		name string,
		tags map[string]string,
		desc string,
	) (*prom.GaugeVec, error)

	// RegisterTimer is a helper method to initialize a Timer
	// histogram vector in the prometheus backend with a given help text.
	// If not called explicitly, the Reporter will create one for
	// you on first use, with a not super helpful HELP string.
	RegisterTimer(
		name string,
		tags map[string]string,
		desc string,
		objectives map[float64]float64,
	) (*prom.SummaryVec, error)
}

type reporter struct {
	objectives map[float64]float64
	counters   map[metricID]*prom.CounterVec
	gauges     map[metricID]*prom.GaugeVec
	summaries  map[metricID]*prom.SummaryVec
	sync.RWMutex
}

func (r *reporter) HTTPHandler() http.Handler {
	return promhttp.Handler()
}

// NewReporter returns a new Reporter for Prometheus client backed metrics
// objectives is the objectives used when creating a new Summary histogram for Timers. See
// https://godoc.org/github.com/prometheus/client_golang/prometheus#SummaryOpts for more details.
func NewReporter(objectives map[float64]float64) Reporter {
	counters := map[metricID]*prom.CounterVec{}
	gauges := map[metricID]*prom.GaugeVec{}
	summaries := map[metricID]*prom.SummaryVec{}
	if objectives == nil {
		objectives = DefaultHistogramObjectives
	}
	return &reporter{
		counters:   counters,
		gauges:     gauges,
		summaries:  summaries,
		objectives: objectives,
	}
}

func (r *reporter) RegisterCounter(
	name string,
	tags map[string]string,
	desc string,
) (*prom.CounterVec, error) {
	ctr := &prom.CounterVec{}
	id := canonicalMetricID(name, tags)
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
	r.Lock()
	defer r.Unlock()
	r.counters[id] = ctr
	return ctr, nil
}

// ReportCounter implements tally.StatsReporter.
func (r *reporter) ReportCounter(name string, tags map[string]string, value int64) {
	id := canonicalMetricID(name, tags)

	r.RLock()
	ctr, ok := r.counters[id]
	r.RUnlock()

	if !ok {
		var err error
		ctr, err = r.RegisterCounter(name, tags, name+" counter")
		if err != nil {
			panic(err)
		}
	}
	ctr.With(tags).Add(float64(value))
}

func (r *reporter) RegisterGauge(
	name string,
	tags map[string]string,
	desc string,
) (*prom.GaugeVec, error) {
	g := &prom.GaugeVec{}
	id := canonicalMetricID(name, tags)
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
	r.Lock()
	defer r.Unlock()
	r.gauges[id] = g
	return g, nil
}

// ReportGauge implements tally.StatsReporter.
func (r *reporter) ReportGauge(name string, tags map[string]string, value float64) {
	id := canonicalMetricID(name, tags)

	r.RLock()
	g, ok := r.gauges[id]
	r.RUnlock()

	if !ok {
		var err error
		g, err = r.RegisterGauge(name, tags, name+" gauge")
		if err != nil {
			panic(err)
		}
	}
	g.With(tags).Set(value)
}

func (r *reporter) RegisterTimer(
	name string,
	tags map[string]string,
	desc string,
	objectives map[float64]float64,
) (*prom.SummaryVec, error) {
	h := &prom.SummaryVec{}
	id := canonicalMetricID(name, tags)
	exists := r.hasSummary(id)
	if exists {
		return h, errorAlreadyRegistered
	}
	labelKeys := keysFromMap(tags)

	if objectives == nil {
		objectives = r.objectives
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
	r.Lock()
	defer r.Unlock()
	r.summaries[id] = h
	return h, nil
}

// ReportTimer implements tally.StatsReporter. It
// reports a timer value into the Summary histogram.
func (r *reporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	id := canonicalMetricID(name, tags)

	r.RLock()
	h, ok := r.summaries[id]
	r.RUnlock()

	if !ok {
		var err error
		h, err = r.RegisterTimer(name, tags, name+" histogram in seconds", nil)
		if err != nil {
			panic(err)
		}
	}
	h.With(tags).Observe(float64(interval))
}

func (r *reporter) Capabilities() tally.Capabilities {
	return r
}

func (r *reporter) Reporting() bool {
	return true
}

func (r *reporter) Tagging() bool {
	return true
}

// Flush does nothing for prometheus
func (r *reporter) Flush() {}

// NOTE: this generates a canonical MetricID for a given name+label keys,
// not values. This omits label values, as we track metrics as
// Vectors in order to support on-the-fly label changes.
func canonicalMetricID(name string, tags map[string]string) metricID {
	return metricID(tally.KeyForPrefixedStringMap(name, tags))
}

func (r *reporter) hasCounter(id metricID) (exists bool) {
	r.RLock()
	defer r.RUnlock()
	_, exists = r.counters[id]
	return
}

func (r *reporter) hasGauge(id metricID) (exists bool) {
	r.RLock()
	defer r.RUnlock()
	_, exists = r.gauges[id]
	return
}

func (r *reporter) hasSummary(id metricID) (exists bool) {
	r.RLock()
	defer r.RUnlock()
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
