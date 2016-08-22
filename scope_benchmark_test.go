package tally

import "testing"

func BenchmarkNameGeneration(b *testing.B) {
	scope := NewScope("funkytown", nil, NullStatsReporter).(*standardScope)
	for n := 0; n < b.N; n++ {
		scope.fullyQualifiedName("take.me.to")
	}
}

func BenchmarkNameGenerationTagged(b *testing.B) {
	tags := map[string]string{
		"style":     "funky",
		"hair":      "wavy",
		"jefferson": "starship",
	}
	scope := NewScope("funkytown", tags, NullStatsReporter).(*standardScope)
	for n := 0; n < b.N; n++ {
		scope.fullyQualifiedName("take.me.to")
	}
}

func BenchmarkNameGenerationNoPrefix(b *testing.B) {
	scope := NewScope("", nil, NullStatsReporter).(*standardScope)
	for n := 0; n < b.N; n++ {
		scope.fullyQualifiedName("im.all.alone")
	}
}
