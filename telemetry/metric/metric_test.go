//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package metric

import (
	"context"
	"os"
	"testing"
)

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

// TestStartAndClean exercises the happy-path of Start and returned cleanup.
func TestStartAndClean(t *testing.T) {
	const (
		metricEP = "localhost:4318"
	)

	ctx := context.Background()
	clean, err := Start(ctx,
		WithEndpoint(metricEP),
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
