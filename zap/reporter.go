package zap

import (
	"strconv"
	"time"

	"github.com/uber-go/tally"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	_ tally.StatsReporter = Reporter{}
	_ tally.Capabilities  = capabilities{}
)

type Reporter struct {
	logger *zap.Logger
}

func New(logger *zap.Logger) Reporter {
	return Reporter{
		logger: logger,
	}
}

func (r Reporter) ReportCounter(
	name string,
	tags map[string]string,
	value int64,
) {
	r.logger.Info(
		"ReportCounter",
		zap.String("type", "counter"),
		zap.String("name", name),
		zap.Object("tags", tagsMarshaler(tags)),
		zap.Int64("value", value),
	)
}

func (r Reporter) ReportGauge(
	name string,
	tags map[string]string,
	value float64,
) {
	r.logger.Info(
		"ReportGauge",
		zap.String("type", "gauge"),
		zap.String("name", name),
		zap.Object("tags", tagsMarshaler(tags)),
		zap.Float64("value", value),
	)
}

func (r Reporter) ReportTimer(
	name string,
	tags map[string]string,
	value time.Duration,
) {
	r.logger.Info(
		"ReportTimer",
		zap.String("type", "timer"),
		zap.String("name", name),
		zap.Object("tags", tagsMarshaler(tags)),
		zap.Stringer("value", value),
	)
}

func (r Reporter) ReportHistogramValueSamples(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
	lower float64,
	upper float64,
	value int64,
) {
	r.logger.Info(
		"ReportHistogramValueSamples",
		zap.String("type", "histogram"),
		zap.String("name", name),
		zap.Object("tags", tagsMarshaler(tags)),
		zap.String("lowerBound", strconv.FormatFloat(lower, 'f', 6, 64)),
		zap.String("upperBound", strconv.FormatFloat(upper, 'f', 6, 64)),
		zap.Int64("value", value),
	)
}

func (r Reporter) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
	lower time.Duration,
	upper time.Duration,
	value int64,
) {
	r.logger.Info(
		"ReportHistogramValueSamples",
		zap.String("type", "histogram"),
		zap.String("name", name),
		zap.Object("tags", tagsMarshaler(tags)),
		zap.String("lowerBound", lower.String()), // n.b. pre-stringify for assertions
		zap.String("upperBound", upper.String()),
		zap.Int64("value", value),
	)
}

func (r Reporter) Flush() {
	// nop
}

func (r Reporter) Capabilities() tally.Capabilities {
	return capabilities{}
}

type capabilities struct{}

func (capabilities) Reporting() bool { return true }
func (capabilities) Tagging() bool   { return true }

type tagsMarshaler map[string]string

func (m tagsMarshaler) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	for k, v := range m {
		enc.AddString(k, v)
	}
	return nil
}
