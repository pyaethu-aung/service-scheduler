package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// ServiceName identifies the service in traces.
const ServiceName = "service-scheduler"

// Tracer returns the service's tracer from the global provider, so spans
// go wherever SetupTracing pointed them (nowhere until it is called).
func Tracer() trace.Tracer {
	return otel.Tracer("github.com/pyaethu-aung/service-scheduler")
}

// SetupTracing installs W3C trace-context propagation and, when endpoint is
// set, an OTLP gRPC exporter to it (for example otel-collector:4317). With
// no endpoint, spans are created but not exported. The returned function
// flushes and stops the exporter.
func SetupTracing(ctx context.Context, endpoint string) (shutdown func(context.Context) error, err error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", ServiceName))),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}
