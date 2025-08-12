//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package trace provides distributed tracing functionality for the trpc-agent-go framework.
// It integrates with OpenTelemetry to provide comprehensive tracing capabilities.
package trace

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"
	noopt "go.opentelemetry.io/otel/trace/noop"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
)

// Tracer is the global tracer instance for telemetry.
var Tracer trace.Tracer = noopt.Tracer{}

// Start collects telemetry with optional configuration.
// The environment variables described below can be used for endpoint configuration.
//
// OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_TRACES_ENDPOINT (default: "https://localhost:4317")
// https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc
func Start(ctx context.Context, opts ...Option) (clean func() error, err error) {
	// Set default options
	options := &options{
		serviceName:      itelemetry.ServiceName,
		serviceVersion:   itelemetry.ServiceVersion,
		serviceNamespace: itelemetry.ServiceNamespace,
		protocol:         itelemetry.ProtocolGRPC, // Default to gRPC
	}
	for _, opt := range opts {
		opt(options)
	}

	// Set endpoint based on protocol if not explicitly set
	if options.tracesEndpoint == "" {
		options.tracesEndpoint = tracesEndpoint(options.protocol)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNamespace(options.serviceNamespace),
			semconv.ServiceName(options.serviceName),
			semconv.ServiceVersion(options.serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	var shutdownTracerProvider func(context.Context) error
	switch options.protocol {
	case itelemetry.ProtocolHTTP:
		shutdownTracerProvider, err = initHTTPTracerProvider(ctx, res, options)
	default:
		shutdownTracerProvider, err = initGRPCTracerProvider(ctx, res, options)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to initialize tracer provider: %w", err)
	}
	// Update global tracer
	Tracer = otel.Tracer(itelemetry.InstrumentName)
	return func() error {
		if err := shutdownTracerProvider(ctx); err != nil {
			return fmt.Errorf("failed to shutdown TracerProvider: %w", err)
		}
		return nil
	}, nil
}

// Option is a function that configures tracer options.
type Option func(*options)

// options holds the configuration options for tracer.
type options struct {
	tracesEndpoint    string
	tracesEndpointURL string
	serviceName       string
	serviceVersion    string
	serviceNamespace  string
	protocol          string            // Protocol to use (grpc or http)
	headers           map[string]string // Headers to send with the request
}

// WithEndpoint sets the traces endpoint(host and port) the Exporter will connect to.
// The provided endpoint should resemble "example.com:4317" (no scheme or path).
// If the OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT environment variable is set,
// and this option is not passed, that variable value will be used.
// If both environment variables are set, OTEL_EXPORTER_OTLP_TRACES_ENDPOINT will take precedence.
// If an environment variable is set, and this option is passed, this option will take precedence.
func WithEndpoint(endpoint string) Option {
	return func(opts *options) {
		opts.tracesEndpoint = endpoint
	}
}

// WithEndpointURL sets the target endpoint URL (scheme, host, port, path) the
// Exporter will connect to.
//
// If the OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
// environment variable is set, and this option is not passed, that variable
// value will be used. If both environment variables are set,
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT will take precedence. If an environment
// variable is set, and this option is passed, this option will take precedence.
//
// If both this option and WithEndpoint are used, the last used option will
// take precedence.
//
// If an invalid URL is provided, the default value will be kept.
//
// By default, if an environment variable is not set, and this option is not
// passed, "localhost:4318" will be used.
//
// This option has no effect if WithGRPCConn is used.
func WithEndpointURL(endpointURL string) Option {
	return func(opts *options) {
		opts.tracesEndpointURL = endpointURL
	}
}

// WithProtocol sets the protocol to use for traces export.
// Supported protocols are "grpc" (default) and "http".
func WithProtocol(protocol string) Option {
	return func(opts *options) {
		opts.protocol = protocol
	}
}

func WithHeaders(headers map[string]string) Option {
	return func(opts *options) {
		opts.headers = headers
	}
}

func tracesEndpoint(protocol string) string {
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		return endpoint
	}

	// Return different default endpoints based on protocol
	switch protocol {
	case itelemetry.ProtocolHTTP:
		return "localhost:4318" // HTTP endpoint base URL (otlptracehttp will add /v1/traces automatically)
	default:
		return "localhost:4317" // gRPC endpoint (host:port)
	}
}

// Initializes an OTLP gRPC exporter, and configures the corresponding trace provider.
func initGRPCTracerProvider(ctx context.Context, res *resource.Resource, opts *options) (
	func(context.Context) error, error) {
	tracesConn, err := itelemetry.NewGRPCConn(opts.tracesEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize traces connection: %w", err)
	}

	otelOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithGRPCConn(tracesConn),
		otlptracegrpc.WithEndpoint(opts.tracesEndpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithHeaders(opts.headers),
	}
	if opts.tracesEndpointURL != "" {
		otelOpts = append(otelOpts, otlptracegrpc.WithEndpointURL(opts.tracesEndpointURL))
	}
	// Set up a trace exporter
	traceExporter, err := otlptracegrpc.New(ctx, otelOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	return setupTracerProvider(res, traceExporter), nil
}

// Initializes an OTLP HTTP exporter, and configures the corresponding trace provider.
func initHTTPTracerProvider(ctx context.Context, res *resource.Resource, opts *options) (
	func(context.Context) error, error) {
	// Set up a trace exporter with HTTP endpoint
	otelOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(opts.tracesEndpoint),
		otlptracehttp.WithInsecure(),
		otlptracehttp.WithHeaders(opts.headers),
	}
	if opts.tracesEndpointURL != "" {
		otelOpts = append(otelOpts, otlptracehttp.WithEndpointURL(opts.tracesEndpointURL))
	}
	traceExporter, err := otlptracehttp.New(ctx, otelOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP trace exporter: %w", err)
	}

	return setupTracerProvider(res, traceExporter), nil
}

// setupTracerProvider sets up the tracer provider with the given resource and exporter.
func setupTracerProvider(res *resource.Resource, traceExporter sdktrace.SpanExporter) func(context.Context) error {
	// Register the trace exporter with a TracerProvider, using a batch
	// span processor to aggregate spans before export.
	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tracerProvider)

	// Set global propagator to tracecontext (the default is no-op).
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Shutdown will flush any remaining spans and shut down the exporter.
	return tracerProvider.Shutdown
}
