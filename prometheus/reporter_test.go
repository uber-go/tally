package prometheus

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCounter(t *testing.T) {
	r := NewReporter()
	name := "test_counter"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	tags2 := map[string]string{
		"foo":  "baz",
		"test": "*",
	}
	m, err := r.RegisterCounter(name, tags, "help string")
	assert.Nil(t, err)
	r.ReportCounter(name, tags, 1)
	r.ReportCounter(name, tags, 1)
	r.ReportCounter(name, tags, 1)
	for i := 0; i < 5; i++ {
		fmt.Printf("%d adding 1\n", i)
		r.ReportCounter(name, tags, 1)
	}
	r.ReportCounter(name, tags2, 1)
	r.ReportCounter(name, tags2, 1)
	_, err = m.GetMetricWith(tags)
	assert.Nil(t, err)
}

func TestGauge(t *testing.T) {
	r := NewReporter()
	name := "test_gauge"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	m, err := r.RegisterGauge(name, tags, "help string")
	assert.Nil(t, err)
	r.ReportGauge(name, tags, 15)
	r.ReportGauge(name, tags, 30)
	_, err = m.GetMetricWith(tags)
	assert.Nil(t, err)
}

func TestTimer(t *testing.T) {
	r := NewReporter()
	name := "test_timer"
	tags := map[string]string{
		"foo":  "bar",
		"test": "everything",
	}
	tags2 := map[string]string{
		"foo":  "baz",
		"test": "*",
	}
	m, err := r.RegisterTimer(name, tags, "help string", nil)
	assert.Nil(t, err)
	r.ReportTimer(name, tags, 10*time.Second)
	r.ReportTimer(name, tags, 320*time.Millisecond)
	r.ReportTimer(name, tags2, 223*time.Millisecond)
	r.ReportTimer(name, tags, 4*time.Second)
	_, err = m.GetMetricWith(tags)
	assert.Nil(t, err)
}
