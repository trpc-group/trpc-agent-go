//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	semconvmetrics "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestWorkflowMetricRecordsSuccessWhenExecutorEventsDisabled(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("work", func(ctx context.Context, state State) (any, error) {
		return State{"ok": true}, nil
	}).SetEntryPoint("work").SetFinishPoint("work")
	exec := compileExecutorForWorkflowMetric(t, sg)

	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-workflow-metric-success"),
		agent.WithInvocationSession(&session.Session{AppName: "test-app", UserID: "user-123"}),
		agent.WithInvocationRunOptions(agent.NewRunOptions(agent.WithDisableGraphExecutorEvents(true))),
	)
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	points := collectWorkflowMetricPoints(t, reader)
	require.Len(t, points, 1)
	require.Equal(t, uint64(1), points[0].Count)
	require.True(t, workflowPointHasAttr(points[0], semconvtrace.KeyGenAIAppName, "test-app"))
	require.True(t, workflowPointHasAttr(points[0], semconvtrace.KeyGenAIUserID, "user-123"))
	require.True(t, workflowPointHasAttr(points[0], semconvtrace.KeyGenAIWorkflowID, "work"))
	require.False(t, workflowPointHasAttrKey(points[0], semconvtrace.KeyErrorType))
}

func TestWorkflowMetricRecordsFinalFailure(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("fail", func(ctx context.Context, state State) (any, error) {
		return nil, fmt.Errorf("boom")
	}).SetEntryPoint("fail").SetFinishPoint("fail")
	exec := compileExecutorForWorkflowMetric(t, sg)

	ch, err := exec.Execute(context.Background(), State{}, agent.NewInvocation(agent.WithInvocationID("inv-workflow-metric-failure")))
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	points := collectWorkflowMetricPoints(t, reader)
	require.Len(t, points, 1)
	require.Equal(t, uint64(1), points[0].Count)
	require.True(t, workflowPointHasAttr(points[0], semconvtrace.KeyGenAIWorkflowID, "fail"))
	require.True(t, workflowPointHasAttr(points[0], semconvtrace.KeyErrorType, semconvtrace.ValueDefaultErrorType))
}

func TestWorkflowMetricRetryRecordsOnce(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	var attempts int32
	policy := RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: time.Nanosecond,
		BackoffFactor:   1,
		MaxInterval:     time.Nanosecond,
		Jitter:          false,
		RetryOn:         []RetryCondition{RetryConditionFunc(func(error) bool { return true })},
	}
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("unstable", func(ctx context.Context, state State) (any, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			return nil, fmt.Errorf("temporary failure")
		}
		return State{"ok": true}, nil
	}, WithRetryPolicy(policy)).SetEntryPoint("unstable").SetFinishPoint("unstable")
	exec := compileExecutorForWorkflowMetric(t, sg)

	ch, err := exec.Execute(context.Background(), State{}, agent.NewInvocation(agent.WithInvocationID("inv-workflow-metric-retry")))
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	require.Equal(t, int32(3), atomic.LoadInt32(&attempts))
	points := collectWorkflowMetricPoints(t, reader)
	require.Len(t, points, 1)
	require.Equal(t, uint64(1), points[0].Count)
	require.False(t, workflowPointHasAttrKey(points[0], semconvtrace.KeyErrorType))
}

func TestWorkflowMetricCacheHitRecordsWithoutCacheDimension(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	var calls int32
	schema := NewStateSchema().
		AddField("n", StateField{Type: reflect.TypeOf(0), Reducer: DefaultReducer}).
		AddField("out", StateField{Type: reflect.TypeOf(0), Reducer: DefaultReducer})
	sg := NewStateGraph(schema).
		WithCache(NewInMemoryCache()).
		WithCachePolicy(DefaultCachePolicy())
	sg.AddNode("cached", func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&calls, 1)
		return State{"out": state["n"].(int) + 1}, nil
	}).SetEntryPoint("cached").SetFinishPoint("cached")
	exec := compileExecutorForWorkflowMetric(t, sg)

	for i := 0; i < 2; i++ {
		ch, err := exec.Execute(context.Background(), State{"n": 1}, agent.NewInvocation(agent.WithInvocationID(fmt.Sprintf("inv-workflow-metric-cache-%d", i))))
		require.NoError(t, err)
		drainWorkflowEvents(ch)
	}

	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
	points := collectWorkflowMetricPoints(t, reader)
	require.Len(t, points, 1)
	require.Equal(t, uint64(2), points[0].Count)
	require.False(t, workflowPointHasAttrKey(points[0], MetadataKeyCacheHit))
}

func TestWorkflowMetricBeforeCallbackCustomResultRecordsSuccess(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	var calls int32
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("short", func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&calls, 1)
		return nil, fmt.Errorf("should not run")
	}).SetEntryPoint("short").SetFinishPoint("short")
	sg.WithNodeCallbacks(NewNodeCallbacks().RegisterBeforeNode(func(ctx context.Context, cb *NodeCallbackContext, state State) (any, error) {
		return State{"ok": true}, nil
	}))
	exec := compileExecutorForWorkflowMetric(t, sg)

	ch, err := exec.Execute(context.Background(), State{}, agent.NewInvocation(agent.WithInvocationID("inv-workflow-metric-before")))
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	require.Equal(t, int32(0), atomic.LoadInt32(&calls))
	points := collectWorkflowMetricPoints(t, reader)
	require.Len(t, points, 1)
	require.Equal(t, uint64(1), points[0].Count)
	require.False(t, workflowPointHasAttrKey(points[0], semconvtrace.KeyErrorType))
}

func setupWorkflowMetricReader(t *testing.T) (*sdkmetric.ManualReader, func()) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.WorkflowMeter
	originalDuration := itelemetry.WorkflowMetricGenAIClientOperationDuration

	itelemetry.MeterProvider = provider
	itelemetry.WorkflowMeter = provider.Meter(semconvmetrics.MeterNameWorkflow)
	var err error
	itelemetry.WorkflowMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		provider,
		semconvmetrics.MeterNameWorkflow,
		semconvmetrics.MetricGenAIClientOperationDuration,
	)
	require.NoError(t, err)

	return reader, func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.WorkflowMeter = originalMeter
		itelemetry.WorkflowMetricGenAIClientOperationDuration = originalDuration
	}
}

func compileExecutorForWorkflowMetric(t *testing.T, sg *StateGraph) *Executor {
	t.Helper()
	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)
	return exec
}

func drainWorkflowEvents(ch <-chan *event.Event) {
	for range ch {
	}
}

func collectWorkflowMetricPoints(
	t *testing.T,
	reader *sdkmetric.ManualReader,
) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	for _, scopeMetric := range rm.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			if metric.Name != semconvmetrics.MetricGenAIClientOperationDuration {
				continue
			}
			hist, ok := metric.Data.(metricdata.Histogram[float64])
			require.True(t, ok)
			return hist.DataPoints
		}
	}
	t.Fatalf("metric %s not found", semconvmetrics.MetricGenAIClientOperationDuration)
	return nil
}

func workflowPointHasAttr(
	point metricdata.HistogramDataPoint[float64],
	key string,
	value string,
) bool {
	for _, attr := range point.Attributes.ToSlice() {
		if string(attr.Key) == key && attr.Value.AsString() == value {
			return true
		}
	}
	return false
}

func workflowPointHasAttrKey(point metricdata.HistogramDataPoint[float64], key string) bool {
	for _, attr := range point.Attributes.ToSlice() {
		if string(attr.Key) == key {
			return true
		}
	}
	return false
}
