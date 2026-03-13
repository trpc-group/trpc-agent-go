//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	semconvmetrics "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Additional unit tests for long-running tool tracking and preprocess

// mockLongRunnerTool implements tool.Tool and a LongRunning() flag.
type mockLongRunnerTool struct {
	name string
	long bool
}

func (m *mockLongRunnerTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: m.name} }
func (m *mockLongRunnerTool) LongRunning() bool              { return m.long }

func useSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := trace.TracerProvider
	originalTracer := trace.Tracer
	trace.TracerProvider = provider
	trace.Tracer = provider.Tracer("llm-flow-disable-tracing-test")
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		trace.TracerProvider = originalProvider
		trace.Tracer = originalTracer
	})
	return recorder
}

func TestCollectLongRunningToolIDs(t *testing.T) {
	calls := []model.ToolCall{
		{ID: "1", Function: model.FunctionDefinitionParam{Name: "fast"}},
		{ID: "2", Function: model.FunctionDefinitionParam{Name: "slow"}},
		{ID: "3", Function: model.FunctionDefinitionParam{Name: "unknown"}},
		{ID: "4", Function: model.FunctionDefinitionParam{Name: "nolong"}},
	}
	tools := map[string]tool.Tool{
		"fast":   &mockLongRunnerTool{name: "fast", long: false},
		"slow":   &mockLongRunnerTool{name: "slow", long: true},
		"nolong": &mockLongRunnerTool{name: "nolong", long: false},
		// unknown not present
	}
	got := collectLongRunningToolIDs(calls, tools)
	require.Contains(t, got, "2")
	require.Len(t, got, 1)
}

// minimalAgent exposes tools for preprocess test.
type minimalAgent struct{ tools []tool.Tool }

func (m *minimalAgent) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}
func (m *minimalAgent) Tools() []tool.Tool              { return m.tools }
func (m *minimalAgent) Info() agent.Info                { return agent.Info{Name: "a"} }
func (m *minimalAgent) SubAgents() []agent.Agent        { return nil }
func (m *minimalAgent) FindSubAgent(string) agent.Agent { return nil }

// flowRecordingSpan captures trace attributes for assertions.
type flowRecordingSpan struct {
	oteltrace.Span
	attrs []attribute.KeyValue
}

func newFlowRecordingSpan() *flowRecordingSpan {
	_, span := oteltrace.NewNoopTracerProvider().Tracer("test").Start(context.Background(), "llmflow-test")
	return &flowRecordingSpan{Span: span}
}

func (s *flowRecordingSpan) IsRecording() bool {
	return true
}

func (s *flowRecordingSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.attrs = append(s.attrs, kv...)
	s.Span.SetAttributes(kv...)
}

func flowHasAttr(attrs []attribute.KeyValue, key string, want any) bool {
	for _, kv := range attrs {
		if string(kv.Key) == key && kv.Value.AsInterface() == want {
			return true
		}
	}
	return false
}

func TestPreprocess_AddsAgentToolsWhenPresent(t *testing.T) {
	f := New(nil, nil, Options{})
	req := &model.Request{Tools: map[string]tool.Tool{}}
	inv := agent.NewInvocation()
	inv.Agent = &minimalAgent{tools: []tool.Tool{&mockLongRunnerTool{name: "t1"}}}
	ch := make(chan *event.Event, 4)
	f.preprocess(context.Background(), inv, req, ch)
	require.Contains(t, req.Tools, "t1")
}

func TestPreprocess_DowngradesOrphanToolCallBeforeModel(t *testing.T) {
	modelStub := &mockModel{
		responses: []*model.Response{
			{
				Choices: []model.Choice{{
					Message: model.Message{Role: model.RoleAssistant, Content: "ok"},
				}},
			},
		},
	}
	f := New(
		[]flow.RequestProcessor{
			&seedMessagesRequestProcessor{
				messages: []model.Message{
					model.NewUserMessage("read file"),
					{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								ID: "call_orphan",
								Function: model.FunctionDefinitionParam{
									Name:      "test_tool",
									Arguments: []byte(`{"path":"a.txt"}`),
								},
							},
						},
					},
					model.NewUserMessage("retry"),
				},
			},
		},
		nil,
		Options{},
	)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(&minimalAgent{tools: []tool.Tool{
			&mockLongRunnerTool{name: "test_tool"},
		}}),
		agent.WithInvocationModel(modelStub),
	)
	req := &model.Request{Tools: map[string]tool.Tool{}}
	ch := make(chan *event.Event, 4)

	f.preprocess(context.Background(), inv, req, ch)
	_, seq, err := f.callLLM(context.Background(), inv, req)
	require.NoError(t, err)
	seq(func(resp *model.Response) bool { return false })

	captured := modelStub.LastRequest()
	require.NotNil(t, captured)
	require.Len(t, captured.Messages, 3)
	require.Equal(t, model.RoleUser, captured.Messages[0].Role)
	require.Equal(t, "read file", captured.Messages[0].Content)
	require.Equal(t, model.RoleUser, captured.Messages[1].Role)
	require.Contains(t, captured.Messages[1].Content, "[orphan_tool_call]")
	require.Empty(t, captured.Messages[1].ToolCalls)
	require.Equal(t, model.RoleUser, captured.Messages[2].Role)
	require.Equal(t, "retry", captured.Messages[2].Content)
}

func TestCreateLLMResponseEvent_LongRunningIDs(t *testing.T) {
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation()
	req := &model.Request{Tools: map[string]tool.Tool{
		"slow": &mockLongRunnerTool{name: "slow", long: true},
	}}
	rsp := &model.Response{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{ID: "x", Function: model.FunctionDefinitionParam{Name: "slow"}}}}}}}
	evt := f.createLLMResponseEvent(inv, inv, rsp, req)
	require.Contains(t, evt.LongRunningToolIDs, "x")
}

// TestProcessStreamingResponses_RepairsToolCallArgumentsWhenEnabled verifies tool call arguments are repaired when enabled.
func TestProcessStreamingResponses_RepairsToolCallArgumentsWhenEnabled(t *testing.T) {
	f := New(nil, nil, Options{})
	repairEnabled := true
	inv := agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{
		ToolCallArgumentsJSONRepairEnabled: &repairEnabled,
	}))
	req := &model.Request{}
	response := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					ToolCalls: []model.ToolCall{
						{
							ID:   "call-1",
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      "tool",
								Arguments: []byte("{a:2}"),
							},
						},
					},
				},
			},
		},
	}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(response)
	}

	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(context.Background(), "s")
	defer span.End()

	lastEvent, err := f.processStreamingResponses(ctx, inv, req, responseSeq, eventChan, span, true)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Equal(t, "{\"a\":2}", string(response.Choices[0].Message.ToolCalls[0].Function.Arguments))
}

func TestRunOneStep_DisableTracingSkipsSpanCreation(t *testing.T) {
	recorder := useSpanRecorder(t)
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-disable-tracing"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableTracing: true,
		}),
	)
	inv.AgentName = "test-agent"
	inv.Model = &mockModel{
		responses: []*model.Response{
			{
				Model: "mock",
				Choices: []model.Choice{
					{Message: model.NewAssistantMessage("ok")},
				},
			},
		},
	}
	eventChan := make(chan *event.Event, 4)
	_, err := f.runOneStep(context.Background(), inv, eventChan)
	require.NoError(t, err)
	require.Empty(t, recorder.Ended())
}

func TestProcessStreamingResponses_UsesInvocationFromContextForResponseOptions(t *testing.T) {
	f := New(nil, nil, Options{})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base"),
	)
	baseInvocation.AgentName = "base-agent"
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking:  true,
			DisablePartialEventIDs:        true,
			DisablePartialEventTimestamps: true,
		}),
		agent.WithInvocationSession(&session.Session{
			ID: "sess-updated",
		}),
	)
	updatedInvocation.AgentName = "updated-agent"
	req := &model.Request{}
	respTimestamp := time.Unix(1, 0).UTC()
	response := &model.Response{
		IsPartial: true,
		Timestamp: respTimestamp,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("partial")},
		},
	}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(response)
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), updatedInvocation),
		"s",
	)
	defer span.End()

	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Nil(t, response.Usage)
	require.Empty(t, lastEvent.ID)
	require.Equal(t, respTimestamp, lastEvent.Timestamp)
	require.Equal(t, baseInvocation.InvocationID, lastEvent.InvocationID)
	require.Equal(t, baseInvocation.AgentName, lastEvent.Author)
}

func TestProcessStreamingResponses_DisableResponseUsageTrackingStillRecordsMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	f := New(nil, nil, Options{})
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-disable-usage-metrics"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	invocation.AgentName = "agent-disable-usage-metrics"
	req := &model.Request{}
	response := &model.Response{
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("done")},
		},
	}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(response)
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), invocation),
		"s",
	)
	defer span.End()
	lastEvent, err := f.processStreamingResponses(
		ctx,
		invocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Nil(t, response.Usage)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.NotEmpty(t, rm.ScopeMetrics)
}

func TestProcessStreamingResponses_UsesStableInvocationForMetricsMetadata(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	f := New(nil, nil, Options{})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-metrics"),
		agent.WithInvocationModel(&mockModel{}),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-base-metrics",
			UserID:  "user-base-metrics",
			AppName: "app-base-metrics",
		}),
	)
	baseInvocation.AgentName = "agent-base-metrics"
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-metrics"),
		agent.WithInvocationModel(&mockIterModel{}),
		agent.WithInvocationSession(&session.Session{
			ID: "sess-updated-metrics",
		}),
	)
	updatedInvocation.AgentName = "agent-updated-metrics"
	req := &model.Request{}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(&model.Response{
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("done")},
			},
		})
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), updatedInvocation),
		"s",
	)
	defer span.End()
	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIAgentName, baseInvocation.AgentName))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, baseInvocation.Model.Info().Name))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIConversationID, baseInvocation.Session.ID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoUserID, baseInvocation.Session.UserID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoAppName, baseInvocation.Session.AppName))
	require.False(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIAgentName, updatedInvocation.AgentName))
	require.False(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, updatedInvocation.Model.Info().Name))
	require.False(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIConversationID, updatedInvocation.Session.ID))
}

func TestProcessStreamingResponses_UsesUpdatedInvocationForMetricsMetadataWhenBaseIsSparse(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	f := New(nil, nil, Options{})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-sparse-metrics"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	updatedModel := &mockModel{}
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-full-metrics"),
		agent.WithInvocationModel(updatedModel),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-updated-full-metrics",
			UserID:  "user-updated-full-metrics",
			AppName: "app-updated-full-metrics",
		}),
	)
	updatedInvocation.AgentName = "agent-updated-full-metrics"
	req := &model.Request{}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(&model.Response{
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("done")},
			},
		})
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), updatedInvocation),
		"s",
	)
	defer span.End()
	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIAgentName, updatedInvocation.AgentName))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, updatedModel.Info().Name))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIConversationID, updatedInvocation.Session.ID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoUserID, updatedInvocation.Session.UserID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoAppName, updatedInvocation.Session.AppName))
}

func TestProcessStreamingResponses_UsesUpdatedInvocationForMetricsMetadataAfterCallbackOnSingleChunk(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := itelemetry.ChatMetricGenAIClientTokenUsage
	originalOperationDuration := itelemetry.ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := itelemetry.ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		itelemetry.ChatMetricGenAIClientTokenUsage = originalTokenUsage
		itelemetry.ChatMetricGenAIClientOperationDuration = originalOperationDuration
		itelemetry.ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	itelemetry.ChatMetricGenAIClientTokenUsage = nil
	itelemetry.ChatMetricGenAIClientOperationDuration = nil
	itelemetry.ChatMetricGenAIServerTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil
	updatedModel := &mockModel{}
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-after-metrics"),
		agent.WithInvocationModel(updatedModel),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-updated-after-metrics",
			UserID:  "user-updated-after-metrics",
			AppName: "app-updated-after-metrics",
		}),
	)
	updatedInvocation.AgentName = "agent-updated-after-metrics"
	f := New(nil, nil, Options{
		ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				return &model.AfterModelResult{
					Context: agent.NewInvocationContext(ctx, updatedInvocation),
				}, nil
			},
		),
	})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-after-metrics"),
	)
	req := &model.Request{}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(&model.Response{
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("done")},
			},
		})
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		"s",
	)
	defer span.End()
	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIAgentName, updatedInvocation.AgentName))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIRequestModel, updatedModel.Info().Name))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyGenAIConversationID, updatedInvocation.Session.ID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoUserID, updatedInvocation.Session.UserID))
	require.True(t, resourceMetricsContainAttribute(rm, semconvtrace.KeyTRPCAgentGoAppName, updatedInvocation.Session.AppName))
}

func TestProcessStreamingResponses_UsesUpdatedInvocationForResponseUsageTiming(t *testing.T) {
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-usage"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	var callbackCount int
	f := New(nil, nil, Options{
		ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				callbackCount++
				if callbackCount == 2 {
					return &model.AfterModelResult{
						Context: agent.NewInvocationContext(ctx, updatedInvocation),
					}, nil
				}
				return &model.AfterModelResult{}, nil
			},
		),
	})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-usage"),
	)
	req := &model.Request{}
	response1 := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("partial")},
		},
	}
	response2 := &model.Response{
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("done")},
		},
	}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(response1)
		yield(response2)
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		"s",
	)
	defer span.End()
	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.NotNil(t, response1.Usage)
	require.Nil(t, response2.Usage)
}

func TestProcessStreamingResponses_UsesUpdatedInvocationForResponseUsageTimingOnSingleChunk(t *testing.T) {
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-single-usage"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	f := New(nil, nil, Options{
		ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				return &model.AfterModelResult{
					Context: agent.NewInvocationContext(ctx, updatedInvocation),
				}, nil
			},
		),
	})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-single-usage"),
	)
	req := &model.Request{}
	response := &model.Response{
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("done")},
		},
	}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(response)
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		"s",
	)
	defer span.End()
	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Nil(t, response.Usage)
}

func TestProcessStreamingResponses_PreservesTimingInfoWhenInvocationChanges(t *testing.T) {
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-usage"),
	)
	var callbackCount int
	f := New(nil, nil, Options{
		ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				callbackCount++
				if callbackCount == 2 {
					return &model.AfterModelResult{
						Context: agent.NewInvocationContext(ctx, updatedInvocation),
					}, nil
				}
				return &model.AfterModelResult{}, nil
			},
		),
	})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-usage"),
	)
	req := &model.Request{}
	response1 := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("partial")},
		},
	}
	response2 := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("partial-updated")},
		},
	}
	response3 := &model.Response{
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("done")},
		},
	}
	responseSeq := func(yield func(*model.Response) bool) {
		time.Sleep(time.Millisecond)
		yield(response1)
		time.Sleep(time.Millisecond)
		yield(response2)
		yield(response3)
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		"s",
	)
	defer span.End()
	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.NotNil(t, response1.Usage)
	require.NotNil(t, response2.Usage)
	require.NotSame(t, response1.Usage, response2.Usage)
	require.Same(t, baseInvocation.GetOrCreateTimingInfo(), response1.Usage.TimingInfo)
	require.Same(t, updatedInvocation.GetOrCreateTimingInfo(), response2.Usage.TimingInfo)
	require.NotZero(t, response2.Usage.TimingInfo.FirstTokenDuration)
	require.Equal(
		t,
		response1.Usage.TimingInfo.FirstTokenDuration,
		response2.Usage.TimingInfo.FirstTokenDuration,
	)
}

func TestProcessStreamingResponses_PreservesReasoningTimingWhenInvocationChanges(t *testing.T) {
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-reasoning"),
	)
	var callbackCount int
	f := New(nil, nil, Options{
		ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				callbackCount++
				if callbackCount == 1 {
					return &model.AfterModelResult{
						Context: agent.NewInvocationContext(ctx, updatedInvocation),
					}, nil
				}
				return &model.AfterModelResult{}, nil
			},
		),
	})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-reasoning"),
	)
	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}
	response1 := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Delta: model.Message{ReasoningContent: "thinking"}},
		},
	}
	response2 := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Delta: model.Message{ReasoningContent: "thinking-more"}},
		},
	}
	response3 := &model.Response{
		Choices: []model.Choice{
			{Delta: model.Message{Content: "done"}},
		},
	}
	responseSeq := func(yield func(*model.Response) bool) {
		time.Sleep(time.Millisecond)
		yield(response1)
		time.Sleep(time.Millisecond)
		yield(response2)
		yield(response3)
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		"s",
	)
	defer span.End()
	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Zero(t, baseInvocation.GetOrCreateTimingInfo().ReasoningDuration)
	require.Greater(t, updatedInvocation.GetOrCreateTimingInfo().ReasoningDuration, time.Duration(0))
}

func TestProcessStreamingResponses_PreservesReasoningTimingWhenTrackingDisabledMidStream(t *testing.T) {
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-disable-reasoning"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	var callbackCount int
	f := New(nil, nil, Options{
		ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				callbackCount++
				if callbackCount == 1 {
					return &model.AfterModelResult{
						Context: agent.NewInvocationContext(ctx, updatedInvocation),
					}, nil
				}
				return &model.AfterModelResult{}, nil
			},
		),
	})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-disable-reasoning"),
	)
	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}
	response1 := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Delta: model.Message{ReasoningContent: "thinking"}},
		},
	}
	response2 := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Delta: model.Message{ReasoningContent: "thinking-more"}},
		},
	}
	response3 := &model.Response{
		Choices: []model.Choice{
			{Delta: model.Message{Content: "done"}},
		},
	}
	responseSeq := func(yield func(*model.Response) bool) {
		time.Sleep(time.Millisecond)
		yield(response1)
		time.Sleep(time.Millisecond)
		yield(response2)
		yield(response3)
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		"s",
	)
	defer span.End()
	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Greater(t, baseInvocation.GetOrCreateTimingInfo().ReasoningDuration, time.Duration(0))
	require.Nil(t, response2.Usage)
	require.Nil(t, response3.Usage)
}

func TestProcessStreamingResponses_PreservesOriginalInvocationEventMetadata(t *testing.T) {
	f := New(nil, nil, Options{
		ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				updatedInvocation := agent.NewInvocation(
					agent.WithInvocationID("inv-updated-event"),
					agent.WithInvocationRunOptions(agent.RunOptions{
						DisablePartialEventIDs:        true,
						DisablePartialEventTimestamps: true,
					}),
				)
				return &model.AfterModelResult{
					Context: agent.NewInvocationContext(ctx, updatedInvocation),
				}, nil
			},
		),
	})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-event"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-base",
		}),
		agent.WithInvocationEventFilterKey("filter-base"),
	)
	baseInvocation.AgentName = "base-agent"
	req := &model.Request{}
	response := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("partial")},
		},
	}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(response)
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		"s",
	)
	defer span.End()

	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.Equal(t, baseInvocation.InvocationID, lastEvent.InvocationID)
	require.Equal(t, baseInvocation.AgentName, lastEvent.Author)
	require.Equal(t, baseInvocation.RunOptions.RequestID, lastEvent.RequestID)
	require.Equal(t, baseInvocation.GetEventFilterKey(), lastEvent.FilterKey)
	require.Empty(t, lastEvent.ID)
	require.True(t, lastEvent.Timestamp.IsZero())
}

func TestProcessStreamingResponses_PostprocessUsesOriginalInvocation(t *testing.T) {
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-postprocess"),
	)
	f := New(nil, []flow.ResponseProcessor{&endInvocationResponseProcessor{}}, Options{
		ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				return &model.AfterModelResult{
					Context: agent.NewInvocationContext(ctx, updatedInvocation),
				}, nil
			},
		),
	})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-postprocess"),
	)
	req := &model.Request{}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(&model.Response{
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("done")},
			},
		})
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		"s",
	)
	defer span.End()

	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.True(t, baseInvocation.EndInvocation)
	require.False(t, updatedInvocation.EndInvocation)
}

func TestProcessStreamingResponses_AfterModelErrorKeepsOriginalInvocationEventMetadata(t *testing.T) {
	var callbackCount int
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-error"),
	)
	f := New(nil, nil, Options{
		ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				callbackCount++
				if callbackCount == 1 {
					return &model.AfterModelResult{
						Context: agent.NewInvocationContext(ctx, updatedInvocation),
					}, nil
				}
				return nil, errors.New("after-model boom")
			},
		),
	})
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-error"),
		agent.WithInvocationEventFilterKey("flow/base"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req-base-error",
		}),
	)
	baseInvocation.AgentName = "base-agent"
	req := &model.Request{}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(&model.Response{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("partial")},
			},
		})
		yield(&model.Response{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("partial-2")},
			},
		})
	}
	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	ctx, span := tracer.Start(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		"s",
	)
	defer span.End()

	lastEvent, err := f.processStreamingResponses(
		ctx,
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.Error(t, err)
	require.Nil(t, lastEvent)
	var errorEvent *event.Event
	for len(eventChan) > 0 {
		evt := <-eventChan
		if evt != nil && evt.Error != nil {
			errorEvent = evt
			break
		}
	}
	require.NotNil(t, errorEvent)
	require.Equal(t, baseInvocation.InvocationID, errorEvent.InvocationID)
	require.Equal(t, baseInvocation.AgentName, errorEvent.Author)
	require.Equal(t, baseInvocation.RunOptions.RequestID, errorEvent.RequestID)
	require.Equal(t, baseInvocation.GetEventFilterKey(), errorEvent.FilterKey)
	require.Equal(t, model.ErrorTypeFlowError, errorEvent.Error.Type)
}

func TestProcessStreamingResponses_UsesStableInvocationForTraceMetadata(t *testing.T) {
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated-trace"),
	)
	f := New(nil, nil, Options{
		ModelCallbacks: model.NewCallbacks().RegisterAfterModel(
			func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				return &model.AfterModelResult{
					Context: agent.NewInvocationContext(ctx, updatedInvocation),
				}, nil
			},
		),
	})
	modelImpl := &mockModel{}
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base-trace"),
		agent.WithInvocationModel(modelImpl),
		agent.WithInvocationSession(&session.Session{
			ID:     "sess-base-trace",
			UserID: "user-base-trace",
		}),
	)
	req := &model.Request{}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(&model.Response{
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("done")},
			},
		})
	}
	eventChan := make(chan *event.Event, 10)
	span := newFlowRecordingSpan()
	lastEvent, err := f.processStreamingResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		baseInvocation,
		req,
		responseSeq,
		eventChan,
		span,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, lastEvent)
	require.True(t, flowHasAttr(span.attrs, semconvtrace.KeyInvocationID, baseInvocation.InvocationID))
	require.True(t, flowHasAttr(span.attrs, semconvtrace.KeyGenAIConversationID, baseInvocation.Session.ID))
	require.True(t, flowHasAttr(span.attrs, semconvtrace.KeyRunnerUserID, baseInvocation.Session.UserID))
	require.True(t, flowHasAttr(span.attrs, semconvtrace.KeyGenAIRequestModel, modelImpl.Info().Name))
}

// mockAgent implements agent.Agent for testing
type mockAgent struct {
	name  string
	tools []tool.CallableTool
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Simple mock implementation
	eventChan := make(chan *event.Event, 1)
	defer close(eventChan)
	return eventChan, nil
}

func (m *mockAgent) Tools() []tool.CallableTool {
	return m.tools
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent for testing",
	}
}

func (m *mockAgent) SubAgents() []agent.Agent {
	return nil
}

func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

// mockAgentWithTools implements agent.Agent with tool.Tool support
type mockAgentWithTools struct {
	name  string
	tools []tool.Tool
}

func (m *mockAgentWithTools) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, 1)
	defer close(eventChan)
	return eventChan, nil
}

func (m *mockAgentWithTools) Tools() []tool.Tool {
	return m.tools
}

func (m *mockAgentWithTools) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: "Mock agent with tools for testing",
	}
}

func (m *mockAgentWithTools) SubAgents() []agent.Agent {
	return nil
}

func (m *mockAgentWithTools) FindSubAgent(name string) agent.Agent {
	return nil
}

// mockModel implements model.Model for testing
type mockModel struct {
	ShouldError bool
	responses   []*model.Response
	currentIdx  int
	mu          sync.Mutex
	requests    []*model.Request
}

func (m *mockModel) Info() model.Info {
	return model.Info{
		Name: "mock",
	}
}

func (m *mockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if m.ShouldError {
		return nil, errors.New("mock model error")
	}
	m.recordRequest(req)

	respChan := make(chan *model.Response, len(m.responses))

	go func() {
		defer close(respChan)
		for _, resp := range m.responses {
			select {
			case respChan <- resp:
			case <-ctx.Done():
				return
			}
		}
	}()

	return respChan, nil
}

func (m *mockModel) recordRequest(req *model.Request) {
	if req == nil {
		return
	}
	cloned := &model.Request{
		Messages: cloneMessagesForTest(req.Messages),
	}
	m.mu.Lock()
	m.requests = append(m.requests, cloned)
	m.mu.Unlock()
}

func (m *mockModel) LastRequest() *model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return nil
	}
	return m.requests[len(m.requests)-1]
}

func cloneMessagesForTest(messages []model.Message) []model.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]model.Message, len(messages))
	for i, msg := range messages {
		cloned[i] = msg
		if len(msg.ContentParts) > 0 {
			cloned[i].ContentParts = append([]model.ContentPart(nil), msg.ContentParts...)
		}
		if len(msg.ToolCalls) > 0 {
			cloned[i].ToolCalls = append([]model.ToolCall(nil), msg.ToolCalls...)
		}
	}
	return cloned
}

type mockIterModel struct {
	IterSeq model.Seq[*model.Response]
	IterErr error

	GenerateContentCalled     bool
	GenerateContentIterCalled bool
}

func (m *mockIterModel) Info() model.Info {
	return model.Info{Name: "mock-iter"}
}

func (m *mockIterModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.GenerateContentCalled = true
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *mockIterModel) GenerateContentIter(ctx context.Context, req *model.Request) (model.Seq[*model.Response], error) {
	m.GenerateContentIterCalled = true
	if m.IterErr != nil {
		return nil, m.IterErr
	}
	return m.IterSeq, nil
}

type stubPluginManager struct{}

func (stubPluginManager) AgentCallbacks() *agent.Callbacks { return nil }
func (stubPluginManager) ModelCallbacks() *model.Callbacks { return nil }
func (stubPluginManager) ToolCallbacks() *tool.Callbacks   { return nil }
func (stubPluginManager) OnEvent(context.Context, *agent.Invocation, *event.Event) (*event.Event, error) {
	return nil, nil
}
func (stubPluginManager) Close(context.Context) error { return nil }

// mockRequestProcessor implements flow.RequestProcessor
type mockRequestProcessor struct{}

func (m *mockRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	evt := event.New(invocation.InvocationID, invocation.AgentName)
	evt.Object = "preprocessing"
	select {
	case ch <- evt:
	default:
	}
}

type seedMessagesRequestProcessor struct {
	messages []model.Message
}

func (p *seedMessagesRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	req.Messages = append(req.Messages, cloneMessagesForTest(p.messages)...)
}

const flowRunPanicTestMsg = "boom"

type panicRequestProcessor struct{}

func (p *panicRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	panic(errors.New(flowRunPanicTestMsg))
}

// mockResponseProcessor implements flow.ResponseProcessor
type mockResponseProcessor struct{}

func (m *mockResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	resp *model.Response,
	ch chan<- *event.Event,
) {
	evt := event.New(invocation.InvocationID, invocation.AgentName)
	evt.Object = "postprocessing"
	select {
	case ch <- evt:
	default:
	}
}

type cancelResponseProcessor struct {
	cancel context.CancelFunc
}

func (c *cancelResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	resp *model.Response,
	ch chan<- *event.Event,
) {
	c.cancel()
}

type endInvocationResponseProcessor struct{}

func (p *endInvocationResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	resp *model.Response,
	ch chan<- *event.Event,
) {
	if invocation != nil {
		invocation.EndInvocation = true
	}
}

func TestFlow_Interface(t *testing.T) {
	llmFlow := New(nil, nil, Options{})
	var f flow.Flow = llmFlow

	// Test that the flow implements the interface
	log.Debugf("Flow interface test: %v", f)

	// Simple compile test
	var _ flow.Flow = f
}

func TestFlow_Run_RecoversPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		2*time.Second,
	)
	defer cancel()

	llmFlow := New(
		[]flow.RequestProcessor{&panicRequestProcessor{}},
		nil,
		Options{},
	)
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)

	var errorEvent *event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		if evt.Error != nil {
			errorEvent = evt
		}
	}

	require.NotNil(t, errorEvent)
	require.Equal(t, model.ErrorTypeFlowError, errorEvent.Error.Type)
	require.Contains(t, errorEvent.Error.Message, flowRunPanicTestMsg)
}

const flowRunPanicTestUnknownValue = 123

func TestRecoverFlowRunPanic_NoPanic(t *testing.T) {
	func() {
		defer recoverFlowRunPanic(context.Background(), nil, nil)
	}()
}

func TestRecoverFlowRunPanic_EmitsEventForUnknownType(t *testing.T) {
	ctx := context.Background()
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
	}
	eventChan := make(chan *event.Event, 1)

	func() {
		defer recoverFlowRunPanic(ctx, invocation, eventChan)
		panic(flowRunPanicTestUnknownValue)
	}()

	select {
	case evt := <-eventChan:
		require.NotNil(t, evt.Error)
		require.Equal(t, model.ErrorTypeFlowError, evt.Error.Type)
		require.Contains(t, evt.Error.Message, "123")
	default:
		t.Fatal("expected error event")
	}
}

func TestFlowInvocationIDAndAgentName(t *testing.T) {
	require.Equal(t, "", flowInvocationID(nil))
	require.Equal(t, "", flowAgentName(nil))

	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
	}
	require.Equal(t, invocation.InvocationID, flowInvocationID(invocation))
	require.Equal(t, invocation.AgentName, flowAgentName(invocation))
}

func TestModelCallbacks_BeforeSkip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return &model.Response{ID: "skip-response"}, nil // Return custom response to skip model call
	})

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		events = append(events, evt)
		if len(events) >= 2 {
			break
		}
	}
	require.Equal(t, 2, len(events))
	require.Equal(t, "skip-response", events[1].Response.ID)
}

func TestModelCBs_BeforeCustom(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return &model.Response{ID: "custom-before"}, nil
	})

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		events = append(events, evt)
		if len(events) >= 2 {
			break
		}
	}
	require.Equal(t, 2, len(events))
	require.Equal(t, "custom-before", events[1].Response.ID)
}

func TestModelCallbacks_BeforeError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
		return nil, errors.New("before error")
	})

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "should-not-be-called"}},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		events = append(events, evt)
		if len(events) >= 2 {
			break
		}
		// Receive the first error event and cancel ctx to prevent deadlock.
		if evt.Error != nil && evt.Error.Message == "before error" {
			cancel()
			break
		}
	}
	require.Equal(t, 2, len(events))
	require.Equal(t, "before error", events[1].Error.Message)
}

func TestModelCallbacks_BeforeSetsContext_AfterSeesValue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	const want = "ctx-from-before"
	afterSawCh := make(chan string, 1)

	modelCallbacks := model.NewCallbacks().
		RegisterBeforeModel(func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: context.WithValue(ctx, testCtxKey{}, want),
			}, nil
		}).
		RegisterAfterModel(func(
			ctx context.Context,
			args *model.AfterModelArgs,
		) (*model.AfterModelResult, error) {
			if v, ok := ctx.Value(testCtxKey{}).(string); ok {
				select {
				case afterSawCh <- v:
				default:
				}
			}
			return nil, nil
		})

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{
				{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("ok")},
					},
				},
			},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)

	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)

	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
	}

	select {
	case got := <-afterSawCh:
		require.Equal(t, want, got)
	case <-ctx.Done():
		t.Fatalf("timed out waiting for after callback to observe context value")
	}
}

func TestModelCBs_AfterOverride(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterAfterModel(
		func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
			return &model.Response{Object: "after-override"}, nil
		},
	)

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "original"}},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		events = append(events, evt)
		if len(events) >= 2 {
			break
		}
	}
	require.Equal(t, 2, len(events))
	t.Log(events[0])
	require.Equal(t, "after-override", events[1].Response.Object)
}

func TestModelCallbacks_AfterError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterAfterModel(
		func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
			return nil, errors.New("after error")
		},
	)

	llmFlow := New(nil, nil, Options{ModelCallbacks: modelCallbacks})
	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "original"}},
		}),
		agent.WithInvocationSession(&session.Session{ID: "test-session"}),
	)
	eventChan, err := llmFlow.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range eventChan {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			invocation.NotifyCompletion(ctx, key)
		}
		events = append(events, evt)
		if len(events) >= 2 {
			break
		}
		// Receive the first error event and cancel ctx to prevent deadlock.
		if evt.Error != nil && evt.Error.Message == "after error" {
			cancel()
			break
		}
	}
	require.Equal(t, 2, len(events))
	require.Equal(t, "after error", events[1].Error.Message)
}

func TestFlow_RunBeforeModelCallbacks_NoModelCallbacks(t *testing.T) {
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation()
	inv.Plugins = stubPluginManager{}

	ctx := context.Background()
	gotCtx, resp, err := f.runBeforeModelCallbacks(ctx, inv, &model.Request{})
	require.NoError(t, err)
	require.Equal(t, ctx, gotCtx)
	require.Nil(t, resp)
}

func TestFlow_RunBeforeModelCallbacks_PreservesInvocationContext(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationID("invocation-id"))
	local := model.NewCallbacks().RegisterBeforeModel(func(
		ctx context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		callbackInvocation, ok := agent.InvocationFromContext(ctx)
		require.True(t, ok)
		require.Same(t, inv, callbackInvocation)
		return &model.BeforeModelResult{Context: context.WithValue(ctx, testCtxKey{}, "v")}, nil
	})
	f := New(nil, nil, Options{ModelCallbacks: local})
	gotCtx, resp, err := f.runBeforeModelCallbacks(context.Background(), inv, &model.Request{})
	require.NoError(t, err)
	require.Nil(t, resp)
	require.Equal(t, "v", gotCtx.Value(testCtxKey{}))
	callbackInvocation, ok := agent.InvocationFromContext(gotCtx)
	require.True(t, ok)
	require.Same(t, inv, callbackInvocation)
}

func TestFlow_RunBeforeModelCallbacks_PreservesReplacedInvocationContext(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationID("original-invocation-id"))
	replacementInvocation := agent.NewInvocation(agent.WithInvocationID("replacement-invocation-id"))
	local := model.NewCallbacks().RegisterBeforeModel(func(
		ctx context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		return &model.BeforeModelResult{
			Context: agent.NewInvocationContext(context.WithValue(ctx, testCtxKey{}, "v"), replacementInvocation),
		}, nil
	})
	f := New(nil, nil, Options{ModelCallbacks: local})
	gotCtx, resp, err := f.runBeforeModelCallbacks(context.Background(), inv, &model.Request{})
	require.NoError(t, err)
	require.Nil(t, resp)
	require.Equal(t, "v", gotCtx.Value(testCtxKey{}))
	callbackInvocation, ok := agent.InvocationFromContext(gotCtx)
	require.True(t, ok)
	require.Same(t, replacementInvocation, callbackInvocation)
}

func TestFlow_RunBeforeModelCallbacks_PreservesInvocationContextWithinCallbackGroup(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationID("invocation-id"))
	local := model.NewCallbacks().
		RegisterBeforeModel(func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: context.WithValue(context.Background(), testCtxKey{}, "v"),
			}, nil
		}).
		RegisterBeforeModel(func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			callbackInvocation, ok := agent.InvocationFromContext(ctx)
			require.True(t, ok)
			require.Same(t, inv, callbackInvocation)
			return nil, nil
		})
	f := New(nil, nil, Options{ModelCallbacks: local})
	gotCtx, resp, err := f.runBeforeModelCallbacks(context.Background(), inv, &model.Request{})
	require.NoError(t, err)
	require.Nil(t, resp)
	require.Equal(t, "v", gotCtx.Value(testCtxKey{}))
	callbackInvocation, ok := agent.InvocationFromContext(gotCtx)
	require.True(t, ok)
	require.Same(t, inv, callbackInvocation)
}

func TestFlow_RunBeforeModelCallbacks_PreservesReplacedInvocationWithinCallbackGroup(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationID("original-invocation-id"))
	replacementInvocation := agent.NewInvocation(agent.WithInvocationID("replacement-invocation-id"))
	local := model.NewCallbacks().
		RegisterBeforeModel(func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(context.Background(), replacementInvocation),
			}, nil
		}).
		RegisterBeforeModel(func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			callbackInvocation, ok := agent.InvocationFromContext(ctx)
			require.True(t, ok)
			require.Same(t, replacementInvocation, callbackInvocation)
			return nil, nil
		})
	f := New(nil, nil, Options{ModelCallbacks: local})
	gotCtx, resp, err := f.runBeforeModelCallbacks(context.Background(), inv, &model.Request{})
	require.NoError(t, err)
	require.Nil(t, resp)
	callbackInvocation, ok := agent.InvocationFromContext(gotCtx)
	require.True(t, ok)
	require.Same(t, replacementInvocation, callbackInvocation)
}

func TestFlow_RunBeforeModelCallbacks_PreservesReplacementInvocationForFreshResultContext(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationID("original-invocation-id"))
	replacementInvocation := agent.NewInvocation(agent.WithInvocationID("replacement-invocation-id"))
	local := model.NewCallbacks().
		RegisterBeforeModel(func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(context.Background(), replacementInvocation),
			}, nil
		}).
		RegisterBeforeModel(func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			callbackInvocation, ok := agent.InvocationFromContext(ctx)
			require.True(t, ok)
			require.Same(t, replacementInvocation, callbackInvocation)
			return &model.BeforeModelResult{
				Context: context.WithValue(context.Background(), testCtxKey{}, "v"),
			}, nil
		})
	f := New(nil, nil, Options{ModelCallbacks: local})
	gotCtx, resp, err := f.runBeforeModelCallbacks(context.Background(), inv, &model.Request{})
	require.NoError(t, err)
	require.Nil(t, resp)
	require.Equal(t, "v", gotCtx.Value(testCtxKey{}))
	callbackInvocation, ok := agent.InvocationFromContext(gotCtx)
	require.True(t, ok)
	require.Same(t, replacementInvocation, callbackInvocation)
}

func TestFlow_RunBeforeModelCallbacks_DoesNotMutateSharedBeforeModelResult(t *testing.T) {
	sharedResult := &model.BeforeModelResult{
		Context: context.WithValue(context.Background(), testCtxKey{}, "v"),
	}
	local := model.NewCallbacks().RegisterBeforeModel(func(
		ctx context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		return sharedResult, nil
	})
	f := New(nil, nil, Options{ModelCallbacks: local})
	firstInvocation := agent.NewInvocation(agent.WithInvocationID("first-invocation-id"))
	firstCtx, resp, err := f.runBeforeModelCallbacks(context.Background(), firstInvocation, &model.Request{})
	require.NoError(t, err)
	require.Nil(t, resp)
	require.Equal(t, "v", firstCtx.Value(testCtxKey{}))
	firstCallbackInvocation, ok := agent.InvocationFromContext(firstCtx)
	require.True(t, ok)
	require.Same(t, firstInvocation, firstCallbackInvocation)
	secondInvocation := agent.NewInvocation(agent.WithInvocationID("second-invocation-id"))
	secondCtx, resp, err := f.runBeforeModelCallbacks(context.Background(), secondInvocation, &model.Request{})
	require.NoError(t, err)
	require.Nil(t, resp)
	require.Equal(t, "v", secondCtx.Value(testCtxKey{}))
	secondCallbackInvocation, ok := agent.InvocationFromContext(secondCtx)
	require.True(t, ok)
	require.Same(t, secondInvocation, secondCallbackInvocation)
	_, ok = agent.InvocationFromContext(sharedResult.Context)
	require.False(t, ok)
}

func TestFlow_GenerateContentSeq_UsesIterModel(t *testing.T) {
	f := New(nil, nil, Options{})
	iterModel := &mockIterModel{
		IterSeq: func(yield func(*model.Response) bool) {
			yield(&model.Response{ID: "iter"})
		},
	}
	inv := agent.NewInvocation(agent.WithInvocationModel(iterModel))

	seq, err := f.generateContentSeq(context.Background(), inv, &model.Request{})
	require.NoError(t, err)
	require.True(t, iterModel.GenerateContentIterCalled)
	require.False(t, iterModel.GenerateContentCalled)

	var responses []*model.Response
	seq(func(resp *model.Response) bool {
		responses = append(responses, resp)
		return true
	})
	require.Len(t, responses, 1)
	require.Equal(t, "iter", responses[0].ID)
}

func TestFlow_GenerateContentSeq_IterModelError(t *testing.T) {
	f := New(nil, nil, Options{})
	iterModel := &mockIterModel{
		IterErr: errors.New("iter error"),
	}
	inv := agent.NewInvocation(agent.WithInvocationModel(iterModel))

	seq, err := f.generateContentSeq(context.Background(), inv, &model.Request{})
	require.Error(t, err)
	require.Nil(t, seq)
	require.True(t, iterModel.GenerateContentIterCalled)
}

func TestFlow_GenerateContentSeq_NilIterModel(t *testing.T) {
	f := New(nil, nil, Options{})
	iterModel := &mockIterModel{}
	inv := agent.NewInvocation(agent.WithInvocationModel(iterModel))

	seq, err := f.generateContentSeq(context.Background(), inv, &model.Request{})
	require.ErrorContains(t, err, errMsgNoModelResponse)
	require.Nil(t, seq)
	require.True(t, iterModel.GenerateContentIterCalled)
	require.False(t, iterModel.GenerateContentCalled)
}

func TestFlow_GenerateContentSeq_NoResponseModel(t *testing.T) {
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation(agent.WithInvocationModel(&noResponseModel{}))
	seq, err := f.generateContentSeq(context.Background(), inv, &model.Request{})
	require.NoError(t, err)
	require.NotNil(t, seq)
}

func TestFlow_CallLLM_MaxLLMCallsExceeded(t *testing.T) {
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{
			responses: []*model.Response{{ID: "ok"}},
		}),
	)
	inv.MaxLLMCalls = 1

	_, _, err := f.callLLM(context.Background(), inv, &model.Request{})
	require.NoError(t, err)

	_, _, err = f.callLLM(context.Background(), inv, &model.Request{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "max LLM calls (1) exceeded")
}

func TestProcessStreamingResponses_ContextCancelledAfterPostprocess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := New(nil, []flow.ResponseProcessor{&cancelResponseProcessor{cancel: cancel}}, Options{})

	inv := agent.NewInvocation()
	req := &model.Request{}
	responseSeq := func(yield func(*model.Response) bool) {
		yield(&model.Response{ID: "resp", Done: true})
	}

	eventChan := make(chan *event.Event, 10)
	tracer := oteltrace.NewNoopTracerProvider().Tracer("t")
	_, span := tracer.Start(ctx, "s")
	defer span.End()

	_, err := f.processStreamingResponses(ctx, inv, req, responseSeq, eventChan, span, true)
	require.ErrorIs(t, err, context.Canceled)
}

// noResponseModel returns a closed channel without emitting any responses.
type noResponseModel struct{}

func (m *noResponseModel) Info() model.Info { return model.Info{Name: "noresp"} }
func (m *noResponseModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func TestRun_NoPanicWhenModelReturnsNoResponses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation(
		agent.WithInvocationModel(&noResponseModel{}),
	)
	ch, err := f.Run(ctx, inv)
	require.NoError(t, err)
	var errorEvent *event.Event
	var count int
	for evt := range ch {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			inv.NotifyCompletion(ctx, key)
		}
		count++
		if evt != nil && evt.Error != nil {
			errorEvent = evt
		}
	}
	require.Equal(t, 1, count)
	require.Nil(t, errorEvent)
}

func TestRun_NilIterModelEmitsErrorEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	f := New(nil, nil, Options{})
	inv := agent.NewInvocation(
		agent.WithInvocationModel(&mockIterModel{}),
	)

	ch, err := f.Run(ctx, inv)
	require.NoError(t, err)

	var errorEvent *event.Event
	var count int
	for evt := range ch {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			inv.NotifyCompletion(ctx, key)
		}
		count++
		if evt != nil && evt.Error != nil {
			errorEvent = evt
		}
	}
	require.Equal(t, 2, count)
	require.NotNil(t, errorEvent)
	require.Equal(t, model.ErrorTypeFlowError, errorEvent.Error.Type)
	require.Contains(t, errorEvent.Error.Message, errMsgNoModelResponse)
}

func resourceMetricsContainAttribute(rm metricdata.ResourceMetrics, key, value string) bool {
	for _, scopeMetric := range rm.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			switch data := metric.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			case metricdata.Sum[float64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			case metricdata.Histogram[int64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			}
		}
	}
	return false
}

func attributeSetContains(set attribute.Set, key, value string) bool {
	for _, kv := range set.ToSlice() {
		if string(kv.Key) == key && kv.Value.AsString() == value {
			return true
		}
	}
	return false
}

// TestRunAfterModelCallbacks_ErrorPassing tests that modelErr is correctly passed to callbacks
// when response.Error is not nil.
func TestRunAfterModelCallbacks_ErrorPassing(t *testing.T) {
	tests := []struct {
		name       string
		response   *model.Response
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "response with error",
			response: &model.Response{
				Error: &model.ResponseError{
					Type:    model.ErrorTypeAPIError,
					Message: "rate limit exceeded",
				},
			},
			wantErr:    true,
			wantErrMsg: "api_error: rate limit exceeded",
		},
		{
			name: "response without error",
			response: &model.Response{
				Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}},
			},
			wantErr:    false,
			wantErrMsg: "",
		},
		{
			name:       "nil response",
			response:   nil,
			wantErr:    false,
			wantErrMsg: "",
		},
		{
			name: "response with nil error field",
			response: &model.Response{
				Error: nil,
			},
			wantErr:    false,
			wantErrMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedErr error
			callbacks := model.NewCallbacks().RegisterAfterModel(
				func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
					receivedErr = modelErr
					return nil, nil
				},
			)

			flow := &Flow{
				modelCallbacks: callbacks,
			}

			_, _, err := flow.runAfterModelCallbacks(
				context.Background(),
				nil,
				&model.Request{},
				tt.response,
			)
			require.NoError(t, err)

			if tt.wantErr {
				require.NotNil(t, receivedErr, "expected callback to receive error, but got nil")
				require.Equal(t, tt.wantErrMsg, receivedErr.Error(), "error message mismatch")
			} else {
				require.Nil(t, receivedErr, "expected callback to receive nil error, but got: %v", receivedErr)
			}
		})
	}
}

// blockingModel emits one response then waits for ctx cancellation.
type blockingModel struct{}

func (m *blockingModel) Info() model.Info {
	return model.Info{Name: "blocking"}
}

func (m *blockingModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Choices: []model.Choice{
			{
				Message: model.NewAssistantMessage("hi"),
			},
		},
	}
	go func() {
		defer close(ch)
		<-ctx.Done()
	}()
	return ch, nil
}

func TestFlow_Run_ContextCanceledIsGraceful(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := New(nil, nil, Options{})
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(&minimalAgent{}),
		agent.WithInvocationModel(&blockingModel{}),
	)

	ch, err := f.Run(ctx, inv)
	require.NoError(t, err)

	var sawLLMEvent bool
	for evt := range ch {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			_ = inv.NotifyCompletion(ctx, key)
		}
		if evt.Response != nil {
			sawLLMEvent = true
			cancel()
		}
	}
	require.True(t, sawLLMEvent)
}

type hookPlugin struct {
	name string
	reg  func(r *plugin.Registry)
}

func (p *hookPlugin) Name() string { return p.name }

func (p *hookPlugin) Register(r *plugin.Registry) {
	if p.reg != nil {
		p.reg(r)
	}
}

type captureModel struct {
	called bool
}

func (m *captureModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.called = true
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true}
	close(ch)
	return ch, nil
}

func (m *captureModel) Info() model.Info { return model.Info{Name: "m"} }

func TestFlow_CallLLM_PluginBeforeModelCanShortCircuit(t *testing.T) {
	plugCalled := false
	localCalled := false

	p := &hookPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				plugCalled = true
				return &model.BeforeModelResult{
					CustomResponse: &model.Response{Done: true},
				}, nil
			})
		},
	}
	pm := plugin.MustNewManager(p)

	local := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, req *model.Request) (*model.Response, error) {
			localCalled = true
			return nil, nil
		},
	)

	flow := &Flow{modelCallbacks: local}
	m := &captureModel{}
	inv := &agent.Invocation{
		AgentName: "a",
		Model:     m,
		Plugins:   pm,
	}

	_, ch, err := flow.callLLM(context.Background(), inv, &model.Request{})
	require.NoError(t, err)
	ch(func(_ *model.Response) bool { return true })
	require.True(t, plugCalled)
	require.False(t, localCalled)
	require.False(t, m.called)
}

type testCtxKey struct{}

func TestFlow_CallLLM_PluginBeforeModelError(t *testing.T) {
	plugCalled := false
	localCalled := false

	p := &hookPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				plugCalled = true
				return nil, fmt.Errorf("boom")
			})
		},
	}
	pm := plugin.MustNewManager(p)

	local := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, req *model.Request) (*model.Response, error) {
			localCalled = true
			return nil, nil
		},
	)

	flow := &Flow{modelCallbacks: local}
	m := &captureModel{}
	inv := &agent.Invocation{
		AgentName: "a",
		Model:     m,
		Plugins:   pm,
	}

	_, ch, err := flow.callLLM(context.Background(), inv, &model.Request{})
	require.Error(t, err)
	require.Nil(t, ch)
	require.True(t, plugCalled)
	require.False(t, localCalled)
	require.False(t, m.called)
}

func TestFlow_CallLLM_PluginBeforeModelContextPropagates(t *testing.T) {
	plugCalled := false
	localSaw := ""

	p := &hookPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.BeforeModel(func(
				ctx context.Context,
				args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				plugCalled = true
				return &model.BeforeModelResult{
					Context: context.WithValue(ctx, testCtxKey{}, "v"),
				}, nil
			})
		},
	}
	pm := plugin.MustNewManager(p)

	local := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, req *model.Request) (*model.Response, error) {
			if v, ok := ctx.Value(testCtxKey{}).(string); ok {
				localSaw = v
			}
			return &model.Response{Done: true}, nil
		},
	)

	flow := &Flow{modelCallbacks: local}
	m := &captureModel{}
	inv := &agent.Invocation{
		AgentName: "a",
		Model:     m,
		Plugins:   pm,
	}

	_, ch, err := flow.callLLM(context.Background(), inv, &model.Request{})
	require.NoError(t, err)
	ch(func(_ *model.Response) bool { return true })
	require.True(t, plugCalled)
	require.Equal(t, "v", localSaw)

}

func TestFlow_AfterModelPluginOverridesLocal(t *testing.T) {
	localCalled := false
	custom := &model.Response{Done: true}

	p := &hookPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.AfterModel(func(
				ctx context.Context,
				args *model.AfterModelArgs,
			) (*model.AfterModelResult, error) {
				return &model.AfterModelResult{
					CustomResponse: custom,
				}, nil
			})
		},
	}
	pm := plugin.MustNewManager(p)

	local := model.NewCallbacks().RegisterAfterModel(
		func(
			ctx context.Context,
			req *model.Request,
			rsp *model.Response,
			modelErr error,
		) (*model.Response, error) {
			localCalled = true
			return nil, nil
		},
	)

	flow := &Flow{modelCallbacks: local}
	inv := &agent.Invocation{Plugins: pm}

	_, got, err := flow.runAfterModelCallbacks(
		context.Background(),
		inv,
		&model.Request{},
		&model.Response{Done: true},
	)
	require.NoError(t, err)
	require.Equal(t, custom, got)
	require.False(t, localCalled)
}

func TestFlow_AfterModelPluginError(t *testing.T) {
	p := &hookPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.AfterModel(func(
				ctx context.Context,
				args *model.AfterModelArgs,
			) (*model.AfterModelResult, error) {
				return nil, fmt.Errorf("boom")
			})
		},
	}
	pm := plugin.MustNewManager(p)

	flow := &Flow{modelCallbacks: nil}
	inv := &agent.Invocation{Plugins: pm}

	_, _, err := flow.runAfterModelCallbacks(
		context.Background(),
		inv,
		&model.Request{},
		&model.Response{Done: true},
	)
	require.Error(t, err)
}

func TestFlow_AfterModelPluginContextPropagatesToLocal(t *testing.T) {
	localSaw := ""
	p := &hookPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.AfterModel(func(
				ctx context.Context,
				args *model.AfterModelArgs,
			) (*model.AfterModelResult, error) {
				return &model.AfterModelResult{
					Context: context.WithValue(ctx, testCtxKey{}, "v"),
				}, nil
			})
		},
	}
	pm := plugin.MustNewManager(p)

	local := model.NewCallbacks().RegisterAfterModel(
		func(
			ctx context.Context,
			req *model.Request,
			rsp *model.Response,
			modelErr error,
		) (*model.Response, error) {
			if v, ok := ctx.Value(testCtxKey{}).(string); ok {
				localSaw = v
			}
			return nil, nil
		},
	)

	flow := &Flow{modelCallbacks: local}
	inv := &agent.Invocation{Plugins: pm}

	_, _, err := flow.runAfterModelCallbacks(
		context.Background(),
		inv,
		&model.Request{},
		&model.Response{Done: true},
	)
	require.NoError(t, err)
	require.Equal(t, "v", localSaw)
}

func TestFlow_AfterModelPluginSeesResponseError(t *testing.T) {
	sawErr := ""
	p := &hookPlugin{
		name: "p",
		reg: func(r *plugin.Registry) {
			r.AfterModel(func(
				ctx context.Context,
				args *model.AfterModelArgs,
			) (*model.AfterModelResult, error) {
				if args != nil && args.Error != nil {
					sawErr = args.Error.Error()
				}
				return nil, nil
			})
		},
	}
	pm := plugin.MustNewManager(p)

	flow := &Flow{modelCallbacks: nil}
	inv := &agent.Invocation{Plugins: pm}
	rsp := &model.Response{
		Done: true,
		Error: &model.ResponseError{
			Type:    "test",
			Message: "boom",
		},
	}

	_, _, err := flow.runAfterModelCallbacks(
		context.Background(),
		inv,
		&model.Request{},
		rsp,
	)
	require.NoError(t, err)
	require.Contains(t, sawErr, "test")
	require.Contains(t, sawErr, "boom")
}

func TestFlow_callLLM_NoModel(t *testing.T) {
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation()
	req := &model.Request{}

	_, ch, err := f.callLLM(context.Background(), inv, req)
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestFlow_callLLM_ModelError(t *testing.T) {
	f := New(nil, nil, Options{})
	inv := agent.NewInvocation(
		agent.WithInvocationModel(&mockModel{ShouldError: true}),
	)
	req := &model.Request{}

	_, ch, err := f.callLLM(context.Background(), inv, req)
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestFlow_Postprocess_WithProcessor(t *testing.T) {
	respProcessor := &mockResponseProcessor{}
	f := New(nil, []flow.ResponseProcessor{respProcessor}, Options{})

	ctx := context.Background()
	inv := agent.NewInvocation()
	req := &model.Request{}
	resp := &model.Response{
		Choices: []model.Choice{
			{
				Message: model.NewAssistantMessage("ok"),
			},
		},
	}
	eventCh := make(chan *event.Event, 2)

	f.postprocess(ctx, inv, req, resp, eventCh)

	var count int
	for {
		select {
		case <-eventCh:
			count++
		default:
			goto done
		}
	}

done:
	require.Equal(t, 1, count)
}

// Test that when RunOptions.Resume is enabled and the latest session event
// is an assistant tool_call response, the flow executes the pending tool
// before issuing a new LLM request.
func TestRun_WithResumeExecutesPendingToolCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Record invocations of the test tool.
	var toolCalls []string
	testTool := function.NewFunctionTool(
		func(_ context.Context, req *struct {
			Value string `json:"value"`
		}) (*struct {
			Value string `json:"value"`
		}, error) {
			toolCalls = append(toolCalls, req.Value)
			return &struct {
				Value string `json:"value"`
			}{Value: "ok:" + req.Value}, nil
		},
		function.WithName("resume_tool"),
		function.WithDescription("resume test tool"),
	)

	// Session contains a single assistant tool_call response.
	sess := &session.Session{}
	resp := &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							ID: "call-1",
							Function: model.FunctionDefinitionParam{
								Name:      "resume_tool",
								Arguments: []byte(`{"value":"resume"}`),
							},
						},
					},
				},
			},
		},
	}
	toolCallEvent := event.NewResponseEvent("inv-1", "agent-resume", resp)
	sess.Events = append(sess.Events, *toolCallEvent)

	// Agent with the test tool and a model that returns no responses.
	agentWithTool := &mockAgentWithTools{
		name:  "agent-resume",
		tools: []tool.Tool{testTool},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-1"),
		agent.WithInvocationAgent(agentWithTool),
		agent.WithInvocationSession(sess),
		agent.WithInvocationModel(&noResponseModel{}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			Resume: true,
		}),
	)

	llmFlow := New(
		nil,
		[]flow.ResponseProcessor{
			processor.NewFunctionCallResponseProcessor(false, nil),
		},
		Options{},
	)

	eventCh, err := llmFlow.Run(ctx, inv)
	require.NoError(t, err)

	var sawToolResult bool
	for evt := range eventCh {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			_ = inv.NotifyCompletion(ctx, key)
		}
		if evt.Response != nil && evt.Response.IsToolResultResponse() {
			sawToolResult = true
		}
		require.Nil(t, evt.Error)
	}

	require.True(t, sawToolResult, "expected tool result event when resuming")
	require.Len(t, toolCalls, 1)
	require.Equal(t, "resume", toolCalls[0])
}

type countingSessionService struct {
	session.Service
	mu         sync.Mutex
	calls      int
	filterKeys []string
}

func (c *countingSessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	c.mu.Lock()
	c.calls++
	c.filterKeys = append(c.filterKeys, filterKey)
	c.mu.Unlock()
	return c.Service.CreateSessionSummary(ctx, sess, filterKey, force)
}

func (c *countingSessionService) snapshot() (int, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, len(c.filterKeys))
	copy(keys, c.filterKeys)
	return c.calls, keys
}

type twoStepToolCallModel struct {
	mu      sync.Mutex
	callNum int
}

func (m *twoStepToolCallModel) Info() model.Info {
	return model.Info{Name: "two-step"}
}

func (m *twoStepToolCallModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.callNum++
	callNum := m.callNum
	m.mu.Unlock()

	response := &model.Response{Done: true}
	if callNum == 1 {
		response.Choices = []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: "call-1",
					Function: model.FunctionDefinitionParam{
						Name:      "intra_run_tool",
						Arguments: []byte("{}"),
					},
				}},
			},
		}}
	} else {
		response.Choices = []model.Choice{{
			Message: model.NewAssistantMessage("done"),
		}}
	}

	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

func (m *twoStepToolCallModel) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callNum
}

func TestRun_SyncSummaryIntraRun_TriggersBetweenIterations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	baseSvc := inmemory.NewSessionService()
	t.Cleanup(func() {
		require.NoError(t, baseSvc.Close())
	})

	sess, err := baseSvc.CreateSession(
		ctx,
		session.Key{AppName: "app", UserID: "user", SessionID: "sess"},
		nil,
	)
	require.NoError(t, err)

	countingSvc := &countingSessionService{Service: baseSvc}
	modelStub := &twoStepToolCallModel{}
	toolStub := function.NewFunctionTool(
		func(_ context.Context, req *struct{}) (*struct{}, error) {
			return &struct{}{}, nil
		},
		function.WithName("intra_run_tool"),
		function.WithDescription("intra run tool"),
	)
	agentWithTool := &mockAgentWithTools{
		name:  "agent-intra-run",
		tools: []tool.Tool{toolStub},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-intra-run"),
		agent.WithInvocationAgent(agentWithTool),
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(countingSvc),
		agent.WithInvocationModel(modelStub),
		agent.WithInvocationEventFilterKey("branch/agent-intra-run"),
	)

	llmFlow := New(
		nil,
		[]flow.ResponseProcessor{
			processor.NewFunctionCallResponseProcessor(false, nil),
		},
		Options{SyncSummaryIntraRun: true},
	)

	eventCh, err := llmFlow.Run(ctx, inv)
	require.NoError(t, err)
	for evt := range eventCh {
		if evt.RequiresCompletion {
			key := agent.AppendEventNoticeKeyPrefix + evt.ID
			_ = inv.NotifyCompletion(ctx, key)
		}
	}

	calls, filterKeys := countingSvc.snapshot()
	require.Equal(t, 2, modelStub.Calls())
	require.Equal(t, 1, calls)
	require.Equal(t, []string{"branch/agent-intra-run"}, filterKeys)

	// Verify the state key is set so the runner can skip async enqueue.
	syncSummaryIntraRun, ok := agent.GetStateValue[bool](
		inv, agent.SyncSummaryIntraRunStateKey,
	)
	require.True(t, ok, "SyncSummaryIntraRunStateKey should be set")
	require.True(t, syncSummaryIntraRun)
}

// errSessionService wraps a real session.Service but forces
// CreateSessionSummary to return an error for testing the
// error-log branch in maybeIntraRunSummary.
type errSessionService struct {
	session.Service
}

func (e *errSessionService) CreateSessionSummary(
	_ context.Context,
	_ *session.Session,
	_ string,
	_ bool,
) error {
	return errors.New("forced summary error")
}

func TestMaybeSyncSummaryIntraRun_GuardClauses(t *testing.T) {
	f := &Flow{syncSummaryIntraRun: true}
	ctx := context.Background()

	// nil invocation — should not panic.
	f.maybeSyncSummaryIntraRun(ctx, nil)

	// Non-nil invocation with nil Session.
	inv := agent.NewInvocation()
	inv.Session = nil
	f.maybeSyncSummaryIntraRun(ctx, inv)

	// Non-nil invocation with session but nil SessionService.
	inv.Session = &session.Session{}
	inv.SessionService = nil
	f.maybeSyncSummaryIntraRun(ctx, inv)

	// syncSummaryIntraRun disabled — should skip even with full
	// invocation.
	fOff := &Flow{syncSummaryIntraRun: false}
	inv.SessionService = inmemory.NewSessionService()
	t.Cleanup(func() {
		_ = inv.SessionService.Close()
	})
	fOff.maybeSyncSummaryIntraRun(ctx, inv)
}

func TestMaybeSyncSummaryIntraRun_ErrorBranch(t *testing.T) {
	f := &Flow{syncSummaryIntraRun: true}
	ctx := context.Background()

	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationSessionService(
			&errSessionService{},
		),
		agent.WithInvocationEventFilterKey("branch/err"),
	)

	// Should not panic; just logs the error internally.
	f.maybeSyncSummaryIntraRun(ctx, inv)
}

func TestRun_SyncSummaryIntraRun_NilInvocation(t *testing.T) {
	// When invocation is nil and syncSummaryIntraRun is true,
	// the SetState guard should prevent a nil-pointer panic.
	llmFlow := New(
		nil,
		nil,
		Options{SyncSummaryIntraRun: true},
	)
	eventCh, err := llmFlow.Run(
		context.Background(), nil,
	)
	require.NoError(t, err)

	// Drain events; the flow should exit quickly since there is
	// no model to call.
	for range eventCh {
	}
}

func TestWaitEventTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	timeout := WaitEventTimeout(ctx)
	require.InDelta(t, time.Second.Seconds(), timeout.Seconds(), 0.1)
}

func TestWaitEventTimeout_NoDeadline(t *testing.T) {
	ctx := context.Background()
	timeout := WaitEventTimeout(ctx)
	require.Equal(t, 5*time.Second, timeout)
}
