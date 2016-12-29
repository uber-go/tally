package prometheus

import (
	"fmt"
	"testing"
	"time"
)

func TestCounter(t *testing.T) {
	r := NewReporter(nil)
	name := "test_counter"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	tags2 := map[string]string{
		"foo":  "baz",
		"test": "*",
	}
	r.ReportCounter(name, tags, 1)
	r.ReportCounter(name, tags, 1)
	r.ReportCounter(name, tags, 1)
	for i := 0; i < 5; i++ {
		fmt.Printf("%d adding 1\n", i)
		r.ReportCounter(name, tags, 1)
	}
	r.ReportCounter(name, tags2, 1)
	r.ReportCounter(name, tags2, 1)
}

func TestGauge(t *testing.T) {
	r := NewReporter(nil)
	name := "test_gauge"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	r.ReportGauge(name, tags, 15)
	r.ReportGauge(name, tags, 30)
}

func TestTimer(t *testing.T) {
	r := NewReporter(nil)
	name := "test_timer"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	tags2 := map[string]string{
		"foo":  "baz",
		"test": "*",
	}
	r.ReportTimer(name, tags, 10*time.Second)
	r.ReportTimer(name, tags, 320*time.Millisecond)
	r.ReportTimer(name, tags2, 223*time.Millisecond)
	r.ReportTimer(name, tags, 4*time.Second)
}
