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
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

func TestChatMetricsTracker_RecordMetrics_NoMetrics_ReturnsNoop(t *testing.T) {
	originalRequestCnt := ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := ChatMetricGenAIClientTokenUsage
	originalOperationDuration := ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := ChatMetricTRPCAgentGoClientOutputTokenPerTime
	t.Cleanup(func() {
		ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		ChatMetricGenAIClientTokenUsage = originalTokenUsage
		ChatMetricGenAIClientOperationDuration = originalOperationDuration
		ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	})

	ChatMetricTRPCAgentGoClientRequestCnt = nil
	ChatMetricGenAIClientTokenUsage = nil
	ChatMetricGenAIClientOperationDuration = nil
	ChatMetricGenAIServerTimeToFirstToken = nil
	ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil

	tracker := NewChatMetricsTracker(context.Background(), nil, nil, &model.TimingInfo{}, nil, nil)
	recordFunc := tracker.RecordMetrics()
	require.NotNil(t, recordFunc)
	require.NotPanics(t, recordFunc)
}

func TestChatMetricsTracker_TrackResponse_ReasoningDuration_UsesLazyNow(t *testing.T) {
	ctx := context.Background()
	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}
	timingInfo := &model.TimingInfo{}
	tracker := NewChatMetricsTracker(ctx, nil, req, timingInfo, nil, nil)

	tracker.TrackResponse(&model.Response{})
	require.True(t, tracker.isFirstToken, "expected empty chunk to be ignored for TTFT")
	require.Zero(t, tracker.firstTokenTimeDuration, "expected TTFT to remain unset after empty chunk")

	tracker.TrackResponse(&model.Response{
		Choices: []model.Choice{
			{
				Delta: model.Message{ReasoningContent: "r1"},
			},
		},
	})
	require.False(t, tracker.isFirstToken, "expected reasoning payload to consume first token")
	require.Greater(t, tracker.firstTokenTimeDuration, time.Duration(0), "expected TTFT to be recorded on first meaningful payload")
	require.False(t, tracker.firstReasoningTime.IsZero(), "expected reasoning start time to be recorded")
	require.Equal(t, tracker.firstReasoningTime, tracker.lastReasoningTime, "expected first reasoning chunk to update last time")

	time.Sleep(10 * time.Millisecond)
	tracker.TrackResponse(&model.Response{
		Choices: []model.Choice{
			{
				Delta: model.Message{ReasoningContent: "r2"},
			},
		},
	})
	require.True(t, tracker.lastReasoningTime.After(tracker.firstReasoningTime), "expected reasoning time to advance")

	tracker.TrackResponse(&model.Response{
		Choices: []model.Choice{
			{
				Delta: model.Message{Content: "done"},
			},
		},
	})
	require.Greater(t, timingInfo.ReasoningDuration, time.Duration(0), "expected reasoning duration to be recorded")
}

func TestChatMetricsTracker_RecordMetrics_SkipsTimeToFirstTokenWithoutValidContent(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	originalProvider := MeterProvider
	originalChatMeter := ChatMeter
	originalRequestCnt := ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := ChatMetricGenAIClientTokenUsage
	originalOperationDuration := ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := ChatMetricTRPCAgentGoClientOutputTokenPerTime
	t.Cleanup(func() {
		MeterProvider = originalProvider
		ChatMeter = originalChatMeter
		ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		ChatMetricGenAIClientTokenUsage = originalTokenUsage
		ChatMetricGenAIClientOperationDuration = originalOperationDuration
		ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	})

	MeterProvider = provider
	ChatMeter = provider.Meter(metrics.MeterNameChat)

	var err error
	ChatMetricTRPCAgentGoClientRequestCnt, err = ChatMeter.Int64Counter(metrics.MetricTRPCAgentGoClientRequestCnt)
	require.NoError(t, err)
	ChatMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameChat, metrics.MetricGenAIClientTokenUsage)
	require.NoError(t, err)
	ChatMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(provider, metrics.MeterNameChat, metrics.MetricGenAIClientOperationDuration)
	require.NoError(t, err)
	ChatMetricGenAIServerTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameChat,
		metrics.MetricGenAIServerTimeToFirstToken,
		metric.WithDescription("Time to first token for server"),
		metric.WithUnit("s"),
	)
	require.NoError(t, err)
	ChatMetricTRPCAgentGoClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameChat,
		metrics.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithDescription("Time to first token for client"),
		metric.WithUnit("s"),
	)
	require.NoError(t, err)
	ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil

	ctx := context.Background()
	tracker := NewChatMetricsTracker(ctx, nil, nil, &model.TimingInfo{}, nil, nil)
	tracker.TrackResponse(&model.Response{
		Usage: &model.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
		},
	})

	tracker.RecordMetrics()()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))

	metricNames := collectMetricNames(rm)
	require.Contains(t, metricNames, metrics.MetricTRPCAgentGoClientRequestCnt)
	require.Contains(t, metricNames, metrics.MetricGenAIClientTokenUsage)
	require.Contains(t, metricNames, metrics.MetricGenAIClientOperationDuration)
	require.NotContains(t, metricNames, metrics.MetricGenAIServerTimeToFirstToken)
	require.NotContains(t, metricNames, metrics.MetricTRPCAgentGoClientTimeToFirstToken)
}

func collectMetricNames(rm metricdata.ResourceMetrics) []string {
	var names []string
	for _, scopeMetrics := range rm.ScopeMetrics {
		for _, metric := range scopeMetrics.Metrics {
			names = append(names, metric.Name)
		}
	}
	return names
}
