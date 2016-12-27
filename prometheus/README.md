# Example Usage

`main.go`:

```
package main

import (
  "fmt"
  "math/rand"
  "net/http"
  "time"

  "github.com/uber-go/tally"
  "github.com/uber-go/tally/prometheus"
)

func main() {
  r := prometheus.NewReporter(nil)
  // note the "_" separator. Prometheus doesnt like metrics with "." in them.
  scope, finisher := tally.NewRootScope("prefix", map[string]string{}, r, 1*time.Second, "_")
  defer finisher.Close()

  counter := scope.Counter("test_counter")
  gauge := scope.Gauge("test_gauge")
  histogram := scope.Timer("test_histogram")

  go func() {
    for {
      counter.Inc(1)
      time.Sleep(1000000)
    }
  }()

  go func() {
    for {
      gauge.Update(rand.Float64() * 1000)
      time.Sleep(1000000)
    }
  }()

  go func() {
    for {
      sw := histogram.Start()
      time.Sleep(time.Duration(rand.Float64() * 1000 * 1000))
      sw.Stop()
      time.Sleep(1000000)
    }
  }()

  http.Handle("/metrics", r.HTTPHandler())
  fmt.Printf("Serving :8080/metrics\n")
  fmt.Printf("%v\n", http.ListenAndServe(":8080", nil))
  select {}

}
```
