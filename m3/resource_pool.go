// Copyright (c) 2021 Uber Technologies, Inc.
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

package m3

import (
	"sync"

	"github.com/uber-go/tally"
	customtransport "github.com/uber-go/tally/m3/customtransports"
	m3thrift "github.com/uber-go/tally/m3/thrift/v2"
	"github.com/uber-go/tally/thirdparty/github.com/apache/thrift/lib/go/thrift"
)

const (
	// Size thresholds for preventing memory bloat
	maxTagBufferSize   = 2048  // 2KB max for tag buffers
	maxKeyBufferSize   = 1024  // 1KB max for key buffers
	maxOutputBatchSize = 65536 // 64KB max for output batches
	maxMetricSliceSize = 1000  // Max metrics per slice

	// Initial capacities for efficiency
	defaultTagCapacity    = 128
	defaultKeyCapacity    = 64
	defaultBatchCapacity  = 32768 // ~32KB
	defaultMetricCapacity = 100
)

// Specialized resource pools for different buffer types
type resourcePool struct {
	// Tag building buffers (map[string]string serialization)
	tagBuffers sync.Pool

	// Metric key building buffers
	keyBuffers sync.Pool

	// Output batch buffers for thrift serialization
	outputBatches sync.Pool

	// Metric slice pooling for batch building
	metricSlices sync.Pool

	// Tag slice pooling with proper sizing
	metricTagSlices sync.Pool

	// Protocol pool (keep this for thrift protocols)
	protoFactory thrift.TProtocolFactory
}

func newResourcePool(protoFac thrift.TProtocolFactory) *resourcePool {
	return &resourcePool{
		tagBuffers: sync.Pool{
			New: func() interface{} {
				// Pre-allocate with typical capacity
				buf := make([]byte, 0, defaultTagCapacity)
				return &buf
			},
		},
		keyBuffers: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 0, defaultKeyCapacity)
				return &buf
			},
		},
		outputBatches: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 0, defaultBatchCapacity)
				return &buf
			},
		},
		metricSlices: sync.Pool{
			New: func() interface{} {
				slice := make([]m3thrift.Metric, 0, defaultMetricCapacity)
				return &slice
			},
		},
		metricTagSlices: sync.Pool{
			New: func() interface{} {
				slice := make([]m3thrift.MetricTag, 0, 8) // Most metrics have few tags
				return &slice
			},
		},
		protoFactory: protoFac,
	}
}

// Tag buffer management with size control
func (r *resourcePool) getTagBuffer() *[]byte {
	return r.tagBuffers.Get().(*[]byte)
}

func (r *resourcePool) releaseTagBuffer(buf *[]byte) {
	// Reset length but keep capacity if reasonable
	*buf = (*buf)[:0]

	// Don't pool oversized buffers to prevent memory bloat
	if cap(*buf) <= maxTagBufferSize {
		r.tagBuffers.Put(buf)
	}
	// Let oversized buffers get GC'd naturally
}

// Key buffer management
func (r *resourcePool) getKeyBuffer() *[]byte {
	return r.keyBuffers.Get().(*[]byte)
}

func (r *resourcePool) releaseKeyBuffer(buf *[]byte) {
	*buf = (*buf)[:0]
	if cap(*buf) <= maxKeyBufferSize {
		r.keyBuffers.Put(buf)
	}
}

// Output batch buffer management
func (r *resourcePool) getOutputBatch() *[]byte {
	return r.outputBatches.Get().(*[]byte)
}

func (r *resourcePool) releaseOutputBatch(buf *[]byte) {
	*buf = (*buf)[:0]
	if cap(*buf) <= maxOutputBatchSize {
		r.outputBatches.Put(buf)
	}
}

// Metric slice management (existing functionality with improvements)
func (r *resourcePool) getMetricSlice() []m3thrift.Metric {
	slice := r.metricSlices.Get().(*[]m3thrift.Metric)
	return *slice
}

func (r *resourcePool) releaseMetricSlice(metrics []m3thrift.Metric) {
	// Clear references to prevent memory leaks
	for i := 0; i < len(metrics); i++ {
		metrics[i].Tags = nil
		metrics[i].Value = m3thrift.MetricValue{} // Zero value instead of nil
	}

	// Reset slice
	metrics = metrics[:0]

	// Only pool reasonably-sized slices
	if cap(metrics) <= maxMetricSliceSize {
		r.metricSlices.Put(&metrics)
	}
}

// Tag slice management with better sizing
func (r *resourcePool) getMetricTagSlice() []m3thrift.MetricTag {
	slice := r.metricTagSlices.Get().(*[]m3thrift.MetricTag)
	return *slice
}

func (r *resourcePool) releaseMetricTagSlice(tags []m3thrift.MetricTag) {
	// Clear tag content
	for i := range tags {
		tags[i].Name = ""
		tags[i].Value = ""
	}

	tags = tags[:0]

	// Pool small tag slices (most metrics have few tags)
	if cap(tags) <= 32 { // Reasonable upper bound
		r.metricTagSlices.Put(&tags)
	}
}

// Protocol creation (no pooling needed - factory is lightweight)
func (r *resourcePool) getProto(transport thrift.TTransport) thrift.TProtocol {
	return r.protoFactory.GetProtocol(transport)
}
