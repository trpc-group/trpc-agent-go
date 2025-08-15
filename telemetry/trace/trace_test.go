//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package trace

import (
	"context"
	"os"
	"testing"
)

func TestGRPCTracesEndpoint(t *testing.T) {
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
	if ep := tracesEndpoint("grpc"); ep != customEndpoint {
		t.Fatalf("expected %s, got %s", customEndpoint, ep)
	}

	// Case 2: fallback to generic when specific is empty.
	_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", genericEndpoint)
	if ep := tracesEndpoint("grpc"); ep != genericEndpoint {
		t.Fatalf("expected %s, got %s", genericEndpoint, ep)
	}

	// Case 3: default when none set.
	_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if ep := tracesEndpoint("grpc"); ep == "" {
		t.Fatalf("expected non-empty default endpoint")
	}
}

// TestStartAndClean exercises the happy-path of Start and returned cleanup.
func TestStartAndClean(t *testing.T) {
	const (
		traceEP = "localhost:4317"
	)

	ctx := context.Background()
	clean, err := Start(ctx,
		WithEndpoint(traceEP),
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
