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
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

// setupInvokeAgentMetrics installs a manual-reader meter provider scoped to
// the test and returns a collector callback alongside a cleanup function.
func setupInvokeAgentMetrics(t *testing.T) (collect func() metricdata.ResourceMetrics, cleanup func()) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	savedProvider := MeterProvider
	savedCnt := InvokeAgentMetricGenAIRequestCnt
	savedTokens := InvokeAgentMetricGenAIClientTokenUsage
	savedTTFT := InvokeAgentMetricGenAIClientTimeToFirstToken
	savedDuration := InvokeAgentMetricGenAIClientOperationDuration
	savedMeter := InvokeAgentMeter

	MeterProvider = provider
	InvokeAgentMeter = provider.Meter(metrics.MeterNameInvokeAgent)

	var err error
	InvokeAgentMetricGenAIRequestCnt, err = InvokeAgentMeter.Int64Counter(
		metrics.MetricTRPCAgentGoClientRequestCnt,
	)
	require.NoError(t, err)
	InvokeAgentMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(
		provider,
		metrics.MeterNameInvokeAgent,
		metrics.MetricGenAIClientTokenUsage,
	)
	require.NoError(t, err)
	InvokeAgentMetricGenAIClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameInvokeAgent,
		metrics.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithUnit("s"),
	)
	require.NoError(t, err)
	InvokeAgentMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameInvokeAgent,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithUnit("s"),
	)
	require.NoError(t, err)

	collect = func() metricdata.ResourceMetrics {
		var rm metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(context.Background(), &rm))
		return rm
	}
	cleanup = func() {
		MeterProvider = savedProvider
		InvokeAgentMetricGenAIRequestCnt = savedCnt
		InvokeAgentMetricGenAIClientTokenUsage = savedTokens
		InvokeAgentMetricGenAIClientTimeToFirstToken = savedTTFT
		InvokeAgentMetricGenAIClientOperationDuration = savedDuration
		InvokeAgentMeter = savedMeter
	}
	return collect, cleanup
}

func TestInvokeAgentSpanName(t *testing.T) {
	require.Equal(t, OperationInvokeAgent, InvokeAgentSpanName(nil))
	require.Equal(t, OperationInvokeAgent, InvokeAgentSpanName(&agent.Invocation{}))
	require.Equal(t,
		OperationInvokeAgent+" worker",
		InvokeAgentSpanName(&agent.Invocation{AgentName: "worker"}),
	)
}

func TestStartInvokeAgent_WritesBeforeAttributes(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	inv := &agent.Invocation{
		AgentName:    "worker",
		InvocationID: "inv-1",
	}

	ctx, rec := StartInvokeAgent(context.Background(), inv, span, true, InvokeAgentOptions{
		Description:  "unit test agent",
		Instructions: "please do a thing",
		Stream:       true,
	})
	require.NotNil(t, rec)
	require.True(t, IsInvokeAgentActive(ctx),
		"expected returned context to be marked as invoke_agent active")
	require.True(t, rec.TraceStarted())
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIAgentName, "worker"))
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIAgentDescription, "unit test agent"))
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAISystemInstructions, "please do a thing"))
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIRequestIsStream, true))

	rec.Finish()
	_ = collect()
}

func TestStartInvokeAgent_GenConfigDefault(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	_, rec := StartInvokeAgent(
		context.Background(),
		&agent.Invocation{AgentName: "worker"},
		span,
		true,
		InvokeAgentOptions{Stream: false},
	)
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIRequestIsStream, false),
		"expected default GenConfig to carry the Stream flag")

	rec.Finish()
	_ = collect()
}

func TestStartInvokeAgent_NoopSpanSkipsAttributes(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := noop.Span{}
	ctx, rec := StartInvokeAgent(context.Background(), &agent.Invocation{
		AgentName: "worker",
	}, span, false, InvokeAgentOptions{
		Description: "ignored",
	})

	require.False(t, rec.TraceStarted())
	require.True(t, IsInvokeAgentActive(ctx),
		"context must still be marked active so children can short-circuit")

	// Finish should still record metrics even when tracing is disabled.
	rec.Finish()
	rm := collect()
	require.NotEmpty(t, rm.ScopeMetrics,
		"expected metrics to be recorded even when tracing is disabled")
}

func TestStartInvokeAgent_NilSpan(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	_, rec := StartInvokeAgent(
		context.Background(),
		&agent.Invocation{AgentName: "worker"},
		nil,
		true,
		InvokeAgentOptions{},
	)
	require.False(t, rec.TraceStarted(),
		"nil span input should degrade to a no-op span")
	require.NotNil(t, rec.Span())

	rec.Finish()
	_ = collect()
}

func TestInvokeAgentRecorder_NilReceiver(t *testing.T) {
	var rec *InvokeAgentRecorder
	require.NotPanics(t, func() {
		rec.Observe(&event.Event{})
		rec.SetResponseErrorType("rate_limit")
		rec.Finish()
		rec.Finish()
		_ = rec.TraceStarted()
		require.NotNil(t, rec.Span())
	})
}

func TestInvokeAgentRecorder_Observe_AccumulatesTokens(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	_, rec := StartInvokeAgent(context.Background(), &agent.Invocation{
		AgentName: "worker",
	}, span, true, InvokeAgentOptions{Stream: true})

	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: true,
		Choices:   []model.Choice{{Delta: model.Message{Content: "Hel"}}},
	}})
	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: false,
		Usage:     &model.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}})
	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: false,
		Usage:     &model.Usage{CompletionTokens: 3, TotalTokens: 3},
	}})

	require.Equal(t, 10, rec.tokenUsage.PromptTokens)
	require.Equal(t, 8, rec.tokenUsage.CompletionTokens)
	require.Equal(t, 18, rec.tokenUsage.TotalTokens)
	require.NotNil(t, rec.fullRespEvent,
		"expected fullRespEvent to be set from the final non-partial event")

	rec.Finish()
	_ = collect()
}

func TestInvokeAgentRecorder_Observe_IgnoresPartialForFullResp(t *testing.T) {
	_, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	_, rec := StartInvokeAgent(context.Background(), &agent.Invocation{
		AgentName: "worker",
	}, span, true, InvokeAgentOptions{})
	rec.Observe(&event.Event{Response: &model.Response{IsPartial: true}})
	require.Nil(t, rec.fullRespEvent)
	rec.Finish()
}

func TestInvokeAgentRecorder_Observe_RecordsErrorEvent(t *testing.T) {
	_, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	_, rec := StartInvokeAgent(context.Background(), &agent.Invocation{
		AgentName: "worker",
	}, span, true, InvokeAgentOptions{})

	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: true,
		Error:     &model.ResponseError{Type: "rate_limit"},
	}})
	require.Equal(t, "rate_limit", rec.responseErrorType)

	rec.Finish()
	// When no fullRespEvent exists, Finish should annotate the span with the
	// observed error classification.
	require.Equal(t, codes.Error, span.status)
	require.True(t,
		hasAttr(span.attrs, semconvtrace.KeyErrorType, "rate_limit"),
		"expected error.type attribute to be written")
}

func TestInvokeAgentRecorder_Finish_Idempotent(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	_, rec := StartInvokeAgent(context.Background(), &agent.Invocation{
		AgentName: "worker",
	}, span, true, InvokeAgentOptions{})

	rec.Finish()
	rm1 := collect()
	rec.Finish()
	rec.Finish()
	rm2 := collect()

	require.Equal(t, len(rm1.ScopeMetrics), len(rm2.ScopeMetrics),
		"repeated Finish calls must not produce additional scope metrics")
}

func TestInvokeAgentRecorder_Finish_SuccessResponseClearsObservedError(t *testing.T) {
	_, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	_, rec := StartInvokeAgent(context.Background(), &agent.Invocation{
		AgentName: "worker",
	}, span, true, InvokeAgentOptions{})

	// Observe a transient error, then a successful terminal response.
	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: true,
		Error:     &model.ResponseError{Type: "transient"},
	}})
	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: false,
		ID:        "rsp-1",
	}})

	rec.Finish()
	require.Empty(t, rec.responseErrorType,
		"a successful terminal response should wipe any earlier error classification")
}

func TestInvokeAgentRecorder_Finish_PreservesTerminalError(t *testing.T) {
	_, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	_, rec := StartInvokeAgent(context.Background(), &agent.Invocation{
		AgentName: "worker",
	}, span, true, InvokeAgentOptions{})

	// A terminal response event carrying an Error wins over earlier
	// observations.
	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: false,
		Error:     &model.ResponseError{Type: "server_error"},
	}})

	rec.Finish()
	require.Equal(t, "server_error", rec.responseErrorType)
}

func TestInvokeAgentRecorder_SetResponseErrorType_Overrides(t *testing.T) {
	_, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	_, rec := StartInvokeAgent(context.Background(), &agent.Invocation{
		AgentName: "worker",
	}, span, true, InvokeAgentOptions{})

	rec.SetResponseErrorType("timeout")
	require.Equal(t, "timeout", rec.responseErrorType)

	// Empty string clears the override.
	rec.SetResponseErrorType("")
	require.Empty(t, rec.responseErrorType)

	rec.Finish()
}

func TestInvokeAgentRecorder_Finish_TerminalResponseWritesAfterAttributes(t *testing.T) {
	_, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	_, rec := StartInvokeAgent(context.Background(), &agent.Invocation{
		AgentName: "worker",
	}, span, true, InvokeAgentOptions{})

	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: false,
		Usage:     &model.Usage{PromptTokens: 7, CompletionTokens: 11},
	}})
	rec.Finish()

	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIUsageInputTokens, int64(7)))
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIUsageOutputTokens, int64(11)))
}

func TestInvokeAgentRecorder_Observe_NilInputs(t *testing.T) {
	_, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	_, rec := StartInvokeAgent(context.Background(), &agent.Invocation{
		AgentName: "worker",
	}, span, true, InvokeAgentOptions{})

	require.NotPanics(t, func() {
		rec.Observe(nil)
		rec.Observe(&event.Event{}) // event with no Response and no Error
	})
	require.Nil(t, rec.fullRespEvent)
	require.Empty(t, rec.responseErrorType)

	rec.Finish()
}
