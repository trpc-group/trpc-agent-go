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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

// InitMeterProvider initializes the meter provider and default meters.
func InitMeterProvider(mp metric.MeterProvider) error {
	itelemetry.MeterProvider = mp

	itelemetry.ChatMeter = mp.Meter(metrics.MeterNameChat)
	var err error
	if itelemetry.ChatMetricTRPCAgentGoClientRequestCnt, err = itelemetry.ChatMeter.Int64Counter(
		metrics.MetricTRPCAgentGoClientRequestCnt,
		metric.WithDescription("Total number of client requests"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric TRPCAgentGoClientRequestCnt: %w", err)
	}
	if itelemetry.ChatMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricGenAIClientTokenUsage,
		metric.WithDescription("Token usage for client"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric GenAIClientTokenUsage: %w", err)
	}
	if itelemetry.ChatMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of client operation"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric GenAIClientOperationDuration: %w", err)
	}
	if itelemetry.ChatMetricGenAIServerTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricGenAIServerTimeToFirstToken,
		metric.WithDescription("Time to first token for server"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric GenAIServerTimeToFirstToken: %w", err)
	}
	if itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithDescription("Time to first token (legacy metric name)"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric TRPCAgentGoClientTimeToFirstToken: %w", err)
	}
	if itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricTRPCAgentGoClientTimePerOutputToken,
		metric.WithDescription("Time per output token for client"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric TRPCAgentGoClientTimePerOutputToken: %w", err)
	}
	if itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricTRPCAgentGoClientOutputTokenPerTime,
		metric.WithDescription("Output token per time for client"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric TRPCAgentGoClientOutputTokenPerTime: %w", err)
	}

	itelemetry.ExecuteToolMeter = mp.Meter(metrics.MeterNameExecuteTool)
	if itelemetry.ExecuteToolMetricTRPCAgentGoClientRequestCnt, err = itelemetry.ExecuteToolMeter.Int64Counter(
		metrics.MetricTRPCAgentGoClientRequestCnt,
		metric.WithDescription("Total number of client requests"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create execute tool metric TRPCAgentGoClientRequestCnt: %w", err)
	}
	if itelemetry.ExecuteToolMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameExecuteTool,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of client operation"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create execute tool metric GenAIClientOperationDuration: %w", err)
	}

	if err := initInvokeAgentMetrics(mp); err != nil {
		return err
	}

	return nil
}

// GetMeterProvider returns the meter provider.
func GetMeterProvider() metric.MeterProvider {
	return itelemetry.MeterProvider
}

// SetHistogramBuckets updates bucket boundaries for a specific histogram metric.
// The metricName should be one of the defined metric names in the metrics package.
// Note: This creates a new histogram instrument; old data is not migrated.
func SetHistogramBuckets(meterName string, metricName string, boundaries []float64) error {
	switch meterName {
	case metrics.MeterNameChat:
		return setChatHistogramBuckets(metricName, boundaries)
	case metrics.MeterNameExecuteTool:
		return setExecuteToolHistogramBuckets(metricName, boundaries)
	case metrics.MeterNameInvokeAgent:
		return setInvokeAgentHistogramBuckets(metricName, boundaries)
	default:
		return fmt.Errorf("unknown or unsupported meter name: %s", meterName)
	}
}

func setChatHistogramBuckets(metricName string, boundaries []float64) error {
	switch metricName {
	case metrics.MetricGenAIClientOperationDuration:
		if itelemetry.ChatMetricGenAIClientOperationDuration == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricGenAIClientOperationDuration.SetBuckets(boundaries)
	case metrics.MetricGenAIClientTokenUsage:
		if itelemetry.ChatMetricGenAIClientTokenUsage == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricGenAIClientTokenUsage.SetBuckets(boundaries)
	case metrics.MetricGenAIServerTimeToFirstToken:
		if itelemetry.ChatMetricGenAIServerTimeToFirstToken == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricGenAIServerTimeToFirstToken.SetBuckets(boundaries)
	case metrics.MetricTRPCAgentGoClientTimeToFirstToken:
		if itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken.SetBuckets(boundaries)
	case metrics.MetricTRPCAgentGoClientTimePerOutputToken:
		if itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken.SetBuckets(boundaries)
	case metrics.MetricTRPCAgentGoClientOutputTokenPerTime:
		if itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime.SetBuckets(boundaries)
	default:
		return fmt.Errorf("unknown or unsupported chat histogram metric: %s", metricName)
	}
}

func setExecuteToolHistogramBuckets(metricName string, boundaries []float64) error {
	switch metricName {
	case metrics.MetricGenAIClientOperationDuration:
		if itelemetry.ExecuteToolMetricGenAIClientOperationDuration == nil {
			return fmt.Errorf("execute tool metric %s not initialized", metricName)
		}
		return itelemetry.ExecuteToolMetricGenAIClientOperationDuration.SetBuckets(boundaries)
	default:
		return fmt.Errorf("unknown or unsupported execute tool histogram metric: %s", metricName)
	}
}

func setInvokeAgentHistogramBuckets(metricName string, boundaries []float64) error {
	switch metricName {
	case metrics.MetricTRPCAgentGoClientTimeToFirstToken:
		if itelemetry.InvokeAgentMetricGenAIClientTimeToFirstToken == nil {
			return fmt.Errorf("invoke agent metric %s not initialized", metricName)
		}
		return itelemetry.InvokeAgentMetricGenAIClientTimeToFirstToken.SetBuckets(boundaries)
	case metrics.MetricGenAIClientTokenUsage:
		if itelemetry.InvokeAgentMetricGenAIClientTokenUsage == nil {
			return fmt.Errorf("invoke agent metric %s not initialized", metricName)
		}
		return itelemetry.InvokeAgentMetricGenAIClientTokenUsage.SetBuckets(boundaries)
	case metrics.MetricGenAIClientOperationDuration:
		if itelemetry.InvokeAgentMetricGenAIClientOperationDuration == nil {
			return fmt.Errorf("invoke agent metric %s not initialized", metricName)
		}
		return itelemetry.InvokeAgentMetricGenAIClientOperationDuration.SetBuckets(boundaries)
	default:
		return fmt.Errorf("unknown or unsupported invoke agent histogram metric: %s", metricName)
	}
}

func initInvokeAgentMetrics(mp metric.MeterProvider) error {
	if mp == nil {
		return fmt.Errorf("invoke agent meter provider is nil")
	}

	itelemetry.InvokeAgentMeter = mp.Meter(metrics.MeterNameInvokeAgent)
	meterName := metrics.MeterNameInvokeAgent
	var err error
	if itelemetry.InvokeAgentMetricGenAIRequestCnt, err = itelemetry.InvokeAgentMeter.Int64Counter(
		metrics.MetricTRPCAgentGoClientRequestCnt,
		metric.WithDescription("Total number of gen ai requests"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricTRPCAgentGoClientRequestCnt, err)
	}
	if itelemetry.InvokeAgentMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(
		mp,
		metrics.MeterNameInvokeAgent,
		metrics.MetricGenAIClientTokenUsage,
		metric.WithDescription("Input tokens usage"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricGenAIClientTokenUsage, err)
	}
	if itelemetry.InvokeAgentMetricGenAIClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameInvokeAgent,
		metrics.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithDescription("Time to first token for client"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricTRPCAgentGoClientTimeToFirstToken, err)
	}
	if itelemetry.InvokeAgentMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameInvokeAgent,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of client operation"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricGenAIClientOperationDuration, err)
	}

	return nil
}

// NewMeterProvider creates a new meter provider with optional configuration.
// The environment variables described below can be used for Endpoint configuration.
// OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (default: "https://localhost:4317")
// https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc
func NewMeterProvider(ctx context.Context, opts ...Option) (*sdkmetric.MeterProvider, error) {
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

	res, err := buildResource(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	var meterProvider *sdkmetric.MeterProvider
	switch options.protocol {
	case itelemetry.ProtocolHTTP:
		meterProvider, err = newHTTPMeterProvider(ctx, res, options.metricsEndpoint)
	default:
		meterProvider, err = newGRPCMeterProvider(ctx, res, options.metricsEndpoint)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to initialize meter provider: %w", err)
	}

	return meterProvider, nil
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
func newHTTPMeterProvider(ctx context.Context, res *resource.Resource, endpoint string) (*sdkmetric.MeterProvider, error) {
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

	return meterProvider, nil
}

// Initializes an OTLP gRPC exporter, and configures the corresponding meter provider.
func newGRPCMeterProvider(ctx context.Context, res *resource.Resource, endpoint string) (*sdkmetric.MeterProvider, error) {
	metricsConn, err := itelemetry.NewGRPCConn(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics connection: %w", err)
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithGRPCConn(metricsConn))
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)

	return meterProvider, nil
}

// Option is a function that configures meter options.
type Option func(*options)

// options holds the configuration options for meter.
type options struct {
	metricsEndpoint    string
	serviceName        string
	serviceVersion     string
	serviceNamespace   string
	protocol           string // Protocol to use (grpc or http)
	resourceAttributes *[]attribute.KeyValue
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

// WithServiceName overrides the service.name resource attribute.
func WithServiceName(serviceName string) Option {
	return func(opts *options) {
		opts.serviceName = serviceName
	}
}

// WithServiceNamespace overrides the service.namespace resource attribute.
func WithServiceNamespace(serviceNamespace string) Option {
	return func(opts *options) {
		opts.serviceNamespace = serviceNamespace
	}
}

// WithServiceVersion overrides the service.version resource attribute.
func WithServiceVersion(serviceVersion string) Option {
	return func(opts *options) {
		opts.serviceVersion = serviceVersion
	}
}

// WithResourceAttributes appends custom resource attributes.
func WithResourceAttributes(attrs ...attribute.KeyValue) Option {
	return func(opts *options) {
		if len(attrs) == 0 {
			return
		}
		if opts.resourceAttributes == nil {
			opts.resourceAttributes = &[]attribute.KeyValue{}
		}
		*opts.resourceAttributes = append(*opts.resourceAttributes, attrs...)
	}
}

func buildResource(ctx context.Context, options *options) (*resource.Resource, error) {
	// Build resource with options values
	resourceOpts := []resource.Option{
		resource.WithAttributes(
			semconv.ServiceNamespace(options.serviceNamespace),
			semconv.ServiceName(options.serviceName),
			semconv.ServiceVersion(options.serviceVersion),
		),
		resource.WithFromEnv(),
		resource.WithHost(),         // Adds host.name
		resource.WithTelemetrySDK(), // Adds telemetry.sdk.{name,language,version}
	}

	// Append custom resource attributes
	if options.resourceAttributes != nil && len(*options.resourceAttributes) > 0 {
		resourceOpts = append(resourceOpts, resource.WithAttributes(*options.resourceAttributes...))
	}

	return resource.New(ctx, resourceOpts...)
}
