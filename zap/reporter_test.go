package zap_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally"
	zapreporter "github.com/uber-go/tally/zap"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestReporter(t *testing.T) {
	var (
		core, logs = observer.New(zapcore.DebugLevel)
		logger     = zap.New(core)
		reporter   = zapreporter.New(logger)
	)

	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		Prefix:   "zap.reporter.test",
		Reporter: reporter,
	}, time.Millisecond)
	defer func() {
		require.NoError(t, closer.Close())
	}()

	scope.Tagged(map[string]string{"hello": "world"}).Counter("foo").Inc(123)
	scope.Gauge("bar").Update(456.0)
	scope.Timer("baz").Record(time.Second)

	dbuckets := tally.MustMakeLinearDurationBuckets(time.Second, time.Second, 3)
	scope.Histogram("bat", dbuckets).RecordDuration(time.Second + time.Nanosecond)

	vbuckets := tally.MustMakeLinearValueBuckets(1.0, 1.0, 3)
	scope.Histogram("quux", vbuckets).RecordValue(1.1)

	// Hacky, but we don't have hooks, so just wait.
	time.Sleep(100 * time.Millisecond)

	observed := logs.FilterMessageSnippet("Report").All()
	require.Len(t, observed, 5)

	observed = logs.FilterMessageSnippet("ReportCounter").All()
	require.Len(t, observed, 1)
	requireFieldKeyValue(t, observed[0].Context, "type", "counter")
	requireFieldKeyValue(t, observed[0].Context, "name", "zap.reporter.test.foo")
	requireFieldKeyValue(t, observed[0].Context, "value", 123)
	requireFieldKeyValue(t, observed[0].Context, "tags", map[string]string{"hello": "world"})

	observed = logs.FilterMessageSnippet("ReportGauge").All()
	require.Len(t, observed, 1)
	requireFieldKeyValue(t, observed[0].Context, "type", "gauge")
	requireFieldKeyValue(t, observed[0].Context, "name", "zap.reporter.test.bar")
	requireFieldKeyValue(t, observed[0].Context, "value", math.Float64bits(456.0))
	requireFieldKeyValue(t, observed[0].Context, "tags", map[string]string{})

	observed = logs.FilterMessageSnippet("ReportTimer").All()
	require.Len(t, observed, 1)
	requireFieldKeyValue(t, observed[0].Context, "type", "timer")
	requireFieldKeyValue(t, observed[0].Context, "name", "zap.reporter.test.baz")
	requireFieldKeyValue(t, observed[0].Context, "value", time.Second)
	requireFieldKeyValue(t, observed[0].Context, "tags", map[string]string{})

	observed = logs.FilterMessageSnippet("ReportHistogram").All()
	require.Len(t, observed, 2)
	requireFieldKeyValue(t, observed[0].Context, "type", "histogram")
	requireFieldKeyValue(t, observed[0].Context, "name", "zap.reporter.test.bat")
	requireFieldKeyValue(t, observed[0].Context, "value", 1)
	requireFieldKeyValue(t, observed[0].Context, "lowerBound", time.Second.String())
	requireFieldKeyValue(t, observed[0].Context, "upperBound", (2 * time.Second).String())
	requireFieldKeyValue(t, observed[1].Context, "type", "histogram")
	requireFieldKeyValue(t, observed[1].Context, "name", "zap.reporter.test.quux")
	requireFieldKeyValue(t, observed[1].Context, "value", 1)
	requireFieldKeyValue(t, observed[1].Context, "lowerBound", "1.000000")
	requireFieldKeyValue(t, observed[1].Context, "upperBound", "2.000000")
}

func requireFieldKeyValue(
	t *testing.T,
	src []zapcore.Field,
	key string,
	value any,
) {
	for i := 0; i < len(src); i++ {
		if src[i].Key == key {
			switch want := value.(type) {
			case string:
				require.Equal(t, want, src[i].String)
			case int, int64, uint64, float64:
				require.EqualValues(t, want, src[i].Integer)
			case time.Duration:
				require.EqualValues(t, want, src[i].Interface)
			case map[string]string:
				require.EqualValues(t, want, src[i].Interface)
			default:
				require.FailNow(t, "bug: non-exhaustive type switch", "%T", value)
			}
			return
		}
	}

	require.FailNow(t, "expected key not present", key)
}
