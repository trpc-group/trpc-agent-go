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
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

// mockModel implements model.Model interface for testing.
type mockModel struct {
	name string
}

func (m *mockModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *mockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	return nil, nil
}

func TestChatAttributes_toAttributes(t *testing.T) {
	tests := []struct {
		name     string
		attrs    chatAttributes
		expected []attribute.KeyValue
	}{
		{
			name: "all fields populated",
			attrs: chatAttributes{
				RequestModelName:  "gpt-4",
				ResponseModelName: "gpt-4-0613",
				Stream:            true,
				AgentName:         "test-agent",
				AppName:           "test-app",
				UserID:            "user-123",
				SessionID:         "session-456",
				ErrorType:         "rate_limit",
				Error:             errors.New("test error"),
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationChat),
				attribute.String(KeyGenAISystem, "gpt-4"),
				attribute.String(KeyGenAIRequestModel, "gpt-4"),
				attribute.Bool(metrics.KeyTRPCAgentGoStream, true),
				attribute.String(KeyGenAIResponseModel, "gpt-4-0613"),
				attribute.String(KeyTRPCAgentGoAppName, "test-app"),
				attribute.String(KeyTRPCAgentGoUserID, "user-123"),
				attribute.String(KeyGenAIConversationID, "session-456"),
				attribute.String(KeyErrorType, "rate_limit"),
				attribute.String(KeyGenAIAgentName, "test-agent"),
			},
		},
		{
			name: "minimal fields",
			attrs: chatAttributes{
				RequestModelName: "gpt-3.5",
				Stream:           false,
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationChat),
				attribute.String(KeyGenAISystem, "gpt-3.5"),
				attribute.String(KeyGenAIRequestModel, "gpt-3.5"),
				attribute.Bool(metrics.KeyTRPCAgentGoStream, false),
			},
		},
		{
			name: "error without error type",
			attrs: chatAttributes{
				RequestModelName: "gpt-4",
				Error:            errors.New("some error"),
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationChat),
				attribute.String(KeyGenAISystem, "gpt-4"),
				attribute.String(KeyGenAIRequestModel, "gpt-4"),
				attribute.Bool(metrics.KeyTRPCAgentGoStream, false),
				attribute.String(KeyErrorType, ValueDefaultErrorType),
			},
		},
		{
			name: "empty optional fields",
			attrs: chatAttributes{
				RequestModelName:  "claude-3",
				ResponseModelName: "",
				AppName:           "",
				UserID:            "",
				SessionID:         "",
				ErrorType:         "",
				AgentName:         "",
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationChat),
				attribute.String(KeyGenAISystem, "claude-3"),
				attribute.String(KeyGenAIRequestModel, "claude-3"),
				attribute.Bool(metrics.KeyTRPCAgentGoStream, false),
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

func TestNewChatMetricsTracker(t *testing.T) {
	ctx := context.Background()
	invocation := &agent.Invocation{
		AgentName: "test-agent",
	}
	llmRequest := &model.Request{
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}
	var err error
	timingInfo := &model.TimingInfo{}

	tracker := NewChatMetricsTracker(ctx, invocation, llmRequest, timingInfo, &err)

	if tracker == nil {
		t.Fatal("expected non-nil tracker")
	}
	if tracker.ctx != ctx {
		t.Error("context not set correctly")
	}
	if !tracker.isFirstToken {
		t.Error("isFirstToken should be true initially")
	}
	if tracker.invocation != invocation {
		t.Error("invocation not set correctly")
	}
	if tracker.llmRequest != llmRequest {
		t.Error("llmRequest not set correctly")
	}
	if tracker.err != &err {
		t.Error("err pointer not set correctly")
	}
	if tracker.start.IsZero() {
		t.Error("start time should be set")
	}
}

func TestChatMetricsTracker_TrackResponse(t *testing.T) {
	ctx := context.Background()
	timingInfo := &model.TimingInfo{}
	tracker := NewChatMetricsTracker(ctx, nil, nil, timingInfo, nil)

	// First response
	response1 := &model.Response{
		Usage: &model.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
		},
	}

	// Wait a bit to ensure time difference
	time.Sleep(10 * time.Millisecond)
	tracker.TrackResponse(response1)

	if tracker.isFirstToken {
		t.Error("isFirstToken should be false after first response")
	}
	if tracker.firstTokenTimeDuration == 0 {
		t.Error("firstTokenTimeDuration should be set")
	}
	if tracker.firstCompleteToken != 5 {
		t.Errorf("expected firstCompleteToken=5, got %d", tracker.firstCompleteToken)
	}
	if tracker.totalPromptTokens != 10 {
		t.Errorf("expected totalPromptTokens=10, got %d", tracker.totalPromptTokens)
	}
	if tracker.totalCompletionTokens != 5 {
		t.Errorf("expected totalCompletionTokens=5, got %d", tracker.totalCompletionTokens)
	}

	// Second response
	response2 := &model.Response{
		Usage: &model.Usage{
			PromptTokens:     0,
			CompletionTokens: 3,
		},
	}
	firstTokenDuration := tracker.firstTokenTimeDuration
	tracker.TrackResponse(response2)

	if tracker.firstTokenTimeDuration != firstTokenDuration {
		t.Error("firstTokenTimeDuration should not change after first response")
	}
	if tracker.totalPromptTokens != 0 {
		t.Errorf("expected totalPromptTokens=0, got %d", tracker.totalPromptTokens)
	}
	if tracker.totalCompletionTokens != 3 {
		t.Errorf("expected totalCompletionTokens=3, got %d", tracker.totalCompletionTokens)
	}
}

func TestChatMetricsTracker_TrackResponse_NilUsage(t *testing.T) {
	ctx := context.Background()
	timingInfo := &model.TimingInfo{}
	tracker := NewChatMetricsTracker(ctx, nil, nil, timingInfo, nil)

	response := &model.Response{
		Usage: nil,
	}

	tracker.TrackResponse(response)

	if tracker.isFirstToken {
		t.Error("isFirstToken should be false after first response")
	}
	if tracker.firstCompleteToken != 0 {
		t.Errorf("expected firstCompleteToken=0, got %d", tracker.firstCompleteToken)
	}
	if tracker.totalPromptTokens != 0 {
		t.Errorf("expected totalPromptTokens=0, got %d", tracker.totalPromptTokens)
	}
	if tracker.totalCompletionTokens != 0 {
		t.Errorf("expected totalCompletionTokens=0, got %d", tracker.totalCompletionTokens)
	}
}

func TestChatMetricsTracker_SetLastEvent(t *testing.T) {
	ctx := context.Background()
	timingInfo := &model.TimingInfo{}
	tracker := NewChatMetricsTracker(ctx, nil, nil, timingInfo, nil)

	evt := &event.Event{
		Response: &model.Response{
			Model: "gpt-4-0613",
		},
	}

	tracker.SetLastEvent(evt)

	if tracker.lastEvent != evt {
		t.Error("lastEvent not set correctly")
	}
}

func TestChatMetricsTracker_FirstTokenTimeDuration(t *testing.T) {
	ctx := context.Background()
	timingInfo := &model.TimingInfo{}
	tracker := NewChatMetricsTracker(ctx, nil, nil, timingInfo, nil)

	if tracker.FirstTokenTimeDuration() != 0 {
		t.Error("initial FirstTokenTimeDuration should be 0")
	}

	time.Sleep(10 * time.Millisecond)
	tracker.TrackResponse(&model.Response{})

	if tracker.FirstTokenTimeDuration() == 0 {
		t.Error("FirstTokenTimeDuration should be non-zero after tracking response")
	}
}

func TestChatMetricsTracker_buildAttributes(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func() *ChatMetricsTracker
		checkFunc func(*testing.T, chatAttributes)
	}{
		{
			name: "with error",
			setupFunc: func() *ChatMetricsTracker {
				testErr := errors.New("test error")
				timingInfo := &model.TimingInfo{}
				return NewChatMetricsTracker(context.Background(), nil, nil, timingInfo, &testErr)
			},
			checkFunc: func(t *testing.T, attrs chatAttributes) {
				if attrs.Error == nil {
					t.Error("expected error to be set")
				}
			},
		},
		{
			name: "with llm request",
			setupFunc: func() *ChatMetricsTracker {
				req := &model.Request{
					GenerationConfig: model.GenerationConfig{
						Stream: true,
					},
				}
				timingInfo := &model.TimingInfo{}
				return NewChatMetricsTracker(context.Background(), nil, req, timingInfo, nil)
			},
			checkFunc: func(t *testing.T, attrs chatAttributes) {
				if !attrs.Stream {
					t.Error("expected Stream to be true")
				}
			},
		},
		{
			name: "with invocation",
			setupFunc: func() *ChatMetricsTracker {
				inv := &agent.Invocation{
					AgentName: "test-agent",
					Model:     &mockModel{name: "gpt-4"},
					Session: &session.Session{
						ID:      "session-123",
						UserID:  "user-456",
						AppName: "test-app",
					},
				}
				timingInfo := &model.TimingInfo{}
				return NewChatMetricsTracker(context.Background(), inv, nil, timingInfo, nil)
			},
			checkFunc: func(t *testing.T, attrs chatAttributes) {
				if attrs.AgentName != "test-agent" {
					t.Errorf("expected AgentName=test-agent, got %s", attrs.AgentName)
				}
				if attrs.RequestModelName != "gpt-4" {
					t.Errorf("expected RequestModelName=gpt-4, got %s", attrs.RequestModelName)
				}
				if attrs.SessionID != "session-123" {
					t.Errorf("expected SessionID=session-123, got %s", attrs.SessionID)
				}
				if attrs.UserID != "user-456" {
					t.Errorf("expected UserID=user-456, got %s", attrs.UserID)
				}
				if attrs.AppName != "test-app" {
					t.Errorf("expected AppName=test-app, got %s", attrs.AppName)
				}
			},
		},
		{
			name: "with last event - response model",
			setupFunc: func() *ChatMetricsTracker {
				timingInfo := &model.TimingInfo{}
				tracker := NewChatMetricsTracker(context.Background(), nil, nil, timingInfo, nil)
				evt := event.New("inv-123", "test-author")
				evt.Model = "gpt-4-0613"
				tracker.SetLastEvent(evt)
				return tracker
			},
			checkFunc: func(t *testing.T, attrs chatAttributes) {
				if attrs.ResponseModelName != "gpt-4-0613" {
					t.Errorf("expected ResponseModelName=gpt-4-0613, got %s", attrs.ResponseModelName)
				}
			},
		},
		{
			name: "with last event - error type",
			setupFunc: func() *ChatMetricsTracker {
				timingInfo := &model.TimingInfo{}
				tracker := NewChatMetricsTracker(context.Background(), nil, nil, timingInfo, nil)
				evt := event.NewErrorEvent("inv-123", "test-author", "rate_limit", "rate limit exceeded")
				tracker.SetLastEvent(evt)
				return tracker
			},
			checkFunc: func(t *testing.T, attrs chatAttributes) {
				if attrs.ErrorType != "rate_limit" {
					t.Errorf("expected ErrorType=rate_limit, got %s", attrs.ErrorType)
				}
			},
		},
		{
			name: "nil invocation",
			setupFunc: func() *ChatMetricsTracker {
				timingInfo := &model.TimingInfo{}
				return NewChatMetricsTracker(context.Background(), nil, nil, timingInfo, nil)
			},
			checkFunc: func(t *testing.T, attrs chatAttributes) {
				if attrs.AgentName != "" {
					t.Error("expected empty AgentName")
				}
				if attrs.RequestModelName != "" {
					t.Error("expected empty RequestModelName")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := tt.setupFunc()
			attrs := tracker.buildAttributes()
			tt.checkFunc(t, attrs)
		})
	}
}

func TestChatMetricsTracker_RecordMetrics(t *testing.T) {
	// Setup metric provider
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	// Save original and restore after test
	originalProvider := MeterProvider
	defer func() {
		MeterProvider = originalProvider
		ChatMeter = MeterProvider.Meter(metrics.MeterNameChat)
	}()

	MeterProvider = provider
	ChatMeter = provider.Meter(metrics.MeterNameChat)

	// Create metrics
	var err error
	ChatMetricTRPCAgentGoClientRequestCnt, err = ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	if err != nil {
		t.Fatalf("failed to create counter: %v", err)
	}
	ChatMetricGenAIClientOperationDuration, err = ChatMeter.Float64Histogram("gen_ai.client.operation.duration")
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	ChatMetricGenAIServerTimeToFirstToken, err = ChatMeter.Float64Histogram("gen_ai.server.time_to_first_token")
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	ChatMetricTRPCAgentGoClientTimeToFirstToken, err = ChatMeter.Float64Histogram("trpc_agent_go.client.time_to_first_token")
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	ChatMetricGenAIClientTokenUsage, err = ChatMeter.Int64Histogram("gen_ai.client.token.usage")
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	ChatMetricTRPCAgentGoClientTimePerOutputToken, err = ChatMeter.Float64Histogram("trpc_agent_go.client.time_per_output_token")
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}
	ChatMetricTRPCAgentGoClientOutputTokenPerTime, err = ChatMeter.Float64Histogram("trpc_agent_go.client.output_token_per_time")
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}

	ctx := context.Background()
	inv := &agent.Invocation{
		AgentName: "test-agent",
		Model:     &mockModel{name: "gpt-4"},
	}
	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	timingInfo := &model.TimingInfo{}
	tracker := NewChatMetricsTracker(ctx, inv, req, timingInfo, nil)

	// Simulate some responses
	time.Sleep(10 * time.Millisecond)
	tracker.TrackResponse(&model.Response{
		Usage: &model.Usage{
			PromptTokens:     10,
			CompletionTokens: 2,
		},
	})

	time.Sleep(10 * time.Millisecond)
	tracker.TrackResponse(&model.Response{
		Usage: &model.Usage{
			CompletionTokens: 3,
		},
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

func TestChatMetricsTracker_recordDerivedMetrics(t *testing.T) {
	tests := []struct {
		name                  string
		firstCompleteToken    int
		totalCompletionTokens int
		firstTokenDuration    time.Duration
		requestDuration       time.Duration
		expectMetrics         bool
	}{
		{
			name:                  "normal case with tokens",
			firstCompleteToken:    2,
			totalCompletionTokens: 10,
			firstTokenDuration:    100 * time.Millisecond,
			requestDuration:       500 * time.Millisecond,
			expectMetrics:         true,
		},
		{
			name:                  "zero tokens after first",
			firstCompleteToken:    5,
			totalCompletionTokens: 5,
			firstTokenDuration:    100 * time.Millisecond,
			requestDuration:       500 * time.Millisecond,
			expectMetrics:         true, // fallback case applies: tokens==0 but totalCompletionTokens>0
		},
		{
			name:                  "fallback case",
			firstCompleteToken:    0,
			totalCompletionTokens: 10,
			firstTokenDuration:    0,
			requestDuration:       500 * time.Millisecond,
			expectMetrics:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup metric provider
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

			originalProvider := MeterProvider
			defer func() {
				MeterProvider = originalProvider
				ChatMeter = MeterProvider.Meter(metrics.MeterNameChat)
			}()

			MeterProvider = provider
			ChatMeter = provider.Meter(metrics.MeterNameChat)

			var err error
			ChatMetricTRPCAgentGoClientTimePerOutputToken, err = ChatMeter.Float64Histogram("time_per_token")
			if err != nil {
				t.Fatalf("failed to create histogram: %v", err)
			}
			ChatMetricTRPCAgentGoClientOutputTokenPerTime, err = ChatMeter.Float64Histogram("token_per_time")
			if err != nil {
				t.Fatalf("failed to create histogram: %v", err)
			}

			ctx := context.Background()
			timingInfo := &model.TimingInfo{}
			tracker := NewChatMetricsTracker(ctx, nil, nil, timingInfo, nil)
			tracker.firstCompleteToken = tt.firstCompleteToken
			tracker.totalCompletionTokens = tt.totalCompletionTokens
			tracker.firstTokenTimeDuration = tt.firstTokenDuration

			otelAttrs := []attribute.KeyValue{
				attribute.String("test", "value"),
			}

			tracker.recordDerivedMetrics(otelAttrs, tt.requestDuration)

			// Verify metrics were recorded (or not)
			var rm metricdata.ResourceMetrics
			if err := reader.Collect(ctx, &rm); err != nil {
				t.Fatalf("failed to collect metrics: %v", err)
			}

			hasMetrics := len(rm.ScopeMetrics) > 0
			if hasMetrics != tt.expectMetrics {
				t.Errorf("expected metrics=%v, got metrics=%v", tt.expectMetrics, hasMetrics)
			}
		})
	}
}

func TestExecuteToolAttributes_toAttributes(t *testing.T) {
	tests := []struct {
		name     string
		attrs    ExecuteToolAttributes
		expected []attribute.KeyValue
	}{
		{
			name: "all fields populated",
			attrs: ExecuteToolAttributes{
				RequestModelName: "gpt-4",
				ToolName:         "calculator",
				AppName:          "test-app",
				AgentName:        "test-agent",
				UserID:           "user-123",
				SessionID:        "session-456",
				ErrorType:        "timeout",
				Error:            errors.New("test error"),
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationExecuteTool),
				attribute.String(KeyGenAISystem, "gpt-4"),
				attribute.String(KeyGenAIToolName, "calculator"),
				attribute.String(KeyTRPCAgentGoAppName, "test-app"),
				attribute.String(KeyTRPCAgentGoUserID, "user-123"),
				attribute.String(KeyGenAIConversationID, "session-456"),
				attribute.String(KeyGenAIAgentName, "test-agent"),
				attribute.String(KeyErrorType, "timeout"),
			},
		},
		{
			name: "minimal fields",
			attrs: ExecuteToolAttributes{
				RequestModelName: "gpt-3.5",
				ToolName:         "search",
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationExecuteTool),
				attribute.String(KeyGenAISystem, "gpt-3.5"),
				attribute.String(KeyGenAIToolName, "search"),
			},
		},
		{
			name: "error without error type",
			attrs: ExecuteToolAttributes{
				RequestModelName: "gpt-4",
				ToolName:         "tool1",
				Error:            errors.New("some error"),
			},
			expected: []attribute.KeyValue{
				attribute.String(KeyGenAIOperationName, OperationExecuteTool),
				attribute.String(KeyGenAISystem, "gpt-4"),
				attribute.String(KeyGenAIToolName, "tool1"),
				attribute.String(KeyErrorType, ValueDefaultErrorType),
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

func TestReportExecuteToolMetrics(t *testing.T) {
	// Setup metric provider
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	originalProvider := MeterProvider
	defer func() {
		MeterProvider = originalProvider
		ExecuteToolMeter = MeterProvider.Meter(metrics.MeterNameExecuteTool)
	}()

	MeterProvider = provider
	ExecuteToolMeter = provider.Meter(metrics.MeterNameExecuteTool)

	var err error
	ExecuteToolMetricTRPCAgentGoClientRequestCnt, err = ExecuteToolMeter.Int64Counter("execute_tool.request.cnt")
	if err != nil {
		t.Fatalf("failed to create counter: %v", err)
	}
	ExecuteToolMetricGenAIClientOperationDuration, err = ExecuteToolMeter.Float64Histogram("execute_tool.duration")
	if err != nil {
		t.Fatalf("failed to create histogram: %v", err)
	}

	ctx := context.Background()
	attrs := ExecuteToolAttributes{
		RequestModelName: "gpt-4",
		ToolName:         "calculator",
		AppName:          "test-app",
	}
	duration := 100 * time.Millisecond

	ReportExecuteToolMetrics(ctx, attrs, duration)

	// Collect metrics
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	if len(rm.ScopeMetrics) == 0 {
		t.Error("expected metrics to be recorded")
	}
}
