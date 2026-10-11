package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Booking outcomes, the label values of service_scheduler_bookings_total.
const (
	OutcomeCreated           = "created"
	OutcomeReplayed          = "replayed"
	OutcomeNoCapacity        = "no_capacity"
	OutcomeConflictExhausted = "conflict_exhausted"
	OutcomeInvalid           = "invalid"
)

// Metrics holds the Section 9 metrics on their own registry. A nil
// *Metrics records nothing, so services can run without it in tests.
type Metrics struct {
	registry        *prometheus.Registry
	httpDuration    *prometheus.HistogramVec
	bookings        *prometheus.CounterVec
	conflictRetries prometheus.Counter
	slotsReturned   prometheus.Histogram
}

// NewMetrics registers the Section 9 metrics plus the Go runtime and
// process collectors.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_server_request_duration_seconds",
			Help:    "HTTP request latency by route template, method and status.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method", "status"}),
		bookings: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "service_scheduler_bookings_total",
			Help: "Booking requests by outcome.",
		}, []string{"outcome"}),
		conflictRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "service_scheduler_booking_conflict_retries_total",
			Help: "Booking attempts retried after losing a race (23P01 or 40P01).",
		}),
		slotsReturned: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "service_scheduler_availability_slots_returned",
			Help:    "Free start times returned per availability request.",
			Buckets: []float64{0, 1, 5, 10, 20, 40, 60, 96},
		}),
	}
	m.registry.MustRegister(
		m.httpDuration, m.bookings, m.conflictRetries, m.slotsReturned,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	for _, o := range []string{OutcomeCreated, OutcomeReplayed, OutcomeNoCapacity, OutcomeConflictExhausted, OutcomeInvalid} {
		m.bookings.WithLabelValues(o) // export every outcome from zero
	}
	return m
}

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// Register adds further collectors, such as the database pool's.
func (m *Metrics) Register(c prometheus.Collector) {
	m.registry.MustRegister(c)
}

// ObserveHTTP records one request.
func (m *Metrics) ObserveHTTP(route, method string, status int, d time.Duration) {
	if m == nil {
		return
	}
	m.httpDuration.WithLabelValues(route, method, strconv.Itoa(status)).Observe(d.Seconds())
}

// Booking counts one booking request by outcome.
func (m *Metrics) Booking(outcome string) {
	if m == nil {
		return
	}
	m.bookings.WithLabelValues(outcome).Inc()
}

// ConflictRetry counts one retried booking attempt.
func (m *Metrics) ConflictRetry() {
	if m == nil {
		return
	}
	m.conflictRetries.Inc()
}

// SlotsReturned records how many free starts one availability request
// returned.
func (m *Metrics) SlotsReturned(n int) {
	if m == nil {
		return
	}
	m.slotsReturned.Observe(float64(n))
}
