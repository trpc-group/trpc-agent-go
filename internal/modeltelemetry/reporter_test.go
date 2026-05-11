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

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvmetrics "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"

	"github.com/stretchr/testify/require"
)

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

func attributeSetHasString(attrs attribute.Set, key string, value string) bool {
	for _, attr := range attrs.ToSlice() {
		if string(attr.Key) == key && attr.Value.AsString() == value {
			return true
		}
	}
	return false
}
