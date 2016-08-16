package statsd

import (
	"sync"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
	"github.com/uber-go/tally"
)

type cactusStatsReporter struct {
	statter  statsd.Statter
	sm       sync.Mutex
	scopes   []tally.Scope
	interval time.Duration
	quit     chan struct{}
}

// NewCactusStatsReporter returns a new StatsReporter that creates a buffered client to a Statsd backend
func NewCactusStatsReporter(statsd statsd.Statter, interval time.Duration) tally.StatsReporter {
	return &cactusStatsReporter{
		quit:     make(chan struct{}),
		statter:  statsd,
		interval: interval,
		scopes:   make([]tally.Scope, 0),
	}
}

func (r *cactusStatsReporter) Start() {
	ticker := time.NewTicker(r.interval)
	for {
		select {
		case <-ticker.C:
			for _, scope := range r.scopes {
				scope.Report(r)
			}
		case <-r.quit:
			return
		}
	}
}

func (r *cactusStatsReporter) registerScope(s tally.Scope) {
	r.sm.Lock()
	r.scopes = append(r.scopes, s)
	r.sm.Unlock()
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
