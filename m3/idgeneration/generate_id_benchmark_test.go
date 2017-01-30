package idgeneration

import (
	"strconv"
	"testing"
)

func BenchmarkGenerateID0(b *testing.B) {
	benchmarkGenerateID(b, 0)
}

func BenchmarkGenerateID2(b *testing.B) {
	benchmarkGenerateID(b, 2)
}

func BenchmarkGenerateID8(b *testing.B) {
	benchmarkGenerateID(b, 8)
}

func benchmarkGenerateID(b *testing.B, numTags int) {
	tags := make(map[string]string, numTags)
	for i := numTags; i > 0; i-- {
		tags["tagName"+strconv.Itoa(i)] = "value" + strconv.Itoa(i)
	}

	for n := 0; n < b.N; n++ {
		Get("random.name.for.testing.purposes", tags)
	}
}
