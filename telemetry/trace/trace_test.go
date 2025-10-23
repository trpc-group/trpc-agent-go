//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
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
		// lint keep alignment
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
	// Start a span to ensure Tracer is initialized
	_, span := Tracer.Start(ctx, "test-span")
	span.End()
	_ = clean() // Ignore cleanup error as no collector is running in tests.
}

func TestStartGRPC_WithURLAndHeaders(t *testing.T) {
	ctx := context.Background()
	clean, err := Start(ctx,
		WithProtocol("grpc"),
		WithEndpoint("localhost:4317"),
		WithEndpointURL("localhost:9999"),
		WithHeaders(map[string]string{"Authorization": "Bearer abc"}),
	)
	if err != nil {
		t.Fatalf("Start(grpc) returned error: %v", err)
	}
	if clean == nil {
		t.Fatalf("expected non-nil cleanup function")
	}
	_ = clean()
}

func TestParseEndpointURL(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		endpoint  string
		urlPath   string
		wantError bool
	}{
		{"with scheme and path", "http://localhost:3000/api/public/otel", "localhost:3000", "/api/public/otel", false},
		{"without scheme", "collector:4318/otlp/v1/traces", "collector:4318", "/otlp/v1/traces", false},
		{"no path implies slash", "example.com", "example.com", "/", false},
		{"no host error", "http:///missing-host", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endp, path, err := parseEndpointURL(tc.in)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got none (endpoint=%q, path=%q)", endp, path)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if endp != tc.endpoint || path != tc.urlPath {
				t.Fatalf("expected (%q,%q), got (%q,%q)", tc.endpoint, tc.urlPath, endp, path)
			}
		})
	}
}

func TestStartHTTP_WithURLAndHeaders(t *testing.T) {
	ctx := context.Background()
	clean, err := Start(ctx,
		WithProtocol("http"),
		WithEndpoint("localhost:4318"),
		WithEndpointURL("http://localhost:4318/custom/path"),
		WithHeaders(map[string]string{"X-Test": "yes"}),
	)
	if err != nil {
		t.Fatalf("Start(http) returned error: %v", err)
	}
	if clean == nil {
		t.Fatalf("expected non-nil cleanup function")
	}
	_ = clean()
}

func TestStartHTTP_InvalidEndpointURL(t *testing.T) {
	ctx := context.Background()
	_, err := Start(ctx,
		WithProtocol("http"),
		WithEndpoint("localhost:4318"),
		WithEndpointURL("http:///bad"), // missing host should fail
	)
	if err == nil {
		t.Fatalf("expected error from invalid endpoint URL")
	}
}

func TestStartHTTP_DefaultNoEnv_NoEndpoint(t *testing.T) {
	// ensure env empty
	origTrace := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	origGeneric := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", origTrace)
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", origGeneric)
	}()
	_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	ctx := context.Background()
	clean, err := Start(ctx,
		WithProtocol("http"),
		WithHeaders(map[string]string{"k": "v"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clean == nil {
		t.Fatalf("expected cleanup")
	}
	_ = clean()
}

func TestStartGRPC_DefaultNoEnv_NoEndpoint(t *testing.T) {
	// ensure env empty
	origTrace := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	origGeneric := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", origTrace)
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", origGeneric)
	}()
	_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	ctx := context.Background()
	clean, err := Start(ctx,
		WithHeaders(map[string]string{"k": "v"}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clean == nil {
		t.Fatalf("expected cleanup")
	}
	_ = clean()
}

func TestStartHTTP_WithURL_NoScheme(t *testing.T) {
	ctx := context.Background()
	clean, err := Start(ctx,
		WithProtocol("http"),
		WithEndpointURL("collector:4318/otlp/v1/traces"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clean == nil {
		t.Fatalf("expected cleanup")
	}
	_ = clean()
}
