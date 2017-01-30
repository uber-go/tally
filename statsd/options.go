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

package statsd

const (
	defaultSampleRate = 1.0
)

// Options represents a set of statsd stats reporter options
type Options interface {
	// SampleRate returns the sample rate
	SampleRate() float32

	// SetSampleRate sets the sample rate and returns new options with the value set
	SetSampleRate(value float32) Options
}

// NewOptions creates a new set of statsd stats reporter options
func NewOptions() Options {
	return &options{
		sampleRate: defaultSampleRate,
	}
}

type options struct {
	sampleRate float32
}

func (o *options) SampleRate() float32 {
	return o.sampleRate
}

func (o *options) SetSampleRate(value float32) Options {
	opts := *o
	opts.sampleRate = value
	return &opts
}
