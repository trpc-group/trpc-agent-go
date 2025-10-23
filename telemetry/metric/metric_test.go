//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package metric

import (
	"context"
	"os"
	"testing"
)

// TestMetricsEndpoint validates metrics endpoint precedence rules.
func TestGRPCMetricsEndpoint(t *testing.T) {
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
	if ep := metricsEndpoint("grpc"); ep != customEndpoint {
		t.Fatalf("expected %s, got %s", customEndpoint, ep)
	}

	_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", genericEndpoint)
	if ep := metricsEndpoint("grpc"); ep != genericEndpoint {
		t.Fatalf("expected %s, got %s", genericEndpoint, ep)
	}

	_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if ep := metricsEndpoint("grpc"); ep != "localhost:4317" {
		t.Fatalf("expected default gRPC endpoint localhost:4317, got %s", ep)
	}

	if ep := metricsEndpoint("http"); ep != "localhost:4318" {
		t.Fatalf("expected default HTTP endpoint localhost:4318, got %s", ep)
	}
}

// TestStartAndClean exercises various Start configurations and cleanup.
func TestStartAndClean(t *testing.T) {
	tests := []struct {
		name        string
		opts        []Option
		expectError bool
	}{
		{
			name: "gRPC endpoint",
			opts: []Option{
				WithEndpoint("localhost:4317"),
				WithProtocol("grpc"),
			},
		},
		{
			name: "HTTP endpoint",
			opts: []Option{
				WithEndpoint("localhost:4318"),
				WithProtocol("http"),
			},
		},
		{
			name: "default options",
			opts: []Option{},
		},
		{
			name: "custom endpoint",
			opts: []Option{
				WithEndpoint("custom:4317"),
			},
		},
		{
			name: "resilient to empty endpoint",
			opts: []Option{
				WithEndpoint(""),
			},
		},
		{
			name: "resilient to invalid protocol",
			opts: []Option{
				WithProtocol("invalid"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			clean, err := Start(ctx, tt.opts...)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Start returned unexpected error: %v", err)
			}
			if clean == nil {
				t.Fatal("expected non-nil cleanup function")
			}

			if err := clean(); err != nil {
				// Ignore cleanup errors in tests as no real collector is running
				t.Logf("cleanup error (expected in tests): %v", err)
			}
		})
	}
}

// TestOptions validates option functions
func TestOptions(t *testing.T) {
	opts := &options{
		protocol:    "grpc",
		serviceName: "original",
	}

	tests := []struct {
		name     string
		option   Option
		validate func(*testing.T, *options)
	}{
		{
			name:   "WithEndpoint",
			option: WithEndpoint("test:4317"),
			validate: func(t *testing.T, opts *options) {
				if opts.metricsEndpoint != "test:4317" {
					t.Errorf("expected endpoint test:4317, got %s", opts.metricsEndpoint)
				}
			},
		},
		{
			name:   "WithProtocol",
			option: WithProtocol("http"),
			validate: func(t *testing.T, opts *options) {
				if opts.protocol != "http" {
					t.Errorf("expected protocol http, got %s", opts.protocol)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy of original options for each test
			testOpts := *opts
			tt.option(&testOpts)
			tt.validate(t, &testOpts)
		})
	}
}
