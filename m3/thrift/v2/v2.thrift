enum MetricType {
    INVALID = 0
    COUNTER = 1
    GAUGE = 2
    TIMER = 3
}

struct MetricValue {
    1: required MetricType metricType
    2: required i64 count
    3: required double gauge
    4: required i64 timer
}

struct MetricTag {
    1: required string name
    2: required string value
}

struct Metric {
    1: required string name
    2: required MetricValue value
    3: required i64 timestamp
    4: optional list<MetricTag> tags
}

struct MetricBatch {
    1: required list<Metric> metrics
    2: optional list<MetricTag> commonTags
}

service M3 {
    oneway void emitMetricBatchV2(1: MetricBatch batch)
}