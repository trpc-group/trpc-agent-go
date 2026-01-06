//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

func TestInvokeAgentAttributes_toAttributes(t *testing.T) {
	tests := []struct {
		name     string
		attrs    invokeAgentAttributes
		expected []attribute.KeyValue
	}{
		{
			name: "all fields populated",
			attrs: invokeAgentAttributes{
				AgentName: "test-agent",
				AppName:   "test-app",
				UserID:    "user-123",
				System:    "gpt-4",
				Stream:    true,
				ErrorType: "rate_limit",
				Error:     errors.New("test error"),
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationInvokeAgent),
				attribute.Bool(metrics.KeyTRPCAgentGoStream, true),
				attribute.String(KeyGenAISystem, "gpt-4"),
				attribute.String(KeyTRPCAgentGoAppName, "test-app"),
				attribute.String(KeyTRPCAgentGoUserID, "user-123"),
				attribute.String(KeyGenAIAgentName, "test-agent"),
				attribute.String(KeyErrorType, "rate_limit"),
			},
		},
		{
			name: "minimal fields",
			attrs: invokeAgentAttributes{
				System: "gpt-3.5",
				Stream: false,
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationInvokeAgent),
				attribute.Bool(metrics.KeyTRPCAgentGoStream, false),
				attribute.String(KeyGenAISystem, "gpt-3.5"),
			},
		},
		{
			name: "error without error type",
			attrs: invokeAgentAttributes{
				System: "gpt-4",
				Error:  errors.New("some error"),
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationInvokeAgent),
				attribute.Bool(metrics.KeyTRPCAgentGoStream, false),
				attribute.String(KeyGenAISystem, "gpt-4"),
				attribute.String(KeyErrorType, ValueDefaultErrorType),
			},
		},
		{
			name: "empty optional fields",
			attrs: invokeAgentAttributes{
				System:    "claude-3",
				AppName:   "",
				UserID:    "",
				AgentName: "",
				ErrorType: "",
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationInvokeAgent),
				attribute.Bool(metrics.KeyTRPCAgentGoStream, false),
				attribute.String(KeyGenAISystem, "claude-3"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.attrs.toAttributes()
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d attributes, got %d", len(tt.expected), len(result))
				return
			}
			for i, attr := range result {
				if attr != tt.expected[i] {
					t.Errorf("attribute %d: expected %v, got %v", i, tt.expected[i], attr)
				}
			}
		})
	}
}

func TestNewInvokeAgentTracker(t *testing.T) {
	ctx := context.Background()
	invocation := &agent.Invocation{
		AgentName: "test-agent",
		Model:     &mockModel{name: "gpt-4"},
		Session: &session.Session{
			ID:      "session-123",
			UserID:  "user-456",
			AppName: "test-app",
		},
	}
	var err error

	tracker := NewInvokeAgentTracker(ctx, invocation, true, &err)

	if tracker == nil {
		t.Fatal("expected non-nil tracker")
	}
	if tracker.ctx != ctx {
		t.Error("context not set correctly")
	}
	if !tracker.isFirstToken {
		t.Error("isFirstToken should be true initially")
	}
	if tracker.attributes.AgentName != "test-agent" {
		t.Errorf("expected AgentName=test-agent, got %s", tracker.attributes.AgentName)
	}
	if tracker.attributes.System != "gpt-4" {
		t.Errorf("expected System=gpt-4, got %s", tracker.attributes.System)
	}
	if tracker.attributes.UserID != "user-456" {
		t.Errorf("expected UserID=user-456, got %s", tracker.attributes.UserID)
	}
	if tracker.attributes.AppName != "test-app" {
		t.Errorf("expected AppName=test-app, got %s", tracker.attributes.AppName)
	}
	if !tracker.attributes.Stream {
		t.Error("expected Stream to be true")
	}
	if tracker.start.IsZero() {
		t.Error("start time should be set")
	}
	if tracker.firstTokenTimeDuration != 0 {
		t.Error("firstTokenTimeDuration should be 0 initially")
	}
	if tracker.totalPromptTokens != 0 {
		t.Error("totalPromptTokens should be 0 initially")
	}
	if tracker.totalCompletionTokens != 0 {
		t.Error("totalCompletionTokens should be 0 initially")
	}
}

func TestNewInvokeAgentTracker_NilInvocation(t *testing.T) {
	ctx := context.Background()
	var err error

	tracker := NewInvokeAgentTracker(ctx, nil, false, &err)

	if tracker == nil {
		t.Fatal("expected non-nil tracker")
	}
	if tracker.attributes.AgentName != "" {
		t.Error("expected empty AgentName")
	}
	if tracker.attributes.System != "" {
		t.Error("expected empty System")
	}
	if tracker.attributes.Stream {
		t.Error("expected Stream to be false")
	}
}

func TestInvokeAgentTracker_TrackResponse(t *testing.T) {
	tests := []struct {
		name                    string
		responses               []*model.Response
		waitBeforeFirstResponse bool
		checkFunc               func(*testing.T, *InvokeAgentTracker)
	}{
		{
			name: "normal response with valid content",
			responses: []*model.Response{
				{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Content: "Hello",
							},
						},
					},
					Usage: &model.Usage{
						PromptTokens:     10,
						CompletionTokens: 5,
					},
					IsPartial: false,
				},
			},
			waitBeforeFirstResponse: true,
			checkFunc: func(t *testing.T, tracker *InvokeAgentTracker) {
				if tracker.isFirstToken {
					t.Error("isFirstToken should be false after first response with valid content")
				}
				if tracker.firstTokenTimeDuration == 0 {
					t.Error("firstTokenTimeDuration should be set")
				}
				if tracker.totalPromptTokens != 10 {
					t.Errorf("expected totalPromptTokens=10, got %d", tracker.totalPromptTokens)
				}
				if tracker.totalCompletionTokens != 5 {
					t.Errorf("expected totalCompletionTokens=5, got %d", tracker.totalCompletionTokens)
				}
			},
		},
		{
			name: "multiple responses",
			responses: []*model.Response{
				{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Content: "Hello",
							},
						},
					},
					Usage: &model.Usage{
						PromptTokens:     10,
						CompletionTokens: 5,
					},
					IsPartial: false,
				},
				{
					Usage: &model.Usage{
						PromptTokens:     0,
						CompletionTokens: 3,
					},
					IsPartial: false,
				},
			},
			waitBeforeFirstResponse: true,
			checkFunc: func(t *testing.T, tracker *InvokeAgentTracker) {
				if tracker.isFirstToken {
					t.Error("isFirstToken should be false after first response with valid content")
				}
				if tracker.firstTokenTimeDuration == 0 {
					t.Error("firstTokenTimeDuration should be set")
				}
				// After tracking both responses, verify final state
				if tracker.totalPromptTokens != 10 {
					t.Errorf("expected totalPromptTokens=10, got %d", tracker.totalPromptTokens)
				}
				if tracker.totalCompletionTokens != 8 {
					t.Errorf("expected totalCompletionTokens=8, got %d", tracker.totalCompletionTokens)
				}
			},
		},
		{
			name:      "nil response",
			responses: []*model.Response{nil},
			checkFunc: func(t *testing.T, tracker *InvokeAgentTracker) {
				if !tracker.isFirstToken {
					t.Error("isFirstToken should remain true for nil response")
				}
				if tracker.totalPromptTokens != 0 {
					t.Errorf("expected totalPromptTokens=0, got %d", tracker.totalPromptTokens)
				}
				if tracker.totalCompletionTokens != 0 {
					t.Errorf("expected totalCompletionTokens=0, got %d", tracker.totalCompletionTokens)
				}
			},
		},
		{
			name: "response without valid content",
			responses: []*model.Response{
				{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Content: "",
							},
						},
					},
					Usage: &model.Usage{
						PromptTokens:     10,
						CompletionTokens: 5,
					},
					IsPartial: false,
				},
			},
			checkFunc: func(t *testing.T, tracker *InvokeAgentTracker) {
				if !tracker.isFirstToken {
					t.Error("isFirstToken should remain true for response without valid content")
				}
				if tracker.firstTokenTimeDuration != 0 {
					t.Error("firstTokenTimeDuration should remain 0")
				}
				// Token usage should still be tracked
				if tracker.totalPromptTokens != 10 {
					t.Errorf("expected totalPromptTokens=10, got %d", tracker.totalPromptTokens)
				}
				if tracker.totalCompletionTokens != 5 {
					t.Errorf("expected totalCompletionTokens=5, got %d", tracker.totalCompletionTokens)
				}
			},
		},
		{
			name: "partial response",
			responses: []*model.Response{
				{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Content: "Hello",
							},
						},
					},
					Usage: &model.Usage{
						PromptTokens:     10,
						CompletionTokens: 5,
					},
					IsPartial: true,
				},
			},
			checkFunc: func(t *testing.T, tracker *InvokeAgentTracker) {
				if tracker.isFirstToken {
					t.Error("isFirstToken should be false after first response with valid content")
				}
				// Token usage should not be tracked for partial responses
				if tracker.totalPromptTokens != 0 {
					t.Errorf("expected totalPromptTokens=0 for partial response, got %d", tracker.totalPromptTokens)
				}
				if tracker.totalCompletionTokens != 0 {
					t.Errorf("expected totalCompletionTokens=0 for partial response, got %d", tracker.totalCompletionTokens)
				}
			},
		},
		{
			name: "response with nil usage",
			responses: []*model.Response{
				{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Content: "Hello",
							},
						},
					},
					Usage:     nil,
					IsPartial: false,
				},
			},
			checkFunc: func(t *testing.T, tracker *InvokeAgentTracker) {
				if tracker.isFirstToken {
					t.Error("isFirstToken should be false after first response with valid content")
				}
				if tracker.totalPromptTokens != 0 {
					t.Errorf("expected totalPromptTokens=0, got %d", tracker.totalPromptTokens)
				}
				if tracker.totalCompletionTokens != 0 {
					t.Errorf("expected totalCompletionTokens=0, got %d", tracker.totalCompletionTokens)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			var err error
			tracker := NewInvokeAgentTracker(ctx, nil, false, &err)

			for i, response := range tt.responses {
				if i == 0 && tt.waitBeforeFirstResponse {
					time.Sleep(10 * time.Millisecond)
				}
				tracker.TrackResponse(response)
			}

			tt.checkFunc(t, tracker)
		})
	}
}

func TestInvokeAgentTracker_SetResponseErrorType(t *testing.T) {
	ctx := context.Background()
	var err error
	tracker := NewInvokeAgentTracker(ctx, nil, false, &err)

	if tracker.attributes.ErrorType != "" {
		t.Error("expected empty ErrorType initially")
	}

	tracker.SetResponseErrorType("rate_limit")
	if tracker.attributes.ErrorType != "rate_limit" {
		t.Errorf("expected ErrorType=rate_limit, got %s", tracker.attributes.ErrorType)
	}

	tracker.SetResponseErrorType("timeout")
	if tracker.attributes.ErrorType != "timeout" {
		t.Errorf("expected ErrorType=timeout, got %s", tracker.attributes.ErrorType)
	}
}

func TestInvokeAgentTracker_FirstTokenTimeDuration(t *testing.T) {
	ctx := context.Background()
	var err error
	tracker := NewInvokeAgentTracker(ctx, nil, false, &err)

	if tracker.FirstTokenTimeDuration() != 0 {
		t.Error("initial FirstTokenTimeDuration should be 0")
	}

	time.Sleep(10 * time.Millisecond)
	tracker.TrackResponse(&model.Response{
		Choices: []model.Choice{
			{
				Delta: model.Message{
					Content: "Hello",
				},
			},
		},
	})

	if tracker.FirstTokenTimeDuration() == 0 {
		t.Error("FirstTokenTimeDuration should be non-zero after tracking response")
	}
}

func TestInvokeAgentTracker_RecordMetrics(t *testing.T) {
	// Setup metric provider
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	// Save original and restore after test
	originalProvider := MeterProvider
	defer func() {
		MeterProvider = originalProvider
		InvokeAgentMeter = MeterProvider.Meter(metrics.MeterNameInvokeAgent)
	}()

	MeterProvider = provider
	InvokeAgentMeter = provider.Meter(metrics.MeterNameInvokeAgent)

	// Create metrics
	var err error
	InvokeAgentMetricGenAIRequestCnt, err = InvokeAgentMeter.Int64Counter("gen_ai.client.request.cnt")
	if err != nil {
		t.Fatalf("failed to create counter: %v", err)
	}
	InvokeAgentMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameInvokeAgent, "gen_ai.client.token.usage")
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	InvokeAgentMetricGenAIClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameInvokeAgent,
		metrics.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithDescription("Time to first token for client"), metric.WithUnit("s"))
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	InvokeAgentMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameInvokeAgent,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of client operation"), metric.WithUnit("s"))
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}

	ctx := context.Background()
	inv := &agent.Invocation{
		AgentName: "test-agent",
		Model:     &mockModel{name: "gpt-4"},
		Session: &session.Session{
			UserID:  "user-123",
			AppName: "test-app",
		},
	}

	tracker := NewInvokeAgentTracker(ctx, inv, true, &err)

	// Simulate some responses
	time.Sleep(10 * time.Millisecond)
	tracker.TrackResponse(&model.Response{
		Choices: []model.Choice{
			{
				Delta: model.Message{
					Content: "Hello",
				},
			},
		},
		Usage: &model.Usage{
			PromptTokens:     10,
			CompletionTokens: 2,
		},
		IsPartial: false,
	})

	time.Sleep(10 * time.Millisecond)
	tracker.TrackResponse(&model.Response{
		Usage: &model.Usage{
			CompletionTokens: 3,
		},
		IsPartial: false,
	})

	// Record metrics
	recordFunc := tracker.RecordMetrics()
	recordFunc()

	// Collect metrics
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	// Verify metrics were recorded
	if len(rm.ScopeMetrics) == 0 {
		t.Error("expected metrics to be recorded")
	}
}

func TestInvokeAgentTracker_RecordMetrics_WithError(t *testing.T) {
	// Setup metric provider
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	originalProvider := MeterProvider
	defer func() {
		MeterProvider = originalProvider
		InvokeAgentMeter = MeterProvider.Meter(metrics.MeterNameInvokeAgent)
	}()

	MeterProvider = provider
	InvokeAgentMeter = provider.Meter(metrics.MeterNameInvokeAgent)

	var err error
	InvokeAgentMetricGenAIRequestCnt, err = InvokeAgentMeter.Int64Counter("gen_ai.client.request.cnt")
	if err != nil {
		t.Fatalf("failed to create counter: %v", err)
	}
	InvokeAgentMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameInvokeAgent, "gen_ai.client.token.usage")
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	InvokeAgentMetricGenAIClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameInvokeAgent,
		metrics.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithDescription("Time to first token for client"), metric.WithUnit("s"))
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	InvokeAgentMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameInvokeAgent,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of client operation"), metric.WithUnit("s"))
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}

	ctx := context.Background()
	testErr := errors.New("test error")
	tracker := NewInvokeAgentTracker(ctx, nil, false, &testErr)
	tracker.SetResponseErrorType("rate_limit")

	recordFunc := tracker.RecordMetrics()
	recordFunc()

	// Collect metrics
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	if len(rm.ScopeMetrics) == 0 {
		t.Error("expected metrics to be recorded")
	}
}

func TestInvokeAgentTracker_RecordMetrics_NoTokens(t *testing.T) {
	// Setup metric provider
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	originalProvider := MeterProvider
	defer func() {
		MeterProvider = originalProvider
		InvokeAgentMeter = MeterProvider.Meter(metrics.MeterNameInvokeAgent)
	}()

	MeterProvider = provider
	InvokeAgentMeter = provider.Meter(metrics.MeterNameInvokeAgent)

	var err error
	InvokeAgentMetricGenAIRequestCnt, err = InvokeAgentMeter.Int64Counter("gen_ai.client.request.cnt")
	if err != nil {
		t.Fatalf("failed to create counter: %v", err)
	}
	InvokeAgentMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameInvokeAgent, "gen_ai.client.token.usage")
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	InvokeAgentMetricGenAIClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameInvokeAgent,
		metrics.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithDescription("Time to first token for client"), metric.WithUnit("s"))
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	InvokeAgentMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameInvokeAgent,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of client operation"), metric.WithUnit("s"))
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}

	ctx := context.Background()
	tracker := NewInvokeAgentTracker(ctx, nil, false, &err)

	// Record metrics without any responses
	recordFunc := tracker.RecordMetrics()
	recordFunc()

	// Collect metrics
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	if len(rm.ScopeMetrics) == 0 {
		t.Error("expected metrics to be recorded even without tokens")
	}
}
