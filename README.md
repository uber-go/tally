# :heavy_check_mark: tally [![GoDoc][doc-img]][doc] [![Build Status][ci-img]][ci] [![Coverage Status][cov-img]][cov]

Fast, buffered, hierarchical stats collection in Go.

## Installation
`go get -u github.com/uber-go/tally/v7`

## Abstract

Tally provides a common interface for emitting metrics, while letting you not worry about the velocity of metrics emission.

By default it buffers counters, gauges and histograms at a specified interval but does not buffer timer values.  This is primarily so timer values can have all their values sampled if desired and if not they can be sampled as summaries or histograms independently by a reporter.

## Structure

- Scope: Keeps track of metrics, and their common metadata.
- Metrics: Counters, Gauges, Timers, Histograms and Native Histograms.
- Reporter: Implemented by you. Accepts aggregated values from the scope. Forwards the aggregated values to your metrics ingestion pipeline.
  - The reporters already available listed alphabetically are:
	 - `github.com/uber-go/tally/v7/m3`: Report m3 metrics, timers are not sampled and forwarded directly.
	 - `github.com/uber-go/tally/v7/multi`: Report to multiple reporters, you can multi-write metrics to other reporters simply.
	 - `github.com/uber-go/tally/v7/prometheus`: Report prometheus metrics, timers by default are made summaries with an option to make them histograms instead.
	 - `github.com/uber-go/tally/v7/statsd`: Report statsd metrics, no support for tags.

### Basics

 - Scopes created with tally provide race-safe registration and use of all metric types `Counter`, `Gauge`, `Timer`, `Histogram`, `NativeValueHistogram` and `NativeDurationHistogram`.
 - `NewRootScope(...)` returns a `Scope` and `io.Closer`, the second return value is used to stop the scope's goroutine reporting values from the scope to it's reporter.  This is to reduce the footprint of `Scope` from the public API for those implementing it themselves to use in Go packages that take a tally `Scope`.

### Acquire a Scope ###
```go
reporter = NewMyStatsReporter()  // Implement as you will
tags := map[string]string{
	"dc": "east-1",
	"type": "master",
}
reportEvery := time.Second

scope := tally.NewRootScope(tally.ScopeOptions{
	Tags: tags,
	Reporter: reporter,
}, reportEvery)
```

### Get/Create a metric, use it ###
```go
// Get a counter, increment a counter
reqCounter := scope.Counter("requests")  // cache me
reqCounter.Inc(1)

queueGauge := scope.Gauge("queue_length")  // cache me
queueGauge.Update(42)
```

### Native histograms ###

A `Histogram` needs its buckets declared up front. A native histogram derives them from the observations instead, within a bucket budget, so you do not have to guess the shape of the distribution before you have seen it.

The tradeoff is how it travels. A classic histogram reports as one counter per bucket, which every metrics protocol can carry. A native histogram rescales its buckets as it observes values, so there is no stable set of bounds to report against, and the whole distribution is serialized into a single payload per interval.

Tally does not define that payload. You supply the accumulator and its encoding through `ScopeOptions.NativeHistogramFactory`, and the reporter you pair it with has to understand whatever that encoder produces:

```go
scope, closer := tally.NewRootScope(tally.ScopeOptions{
	Reporter:                         myNativeHistogramReporter,
	NativeHistogramFactory:           myEncoder.New,
	DefaultNativeHistogramMaxBuckets: 160,
}, time.Second)

scope.NativeValueHistogram("payload_bytes").RecordValue(2048)
scope.NativeDurationHistogram("latency").RecordDuration(120 * time.Millisecond)
```

Values and durations are separate metrics rather than two ways of writing to one. A native histogram has no declared buckets to signal which it holds, so mixing them would silently blend two scales into one distribution. Durations are recorded as milliseconds.

**None of the bundled reporters can transmit native histograms.** The m3 and statsd wire formats have no field an opaque payload can travel in, and prometheus cannot turn one into a `prometheus.Collector`. The feature is for reporters paired with a matching encoder.

Note that a scope drops native histograms it cannot encode or transmit, and says nothing when it does. The default is one such case: leaving `NativeHistogramFactory` unset keeps `NativeValueHistogram` and `NativeDurationHistogram` safe to call, but their values are then only counted, never reported, with no error or warning to say so. Set the factory at the same time you set the reporter.

### Report your metrics ###
Use the inbuilt statsd reporter:

```go
import (
	"io"
	"github.com/cactus/go-statsd-client/v5/statsd"
	"github.com/uber-go/tally/v7"
	tallystatsd "github.com/uber-go/tally/v7/statsd"
	// ...
)

func newScope() (tally.Scope, io.Closer) {
	statter, _ := statsd.NewBufferedClient("127.0.0.1:8125",
		"stats", 100*time.Millisecond, 1440)

	reporter := tallystatsd.NewReporter(statter, tallystatsd.Options{
		SampleRate: 1.0,
	})

	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		Prefix:   "my-service",
		Tags:     map[string]string{},
		Reporter: reporter,
	}, time.Second)

	return scope, closer
}
```

Implement your own reporter using the `StatsReporter` interface:

```go

// BaseStatsReporter implements the shared reporter methods.
type BaseStatsReporter interface {
	Capabilities() Capabilities
	Flush()
}

// StatsReporter is a backend for Scopes to report metrics to.
type StatsReporter interface {
	BaseStatsReporter

	// ReportCounter reports a counter value
	ReportCounter(
		name string,
		tags map[string]string,
		value int64,
	)

	// ReportGauge reports a gauge value
	ReportGauge(
		name string,
		tags map[string]string,
		value float64,
	)

	// ReportTimer reports a timer value
	ReportTimer(
		name string,
		tags map[string]string,
		interval time.Duration,
	)

	// ReportHistogramValueSamples reports histogram samples for a bucket
	ReportHistogramValueSamples(
		name string,
		tags map[string]string,
		buckets Buckets,
		bucketLowerBound,
		bucketUpperBound float64,
		samples int64,
	)

	// ReportHistogramDurationSamples reports histogram samples for a bucket
	ReportHistogramDurationSamples(
		name string,
		tags map[string]string,
		buckets Buckets,
		bucketLowerBound,
		bucketUpperBound time.Duration,
		samples int64,
	)

	// ReportNativeHistogram reports a serialized native histogram covering
	// the samples observed since the last report. The payload encoding is
	// determined by the NativeHistogramData the scope was configured with.
	ReportNativeHistogram(
		name string,
		tags map[string]string,
		payload []byte,
		samples uint64,
	)
}
```

A `CachedStatsReporter` additionally pre-allocates a handle per metric with `AllocateNativeHistogram(name, tags, maxBuckets)`. There is no per-bucket equivalent of `CachedHistogramBucket`: a native histogram has no stable bounds to pre-allocate handles against, so the whole distribution is reported as one payload.

Or implement your own metrics implementation that matches the tally `Scope` interface to use different buffering semantics:

```go
type Scope interface {
	// Counter returns the Counter object corresponding to the name.
	Counter(name string) Counter

	// Gauge returns the Gauge object corresponding to the name.
	Gauge(name string) Gauge

	// Timer returns the Timer object corresponding to the name.
	Timer(name string) Timer

	// Histogram returns the Histogram object corresponding to the name.
	// To use default value and duration buckets configured for the scope
	// simply pass tally.DefaultBuckets or nil.
	// You can use tally.ValueBuckets{x, y, ...} for value buckets.
	// You can use tally.DurationBuckets{x, y, ...} for duration buckets.
	// You can use tally.MustMakeLinearValueBuckets(start, width, count) for linear values.
	// You can use tally.MustMakeLinearDurationBuckets(start, width, count) for linear durations.
	// You can use tally.MustMakeExponentialValueBuckets(start, factor, count) for exponential values.
	// You can use tally.MustMakeExponentialDurationBuckets(start, factor, count) for exponential durations.
	Histogram(name string, buckets Buckets) Histogram

	// NativeValueHistogram returns the NativeValueHistogram object
	// corresponding to the name.
	//
	// Unlike Histogram, bucket boundaries are derived from the observations
	// rather than declared up front, and the distribution is reported as a
	// single serialized payload. The bucket budget is
	// ScopeOptions.DefaultNativeHistogramMaxBuckets. Requires
	// ScopeOptions.NativeHistogramFactory to be set; without it the metric
	// accumulates but is never reported.
	NativeValueHistogram(name string) NativeValueHistogram

	// NativeDurationHistogram is NativeValueHistogram for durations, which it
	// records as milliseconds.
	//
	// Durations are a separate metric from values, not another way to write to
	// the same one, so a name used for both yields two distributions reported
	// under it -- as Counter and Gauge already do.
	NativeDurationHistogram(name string) NativeDurationHistogram

	// Tagged returns a new child scope with the given tags and current tags.
	Tagged(tags map[string]string) Scope

	// SubScope returns a new child scope appending a further name prefix.
	SubScope(name string) Scope

	// Capabilities returns a description of metrics reporting capabilities.
	Capabilities() Capabilities
}

// Capabilities is a description of metrics reporting capabilities.
type Capabilities interface {
	// Reporting returns whether the reporter has the ability to actively report.
	Reporting() bool

	// Tagging returns whether the reporter has the capability for tagged metrics.
	Tagging() bool
}
```

## Performance

This stuff needs to be fast. With that in mind, we avoid locks and unnecessary memory allocations.

```
BenchmarkCounterInc-8               	200000000	         7.68 ns/op
BenchmarkReportCounterNoData-8      	300000000	         4.88 ns/op
BenchmarkReportCounterWithData-8    	100000000	        21.6 ns/op
BenchmarkGaugeSet-8                 	100000000	        16.0 ns/op
BenchmarkReportGaugeNoData-8        	100000000	        10.4 ns/op
BenchmarkReportGaugeWithData-8      	50000000	        27.6 ns/op
BenchmarkTimerInterval-8            	50000000	        37.7 ns/op
BenchmarkTimerReport-8              	300000000	         5.69 ns/op
```

<hr>

Released under the [MIT License](LICENSE).

[doc-img]: https://godoc.org/github.com/uber-go/tally?status.svg
[doc]: https://godoc.org/github.com/uber-go/tally
[ci-img]: https://travis-ci.org/uber-go/tally.svg?branch=master
[ci]: https://travis-ci.org/uber-go/tally
[cov-img]: https://coveralls.io/repos/github/uber-go/tally/badge.svg?branch=master
[cov]: https://coveralls.io/github/uber-go/tally?branch=master
[glide.lock]: https://github.com/uber-go/tally/blob/master/glide.lock
[v1]: https://github.com/uber-go/tally/milestones
