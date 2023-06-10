package tally

import (
	_ "github.com/golang/mock/mockgen/model"
)

//go:generate mockgen -package tallymock -destination tallymock/stats_reporter.go -imports github.com/uber-go/tally github.com/uber-go/tally StatsReporter
