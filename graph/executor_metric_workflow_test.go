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
	elapsedPts := collectWorkflowElapsedMetricPoints(t, reader)
	require.Len(t, elapsedPts, 1)
	require.Equal(t, uint64(1), elapsedPts[0].Count)
	require.False(t, workflowPointHasAttrKey(elapsedPts[0], semconvtrace.KeyErrorType))
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
	elapsedPts := collectWorkflowElapsedMetricPoints(t, reader)
	require.Len(t, elapsedPts, 1)
	require.Equal(t, uint64(2), elapsedPts[0].Count)
	require.False(t, workflowPointHasAttrKey(elapsedPts[0], MetadataKeyCacheHit))
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
	elapsedPts := collectWorkflowElapsedMetricPoints(t, reader)
	require.Len(t, elapsedPts, 1)
	require.Equal(t, uint64(1), elapsedPts[0].Count)
	require.False(t, workflowPointHasAttrKey(elapsedPts[0], semconvtrace.KeyErrorType))
}

func TestWorkflowElapsedMetricRecordsOnSuccess(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("work", func(ctx context.Context, state State) (any, error) {
		time.Sleep(10 * time.Millisecond)
		return State{"ok": true}, nil
	}).SetEntryPoint("work").SetFinishPoint("work")
	exec := compileExecutorForWorkflowMetric(t, sg)

	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-workflow-elapsed-success"),
		agent.WithInvocationSession(&session.Session{AppName: "test-app", UserID: "user-1"}),
	)
	ch, err := exec.Execute(context.Background(), State{}, inv)
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	nodePts := collectWorkflowMetricPoints(t, reader)
	require.Len(t, nodePts, 1)
	require.False(t, workflowPointHasAttrKey(nodePts[0], semconvmetrics.KeyGenAIWorkflowElapsedFrom))
	require.False(t, workflowPointHasAttrKey(nodePts[0], semconvmetrics.KeyGenAIWorkflowElapsedTo))

	elapsedPts := collectWorkflowElapsedMetricPoints(t, reader)
	require.Len(t, elapsedPts, 1)
	require.Equal(t, uint64(1), elapsedPts[0].Count)
	require.GreaterOrEqual(t, elapsedPts[0].Sum, nodePts[0].Sum)
	require.True(t, workflowPointHasAttr(elapsedPts[0], semconvtrace.KeyGenAIWorkflowID, "work"))
	require.True(t, workflowPointHasAttr(elapsedPts[0], semconvtrace.KeyGenAIAppName, "test-app"))
	require.True(t, workflowPointHasAttr(elapsedPts[0], semconvmetrics.KeyGenAIWorkflowElapsedFrom, semconvmetrics.ValueGenAIWorkflowElapsedFromRootWorkflowStart))
	require.True(t, workflowPointHasAttr(elapsedPts[0], semconvmetrics.KeyGenAIWorkflowElapsedTo, semconvmetrics.ValueGenAIWorkflowElapsedToCurrentWorkflowEnd))
	require.False(t, workflowPointHasAttrKey(elapsedPts[0], semconvtrace.KeyErrorType))
}

func TestWorkflowElapsedMetricRecordsOnFailure(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("fail", func(ctx context.Context, state State) (any, error) {
		return nil, fmt.Errorf("boom")
	}).SetEntryPoint("fail").SetFinishPoint("fail")
	exec := compileExecutorForWorkflowMetric(t, sg)

	ch, err := exec.Execute(context.Background(), State{}, agent.NewInvocation(
		agent.WithInvocationID("inv-workflow-elapsed-failure"),
	))
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	elapsedPts := collectWorkflowElapsedMetricPoints(t, reader)
	require.Len(t, elapsedPts, 1)
	require.Equal(t, uint64(1), elapsedPts[0].Count)
	require.True(t, workflowPointHasAttr(elapsedPts[0], semconvtrace.KeyGenAIWorkflowID, "fail"))
	require.True(t, workflowPointHasAttr(elapsedPts[0], semconvtrace.KeyErrorType, semconvtrace.ValueDefaultErrorType))
}

func TestWorkflowElapsedMetricMultipleNodes(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("step1", func(ctx context.Context, state State) (any, error) {
		time.Sleep(20 * time.Millisecond)
		return State{"step1": true}, nil
	})
	sg.AddNode("step2", func(ctx context.Context, state State) (any, error) {
		time.Sleep(20 * time.Millisecond)
		return State{"step2": true}, nil
	})
	sg.SetEntryPoint("step1")
	sg.AddEdge("step1", "step2")
	sg.SetFinishPoint("step2")
	exec := compileExecutorForWorkflowMetric(t, sg)

	ch, err := exec.Execute(context.Background(), State{}, agent.NewInvocation(
		agent.WithInvocationID("inv-workflow-elapsed-multi"),
	))
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	elapsedPts := collectWorkflowElapsedMetricPoints(t, reader)
	require.Len(t, elapsedPts, 2)

	var step1Elapsed, step2Elapsed float64
	for _, pt := range elapsedPts {
		if workflowPointHasAttr(pt, semconvtrace.KeyGenAIWorkflowID, "step1") {
			step1Elapsed = pt.Sum
		}
		if workflowPointHasAttr(pt, semconvtrace.KeyGenAIWorkflowID, "step2") {
			step2Elapsed = pt.Sum
		}
	}
	require.Positive(t, step1Elapsed)
	require.Positive(t, step2Elapsed)
	require.Greater(t, step2Elapsed, step1Elapsed)
}

func TestWorkflowMetricRecorderBuildsFallbackAttributes(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("unnamed", func(ctx context.Context, state State) (any, error) {
		return State{"ok": true}, nil
	}, WithName("")).SetEntryPoint("unnamed").SetFinishPoint("unnamed")
	exec := compileExecutorForWorkflowMetric(t, sg)
	execCtx := &ExecutionContext{
		State: State{
			StateKeySession: &session.Session{AppName: "state-app", UserID: "state-user"},
		},
	}
	inv := agent.NewInvocation(agent.WithInvocationModel(&MockModel{}))
	inv.AgentName = "agent-name"

	rec := exec.newWorkflowMetricRecorder(inv, execCtx, "unnamed", NodeTypeLLM, time.Now())

	require.Equal(t, "agent-name", rec.attributes.AgentID)
	require.Equal(t, "agent-name", rec.attributes.AgentName)
	require.Equal(t, "mock-model", rec.attributes.System)
	require.Equal(t, "state-app", rec.attributes.AppName)
	require.Equal(t, "state-user", rec.attributes.UserID)
	require.Equal(t, "unnamed", rec.attributes.WorkflowName)
	require.Equal(t, itelemetry.WorkflowTypeLLM.String(), rec.attributes.WorkflowType)

	appName, userID := exec.getSessionIdentity(nil)
	require.Empty(t, appName)
	require.Empty(t, userID)
}

func TestWorkflowMetricConditionalEdgeFailureRecordsError(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("route", func(ctx context.Context, state State) (any, error) {
		return State{"ok": true}, nil
	}).SetEntryPoint("route").SetFinishPoint("route")
	sg.AddConditionalEdges("route", func(ctx context.Context, state State) (string, error) {
		return "", fmt.Errorf("route boom")
	}, map[string]string{End: End})
	exec := compileExecutorForWorkflowMetric(t, sg)

	ch, err := exec.Execute(context.Background(), State{}, agent.NewInvocation(agent.WithInvocationID("inv-workflow-metric-route-error")))
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	point := requireWorkflowErrorPoint(t, collectWorkflowMetricPoints(t, reader))
	require.Equal(t, uint64(1), point.Count)
}

func TestWorkflowMetricBeforeCallbackConditionalEdgeFailureRecordsError(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	var calls int32
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("route", func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&calls, 1)
		return State{"ok": true}, nil
	}).SetEntryPoint("route").SetFinishPoint("route")
	sg.WithNodeCallbacks(NewNodeCallbacks().RegisterBeforeNode(func(ctx context.Context, cb *NodeCallbackContext, state State) (any, error) {
		return State{"from_callback": true}, nil
	}))
	sg.AddConditionalEdges("route", func(ctx context.Context, state State) (string, error) {
		return "", fmt.Errorf("before route boom")
	}, map[string]string{End: End})
	exec := compileExecutorForWorkflowMetric(t, sg)

	ch, err := exec.Execute(context.Background(), State{}, agent.NewInvocation(agent.WithInvocationID("inv-workflow-metric-before-route-error")))
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	require.Equal(t, int32(0), atomic.LoadInt32(&calls))
	point := requireWorkflowErrorPoint(t, collectWorkflowMetricPoints(t, reader))
	require.Equal(t, uint64(1), point.Count)
}

func TestWorkflowMetricRecoveredNodeConditionalEdgeFailureRecordsError(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("recover", func(ctx context.Context, state State) (any, error) {
		return nil, fmt.Errorf("node boom")
	}).SetEntryPoint("recover").SetFinishPoint("recover")
	sg.WithNodeCallbacks(NewNodeCallbacks().RegisterAfterNode(func(
		ctx context.Context,
		cb *NodeCallbackContext,
		state State,
		result any,
		nodeErr error,
	) (any, error) {
		require.Error(t, nodeErr)
		return State{"recovered": true}, nil
	}))
	sg.AddConditionalEdges("recover", func(ctx context.Context, state State) (string, error) {
		return "", fmt.Errorf("recovered route boom")
	}, map[string]string{End: End})
	exec := compileExecutorForWorkflowMetric(t, sg)

	ch, err := exec.Execute(context.Background(), State{}, agent.NewInvocation(agent.WithInvocationID("inv-workflow-metric-recovered-route-error")))
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	point := requireWorkflowErrorPoint(t, collectWorkflowMetricPoints(t, reader))
	require.Equal(t, uint64(1), point.Count)
}

func TestWorkflowMetricAfterCallbackErrorOnFailedNodeRecordsError(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("fail", func(ctx context.Context, state State) (any, error) {
		return nil, fmt.Errorf("node boom")
	}).SetEntryPoint("fail").SetFinishPoint("fail")
	sg.WithNodeCallbacks(NewNodeCallbacks().RegisterAfterNode(func(
		ctx context.Context,
		cb *NodeCallbackContext,
		state State,
		result any,
		nodeErr error,
	) (any, error) {
		require.Error(t, nodeErr)
		return nil, fmt.Errorf("after boom")
	}))
	exec := compileExecutorForWorkflowMetric(t, sg)

	ch, err := exec.Execute(context.Background(), State{}, agent.NewInvocation(agent.WithInvocationID("inv-workflow-metric-after-error")))
	require.NoError(t, err)
	drainWorkflowEvents(ch)

	point := requireWorkflowErrorPoint(t, collectWorkflowMetricPoints(t, reader))
	require.Equal(t, uint64(1), point.Count)
}

func TestWorkflowMetricCacheHitConditionalEdgeFailureRecordsError(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	var calls int32
	var routeCalls int32
	sg := NewStateGraph(NewStateSchema()).
		WithCache(NewInMemoryCache()).
		WithCachePolicy(DefaultCachePolicy())
	sg.AddNode("cached", func(ctx context.Context, state State) (any, error) {
		atomic.AddInt32(&calls, 1)
		return State{"ok": true}, nil
	}).SetEntryPoint("cached").SetFinishPoint("cached")
	sg.AddConditionalEdges("cached", func(ctx context.Context, state State) (string, error) {
		if atomic.AddInt32(&routeCalls, 1) == 1 {
			return End, nil
		}
		return "", fmt.Errorf("cache route boom")
	}, map[string]string{End: End})
	exec := compileExecutorForWorkflowMetric(t, sg)

	for i := 0; i < 2; i++ {
		ch, err := exec.Execute(context.Background(), State{}, agent.NewInvocation(agent.WithInvocationID(fmt.Sprintf("inv-workflow-metric-cache-route-%d", i))))
		require.NoError(t, err)
		drainWorkflowEvents(ch)
	}

	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
	point := requireWorkflowErrorPoint(t, collectWorkflowMetricPoints(t, reader))
	require.Equal(t, uint64(1), point.Count)
}

func TestWorkflowMetricRetryCancellationBeforeRetryRecordsError(t *testing.T) {
	reader, cleanup := setupWorkflowMetricReader(t)
	defer cleanup()

	exec := &Executor{}
	inv := agent.NewInvocation(agent.WithInvocationID("inv-workflow-metric-retry-cancel"))
	execCtx := &ExecutionContext{
		EventChan:    make(chan *event.Event, 4),
		InvocationID: inv.InvocationID,
	}
	nodeCtx := &nodeExecutionContext{
		nodeType: NodeTypeFunction,
		metricRecorder: &workflowMetricRecorder{
			start: time.Now(),
			attributes: itelemetry.WorkflowAttributes{
				WorkflowID:   "retry",
				WorkflowName: "retry",
				WorkflowType: itelemetry.WorkflowTypeFunction.String(),
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	shouldRetry, err := exec.waitBeforeRetry(
		ctx,
		inv,
		execCtx,
		"retry",
		1,
		nodeCtx,
		&retryContext{attempt: 1, err: fmt.Errorf("temporary boom")},
		RetryPolicy{InitialInterval: time.Hour, BackoffFactor: 1},
		2,
	)

	require.False(t, shouldRetry)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	point := requireWorkflowErrorPoint(t, collectWorkflowMetricPoints(t, reader))
	require.Equal(t, uint64(1), point.Count)
}

func setupWorkflowMetricReader(t *testing.T) (*sdkmetric.ManualReader, func()) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.WorkflowMeter
	originalDuration := itelemetry.WorkflowMetricGenAIClientOperationDuration
	originalElapsed := itelemetry.WorkflowMetricGenAIWorkflowElapsedTime

	itelemetry.MeterProvider = provider
	itelemetry.WorkflowMeter = provider.Meter(semconvmetrics.MeterNameWorkflow)
	var err error
	itelemetry.WorkflowMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		provider,
		semconvmetrics.MeterNameWorkflow,
		semconvmetrics.MetricGenAIClientOperationDuration,
	)
	require.NoError(t, err)
	itelemetry.WorkflowMetricGenAIWorkflowElapsedTime, err = histogram.NewDynamicFloat64Histogram(
		provider,
		semconvmetrics.MeterNameWorkflow,
		semconvmetrics.MetricGenAIWorkflowElapsedTime,
	)
	require.NoError(t, err)

	return reader, func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.WorkflowMeter = originalMeter
		itelemetry.WorkflowMetricGenAIClientOperationDuration = originalDuration
		itelemetry.WorkflowMetricGenAIWorkflowElapsedTime = originalElapsed
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
	return collectWorkflowMetricPointsByName(t, reader, semconvmetrics.MetricGenAIClientOperationDuration)
}

func collectWorkflowElapsedMetricPoints(
	t *testing.T,
	reader *sdkmetric.ManualReader,
) []metricdata.HistogramDataPoint[float64] {
	return collectWorkflowMetricPointsByName(t, reader, semconvmetrics.MetricGenAIWorkflowElapsedTime)
}

func collectWorkflowMetricPointsByName(
	t *testing.T,
	reader *sdkmetric.ManualReader,
	metricName string,
) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	for _, scopeMetric := range rm.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			if metric.Name != metricName {
				continue
			}
			hist, ok := metric.Data.(metricdata.Histogram[float64])
			require.True(t, ok)
			return hist.DataPoints
		}
	}
	t.Fatalf("metric %s not found", metricName)
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

func requireWorkflowErrorPoint(
	t *testing.T,
	points []metricdata.HistogramDataPoint[float64],
) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	for _, point := range points {
		if workflowPointHasAttr(point, semconvtrace.KeyErrorType, semconvtrace.ValueDefaultErrorType) {
			return point
		}
	}
	t.Fatalf("workflow error point not found")
	return metricdata.HistogramDataPoint[float64]{}
}
