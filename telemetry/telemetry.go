package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	noopm "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"
	noopt "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	// Tracer is the global OpenTelemetry tracer for the trpc-go-agent.
	Tracer trace.Tracer = noopt.Tracer{}
	// Meter is the global OpenTelemetry meter for the trpc-go-agent.
	Meter metric.Meter = noopm.Meter{}
)

// Start collects telemetry with optional configuration.
func Start(ctx context.Context, opts ...Option) (clean func() error, err error) {
	// Set default options
	options := &options{
		tracesEndpoint:   tracesEndpoint(),
		metricsEndpoint:  metricsEndpoint(),
		serviceName:      "telemetry",
		serviceVersion:   "v0.1.0",
		serviceNamespace: "trpc-go-agent",
	}
	for _, opt := range opts {
		opt(options)
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

	tracesConn, err := newConn(options.tracesEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize traces connection: %w", err)
	}
	shutdownTracerProvider, err := initTracerProvider(ctx, res, tracesConn)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize tracer provider: %w", err)
	}

	metricsConn := tracesConn
	if options.metricsEndpoint != options.tracesEndpoint {
		metricsConn, err = newConn(options.metricsEndpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize metrics connection: %w", err)
		}
	}
	shutdownMeterProvider, err := initMeterProvider(ctx, res, metricsConn)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize meter provider: %w", err)
	}

	clean = func() error {
		var err error
		if tracerErr := shutdownTracerProvider(ctx); tracerErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to shutdown TracerProvider: %w", tracerErr))
		}
		if meterErr := shutdownMeterProvider(ctx); meterErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to shutdown MeterProvider: %w", meterErr))
		}
		return err
	}

	// Update global tracer and meter
	Tracer = otel.Tracer("trpc.agent")
	Meter = otel.Meter("trpc.agent")
	return clean, nil
}

// https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc
// https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp
func tracesEndpoint() string {
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return "localhost:4317" // default endpoint
}

// https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc
// https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp
func metricsEndpoint() string {
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return "localhost:4318"
}

// Initializes an OTLP exporter, and configures the corresponding trace provider.
func initTracerProvider(ctx context.Context, res *resource.Resource, conn *grpc.ClientConn) (func(context.Context) error, error) {
	// Set up a trace exporter
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

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
	return tracerProvider.Shutdown, nil
}

// Initializes an OTLP exporter, and configures the corresponding meter provider.
func initMeterProvider(ctx context.Context, res *resource.Resource, conn *grpc.ClientConn) (func(context.Context) error, error) {
	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithGRPCConn(conn))
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

func newConn(endpoint string) (*grpc.ClientConn, error) {
	// It connects the OpenTelemetry Collector through gRPC connection.
	// You can customize the endpoint using SetConfig() or environment variables.
	conn, err := grpc.NewClient(endpoint,
		// Note the use of insecure transport here. TLS is recommended in production.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	return conn, err
}

// Option is a function that configures telemetry options.
type Option func(*options)

// options holds the configuration options for telemetry.
type options struct {
	tracesEndpoint   string
	metricsEndpoint  string
	serviceName      string
	serviceVersion   string
	serviceNamespace string
}

// WithTracesEndpoint sets the traces endpoint(host and port) the Exporter will connect to.
// The provided endpoint should resemble "example.com:4317" (no scheme or path).
// If the OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT environment variable is set,
// and this option is not passed, that variable value will be used.
// If both environment variables are set, OTEL_EXPORTER_OTLP_TRACES_ENDPOINT will take precedence.
// If an environment variable is set, and this option is passed, this option will take precedence.
func WithTracesEndpoint(endpoint string) Option {
	return func(opts *options) {
		opts.tracesEndpoint = endpoint
	}
}

// WithMetricsEndpoint sets the metrics endpoint(host and port) the Exporter will connect to.
// The provided endpoint should resemble "example.com:4317" (no scheme or path).
// If the OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_METRICS_ENDPOINT environment variable is set,
// and this option is not passed, that variable value will be used.
// If both environment variables are set, OTEL_EXPORTER_OTLP_METRICS_ENDPOINT will take precedence.
// If an environment variable is set, and this option is passed, this option will take precedence.
func WithMetricsEndpoint(endpoint string) Option {
	return func(opts *options) {
		opts.metricsEndpoint = endpoint
	}
}
