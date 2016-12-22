# Example Usage

`main.go`:

```
package main

import (
        "fmt"
        "github.com/uber-go/tally/prometheus"
        "math/rand"
        "net/http"
        "time"
)

func main() {
        r := prometheus.NewReporter()

        r.RegisterCounter("test_counter", map[string]string{"labela": "foo"}, "This is a counter")
        r.RegisterGauge("test_gauge", map[string]string{"labela": "foo"}, "This is a gauge")
        r.RegisterTimer("test_histogram", map[string]string{"labela": "foo"}, "This is a histogram", nil)

        go func() {
                for {
                        r.ReportCounter("test_counter", map[string]string{"labela": "foo"}, 1)
                        time.Sleep(1000000)
                }
        }()
        go func() {
                for {
                        r.ReportGauge("test_gauge", map[string]string{"labela": "foo"}, rand.Float64()*1000)
                        time.Sleep(1000000)
                }
        }()

        go func() {
                for {
                        r.ReportTimer("test_histogram", map[string]string{"labela": "foo"}, time.Duration(rand.Float64()*1000*1000))
                        time.Sleep(1000000)
                }
        }()
        fmt.Printf("Hello, listening on :8080\n")
        http.Handle("/metrics", r.HTTPHandler())
        fmt.Printf("%v\n", http.ListenAndServe(":8080", nil))
        select {}

}
```
