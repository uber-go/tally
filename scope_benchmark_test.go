package tally

import "testing"

func BenchmarkNameGeneration(b *testing.B) {
	root, _ := NewRootScope("funkytown", nil, NullStatsReporter, 0)
	s := root.(*scope)
	for n := 0; n < b.N; n++ {
		s.fullyQualifiedName("take.me.to")
	}
}

func BenchmarkNameGenerationTagged(b *testing.B) {
	tags := map[string]string{
		"style":     "funky",
		"hair":      "wavy",
		"jefferson": "starship",
	}
	root, _ := NewRootScope("funkytown", tags, NullStatsReporter, 0)
	s := root.(*scope)
	for n := 0; n < b.N; n++ {
		s.fullyQualifiedName("take.me.to")
	}
}

func BenchmarkNameGenerationNoPrefix(b *testing.B) {
	root, _ := NewRootScope("", nil, NullStatsReporter, 0)
	s := root.(*scope)
	for n := 0; n < b.N; n++ {
		s.fullyQualifiedName("im.all.alone")
	}
}
