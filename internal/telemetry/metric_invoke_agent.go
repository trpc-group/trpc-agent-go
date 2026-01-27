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
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

var (
	// InvokeAgentMeter is the meter used for recording agent invocation metrics.
	InvokeAgentMeter metric.Meter = MeterProvider.Meter(metrics.MeterNameInvokeAgent)

	// InvokeAgentMetricGenAIRequestCnt records the number of invoke agent requests made.
	InvokeAgentMetricGenAIRequestCnt metric.Int64Counter
	// InvokeAgentMetricGenAIClientTokenUsage records the distribution of input and output token usage.
	InvokeAgentMetricGenAIClientTokenUsage *histogram.DynamicInt64Histogram
	// InvokeAgentMetricGenAIClientTimeToFirstToken records the distribution of time to first token latency in seconds.
	InvokeAgentMetricGenAIClientTimeToFirstToken *histogram.DynamicFloat64Histogram
	// InvokeAgentMetricGenAIClientOperationDuration records the distribution of total agent invocation durations in seconds.
	InvokeAgentMetricGenAIClientOperationDuration *histogram.DynamicFloat64Histogram
)

// invokeAgentAttributes is the attributes for invoke agent metrics.
// It is a subset of chat attributes.
type invokeAgentAttributes struct {
	AgentName string
	AppName   string
	UserID    string
	System    string
	Stream    bool
	ErrorType string
	Error     error
}

func (a invokeAgentAttributes) toAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(KeyGenAIOperationName, OperationInvokeAgent),
		attribute.Bool(metrics.KeyTRPCAgentGoStream, a.Stream),
		attribute.String(KeyGenAISystem, a.System),
	}
	if a.AppName != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoAppName, a.AppName))
	}
	if a.UserID != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoUserID, a.UserID))
	}
	if a.AgentName != "" {
		attrs = append(attrs, attribute.String(KeyGenAIAgentName, a.AgentName))
	}
	if a.ErrorType != "" {
		attrs = append(attrs, attribute.String(KeyErrorType, a.ErrorType))
	} else if a.Error != nil {
		attrs = append(attrs, attribute.String(KeyErrorType, ToErrorType(a.Error, ValueDefaultErrorType)))
	}

	return attrs
}

// InvokeAgentTracker tracks metrics for a single agent invocation lifecycle.
type InvokeAgentTracker struct {
	ctx                    context.Context
	start                  time.Time
	isFirstToken           bool
	firstTokenTimeDuration time.Duration
	totalCompletionTokens  int
	totalPromptTokens      int

	attributes invokeAgentAttributes
}

// NewInvokeAgentTracker creates a new telemetry tracker for agent invocation.
func NewInvokeAgentTracker(
	ctx context.Context,
	invocation *agent.Invocation,
	stream bool,
	err *error,
) *InvokeAgentTracker {
	attributes := invokeAgentAttributes{Stream: stream, Error: *err}
	if invocation != nil {
		if invocation.AgentName != "" {
			attributes.AgentName = invocation.AgentName
		}
		if invocation.Model != nil {
			attributes.System = invocation.Model.Info().Name
		}
		if invocation.Session != nil {
			attributes.UserID = invocation.Session.UserID
			attributes.AppName = invocation.Session.AppName
		}
	}

	return &InvokeAgentTracker{
		ctx:          ctx,
		start:        time.Now(),
		isFirstToken: true,
		attributes:   attributes,
	}
}

// TrackResponse updates telemetry state for each response chunk.
func (t *InvokeAgentTracker) TrackResponse(response *model.Response) {
	if response == nil {
		return
	}

	if t.isFirstToken && response.IsValidContent() {
		t.firstTokenTimeDuration = time.Since(t.start)
		t.isFirstToken = false
	}

	// Track token usage
	if !response.IsPartial && response.Usage != nil {
		t.totalPromptTokens += response.Usage.PromptTokens
		t.totalCompletionTokens += response.Usage.CompletionTokens
	}
}

// SetResponseErrorType updates the response error type seen (for extracting error info).
func (t *InvokeAgentTracker) SetResponseErrorType(errorType string) {
	t.attributes.ErrorType = errorType
}

// RecordMetrics returns a defer function that records all telemetry metrics.
// Should be called with defer immediately after creating the tracker.
func (t *InvokeAgentTracker) RecordMetrics() func() {
	return func() {
		requestDuration := time.Since(t.start)
		otelAttrs := t.attributes.toAttributes()

		// Increment request counter
		if InvokeAgentMetricGenAIRequestCnt != nil {
			InvokeAgentMetricGenAIRequestCnt.Add(t.ctx, 1, metric.WithAttributes(otelAttrs...))
		}

		// Record request duration
		if InvokeAgentMetricGenAIClientOperationDuration != nil {
			InvokeAgentMetricGenAIClientOperationDuration.Record(t.ctx, requestDuration.Seconds(), metric.WithAttributes(otelAttrs...))
		}

		// Record time to first token
		if InvokeAgentMetricGenAIClientTimeToFirstToken != nil {
			InvokeAgentMetricGenAIClientTimeToFirstToken.Record(t.ctx, t.firstTokenTimeDuration.Seconds(),
				metric.WithAttributes(otelAttrs...))
		}

		// Record input token usage
		if InvokeAgentMetricGenAIClientTokenUsage != nil {
			InvokeAgentMetricGenAIClientTokenUsage.Record(t.ctx, int64(t.totalPromptTokens),
				metric.WithAttributes(append(otelAttrs, attribute.String(KeyGenAITokenType, metrics.KeyTRPCAgentGoInputTokenType))...))
		}

		// Record output token usage
		if InvokeAgentMetricGenAIClientTokenUsage != nil {
			InvokeAgentMetricGenAIClientTokenUsage.Record(t.ctx, int64(t.totalCompletionTokens),
				metric.WithAttributes(append(otelAttrs, attribute.String(KeyGenAITokenType, metrics.KeyTRPCAgentGoOutputTokenType))...))
		}

	}
}

// FirstTokenTimeDuration returns the time to first token duration.
func (t *InvokeAgentTracker) FirstTokenTimeDuration() time.Duration {
	return t.firstTokenTimeDuration
}
