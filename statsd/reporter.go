package statsd

import (
	"sync"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
	"github.com/uber-go/tally"
)

type cactusStatsReporter struct {
	statter statsd.Statter
	sm      sync.Mutex
	quit    chan struct{}
}

// NewStatsdReporter wraps a statsd.Statter for use with tally. Use either statsd.NewClient or statsd.NewBufferedClient.
func NewStatsdReporter(statsd statsd.Statter) tally.StatsReporter {
	return &cactusStatsReporter{
		quit:    make(chan struct{}),
		statter: statsd,
	}
}

func (r *cactusStatsReporter) ReportCounter(name string, tags map[string]string, value int64) {
	r.statter.Inc(name, value, 1.0)
}

func (r *cactusStatsReporter) ReportGauge(name string, tags map[string]string, value int64) {
	r.statter.Gauge(name, value, 1.0)
}

func (r *cactusStatsReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	r.statter.TimingDuration(name, interval, 1.0)
}

func (r *cactusStatsReporter) Capabilities() tally.Capabilities {
	return r
}

func (r *cactusStatsReporter) Reporting() bool {
	return true
}

func (r *cactusStatsReporter) Tagging() bool {
	return false
}

func (r *cactusStatsReporter) Flush() {
	// no-op
}
