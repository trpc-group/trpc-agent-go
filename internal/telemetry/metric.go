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
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

var (
	// MeterProvider is the global OpenTelemetry meter provider used for creating meters.
	// It defaults to a no-op implementation and should be initialized with a real provider.
	MeterProvider metric.MeterProvider = noop.NewMeterProvider()

	// ChatMeter is the meter used for recording chat-related metrics.
	ChatMeter metric.Meter = MeterProvider.Meter(metrics.MeterNameChat)

	// ChatMetricTRPCAgentGoClientRequestCnt records the number of chat requests made.
	ChatMetricTRPCAgentGoClientRequestCnt metric.Int64Counter = noop.Int64Counter{}
	// ChatMetricGenAIClientTokenUsage records the distribution of token usage (both input and output tokens).
	ChatMetricGenAIClientTokenUsage metric.Int64Histogram = noop.Int64Histogram{}
	// ChatMetricGenAIClientOperationDuration records the distribution of total chat operation durations in seconds.
	ChatMetricGenAIClientOperationDuration metric.Float64Histogram = noop.Float64Histogram{}
	// ChatMetricGenAIServerTimeToFirstToken records the distribution of time to first token latency in seconds.
	// This measures the time from request start until the first token is received.
	ChatMetricGenAIServerTimeToFirstToken metric.Float64Histogram = noop.Float64Histogram{}
	// ChatMetricTRPCAgentGoClientTimeToFirstToken records the distribution of time to first token latency in seconds.
	// Note: This metric is reported alongside ChatMetricGenAIServerTimeToFirstToken with the same value.
	ChatMetricTRPCAgentGoClientTimeToFirstToken metric.Float64Histogram = noop.Float64Histogram{}
	// ChatMetricTRPCAgentGoClientTimePerOutputToken records the distribution of average time per output token in seconds.
	// This metric measures the decode phase performance by calculating (total_duration - time_to_first_token) / (output_tokens - first_token_count).
	ChatMetricTRPCAgentGoClientTimePerOutputToken metric.Float64Histogram = noop.Float64Histogram{}
	// ChatMetricTRPCAgentGoClientOutputTokenPerTime records the distribution of output token per time for client.
	// 1 / ChatMetricTRPCAgentGoClientTimePerOutputToken.
	ChatMetricTRPCAgentGoClientOutputTokenPerTime metric.Float64Histogram = noop.Float64Histogram{}
)

// chatAttributes is the attributes for chat metrics.
type chatAttributes struct {
	RequestModelName  string
	ResponseModelName string
	Stream            bool
	AgentName         string

	AppName   string
	UserID    string
	SessionID string

	ErrorType string
	Error     error
}

func (a chatAttributes) toAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(KeyGenAIOperationName, OperationChat),
		attribute.String(KeyGenAISystem, a.RequestModelName),
		attribute.String(KeyGenAIRequestModel, a.RequestModelName),
		attribute.Bool(metrics.KeyTRPCAgentGoStream, a.Stream),
	}
	if a.ResponseModelName != "" {
		attrs = append(attrs, attribute.String(KeyGenAIResponseModel, a.ResponseModelName))
	}
	if a.AppName != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoAppName, a.AppName))
	}
	if a.UserID != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoUserID, a.UserID))
	}
	if a.SessionID != "" {
		attrs = append(attrs, attribute.String(KeyGenAIConversationID, a.SessionID))
	}
	if a.ErrorType != "" {
		attrs = append(attrs, attribute.String(KeyErrorType, a.ErrorType))
	} else if a.Error != nil {
		attrs = append(attrs, attribute.String(KeyErrorType, ValueDefaultErrorType))
	}
	if a.AgentName != "" {
		attrs = append(attrs, attribute.String(KeyGenAIAgentName, a.AgentName))
	}

	return attrs
}

// ChatMetricsTracker tracks metrics for a single chat request lifecycle.
type ChatMetricsTracker struct {
	ctx                    context.Context
	start                  time.Time
	isFirstToken           bool
	firstTokenTimeDuration time.Duration
	firstCompleteToken     int
	totalCompletionTokens  int
	totalPromptTokens      int
	lastEvent              *event.Event

	// Timing tracking for streaming reasoning phases
	firstReasoningTime time.Time
	lastReasoningTime  time.Time

	// TimingInfo is response timing info that will be recorded in session and attached to events
	timingInfo *model.TimingInfo

	// Configuration
	invocation *agent.Invocation
	llmRequest *model.Request
	err        *error // pointer to capture final error
}

// NewChatMetricsTracker creates a new telemetry tracker.
// The timingInfo parameter should be obtained from invocation state to ensure
// only the first LLM call records timing information.
func NewChatMetricsTracker(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	timingInfo *model.TimingInfo,
	err *error,
) *ChatMetricsTracker {
	return &ChatMetricsTracker{
		ctx:          ctx,
		start:        time.Now(),
		isFirstToken: true,
		invocation:   invocation,
		llmRequest:   llmRequest,
		err:          err,
		timingInfo:   timingInfo,
	}
}

// TrackResponse updates telemetry state and timing info for each response chunk.
// This method tracks both token usage metrics and timing information (FirstTokenDuration and ReasoningDuration).
// Call this for each response received from the LLM.
func (t *ChatMetricsTracker) TrackResponse(response *model.Response) {
	if response == nil {
		return
	}

	now := time.Now()

	// Track first token timing (for both metrics and timing info)
	if t.isFirstToken {
		// Always record firstTokenTimeDuration for metrics (even if no content)
		t.firstTokenTimeDuration = time.Since(t.start)
		t.isFirstToken = false

		// Update FirstTokenDuration in TimingInfo only if not already recorded (first LLM call only)
		// Meaningful content = reasoning content, regular content, or tool calls
		if t.timingInfo != nil && t.timingInfo.FirstTokenDuration == 0 && len(response.Choices) > 0 {
			t.timingInfo.FirstTokenDuration = now.Sub(t.start)
		}

		if response.Usage != nil {
			t.firstCompleteToken = response.Usage.CompletionTokens
		}
	}

	// Track token usage
	if response.Usage != nil {
		t.totalPromptTokens = response.Usage.PromptTokens
		t.totalCompletionTokens = response.Usage.CompletionTokens
	}

	// Track reasoning duration (streaming mode only, first LLM call only)
	// Measures from first reasoning chunk to last reasoning chunk
	if t.llmRequest != nil &&
		t.llmRequest.Stream &&
		t.timingInfo != nil &&
		t.timingInfo.ReasoningDuration == 0 &&
		len(response.Choices) > 0 {
		choice := response.Choices[0]
		hasReasoningContent := choice.Delta.ReasoningContent != "" || choice.Message.ReasoningContent != ""

		if hasReasoningContent {
			// Track reasoning phase start and continuation
			if t.firstReasoningTime.IsZero() {
				t.firstReasoningTime = now
			}
			t.lastReasoningTime = now
		} else if !t.firstReasoningTime.IsZero() && !t.lastReasoningTime.IsZero() {
			// Reasoning phase ended (first non-reasoning chunk received), record duration
			t.timingInfo.ReasoningDuration = t.lastReasoningTime.Sub(t.firstReasoningTime)
		}
	}
}

// SetLastEvent updates the last event seen (for extracting response model name and error).
func (t *ChatMetricsTracker) SetLastEvent(evt *event.Event) {
	t.lastEvent = evt
}

// FirstTokenTimeDuration returns the time to first token duration.
func (t *ChatMetricsTracker) FirstTokenTimeDuration() time.Duration {
	return t.firstTokenTimeDuration
}

// GetTimingInfo returns the current TimingInfo for attaching to responses.
func (t *ChatMetricsTracker) GetTimingInfo() *model.TimingInfo {
	return t.timingInfo
}

// RecordMetrics returns a defer function that records all telemetry metrics.
// Should be called with defer immediately after creating the tracker.
func (t *ChatMetricsTracker) RecordMetrics() func() {
	return func() {
		attrs := t.buildAttributes()
		requestDuration := time.Since(t.start)
		otelAttrs := attrs.toAttributes()

		// Increment chat request counter
		ChatMetricTRPCAgentGoClientRequestCnt.Add(t.ctx, 1, metric.WithAttributes(otelAttrs...))

		// Record chat request duration
		ChatMetricGenAIClientOperationDuration.Record(t.ctx, requestDuration.Seconds(), metric.WithAttributes(otelAttrs...))

		// Record time to first token (report both metrics with the same value)
		ChatMetricGenAIServerTimeToFirstToken.Record(t.ctx, t.firstTokenTimeDuration.Seconds(),
			metric.WithAttributes(otelAttrs...))
		ChatMetricTRPCAgentGoClientTimeToFirstToken.Record(t.ctx, t.firstTokenTimeDuration.Seconds(),
			metric.WithAttributes(otelAttrs...))

		// Record input token usage
		ChatMetricGenAIClientTokenUsage.Record(t.ctx, int64(t.totalPromptTokens),
			metric.WithAttributes(append(otelAttrs, attribute.String(KeyGenAITokenType, metrics.KeyTRPCAgentGoInputTokenType))...))

		// Record output token usage
		ChatMetricGenAIClientTokenUsage.Record(t.ctx, int64(t.totalCompletionTokens),
			metric.WithAttributes(append(otelAttrs, attribute.String(KeyGenAITokenType, metrics.KeyTRPCAgentGoOutputTokenType))...))

		// Calculate and record derived metrics
		t.recordDerivedMetrics(otelAttrs, requestDuration)
	}
}

// buildAttributes constructs chatAttributes from tracked state.
func (t *ChatMetricsTracker) buildAttributes() chatAttributes {
	attrs := chatAttributes{}

	// Extract error
	if t.err != nil && *t.err != nil {
		attrs.Error = *t.err
	}

	// Extract request attributes
	if t.llmRequest != nil {
		attrs.Stream = t.llmRequest.GenerationConfig.Stream
	}

	// Extract invocation attributes (with nil safety)
	if t.invocation != nil {
		if t.invocation.AgentName != "" {
			attrs.AgentName = t.invocation.AgentName
		}
		if t.invocation.Model != nil {
			attrs.RequestModelName = t.invocation.Model.Info().Name
		}
		if t.invocation.Session != nil {
			attrs.SessionID = t.invocation.Session.ID
			attrs.UserID = t.invocation.Session.UserID
			attrs.AppName = t.invocation.Session.AppName
		}
	}

	// Extract response attributes from last event
	if t.lastEvent != nil {
		if t.lastEvent.Response != nil {
			attrs.ResponseModelName = t.lastEvent.Response.Model
		}
		if t.lastEvent.Error != nil {
			attrs.ErrorType = t.lastEvent.Error.Type
		}
	}

	return attrs
}

// recordDerivedMetrics calculates and records time-per-token and token-per-time metrics.
func (t *ChatMetricsTracker) recordDerivedMetrics(otelAttrs []attribute.KeyValue, requestDuration time.Duration) {
	tokens := t.totalCompletionTokens - t.firstCompleteToken
	duration := requestDuration - t.firstTokenTimeDuration

	if tokens > 0 && duration > 0 {
		// Record time per output token
		ChatMetricTRPCAgentGoClientTimePerOutputToken.Record(t.ctx, (duration / time.Duration(tokens)).Seconds(),
			metric.WithAttributes(otelAttrs...))
		// Record output token per time
		ChatMetricTRPCAgentGoClientOutputTokenPerTime.Record(t.ctx, float64(tokens)/duration.Seconds(),
			metric.WithAttributes(otelAttrs...))
	} else if tokens == 0 && t.totalCompletionTokens > 0 && requestDuration > 0 {
		// Record time per output token
		ChatMetricTRPCAgentGoClientTimePerOutputToken.Record(t.ctx, (requestDuration / time.Duration(t.totalCompletionTokens)).Seconds(),
			metric.WithAttributes(otelAttrs...))
		// Record output token per time
		ChatMetricTRPCAgentGoClientOutputTokenPerTime.Record(t.ctx, float64(t.totalCompletionTokens)/requestDuration.Seconds(),
			metric.WithAttributes(otelAttrs...))
	}
}

var (
	// ExecuteToolMeter is the meter used for recording tool execution metrics.
	ExecuteToolMeter = MeterProvider.Meter(metrics.MeterNameExecuteTool)

	// ExecuteToolMetricTRPCAgentGoClientRequestCnt records the number of tool execution requests made.
	ExecuteToolMetricTRPCAgentGoClientRequestCnt metric.Int64Counter = noop.Int64Counter{}
	// ExecuteToolMetricGenAIClientOperationDuration records the distribution of tool execution durations in seconds.
	ExecuteToolMetricGenAIClientOperationDuration metric.Float64Histogram = noop.Float64Histogram{}
)

// ExecuteToolAttributes is the attributes for tool execution metrics.
type ExecuteToolAttributes struct {
	RequestModelName string
	ToolName         string
	AppName          string
	AgentName        string
	UserID           string
	SessionID        string
	Error            error
	ErrorType        string
}

func (a ExecuteToolAttributes) toAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(KeyGenAIOperationName, OperationExecuteTool),
		attribute.String(KeyGenAISystem, a.RequestModelName),
		attribute.String(KeyGenAIToolName, a.ToolName),
	}
	if a.AppName != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoAppName, a.AppName))
	}
	if a.UserID != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoUserID, a.UserID))
	}
	if a.SessionID != "" {
		attrs = append(attrs, attribute.String(KeyGenAIConversationID, a.SessionID))
	}
	if a.AgentName != "" {
		attrs = append(attrs, attribute.String(KeyGenAIAgentName, a.AgentName))
	}
	if a.ErrorType != "" {
		attrs = append(attrs, attribute.String(KeyErrorType, a.ErrorType))
	} else if a.Error != nil {
		attrs = append(attrs, attribute.String(KeyErrorType, ValueDefaultErrorType))
	}
	return attrs
}

// ReportExecuteToolMetrics reports the tool execution metrics.
func ReportExecuteToolMetrics(ctx context.Context, attrs ExecuteToolAttributes, duration time.Duration) {
	as := attrs.toAttributes()
	ExecuteToolMetricTRPCAgentGoClientRequestCnt.Add(ctx, 1, metric.WithAttributes(as...))
	ExecuteToolMetricGenAIClientOperationDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(as...))
}
