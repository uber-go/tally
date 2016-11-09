package tally

import "testing"

func BenchmarkNameGeneration(b *testing.B) {
	scope := NewRootScope("funkytown", nil, NullStatsReporter, 0).(*scope)
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
	scope := NewRootScope("funkytown", tags, NullStatsReporter, 0).(*scope)
	for n := 0; n < b.N; n++ {
		scope.fullyQualifiedName("take.me.to")
	}
}

func BenchmarkNameGenerationNoPrefix(b *testing.B) {
	scope := NewRootScope("", nil, NullStatsReporter, 0).(*scope)
	for n := 0; n < b.N; n++ {
		scope.fullyQualifiedName("im.all.alone")
	}
}
