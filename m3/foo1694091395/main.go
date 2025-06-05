
package main

import (
	"time"

	tally "github.com/uber-go/tally/v6"
	"github.com/uber-go/tally/v6/m3"
)

func main() {
	r, err := m3.NewReporter(m3.Options{
		HostPorts: []string{"127.0.0.1:64424"},
		Service:   "test-service",
		Env:       "test",
	})
	if err != nil {
		panic(err)
	}

	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		CachedReporter: r,
		OmitCardinalityMetrics: true,
	}, 5 * time.Second)
	defer closer.Close()

	scope.Counter("my-counter").Inc(42)
	scope.Gauge("my-gauge").Update(123)
	scope.Timer("my-timer").Record(456 * time.Millisecond)
}
	