//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package metric provides metrics collection functionality for the trpc-agent-go framework.
// It integrates with OpenTelemetry to provide comprehensive metrics capabilities.
package metric

import (
	"context"
	"fmt"
	"os"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	noopm "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
)

var (
	// Meter is the global OpenTelemetry meter for the trpc-go-agent.
	Meter metric.Meter = noopm.Meter{}
)

// Start collects telemetry with optional configuration.
// The environment variables described below can be used for Endpoint configuration.
// OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (default: "https://localhost:4317")
// https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc
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
	if options.metricsEndpoint == "" {
		options.metricsEndpoint = metricsEndpoint(options.protocol)
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

	var shutdownMeterProvider func(context.Context) error
	switch options.protocol {
	case itelemetry.ProtocolHTTP:
		shutdownMeterProvider, err = initHTTPMeterProvider(ctx, res, options.metricsEndpoint)
	default:
		shutdownMeterProvider, err = initGRPCMeterProvider(ctx, res, options.metricsEndpoint)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to initialize meter provider: %w", err)
	}

	Meter = otel.Meter(itelemetry.InstrumentName)
	return func() error {
		if err := shutdownMeterProvider(ctx); err != nil {
			return fmt.Errorf("failed to shutdown MeterProvider: %w", err)
		}
		return err
	}, nil
}

func metricsEndpoint(protocol string) string {
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		return endpoint
	}

	// Return different default endpoints based on protocol
	switch protocol {
	case itelemetry.ProtocolHTTP:
		return "localhost:4318" // HTTP endpoint base URL (otlpmetrichttp will add /v1/metrics automatically)
	default:
		return "localhost:4317" // gRPC endpoint (host:port)
	}
}

// Initializes an OTLP HTTP exporter, and configures the corresponding meter provider.
func initHTTPMeterProvider(ctx context.Context, res *resource.Resource, endpoint string) (func(context.Context) error, error) {
	metricExporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(endpoint),
		otlpmetrichttp.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP metrics exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	return meterProvider.Shutdown, nil
}

// Initializes an OTLP gRPC exporter, and configures the corresponding meter provider.
func initGRPCMeterProvider(ctx context.Context, res *resource.Resource, endpoint string) (func(context.Context) error, error) {
	metricsConn, err := itelemetry.NewGRPCConn(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics connection: %w", err)
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithGRPCConn(metricsConn))
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	return meterProvider.Shutdown, nil
}

// Option is a function that configures meter options.
type Option func(*options)

// options holds the configuration options for meter.
type options struct {
	metricsEndpoint  string
	serviceName      string
	serviceVersion   string
	serviceNamespace string
	protocol         string // Protocol to use (grpc or http)
}

// WithEndpoint sets the metrics endpoint(host and port) the Exporter will connect to.
// The provided endpoint should resemble "example.com:4317" (no scheme or path).
// If the OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_METRICS_ENDPOINT environment variable is set,
// and this option is not passed, that variable value will be used.
// If both environment variables are set, OTEL_EXPORTER_OTLP_METRICS_ENDPOINT will take precedence.
// If an environment variable is set, and this option is passed, this option will take precedence.
func WithEndpoint(endpoint string) Option {
	return func(opts *options) {
		opts.metricsEndpoint = endpoint
	}
}

// WithProtocol sets the protocol to use for metrics export.
// Supported protocols are "grpc" (default) and "http".
func WithProtocol(protocol string) Option {
	return func(opts *options) {
		opts.protocol = protocol
	}
}
