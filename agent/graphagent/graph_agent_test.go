//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graphagent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/barrier"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type staticGraphAgentModel struct {
	name    string
	content string
}

type emptyIDGraphAgentModel struct {
	name    string
	content string
}

type streamingGraphAgentModel struct {
	name   string
	deltas []string
	final  string
}

type usageGraphAgentModel struct {
	name    string
	content string
	usage   model.Usage
}

func (m *staticGraphAgentModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:   "graphagent-response-" + m.name,
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(m.content),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *staticGraphAgentModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *emptyIDGraphAgentModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:   "",
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(m.content),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *emptyIDGraphAgentModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *streamingGraphAgentModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, len(m.deltas)+1)
	for _, delta := range m.deltas {
		ch <- &model.Response{
			ID:        "graphagent-response-" + m.name,
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.NewAssistantMessage(delta),
			}},
		}
	}
	ch <- &model.Response{
		ID:   "graphagent-response-" + m.name,
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(m.final),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *streamingGraphAgentModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *usageGraphAgentModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:   "graphagent-response-" + m.name,
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(m.content),
		}},
		Usage: &m.usage,
	}
	close(ch)
	return ch, nil
}

func (m *usageGraphAgentModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type disableTracingModel struct{}

func (m *disableTracingModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	out := make(chan *model.Response, 1)
	out <- &model.Response{
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("ok")},
		},
	}
	close(out)
	return out, nil
}

func (m *disableTracingModel) Info() model.Info {
	return model.Info{Name: "disable-tracing-model"}
}

func useSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	recorder := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
	originalProvider := trace.TracerProvider
	originalTracer := trace.Tracer
	trace.TracerProvider = provider
	trace.Tracer = provider.Tracer("graph-agent-disable-tracing-test")
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		trace.TracerProvider = originalProvider
		trace.Tracer = originalTracer
	})
	return recorder
}

func TestNewGraphAgent(t *testing.T) {
	// Create a simple graph using the new API.
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			input := state["input"].(string)
			return graph.State{"output": "processed: " + input}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Test creating graph agent.
	graphAgent, err := New("test-agent", g)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if graphAgent == nil {
		t.Fatal("Expected non-nil graph agent")
	}

	// Test agent info.
	info := graphAgent.Info()
	if info.Name != "test-agent" {
		t.Errorf("Expected name 'test-agent', got '%s'", info.Name)
	}
}

func TestGraphAgentWithOptions(t *testing.T) {
	// Create a simple graph using the new API.
	schema := graph.NewStateSchema().
		AddField("counter", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("increment", func(ctx context.Context, state graph.State) (any, error) {
			counter, _ := state["counter"].(int)
			return graph.State{"counter": counter + 1}, nil
		}).
		SetEntryPoint("increment").
		SetFinishPoint("increment").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Test creating graph agent with options.
	initialState := graph.State{"counter": 5}
	graphAgent, err := New("test-agent", g,
		WithDescription("Test agent description"),
		WithInitialState(initialState),
		WithChannelBufferSize(512))

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test that options were applied.
	info := graphAgent.Info()
	if info.Description != "Test agent description" {
		t.Errorf("Expected description to be set")
	}
}

func TestGraphAgentRun_NilInvocation(t *testing.T) {
	const nodeNoop = "noop"

	stateGraph := graph.NewStateGraph(graph.NewStateSchema())
	stateGraph.AddNode(
		nodeNoop,
		func(context.Context, graph.State) (any, error) {
			return nil, nil
		},
	)
	stateGraph.SetEntryPoint(nodeNoop)
	stateGraph.SetFinishPoint(nodeNoop)

	g, err := stateGraph.Compile()
	require.NoError(t, err)

	graphAgent, err := New("test-agent", g)
	require.NoError(t, err)

	eventCh, err := graphAgent.Run(context.Background(), nil)
	require.Error(t, err)
	require.Nil(t, eventCh)
	require.Equal(t, invocationNilErrMsg, err.Error())
}

func TestGraphAgentRun_UsesInvocationEventChannelBufferSize(t *testing.T) {
	stateGraph := graph.NewStateGraph(graph.NewStateSchema())
	stateGraph.AddNode("noop", func(context.Context, graph.State) (any, error) {
		return nil, nil
	})
	stateGraph.SetEntryPoint("noop")
	stateGraph.SetFinishPoint("noop")

	g, err := stateGraph.Compile()
	require.NoError(t, err)

	graphAgent, err := New("test-agent", g, WithChannelBufferSize(1))
	require.NoError(t, err)

	invocation := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithEventChannelBufferSize(7),
		)),
	)

	events, err := graphAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, 7, cap(events))
	for range events {
	}
}

func TestGraphAgentRun_DisableTracingFastPath(t *testing.T) {
	stateGraph := graph.NewStateGraph(graph.MessagesStateSchema())
	stateGraph.AddLLMNode("llm", &disableTracingModel{}, "analyze", nil)
	stateGraph.SetEntryPoint("llm")
	stateGraph.SetFinishPoint("llm")

	g, err := stateGraph.Compile()
	require.NoError(t, err)

	graphAgent, err := New("test-agent", g)
	require.NoError(t, err)

	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-disable-tracing"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableTracing: true,
		}),
	)

	events, err := graphAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	var sawResponse bool
	for evt := range events {
		if evt != nil && evt.Response != nil {
			sawResponse = true
		}
	}

	require.True(t, sawResponse)
}

func TestGraphAgentRun_DisableTracingFastPathKeepsOuterBufferSize(t *testing.T) {
	stateGraph := graph.NewStateGraph(graph.MessagesStateSchema())
	stateGraph.AddNode("done", func(context.Context, graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "ok"}, nil
	})
	stateGraph.SetEntryPoint("done")
	stateGraph.SetFinishPoint("done")
	g, err := stateGraph.Compile()
	require.NoError(t, err)
	graphAgent, err := New(
		"test-agent",
		g,
		WithChannelBufferSize(1),
		WithExecutorOptions(graph.WithChannelBufferSize(8)),
	)
	require.NoError(t, err)
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-disable-tracing-buffer"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableTracing: true,
		}),
	)
	events, err := graphAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, 1, cap(events))
	for range events {
	}
}

func TestGraphAgentRun_DisableTracingFastPath_PreservesVisibleCompletion(t *testing.T) {
	stateGraph := graph.NewStateGraph(graph.MessagesStateSchema())
	stateGraph.AddNode("done", func(context.Context, graph.State) (any, error) {
		return graph.State{
			graph.StateKeyLastResponse: "ok",
		}, nil
	})
	stateGraph.SetEntryPoint("done")
	stateGraph.SetFinishPoint("done")
	g, err := stateGraph.Compile()
	require.NoError(t, err)
	graphAgent, err := New("test-agent", g)
	require.NoError(t, err)
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-disable-tracing-hidden"),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableTracing(true),
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	events, err := graphAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	var sawRawCompletion bool
	var sawVisibleCompletion bool
	for evt := range events {
		if evt != nil && evt.Done && evt.Object == graph.ObjectTypeGraphExecution {
			sawRawCompletion = true
		}
		if evt != nil && evt.Done && evt.Object == model.ObjectTypeChatCompletion &&
			len(evt.StateDelta) > 0 {
			sawVisibleCompletion = true
		}
	}
	require.False(t, sawRawCompletion)
	require.True(t, sawVisibleCompletion)
}

func TestGraphAgentRun_DisableTracingWithCallbacksSkipsSpanCreation(t *testing.T) {
	recorder := useSpanRecorder(t)
	stateGraph := graph.NewStateGraph(graph.MessagesStateSchema())
	stateGraph.AddNode("done", func(context.Context, graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "ok"}, nil
	})
	stateGraph.SetEntryPoint("done")
	stateGraph.SetFinishPoint("done")
	g, err := stateGraph.Compile()
	require.NoError(t, err)
	callbacks := agent.NewCallbacks().RegisterBeforeAgent(func(
		ctx context.Context,
		args *agent.BeforeAgentArgs,
	) (*agent.BeforeAgentResult, error) {
		return &agent.BeforeAgentResult{}, nil
	})
	graphAgent, err := New("test-agent", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-disable-tracing-callbacks"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableTracing: true,
		}),
	)
	events, err := graphAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	for range events {
	}
	require.Empty(t, recorder.Ended())
}

func TestGraphAgentRun_DisableTracingSubAgentSkipsSpanCreation(t *testing.T) {
	recorder := useSpanRecorder(t)
	childGraph := graph.NewStateGraph(graph.MessagesStateSchema())
	childGraph.AddNode("child_done", func(context.Context, graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "child ok"}, nil
	})
	childGraph.SetEntryPoint("child_done")
	childGraph.SetFinishPoint("child_done")
	compiledChild, err := childGraph.Compile()
	require.NoError(t, err)
	childAgent, err := New("child", compiledChild)
	require.NoError(t, err)
	parentGraph := graph.NewStateGraph(graph.MessagesStateSchema())
	parentGraph.AddAgentNode("child")
	parentGraph.SetEntryPoint("child")
	parentGraph.SetFinishPoint("child")
	compiledParent, err := parentGraph.Compile()
	require.NoError(t, err)
	parentAgent, err := New("parent", compiledParent, WithSubAgents([]agent.Agent{childAgent}))
	require.NoError(t, err)
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-disable-tracing-subagent"),
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableTracing: true,
		}),
	)
	events, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	for range events {
	}
	require.Empty(t, recorder.Ended())
}
func TestGraphAgent_WithMaxConcurrency(t *testing.T) {
	const (
		nodeRoot       = "root"
		nodeCount      = 12
		maxConcurrency = 3
		waitTimeout    = 2 * time.Second
	)

	stateGraph := graph.NewStateGraph(graph.NewStateSchema())
	stateGraph.AddNode(nodeRoot, func(context.Context, graph.State) (any, error) {
		return nil, nil
	})
	stateGraph.SetEntryPoint(nodeRoot)

	var active atomic.Int64
	var maxActive atomic.Int64
	started := make(chan struct{}, nodeCount)
	unblock := make(chan struct{})

	worker := func(ctx context.Context, state graph.State) (any, error) {
		cur := active.Add(1)
		updateMaxInt64(&maxActive, cur)
		started <- struct{}{}
		<-unblock
		active.Add(-1)
		return nil, nil
	}

	for i := 0; i < nodeCount; i++ {
		nodeID := fmt.Sprintf("w%d", i)
		stateGraph.AddNode(nodeID, worker)
		stateGraph.AddEdge(nodeRoot, nodeID)
	}

	g, err := stateGraph.Compile()
	require.NoError(t, err)

	graphAgent, err := New(
		"test-agent",
		g,
		WithMaxConcurrency(maxConcurrency),
	)
	require.NoError(t, err)

	invocation := &agent.Invocation{
		Agent:        graphAgent,
		AgentName:    "test-agent",
		InvocationID: "inv-max-concurrency",
	}

	events, err := graphAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()

	waitForNSignals(t, started, maxConcurrency, waitTimeout)

	select {
	case <-started:
		t.Fatalf("expected at most %d tasks to start", maxConcurrency)
	case <-time.After(200 * time.Millisecond):
	}

	close(unblock)

	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("timeout waiting for graphagent to complete")
	}

	require.LessOrEqual(t, maxActive.Load(), int64(maxConcurrency))
}

func TestGraphAgent_WithExecutionEngine_DagSchedulesEagerly(t *testing.T) {
	const (
		nodeEntry      = "preprocess"
		nodeSlow       = "slow"
		nodeFast       = "fast"
		nodeDownstream = "downstream"
		waitTimeout    = 2 * time.Second
	)

	slowRelease := make(chan struct{})
	slowStarted := make(chan struct{}, 1)
	fastDone := make(chan struct{}, 1)
	downStarted := make(chan struct{}, 1)

	notify := func(ch chan<- struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	stateGraph := graph.NewStateGraph(graph.NewStateSchema())
	stateGraph.AddNode(nodeEntry, func(context.Context, graph.State) (any, error) {
		return nil, nil
	})
	stateGraph.AddNode(
		nodeSlow,
		func(ctx context.Context, state graph.State) (any, error) {
			notify(slowStarted)
			select {
			case <-slowRelease:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	)
	stateGraph.AddNode(
		nodeFast,
		func(context.Context, graph.State) (any, error) {
			notify(fastDone)
			return nil, nil
		},
	)
	stateGraph.AddNode(
		nodeDownstream,
		func(context.Context, graph.State) (any, error) {
			notify(downStarted)
			return nil, nil
		},
	)
	stateGraph.SetEntryPoint(nodeEntry)
	stateGraph.AddEdge(nodeEntry, nodeSlow)
	stateGraph.AddEdge(nodeEntry, nodeFast)
	stateGraph.AddEdge(nodeFast, nodeDownstream)

	g, err := stateGraph.Compile()
	require.NoError(t, err)

	graphAgent, err := New(
		"test-agent",
		g,
		WithExecutionEngine(graph.ExecutionEngineDAG),
		WithMaxConcurrency(2),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()

	invocation := &agent.Invocation{
		Agent:        graphAgent,
		AgentName:    "test-agent",
		InvocationID: "inv-dag-engine",
	}

	events, err := graphAgent.Run(ctx, invocation)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()

	waitForNSignals(t, slowStarted, 1, waitTimeout)
	waitForNSignals(t, fastDone, 1, waitTimeout)
	waitForNSignals(t, downStarted, 1, waitTimeout)

	close(slowRelease)

	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("timeout waiting for graphagent to complete")
	}
}

func updateMaxInt64(max *atomic.Int64, value int64) {
	for {
		current := max.Load()
		if value <= current {
			return
		}
		if max.CompareAndSwap(current, value) {
			return
		}
	}
}

func waitForNSignals(
	t *testing.T,
	ch <-chan struct{},
	n int,
	timeout time.Duration,
) {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-timer.C:
			t.Fatalf("timeout waiting for %d signals", n)
		}
	}
}

func TestGraphAgentRun(t *testing.T) {
	// Create a simple graph using the new API.
	schema := graph.NewStateSchema().
		AddField("message", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("response", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("respond", func(ctx context.Context, state graph.State) (any, error) {
			message := state["message"].(string)
			return graph.State{"response": "Echo: " + message}, nil
		}).
		SetEntryPoint("respond").
		SetFinishPoint("respond").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create graph agent.
	initialState := graph.State{"message": "hello"}
	graphAgent, err := New("echo-agent", g, WithInitialState(initialState))
	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Test running the agent.
	invocation := &agent.Invocation{
		Agent:        graphAgent,
		AgentName:    "echo-agent",
		InvocationID: "test-invocation",
	}

	events, err := graphAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Collect events.
	eventCount := 0
	for range events {
		eventCount++
	}

	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}

func TestGraphAgentWithRuntimeState(t *testing.T) {
	// Create a simple graph that uses runtime state.
	schema := graph.NewStateSchema().
		AddField("user_id", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("room_id", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("base_value", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			// Verify that runtime state was merged correctly.
			userID, hasUserID := state["user_id"]
			roomID, hasRoomID := state["room_id"]
			baseValue, hasBaseValue := state["base_value"]

			if !hasUserID || !hasRoomID || !hasBaseValue {
				return nil, fmt.Errorf("missing expected state fields")
			}

			if userID != "user123" || roomID != "room456" || baseValue != "default" {
				return nil, fmt.Errorf("unexpected state values")
			}

			return graph.State{"status": "success"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create graph agent with base initial state.
	baseState := graph.State{"base_value": "default"}
	graphAgent, err := New("test-agent", g, WithInitialState(baseState))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test that runtime state is properly merged.
	ctx := context.Background()
	message := model.NewUserMessage("test message")

	// Create invocation with runtime state.
	invocation := &agent.Invocation{
		Message: message,
		RunOptions: agent.RunOptions{
			RuntimeState: graph.State{
				"user_id": "user123",
				"room_id": "room456",
			},
		},
	}

	// Run the agent.
	eventChan, err := graphAgent.Run(ctx, invocation)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Process events to ensure no errors occurred.
	eventCount := 0
	for range eventChan {
		eventCount++
	}

	// If we get here without errors, the runtime state was merged correctly.
	if eventCount == 0 {
		t.Error("Expected at least one event")
	}
}

func TestGraphAgentRuntimeStateOverridesBaseState(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			input := state["input"].(string)
			// Return a response that can be converted to model.Response.
			return &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "processed: " + input,
					},
				}},
			}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create GraphAgent with base initial state.
	graphAgent, err := New("test-agent", g,
		WithInitialState(graph.State{"input": "base input"}))
	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Test with runtime state that overrides base state.
	invocation := &agent.Invocation{
		Message: model.NewUserMessage("runtime input"),
		RunOptions: agent.RunOptions{
			RuntimeState: graph.State{"input": "runtime input"},
		},
	}

	events, err := graphAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Collect events.
	eventCount := 0
	for range events {
		eventCount++
	}

	if eventCount == 0 {
		t.Fatal("Expected at least one event")
	}

	// The test passes if we get here without errors, which means the runtime state override worked correctly.
}

func TestGraphAgentWithSubAgents(t *testing.T) {
	// Create a mock sub-agent.
	mockSubAgent := &mockAgent{
		name:         "sub-agent",
		eventCount:   1,
		eventContent: "Hello from sub-agent!",
	}

	// Create a simple graph that uses the sub-agent.
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddAgentNode("call_sub_agent",
			graph.WithName("Call Sub Agent"),
			graph.WithDescription("Calls the sub-agent to process the input"),
		).
		SetEntryPoint("call_sub_agent").
		SetFinishPoint("call_sub_agent").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// Create GraphAgent with sub-agents.
	graphAgent, err := New("test-agent", g,
		WithSubAgents([]agent.Agent{mockSubAgent}),
		WithDescription("Test agent with sub-agents"))
	if err != nil {
		t.Fatalf("Failed to create graph agent: %v", err)
	}

	// Test sub-agent methods.
	subAgents := graphAgent.SubAgents()
	if len(subAgents) != 1 {
		t.Errorf("Expected 1 sub-agent, got %d", len(subAgents))
	}

	foundSubAgent := graphAgent.FindSubAgent("sub-agent")
	if foundSubAgent == nil {
		t.Error("Expected to find sub-agent 'sub-agent'")
	}
	if foundSubAgent.Info().Name != "sub-agent" {
		t.Errorf("Expected sub-agent name 'sub-agent', got '%s'", foundSubAgent.Info().Name)
	}

	notFoundSubAgent := graphAgent.FindSubAgent("non-existent")
	if notFoundSubAgent != nil {
		t.Error("Expected to not find non-existent sub-agent")
	}

	// Test running the graph with sub-agent.
	invocation := &agent.Invocation{
		Message: model.NewUserMessage("test input"),
	}

	events, err := graphAgent.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Collect events.
	eventCount := 0
	for range events {
		eventCount++
	}

	if eventCount == 0 {
		t.Fatal("Expected at least one event")
	}

	// The test passes if we get here without errors, which means the sub-agent was called successfully.
}

// mockAgent is a test implementation of agent.Agent for testing sub-agents.
type mockAgent struct {
	name           string
	shouldError    bool
	eventCount     int
	eventContent   string
	executionOrder *[]string
	tools          []tool.Tool
}

type stubSummarizer struct {
	summary string
}

func (s *stubSummarizer) ShouldSummarize(_ *session.Session) bool { return true }
func (s *stubSummarizer) Summarize(_ context.Context, _ *session.Session) (string, error) {
	return s.summary, nil
}
func (s *stubSummarizer) SetPrompt(prompt string)  {}
func (s *stubSummarizer) SetModel(m model.Model)   {}
func (s *stubSummarizer) Metadata() map[string]any { return nil }

var _ summary.SessionSummarizer = (*stubSummarizer)(nil)

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

func (m *mockAgent) Tools() []tool.Tool {
	return m.tools
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if m.shouldError {
		return nil, errors.New("mock agent error")
	}

	ch := make(chan *event.Event, m.eventCount)
	go func() {
		defer close(ch)
		for i := 0; i < m.eventCount; i++ {
			response := &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: m.eventContent,
					},
				}},
			}
			evt := event.NewResponseEvent(invocation.InvocationID, m.name, response)
			select {
			case ch <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// TestGraphAgent_InvocationContextAccess verifies that GraphAgent can access invocation
// from context when called through runner (after removing duplicate injection).
func TestGraphAgent_InvocationContextAccess(t *testing.T) {
	// Create a simple graph agent.
	stateGraph := graph.NewStateGraph(nil)
	stateGraph.AddNode("test-node", func(ctx context.Context, state graph.State) (any, error) {
		// Verify that invocation is accessible from context.
		invocation, ok := agent.InvocationFromContext(ctx)
		if !ok || invocation == nil {
			return nil, fmt.Errorf("invocation not found in context")
		}

		// Return success state.
		return graph.State{
			"invocation_id": invocation.InvocationID,
			"agent_name":    invocation.AgentName,
			"status":        "success",
		}, nil
	})
	stateGraph.SetEntryPoint("test-node")
	stateGraph.SetFinishPoint("test-node")

	compiledGraph, err := stateGraph.Compile()
	require.NoError(t, err)

	graphAgent, err := New("test-graph-agent", compiledGraph)
	require.NoError(t, err)

	// Create invocation with context that contains invocation.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation-123",
		AgentName:    "test-graph-agent",
		Message:      model.NewUserMessage("Test invocation context access"),
	}

	// Create context with invocation (simulating what runner does).
	ctx := agent.NewInvocationContext(context.Background(), invocation)

	// Run the agent.
	eventCh, err := graphAgent.Run(ctx, invocation)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Collect events.
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Verify that the agent can access invocation from context.
	// This test ensures that even after removing the duplicate injection from LLMAgent,
	// GraphAgent can still access invocation when called through runner.
	require.Greater(t, len(events), 0)

	// The agent should have been able to run successfully, which means
	// it could access the invocation from context for any internal operations.
	t.Logf("GraphAgent successfully executed with %d events, confirming invocation context access", len(events))
}

// TestGraphAgent_WithCheckpointSaver tests the WithCheckpointSaver option.
func TestGraphAgent_WithCheckpointSaver(t *testing.T) {
	// Create a mock checkpoint saver.
	saver := &mockCheckpointSaver{}

	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("counter", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("increment", func(ctx context.Context, state graph.State) (any, error) {
			counter, _ := state["counter"].(int)
			return graph.State{"counter": counter + 1}, nil
		}).
		SetEntryPoint("increment").
		SetFinishPoint("increment").
		Compile()

	require.NoError(t, err)

	// Create graph agent with checkpoint saver.
	graphAgent, err := New("test-agent", g, WithCheckpointSaver(saver))
	require.NoError(t, err)
	require.NotNil(t, graphAgent)

	// Verify the executor is accessible.
	executor := graphAgent.Executor()
	require.NotNil(t, executor)
}

// TestGraphAgent_Tools tests the Tools method.
func TestGraphAgent_Tools(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "done"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	require.NoError(t, err)

	graphAgent, err := New("test-agent", g)
	require.NoError(t, err)

	// GraphAgent should return nil for tools.
	tools := graphAgent.Tools()
	require.Nil(t, tools)
}

// TestGraphAgent_CreateInitialStateWithSession tests createInitialState with session.
func TestGraphAgent_CreateInitialStateWithSession(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			// Check if messages from session were added.
			messages, ok := state[graph.StateKeyMessages]
			if !ok {
				return nil, fmt.Errorf("messages not found in state")
			}
			msgSlice, ok := messages.([]model.Message)
			if !ok || len(msgSlice) == 0 {
				return nil, fmt.Errorf("expected non-empty messages")
			}
			return graph.State{"status": "success"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	require.NoError(t, err)

	graphAgent, err := New("test-agent", g)
	require.NoError(t, err)

	// Create session with some events.
	sess := &session.Session{
		ID: "test-session",
		Events: []event.Event{
			{
				InvocationID: "inv-1",
				Response: &model.Response{
					Choices: []model.Choice{{
						Message: model.Message{Role: model.RoleUser, Content: "Hello"},
					}},
				},
			},
		},
	}

	// Create invocation with session.
	invocation := &agent.Invocation{
		Message: model.NewUserMessage("Test message"),
		Session: sess,
	}

	// Run the agent.
	eventChan, err := graphAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	// Collect events.
	eventCount := 0
	for range eventChan {
		eventCount++
	}

	require.Greater(t, eventCount, 0)
}

func TestGraphAgent_CreateInitialStateWithSessionSummary(t *testing.T) {
	const agentName = "test-agent"
	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return state, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	graphAgent, err := New(agentName, g, WithAddSessionSummary(true))
	require.NoError(t, err)

	sess := &session.Session{
		ID: "test-session",
		Summaries: map[string]*session.Summary{
			agentName: {
				Summary:   "branch summary content",
				UpdatedAt: time.Now(),
			},
		},
	}

	invocation := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationEventFilterKey(agentName),
	)
	graphAgent.setupInvocation(invocation)

	state := graphAgent.createInitialState(context.Background(), invocation)
	messages, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	require.True(t, ok)
	require.Len(t, messages, 2)
	require.Equal(t, model.RoleSystem, messages[0].Role)
	require.Contains(t, messages[0].Content, "branch summary content")
	require.Equal(t, model.RoleUser, messages[1].Role)
	require.Equal(t, "hello", messages[1].Content)
}

func TestGraphAgent_CreateInitialStateWithSessionSummary_Disabled(t *testing.T) {
	const agentName = "test-agent"
	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return state, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	graphAgent, err := New(agentName, g)
	require.NoError(t, err)

	sess := &session.Session{
		ID: "test-session",
		Summaries: map[string]*session.Summary{
			agentName: {
				Summary:   "branch summary content",
				UpdatedAt: time.Now(),
			},
		},
	}

	invocation := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationEventFilterKey(agentName),
	)
	graphAgent.setupInvocation(invocation)

	state := graphAgent.createInitialState(context.Background(), invocation)
	messages, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	require.True(t, ok)
	require.Len(t, messages, 1)
	require.Equal(t, model.RoleUser, messages[0].Role)
	require.Equal(t, "hello", messages[0].Content)
}

func TestGraphAgent_CreateInitialStateWithEventMessageProjector(
	t *testing.T,
) {
	const (
		agentName    = "test-agent"
		requestID    = "req-1"
		invocationID = "inv-1"
	)

	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(
			ctx context.Context,
			state graph.State,
		) (any, error) {
			return state, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	projector := func(
		_ *agent.Invocation,
		_ event.Event,
		msg model.Message,
	) model.Message {
		if msg.Role != model.RoleUser {
			return msg
		}
		msg.Content = "Projected: " + msg.Content
		return msg
	}

	graphAgent, err := New(
		agentName,
		g,
		WithEventMessageProjector(projector),
	)
	require.NoError(t, err)

	userEvt := event.NewResponseEvent(
		invocationID,
		"user",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("hello"),
			}},
		},
	)
	userEvt.RequestID = requestID
	userEvt.FilterKey = agentName

	sess := &session.Session{
		ID:     "test-session",
		Events: []event.Event{*userEvt},
	}
	invocation := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(
			model.NewUserMessage("hello"),
		),
		agent.WithInvocationEventFilterKey(agentName),
	)
	invocation.InvocationID = invocationID
	invocation.RunOptions.RequestID = requestID
	graphAgent.setupInvocation(invocation)

	state := graphAgent.createInitialState(
		context.Background(),
		invocation,
	)
	messages, ok := graph.GetStateValue[[]model.Message](
		state,
		graph.StateKeyMessages,
	)
	require.True(t, ok)
	require.Len(t, messages, 1)
	require.Equal(t, "Projected: hello", messages[0].Content)
}

func TestGraphAgent_CreateInitialStateWithSessionSummary_FromService(t *testing.T) {
	const agentName = "test-agent"
	ctx := context.Background()

	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return state, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	graphAgent, err := New(agentName, g, WithAddSessionSummary(true))
	require.NoError(t, err)

	// Session service with a stub summarizer to emulate real summarization flow.
	sum := &stubSummarizer{summary: "auto summary from service"}
	sessSvc := inmemory.NewSessionService(inmemory.WithSummarizer(sum))
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := sessSvc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	evt := event.NewResponseEvent("inv-1", agentName, &model.Response{
		Choices: []model.Choice{{Message: model.Message{
			Role:    model.RoleUser,
			Content: "hi there",
		}}},
	})
	evt.FilterKey = agentName
	require.NoError(t, sessSvc.AppendEvent(ctx, sess, evt))
	require.NoError(t, sessSvc.CreateSessionSummary(ctx, sess, agentName, true))

	// Reload session to ensure we read persisted summaries.
	storedSess, err := sessSvc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, storedSess)

	invocation := agent.NewInvocation(
		agent.WithInvocationSession(storedSess),
		agent.WithInvocationMessage(model.NewUserMessage("next turn")),
		agent.WithInvocationEventFilterKey(agentName),
	)
	graphAgent.setupInvocation(invocation)

	state := graphAgent.createInitialState(ctx, invocation)
	messages, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(messages), 1)
	require.Equal(t, model.RoleSystem, messages[0].Role)
	require.Contains(t, messages[0].Content, "auto summary from service")
	// Summary already covers prior history, so the latest run may start with summary only.
}

func TestGraphAgent_CreateInitialStateWithContextCompaction(t *testing.T) {
	const agentName = "test-agent"
	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return state, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	graphAgent, err := New(
		agentName,
		g,
		WithAddSessionSummary(true),
		WithEnableContextCompaction(true),
		WithContextCompactionToolResultMaxTokens(10),
		WithContextCompactionKeepRecentRequests(1),
	)
	require.NoError(t, err)

	sess := &session.Session{
		ID: "test-session",
		Events: []event.Event{
			{
				RequestID: "req-old",
				FilterKey: agentName,
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID: "tool-call-old",
								Function: model.FunctionDefinitionParam{
									Name:      "worker",
									Arguments: []byte(`{}`),
								},
							}},
						},
					}},
				},
			},
			{
				RequestID: "req-old",
				FilterKey: agentName,
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewToolMessage(
							"tool-call-old",
							"worker",
							strings.Repeat("old-result ", 64),
						),
					}},
				},
			},
			{
				RequestID: "req-recent",
				FilterKey: agentName,
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID: "tool-call-recent",
								Function: model.FunctionDefinitionParam{
									Name:      "worker",
									Arguments: []byte(`{}`),
								},
							}},
						},
					}},
				},
			},
			{
				RequestID: "req-recent",
				FilterKey: agentName,
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewToolMessage(
							"tool-call-recent",
							"worker",
							strings.Repeat("recent-result ", 64),
						),
					}},
				},
			},
		},
	}

	invocation := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
		agent.WithInvocationEventFilterKey(agentName),
	)
	graphAgent.setupInvocation(invocation)

	state := graphAgent.createInitialState(context.Background(), invocation)
	messages, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	require.True(t, ok)
	require.Len(t, messages, 5)
	require.Equal(t, model.RoleAssistant, messages[0].Role)
	require.Equal(t, model.RoleTool, messages[1].Role)
	require.Equal(t, "Historical tool result omitted to save context.", messages[1].Content)
	require.Equal(t, "tool-call-old", messages[1].ToolID)
	require.Equal(t, "worker", messages[1].ToolName)
	require.Equal(t, model.RoleAssistant, messages[2].Role)
	require.Contains(t, messages[3].Content, "recent-result")
	require.Equal(t, model.RoleTool, messages[3].Role)
	require.Equal(t, "tool-call-recent", messages[3].ToolID)
	require.Equal(t, "worker", messages[3].ToolName)
	require.Equal(t, "hello", messages[4].Content)
}

// TestGraphAgent_CreateInitialStateWithResume tests checkpoint resume behavior.
func TestGraphAgent_CreateInitialStateWithResume(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("user_input", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			// Check if user_input is present or not based on resume signal.
			userInput, hasInput := state[graph.StateKeyUserInput]
			if hasInput {
				return graph.State{"processed": userInput}, nil
			}
			return graph.State{"processed": "no input"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	require.NoError(t, err)

	graphAgent, err := New("test-agent", g)
	require.NoError(t, err)

	// Test resume with "resume" message - should skip adding user_input.
	invocation := &agent.Invocation{
		Message: model.NewUserMessage("resume"),
		RunOptions: agent.RunOptions{
			RuntimeState: graph.State{
				graph.CfgKeyCheckpointID: "checkpoint-123",
			},
		},
	}

	eventChan, err := graphAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	// Collect events.
	for range eventChan {
		// Just drain the channel.
	}

	// Test resume with meaningful message - should add user_input.
	invocation2 := &agent.Invocation{
		Message: model.NewUserMessage("meaningful input"),
		RunOptions: agent.RunOptions{
			RuntimeState: graph.State{
				graph.CfgKeyCheckpointID: "checkpoint-123",
			},
		},
	}

	eventChan2, err := graphAgent.Run(context.Background(), invocation2)
	require.NoError(t, err)

	// Collect events.
	for range eventChan2 {
		// Just drain the channel.
	}
}

func TestGraphAgent_CreateInitialStateWithToolMessageDoesNotSetUserInput(t *testing.T) {
	schema := graph.NewStateSchema().
		AddField("messages", graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		}).
		AddField(graph.StateKeyUserInput, graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return state, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	graphAgent, err := New("test-agent", g)
	require.NoError(t, err)

	toolMsg := model.NewToolMessage("call-1", "calc", "result")
	toolEvt := event.NewResponseEvent("inv", "test-agent", &model.Response{
		Choices: []model.Choice{{Index: 0, Message: toolMsg}},
	})

	sess := &session.Session{
		ID:     "sid",
		Events: []event.Event{*toolEvt},
	}

	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv"),
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(toolMsg),
	)
	graphAgent.setupInvocation(invocation)

	state := graphAgent.createInitialState(context.Background(), invocation)
	_, hasUserInput := state[graph.StateKeyUserInput]
	require.False(t, hasUserInput)

	messages, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	require.True(t, ok)
	require.NotEmpty(t, messages)
	require.Equal(t, model.RoleTool, messages[len(messages)-1].Role)
	require.Equal(t, "call-1", messages[len(messages)-1].ToolID)
	require.Equal(t, "result", messages[len(messages)-1].Content)
}

// mockCheckpointSaver is a mock implementation of graph.CheckpointSaver.
type mockCheckpointSaver struct{}

func (m *mockCheckpointSaver) Get(ctx context.Context, config map[string]any) (*graph.Checkpoint, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) GetTuple(ctx context.Context, config map[string]any) (*graph.CheckpointTuple, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) List(ctx context.Context, config map[string]any, filter *graph.CheckpointFilter) ([]*graph.CheckpointTuple, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) Put(ctx context.Context, req graph.PutRequest) (map[string]any, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) PutWrites(ctx context.Context, req graph.PutWritesRequest) error {
	return nil
}

func (m *mockCheckpointSaver) PutFull(ctx context.Context, req graph.PutFullRequest) (map[string]any, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) DeleteLineage(ctx context.Context, lineageID string) error {
	return nil
}

func (m *mockCheckpointSaver) Close() error {
	return nil
}

func TestGraphAgent_MessageFilterMode(t *testing.T) {
	options := &Options{
		messageBranchFilterMode: "prefix",
	}
	WithMessageTimelineFilterMode("all")(options)

	require.Equal(t, options.messageTimelineFilterMode, "all")
	require.Equal(t, options.messageBranchFilterMode, "prefix")

	options = &Options{
		messageBranchFilterMode:   "prefix",
		messageTimelineFilterMode: "all",
	}
	WithMessageTimelineFilterMode("request")(options)
	WithMessageBranchFilterMode("exact")(options)

	require.Equal(t, options.messageTimelineFilterMode, "request")
	require.Equal(t, options.messageBranchFilterMode, "exact")
}

func TestGraphAgent_BeforeCallbackReturnsResponse(t *testing.T) {
	// Create a minimal graph.
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("noop", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "should not run"}, nil
		}).
		SetEntryPoint("noop").
		SetFinishPoint("noop").
		Compile()
	require.NoError(t, err)

	// Create callbacks that return early.
	callbacks := agent.NewCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return &model.Response{
			Object: "before.custom",
			Done:   true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "early return",
				},
			}},
		}, nil
	})

	// Create graph agent with callbacks.
	ga, err := New("test-before", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv-before",
		AgentName:    "test-before",
	}

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	// Collect events.
	var collected []*event.Event
	for e := range events {
		collected = append(collected, e)
	}

	require.Len(t, collected, 1)
	require.Equal(t, "before.custom", collected[0].Object)
	require.Equal(t, "early return", collected[0].Response.Choices[0].Message.Content)
}

func TestGraphAgent_BeforeCallbackReturnsError(t *testing.T) {
	// Create a minimal graph.
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("noop", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "should not run"}, nil
		}).
		SetEntryPoint("noop").
		SetFinishPoint("noop").
		Compile()
	require.NoError(t, err)

	// Create callbacks that return error.
	callbacks := agent.NewCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return nil, errors.New("before callback failed")
	})

	// Create graph agent with callbacks.
	ga, err := New("test-before-err", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv-before-err",
		AgentName:    "test-before-err",
	}

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	// Collect events and expect a final error event.
	var collected []*event.Event
	for e := range events {
		collected = append(collected, e)
	}
	require.Len(t, collected, 1)
	require.NotNil(t, collected[0].Error)
	require.Equal(t, model.ErrorTypeFlowError, collected[0].Error.Type)
	require.Contains(t, collected[0].Error.Message, "before callback failed")
}

func TestGraphAgent_AfterCallbackReturnsResponse(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "processed"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	// Create callbacks with after agent.
	callbacks := agent.NewCallbacks()
	var callbackErr error
	callbacks.RegisterAfterAgent(func(
		ctx context.Context,
		inv *agent.Invocation,
		err error,
	) (*model.Response, error) {
		callbackErr = err
		return &model.Response{
			Object: "after.custom",
			Done:   true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "after callback",
				},
			}},
		}, nil
	})

	// Create graph agent with callbacks.
	ga, err := New("test-after", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv-after",
		AgentName:    "test-after",
		Message:      model.NewUserMessage("test"),
	}

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	// Collect events.
	var collected []*event.Event
	for e := range events {
		collected = append(collected, e)
	}

	// Should have graph execution event(s) plus after callback event.
	require.Greater(t, len(collected), 0)

	// After-callback in success path should see nil error.
	require.NoError(t, callbackErr)

	// Last event should be from after callback.
	last := collected[len(collected)-1]
	require.Equal(t, "after.custom", last.Object)
	require.Equal(t, "after callback", last.Response.Choices[0].Message.Content)
}

func TestGraphAgent_AfterCallbackReturnsError(t *testing.T) {
	// Create a simple graph.
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "processed"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	// Create callbacks with after agent error.
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, err error) (*model.Response, error) {
		return nil, errors.New("after callback failed")
	})

	// Create graph agent with callbacks.
	ga, err := New("test-after-err", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv-after-err",
		AgentName:    "test-after-err",
		Message:      model.NewUserMessage("test"),
	}

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	// Collect events.
	var collected []*event.Event
	for e := range events {
		collected = append(collected, e)
	}

	// Should have graph execution event(s) plus after callback error event.
	require.Greater(t, len(collected), 0)

	// Last event should be error from after callback.
	last := collected[len(collected)-1]
	require.NotNil(t, last.Error)
	require.Equal(t, agent.ErrorTypeAgentCallbackError, last.Error.Type)
	require.Contains(t, last.Error.Message, "after callback failed")
}

func TestGraphAgent_AfterCallbackReceivesExecutionError(t *testing.T) {
	// Create a simple graph that fails at the node.
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("fail", func(
			ctx context.Context,
			state graph.State,
		) (any, error) {
			return nil, fmt.Errorf("node failed")
		}).
		SetEntryPoint("fail").
		SetFinishPoint("fail").
		Compile()
	require.NoError(t, err)

	// After-agent callback should receive non-nil error derived from the
	// final response event.
	callbacks := agent.NewCallbacks()
	var callbackErr error
	callbacks.RegisterAfterAgent(func(
		ctx context.Context,
		inv *agent.Invocation,
		err error,
	) (*model.Response, error) {
		callbackErr = err
		return nil, nil
	})

	ga, err := New(
		"test-after-exec-err",
		g,
		WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv-after-exec-err",
		AgentName:    "test-after-exec-err",
		Message:      model.NewUserMessage("test"),
	}

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	// Drain all events to ensure after-callback has run.
	for range events {
	}

	require.Error(t, callbackErr)
	require.Contains(t, callbackErr.Error(), "flow_error:")
}

func TestGraphAgent_DisableGraphCompletionEvent_PreservesAfterAgentResponse(t *testing.T) {
	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
		}).
		SetEntryPoint("done").
		SetFinishPoint("done").
		Compile()
	require.NoError(t, err)
	callbacks := agent.NewCallbacks()
	var fullRespEvent *event.Event
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		fullRespEvent = args.FullResponseEvent
		return nil, nil
	})
	ga, err := New(
		"test-after-hidden-completion",
		g,
		WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)
	for evt := range events {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
	}
	require.NotNil(t, fullRespEvent)
	require.NotNil(t, fullRespEvent.Response)
	require.Len(t, fullRespEvent.Response.Choices, 1)
	require.Equal(t, "child-final", fullRespEvent.Response.Choices[0].Message.Content)
}

func TestGraphAgent_DisableGraphCompletionEvent_PreservesOutputWithCaptureContext(t *testing.T) {
	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
		}).
		SetEntryPoint("done").
		SetFinishPoint("done").
		Compile()
	require.NoError(t, err)
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		return nil, nil
	})
	ga, err := New(
		"test-after-hidden-completion-capture",
		g,
		WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	events, err := ga.Run(graph.WithGraphCompletionCapture(context.Background()), inv)
	require.NoError(t, err)
	var sawGraphCompletion bool
	for evt := range events {
		if evt.Done && evt.Object == graph.ObjectTypeGraphExecution {
			sawGraphCompletion = true
		}
	}
	require.True(t, sawGraphCompletion)
}

func TestGraphAgent_DisableGraphCompletionEvent_WithCaptureContext_AfterCallbackSeesVisibleCompletion(
	t *testing.T,
) {
	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{graph.StateKeyLastResponse: "child-final"}, nil
		}).
		SetEntryPoint("done").
		SetFinishPoint("done").
		Compile()
	require.NoError(t, err)
	callbacks := agent.NewCallbacks()
	var fullRespEvent *event.Event
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		fullRespEvent = args.FullResponseEvent
		return nil, nil
	})
	ga, err := New(
		"test-after-hidden-completion-capture-visible",
		g,
		WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	events, err := ga.Run(graph.WithGraphCompletionCapture(context.Background()), inv)
	require.NoError(t, err)
	for range events {
	}
	require.NotNil(t, fullRespEvent)
	require.True(t, graph.IsVisibleGraphCompletionEvent(fullRespEvent))
	require.Len(t, fullRespEvent.Response.Choices, 1)
	require.Equal(t, "child-final", fullRespEvent.Response.Choices[0].Message.Content)
}

func TestGraphAgent_DisableGraphCompletionEvent_PreservesVisibleResponseWithoutCallbacks(t *testing.T) {
	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{
				graph.StateKeyLastResponse: "child-final",
				"child_state":              "child-state",
			}, nil
		}).
		SetEntryPoint("done").
		SetFinishPoint("done").
		Compile()
	require.NoError(t, err)
	ga, err := New("test-hidden-completion-visible-response", g)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)
	var visibleEvent *event.Event
	for evt := range events {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt != nil && evt.Response != nil && !evt.Response.IsPartial && len(evt.StateDelta) > 0 {
			visibleEvent = evt
		}
	}
	require.NotNil(t, visibleEvent)
	require.Equal(t, model.ObjectTypeChatCompletion, visibleEvent.Object)
	require.Equal(t, "test-hidden-completion-visible-response", visibleEvent.Author)
	require.Len(t, visibleEvent.Response.Choices, 1)
	require.Equal(t, "child-final", visibleEvent.Response.Choices[0].Message.Content)
	require.Equal(t, []byte(`"child-final"`), visibleEvent.StateDelta[graph.StateKeyLastResponse])
	require.Equal(t, []byte(`"child-state"`), visibleEvent.StateDelta["child_state"])
}

func TestGraphAgent_DisableGraphCompletionEvent_PreservesStateOnlyVisibleResponse(t *testing.T) {
	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{
				"child_state": "child-state",
			}, nil
		}).
		SetEntryPoint("done").
		SetFinishPoint("done").
		Compile()
	require.NoError(t, err)
	ga, err := New("test-hidden-completion-state-only", g)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)
	var visibleEvent *event.Event
	for evt := range events {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt != nil && evt.Object == model.ObjectTypeChatCompletion && len(evt.StateDelta) > 0 {
			visibleEvent = evt
		}
	}
	require.NotNil(t, visibleEvent)
	require.Empty(t, visibleEvent.Response.Choices)
	require.Equal(t, []byte(`"child-state"`), visibleEvent.StateDelta["child_state"])
}

func TestGraphAgent_DisableGraphCompletionEvent_GraphEmitFinalModelResponses_DedupsVisibleCompletion(t *testing.T) {
	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			"n1",
			&staticGraphAgentModel{name: "m1", content: "answer"},
			"i1",
			nil,
		).
		SetEntryPoint("n1").
		SetFinishPoint("n1").
		Compile()
	require.NoError(t, err)
	ga, err := New("test-hidden-completion-dedup", g)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
			agent.WithGraphEmitFinalModelResponses(true),
		)),
	)
	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)
	var assistantResponses int
	var visibleEvent *event.Event
	for evt := range events {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt != nil && evt.Response != nil && len(evt.Response.Choices) > 0 {
			require.Equal(t, "answer", evt.Response.Choices[0].Message.Content)
			assistantResponses++
		}
		if evt != nil && evt.Object == model.ObjectTypeChatCompletion && len(evt.StateDelta) > 0 {
			visibleEvent = evt
		}
	}
	require.Equal(t, 1, assistantResponses)
	require.NotNil(t, visibleEvent)
	require.Empty(t, visibleEvent.Response.Choices)
	require.Equal(t, []byte(`"answer"`), visibleEvent.StateDelta[graph.StateKeyLastResponse])
}

func TestGraphAgent_DisableGraphCompletionEvent_GraphEmitFinalModelResponses_DedupsVisibleCompletionWhenResponseIDEmpty(
	t *testing.T,
) {
	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			"n1",
			&emptyIDGraphAgentModel{name: "m-empty", content: "answer"},
			"i1",
			nil,
		).
		SetEntryPoint("n1").
		SetFinishPoint("n1").
		Compile()
	require.NoError(t, err)
	ga, err := New("test-hidden-completion-dedup-empty-id", g)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
			agent.WithGraphEmitFinalModelResponses(true),
		)),
	)
	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	var assistantResponses int
	var visibleEvent *event.Event
	for evt := range events {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt != nil && evt.Response != nil && len(evt.Response.Choices) > 0 && len(evt.StateDelta) == 0 {
			require.Len(t, evt.Response.Choices, 1)
			require.Equal(t, "answer", evt.Response.Choices[0].Message.Content)
			assistantResponses++
		}
		if graph.IsVisibleGraphCompletionEvent(evt) {
			visibleEvent = evt
		}
	}

	require.Equal(t, 1, assistantResponses)
	require.NotNil(t, visibleEvent)
	require.Empty(t, visibleEvent.Response.Choices)
	require.Equal(t, []byte(`"answer"`), visibleEvent.StateDelta[graph.StateKeyLastResponse])
}

func TestGraphAgent_DisableGraphCompletionEvent_GraphEmitFinalModelResponses_AfterCallbackSeesFullVisibleCompletion(
	t *testing.T,
) {
	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			"n1",
			&staticGraphAgentModel{name: "m1", content: "answer"},
			"i1",
			nil,
		).
		SetEntryPoint("n1").
		SetFinishPoint("n1").
		Compile()
	require.NoError(t, err)
	callbacks := agent.NewCallbacks()
	var fullRespEvent *event.Event
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		fullRespEvent = args.FullResponseEvent
		return nil, nil
	})
	ga, err := New(
		"test-hidden-completion-dedup-after-callback",
		g,
		WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
			agent.WithGraphEmitFinalModelResponses(true),
		)),
	)
	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}

	require.NotNil(t, fullRespEvent)
	require.True(t, graph.IsVisibleGraphCompletionEvent(fullRespEvent))
	require.Len(t, fullRespEvent.Response.Choices, 1)
	require.Equal(t, "answer", fullRespEvent.Response.Choices[0].Message.Content)
}

func TestGraphAgent_GraphTerminalMessagesOnly_DefaultCompatibility(
	t *testing.T,
) {
	ga := newSerialTerminalMessageGraphAgent(t, "", "")
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithGraphEmitFinalModelResponses(true),
		)),
	)

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	var sawFirstChunk bool
	var sawFinalChunk bool
	firstPrefix := scopePrefix(ga.Info().Name, "first")
	finalPrefix := scopePrefix(ga.Info().Name, "final")
	for evt := range events {
		if !evt.IsPartial || evt.Response == nil ||
			len(evt.Response.Choices) == 0 {
			continue
		}
		if hasFilterPrefix(evt.FilterKey, firstPrefix) &&
			evt.Response.Choices[0].Delta.Content == "draft" {
			sawFirstChunk = true
		}
		if hasFilterPrefix(evt.FilterKey, finalPrefix) &&
			evt.Response.Choices[0].Delta.Content == "answer" {
			sawFinalChunk = true
		}
	}
	require.True(t, sawFirstChunk)
	require.True(t, sawFinalChunk)
}

func TestGraphAgent_GraphTerminalMessagesOnly_HidesIntermediateAgentNodes(
	t *testing.T,
) {
	firstScope := "scopes/first"
	finalScope := "scopes/final"
	ga := newSerialTerminalMessageGraphAgent(t, firstScope, finalScope)
	firstNode, ok := ga.graph.Node("first")
	require.True(t, ok)
	require.False(t, isTerminalMessageNode(ga.graph, firstNode))
	finalNode, ok := ga.graph.Node("final")
	require.True(t, ok)
	require.True(t, isTerminalMessageNode(ga.graph, finalNode))
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithGraphEmitFinalModelResponses(true),
			agent.WithGraphTerminalMessagesOnly(true),
		)),
	)

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	var sawFirstChunk bool
	var sawFinalChunk bool
	var firstChunkFilterKey string
	var firstChunkAuthor string
	firstPrefix := scopePrefix(ga.Info().Name, firstScope)
	finalPrefix := scopePrefix(ga.Info().Name, finalScope)
	for evt := range events {
		if !evt.IsPartial || evt.Response == nil ||
			len(evt.Response.Choices) == 0 {
			continue
		}
		if hasFilterPrefix(evt.FilterKey, firstPrefix) &&
			evt.Response.Choices[0].Delta.Content == "draft" {
			sawFirstChunk = true
			firstChunkFilterKey = evt.FilterKey
			firstChunkAuthor = evt.Author
		}
		if hasFilterPrefix(evt.FilterKey, finalPrefix) &&
			evt.Response.Choices[0].Delta.Content == "answer" {
			sawFinalChunk = true
		}
	}
	require.Falsef(
		t,
		sawFirstChunk,
		"first chunk filter=%q author=%q",
		firstChunkFilterKey,
		firstChunkAuthor,
	)
	require.True(t, sawFinalChunk)
}

func TestGraphAgent_GraphTerminalMessagesOnly_HidesIntermediateAgentNodes_NoBarrier(
	t *testing.T,
) {
	ga := newSerialTerminalMessageGraphAgent(t, "", "")
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableTracing(true),
			agent.WithGraphEmitFinalModelResponses(true),
			agent.WithGraphTerminalMessagesOnly(true),
		)),
	)

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	var sawFirstChunk bool
	var sawFinalChunk bool
	firstPrefix := scopePrefix(ga.Info().Name, "first")
	finalPrefix := scopePrefix(ga.Info().Name, "final")
	for evt := range events {
		if !evt.IsPartial || evt.Response == nil ||
			len(evt.Response.Choices) == 0 {
			continue
		}
		if hasFilterPrefix(evt.FilterKey, firstPrefix) &&
			evt.Response.Choices[0].Delta.Content == "draft" {
			sawFirstChunk = true
		}
		if hasFilterPrefix(evt.FilterKey, finalPrefix) &&
			evt.Response.Choices[0].Delta.Content == "answer" {
			sawFinalChunk = true
		}
	}
	require.False(t, sawFirstChunk)
	require.True(t, sawFinalChunk)
}

func TestGraphAgent_GraphTerminalMessagesOnly_KeepsParallelTerminalNodes(
	t *testing.T,
) {
	workerA := newStreamingChildGraphAgent(t, "final_a", "A")
	workerB := newStreamingChildGraphAgent(t, "final_b", "B")

	parentGraph, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddNode(
			"start",
			func(ctx context.Context, state graph.State) (any, error) {
				return graph.State{}, nil
			},
		).
		AddAgentNode("final_a").
		AddAgentNode("final_b").
		AddEdge("start", "final_a").
		AddEdge("start", "final_b").
		SetEntryPoint("start").
		SetFinishPoint("final_a").
		SetFinishPoint("final_b").
		Compile()
	require.NoError(t, err)

	ga, err := New(
		"parallel-terminal-message-filter",
		parentGraph,
		WithSubAgents([]agent.Agent{workerA, workerB}),
	)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithGraphEmitFinalModelResponses(true),
			agent.WithGraphTerminalMessagesOnly(true),
		)),
	)

	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)

	var sawA bool
	var sawB bool
	prefixA := scopePrefix(ga.Info().Name, "final_a")
	prefixB := scopePrefix(ga.Info().Name, "final_b")
	for evt := range events {
		if !evt.IsPartial || evt.Response == nil ||
			len(evt.Response.Choices) == 0 {
			continue
		}
		if hasFilterPrefix(evt.FilterKey, prefixA) &&
			evt.Response.Choices[0].Delta.Content == "A" {
			sawA = true
		}
		if hasFilterPrefix(evt.FilterKey, prefixB) &&
			evt.Response.Choices[0].Delta.Content == "B" {
			sawB = true
		}
	}
	require.True(t, sawA)
	require.True(t, sawB)
}

func TestTerminalMessageRootFilterKey(t *testing.T) {
	const (
		rootFilterKey = "root/filter"
		rootAgentName = "root-agent"
	)

	require.Empty(t, terminalMessageRootFilterKey(nil))

	invWithFilter := agent.NewInvocation(
		agent.WithInvocationEventFilterKey(rootFilterKey),
	)
	invWithFilter.AgentName = rootAgentName
	require.Equal(
		t,
		rootFilterKey,
		terminalMessageRootFilterKey(invWithFilter),
	)

	invWithAgentName := agent.NewInvocation()
	invWithAgentName.AgentName = rootAgentName
	require.Equal(
		t,
		rootAgentName,
		terminalMessageRootFilterKey(invWithAgentName),
	)
}

func TestMatchesFilterPrefix(t *testing.T) {
	const (
		rootPrefix = "root/child"
		childKey   = "root/child/grandchild"
	)

	require.True(t, matchesFilterPrefix(rootPrefix, rootPrefix))
	require.True(t, matchesFilterPrefix(childKey, rootPrefix))
	require.False(t, matchesFilterPrefix("root/other", rootPrefix))
	require.False(t, matchesFilterPrefix("", rootPrefix))
	require.False(t, matchesFilterPrefix(rootPrefix, ""))
}

func TestTerminalAgentScopePrefix(t *testing.T) {
	const (
		rootFilterKey = "root"
		agentID       = "child"
		customScope   = "scope/child"
	)

	require.Empty(t, terminalAgentScopePrefix(rootFilterKey, nil))
	require.Empty(t, terminalAgentScopePrefix("", &graph.Node{}))

	defaultScopeGraph, err := graph.NewStateGraph(
		graph.MessagesStateSchema(),
	).
		AddAgentNode(agentID).
		SetEntryPoint(agentID).
		SetFinishPoint(agentID).
		Compile()
	require.NoError(t, err)
	defaultScopeNode, ok := defaultScopeGraph.Node(agentID)
	require.True(t, ok)
	require.Equal(
		t,
		agentID,
		terminalAgentScopePrefix("", defaultScopeNode),
	)

	customScopeGraph, err := graph.NewStateGraph(
		graph.MessagesStateSchema(),
	).
		AddAgentNode(agentID, graph.WithSubgraphEventScope(customScope)).
		SetEntryPoint(agentID).
		SetFinishPoint(agentID).
		Compile()
	require.NoError(t, err)
	customScopeNode, ok := customScopeGraph.Node(agentID)
	require.True(t, ok)
	require.Equal(
		t,
		scopePrefix(rootFilterKey, customScope),
		terminalAgentScopePrefix(rootFilterKey, customScopeNode),
	)
}

func TestIsTerminalMessageNode(t *testing.T) {
	const (
		firstNodeID        = "first"
		finalNodeID        = "final"
		routerNodeID       = "router"
		nextNodeID         = "next"
		directRouterNodeID = "direct_router"
	)

	require.False(t, isTerminalMessageNode(nil, nil))

	serialGraph := newSerialTerminalMessageGraphAgent(t, "", "").graph
	firstNode, ok := serialGraph.Node(firstNodeID)
	require.True(t, ok)
	require.False(t, isTerminalMessageNode(serialGraph, firstNode))
	finalNode, ok := serialGraph.Node(finalNodeID)
	require.True(t, ok)
	require.True(t, isTerminalMessageNode(serialGraph, finalNode))

	endsGraph, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddAgentNode(
			routerNodeID,
			graph.WithEndsMap(map[string]string{
				nextNodeID: nextNodeID,
			}),
		).
		AddNode(
			nextNodeID,
			func(ctx context.Context, state graph.State) (any, error) {
				return graph.State{}, nil
			},
		).
		SetEntryPoint(routerNodeID).
		Compile()
	require.NoError(t, err)
	routerNode, ok := endsGraph.Node(routerNodeID)
	require.True(t, ok)
	require.False(t, isTerminalMessageNode(endsGraph, routerNode))

	endOnlyGraph, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddAgentNode(
			routerNodeID,
			graph.WithEndsMap(map[string]string{
				"done": graph.End,
			}),
		).
		SetEntryPoint(routerNodeID).
		SetFinishPoint(routerNodeID).
		Compile()
	require.NoError(t, err)
	endOnlyNode, ok := endOnlyGraph.Node(routerNodeID)
	require.True(t, ok)
	require.True(t, isTerminalMessageNode(endOnlyGraph, endOnlyNode))

	conditionalGraph, err := graph.NewStateGraph(
		graph.MessagesStateSchema(),
	).
		AddAgentNode(routerNodeID).
		AddNode(
			nextNodeID,
			func(ctx context.Context, state graph.State) (any, error) {
				return graph.State{}, nil
			},
		).
		AddConditionalEdges(
			routerNodeID,
			func(ctx context.Context, state graph.State) (string, error) {
				return nextNodeID, nil
			},
			map[string]string{
				nextNodeID: nextNodeID,
			},
		).
		SetEntryPoint(routerNodeID).
		Compile()
	require.NoError(t, err)
	conditionalNode, ok := conditionalGraph.Node(routerNodeID)
	require.True(t, ok)
	require.False(t, isTerminalMessageNode(conditionalGraph, conditionalNode))

	directConditionalGraph, err := graph.NewStateGraph(
		graph.MessagesStateSchema(),
	).
		AddAgentNode(directRouterNodeID).
		AddNode(
			nextNodeID,
			func(ctx context.Context, state graph.State) (any, error) {
				return graph.State{}, nil
			},
		).
		AddConditionalEdges(
			directRouterNodeID,
			func(ctx context.Context, state graph.State) (string, error) {
				return nextNodeID, nil
			},
			nil,
		).
		SetEntryPoint(directRouterNodeID).
		Compile()
	require.NoError(t, err)
	directConditionalNode, ok := directConditionalGraph.Node(
		directRouterNodeID,
	)
	require.True(t, ok)
	require.False(
		t,
		isTerminalMessageNode(
			directConditionalGraph,
			directConditionalNode,
		),
	)

	endConditionalGraph, err := graph.NewStateGraph(
		graph.MessagesStateSchema(),
	).
		AddAgentNode(routerNodeID).
		AddConditionalEdges(
			routerNodeID,
			func(ctx context.Context, state graph.State) (string, error) {
				return "done", nil
			},
			map[string]string{
				"done": graph.End,
			},
		).
		SetEntryPoint(routerNodeID).
		SetFinishPoint(routerNodeID).
		Compile()
	require.NoError(t, err)
	endConditionalNode, ok := endConditionalGraph.Node(routerNodeID)
	require.True(t, ok)
	require.True(
		t,
		isTerminalMessageNode(endConditionalGraph, endConditionalNode),
	)
}

func TestNewTerminalMessageFilter(t *testing.T) {
	const (
		rootFilterKey = "root"
		rootAgentName = "graph-agent"
		draftNodeID   = "draft"
		finalNodeID   = "final"
		firstNodeID   = "first"
		finalScope    = "scope/final"
	)

	require.False(t, newTerminalMessageFilter(nil, nil).enabled)

	disabledInv := agent.NewInvocation()
	disabledGraph := newSerialTerminalMessageGraphAgent(t, "", "").graph
	require.False(
		t,
		newTerminalMessageFilter(disabledInv, disabledGraph).enabled,
	)

	llmGraph, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			draftNodeID,
			&staticGraphAgentModel{content: "draft"},
			"inst",
			nil,
		).
		AddLLMNode(
			finalNodeID,
			&staticGraphAgentModel{content: "final"},
			"inst",
			nil,
		).
		AddEdge(draftNodeID, finalNodeID).
		SetEntryPoint(draftNodeID).
		SetFinishPoint(finalNodeID).
		Compile()
	require.NoError(t, err)

	llmInv := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithGraphTerminalMessagesOnly(true),
		)),
		agent.WithInvocationEventFilterKey(rootFilterKey),
	)
	llmFilter := newTerminalMessageFilter(llmInv, llmGraph)
	require.True(t, llmFilter.enabled)
	require.Contains(t, llmFilter.llmNodeAuthors, draftNodeID)
	require.Contains(t, llmFilter.llmNodeAuthors, finalNodeID)
	require.NotContains(t, llmFilter.terminalLLMAuthors, draftNodeID)
	require.Contains(t, llmFilter.terminalLLMAuthors, finalNodeID)

	agentGraph := newSerialTerminalMessageGraphAgent(
		t,
		firstNodeID,
		finalScope,
	).graph
	agentInv := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithGraphTerminalMessagesOnly(true),
		)),
	)
	agentInv.AgentName = rootAgentName
	agentFilter := newTerminalMessageFilter(agentInv, agentGraph)
	require.Contains(
		t,
		agentFilter.agentScopePrefixes,
		scopePrefix(rootAgentName, firstNodeID),
	)
	require.Contains(
		t,
		agentFilter.terminalScopePrefixes,
		scopePrefix(rootAgentName, finalScope),
	)
}

func TestTerminalMessageFilterMatchAgentScopePrefix(t *testing.T) {
	filter := terminalMessageFilter{
		agentScopePrefixes: map[string]struct{}{
			"root/child": {},
			"root/final": {},
		},
	}

	prefix, ok := filter.matchAgentScopePrefix(
		"root/final/leaf",
	)
	require.True(t, ok)
	require.Equal(t, "root/final", prefix)

	overlappingFilter := terminalMessageFilter{
		agentScopePrefixes: map[string]struct{}{
			"root/child":       {},
			"root/child/grand": {},
		},
	}

	_, ok = overlappingFilter.matchAgentScopePrefix(
		"root/child/grand/leaf",
	)
	require.False(t, ok)

	_, ok = filter.matchAgentScopePrefix("")
	require.False(t, ok)
}

func TestTerminalMessageFilterAllows(t *testing.T) {
	var nilFilter *terminalMessageFilter
	require.True(t, nilFilter.Allows(&event.Event{}))

	filter := terminalMessageFilter{
		enabled: true,
		llmNodeAuthors: map[string]struct{}{
			"draft_llm": {},
			"final_llm": {},
		},
		terminalLLMAuthors: map[string]struct{}{
			"final_llm": {},
		},
		agentScopePrefixes: map[string]struct{}{
			"root/first": {},
			"root/final": {},
		},
		terminalScopePrefixes: map[string]struct{}{
			"root/final": {},
		},
	}

	require.True(t, filter.Allows(nil))
	require.True(t, filter.Allows(&event.Event{}))

	nonTerminalAgentEvent := &event.Event{
		FilterKey: "root/first/child",
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("draft"),
			}},
		},
	}
	require.False(t, filter.Allows(nonTerminalAgentEvent))

	terminalAgentEvent := &event.Event{
		FilterKey: "root/final",
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	}
	require.True(t, filter.Allows(terminalAgentEvent))

	nonTerminalLLMEvent := &event.Event{
		Author: "draft_llm",
		Response: &model.Response{
			ID: "draft-response",
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("draft"),
			}},
		},
	}
	require.False(t, filter.Allows(nonTerminalLLMEvent))

	terminalLLMEvent := &event.Event{
		Author: "final_llm",
		Response: &model.Response{
			ID: "final-response",
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	}
	require.True(t, filter.Allows(terminalLLMEvent))

	unknownAuthorEvent := &event.Event{
		Author: "external",
		Response: &model.Response{
			ID: "external-response",
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	}
	require.True(t, filter.Allows(unknownAuthorEvent))
	_, ok := filter.nonTerminalResponseIDs["external-response"]
	require.False(t, ok)

	emptyResponseIDEvent := &event.Event{
		Author: "final_llm",
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	}
	require.True(t, filter.Allows(emptyResponseIDEvent))

	ambiguousScopeFilter := terminalMessageFilter{
		enabled: true,
		agentScopePrefixes: map[string]struct{}{
			"root/child":       {},
			"root/child/grand": {},
		},
		terminalScopePrefixes: map[string]struct{}{
			"root/child/grand": {},
		},
	}
	ambiguousScopeEvent := &event.Event{
		FilterKey: "root/child/grand/leaf",
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	}
	require.True(t, ambiguousScopeFilter.Allows(ambiguousScopeEvent))

	rawCompletion := event.New(
		"invocation-id",
		"graph-agent",
		event.WithObject(graph.ObjectTypeGraphExecution),
	)
	rawCompletion.Response = &model.Response{
		Done:   true,
		Object: graph.ObjectTypeGraphExecution,
	}
	visibleCompletion, ok := graph.VisibleGraphCompletionEvent(rawCompletion)
	require.True(t, ok)
	require.True(t, shouldFilterTerminalMessageEvent(visibleCompletion))

	nonTerminalCompletion := visibleCompletion.Clone()
	nonTerminalCompletion.StateDelta[graph.StateKeyLastResponseID] = []byte(
		`"draft-response"`,
	)
	require.False(t, filter.Allows(nonTerminalCompletion))

	terminalCompletion := visibleCompletion.Clone()
	terminalCompletion.StateDelta[graph.StateKeyLastResponseID] = []byte(
		`"final-response"`,
	)
	require.True(t, filter.Allows(terminalCompletion))

	unknownCompletion := visibleCompletion.Clone()
	unknownCompletion.StateDelta[graph.StateKeyLastResponseID] = []byte(
		`"unknown-response"`,
	)
	require.True(t, filter.Allows(unknownCompletion))

	missingResponseIDCompletion := visibleCompletion.Clone()
	delete(
		missingResponseIDCompletion.StateDelta,
		graph.StateKeyLastResponseID,
	)
	require.True(t, filter.Allows(missingResponseIDCompletion))
}

func TestTerminalMessageFilterRecordResponseID(t *testing.T) {
	filter := terminalMessageFilter{
		terminalResponseIDs: make(map[string]struct{}),
	}

	filter.recordResponseID("", true)
	require.Empty(t, filter.terminalResponseIDs)

	const responseID = "terminal-response"
	filter.recordResponseID(responseID, true)
	filter.recordResponseID(responseID, false)
	_, ok := filter.nonTerminalResponseIDs[responseID]
	require.False(t, ok)
}

func TestCompletionResponseIDFromStateDelta(t *testing.T) {
	require.Empty(t, completionResponseIDFromStateDelta(nil))
	require.Empty(t, completionResponseIDFromStateDelta(&event.Event{}))

	emptyStateDelta := &event.Event{StateDelta: map[string][]byte{}}
	require.Empty(t, completionResponseIDFromStateDelta(emptyStateDelta))

	invalidStateDelta := &event.Event{
		StateDelta: map[string][]byte{
			graph.StateKeyLastResponseID: []byte("{"),
		},
	}
	require.Empty(t, completionResponseIDFromStateDelta(invalidStateDelta))

	validStateDelta := &event.Event{
		StateDelta: map[string][]byte{
			graph.StateKeyLastResponseID: []byte(`"response-id"`),
		},
	}
	require.Equal(
		t,
		"response-id",
		completionResponseIDFromStateDelta(validStateDelta),
	)
}

func TestGraphAgent_DisableGraphCompletionEvent_WithAfterCallbackCustomResponse(t *testing.T) {
	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{
				graph.StateKeyLastResponse: "child-final",
			}, nil
		}).
		SetEntryPoint("done").
		SetFinishPoint("done").
		Compile()
	require.NoError(t, err)
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		return &agent.AfterAgentResult{
			CustomResponse: &model.Response{
				Object: "after.custom",
				Done:   true,
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "after callback",
					},
				}},
			},
		}, nil
	})
	ga, err := New(
		"test-hidden-completion-with-after-custom-response",
		g,
		WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	events, err := ga.Run(context.Background(), inv)
	require.NoError(t, err)
	var collected []*event.Event
	var visibleEvent *event.Event
	for evt := range events {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		collected = append(collected, evt)
		if evt != nil && evt.Object == model.ObjectTypeChatCompletion && len(evt.StateDelta) > 0 {
			visibleEvent = evt
		}
	}
	require.NotNil(t, visibleEvent)
	require.Len(t, visibleEvent.Response.Choices, 1)
	require.Equal(t, "child-final", visibleEvent.Response.Choices[0].Message.Content)
	require.NotEmpty(t, collected)
	last := collected[len(collected)-1]
	require.Equal(t, "after.custom", last.Object)
	require.Len(t, last.Response.Choices, 1)
	require.Equal(t, "after callback", last.Response.Choices[0].Message.Content)
}

func TestGraphAgent_BarrierWaitsForCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	schema := graph.NewStateSchema().
		AddField(graph.StateKeyMessages, graph.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: graph.DefaultReducer,
		})
	g, err := graph.NewStateGraph(schema).
		AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"ok": true}, nil
		}).
		SetEntryPoint("done").
		SetFinishPoint("done").
		Compile()
	require.NoError(t, err)

	ga, err := New("barrier-test", g)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "u", "s")),
	)
	barrier.Enable(inv)

	ch, err := ga.Run(ctx, inv)
	require.NoError(t, err)

	var barrierEvt *event.Event
	select {
	case barrierEvt = <-ch:
	case <-ctx.Done():
		require.NoError(t, ctx.Err(), "did not receive barrier event")
	}
	require.NotNil(t, barrierEvt)
	require.Equal(t, graph.ObjectTypeGraphBarrier, barrierEvt.Object)
	require.True(t, barrierEvt.RequiresCompletion)

	select {
	case evt, ok := <-ch:
		require.False(t, ok, "unexpected event before completion: %+v", evt)
	default:
	}

	completionID := agent.GetAppendEventNoticeKey(barrierEvt.ID)
	require.NoError(t, inv.NotifyCompletion(ctx, completionID))

	var received []*event.Event
	var sawNodeBarrier bool
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				goto done
			}
			if evt.Object == graph.ObjectTypeGraphNodeBarrier {
				sawNodeBarrier = true
			}
			if evt.RequiresCompletion {
				completionID := agent.GetAppendEventNoticeKey(evt.ID)
				require.NoError(t, inv.NotifyCompletion(ctx, completionID))
			}
			received = append(received, evt)
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), "timed out waiting for graph events")
		}
	}
done:
	require.NotEmpty(t, received)
	require.True(t, sawNodeBarrier)
	var hasGraphExec bool
	for _, evt := range received {
		if evt.Object == graph.ObjectTypeGraphExecution {
			hasGraphExec = true
		}
	}
	require.True(t, hasGraphExec)
}

func TestGraphAgent_RunWithBarrierEmitError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	schema := graph.NewStateSchema().
		AddField("done", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})
	g, err := graph.NewStateGraph(schema).
		AddNode("finish", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"done": "ok"}, nil
		}).
		SetEntryPoint("finish").
		SetFinishPoint("finish").
		Compile()
	require.NoError(t, err)

	ga, err := New("barrier-error", g)
	require.NoError(t, err)

	inv := &agent.Invocation{
		AgentName:    "barrier-error",
		InvocationID: "inv-barrier-error",
		// noticeMu left nil to force AddNoticeChannel to fail.
	}
	barrier.Enable(inv)

	out := make(chan *event.Event, 1)
	go ga.runWithBarrier(ctx, inv, out)

	var events []*event.Event
	for evt := range out {
		events = append(events, evt)
	}

	require.Len(t, events, 1)
	require.NotNil(t, events[0].Response)
	require.NotNil(t, events[0].Response.Error)
	require.Equal(t, model.ErrorTypeFlowError, events[0].Response.Error.Type)
	require.Contains(t, events[0].Response.Error.Message, "add notice channel")
}

func TestGraphAgent_RunWithBarrier_DisableGraphExecutorEventsHidesBarrierEvents(
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	schema := graph.NewStateSchema().
		AddField("done", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})
	g, err := graph.NewStateGraph(schema).
		AddNode("finish", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"done": "ok"}, nil
		}).
		SetEntryPoint("finish").
		SetFinishPoint("finish").
		Compile()
	require.NoError(t, err)
	ga, err := New("barrier-hidden", g)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(session.NewSession("app", "u", "s")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphExecutorEvents(true),
		)),
	)
	barrier.Enable(inv)
	ch, err := ga.Run(ctx, inv)
	require.NoError(t, err)
	var sawCompletion bool
	for evt := range ch {
		require.NotNil(t, evt)
		require.NotEqual(t, graph.ObjectTypeGraphBarrier, evt.Object)
		require.NotEqual(t, graph.ObjectTypeGraphNodeBarrier, evt.Object)
		if evt.Object == graph.ObjectTypeGraphExecution {
			sawCompletion = true
		}
	}
	require.True(t, sawCompletion)
}

func TestGraphAgent_WithExecutorOptions(t *testing.T) {
	// Create a simple graph
	schema := graph.NewStateSchema().
		AddField("counter", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("increment", func(ctx context.Context, state graph.State) (any, error) {
			counter, _ := state["counter"].(int)
			return graph.State{"counter": counter + 1}, nil
		}).
		SetEntryPoint("increment").
		SetFinishPoint("increment").
		Compile()

	require.NoError(t, err)

	// Test creating graph agent with executor options
	graphAgent, err := New("test-agent", g,
		WithExecutorOptions(
			graph.WithMaxSteps(50),
			graph.WithStepTimeout(5*time.Minute),
			graph.WithNodeTimeout(2*time.Minute),
		))

	require.NoError(t, err)
	require.NotNil(t, graphAgent)

	// Verify that executor was created successfully
	executor := graphAgent.Executor()
	require.NotNil(t, executor)

	// Test that the agent can execute with the configured options
	ctx := context.Background()
	invocation := &agent.Invocation{
		InvocationID: "test-executor-options",
		AgentName:    "test-agent",
	}

	eventChan, err := graphAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Consume events to ensure execution completes
	var done bool
	for evt := range eventChan {
		if evt.Done || evt.IsRunnerCompletion() {
			done = true
			break
		}
	}
	require.True(t, done, "Execution should complete")
}

func TestGraphAgent_WithExecutorOptions_OverrideMappedOptions(t *testing.T) {
	// Create a simple graph
	schema := graph.NewStateSchema().
		AddField("value", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	g, err := graph.NewStateGraph(schema).
		AddNode("set", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"value": "test"}, nil
		}).
		SetEntryPoint("set").
		SetFinishPoint("set").
		Compile()

	require.NoError(t, err)

	// Test that executor options can override mapped options
	// Set MaxConcurrency via mapped option, then override via executor option
	graphAgent, err := New("test-agent", g,
		WithMaxConcurrency(4),
		WithExecutorOptions(
			graph.WithMaxConcurrency(8), // This should override the mapped option
			graph.WithMaxSteps(100),
		))

	require.NoError(t, err)
	require.NotNil(t, graphAgent)

	// Verify executor was created
	executor := graphAgent.Executor()
	require.NotNil(t, executor)

	// Test execution
	ctx := context.Background()
	invocation := &agent.Invocation{
		InvocationID: "test-override",
		AgentName:    "test-agent",
	}

	eventChan, err := graphAgent.Run(ctx, invocation)
	require.NoError(t, err)

	// Consume events
	var done bool
	for evt := range eventChan {
		if evt.Done || evt.IsRunnerCompletion() {
			done = true
			break
		}
	}
	require.True(t, done, "Execution should complete")
}

// recordingSpanForEmitError captures span operations for testing emit error handling.
type recordingSpanForEmitError struct {
	oteltrace.Span
	mu         sync.Mutex
	name       string
	statusCode codes.Code
	statusDesc string
	attributes []attribute.KeyValue
}

func (s *recordingSpanForEmitError) SetStatus(code codes.Code, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusCode = code
	s.statusDesc = description
	s.Span.SetStatus(code, description)
}

func (s *recordingSpanForEmitError) SetAttributes(kv ...attribute.KeyValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes = append(s.attributes, kv...)
	s.Span.SetAttributes(kv...)
}

func (s *recordingSpanForEmitError) IsRecording() bool {
	return true
}

func (s *recordingSpanForEmitError) getStatus() (codes.Code, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusCode, s.statusDesc
}

func (s *recordingSpanForEmitError) getAttributes() []attribute.KeyValue {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]attribute.KeyValue, len(s.attributes))
	copy(result, s.attributes)
	return result
}

// recordingTracerForEmitError returns a tracer that creates recording spans.
type recordingTracerForEmitError struct {
	oteltrace.Tracer
	mu    sync.Mutex
	spans []*recordingSpanForEmitError
}

func newRecordingTracerForEmitError() *recordingTracerForEmitError {
	baseTracer := noop.NewTracerProvider().Tracer("test")
	return &recordingTracerForEmitError{Tracer: baseTracer}
}

func (t *recordingTracerForEmitError) Start(ctx context.Context, spanName string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, baseSpan := t.Tracer.Start(ctx, spanName, opts...)
	recordingSpan := &recordingSpanForEmitError{Span: baseSpan, name: spanName}
	t.spans = append(t.spans, recordingSpan)
	return ctx, recordingSpan
}

func findRecordingSpanForEmitErrorByName(spans []*recordingSpanForEmitError, spanName string) *recordingSpanForEmitError {
	for _, span := range spans {
		if span.name == spanName {
			return span
		}
	}
	return nil
}

func TestRecordTraceEvent(t *testing.T) {
	newTracker := func() *itelemetry.InvokeAgentTracker {
		var trackerErr error
		return itelemetry.NewInvokeAgentTracker(
			context.Background(),
			&agent.Invocation{AgentName: "trace-agent"},
			true,
			&trackerErr,
		)
	}
	newUsage := func() *model.Usage {
		return &model.Usage{
			PromptTokens:     1,
			CompletionTokens: 2,
			TotalTokens:      3,
		}
	}

	tests := []struct {
		name           string
		buildEvent     func() *event.Event
		sleepBefore    time.Duration
		wantUpdate     bool
		wantTokenUsage itelemetry.TokenUsage
		wantFirstToken bool
	}{
		{
			name:           "ignores nil event",
			buildEvent:     func() *event.Event { return nil },
			wantTokenUsage: itelemetry.TokenUsage{},
		},
		{
			name: "ignores event without response",
			buildEvent: func() *event.Event {
				return &event.Event{InvocationID: "inv", Author: "graph-agent"}
			},
			wantTokenUsage: itelemetry.TokenUsage{},
		},
		{
			name: "tracks partial response without updating final event",
			buildEvent: func() *event.Event {
				return event.NewResponseEvent("inv", "graph-agent", &model.Response{
					IsPartial: true,
					Choices: []model.Choice{{
						Delta: model.Message{
							Role:    model.RoleAssistant,
							Content: "chunk",
						},
					}},
					Usage: newUsage(),
				})
			},
			sleepBefore:    time.Millisecond,
			wantTokenUsage: itelemetry.TokenUsage{},
			wantFirstToken: true,
		},
		{
			name: "ignores invalid non terminal response",
			buildEvent: func() *event.Event {
				return event.NewResponseEvent("inv", "graph-agent", &model.Response{
					Usage: newUsage(),
				})
			},
			wantTokenUsage: itelemetry.TokenUsage{},
		},
		{
			name: "records graph execution completion response",
			buildEvent: func() *event.Event {
				return event.NewResponseEvent("inv", "graph-agent", &model.Response{
					Object: graph.ObjectTypeGraphExecution,
					Done:   true,
					Usage:  newUsage(),
				})
			},
			wantUpdate: true,
			wantTokenUsage: itelemetry.TokenUsage{
				PromptTokens:     1,
				CompletionTokens: 2,
				TotalTokens:      3,
			},
		},
		{
			name: "records error response",
			buildEvent: func() *event.Event {
				evt := event.NewErrorEvent("inv", "graph-agent", model.ErrorTypeFlowError, "boom")
				evt.Usage = newUsage()
				return evt
			},
			wantUpdate: true,
			wantTokenUsage: itelemetry.TokenUsage{
				PromptTokens:     1,
				CompletionTokens: 2,
				TotalTokens:      3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := newTracker()
			tokenUsage := &itelemetry.TokenUsage{}
			initialFullRespEvent := event.NewResponseEvent("inv", "graph-agent", &model.Response{
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("existing"),
				}},
			})

			if tt.sleepBefore > 0 {
				time.Sleep(tt.sleepBefore)
			}
			evt := tt.buildEvent()
			got := recordTraceEvent(tracker, tokenUsage, initialFullRespEvent, evt)

			if tt.wantUpdate {
				require.Same(t, evt, got)
			} else {
				require.Same(t, initialFullRespEvent, got)
			}
			require.Equal(t, tt.wantTokenUsage, *tokenUsage)

			if tt.wantFirstToken {
				require.Greater(t, tracker.FirstTokenTimeDuration(), time.Duration(0))
			} else {
				require.Zero(t, tracker.FirstTokenTimeDuration())
			}
		})
	}
}

func TestGraphAgent_RunWithBarrier_EmitEventError(t *testing.T) {
	// Save original tracer
	originalTracer := trace.Tracer
	defer func() {
		trace.Tracer = originalTracer
	}()

	// Create recording tracer
	recordingTracer := newRecordingTracerForEmitError()
	trace.Tracer = recordingTracer

	// Create a simple graph that produces events
	schema := graph.NewStateSchema().
		AddField("output", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})
	g, err := graph.NewStateGraph(schema).
		AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
			return graph.State{"output": "result"}, nil
		}).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()
	require.NoError(t, err)

	ga, err := New("emit-error-test", g)
	require.NoError(t, err)

	inv := &agent.Invocation{
		AgentName:    "emit-error-test",
		InvocationID: "inv-emit-error",
		Message:      model.NewUserMessage("test"),
	}

	// Create output channel with buffer size 0 to make EmitEvent block
	out := make(chan *event.Event)

	// Create a context that will be canceled to cause EmitEvent to fail
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start runWithBarrier in a goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		ga.runWithBarrier(ctx, inv, out)
	}()

	// Wait a bit for the graph to start executing and produce an event
	// The event will try to be emitted but will block on the unbuffered channel
	time.Sleep(100 * time.Millisecond)

	// Cancel the context to cause EmitEvent to fail with context.Canceled
	cancel()

	// Wait for the goroutine to finish
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for runWithBarrier to complete")
	}

	// Verify that span operations were called
	recordingTracer.mu.Lock()
	spans := recordingTracer.spans
	recordingTracer.mu.Unlock()

	require.NotEmpty(t, spans, "expected at least one span to be created")

	expectedSpanName := fmt.Sprintf("%s %s", itelemetry.OperationInvokeAgent, inv.AgentName)
	agentSpan := findRecordingSpanForEmitErrorByName(spans, expectedSpanName)
	require.NotNil(t, agentSpan, "expected agent span to be created")

	// Verify SetStatus was called with Error code
	statusCode, statusDesc := agentSpan.getStatus()
	require.Equal(t, codes.Error, statusCode, "expected span status to be Error")
	require.NotEmpty(t, statusDesc, "expected span status description to be set")

	// Verify SetAttributes was called with error type
	attrs := agentSpan.getAttributes()
	var errorTypeAttr *attribute.KeyValue
	for i := range attrs {
		if string(attrs[i].Key) == string(semconvtrace.KeyErrorType) {
			errorTypeAttr = &attrs[i]
			break
		}
	}
	require.NotNil(t, errorTypeAttr, "expected error type attribute to be set")
	errorTypeValue := errorTypeAttr.Value.AsString()
	require.Equal(t, model.ErrorTypeFlowError, errorTypeValue, "expected error type to be FlowError")
}

func TestGraphAgent_Run_RecordsStreamTraceAttribute(t *testing.T) {
	boolPtr := func(v bool) *bool {
		return &v
	}

	testCases := []struct {
		name   string
		stream *bool
		want   bool
	}{
		{
			name: "defaults to stream when unset",
			want: true,
		},
		{
			name:   "honors explicit stream true",
			stream: boolPtr(true),
			want:   true,
		},
		{
			name:   "honors explicit stream false",
			stream: boolPtr(false),
			want:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			originalTracer := trace.Tracer
			defer func() {
				trace.Tracer = originalTracer
			}()

			spanRecorder := tracetest.NewSpanRecorder()
			tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(spanRecorder))
			defer func() {
				_ = tp.Shutdown(context.Background())
			}()
			trace.Tracer = tp.Tracer("test")

			schema := graph.NewStateSchema().
				AddField("output", graph.StateField{
					Type:    reflect.TypeOf(""),
					Reducer: graph.DefaultReducer,
				})
			g, err := graph.NewStateGraph(schema).
				AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
					return graph.State{"output": "result"}, nil
				}).
				SetEntryPoint("process").
				SetFinishPoint("process").
				Compile()
			require.NoError(t, err)

			ga, err := New("stream-trace-test", g)
			require.NoError(t, err)

			invocation := &agent.Invocation{
				InvocationID: "test-invocation",
				AgentName:    "stream-trace-test",
				Message:      model.Message{Role: model.RoleUser, Content: "hello"},
				RunOptions: agent.RunOptions{
					Stream: tc.stream,
				},
			}

			events, err := ga.Run(context.Background(), invocation)
			require.NoError(t, err)
			for range events {
			}

			var agentSpan tracesdk.ReadOnlySpan
			expectedSpanName := fmt.Sprintf("%s %s", itelemetry.OperationInvokeAgent, invocation.AgentName)
			for _, span := range spanRecorder.Ended() {
				if span.Name() == expectedSpanName {
					agentSpan = span
					break
				}
			}
			require.NotNil(t, agentSpan, "expected invoke_agent span to be created")

			found := false
			for _, attr := range agentSpan.Attributes() {
				if string(attr.Key) == semconvtrace.KeyGenAIRequestIsStream {
					found = true
					require.Equal(t, tc.want, attr.Value.AsBool())
					break
				}
			}
			require.True(t, found, "expected stream trace attribute to be recorded")
		})
	}
}

func TestGraphAgent_GraphTerminalMessagesOnly_TracksHiddenNodeUsage(
	t *testing.T,
) {
	originalTracer := trace.Tracer
	defer func() {
		trace.Tracer = originalTracer
	}()

	spanRecorder := tracetest.NewSpanRecorder()
	tp := tracesdk.NewTracerProvider(
		tracesdk.WithSpanProcessor(spanRecorder),
	)
	defer func() {
		_ = tp.Shutdown(context.Background())
	}()
	trace.Tracer = tp.Tracer("graph-terminal-usage-test")

	firstUsage := model.Usage{
		PromptTokens:     2,
		CompletionTokens: 3,
		TotalTokens:      5,
	}
	finalUsage := model.Usage{
		PromptTokens:     5,
		CompletionTokens: 7,
		TotalTokens:      12,
	}
	ga := newSerialUsageTerminalMessageGraphAgent(
		t,
		firstUsage,
		finalUsage,
	)

	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithGraphEmitFinalModelResponses(true),
			agent.WithGraphTerminalMessagesOnly(true),
		)),
	)

	events, err := ga.Run(context.Background(), invocation)
	require.NoError(t, err)
	for range events {
	}

	expectedSpanName := fmt.Sprintf(
		"%s %s",
		itelemetry.OperationInvokeAgent,
		ga.Info().Name,
	)
	var agentSpan tracesdk.ReadOnlySpan
	for _, span := range spanRecorder.Ended() {
		if span.Name() == expectedSpanName {
			agentSpan = span
			break
		}
	}
	require.NotNil(t, agentSpan)
	require.True(
		t,
		hasInt64Attr(
			agentSpan.Attributes(),
			semconvtrace.KeyGenAIUsageInputTokens,
			int64(firstUsage.PromptTokens+finalUsage.PromptTokens),
		),
	)
	require.True(
		t,
		hasInt64Attr(
			agentSpan.Attributes(),
			semconvtrace.KeyGenAIUsageOutputTokens,
			int64(
				firstUsage.CompletionTokens+
					finalUsage.CompletionTokens,
			),
		),
	)
}

func TestGraphAgent_Run_TraceAfterInvokeAgent(t *testing.T) {
	originalTracer := trace.Tracer
	defer func() {
		trace.Tracer = originalTracer
	}()

	recordingTracer := newRecordingTracerForEmitError()
	trace.Tracer = recordingTracer

	g := buildTrivialGraph(t)
	callbacks := agent.NewCallbacks().
		RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
			return &model.Response{
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("graph result"),
				}},
			}, nil
		})

	ga, err := New("trace-after-test", g, WithAgentCallbacks(callbacks))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream := true
	eventChan, err := ga.Run(ctx, &agent.Invocation{
		InvocationID: "inv-trace-after",
		Message:      model.NewUserMessage("test"),
		RunOptions:   agent.RunOptions{Stream: &stream},
	})
	require.NoError(t, err)

	for range eventChan {
	}

	recordingTracer.mu.Lock()
	spans := recordingTracer.spans
	recordingTracer.mu.Unlock()

	require.NotEmpty(t, spans, "expected at least one span to be created")

	expectedSpanName := fmt.Sprintf("%s %s", itelemetry.OperationInvokeAgent, ga.Info().Name)
	agentSpan := findRecordingSpanForEmitErrorByName(spans, expectedSpanName)
	require.NotNil(t, agentSpan, "expected invoke_agent span to be created")

	attrs := agentSpan.getAttributes()
	var requestIsStream bool
	var foundRequestIsStream bool
	for _, attr := range attrs {
		if string(attr.Key) == string(semconvtrace.KeyGenAIRequestIsStream) {
			requestIsStream = attr.Value.AsBool()
			foundRequestIsStream = true
			break
		}
	}
	require.True(t, foundRequestIsStream, "expected request stream attribute to be set")
	require.True(t, requestIsStream, "expected request stream attribute to be true")
	var outputMessages string
	for _, attr := range attrs {
		if string(attr.Key) == string(semconvtrace.KeyGenAIOutputMessages) {
			outputMessages = attr.Value.AsString()
			break
		}
	}
	require.NotEmpty(t, outputMessages, "expected output messages attribute to be set")
	require.Contains(t, outputMessages, "graph result")
}

func TestGraphAgent_Run_ExecutionTraceCapturesComplexGraph(t *testing.T) {
	schema := graph.NewStateSchema().
		AddField("route_count", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
			Default: func() any { return 0 },
		}).
		AddField("visited", graph.StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: graph.StringSliceReducer,
			Default: func() any { return []string{} },
		})
	builder := graph.NewStateGraph(schema)
	builder.AddNode("start", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"start"}}, nil
	})
	builder.AddNode("prepare", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"prepare"}}, nil
	})
	builder.AddNode("route", func(_ context.Context, state graph.State) (any, error) {
		count, _ := state["route_count"].(int)
		return graph.State{
			"route_count": count + 1,
			"visited":     []string{"route"},
		}, nil
	})
	builder.AddNode("tools", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"tools"}}, nil
	})
	builder.AddNode("branch_a", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"branch_a"}}, nil
	})
	builder.AddNode("branch_b", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"branch_b"}}, nil
	})
	builder.AddNode("branch_never", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"branch_never"}}, nil
	})
	builder.AddNode("join", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"join"}}, nil
	})
	builder.AddNode("done", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"done"}}, nil
	})
	builder.SetEntryPoint("start")
	builder.AddEdge("start", "route")
	builder.AddEdge("start", "prepare")
	builder.AddConditionalEdges("route", func(_ context.Context, state graph.State) (string, error) {
		count, _ := state["route_count"].(int)
		if count == 1 {
			return "tools", nil
		}
		return "branch_a", nil
	}, map[string]string{
		"tools":    "tools",
		"branch_a": "branch_a",
	})
	builder.AddEdge("tools", "route")
	builder.AddEdge("prepare", "branch_b")
	builder.AddJoinEdge([]string{"branch_a", "branch_b"}, "join")
	builder.AddConditionalEdges("join", func(context.Context, graph.State) (string, error) {
		return "done", nil
	}, map[string]string{
		"done":         "done",
		"branch_never": "branch_never",
	})
	builder.SetFinishPoint("done")
	compiled := builder.MustCompile()
	ag, err := New("assistant", compiled, WithMaxConcurrency(1))
	require.NoError(t, err)
	r := runner.NewRunner("app", ag, runner.WithSessionService(inmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello graph trace"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	var completion *event.Event
	for evt := range eventCh {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.ExecutionTrace)
	executionTrace := completion.ExecutionTrace
	require.Equal(t, atrace.Trace{
		RootAgentName:    "assistant",
		RootInvocationID: "root-invocation",
		SessionID:        "session-1",
		StartedAt:        graphAgentTraceAssertionStartTime,
		EndedAt:          graphAgentTraceAssertionEndTime,
		Status:           atrace.TraceStatusCompleted,
		Steps: []atrace.Step{
			{
				StepID:             "assistant/branch_a#1",
				InvocationID:       "root-invocation",
				ParentInvocationID: "",
				AgentName:          "assistant",
				Branch:             "assistant/branch_a",
				NodeID:             "assistant/branch_a",
				StartedAt:          graphAgentTraceAssertionStartTime,
				EndedAt:            graphAgentTraceAssertionEndTime,
				PredecessorStepIDs: []string{"assistant/route#2"},
				Input:              &atrace.Snapshot{Text: "{\"checkpoint_ns\":\"assistant\",\"messages\":[{\"role\":\"user\",\"content\":\"hello graph trace\"}],\"route_count\":2,\"user_input\":\"hello graph trace\",\"visited\":[\"start\",\"prepare\",\"route\",\"branch_b\",\"tools\",\"route\"]}"},
				Output:             &atrace.Snapshot{Text: "{\"visited\":[\"branch_a\"]}"},
				Error:              "",
			},
			{
				StepID:             "assistant/branch_b#1",
				InvocationID:       "root-invocation",
				ParentInvocationID: "",
				AgentName:          "assistant",
				Branch:             "assistant/branch_b",
				NodeID:             "assistant/branch_b",
				StartedAt:          graphAgentTraceAssertionStartTime,
				EndedAt:            graphAgentTraceAssertionEndTime,
				PredecessorStepIDs: []string{"assistant/prepare#1"},
				Input:              &atrace.Snapshot{Text: "{\"checkpoint_ns\":\"assistant\",\"messages\":[{\"role\":\"user\",\"content\":\"hello graph trace\"}],\"route_count\":1,\"user_input\":\"hello graph trace\",\"visited\":[\"start\",\"prepare\",\"route\"]}"},
				Output:             &atrace.Snapshot{Text: "{\"visited\":[\"branch_b\"]}"},
				Error:              "",
			},
			{
				StepID:             "assistant/done#1",
				InvocationID:       "root-invocation",
				ParentInvocationID: "",
				AgentName:          "assistant",
				Branch:             "assistant/done",
				NodeID:             "assistant/done",
				StartedAt:          graphAgentTraceAssertionStartTime,
				EndedAt:            graphAgentTraceAssertionEndTime,
				PredecessorStepIDs: []string{"assistant/join#1"},
				Input:              &atrace.Snapshot{Text: "{\"checkpoint_ns\":\"assistant\",\"messages\":[{\"role\":\"user\",\"content\":\"hello graph trace\"}],\"route_count\":2,\"user_input\":\"hello graph trace\",\"visited\":[\"start\",\"prepare\",\"route\",\"branch_b\",\"tools\",\"route\",\"branch_a\",\"join\"]}"},
				Output:             &atrace.Snapshot{Text: "{\"visited\":[\"done\"]}"},
				Error:              "",
			},
			{
				StepID:             "assistant/join#1",
				InvocationID:       "root-invocation",
				ParentInvocationID: "",
				AgentName:          "assistant",
				Branch:             "assistant/join",
				NodeID:             "assistant/join",
				StartedAt:          graphAgentTraceAssertionStartTime,
				EndedAt:            graphAgentTraceAssertionEndTime,
				PredecessorStepIDs: []string{"assistant/branch_a#1", "assistant/branch_b#1"},
				Input:              &atrace.Snapshot{Text: "{\"checkpoint_ns\":\"assistant\",\"messages\":[{\"role\":\"user\",\"content\":\"hello graph trace\"}],\"route_count\":2,\"user_input\":\"hello graph trace\",\"visited\":[\"start\",\"prepare\",\"route\",\"branch_b\",\"tools\",\"route\",\"branch_a\"]}"},
				Output:             &atrace.Snapshot{Text: "{\"visited\":[\"join\"]}"},
				Error:              "",
			},
			{
				StepID:             "assistant/prepare#1",
				InvocationID:       "root-invocation",
				ParentInvocationID: "",
				AgentName:          "assistant",
				Branch:             "assistant/prepare",
				NodeID:             "assistant/prepare",
				StartedAt:          graphAgentTraceAssertionStartTime,
				EndedAt:            graphAgentTraceAssertionEndTime,
				PredecessorStepIDs: []string{"assistant/start#1"},
				Input:              &atrace.Snapshot{Text: "{\"checkpoint_ns\":\"assistant\",\"messages\":[{\"role\":\"user\",\"content\":\"hello graph trace\"}],\"route_count\":0,\"user_input\":\"hello graph trace\",\"visited\":[\"start\"]}"},
				Output:             &atrace.Snapshot{Text: "{\"visited\":[\"prepare\"]}"},
				Error:              "",
			},
			{
				StepID:             "assistant/route#1",
				InvocationID:       "root-invocation",
				ParentInvocationID: "",
				AgentName:          "assistant",
				Branch:             "assistant/route",
				NodeID:             "assistant/route",
				StartedAt:          graphAgentTraceAssertionStartTime,
				EndedAt:            graphAgentTraceAssertionEndTime,
				PredecessorStepIDs: []string{"assistant/start#1"},
				Input:              &atrace.Snapshot{Text: "{\"checkpoint_ns\":\"assistant\",\"messages\":[{\"role\":\"user\",\"content\":\"hello graph trace\"}],\"route_count\":0,\"user_input\":\"hello graph trace\",\"visited\":[\"start\"]}"},
				Output:             &atrace.Snapshot{Text: "{\"route_count\":1,\"visited\":[\"route\"]}"},
				Error:              "",
			},
			{
				StepID:             "assistant/route#2",
				InvocationID:       "root-invocation",
				ParentInvocationID: "",
				AgentName:          "assistant",
				Branch:             "assistant/route",
				NodeID:             "assistant/route",
				StartedAt:          graphAgentTraceAssertionStartTime,
				EndedAt:            graphAgentTraceAssertionEndTime,
				PredecessorStepIDs: []string{"assistant/tools#1"},
				Input:              &atrace.Snapshot{Text: "{\"checkpoint_ns\":\"assistant\",\"messages\":[{\"role\":\"user\",\"content\":\"hello graph trace\"}],\"route_count\":1,\"user_input\":\"hello graph trace\",\"visited\":[\"start\",\"prepare\",\"route\",\"branch_b\",\"tools\"]}"},
				Output:             &atrace.Snapshot{Text: "{\"route_count\":2,\"visited\":[\"route\"]}"},
				Error:              "",
			},
			{
				StepID:             "assistant/start#1",
				InvocationID:       "root-invocation",
				ParentInvocationID: "",
				AgentName:          "assistant",
				Branch:             "assistant/start",
				NodeID:             "assistant/start",
				StartedAt:          graphAgentTraceAssertionStartTime,
				EndedAt:            graphAgentTraceAssertionEndTime,
				PredecessorStepIDs: []string{},
				Input:              &atrace.Snapshot{Text: "{\"checkpoint_ns\":\"assistant\",\"messages\":[{\"role\":\"user\",\"content\":\"hello graph trace\"}],\"route_count\":0,\"user_input\":\"hello graph trace\",\"visited\":[]}"},
				Output:             &atrace.Snapshot{Text: "{\"visited\":[\"start\"]}"},
				Error:              "",
			},
			{
				StepID:             "assistant/tools#1",
				InvocationID:       "root-invocation",
				ParentInvocationID: "",
				AgentName:          "assistant",
				Branch:             "assistant/tools",
				NodeID:             "assistant/tools",
				StartedAt:          graphAgentTraceAssertionStartTime,
				EndedAt:            graphAgentTraceAssertionEndTime,
				PredecessorStepIDs: []string{"assistant/route#1"},
				Input:              &atrace.Snapshot{Text: "{\"checkpoint_ns\":\"assistant\",\"messages\":[{\"role\":\"user\",\"content\":\"hello graph trace\"}],\"route_count\":1,\"user_input\":\"hello graph trace\",\"visited\":[\"start\",\"prepare\",\"route\"]}"},
				Output:             &atrace.Snapshot{Text: "{\"visited\":[\"tools\"]}"},
				Error:              "",
			},
		},
	}, normalizeGraphAgentTraceForAssertion(executionTrace))
}

func TestResolveGraphAgentErrorType(t *testing.T) {
	code := "70002"
	testCases := []struct {
		name               string
		fullRespEvent      *event.Event
		operationErrorType string
		want               string
	}{
		{
			name: "operation error wins over final success",
			fullRespEvent: event.NewResponseEvent(
				"inv",
				"graph-agent",
				&model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}}},
			),
			operationErrorType: model.ErrorTypeFlowError,
			want:               model.ErrorTypeFlowError,
		},
		{
			name: "final success clears prior response errors",
			fullRespEvent: event.NewResponseEvent(
				"inv",
				"graph-agent",
				&model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}}},
			),
			want: "",
		},
		{
			name: "final error response is reported when no operation error",
			fullRespEvent: event.NewErrorEvent(
				"inv",
				"graph-agent",
				agent.ErrorTypeAgentCallbackError,
				"callback failed",
			),
			want: agent.ErrorTypeAgentCallbackError,
		},
		{
			name: "final error response keeps code suffix",
			fullRespEvent: event.NewResponseEvent(
				"inv",
				"graph-agent",
				&model.Response{
					Error: &model.ResponseError{
						Type:    model.ErrorTypeFlowError,
						Code:    &code,
						Message: "graph failed",
					},
				},
			),
			want: model.ErrorTypeFlowError + "_70002",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveGraphAgentErrorType(tc.fullRespEvent, tc.operationErrorType)
			require.Equal(t, tc.want, got)
		})
	}
}

func newStreamingChildGraphAgent(
	t *testing.T,
	name string,
	final string,
) *GraphAgent {
	t.Helper()

	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			"child_llm",
			&streamingGraphAgentModel{
				name:   name,
				deltas: []string{final},
				final:  final,
			},
			"i1",
			nil,
		).
		SetEntryPoint("child_llm").
		SetFinishPoint("child_llm").
		Compile()
	require.NoError(t, err)

	ga, err := New(name, g)
	require.NoError(t, err)
	return ga
}

func newUsageChildGraphAgent(
	t *testing.T,
	name string,
	final string,
	usage model.Usage,
) *GraphAgent {
	t.Helper()

	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			"child_llm",
			&usageGraphAgentModel{
				name:    name,
				content: final,
				usage:   usage,
			},
			"i1",
			nil,
		).
		SetEntryPoint("child_llm").
		SetFinishPoint("child_llm").
		Compile()
	require.NoError(t, err)

	ga, err := New(name, g)
	require.NoError(t, err)
	return ga
}

func newSerialTerminalMessageGraphAgent(
	t *testing.T,
	firstScope string,
	finalScope string,
) *GraphAgent {
	t.Helper()

	firstAgent := newStreamingChildGraphAgent(t, "first", "draft")
	finalAgent := newStreamingChildGraphAgent(t, "final", "answer")

	builder := graph.NewStateGraph(graph.MessagesStateSchema())
	firstOpts := []graph.Option{}
	if firstScope != "" {
		firstOpts = append(firstOpts, graph.WithSubgraphEventScope(firstScope))
	}
	finalOpts := []graph.Option{
		graph.WithSubgraphInputFromLastResponse(),
	}
	if finalScope != "" {
		finalOpts = append(finalOpts, graph.WithSubgraphEventScope(finalScope))
	}
	parentGraph, err := builder.
		AddAgentNode("first", firstOpts...).
		AddAgentNode("final", finalOpts...).
		AddEdge("first", "final").
		SetEntryPoint("first").
		SetFinishPoint("final").
		Compile()
	require.NoError(t, err)

	ga, err := New(
		"serial-terminal-message-filter",
		parentGraph,
		WithSubAgents([]agent.Agent{firstAgent, finalAgent}),
	)
	require.NoError(t, err)
	return ga
}

func newSerialUsageTerminalMessageGraphAgent(
	t *testing.T,
	firstUsage model.Usage,
	finalUsage model.Usage,
) *GraphAgent {
	t.Helper()

	firstAgent := newUsageChildGraphAgent(
		t,
		"first_usage",
		"draft",
		firstUsage,
	)
	finalAgent := newUsageChildGraphAgent(
		t,
		"final_usage",
		"answer",
		finalUsage,
	)

	parentGraph, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddAgentNode("first_usage").
		AddAgentNode(
			"final_usage",
			graph.WithSubgraphInputFromLastResponse(),
		).
		AddEdge("first_usage", "final_usage").
		SetEntryPoint("first_usage").
		SetFinishPoint("final_usage").
		Compile()
	require.NoError(t, err)

	ga, err := New(
		"serial-terminal-usage-filter",
		parentGraph,
		WithSubAgents([]agent.Agent{firstAgent, finalAgent}),
	)
	require.NoError(t, err)
	return ga
}

func scopePrefix(root string, scope string) string {
	if root == "" {
		return scope
	}
	return root + agent.EventFilterKeyDelimiter + scope
}

func hasFilterPrefix(filterKey string, prefix string) bool {
	if filterKey == prefix {
		return true
	}
	if filterKey == "" || prefix == "" {
		return false
	}
	return strings.HasPrefix(
		filterKey,
		prefix+agent.EventFilterKeyDelimiter,
	)
}

func hasInt64Attr(
	attrs []attribute.KeyValue,
	key string,
	want int64,
) bool {
	for _, attr := range attrs {
		if string(attr.Key) != key {
			continue
		}
		return attr.Value.AsInt64() == want
	}
	return false
}

func normalizeGraphAgentTraceForAssertion(executionTrace *atrace.Trace) atrace.Trace {
	if executionTrace == nil {
		return atrace.Trace{}
	}
	normalized := atrace.Trace{
		RootAgentName:    executionTrace.RootAgentName,
		RootInvocationID: "root-invocation",
		SessionID:        executionTrace.SessionID,
		StartedAt:        graphAgentTraceAssertionStartTime,
		EndedAt:          graphAgentTraceAssertionEndTime,
		Status:           executionTrace.Status,
		Steps:            make([]atrace.Step, 0, len(executionTrace.Steps)),
	}
	stepLabels := make(map[string]string, len(executionTrace.Steps))
	nodeCounts := make(map[string]int)
	for _, step := range executionTrace.Steps {
		nodeCounts[step.NodeID]++
		stepLabels[step.StepID] = fmt.Sprintf("%s#%d", step.NodeID, nodeCounts[step.NodeID])
	}
	for _, step := range executionTrace.Steps {
		predecessors := make([]string, 0, len(step.PredecessorStepIDs))
		for _, predecessorStepID := range step.PredecessorStepIDs {
			predecessors = append(predecessors, stepLabels[predecessorStepID])
		}
		sort.Strings(predecessors)
		var input *atrace.Snapshot
		if step.Input != nil {
			input = &atrace.Snapshot{Text: step.Input.Text}
		}
		var output *atrace.Snapshot
		if step.Output != nil {
			output = &atrace.Snapshot{Text: step.Output.Text}
		}
		normalized.Steps = append(normalized.Steps, atrace.Step{
			StepID:             stepLabels[step.StepID],
			InvocationID:       "root-invocation",
			ParentInvocationID: "",
			AgentName:          step.AgentName,
			Branch:             step.Branch,
			NodeID:             step.NodeID,
			StartedAt:          graphAgentTraceAssertionStartTime,
			EndedAt:            graphAgentTraceAssertionEndTime,
			PredecessorStepIDs: predecessors,
			Input:              input,
			Output:             output,
			Error:              step.Error,
		})
	}
	sort.Slice(normalized.Steps, func(i, j int) bool {
		return normalized.Steps[i].StepID < normalized.Steps[j].StepID
	})
	return normalized
}

var (
	graphAgentTraceAssertionStartTime = time.Unix(1, 0).UTC()
	graphAgentTraceAssertionEndTime   = time.Unix(2, 0).UTC()
)
