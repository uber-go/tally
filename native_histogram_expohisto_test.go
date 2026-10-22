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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

type decodedBuckets struct {
	offset int32
	counts []uint64
}

type decodedExpoHistogram struct {
	scale     int32
	count     uint64
	zeroCount uint64
	sum       float64
	min       float64
	max       float64
	positive  decodedBuckets
	negative  decodedBuckets
}

// decodeExpoHistogram decodes an ExponentialHistogram payload independently of
// the encoder, so the tests check the wire format and not only a round trip.
func decodeExpoHistogram(t *testing.T, b []byte) decodedExpoHistogram {
	t.Helper()

	var h decodedExpoHistogram
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		require.GreaterOrEqual(t, n, 0, "invalid tag")
		b = b[n:]

		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			require.GreaterOrEqual(t, n, 0, "invalid varint")
			b = b[n:]

			switch num {
			case expoFieldScale:
				h.scale = int32(v)
			case expoFieldCount:
				h.count = v
			case expoFieldZeroCount:
				h.zeroCount = v
			default:
				t.Fatalf("unexpected varint field %d", num)
			}
		case protowire.Fixed64Type:
			v, n := protowire.ConsumeFixed64(b)
			require.GreaterOrEqual(t, n, 0, "invalid fixed64")
			b = b[n:]

			switch num {
			case expoFieldSum:
				h.sum = math.Float64frombits(v)
			case expoFieldMin:
				h.min = math.Float64frombits(v)
			case expoFieldMax:
				h.max = math.Float64frombits(v)
			default:
				t.Fatalf("unexpected fixed64 field %d", num)
			}
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			require.GreaterOrEqual(t, n, 0, "invalid bytes")
			b = b[n:]

			switch num {
			case expoFieldPositive:
				h.positive = decodeBuckets(t, v)
			case expoFieldNegative:
				h.negative = decodeBuckets(t, v)
			default:
				t.Fatalf("unexpected bytes field %d", num)
			}
		default:
			t.Fatalf("unexpected wire type %d for field %d", typ, num)
		}
	}

	return h
}

func decodeBuckets(t *testing.T, b []byte) decodedBuckets {
	t.Helper()

	var d decodedBuckets
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		require.GreaterOrEqual(t, n, 0, "invalid tag")
		b = b[n:]

		switch {
		case num == bucketsFieldOffset && typ == protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			require.GreaterOrEqual(t, n, 0, "invalid varint")
			b = b[n:]
			d.offset = int32(protowire.DecodeZigZag(v))
		case num == bucketsFieldCounts && typ == protowire.BytesType:
			packed, n := protowire.ConsumeBytes(b)
			require.GreaterOrEqual(t, n, 0, "invalid bytes")
			b = b[n:]
			for len(packed) > 0 {
				v, n := protowire.ConsumeVarint(packed)
				require.GreaterOrEqual(t, n, 0, "invalid packed varint")
				packed = packed[n:]
				d.counts = append(d.counts, v)
			}
		default:
			t.Fatalf("unexpected field %d with wire type %d", num, typ)
		}
	}

	return d
}

func sumCounts(counts []uint64) uint64 {
	var total uint64
	for _, c := range counts {
		total += c
	}
	return total
}

// m3collector rejects a payload whose count does not equal the zero count plus
// the bucket counts.
func assertCountInvariant(t *testing.T, h decodedExpoHistogram) {
	t.Helper()
	assert.Equal(
		t,
		h.count,
		h.zeroCount+sumCounts(h.positive.counts)+sumCounts(h.negative.counts),
		"count must equal zero count plus bucket counts",
	)
}

func marshalExpo(t *testing.T, maxBuckets int, values ...float64) []byte {
	t.Helper()

	d := newExpoHistogramData(maxBuckets)
	for _, v := range values {
		d.Update(v)
	}
	payload, err := d.MarshalBinary()
	require.NoError(t, err)
	return payload
}

func TestExpoHistogramDataGoldenBytes(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		want   []byte
	}{
		{
			// Scale() is 0 when every sample is zero, and proto3 leaves out
			// the zero sum, min and max.
			name:   "zeros only",
			values: []float64{0, 0, 0},
			want: []byte{
				0x10, 0x03, // count = 3
				0x18, 0x03, // zero_count = 3
			},
		},
		{
			// One sample keeps the histogram at its maximum scale, 20. 1.0
			// is an exact power of two, so it lands in bucket -1.
			name:   "single positive",
			values: []float64{1},
			want: []byte{
				0x08, 0x14, // scale = 20
				0x10, 0x01, // count = 1
				0x21, 0, 0, 0, 0, 0, 0, 0xf0, 0x3f, // sum = 1.0
				0x29, 0, 0, 0, 0, 0, 0, 0xf0, 0x3f, // min = 1.0
				0x31, 0, 0, 0, 0, 0, 0, 0xf0, 0x3f, // max = 1.0
				0x3a, 0x05, // positive, 5 bytes
				0x08, 0x01, // offset = zigzag(-1)
				0x12, 0x01, 0x01, // bucket_counts = [1]
			},
		},
		{
			name:   "single negative",
			values: []float64{-1},
			want: []byte{
				0x08, 0x14, // scale = 20
				0x10, 0x01, // count = 1
				0x21, 0, 0, 0, 0, 0, 0, 0xf0, 0xbf, // sum = -1.0
				0x29, 0, 0, 0, 0, 0, 0, 0xf0, 0xbf, // min = -1.0
				0x31, 0, 0, 0, 0, 0, 0, 0xf0, 0xbf, // max = -1.0
				0x42, 0x05, // negative, 5 bytes
				0x08, 0x01, // offset = zigzag(-1)
				0x12, 0x01, 0x01, // bucket_counts = [1]
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := marshalExpo(t, DefaultNativeHistogramMaxBuckets, tt.values...)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The input matches TestSketchPayloadRoundTrip in m3collector.
func TestExpoHistogramDataMixedValues(t *testing.T) {
	payload := marshalExpo(
		t,
		DefaultNativeHistogramMaxBuckets,
		0.5, 1, 1.5, 2, 8, 64, 1024, 0, -3,
	)
	h := decodeExpoHistogram(t, payload)

	// The positive values span 11 octaves. Scale 3 uses 8 buckets per octave
	// and is the finest scale that fits in 160 buckets.
	assert.Equal(t, int32(3), h.scale)
	assert.Equal(t, uint64(9), h.count)
	assert.Equal(t, uint64(1), h.zeroCount)
	assert.Equal(t, 1098.0, h.sum)
	assert.Equal(t, -3.0, h.min)
	assert.Equal(t, 1024.0, h.max)

	// At scale 3, 0.5 maps to bucket -9 and 1024 maps to bucket 79.
	assert.Equal(t, int32(-9), h.positive.offset)
	assert.Len(t, h.positive.counts, 89)
	assert.Equal(t, uint64(7), sumCounts(h.positive.counts))

	// 3 maps to bucket ceil(8*log2(3)) - 1 = 12.
	assert.Equal(t, int32(12), h.negative.offset)
	assert.Equal(t, []uint64{1}, h.negative.counts)

	assertCountInvariant(t, h)
}

func TestExpoHistogramDataDropsNaNAndInf(t *testing.T) {
	d := newExpoHistogramData(DefaultNativeHistogramMaxBuckets)

	d.Update(math.NaN())
	d.Update(math.Inf(1))
	d.Update(math.Inf(-1))
	assert.Equal(t, uint64(0), d.Count())

	d.Update(2)
	assert.Equal(t, uint64(1), d.Count())

	payload, err := d.MarshalBinary()
	require.NoError(t, err)

	h := decodeExpoHistogram(t, payload)
	assert.Equal(t, 2.0, h.sum)
	assertCountInvariant(t, h)
}

func TestExpoHistogramDataClampsMaxBuckets(t *testing.T) {
	// The values span 200 octaves. Without the clamp, a budget of 1000 keeps
	// scale 2 and uses 800 buckets.
	values := make([]float64, 0, 2001)
	for i := 0; i <= 2000; i++ {
		values = append(values, math.Pow(2, float64(i)/10))
	}

	h := decodeExpoHistogram(t, marshalExpo(t, 1000, values...))

	assert.LessOrEqual(t, len(h.positive.counts), maxNativeHistogramBuckets)
	assert.Equal(t, uint64(len(values)), h.count)
	assertCountInvariant(t, h)
}

func TestExpoHistogramDataRejectsScaleBelowMinimum(t *testing.T) {
	// Two buckets cannot cover 1e-300 to 1e300 above scale -4.
	d := newExpoHistogramData(2)
	d.Update(1e-300)
	d.Update(1e300)

	payload, err := d.MarshalBinary()
	assert.Nil(t, payload)
	assert.ErrorIs(t, err, errNativeHistogramScaleTooLow)
}

func TestExpoHistogramDataClear(t *testing.T) {
	d := newExpoHistogramData(DefaultNativeHistogramMaxBuckets)
	d.Update(1)
	d.Update(-1)
	d.Clear()
	assert.Equal(t, uint64(0), d.Count())

	d.Update(0)
	payload, err := d.MarshalBinary()
	require.NoError(t, err)
	assert.Equal(t, []byte{0x10, 0x01, 0x18, 0x01}, payload,
		"nothing from before Clear is encoded")
}
