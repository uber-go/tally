package metrics

// Copied from code.uber.internal:go-common.git at version 139e3b5b4b4b775ff9ed8abb2a9f31b7bd1aad58

import (
	"time"

	"github.com/uber-common/bark"
	"github.com/uber-go/tally"
)

// MetricType is the type of metric being emitted (counter/gauge/timer)
type MetricType int

const (
	// CounterType is a statsd style counter
	CounterType MetricType = iota + 1
	// TimerType is a statsd style timer
	TimerType
	// GaugeType is a statsd style gauge
	GaugeType
)

// A Backend is responsible for storing and aggregating metrics.  We offer both
// a local backend (which stores metric values in-vm, and allows gathering
// periodic snapshots to send to a remote system) and a statsd backend (which
// immediately forwards all metrics to statsd)
//
// The implementations of the backend guarantee that their reporting calls are
// efficient enough so they won't add significant latencies to the caller (e.g.,
// by running blocking IO in separate goroutines).
type Backend interface {
	bark.StatsReporter

	// Capabilities returns a description of metrics capabilities
	Capabilities() Capabilities
}

// Capabilities is a description of metrics capabilities
type Capabilities interface {
	// Tagging returns whether the backend has the capability for tagging metrics
	Tagging() bool
}

// MetricID is an ID of a metric that is registered with a backend
type MetricID interface {
	// UnregisterID
	UnregisterID()
}

// A BufferedBackend acts like a backend, but it provides the ability to buffer
// counters, gauges and timers values and also allows these to be cached so a new
// metric object doesn't have to be created on each emission
type BufferedBackend interface {
	tally.CachedStatsReporter

	// RegisterForID generates a unique ID for a metric given
	// it's name and tags and registers the id with the backend.
	// This ID is then used to emit metric values.
	RegisterForID(name string, tags bark.Tags, t MetricType) MetricID

	// GetForID checks whether there is a metric for the given
	// name and tags and if so returns that.
	GetForID(name string, tags bark.Tags, t MetricType) (MetricID, bool)

	// Increment a statsd-like counter.
	IncCounter(id MetricID, value int64)

	// Increment a statsd-like gauge ("set" of the value).
	UpdateGauge(id MetricID, value int64)

	// Record a statsd-like timer.
	RecordTimer(id MetricID, d time.Duration)

	// Close closes the buffered backend
	Close()
}
