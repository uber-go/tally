// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package http

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/uber-go/tally"
	"github.com/uber-go/tally/m3"
)

const (
	counterType   = "counter"
	gaugeType     = "gauge"
	timerType     = "timer"
	histogramType = "histogram"

	maxUpperBoundStr = "+Inf"
)

var (
	encPool = newEncoderPool(64, 32, 32, 32)
)

type encoderPool struct {
	stringsPool *tally.ObjectPool
	floatsPool  *tally.ObjectPool
	intsPool    *tally.ObjectPool
}

// Encoder encodes a tally Snapshot and writes it to an underlying io.Writer.
type Encoder interface {
	Encode(s tally.Snapshot) error
}

type encoder struct {
	w    io.Writer
	opts Options
}

// Options is a set of options for HTTP encoding.
type Options struct {
	TagsFormat TagsFormat
}

// TagsFormat describes the formatting of tags for different
// metric types when presenting the metrics.
type TagsFormat uint

const (
	// PrometheusTagsFormat is the format Prometheus
	// uses, using the "le" tag for histogram buckets, etc.
	PrometheusTagsFormat TagsFormat = iota
	// M3TagsFormat is the format M3 uses, using the
	// "bucketid" and "bucket" tags for histogram buckets, etc.
	M3TagsFormat

	unknownTagsFormat
)

// DefaultTagsFormat is the default tags format using
// Prometheus tags formatting.
const DefaultTagsFormat = PrometheusTagsFormat

// ValidTagsFormats returns the current valid tag formats
// as a new array. This avoids consumers mutating a single
// instance of a valid tags array.
func ValidTagsFormats() []TagsFormat {
	return []TagsFormat{
		PrometheusTagsFormat,
		M3TagsFormat,
	}
}

func (f TagsFormat) String() string {
	switch f {
	case PrometheusTagsFormat:
		return "prometheus"
	case M3TagsFormat:
		return "m3"
	}
	return "unknown"
}

// internal known non-mutated valid tags
var validTagsFormats = ValidTagsFormats()

// ParseTagsFormat parses a string representation of the
// tags format and returns the concrete enum value.
func ParseTagsFormat(str string) (TagsFormat, error) {
	for _, f := range validTagsFormats {
		if str == f.String() {
			return f, nil
		}
	}
	valids := make([]string, len(validTagsFormats))
	for i, f := range validTagsFormats {
		valids[i] = f.String()
	}
	return unknownTagsFormat, fmt.Errorf(
		"unknown tags format %s, valid formats: %v", str, valids)
}

// NewEncoder returns a new Encoder which encodes a metrics snapshot
// to an io.Writer in a text format similiar to the one used by Prometheus.
func NewEncoder(w io.Writer, opts Options) Encoder {
	return &encoder{w: w, opts: opts}
}

func (e *encoder) Encode(s tally.Snapshot) error {
	_, err := e.encode(s)
	return err
}

func (e *encoder) encode(s tally.Snapshot) (int, error) {
	var written int

	// We sort the id's so we can ensure consistent order across calls.
	keys := encPool.stringsPool.Get().([]string)
	defer encPool.releaseStrings(keys)

	counters := s.Counters()
	for k := range counters {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		counter := counters[key]
		n, err := e.encodeCounter(counter)
		written += n
		if err != nil {
			return written, err
		}
	}

	keys = keys[:0]
	gauges := s.Gauges()
	for k := range gauges {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		gauge := gauges[key]
		n, err := e.encodeGauge(gauge)
		written += n
		if err != nil {
			return written, err
		}
	}

	keys = keys[:0]
	timers := s.Timers()
	for k := range timers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		timer := timers[key]
		n, err := e.encodeTimer(timer)
		written += n
		if err != nil {
			return written, err
		}
	}

	keys = keys[:0]
	histograms := s.Histograms()
	for k := range histograms {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		histogram := histograms[key]
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

func (e *encoder) encodeNameAndTags(
	meta tally.Metadata,
	secondLastTagName, secondLastTagValue string,
	lastTagName, lastTagValue string,
) (int, error) {
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

	if secondLastTagName != "" {
		n, err := fmt.Fprintf(e.w, tagFormat, separator, secondLastTagName, escapeString(secondLastTagValue))
		written += n
		if err != nil {
			return written, err
		}
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

	n, err = e.encodeNameAndTags(counter, "", "", "", "")
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

	n, err = e.encodeNameAndTags(gauge, "", "", "", "")
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

	n, err = e.encodeNameAndTags(timer, "", "", "", "")
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

		switch e.opts.TagsFormat {
		case PrometheusTagsFormat:
			var count int64
			for _, upperBound := range valueUpperBounds {
				var upperBoundStr string
				if upperBound == math.MaxFloat64 {
					upperBoundStr = maxUpperBoundStr
				} else {
					upperBoundStr = fmt.Sprint(upperBound)
				}

				n, err = e.encodeNameAndTags(histogram, "le", upperBoundStr, "", "")
				written += n
				if err != nil {
					return written, err
				}

				count += values[upperBound]

				n, err = fmt.Fprintf(e.w, " %v\n", count)
				written += n
				if err != nil {
					return written, err
				}
			}
		case M3TagsFormat:
			bucketIDFmt := m3.HistogramBucketIDFmt(len(valueUpperBounds) + 2)
			bucketValFmt := m3.DefaultHistogramValueBucketFmt
			bucketName := m3.HistogramValueBucketString(
				-math.MaxFloat64, valueUpperBounds[0], bucketValFmt)
			n, err := e.encodeNameAndTags(histogram,
				m3.DefaultHistogramBucketIDName, fmt.Sprintf(bucketIDFmt, 0),
				m3.DefaultHistogramBucketName, bucketName)
			written += n
			if err != nil {
				return written, err
			}

			n, err = fmt.Fprintf(e.w, " %v\n", valueUpperBounds[0])
			written += n
			if err != nil {
				return written, err
			}

			prevValue := valueUpperBounds[0]
			for i := 1; i < len(valueUpperBounds); i++ {
				bucketName = m3.HistogramValueBucketString(
					prevValue, valueUpperBounds[i], bucketValFmt)
				n, err = e.encodeNameAndTags(histogram,
					m3.DefaultHistogramBucketIDName, fmt.Sprintf(bucketIDFmt, i),
					m3.DefaultHistogramBucketName, bucketName)
				written += n
				if err != nil {
					return written, err
				}

				n, err = fmt.Fprintf(e.w, " %v\n", values[valueUpperBounds[i]])
				written += n
				if err != nil {
					return written, err
				}

				prevValue = valueUpperBounds[i]
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

		switch e.opts.TagsFormat {
		case PrometheusTagsFormat:
			var count int64
			for _, upperBound := range durationUpperBounds {
				var upperBoundStr string
				if upperBound == math.MaxInt64 {
					upperBoundStr = maxUpperBoundStr
				} else {
					upperBoundStr = durationString(time.Duration(upperBound))
				}

				n, err = e.encodeNameAndTags(histogram, "le", upperBoundStr, "", "")
				written += n
				if err != nil {
					return written, err
				}

				count += durations[time.Duration(upperBound)]

				n, err = fmt.Fprintf(e.w, " %v\n", count)
				written += n
				if err != nil {
					return written, err
				}
			}
		case M3TagsFormat:
			bucketIDFmt := m3.HistogramBucketIDFmt(len(durationUpperBounds) + 2)
			bucketName := m3.HistogramDurationBucketString(
				time.Duration(math.MinInt64), time.Duration(durationUpperBounds[0]))
			n, err := e.encodeNameAndTags(histogram,
				m3.DefaultHistogramBucketIDName, fmt.Sprintf(bucketIDFmt, 0),
				m3.DefaultHistogramBucketName, bucketName)
			written += n
			if err != nil {
				return written, err
			}

			count := durations[time.Duration(durationUpperBounds[0])]

			n, err = fmt.Fprintf(e.w, " %v\n", count)
			written += n
			if err != nil {
				return written, err
			}

			prevValue := durationUpperBounds[0]
			for i := 1; i < len(durationUpperBounds); i++ {
				bucketName = m3.HistogramDurationBucketString(
					time.Duration(prevValue), time.Duration(durationUpperBounds[i]))
				n, err = e.encodeNameAndTags(histogram,
					m3.DefaultHistogramBucketIDName, fmt.Sprintf(bucketIDFmt, i),
					m3.DefaultHistogramBucketName, bucketName)
				written += n
				if err != nil {
					return written, err
				}

				count := durations[time.Duration(durationUpperBounds[i])]

				n, err = fmt.Fprintf(e.w, " %v\n", count)
				written += n
				if err != nil {
					return written, err
				}

				prevValue = durationUpperBounds[i]
			}
		}

	}

	return written, nil
}

var (
	escaper = strings.NewReplacer("\\", `\\`, "\n", `\n`, "\"", `\"`)
)

// durationString returns the string representation of a time.Duration. We need a special function
// here because in Go 1.7 the representation of 0 was changed from "0" to "0s". Consequently,
// to ensure consistent output on versions of Go older than 1.7 we need to return "0s" explicitly.
func durationString(d time.Duration) string {
	if d == 0 {
		return "0s"
	}

	return fmt.Sprint(d)
}

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
	p.floatsPool.Put(floats[:0])
}

func (p *encoderPool) releaseInts(ints []int) {
	p.intsPool.Put(ints[:0])
}
