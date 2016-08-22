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
		scopes:   make([]tally.Scope, 4),
	}
}

// RegisterScope should be called at setup. The reporter will periodically tell any registered
// Scopes to dump their collected stats the the cactusStatsReporter. Scopes must be registered
// before running Start()
func (r *cactusStatsReporter) RegisterScope(s tally.Scope) {
	r.sm.Lock()
	r.scopes = append(r.scopes, s)
	r.sm.Unlock()
}

// Start begins the stats reporter loop. Run this after registering one or more scopes.
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

func (r *cactusStatsReporter) ReportCounter(name string, tags map[string]string, value int64) {
	r.statter.Inc(name, value, 1.0)
}

func (r *cactusStatsReporter) ReportGauge(name string, tags map[string]string, value int64) {
	r.statter.Gauge(name, value, 1.0)
}

func (r *cactusStatsReporter) ReportTimer(name string, tags map[string]string, interval time.Duration) {
	r.statter.TimingDuration(name, interval, 1.0)
}
