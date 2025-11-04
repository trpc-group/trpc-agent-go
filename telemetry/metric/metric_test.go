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

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
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
		{
			name:   "WithServiceName",
			option: WithServiceName("custom-service"),
			validate: func(t *testing.T, opts *options) {
				if opts.serviceName != "custom-service" {
					t.Errorf("expected service name custom-service, got %s", opts.serviceName)
				}
				if !opts.serviceNameSet {
					t.Errorf("expected serviceNameSet to be true")
				}
			},
		},
		{
			name:   "WithServiceNamespace",
			option: WithServiceNamespace("custom-ns"),
			validate: func(t *testing.T, opts *options) {
				if opts.serviceNamespace != "custom-ns" {
					t.Errorf("expected service namespace custom-ns, got %s", opts.serviceNamespace)
				}
				if !opts.serviceNamespaceSet {
					t.Errorf("expected serviceNamespaceSet to be true")
				}
			},
		},
		{
			name:   "WithServiceVersion",
			option: WithServiceVersion("1.2.3"),
			validate: func(t *testing.T, opts *options) {
				if opts.serviceVersion != "1.2.3" {
					t.Errorf("expected service version 1.2.3, got %s", opts.serviceVersion)
				}
				if !opts.serviceVersionSet {
					t.Errorf("expected serviceVersionSet to be true")
				}
			},
		},
		{
			name: "WithResourceAttributes",
			option: WithResourceAttributes(
				attribute.String("key", "value"),
			),
			validate: func(t *testing.T, opts *options) {
				if len(opts.resourceAttributes) != 1 {
					t.Fatalf("expected 1 resource attribute, got %d", len(opts.resourceAttributes))
				}
				if opts.resourceAttributes[0].Key != "key" || opts.resourceAttributes[0].Value.AsString() != "value" {
					t.Fatalf("unexpected resource attribute: %v", opts.resourceAttributes[0])
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

func TestBuildResource_WithResourceAttributesAndEnv(t *testing.T) {
	origMetric := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	origGeneric := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	origService := os.Getenv("OTEL_SERVICE_NAME")
	origAttrs := os.Getenv("OTEL_RESOURCE_ATTRIBUTES")
	defer func() {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", origMetric)
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", origGeneric)
		_ = os.Setenv("OTEL_SERVICE_NAME", origService)
		_ = os.Setenv("OTEL_RESOURCE_ATTRIBUTES", origAttrs)
	}()

	_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	_ = os.Setenv("OTEL_SERVICE_NAME", "env-metric-service")
	_ = os.Setenv("OTEL_RESOURCE_ATTRIBUTES", "team=ops,region=us-east")

	ctx := context.Background()
	opts := &options{
		serviceName:      itelemetry.ServiceName,
		serviceVersion:   itelemetry.ServiceVersion,
		serviceNamespace: itelemetry.ServiceNamespace,
	}
	WithServiceName("metric-option-service")(opts)
	WithResourceAttributes(
		attribute.String("team", "ml"),
		attribute.String("priority", "high"),
	)(opts)

	res, err := buildResource(ctx, opts)
	if err != nil {
		t.Fatalf("buildResource returned error: %v", err)
	}

	attrMap := make(map[string]string)
	iter := res.Iter()
	for iter.Next() {
		kv := iter.Attribute()
		if kv.Value.Type() == attribute.STRING {
			attrMap[string(kv.Key)] = kv.Value.AsString()
		}
	}

	if attrMap[string(semconv.ServiceNameKey)] != "metric-option-service" {
		t.Fatalf("expected service.name metric-option-service, got %q", attrMap[string(semconv.ServiceNameKey)])
	}
	if attrMap["region"] != "us-east" {
		t.Fatalf("expected region=us-east, got %q", attrMap["region"])
	}
	if attrMap["team"] != "ml" {
		t.Fatalf("expected team=ml, got %q", attrMap["team"])
	}
	if attrMap["priority"] != "high" {
		t.Fatalf("expected priority=high, got %q", attrMap["priority"])
	}
}
