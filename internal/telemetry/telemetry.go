// Package telemetry provides OpenTelemetry setup for the AgentAnycast daemon.
//
// When enabled, it initializes a TracerProvider with an OTLP gRPC exporter and
// registers it as the global tracer provider. All spans are tagged with
// service.name=agentanycast-node.
package telemetry

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Config holds telemetry configuration.
type Config struct {
	// Enabled controls whether tracing is active.
	Enabled bool

	// OTLPEndpoint is the gRPC endpoint for the OTLP collector (e.g. "localhost:4317").
	OTLPEndpoint string

	// SampleRate is the fraction of traces to sample (0.0 - 1.0). Default: 1.0.
	SampleRate float64

	// ServiceName overrides the default service name.
	ServiceName string

	// Version is the service version.
	Version string

	// Logger is the structured logger.
	Logger *slog.Logger
}

// ShutdownFunc is called to flush and shut down the tracer provider.
type ShutdownFunc func(ctx context.Context) error

// Setup initializes the OpenTelemetry tracer provider and returns a shutdown function.
// If tracing is disabled, it returns a no-op shutdown function and sets a no-op tracer.
func Setup(ctx context.Context, cfg Config) (ShutdownFunc, error) {
	if !cfg.Enabled || cfg.OTLPEndpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 1.0
	}
	if cfg.SampleRate > 1.0 {
		cfg.SampleRate = 1.0
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "agentanycast-node"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.Version),
		),
	)
	if err != nil {
		return nil, err
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRate))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	cfg.Logger.Info("OpenTelemetry tracing enabled",
		"endpoint", cfg.OTLPEndpoint,
		"sample_rate", cfg.SampleRate,
	)

	shutdown := func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(shutdownCtx)
	}

	return shutdown, nil
}

// Tracer returns a named tracer from the global tracer provider.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
