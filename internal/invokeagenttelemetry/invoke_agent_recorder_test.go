//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package invokeagenttelemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	oteltrace "go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	metricsemconv "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

type recordingSpan struct {
	oteltrace.Span
	attrs          []attribute.KeyValue
	status         codes.Code
	statusDesc     string
	recordedErrors []error
}

func (s *recordingSpan) IsRecording() bool {
	return true
}

func (s *recordingSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.attrs = append(s.attrs, kv...)
	s.Span.SetAttributes(kv...)
}

func (s *recordingSpan) SetStatus(code codes.Code, description string) {
	s.status = code
	s.statusDesc = description
	s.Span.SetStatus(code, description)
}

func (s *recordingSpan) RecordError(err error, opts ...oteltrace.EventOption) {
	s.recordedErrors = append(s.recordedErrors, err)
	s.Span.RecordError(err, opts...)
}

func newRecordingSpan() *recordingSpan {
	_, span := nooptrace.NewTracerProvider().Tracer("test").Start(context.Background(), "op")
	return &recordingSpan{Span: span}
}

func hasAttr(attrs []attribute.KeyValue, key string, want any) bool {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			switch got := kv.Value.AsInterface().(type) {
			case []string:
				expected, ok := want.([]string)
				if !ok || len(got) != len(expected) {
					return false
				}
				for i := range got {
					if got[i] != expected[i] {
						return false
					}
				}
				return true
			default:
				return got == want
			}
		}
	}
	return false
}

func testInvocationView(agentName, invocationID string) *InvocationView {
	return &InvocationView{
		AgentName:    agentName,
		InvocationID: invocationID,
	}
}

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

	require.NoError(t, InitMeterProvider(provider))

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
	require.Equal(t, OperationInvokeAgent, InvokeAgentSpanName(&InvocationView{}))
	require.Equal(t, OperationInvokeAgent+" worker", InvokeAgentSpanName(testInvocationView("worker", "")))
}

func TestStartInvokeAgent_WritesBeforeAttributes(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	rec := StartInvokeAgent(
		context.Background(),
		testInvocationView("worker", "inv-1"),
		span,
		true,
		InvokeAgentOptions{
			Description:  "unit test agent",
			Instructions: "please do a thing",
			Stream:       true,
		},
	)

	require.NotNil(t, rec)
	require.True(t, rec.TraceStarted())
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIAgentName, "worker"))
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIAgentDescription, "unit test agent"))
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAISystemInstructions, "please do a thing"))
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIRequestIsStream, true))

	rec.Finish()
	_ = collect()
}

func TestStartInvokeAgent_NoopSpanStillRecordsMetrics(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	rec := StartInvokeAgent(
		context.Background(),
		testInvocationView("worker", ""),
		nooptrace.Span{},
		false,
		InvokeAgentOptions{Description: "ignored"},
	)

	require.False(t, rec.TraceStarted())
	rec.Finish()

	rm := collect()
	require.NotEmpty(t, rm.ScopeMetrics)
}

func TestInvokeAgentRecorder_ObserveAndFinish(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	rec := StartInvokeAgent(
		context.Background(),
		testInvocationView("worker", "inv-1"),
		span,
		true,
		InvokeAgentOptions{Stream: true},
	)

	// This partial content only triggers valid-content detection; token counts come from explicit Usage fields below.
	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: true,
		Choices:   []model.Choice{{Delta: model.Message{Content: "partial chunk"}}},
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
	require.NotNil(t, rec.fullRespEvent)

	rec.Finish()

	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIUsageInputTokens, int64(10)))
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyGenAIUsageOutputTokens, int64(8)))
	require.True(t, rec.finished)
	require.NotEmpty(t, collect().ScopeMetrics)
}

func TestInvokeAgentRecorder_Finish_ObservedErrorWithoutTerminalResponse(t *testing.T) {
	_, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	rec := StartInvokeAgent(
		context.Background(),
		testInvocationView("worker", "inv-1"),
		span,
		true,
		InvokeAgentOptions{},
	)

	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: true,
		Error:     &model.ResponseError{Type: "rate_limit"},
	}})

	rec.Finish()

	require.Equal(t, codes.Error, span.status)
	require.True(t, hasAttr(span.attrs, semconvtrace.KeyErrorType, "rate_limit"))
}

func TestInvokeAgentRecorder_Finish_TerminalResponseWinsOverEarlierError(t *testing.T) {
	_, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	span := newRecordingSpan()
	rec := StartInvokeAgent(
		context.Background(),
		testInvocationView("worker", "inv-1"),
		span,
		true,
		InvokeAgentOptions{},
	)

	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: true,
		Error:     &model.ResponseError{Type: "transient"},
	}})
	rec.Observe(&event.Event{Response: &model.Response{
		IsPartial: false,
		ID:        "rsp-1",
	}})

	rec.Finish()
	require.Empty(t, rec.responseErrorType)
}

func TestInvokeAgentRecorder_Finish_Idempotent(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	rec := StartInvokeAgent(
		context.Background(),
		testInvocationView("worker", "inv-1"),
		newRecordingSpan(),
		true,
		InvokeAgentOptions{},
	)

	rec.Finish()
	first := collect()
	rec.Finish()
	rec.Finish()
	second := collect()

	require.Equal(t, len(first.ScopeMetrics), len(second.ScopeMetrics))
}

func TestInvokeAgentTracker_RecordMetrics(t *testing.T) {
	collect, cleanup := setupInvokeAgentMetrics(t)
	defer cleanup()

	var trackerErr error
	tracker := NewInvokeAgentTracker(
		context.Background(),
		&InvocationView{AgentName: "worker", InvocationID: "inv-1"},
		true,
		&trackerErr,
	)

	tracker.TrackResponse(&model.Response{
		IsPartial: false,
		Choices:   []model.Choice{{Message: model.NewAssistantMessage("ok")}},
		Usage:     &model.Usage{PromptTokens: 4, CompletionTokens: 6, TotalTokens: 10},
	})
	tracker.SetResponseErrorType("timeout")
	tracker.RecordMetrics()()

	rm := collect()
	require.NotEmpty(t, rm.ScopeMetrics)

	var foundInvokeMetric bool
	for _, scopeMetric := range rm.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			if metric.Name == metricsemconv.MetricTRPCAgentGoClientRequestCnt {
				foundInvokeMetric = true
				break
			}
		}
	}
	require.True(t, foundInvokeMetric)
}
