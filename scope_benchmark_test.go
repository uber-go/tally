package tally

import "testing"

func BenchmarkNameGeneration(b *testing.B) {
	scope := NewScope("funkytown", nil, NullStatsReporter)
	for n := 0; n < b.N; n++ {
		scope.scopedName("take.me.to")
	}
}

func BenchmarkNameGenerationTagged(b *testing.B) {
	tags := map[string]string{
		"style":     "funky",
		"hair":      "wavy",
		"jefferson": "starship",
	}
	scope := NewScope("funkytown", tags, NullStatsReporter)
	for n := 0; n < b.N; n++ {
		scope.scopedName("take.me.to")
	}
}

func BenchmarkNameGenerationNoPrefix(b *testing.B) {
	scope := NewScope("", nil, NullStatsReporter)
	for n := 0; n < b.N; n++ {
		scope.scopedName("im.all.alone")
	}
}
