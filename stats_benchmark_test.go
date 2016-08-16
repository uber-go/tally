package tally

import "testing"

func BenchmarkCounterInc(b *testing.B) {
	c := &counter{}
	for n := 0; n < b.N; n++ {
		c.Inc(1)
	}
}

func BenchmarkReportCounterNoData(b *testing.B) {
	c := &counter{}
	for n := 0; n < b.N; n++ {
		c.report("foo", nil, NullStatsReporter)
	}
}

func BenchmarkReportCounterWithData(b *testing.B) {
	c := &counter{}
	for n := 0; n < b.N; n++ {
		c.Inc(1)
		c.report("foo", nil, NullStatsReporter)
	}
}

func BenchmarkGaugeSet(b *testing.B) {
	g := &gauge{}
	for n := 0; n < b.N; n++ {
		g.Update(42)
	}
}

func BenchmarkReportGaugeNoData(b *testing.B) {
	g := &gauge{}
	for n := 0; n < b.N; n++ {
		g.report("bar", nil, NullStatsReporter)
	}
}

func BenchmarkReportGaugeWithData(b *testing.B) {
	g := &gauge{}
	for n := 0; n < b.N; n++ {
		g.Update(73)
		g.report("bar", nil, NullStatsReporter)
	}
}

func BenchmarkTimerInterval(b *testing.B) {
	t := &timer{
		name:     "bencher",
		tags:     nil,
		reporter: NullStatsReporter,
	}
	for n := 0; n < b.N; n++ {
		t.Begin()() // call and imediately terminate
	}
}

func BenchmarkTimerReport(b *testing.B) {
	t := &timer{
		name:     "bencher",
		tags:     nil,
		reporter: NullStatsReporter,
	}
	for n := 0; n < b.N; n++ {
		t.Record(1234)
	}
}
