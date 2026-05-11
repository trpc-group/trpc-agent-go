//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package modeltelemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	semconvmetrics "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	telemetrytrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

type testModel struct {
	name string
}

func (m *testModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *testModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func TestInvocationViewCopiesInvocationAndUsesFallbackModel(t *testing.T) {
	fallbackModel := &testModel{name: "fallback-model"}
	existingSession := &session.Session{
		ID:     "session-id",
		UserID: "user-id",
	}
	inv := &agent.Invocation{
		AgentName:    "agent-name",
		InvocationID: "invocation-id",
		Session:      existingSession,
		RunOptions: agent.RunOptions{
			AppName: "app-name",
		},
	}

	view := invocationView(agent.NewInvocationContext(context.Background(), inv), fallbackModel)
	require.NotSame(t, inv, view)
	require.Equal(t, "agent-name", view.AgentName)
	require.Equal(t, "invocation-id", view.InvocationID)
	require.Same(t, existingSession, view.Session)
	require.True(t, view.Model == fallbackModel)
	require.Equal(t, "app-name", view.RunOptions.AppName)
	require.Nil(t, inv.Model)
}

func TestInvocationViewKeepsInvocationModel(t *testing.T) {
	fallbackModel := &testModel{name: "fallback-model"}
	existingModel := &testModel{name: "existing-model"}
	inv := &agent.Invocation{
		Model: existingModel,
	}

	view := invocationView(agent.NewInvocationContext(context.Background(), inv), fallbackModel)
	require.True(t, view.Model == existingModel)
}

func TestReporterTrackResponseTracesWithTracker(t *testing.T) {
	recorder := useChatTelemetrySpanRecorder(t)
	llm := &testModel{name: "request-model"}
	reporter := StartChat(context.Background(), llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	}, true)

	reporter.TrackResponse(&model.Response{
		ID:    "response-id",
		Model: "response-model",
		Usage: &model.Usage{
			PromptTokens:     3,
			CompletionTokens: 5,
		},
	})
	reporter.End()

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	attrs := attributesMap(spans[0].Attributes())
	require.Equal(t, "request-model", attrs[semconvtrace.KeyGenAIRequestModel].AsString())
	require.Equal(t, "response-model", attrs[semconvtrace.KeyGenAIResponseModel].AsString())
	require.Equal(t, "response-id", attrs[semconvtrace.KeyGenAIResponseID].AsString())
	require.Equal(t, int64(3), attrs[semconvtrace.KeyGenAIUsageInputTokens].AsInt64())
	require.Equal(t, int64(5), attrs[semconvtrace.KeyGenAIUsageOutputTokens].AsInt64())
}

func TestReporterTrackResponseHandlesNilInputsAndNilTracker(t *testing.T) {
	recorder := useChatTelemetrySpanRecorder(t)
	_, span := telemetrytrace.Tracer.Start(context.Background(), "manual-chat")

	var nilReporter *Reporter
	require.NotPanics(t, func() {
		nilReporter.TrackResponse(&model.Response{})
		(&Reporter{}).TrackResponse(nil)
		(&Reporter{span: span, startedSpan: true}).TrackResponse(&model.Response{
			ID:    "manual-response-id",
			Model: "manual-response-model",
		})
		span.End()
	})

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	attrs := attributesMap(spans[0].Attributes())
	require.Equal(t, "manual-response-model", attrs[semconvtrace.KeyGenAIResponseModel].AsString())
	require.Equal(t, "manual-response-id", attrs[semconvtrace.KeyGenAIResponseID].AsString())
}

func TestReporterEndRecordsContextCancellationErrorType(t *testing.T) {
	reader := useChatTelemetryRequestCounter(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	reporter := StartChat(ctx, nil, &model.Request{}, true)
	reporter.End()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, metricHasStringAttribute(
		rm,
		semconvmetrics.MetricTRPCAgentGoClientRequestCnt,
		semconvtrace.KeyErrorType,
		semconvtrace.ValueDefaultErrorType,
	))
}

func useChatTelemetryRequestCounter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalChatMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTTFT := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTTFT := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	t.Cleanup(func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalChatMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTTFT
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTTFT
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalTokenPerTime
		_ = provider.Shutdown(context.Background())
	})

	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter(semconvmetrics.MetricTRPCAgentGoClientRequestCnt)
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	return reader
}

func metricHasStringAttribute(
	rm metricdata.ResourceMetrics,
	metricName string,
	key string,
	value string,
) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			if metric.Name != metricName {
				continue
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				if attributeSetHasString(point.Attributes, key, value) {
					return true
				}
			}
		}
	}
	return false
}

func useChatTelemetrySpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := telemetrytrace.TracerProvider
	originalTracer := telemetrytrace.Tracer
	telemetrytrace.TracerProvider = provider
	telemetrytrace.Tracer = provider.Tracer(itelemetry.InstrumentName)
	t.Cleanup(func() {
		telemetrytrace.TracerProvider = originalProvider
		telemetrytrace.Tracer = originalTracer
		_ = provider.Shutdown(context.Background())
	})
	return recorder
}

func attributesMap(attrs []attribute.KeyValue) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value
	}
	return out
}

func attributeSetHasString(attrs attribute.Set, key string, value string) bool {
	for _, attr := range attrs.ToSlice() {
		if string(attr.Key) == key && attr.Value.AsString() == value {
			return true
		}
	}
	return false
}
