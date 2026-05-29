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

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestWorkflowAttributesToAttributes(t *testing.T) {
	attrs := WorkflowAttributes{
		System:       "openai",
		AppName:      "test-app",
		UserID:       "user-123",
		AgentID:      "agent-1",
		AgentName:    "agent-name",
		WorkflowID:   "retrieve",
		WorkflowName: "execute_function_node retrieve",
		WorkflowType: WorkflowTypeFunction.String(),
		ErrorType:    "timeout",
		Error:        errors.New("ignored when error type is set"),
	}

	result := attrs.toAttributes()

	requireWorkflowAttr(t, result, semconvtrace.KeyGenAIOperationName, OperationWorkflow)
	requireWorkflowAttr(t, result, semconvtrace.KeyGenAISystem, "openai")
	requireWorkflowAttr(t, result, semconvtrace.KeyGenAIAppName, "test-app")
	requireWorkflowAttr(t, result, semconvtrace.KeyGenAIUserID, "user-123")
	requireWorkflowAttr(t, result, semconvtrace.KeyGenAIAgentID, "agent-1")
	requireWorkflowAttr(t, result, semconvtrace.KeyGenAIAgentName, "agent-name")
	requireWorkflowAttr(t, result, semconvtrace.KeyGenAIWorkflowID, "retrieve")
	requireWorkflowAttr(t, result, semconvtrace.KeyGenAIWorkflowName, "execute_function_node retrieve")
	requireWorkflowAttr(t, result, semconvtrace.KeyGenAIWorkflowType, WorkflowTypeFunction.String())
	requireWorkflowAttr(t, result, semconvtrace.KeyErrorType, "timeout")
}

func TestWorkflowAttributesAllowEmptySystemAndFallbackErrorType(t *testing.T) {
	attrs := WorkflowAttributes{
		AppName:      "test-app",
		UserID:       "user-123",
		AgentID:      "agent-1",
		WorkflowID:   "retrieve",
		WorkflowName: "retrieve",
		WorkflowType: WorkflowTypeFunction.String(),
		Error:        errors.New("boom"),
	}

	result := attrs.toAttributes()

	requireWorkflowAttr(t, result, semconvtrace.KeyGenAISystem, "")
	requireWorkflowAttr(t, result, semconvtrace.KeyErrorType, semconvtrace.ValueDefaultErrorType)
	requireNoWorkflowAttr(t, result, semconvtrace.KeyGenAIAgentName)
}

func TestReportWorkflowMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	originalProvider := MeterProvider
	originalMeter := WorkflowMeter
	originalDuration := WorkflowMetricGenAIClientOperationDuration
	originalElapsed := WorkflowMetricGenAIWorkflowElapsedTime
	defer func() {
		MeterProvider = originalProvider
		WorkflowMeter = originalMeter
		WorkflowMetricGenAIClientOperationDuration = originalDuration
		WorkflowMetricGenAIWorkflowElapsedTime = originalElapsed
	}()

	MeterProvider = provider
	WorkflowMeter = provider.Meter(metrics.MeterNameWorkflow)
	var err error
	WorkflowMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameWorkflow,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithUnit("s"),
	)
	require.NoError(t, err)

	ctx := context.Background()
	ReportWorkflowMetrics(ctx, WorkflowAttributes{
		System:       "",
		AppName:      "test-app",
		UserID:       "user-123",
		AgentID:      "agent-1",
		WorkflowID:   "retrieve",
		WorkflowName: "retrieve",
		WorkflowType: WorkflowTypeFunction.String(),
	}, 100*time.Millisecond)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))
	points := workflowHistogramPoints(t, rm)
	require.Len(t, points, 1)
	require.Equal(t, uint64(1), points[0].Count)
	require.True(t, workflowAttrSetContains(points[0].Attributes, semconvtrace.KeyGenAIWorkflowID, "retrieve"))
	require.False(t, workflowAttrSetContainsKey(points[0].Attributes, metrics.KeyGenAIWorkflowElapsedFrom))
	require.False(t, workflowAttrSetContainsKey(points[0].Attributes, metrics.KeyGenAIWorkflowElapsedTo))
}

func TestReportWorkflowElapsedMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	originalProvider := MeterProvider
	originalMeter := WorkflowMeter
	originalElapsed := WorkflowMetricGenAIWorkflowElapsedTime
	defer func() {
		MeterProvider = originalProvider
		WorkflowMeter = originalMeter
		WorkflowMetricGenAIWorkflowElapsedTime = originalElapsed
	}()

	MeterProvider = provider
	WorkflowMeter = provider.Meter(metrics.MeterNameWorkflow)
	var err error
	WorkflowMetricGenAIWorkflowElapsedTime, err = histogram.NewDynamicFloat64Histogram(
		provider,
		metrics.MeterNameWorkflow,
		metrics.MetricGenAIWorkflowElapsedTime,
		metric.WithUnit("s"),
	)
	require.NoError(t, err)

	ctx := context.Background()
	ReportWorkflowElapsedMetrics(ctx, WorkflowAttributes{
		AppName:      "test-app",
		UserID:       "user-123",
		AgentID:      "agent-1",
		WorkflowID:   "retrieve",
		WorkflowName: "retrieve",
		WorkflowType: WorkflowTypeFunction.String(),
	}, 250*time.Millisecond)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))
	points := workflowHistogramPointsByName(t, rm, metrics.MetricGenAIWorkflowElapsedTime)
	require.Len(t, points, 1)
	require.Equal(t, uint64(1), points[0].Count)
	require.True(t, workflowAttrSetContains(points[0].Attributes, semconvtrace.KeyGenAIWorkflowID, "retrieve"))
	require.True(t, workflowAttrSetContains(points[0].Attributes, metrics.KeyGenAIWorkflowElapsedFrom, metrics.ValueGenAIWorkflowElapsedFromRootWorkflowStart))
	require.True(t, workflowAttrSetContains(points[0].Attributes, metrics.KeyGenAIWorkflowElapsedTo, metrics.ValueGenAIWorkflowElapsedToCurrentWorkflowEnd))
}

func TestReportWorkflowMetricsNoopWhenHistogramNil(t *testing.T) {
	originalDuration := WorkflowMetricGenAIClientOperationDuration
	defer func() {
		WorkflowMetricGenAIClientOperationDuration = originalDuration
	}()
	WorkflowMetricGenAIClientOperationDuration = nil

	require.NotPanics(t, func() {
		ReportWorkflowMetrics(context.Background(), WorkflowAttributes{}, time.Millisecond)
	})
}

func TestReportWorkflowElapsedMetricsNoopWhenHistogramNil(t *testing.T) {
	originalElapsed := WorkflowMetricGenAIWorkflowElapsedTime
	defer func() {
		WorkflowMetricGenAIWorkflowElapsedTime = originalElapsed
	}()
	WorkflowMetricGenAIWorkflowElapsedTime = nil

	require.NotPanics(t, func() {
		ReportWorkflowElapsedMetrics(context.Background(), WorkflowAttributes{}, time.Millisecond)
	})
}

func requireWorkflowAttr(t *testing.T, attrs []attribute.KeyValue, key string, value string) {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			require.Equal(t, value, attr.Value.AsString())
			return
		}
	}
	t.Fatalf("attribute %s not found", key)
}

func requireNoWorkflowAttr(t *testing.T, attrs []attribute.KeyValue, key string) {
	t.Helper()
	for _, attr := range attrs {
		require.NotEqual(t, key, string(attr.Key))
	}
}

func workflowHistogramPoints(
	t *testing.T,
	rm metricdata.ResourceMetrics,
) []metricdata.HistogramDataPoint[float64] {
	return workflowHistogramPointsByName(t, rm, metrics.MetricGenAIClientOperationDuration)
}

func workflowHistogramPointsByName(
	t *testing.T,
	rm metricdata.ResourceMetrics,
	metricName string,
) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	for _, scopeMetric := range rm.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			if metric.Name != metricName {
				continue
			}
			require.Equal(t, "s", metric.Unit)
			hist, ok := metric.Data.(metricdata.Histogram[float64])
			require.True(t, ok)
			return hist.DataPoints
		}
	}
	t.Fatalf("metric %s not found", metricName)
	return nil
}

func workflowAttrSetContains(set attribute.Set, key string, value string) bool {
	for _, kv := range set.ToSlice() {
		if string(kv.Key) == key && kv.Value.AsString() == value {
			return true
		}
	}
	return false
}

func workflowAttrSetContainsKey(set attribute.Set, key string) bool {
	for _, kv := range set.ToSlice() {
		if string(kv.Key) == key {
			return true
		}
	}
	return false
}
