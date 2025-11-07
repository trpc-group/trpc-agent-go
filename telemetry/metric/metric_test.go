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
	"errors"
	"os"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
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

// TestStartAndClean exercises various Start configurations.
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
			mp, err := NewMeterProvider(ctx, tt.opts...)
			if err != nil {
				t.Fatalf("NewMeterProvider returned error: %v", err)
			}

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}

			// Verify meter provider was created successfully
			if mp == nil {
				t.Fatal("expected non-nil meter provider")
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
			},
		},
		{
			name:   "WithServiceNamespace",
			option: WithServiceNamespace("custom-ns"),
			validate: func(t *testing.T, opts *options) {
				if opts.serviceNamespace != "custom-ns" {
					t.Errorf("expected service namespace custom-ns, got %s", opts.serviceNamespace)
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
			},
		},
		{
			name: "WithResourceAttributes",
			option: WithResourceAttributes(
				attribute.String("key", "value"),
			),
			validate: func(t *testing.T, opts *options) {
				if opts.resourceAttributes == nil || len(*opts.resourceAttributes) != 1 {
					t.Fatalf("expected 1 resource attribute, got %v", opts.resourceAttributes)
				}
				if (*opts.resourceAttributes)[0].Key != "key" || (*opts.resourceAttributes)[0].Value.AsString() != "value" {
					t.Fatalf("unexpected resource attribute: %v", (*opts.resourceAttributes)[0])
				}
			},
		},
		{
			name:   "WithResourceAttributes empty",
			option: WithResourceAttributes(),
			validate: func(t *testing.T, opts *options) {
				if opts.resourceAttributes != nil && len(*opts.resourceAttributes) != 0 {
					t.Fatalf("expected no resource attributes, got %d", len(*opts.resourceAttributes))
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
	opts := &options{}
	WithServiceName("metric-option-service")(opts)
	WithServiceNamespace("metric-ns")(opts)
	WithServiceVersion("9.9.9")(opts)
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

	// Per OpenTelemetry spec: environment variables take precedence over code configuration
	// OTEL_SERVICE_NAME=env-metric-service should override WithServiceName("metric-option-service")
	if attrMap[string(semconv.ServiceNameKey)] != "env-metric-service" {
		t.Fatalf("service.name should be from env, got %q", attrMap[string(semconv.ServiceNameKey)])
	}
	// OTEL_RESOURCE_ATTRIBUTES env attributes present
	if attrMap["region"] != "us-east" {
		t.Fatalf("expected region=us-east from OTEL_RESOURCE_ATTRIBUTES, got %q", attrMap["region"])
	}
	// WithResourceAttributes should override OTEL_RESOURCE_ATTRIBUTES for same keys
	if attrMap["team"] != "ml" {
		t.Fatalf("expected team=ml from WithResourceAttributes, got %q", attrMap["team"])
	}
	// Custom attribute from WithResourceAttributes
	if attrMap["priority"] != "high" {
		t.Fatalf("expected priority=high from WithResourceAttributes, got %q", attrMap["priority"])
	}
	// service.namespace and service.version from code (no env override, so code value used)
	if attrMap[string(semconv.ServiceNamespaceKey)] != "metric-ns" {
		t.Fatalf("expected service.namespace metric-ns, got %q", attrMap[string(semconv.ServiceNamespaceKey)])
	}
	if attrMap[string(semconv.ServiceVersionKey)] != "9.9.9" {
		t.Fatalf("expected service.version 9.9.9, got %q", attrMap[string(semconv.ServiceVersionKey)])
	}
}

// TestInitMeterProvider tests the InitMeterProvider function
func TestInitMeterProvider(t *testing.T) {
	ctx := context.Background()

	// Save original meter provider
	originalMP := itelemetry.MeterProvider
	defer func() {
		itelemetry.MeterProvider = originalMP
	}()

	// Create a test meter provider
	mp, err := NewMeterProvider(ctx)
	if err != nil {
		t.Fatalf("failed to create meter provider: %v", err)
	}

	// Test InitMeterProvider
	err = InitMeterProvider(mp)
	if err != nil {
		t.Fatalf("InitMeterProvider failed: %v", err)
	}

	// Verify that the meter provider was set
	if itelemetry.MeterProvider != mp {
		t.Error("MeterProvider was not set correctly")
	}

	// Verify that chat metrics were created
	if itelemetry.ChatMeter == nil {
		t.Error("ChatMeter was not created")
	}
	if itelemetry.ChatMetricTRPCAgentGoClientRequestCnt == nil {
		t.Error("ChatMetricTRPCAgentGoClientRequestCnt was not created")
	}
	if itelemetry.ChatMetricGenAIClientTokenUsage == nil {
		t.Error("ChatMetricGenAIClientTokenUsage was not created")
	}
	if itelemetry.ChatMetricGenAIClientOperationDuration == nil {
		t.Error("ChatMetricGenAIClientOperationDuration was not created")
	}
	if itelemetry.ChatMetricGenAIServerTimeToFirstToken == nil {
		t.Error("ChatMetricGenAIServerTimeToFirstToken was not created")
	}
	if itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken == nil {
		t.Error("ChatMetricTRPCAgentGoClientTimeToFirstToken was not created")
	}
	if itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken == nil {
		t.Error("ChatMetricTRPCAgentGoClientTimePerOutputToken was not created")
	}
	if itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime == nil {
		t.Error("ChatMetricTRPCAgentGoClientOutputTokenPerTime was not created")
	}

	// Verify that execute tool metrics were created
	if itelemetry.ExecuteToolMeter == nil {
		t.Error("ExecuteToolMeter was not created")
	}
	if itelemetry.ExecuteToolMetricTRPCAgentGoClientRequestCnt == nil {
		t.Error("ExecuteToolMetricTRPCAgentGoClientRequestCnt was not created")
	}
	if itelemetry.ExecuteToolMetricGenAIClientOperationDuration == nil {
		t.Error("ExecuteToolMetricGenAIClientOperationDuration was not created")
	}
}

// TestGetMeterProvider tests the GetMeterProvider function
func TestGetMeterProvider(t *testing.T) {
	ctx := context.Background()

	// Save original meter provider
	originalMP := itelemetry.MeterProvider
	defer func() {
		itelemetry.MeterProvider = originalMP
	}()

	// Create and set a test meter provider
	mp, err := NewMeterProvider(ctx)
	if err != nil {
		t.Fatalf("failed to create meter provider: %v", err)
	}

	err = InitMeterProvider(mp)
	if err != nil {
		t.Fatalf("InitMeterProvider failed: %v", err)
	}

	// Test GetMeterProvider
	retrievedMP := GetMeterProvider()
	if retrievedMP != mp {
		t.Error("GetMeterProvider did not return the correct meter provider")
	}
}

// TestNewMeterProviderWithEnvironmentVariables tests NewMeterProvider with environment variables
func TestNewMeterProviderWithEnvironmentVariables(t *testing.T) {
	origMetric := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	origGeneric := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", origMetric)
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", origGeneric)
	}()

	tests := []struct {
		name            string
		metricsEndpoint string
		genericEndpoint string
		opts            []Option
	}{
		{
			name:            "with OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
			metricsEndpoint: "metrics-endpoint:4317",
			genericEndpoint: "",
			opts:            []Option{},
		},
		{
			name:            "with OTEL_EXPORTER_OTLP_ENDPOINT",
			metricsEndpoint: "",
			genericEndpoint: "generic-endpoint:4317",
			opts:            []Option{},
		},
		{
			name:            "with both env vars set",
			metricsEndpoint: "metrics-endpoint:4317",
			genericEndpoint: "generic-endpoint:4317",
			opts:            []Option{},
		},
		{
			name:            "option overrides env vars",
			metricsEndpoint: "metrics-endpoint:4317",
			genericEndpoint: "generic-endpoint:4317",
			opts:            []Option{WithEndpoint("custom:4317")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", tt.metricsEndpoint)
			_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tt.genericEndpoint)

			ctx := context.Background()
			mp, err := NewMeterProvider(ctx, tt.opts...)
			if err != nil {
				t.Fatalf("NewMeterProvider failed: %v", err)
			}

			// Verify meter provider was created successfully
			if mp == nil {
				t.Fatal("expected non-nil meter provider")
			}
		})
	}
}

// TestNewHTTPMeterProvider tests HTTP meter provider creation
func TestNewHTTPMeterProvider(t *testing.T) {
	ctx := context.Background()

	mp, err := NewMeterProvider(ctx,
		WithProtocol("http"),
		WithEndpoint("localhost:4318"),
	)
	if err != nil {
		t.Fatalf("failed to create HTTP meter provider: %v", err)
	}

	if mp == nil {
		t.Fatal("expected non-nil meter provider")
	}
}

// TestNewGRPCMeterProvider tests gRPC meter provider creation
func TestNewGRPCMeterProvider(t *testing.T) {
	ctx := context.Background()

	mp, err := NewMeterProvider(ctx,
		WithProtocol("grpc"),
		WithEndpoint("localhost:4317"),
	)
	if err != nil {
		t.Fatalf("failed to create gRPC meter provider: %v", err)
	}

	if mp == nil {
		t.Fatal("expected non-nil meter provider")
	}
}

// TestMetricsEndpointProtocols tests metricsEndpoint with different protocols
func TestMetricsEndpointProtocols(t *testing.T) {
	origMetric := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	origGeneric := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", origMetric)
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", origGeneric)
	}()

	// Clear environment variables
	_ = os.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	tests := []struct {
		name     string
		protocol string
		expected string
	}{
		{
			name:     "grpc protocol",
			protocol: "grpc",
			expected: "localhost:4317",
		},
		{
			name:     "http protocol",
			protocol: "http",
			expected: "localhost:4318",
		},
		{
			name:     "unknown protocol defaults to grpc",
			protocol: "unknown",
			expected: "localhost:4317",
		},
		{
			name:     "empty protocol defaults to grpc",
			protocol: "",
			expected: "localhost:4317",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint := metricsEndpoint(tt.protocol)
			if endpoint != tt.expected {
				t.Errorf("expected endpoint %s, got %s", tt.expected, endpoint)
			}
		})
	}
}

// TestMultipleOptions tests applying multiple options
func TestMultipleOptions(t *testing.T) {
	ctx := context.Background()

	mp, err := NewMeterProvider(ctx,
		WithEndpoint("test-endpoint:4317"),
		WithProtocol("grpc"),
	)
	if err != nil {
		t.Fatalf("NewMeterProvider with multiple options failed: %v", err)
	}

	if mp == nil {
		t.Fatal("expected non-nil meter provider")
	}
}

// mockMeter is a mock implementation of metric.Meter that can return errors
type mockMeter struct {
	noop.Meter
	shouldFail bool
	failOn     string
}

func (m *mockMeter) Float64Histogram(name string, options ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	if m.shouldFail && name == m.failOn {
		return nil, errors.New("mock error: failed to create histogram")
	}
	return noop.Float64Histogram{}, nil
}

func (m *mockMeter) Int64Counter(name string, options ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if m.shouldFail && name == m.failOn {
		return nil, errors.New("mock error: failed to create counter")
	}
	return noop.Int64Counter{}, nil
}

func (m *mockMeter) Int64Histogram(name string, options ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	if m.shouldFail && name == m.failOn {
		return nil, errors.New("mock error: failed to create histogram")
	}
	return noop.Int64Histogram{}, nil
}

// mockMeterProvider is a mock implementation of metric.MeterProvider
type mockMeterProvider struct {
	noop.MeterProvider
	meter *mockMeter
}

func (m *mockMeterProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	return m.meter
}

// TestInitMeterProvider_ErrorHandling tests error handling in InitMeterProvider
func TestInitMeterProvider_ErrorHandling(t *testing.T) {
	tests := []struct {
		name        string
		failOn      string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "fail on ChatMetricGenAIServerTimeToFirstToken",
			failOn:      "gen_ai.server.time_to_first_token",
			expectError: true,
			errorMsg:    "failed to create chat metric GenAIServerTimeToFirstToken",
		},
		{
			name:        "fail on ChatMetricTRPCAgentGoClientTimeToFirstToken",
			failOn:      "trpc_agent_go.client.time_to_first_token",
			expectError: true,
			errorMsg:    "failed to create chat metric TRPCAgentGoClientTimeToFirstToken",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original values
			origMeterProvider := itelemetry.MeterProvider
			origChatMeter := itelemetry.ChatMeter
			defer func() {
				itelemetry.MeterProvider = origMeterProvider
				itelemetry.ChatMeter = origChatMeter
			}()

			// Create mock meter provider that will fail on specific metric
			mockMeter := &mockMeter{
				shouldFail: tt.expectError,
				failOn:     tt.failOn,
			}
			mp := &mockMeterProvider{meter: mockMeter}

			// Test initialization
			err := InitMeterProvider(mp)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error containing '%s', got nil", tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			}
		})
	}
}
