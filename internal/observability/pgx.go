package observability

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// QueryTracer is a pgx tracer that opens one span per SQL statement, named
// after the sqlc query ("BookAppointment"), never the SQL text, so spans
// carry no parameters or customer data. It only records statements that run
// inside a traced operation; a statement without a parent span (a probe or
// a background job) would otherwise appear in Jaeger as a stray root trace.
// A zero QueryTracer uses the global tracer provider.
type QueryTracer struct {
	Tracer trace.Tracer
}

var _ pgx.QueryTracer = QueryTracer{}

// TraceQueryStart starts the statement's span.
func (q QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx // no parent: TraceQueryEnd ends the no-op span in ctx
	}
	tracer := q.Tracer
	if tracer == nil {
		tracer = Tracer()
	}
	ctx, _ = tracer.Start(ctx, QueryName(data.SQL),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.system", "postgresql")))
	return ctx
}

// TraceQueryEnd ends the span, recording any error.
func (q QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span := trace.SpanFromContext(ctx)
	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
	}
	span.End()
}

var sqlcName = regexp.MustCompile(`^\s*-- name: (\w+)`)

// QueryName names a statement: the sqlc query name when there is one,
// otherwise its leading keyword (BEGIN, COMMIT, SELECT, ...).
func QueryName(sql string) string {
	if m := sqlcName.FindStringSubmatch(sql); m != nil {
		return m[1]
	}
	if f := strings.Fields(sql); len(f) > 0 {
		return strings.ToUpper(f[0])
	}
	return "query"
}

// PoolStats is what pgxpool.Stat reports.
type PoolStats interface {
	AcquiredConns() int32
	IdleConns() int32
	TotalConns() int32
	MaxConns() int32
	AcquireCount() int64
	EmptyAcquireCount() int64
	AcquireDuration() time.Duration
}

// NewPoolCollector exports pool saturation as db_pool_* metrics, reading
// stats on every scrape (Section 9: connection saturation before it becomes
// latency).
func NewPoolCollector[S PoolStats](stat func() S) prometheus.Collector {
	return poolCollector[S]{stat: stat}
}

type poolCollector[S PoolStats] struct {
	stat func() S
}

var (
	poolAcquired = prometheus.NewDesc("db_pool_acquired_conns", "Connections currently in use.", nil, nil)
	poolIdle     = prometheus.NewDesc("db_pool_idle_conns", "Idle connections.", nil, nil)
	poolTotal    = prometheus.NewDesc("db_pool_total_conns", "Open connections.", nil, nil)
	poolMax      = prometheus.NewDesc("db_pool_max_conns", "Maximum connections.", nil, nil)
	poolAcquires = prometheus.NewDesc("db_pool_acquires_total", "Connections acquired.", nil, nil)
	poolWaits    = prometheus.NewDesc("db_pool_empty_acquires_total", "Acquires that had to wait for a connection.", nil, nil)
	poolWait     = prometheus.NewDesc("db_pool_acquire_duration_seconds_total", "Total time spent acquiring connections.", nil, nil)
)

func (c poolCollector[S]) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{poolAcquired, poolIdle, poolTotal, poolMax, poolAcquires, poolWaits, poolWait} {
		ch <- d
	}
}

func (c poolCollector[S]) Collect(ch chan<- prometheus.Metric) {
	s := c.stat()
	ch <- prometheus.MustNewConstMetric(poolAcquired, prometheus.GaugeValue, float64(s.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(poolIdle, prometheus.GaugeValue, float64(s.IdleConns()))
	ch <- prometheus.MustNewConstMetric(poolTotal, prometheus.GaugeValue, float64(s.TotalConns()))
	ch <- prometheus.MustNewConstMetric(poolMax, prometheus.GaugeValue, float64(s.MaxConns()))
	ch <- prometheus.MustNewConstMetric(poolAcquires, prometheus.CounterValue, float64(s.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(poolWaits, prometheus.CounterValue, float64(s.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(poolWait, prometheus.CounterValue, s.AcquireDuration().Seconds())
}
