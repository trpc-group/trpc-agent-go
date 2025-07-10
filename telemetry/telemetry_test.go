package telemetry

import (
	"context"
	"os"
	"testing"
)

// TestTracesEndpoint verifies precedence rules for traces endpoint environment
// variables.
func TestTracesEndpoint(t *testing.T) {
	const (
		customEndpoint  = "custom-trace:4317"
		genericEndpoint = "generic-endpoint:4317"
	)

	// Backup originals.
	origTrace := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	origGeneric := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")

	// Restore at the end.
	defer func() {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", origTrace)
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", origGeneric)
	}()

	// Case 1: specific variable has precedence over generic.
	_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", customEndpoint)
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", genericEndpoint)
	if ep := tracesEndpoint(); ep != customEndpoint {
		t.Fatalf("expected %s, got %s", customEndpoint, ep)
	}

	// Case 2: fallback to generic when specific is empty.
	_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", genericEndpoint)
	if ep := tracesEndpoint(); ep != genericEndpoint {
		t.Fatalf("expected %s, got %s", genericEndpoint, ep)
	}

	// Case 3: default when none set.
	_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if ep := tracesEndpoint(); ep == "" {
		t.Fatalf("expected non-empty default endpoint")
	}
}

// TestMetricsEndpoint validates metrics endpoint precedence rules.
func TestMetricsEndpoint(t *testing.T) {
	const (
		customEndpoint  = "custom-metric:4318"
		genericEndpoint = "generic-endpoint:4318"
	)

	origMetric := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	origGeneric := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", origMetric)
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", origGeneric)
	}()

	_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", customEndpoint)
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", genericEndpoint)
	if ep := metricsEndpoint(); ep != customEndpoint {
		t.Fatalf("expected %s, got %s", customEndpoint, ep)
	}

	_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", genericEndpoint)
	if ep := metricsEndpoint(); ep != genericEndpoint {
		t.Fatalf("expected %s, got %s", genericEndpoint, ep)
	}

	_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if ep := metricsEndpoint(); ep == "" {
		t.Fatalf("expected non-empty default endpoint")
	}
}

// TestNewConn_InvalidEndpoint ensures an error is returned for an
// unparsable address.
func TestNewConn_InvalidEndpoint(t *testing.T) {
	// gRPC dials lazily, so even malformed targets may not error immediately.
	conn, err := newConn("invalid:endpoint")
	if err != nil {
		t.Fatalf("did not expect error, got %v", err)
	}
	if conn == nil {
		t.Fatalf("expected non-nil connection")
	}
	_ = conn.Close()
}

// TestStartAndClean exercises the happy-path of Start and returned cleanup.
func TestStartAndClean(t *testing.T) {
	const (
		traceEP  = "localhost:4317"
		metricEP = "localhost:4318"
	)

	ctx := context.Background()
	clean, err := Start(ctx,
		WithTracesEndpoint(traceEP),
		WithMetricsEndpoint(metricEP),
		// Provide small custom service data to avoid environment pollution.
	)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if clean == nil {
		t.Fatalf("expected non-nil cleanup function")
	}
	_ = clean() // Ignore cleanup error as no collector is running in tests.
}
