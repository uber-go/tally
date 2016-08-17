# :heavy_check_mark: tally [![GoDoc][doc-img]][doc] [![Build Status][ci-img]][ci] [![Coverage Status][cov-img]][cov]

Fast, buffered, heirarchical stats collection in Go.

## Installation
`go get -u github.com/uber-go/tally`

## Abstract

Tally provides a common interface for tracking metrics, while letting
you not worry about the velocity of logging.

## Structure

- Scope Keeps track of metrics, and their common metadata.
- Metrics: Counters, Gauges, Timers
- Reporter: Implemented by you. Accepts aggregated values from the scope. Forwards the aggregated values on the your analytics DB.

### Acquire a Scope ###
```golang
reporter = MyStatsReporter()  // Implement as you will
tags := map[string]string{
	"dc": "east-1",
	"type": "master",
}
scope := tally.NewScope("coolserver", tags, reporter)
```

### Get/Create a metric, use it ###
```golang
// Get a counter, increment a counter
reqCounter := scope.Counter('requests')  // cache me
reqCounter.Inc(1)

memGauge := scope.Gauge('mem_usage')  // cache me
memGauge.Update(42)
```

### Report your metrics ###
``` golang
func (r *myStatsReporter) start(scope) {
	ticker := time.NewTicker(r.interval)
	for {
		select {
		case <-ticker.C:
			scope.Report(r)
		case <-r.quit:
			return
		}
	}
}
```

## Performance

Something, something, something, dark side

## Development Status: Pre-Beta

Not quite ready yet. You probably don't want to use this just yet.

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
