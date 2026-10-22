// Copyright (c) 2026 Uber Technologies, Inc.
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

package tally

import (
	"errors"
	"math"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/expohisto/structure"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	// maxNativeHistogramBuckets caps the bucket budget of the default
	// accumulator. m3collector rejects a payload with more buckets than this
	// on either side.
	maxNativeHistogramBuckets = 160

	// minNativeHistogramScale is the coarsest scale m3collector accepts. The
	// accumulator only goes below it when its bucket budget is very small.
	minNativeHistogramScale = -4
)

var errNativeHistogramScaleTooLow = errors.New(
	"native histogram scale is below the minimum the encoding allows",
)

// Field numbers of the ExponentialHistogram proto3 message that the default
// accumulator marshals to:
//
//	message ExponentialHistogram {
//	  int32  scale      = 1;
//	  uint64 count      = 2;
//	  uint64 zero_count = 3;
//	  double sum        = 4;
//	  double min        = 5;
//	  double max        = 6;
//	  ExponentialBuckets positive = 7;
//	  ExponentialBuckets negative = 8;
//	}
//
//	message ExponentialBuckets {
//	  sint32          offset        = 1;
//	  repeated uint64 bucket_counts = 2;
//	}
const (
	expoFieldScale     protowire.Number = 1
	expoFieldCount     protowire.Number = 2
	expoFieldZeroCount protowire.Number = 3
	expoFieldSum       protowire.Number = 4
	expoFieldMin       protowire.Number = 5
	expoFieldMax       protowire.Number = 6
	expoFieldPositive  protowire.Number = 7
	expoFieldNegative  protowire.Number = 8

	bucketsFieldOffset protowire.Number = 1
	bucketsFieldCounts protowire.Number = 2
)

// expoHistogramData is the default NativeHistogramData. It accumulates into an
// OpenTelemetry base-2 exponential histogram and marshals it as the proto3
// ExponentialHistogram message above, which is the payload m3collector
// decodes.
type expoHistogramData struct {
	hist structure.Float64
}

func newExpoHistogramData(maxBuckets int) NativeHistogramData {
	// Init raises a budget below structure.MinSize on its own, so only the
	// upper bound needs a clamp here.
	maxBuckets = min(maxBuckets, maxNativeHistogramBuckets)

	d := &expoHistogramData{}
	d.hist.Init(structure.NewConfig(structure.WithMaxSize(int32(maxBuckets))))
	return d
}

// Update drops NaN and infinities. The histogram has no bucket for them, and
// they would make the sum and the bucket indexes invalid, so the receiver
// would reject the whole payload.
func (d *expoHistogramData) Update(value float64) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	d.hist.Update(value)
}

func (d *expoHistogramData) Count() uint64 {
	return d.hist.Count()
}

func (d *expoHistogramData) Clear() {
	d.hist.Clear()
}

// MarshalBinary encodes the histogram as proto3, so fields with a zero value
// are left out.
func (d *expoHistogramData) MarshalBinary() ([]byte, error) {
	h := &d.hist

	scale := h.Scale()
	if scale < minNativeHistogramScale {
		return nil, errNativeHistogramScaleTooLow
	}

	positive, negative := h.Positive(), h.Negative()

	// Each count is a varint of at most 10 bytes. The fixed fields need at
	// most 11 bytes each.
	b := make([]byte, 0, 8*11+10*int(positive.Len()+negative.Len()))

	if scale != 0 {
		// An int32 is sign-extended to 64 bits on the wire.
		b = appendVarintField(b, expoFieldScale, uint64(int64(scale)))
	}
	b = appendVarintField(b, expoFieldCount, h.Count())
	b = appendVarintField(b, expoFieldZeroCount, h.ZeroCount())
	b = appendDoubleField(b, expoFieldSum, h.Sum())
	b = appendDoubleField(b, expoFieldMin, h.Min())
	b = appendDoubleField(b, expoFieldMax, h.Max())
	b = appendBucketsField(b, expoFieldPositive, positive)
	b = appendBucketsField(b, expoFieldNegative, negative)

	return b, nil
}

func appendVarintField(b []byte, num protowire.Number, v uint64) []byte {
	if v == 0 {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func appendDoubleField(b []byte, num protowire.Number, v float64) []byte {
	bits := math.Float64bits(v)
	if bits == 0 {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.Fixed64Type)
	return protowire.AppendFixed64(b, bits)
}

// appendBucketsField appends an ExponentialBuckets message. It sizes the
// message first, so it can write the length prefix without a second buffer.
func appendBucketsField(
	b []byte,
	num protowire.Number,
	buckets *structure.Buckets,
) []byte {
	n := buckets.Len()
	if n == 0 {
		return b
	}

	countsLen := 0
	for i := uint32(0); i < n; i++ {
		countsLen += protowire.SizeVarint(buckets.At(i))
	}

	offset := protowire.EncodeZigZag(int64(buckets.Offset()))

	msgLen := protowire.SizeTag(bucketsFieldCounts) + protowire.SizeBytes(countsLen)
	if offset != 0 {
		msgLen += protowire.SizeTag(bucketsFieldOffset) + protowire.SizeVarint(offset)
	}

	b = protowire.AppendTag(b, num, protowire.BytesType)
	b = protowire.AppendVarint(b, uint64(msgLen))

	b = appendVarintField(b, bucketsFieldOffset, offset)

	b = protowire.AppendTag(b, bucketsFieldCounts, protowire.BytesType)
	b = protowire.AppendVarint(b, uint64(countsLen))
	for i := uint32(0); i < n; i++ {
		b = protowire.AppendVarint(b, buckets.At(i))
	}

	return b
}
