package http

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/uber-go/tally"
)

const (
	counterType   = "counter"
	gaugeType     = "gauge"
	timerType     = "timer"
	histogramType = "histogram"
)

var (
	encPool = newEncoderPool(64, 32, 32, 32)
)

type encoderPool struct {
	stringsPool *tally.ObjectPool
	floatsPool  *tally.ObjectPool
	intsPool    *tally.ObjectPool
}

// Encoder encodes a tally Snapshot and writes it to an underlying io.Writer
type Encoder interface {
	Encode(s tally.Snapshot) error
}

type encoder struct {
	w io.Writer
}

// NewEncoder returns a new Encoder which encodes a metrics snapshot to an io.Writer in a text
// format similiar to the one used by Prometheus.
func NewEncoder(w io.Writer) Encoder {
	return &encoder{w: w}
}

func (e *encoder) Encode(s tally.Snapshot) error {
	_, err := e.encode(s)
	return err
}

func (e *encoder) encode(s tally.Snapshot) (int, error) {
	var written int

	for _, counter := range s.Counters() {
		n, err := e.encodeCounter(counter)
		written += n
		if err != nil {
			return written, err
		}
	}

	for _, gauge := range s.Gauges() {
		n, err := e.encodeGauge(gauge)
		written += n
		if err != nil {
			return written, err
		}
	}

	for _, timer := range s.Timers() {
		n, err := e.encodeTimer(timer)
		written += n
		if err != nil {
			return written, err
		}
	}

	for _, histogram := range s.Histograms() {
		n, err := e.encodeHistogram(histogram)
		written += n
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func (e *encoder) encodeComments(meta tally.Metadata, metricType string) (int, error) {
	var written int

	// TODO(jeromefroe): Add an optional description to metrics and encode it here when present to
	// match the Prometheus 'HELP' comments.

	n, err := fmt.Fprintf(e.w, "# TYPE %s %s\n", meta.Name(), metricType)
	written += n
	if err != nil {
		return written, err
	}

	return written, nil
}

func (e *encoder) encodeNameAndTags(meta tally.Metadata, lastTagName, lastTagValue string) (int, error) {
	var written int

	n, err := fmt.Fprint(e.w, meta.Name())
	written += n
	if err != nil {
		return written, err
	}

	tags := meta.Tags()

	keys := encPool.stringsPool.Get().([]string)
	defer encPool.releaseStrings(keys)
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	separator := '{'
	tagFormat := `%c%s="%s"`
	for _, k := range keys {
		// NB(jeromefroe): We follow the Prometheus approach here and only escape tag values as they
		// are more likely to be defined elsewhere in a program and accidentally contain control
		// characters in. In the future, if we ever perform validation on tags we can remove this.
		v := escapeString(tags[k])
		n, err := fmt.Fprintf(e.w, tagFormat, separator, k, v)
		written += n
		if err != nil {
			return written, err
		}
		separator = ','
	}

	if lastTagName != "" {
		n, err := fmt.Fprintf(e.w, tagFormat, separator, lastTagName, escapeString(lastTagValue))
		written += n
		if err != nil {
			return written, err
		}
	}

	n, err = e.w.Write([]byte{'}'})
	written += n
	if err != nil {
		return written, err
	}

	return written, nil
}

func (e *encoder) encodeCounter(counter tally.CounterSnapshot) (int, error) {
	var written int

	n, err := e.encodeComments(counter, counterType)
	written += n
	if err != nil {
		return written, err
	}

	n, err = e.encodeNameAndTags(counter, "", "")
	written += n
	if err != nil {
		return written, err
	}

	n, err = fmt.Fprintf(e.w, " %v\n", counter.Value())
	written += n
	if err != nil {
		return written, err
	}

	return written, nil
}

func (e *encoder) encodeGauge(gauge tally.GaugeSnapshot) (int, error) {
	var written int

	n, err := e.encodeComments(gauge, gaugeType)
	written += n
	if err != nil {
		return written, err
	}

	n, err = e.encodeNameAndTags(gauge, "", "")
	written += n
	if err != nil {
		return written, err
	}

	n, err = fmt.Fprintf(e.w, " %v\n", gauge.Value())
	written += n
	if err != nil {
		return written, err
	}

	return written, nil
}

func (e *encoder) encodeTimer(timer tally.TimerSnapshot) (int, error) {
	var written int

	n, err := e.encodeComments(timer, timerType)
	written += n
	if err != nil {
		return written, err
	}

	n, err = e.encodeNameAndTags(timer, "", "")
	written += n
	if err != nil {
		return written, err
	}

	n, err = fmt.Fprintf(e.w, " %v\n", timer.Values())
	written += n
	if err != nil {
		return written, err
	}

	return written, nil
}

func (e *encoder) encodeHistogram(histogram tally.HistogramSnapshot) (int, error) {
	var written int
	maxUpperBoundStr := "+Inf"

	n, err := e.encodeComments(histogram, histogramType)
	written += n
	if err != nil {
		return written, err
	}

	values := histogram.Values()

	if len(values) > 0 {
		valueUpperBounds := encPool.floatsPool.Get().([]float64)
		defer encPool.releaseFloats(valueUpperBounds)

		for ub := range values {
			valueUpperBounds = append(valueUpperBounds, ub)
		}
		sort.Float64s(valueUpperBounds)

		for _, upperBound := range valueUpperBounds {
			var upperBoundStr string
			if upperBound == math.MaxFloat64 {
				upperBoundStr = maxUpperBoundStr
			} else {
				upperBoundStr = fmt.Sprint(upperBound)
			}

			n, err = e.encodeNameAndTags(histogram, "le", upperBoundStr)
			written += n
			if err != nil {
				return written, err
			}

			count := values[upperBound]

			n, err = fmt.Fprintf(e.w, " %v\n", count)
			written += n
			if err != nil {
				return written, err
			}
		}
	}

	durations := histogram.Durations()

	if len(durations) > 0 {
		durationUpperBounds := encPool.intsPool.Get().([]int)
		defer encPool.releaseInts(durationUpperBounds)

		for ub := range durations {
			durationUpperBounds = append(durationUpperBounds, int(ub))
		}
		sort.Ints(durationUpperBounds)

		for _, upperBound := range durationUpperBounds {
			var upperBoundStr string
			if upperBound == math.MaxInt64 {
				upperBoundStr = maxUpperBoundStr
			} else {
				upperBoundStr = fmt.Sprint(time.Duration(upperBound))
			}

			n, err = e.encodeNameAndTags(histogram, "le", upperBoundStr)
			written += n
			if err != nil {
				return written, err
			}

			count := durations[time.Duration(upperBound)]

			n, err = fmt.Fprintf(e.w, " %v\n", count)
			written += n
			if err != nil {
				return written, err
			}
		}
	}

	return written, nil
}

var (
	escaper = strings.NewReplacer("\\", `\\`, "\n", `\n`, "\"", `\"`)
)

// escapeString replaces a backslash with '\\', a new line with '\n', and a double quote with '\"'.
func escapeString(s string) string {
	return escaper.Replace(s)
}

func newEncoderPool(size, slen, flen, ilen int) *encoderPool {
	s := tally.NewObjectPool(size)
	s.Init(func() interface{} {
		return make([]string, 0, slen)
	})

	f := tally.NewObjectPool(size)
	f.Init(func() interface{} {
		return make([]float64, 0, flen)
	})

	i := tally.NewObjectPool(size)
	i.Init(func() interface{} {
		return make([]int, 0, ilen)
	})

	return &encoderPool{
		stringsPool: s,
		floatsPool:  f,
		intsPool:    i,
	}
}

func (p *encoderPool) releaseStrings(strs []string) {
	for i := range strs {
		strs[i] = ""
	}
	p.stringsPool.Put(strs[:0])
}

func (p *encoderPool) releaseFloats(floats []float64) {
	for i := range floats {
		floats[i] = 0
	}
	p.floatsPool.Put(floats[:0])
}

func (p *encoderPool) releaseInts(ints []int) {
	for i := range ints {
		ints[i] = 0
	}
	p.intsPool.Put(ints[:0])
}
