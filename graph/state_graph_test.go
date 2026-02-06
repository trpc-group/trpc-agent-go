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
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	ichannel "trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewBuilder(t *testing.T) {
	builder := NewStateGraph(NewStateSchema())
	if builder == nil {
		t.Fatal("Expected non-nil builder")
	}
	if builder.graph == nil {
		t.Error("Expected builder to have initialized state graph")
	}
}

func TestBuilderAddFunctionNode(t *testing.T) {
	builder := NewStateGraph(NewStateSchema())

	testFunc := func(ctx context.Context, state State) (any, error) {
		return State{"processed": true}, nil
	}

	result := builder.AddNode("test", testFunc)
	if result != builder {
		t.Error("Expected fluent interface to return builder")
	}

	graph, err := builder.
		SetEntryPoint("test").
		SetFinishPoint("test").
		Compile()
	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	node, exists := graph.Node("test")
	if !exists {
		t.Error("Expected test node to be added")
	}
	if node.Name != "test" {
		t.Errorf("Expected node name 'test', got '%s'", node.Name)
	}
	if node.Function == nil {
		t.Error("Expected node to have function")
	}
}

func TestWithToolSets_EmptyClearsNodeToolSets(t *testing.T) {
	stateGraph := NewStateGraph(NewStateSchema())

	stateGraph.AddNode("n",
		func(ctx context.Context,
			state State) (any, error) {
			return state, nil
		},
		WithToolSets(nil),
	)

	node := stateGraph.graph.nodes["n"]
	if node == nil {
		t.Fatalf("expected node n to exist")
	}
	if node.toolSets != nil {
		t.Fatalf("expected toolSets to be nil for empty input")
	}
}

func TestWithToolSets_CopiesSlice(t *testing.T) {
	stateGraph := NewStateGraph(NewStateSchema())

	originalToolSets := []tool.ToolSet{
		&simpleToolSet{name: "simple"},
	}

	stateGraph.AddNode("n",
		func(ctx context.Context,
			state State) (any, error) {
			return state, nil
		},
		WithToolSets(originalToolSets),
	)

	node := stateGraph.graph.nodes["n"]
	if node == nil {
		t.Fatalf("expected node n to exist")
	}
	if len(node.toolSets) != 1 {
		t.Fatalf("expected 1 toolset on node, got %d",
			len(node.toolSets))
	}

	snapshot := node.toolSets[0]
	originalToolSets[0] = &simpleToolSet{name: "updated"}

	if node.toolSets[0] != snapshot {
		t.Fatalf("expected node toolset reference to be stable")
	}
	if node.toolSets[0] == originalToolSets[0] {
		t.Fatalf("expected node toolsets slice to be copied")
	}
}

func TestMergeToolsWithToolSets_EmitsConflictWarning(t *testing.T) {
	base := map[string]tool.Tool{
		"simple_echo": &echoTool{name: "simple_echo"},
	}
	toolSets := []tool.ToolSet{
		&simpleToolSet{name: "simple"},
	}

	out := mergeToolsWithToolSets(
		context.Background(),
		base,
		toolSets,
	)

	if len(out) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(out))
	}
	if _, ok := out["simple_echo"]; !ok {
		t.Fatalf("expected key simple_echo in merged tools")
	}
}

// blockingTool is a test tool that blocks until allowed, reporting start and
// optionally respecting context cancellation.
type blockingTool struct {
	name       string
	startedCh  chan<- string
	proceedCh  <-chan struct{}
	result     any
	returnErr  error
	respectCtx bool
}

func (b *blockingTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: b.name} }

func (b *blockingTool) Call(ctx context.Context, _ []byte) (any, error) {
	if b.startedCh != nil {
		b.startedCh <- b.name
	}
	if b.returnErr != nil {
		return nil, b.returnErr
	}
	if b.proceedCh != nil {
		if b.respectCtx {
			select {
			case <-b.proceedCh:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			<-b.proceedCh
		}
	}
	return b.result, nil
}

type resultWithErrorTool struct {
	name   string
	result any
	err    error
}

func (t *resultWithErrorTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: t.name} }

func (t *resultWithErrorTool) Call(_ context.Context, _ []byte) (any, error) {
	return t.result, t.err
}

// helper to build tool calls with fixed IDs and names.
func makeToolCalls(names ...string) []model.ToolCall {
	calls := make([]model.ToolCall, 0, len(names))
	for _, n := range names {
		calls = append(calls, model.ToolCall{
			Type: "function",
			ID:   "call_" + n,
			Function: model.FunctionDefinitionParam{
				Name:      n,
				Arguments: []byte(`{}`),
			},
		})
	}
	return calls
}

func TestProcessToolCalls_SerialVsParallel(t *testing.T) {
	t.Run("serial executes strictly one-by-one", func(t *testing.T) {
		started := make(chan string, 2)
		allowA := make(chan struct{})
		allowB := make(chan struct{})

		tools := map[string]tool.Tool{
			"A": &blockingTool{name: "A", startedCh: started, proceedCh: allowA, result: map[string]string{"v": "A"}},
			"B": &blockingTool{name: "B", startedCh: started, proceedCh: allowB, result: map[string]string{"v": "B"}},
		}
		calls := makeToolCalls("A", "B")

		done := make(chan []model.Message, 1)
		go func() {
			msgs, err := processToolCalls(context.Background(), toolCallsConfig{
				ToolCalls:      calls,
				Tools:          tools,
				InvocationID:   "inv",
				EventChan:      nil,
				Span:           nil,
				State:          State{},
				EnableParallel: false,
			})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			done <- msgs
		}()

		// Expect only A to have started initially.
		select {
		case got := <-started:
			if got != "A" {
				t.Fatalf("first started tool = %s, want A", got)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timeout waiting for first tool start in serial case")
		}
		// B should not have started yet (non-blocking check).
		select {
		case s := <-started:
			t.Fatalf("unexpected second start before allowing A; got %s", s)
		default:
		}
		// Allow A to finish, then B should start.
		close(allowA)
		select {
		case got := <-started:
			if got != "B" {
				t.Fatalf("second started tool = %s, want B", got)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timeout waiting for second tool start in serial case")
		}
		close(allowB)

		msgs := <-done
		if len(msgs) != 2 {
			t.Fatalf("messages length = %d, want 2", len(msgs))
		}
		// Verify order and JSON content preserved.
		if msgs[0].ToolName != "A" || msgs[0].ToolID != "call_A" {
			t.Fatalf("first msg tool mismatch: %+v", msgs[0])
		}
		if msgs[1].ToolName != "B" || msgs[1].ToolID != "call_B" {
			t.Fatalf("second msg tool mismatch: %+v", msgs[1])
		}
		// Content is JSON string of the result.
		var c0 map[string]string
		if err := json.Unmarshal([]byte(msgs[0].Content), &c0); err != nil || c0["v"] != "A" {
			t.Fatalf("first msg content = %q, parsed = %v, err = %v", msgs[0].Content, c0, err)
		}
		var c1 map[string]string
		if err := json.Unmarshal([]byte(msgs[1].Content), &c1); err != nil || c1["v"] != "B" {
			t.Fatalf("second msg content = %q, parsed = %v, err = %v", msgs[1].Content, c1, err)
		}
	})

	t.Run("parallel starts both before completion and preserves order", func(t *testing.T) {
		started := make(chan string, 2)
		allowA := make(chan struct{})
		allowB := make(chan struct{})

		tools := map[string]tool.Tool{
			"A": &blockingTool{name: "A", startedCh: started, proceedCh: allowA, result: map[string]int{"i": 1}},
			"B": &blockingTool{name: "B", startedCh: started, proceedCh: allowB, result: map[string]int{"i": 2}},
		}
		calls := makeToolCalls("A", "B")

		done := make(chan []model.Message, 1)
		go func() {
			msgs, err := processToolCalls(context.Background(), toolCallsConfig{
				ToolCalls:      calls,
				Tools:          tools,
				InvocationID:   "inv",
				EventChan:      nil,
				Span:           nil,
				State:          State{},
				EnableParallel: true,
			})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			done <- msgs
		}()

		// Expect both to start before we allow either to proceed.
		saw := map[string]bool{}
		for i := 0; i < 2; i++ {
			select {
			case nm := <-started:
				saw[nm] = true
			case <-time.After(500 * time.Millisecond):
				t.Fatal("timeout waiting for parallel tool starts")
			}
		}
		if !saw["A"] || !saw["B"] {
			t.Fatalf("parallel start saw=%v, want both A and B", saw)
		}
		close(allowA)
		close(allowB)
		msgs := <-done
		if len(msgs) != 2 {
			t.Fatalf("messages length = %d, want 2", len(msgs))
		}
		if msgs[0].ToolName != "A" || msgs[1].ToolName != "B" {
			t.Fatalf("order not preserved: %s then %s", msgs[0].ToolName, msgs[1].ToolName)
		}
	})
}

func TestProcessAgentEventStream_UnmarshalErrorLogged(t *testing.T) {
	ctx := context.Background()
	agentEvents := make(chan *event.Event, 1)
	parentEventChan := make(chan *event.Event, 1)

	agentEvents <- &event.Event{
		Response: &model.Response{
			Object: ObjectTypeGraphExecution,
			Done:   true,
		},
		StateDelta: map[string][]byte{
			"bad": []byte("not-json"),
		},
	}
	close(agentEvents)

	res, err := processAgentEventStream(
		ctx,
		agentEvents,
		nil,
		"node",
		State{},
		parentEventChan,
		"agent",
		&itelemetry.InvokeAgentTracker{},
	)
	require.NoError(t, err)
	require.Equal(t, "", res.lastResponse)
	require.NotNil(t, res.finalState)
	require.Len(t, res.rawDelta, 1)
}

func TestProcessAgentEventStream_AccumulatesTokenUsage(t *testing.T) {
	ctx := context.Background()
	agentEvents := make(chan *event.Event, 2)
	parentEventChan := make(chan *event.Event, 2)

	agentEvents <- &event.Event{
		Response: &model.Response{
			IsPartial: true,
			Usage: &model.Usage{
				PromptTokens:     1,
				CompletionTokens: 2,
				TotalTokens:      3,
			},
			Choices: []model.Choice{{Message: model.NewAssistantMessage("partial")}},
		},
	}

	finalUsage := &model.Usage{
		PromptTokens:     10,
		CompletionTokens: 20,
		TotalTokens:      30,
	}
	finalEvent := &event.Event{
		Response: &model.Response{
			IsPartial: false,
			Usage:     finalUsage,
			Choices:   []model.Choice{{Message: model.NewAssistantMessage("final")}},
		},
	}
	agentEvents <- finalEvent
	close(agentEvents)

	res, err := processAgentEventStream(
		ctx,
		agentEvents,
		nil,
		"node",
		State{},
		parentEventChan,
		"agent",
		&itelemetry.InvokeAgentTracker{},
	)
	require.NoError(t, err)
	require.Equal(t, "final", res.lastResponse)
	require.Equal(t, finalUsage.PromptTokens, res.tokenUsage.PromptTokens)
	require.Equal(
		t,
		finalUsage.CompletionTokens,
		res.tokenUsage.CompletionTokens,
	)
	require.Equal(t, finalUsage.TotalTokens, res.tokenUsage.TotalTokens)
	require.Equal(t, finalEvent, res.fullRespEvent)
	require.Len(t, parentEventChan, 2)
}

func TestProcessAgentEventStream_CapturesStructuredOutput(t *testing.T) {
	ctx := context.Background()
	agentEvents := make(chan *event.Event, 2)
	parentEventChan := make(chan *event.Event, 2)

	// First event without structured output
	agentEvents <- &event.Event{
		Response: &model.Response{
			IsPartial: false,
			Choices:   []model.Choice{{Message: model.NewAssistantMessage("text response")}},
		},
	}

	// Second event with structured output
	structuredData := map[string]any{
		"status": "success",
		"data":   []string{"item1", "item2"},
		"count":  2,
	}
	agentEvents <- &event.Event{
		Response: &model.Response{
			IsPartial: false,
			Choices:   []model.Choice{{Message: model.NewAssistantMessage("final")}},
		},
		StructuredOutput: structuredData,
	}
	close(agentEvents)

	res, err := processAgentEventStream(
		ctx,
		agentEvents,
		nil,
		"node",
		State{},
		parentEventChan,
		"agent",
		&itelemetry.InvokeAgentTracker{},
	)
	require.NoError(t, err)
	require.Equal(t, "final", res.lastResponse)
	require.NotNil(
		t,
		res.structuredOutput,
		"should capture structured output from event",
	)
	require.Equal(
		t,
		structuredData,
		res.structuredOutput,
		"captured output should match event payload",
	)
}

func TestProcessToolCalls_ParallelCancelOnFirstError(t *testing.T) {
	started := make(chan string, 2)
	// Tool X errors immediately; Tool Y waits but respects context and should be canceled.
	tools := map[string]tool.Tool{
		"X": &blockingTool{name: "X", startedCh: started, returnErr: assertAnError{}},
		"Y": &blockingTool{name: "Y", startedCh: started, proceedCh: make(chan struct{}), respectCtx: true, result: "ok"},
	}
	calls := makeToolCalls("X", "Y")
	_, err := processToolCalls(context.Background(), toolCallsConfig{
		ToolCalls:      calls,
		Tools:          tools,
		InvocationID:   "inv",
		EventChan:      nil,
		Span:           oteltrace.SpanFromContext(context.Background()),
		State:          State{},
		EnableParallel: true,
	})
	if err == nil {
		t.Fatal("expected error when one tool fails in parallel execution")
	}
}

type assertAnError struct{}

func (assertAnError) Error() string { return "boom" }

func TestNewToolsNodeFunc_WithEnableParallelTools(t *testing.T) {
	started := make(chan string, 2)
	allowA, allowB := make(chan struct{}), make(chan struct{})
	tools := map[string]tool.Tool{
		"A": &blockingTool{name: "A", startedCh: started, proceedCh: allowA, result: 1},
		"B": &blockingTool{name: "B", startedCh: started, proceedCh: allowB, result: 2},
	}
	// Build a node func with parallel enabled via option.
	nf := NewToolsNodeFunc(tools, WithEnableParallelTools(true))

	// Prepare state with last assistant message containing tool calls.
	state := State{
		StateKeyMessages: []model.Message{{
			Role:      model.RoleAssistant,
			ToolCalls: makeToolCalls("A", "B"),
		}},
	}

	// Run node function.
	done := make(chan State, 1)
	go func() {
		res, err := nf(context.Background(), state)
		if err != nil {
			t.Errorf("unexpected error running tools node: %v", err)
		}
		if res == nil {
			t.Errorf("unexpected nil result from tools node")
		} else if _, ok := res.(State)[StateKeyMessages].([]model.Message); !ok {
			t.Errorf("tools node did not return messages state")
		}
		done <- res.(State)
	}()

	// Expect both starts before proceeding (parallel honored).
	saw := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case nm := <-started:
			saw[nm] = true
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timeout waiting for parallel starts via node func")
		}
	}
	if !saw["A"] || !saw["B"] {
		t.Fatalf("expected both A and B to start, got %v", saw)
	}
	close(allowA)
	close(allowB)
	<-done
}

func TestNewToolsNodeFunc_WithToolCallbacks(t *testing.T) {
	// Track callback invocations.
	var beforeCalled, afterCalled bool
	callbacks := tool.NewCallbacks()
	callbacks.BeforeTool = append(callbacks.BeforeTool,
		func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
			beforeCalled = true
			return &tool.BeforeToolResult{Context: ctx}, nil
		},
	)
	callbacks.AfterTool = append(callbacks.AfterTool,
		func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
			afterCalled = true
			return &tool.AfterToolResult{Context: ctx}, nil
		},
	)

	// Create a simple tool.
	simpleTool := &dummyTool{name: "simple"}
	tools := map[string]tool.Tool{"simple": simpleTool}

	// Build node func with tool callbacks configured via WithToolCallbacks option.
	nf := NewToolsNodeFunc(tools, WithToolCallbacks(callbacks))

	// Prepare state with tool call.
	state := State{
		StateKeyMessages: []model.Message{{
			Role:      model.RoleAssistant,
			ToolCalls: makeToolCalls("simple"),
		}},
	}

	// Run the node function.
	res, err := nf(context.Background(), state)
	require.NoError(t, err)
	require.NotNil(t, res)

	// Verify callbacks were invoked.
	require.True(t, beforeCalled, "BeforeTool callback should be invoked")
	require.True(t, afterCalled, "AfterTool callback should be invoked")

	// Verify messages were created.
	msgs, ok := res.(State)[StateKeyMessages].([]model.Message)
	require.True(t, ok, "expected messages in result state")
	require.Len(t, msgs, 1, "expected one tool result message")
}

func TestNewToolsNodeFunc_ToolCallbacksPrecedence(t *testing.T) {
	// Test that node-configured callbacks take precedence over state callbacks.
	var nodeCallbackUsed, stateCallbackUsed bool

	// Node-level callbacks.
	nodeCallbacks := tool.NewCallbacks()
	nodeCallbacks.BeforeTool = append(nodeCallbacks.BeforeTool,
		func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
			nodeCallbackUsed = true
			return &tool.BeforeToolResult{Context: ctx}, nil
		},
	)

	// State-level callbacks.
	stateCallbacks := tool.NewCallbacks()
	stateCallbacks.BeforeTool = append(stateCallbacks.BeforeTool,
		func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
			stateCallbackUsed = true
			return &tool.BeforeToolResult{Context: ctx}, nil
		},
	)

	// Create a simple tool.
	simpleTool := &dummyTool{name: "simple"}
	tools := map[string]tool.Tool{"simple": simpleTool}

	// Build node func with node-level callbacks.
	nf := NewToolsNodeFunc(tools, WithToolCallbacks(nodeCallbacks))

	// Prepare state with tool call and state-level callbacks.
	state := State{
		StateKeyMessages: []model.Message{{
			Role:      model.RoleAssistant,
			ToolCalls: makeToolCalls("simple"),
		}},
		StateKeyToolCallbacks: stateCallbacks,
	}

	// Run the node function.
	res, err := nf(context.Background(), state)
	require.NoError(t, err)
	require.NotNil(t, res)

	// Verify only node-level callbacks were used.
	require.True(t, nodeCallbackUsed, "node callback should be used")
	require.False(t, stateCallbackUsed, "state callback should NOT be used when node callback is configured")
}

func TestNewToolsNodeFunc_StateCallbacksFallback(t *testing.T) {
	// Test that state callbacks are used when node callbacks are not configured.
	var stateCallbackUsed bool

	// State-level callbacks.
	stateCallbacks := tool.NewCallbacks()
	stateCallbacks.BeforeTool = append(stateCallbacks.BeforeTool,
		func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
			stateCallbackUsed = true
			return &tool.BeforeToolResult{Context: ctx}, nil
		},
	)

	// Create a simple tool.
	simpleTool := &dummyTool{name: "simple"}
	tools := map[string]tool.Tool{"simple": simpleTool}

	// Build node func WITHOUT node-level callbacks.
	nf := NewToolsNodeFunc(tools)

	// Prepare state with tool call and state-level callbacks.
	state := State{
		StateKeyMessages: []model.Message{{
			Role:      model.RoleAssistant,
			ToolCalls: makeToolCalls("simple"),
		}},
		StateKeyToolCallbacks: stateCallbacks,
	}

	// Run the node function.
	res, err := nf(context.Background(), state)
	require.NoError(t, err)
	require.NotNil(t, res)

	// Verify state callbacks were used.
	require.True(t, stateCallbackUsed, "state callback should be used as fallback")
}

func TestBuilderEdges(t *testing.T) {
	builder := NewStateGraph(NewStateSchema())

	testFunc := func(ctx context.Context, state State) (any, error) {
		return State{"processed": true}, nil
	}

	graph, err := builder.
		AddNode("node1", testFunc).
		AddNode("node2", testFunc).
		SetEntryPoint("node1").
		AddEdge("node1", "node2").
		SetFinishPoint("node2").
		Compile()

	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	if graph.EntryPoint() != "node1" {
		t.Errorf("Expected entry point 'node1', got '%s'", graph.EntryPoint())
	}

	edges := graph.Edges("node1")
	if len(edges) != 1 {
		t.Errorf("Expected 1 edge from node1, got %d", len(edges))
	}
	if edges[0].To != "node2" {
		t.Errorf("Expected edge to node2, got %s", edges[0].To)
	}
}

func TestBuilderJoinEdges(t *testing.T) {
	builder := NewStateGraph(NewStateSchema())

	testFunc := func(ctx context.Context, state State) (any, error) {
		return nil, nil
	}

	graph, err := builder.
		AddNode("a", testFunc).
		AddNode("b", testFunc).
		AddNode("c", testFunc).
		SetEntryPoint("a").
		AddJoinEdge([]string{"a", "b"}, "c").
		SetFinishPoint("c").
		Compile()
	require.NoError(t, err)

	edgesA := graph.Edges("a")
	require.Len(t, edgesA, 1)
	require.Equal(t, "c", edgesA[0].To)

	edgesB := graph.Edges("b")
	require.Len(t, edgesB, 1)
	require.Equal(t, "c", edgesB[0].To)

	starts := []string{"a", "b"}
	joinChan := joinChannelName("c", starts)

	ch, ok := graph.getChannel(joinChan)
	require.True(t, ok)
	require.Equal(t, ichannel.BehaviorBarrier, ch.Behavior)
	require.Equal(t, starts, ch.BarrierExpected)

	nodeC, exists := graph.Node("c")
	require.True(t, exists)
	require.Contains(t, nodeC.triggers, joinChan)

	nodeA, exists := graph.Node("a")
	require.True(t, exists)
	require.True(t, hasJoinWriter(nodeA.writers, joinChan, "a"))

	nodeB, exists := graph.Node("b")
	require.True(t, exists)
	require.True(t, hasJoinWriter(nodeB.writers, joinChan, "b"))
}

func TestBuilderJoinEdges_ChannelNameAvoidsCollisions(t *testing.T) {
	builder := NewStateGraph(NewStateSchema())

	testFunc := func(ctx context.Context, state State) (any, error) {
		return nil, nil
	}

	joinNode := "join"

	starts1 := normalizeJoinStarts([]string{"a+b", "c"})
	starts2 := normalizeJoinStarts([]string{"a", "b+c"})
	require.NotEqual(t, starts1, starts2)

	graph, err := builder.
		AddNode(starts1[0], testFunc).
		AddNode(starts1[1], testFunc).
		AddNode(starts2[0], testFunc).
		AddNode(starts2[1], testFunc).
		AddNode(joinNode, testFunc).
		SetEntryPoint(starts1[0]).
		AddJoinEdge(starts1, joinNode).
		AddJoinEdge(starts2, joinNode).
		SetFinishPoint(joinNode).
		Compile()
	require.NoError(t, err)

	joinChan1 := joinChannelName(joinNode, starts1)
	joinChan2 := joinChannelName(joinNode, starts2)
	require.NotEqual(t, joinChan1, joinChan2)

	ch1, ok := graph.getChannel(joinChan1)
	require.True(t, ok)
	require.Equal(t, ichannel.BehaviorBarrier, ch1.Behavior)
	require.Equal(t, starts1, ch1.BarrierExpected)

	ch2, ok := graph.getChannel(joinChan2)
	require.True(t, ok)
	require.Equal(t, ichannel.BehaviorBarrier, ch2.Behavior)
	require.Equal(t, starts2, ch2.BarrierExpected)
}

func TestNormalizeJoinStarts(t *testing.T) {
	require.Nil(t, normalizeJoinStarts(nil))
	require.Nil(t, normalizeJoinStarts([]string{}))

	in := []string{"b", "", Start, "a", "b", End}
	require.Equal(t, []string{"a", "b"}, normalizeJoinStarts(in))
}

func TestBuilderJoinEdges_IgnoresInvalidInput(t *testing.T) {
	builder := NewStateGraph(NewStateSchema())

	testFunc := func(ctx context.Context, state State) (any, error) {
		return nil, nil
	}

	graph, err := builder.
		AddNode("a", testFunc).
		AddNode("b", testFunc).
		SetEntryPoint("a").
		AddJoinEdge(nil, "b").
		AddJoinEdge([]string{""}, "b").
		AddJoinEdge([]string{"a"}, "").
		AddJoinEdge([]string{"a"}, Start).
		SetFinishPoint("b").
		Compile()
	require.NoError(t, err)

	for name := range graph.getAllChannels() {
		require.False(t, strings.HasPrefix(name, ChannelJoinPrefix))
	}
}

func hasJoinWriter(
	writers []channelWriteEntry,
	channelName string,
	value string,
) bool {
	for _, w := range writers {
		v, ok := w.Value.(string)
		if w.Channel == channelName && ok && v == value {
			return true
		}
	}
	return false
}

func TestStateGraphBasic(t *testing.T) {
	schema := NewStateSchema().
		AddField(StateKeyUserInput, StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField(StateKeyLastResponse, StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	sg := NewStateGraph(schema)
	if sg == nil {
		t.Fatal("Expected non-nil StateGraph")
	}

	testFunc := func(ctx context.Context, state State) (any, error) {
		input := state[StateKeyUserInput].(string)
		return State{StateKeyLastResponse: "processed: " + input}, nil
	}

	graph, err := sg.
		AddNode("process", testFunc).
		SetEntryPoint("process").
		SetFinishPoint("process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	node, exists := graph.Node("process")
	if !exists {
		t.Error("Expected process node to exist")
	}
	if node.Function == nil {
		t.Error("Expected node to have function")
	}
}

func TestConditionalEdges(t *testing.T) {
	schema := NewStateSchema().
		AddField(StateKeyUserInput, StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField(StateKeyLastResponse, StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	routingFunc := func(ctx context.Context, state State) (string, error) {
		input := state[StateKeyUserInput].(string)
		if len(input) > 5 {
			return "long", nil
		}
		return "short", nil
	}

	passThrough := func(ctx context.Context, state State) (any, error) {
		return State(state), nil
	}

	processLong := func(ctx context.Context, state State) (any, error) {
		return State{"result": "long processing"}, nil
	}

	processShort := func(ctx context.Context, state State) (any, error) {
		return State{"result": "short processing"}, nil
	}

	graph, err := NewStateGraph(schema).
		AddNode("router", passThrough).
		AddNode("long_process", processLong).
		AddNode("short_process", processShort).
		SetEntryPoint("router").
		AddConditionalEdges("router", routingFunc, map[string]string{
			"long":  "long_process",
			"short": "short_process",
		}).
		SetFinishPoint("long_process").
		SetFinishPoint("short_process").
		Compile()

	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Check that conditional edge was added
	condEdge, exists := graph.ConditionalEdge("router")
	if !exists {
		t.Error("Expected conditional edge to exist")
	}
	if condEdge.PathMap["long"] != "long_process" {
		t.Error("Expected correct path mapping for 'long'")
	}
	if condEdge.PathMap["short"] != "short_process" {
		t.Error("Expected correct path mapping for 'short'")
	}
}

func TestConditionalEdgeProcessing(t *testing.T) {
	// Create a simple state schema
	schema := NewStateSchema().
		AddField("input", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField("result", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	// Create a conditional function
	conditionFunc := func(ctx context.Context, state State) (string, error) {
		input := state["input"].(string)
		if len(input) > 5 {
			return "long", nil
		}
		return "short", nil
	}

	// Create nodes
	longNode := func(ctx context.Context, state State) (any, error) {
		return State{"result": "processed as long"}, nil
	}

	shortNode := func(ctx context.Context, state State) (any, error) {
		return State{"result": "processed as short"}, nil
	}

	// Build graph
	stateGraph := NewStateGraph(schema)
	stateGraph.
		AddNode("start", func(ctx context.Context, state State) (any, error) {
			return state, nil // Pass through
		}).
		AddNode("long", longNode).
		AddNode("short", shortNode).
		SetEntryPoint("start").
		SetFinishPoint("long").
		SetFinishPoint("short").
		AddConditionalEdges("start", conditionFunc, map[string]string{
			"long":  "long",
			"short": "short",
		})

	// Compile graph
	graph, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Test with short input
	t.Run("Short Input", func(t *testing.T) {
		executor, err := NewExecutor(graph)
		if err != nil {
			t.Fatalf("Failed to create executor: %v", err)
		}
		invocation := &agent.Invocation{
			InvocationID: "test-invocation-short",
		}
		eventChan, err := executor.Execute(context.Background(), State{"input": "hi"}, invocation)
		if err != nil {
			t.Fatalf("Failed to execute graph: %v", err)
		}

		// Process events to completion
		for event := range eventChan {
			if event.Error != nil {
				t.Errorf("Execution error: %v", event.Error)
			}
			if event.Done {
				break
			}
		}

		// Verify that short node was triggered
		// This is a basic test - in a real scenario, you'd check the final state
		t.Log("Short input test completed")
	})

	// Test with long input
	t.Run("Long Input", func(t *testing.T) {
		executor, err := NewExecutor(graph)
		if err != nil {
			t.Fatalf("Failed to create executor: %v", err)
		}
		invocation := &agent.Invocation{
			InvocationID: "test-invocation-long",
		}
		eventChan, err := executor.Execute(context.Background(), State{"input": "this is a long input"}, invocation)
		if err != nil {
			t.Fatalf("Failed to execute graph: %v", err)
		}

		// Process events to completion
		for event := range eventChan {
			if event.Error != nil {
				t.Errorf("Execution error: %v", event.Error)
			}
			if event.Done {
				break
			}
		}

		// Verify that long node was triggered
		t.Log("Long input test completed")
	})
}

func TestConditionalEdgeWithTools(t *testing.T) {
	// Create a state schema for messages
	schema := MessagesStateSchema()

	// Create a tools conditional edge test
	stateGraph := NewStateGraph(schema)
	stateGraph.
		AddNode("llm", func(ctx context.Context, state State) (any, error) {
			// Simulate LLM response with tool calls
			return State{
				StateKeyMessages: []model.Message{
					model.NewUserMessage("test"),
					model.NewAssistantMessage("test response"),
				},
			}, nil
		}).
		AddNode("tools", func(ctx context.Context, state State) (any, error) {
			return State{"result": "tools executed"}, nil
		}).
		AddNode("fallback", func(ctx context.Context, state State) (any, error) {
			return State{"result": "fallback executed"}, nil
		}).
		SetEntryPoint("llm").
		SetFinishPoint("tools").
		SetFinishPoint("fallback").
		AddToolsConditionalEdges("llm", "tools", "fallback")

	// Compile graph
	graph, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Test execution
	executor, err := NewExecutor(graph)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}
	invocation := &agent.Invocation{
		InvocationID: "test-invocation-tools",
	}
	eventChan, err := executor.Execute(context.Background(), State{}, invocation)
	if err != nil {
		t.Fatalf("Failed to execute graph: %v", err)
	}

	// Process events to completion
	for event := range eventChan {
		if event.Error != nil {
			t.Errorf("Execution error: %v", event.Error)
		}
		if event.Done {
			break
		}
	}

	t.Log("Tools conditional edge test completed")
}

// TestMultiConditionalEdges_FanOut verifies that a multi-conditional edge
// can trigger multiple next nodes in parallel by returning multiple branch
// keys. Each target node writes to a distinct state key so both effects are
// visible in the final state.
func TestMultiConditionalEdges_FanOut(t *testing.T) {
	schema := NewStateSchema().
		AddField("a", StateField{Type: reflect.TypeOf(0),
			Reducer: DefaultReducer}).
		AddField("b", StateField{Type: reflect.TypeOf(0),
			Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	// Router does nothing; branching decided by multi-condition.
	sg.AddNode("router", func(ctx context.Context,
		s State) (any, error) {
		return nil, nil
	})
	sg.AddNode("A", func(ctx context.Context,
		s State) (any, error) {
		return State{"a": 1}, nil
	})
	sg.AddNode("B", func(ctx context.Context,
		s State) (any, error) {
		return State{"b": 2}, nil
	})
	sg.SetEntryPoint("router")
	// Return two branch keys -> both A and B should run.
	sg.AddMultiConditionalEdges("router",
		func(ctx context.Context, s State) ([]string, error) {
			return []string{"toA", "toB"}, nil
		}, map[string]string{
			"toA": "A",
			"toB": "B",
		})
	sg.SetFinishPoint("A").SetFinishPoint("B")

	g, err := sg.Compile()
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	exec, err := NewExecutor(g)
	if err != nil {
		t.Fatalf("executor create failed: %v", err)
	}

	ch, err := exec.Execute(context.Background(), State{},
		&agent.Invocation{InvocationID: "inv-multi-cond"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	final := make(State)
	for ev := range ch {
		if ev.Done && ev.StateDelta != nil {
			for k, vb := range ev.StateDelta {
				if k == MetadataKeyNode ||
					k == MetadataKeyPregel ||
					k == MetadataKeyChannel ||
					k == MetadataKeyState ||
					k == MetadataKeyCompletion {
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					final[k] = v
				}
			}
		}
	}
	// Both branches should have run and set their keys.
	if final["a"] != float64(1) && final["a"] != 1 {
		t.Fatalf("final[a]=%v, want 1", final["a"])
	}
	if final["b"] != float64(2) && final["b"] != 2 {
		t.Fatalf("final[b]=%v, want 2", final["b"])
	}
}

func dummyReducer(existing, update any) any {
	return update
}

func intDefault() any {
	return 42
}

func stringDefault() any {
	return "default"
}

func nilDefault() any {
	return nil
}

func TestStateSchema_validateSchema(t *testing.T) {
	tests := []struct {
		name        string
		fields      map[string]StateField
		wantErr     bool
		errContains string
	}{
		{
			name: "valid schema with all required fields",
			fields: map[string]StateField{
				"testField": {
					Type:     reflect.TypeOf(""),
					Reducer:  dummyReducer,
					Required: true,
				},
			},
			wantErr: false,
		},
		{
			name: "valid schema with default value matching type",
			fields: map[string]StateField{
				"intField": {
					Type:     reflect.TypeOf(0),
					Reducer:  dummyReducer,
					Default:  intDefault,
					Required: false,
				},
			},
			wantErr: false,
		},
		{
			name: "valid schema with string default value matching type",
			fields: map[string]StateField{
				"intField": {
					Type:     reflect.TypeOf(""),
					Reducer:  dummyReducer,
					Default:  stringDefault,
					Required: false,
				},
			},
			wantErr: false,
		},
		{
			name: "field with nil type should error",
			fields: map[string]StateField{
				"invalidField": {
					Type:     nil,
					Reducer:  dummyReducer,
					Required: true,
				},
			},
			wantErr:     true,
			errContains: "has nil type",
		},
		{
			name: "field with nil reducer should error",
			fields: map[string]StateField{
				"invalidField": {
					Type:     reflect.TypeOf(""),
					Reducer:  nil,
					Required: true,
				},
			},
			wantErr:     true,
			errContains: "has nil reducer",
		},
		{
			name: "field with incompatible default value type",
			fields: map[string]StateField{
				"stringField": {
					Type:     reflect.TypeOf(""),
					Reducer:  dummyReducer,
					Default:  intDefault,
					Required: false,
				},
			},
			wantErr:     true,
			errContains: "has incompatible default value",
		},
		{
			name: "field with nil default for pointer type (should pass)",
			fields: map[string]StateField{
				"pointerField": {
					Type:     reflect.TypeOf((*int)(nil)),
					Reducer:  dummyReducer,
					Default:  nilDefault,
					Required: false,
				},
			},
			wantErr: false,
		},
		{
			name: "field with nil default for interface type (should pass)",
			fields: map[string]StateField{
				"interfaceField": {
					Type:     reflect.TypeOf((*any)(nil)).Elem(),
					Reducer:  dummyReducer,
					Default:  nilDefault,
					Required: false,
				},
			},
			wantErr: false,
		},
		{
			name: "field with nil default for slice type (should pass)",
			fields: map[string]StateField{
				"sliceField": {
					Type:     reflect.TypeOf([]int{}),
					Reducer:  dummyReducer,
					Default:  nilDefault,
					Required: false,
				},
			},
			wantErr: false,
		},
		{
			name: "field with nil default for non-nillable type (should error)",
			fields: map[string]StateField{
				"intField": {
					Type:     reflect.TypeOf(0),
					Reducer:  dummyReducer,
					Default:  nilDefault,
					Required: false,
				},
			},
			wantErr:     true,
			errContains: "nil is not assignable",
		},
		{
			name: "multiple fields with one invalid",
			fields: map[string]StateField{
				"validField1": {
					Type:     reflect.TypeOf(""),
					Reducer:  dummyReducer,
					Required: true,
				},
				"invalidField": {
					Type:     nil,
					Reducer:  dummyReducer,
					Required: true,
				},
				"validField2": {
					Type:     reflect.TypeOf(0),
					Reducer:  dummyReducer,
					Required: false,
				},
			},
			wantErr:     true,
			errContains: "has nil type",
		},
		{
			name: "complex type with valid default",
			fields: map[string]StateField{
				"structField": {
					Type: reflect.TypeOf(struct {
						Name string
						Age  int
					}{}),
					Reducer: dummyReducer,
					Default: func() any {
						return struct {
							Name string
							Age  int
						}{
							Name: "default",
							Age:  25,
						}
					},
					Required: false,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &StateSchema{
				Fields: tt.fields,
			}

			err := s.validateSchema()

			if (err != nil) != tt.wantErr {
				t.Errorf("StateSchema.validateSchema() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if err == nil || err.Error() == "" {
					t.Errorf("Expected error containing '%s', but got nil error", tt.errContains)
					return
				}

				errorMsg := err.Error()
				if tt.errContains != "" && !strings.Contains(errorMsg, tt.errContains) {
					t.Errorf("StateSchema.validateSchema() error = %v, should contain %v", errorMsg, tt.errContains)
				}
			}

			if !tt.wantErr && err != nil {
				t.Errorf("StateSchema.validateSchema() unexpected error = %v", err)
			}
		})
	}
}

func TestStateSchema_validateSchema_Concurrent(t *testing.T) {
	schema := &StateSchema{
		Fields: map[string]StateField{
			"testField": {
				Type:     reflect.TypeOf(""),
				Reducer:  dummyReducer,
				Required: true,
			},
		},
	}

	var wg sync.WaitGroup
	iterations := 100

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			err := schema.validateSchema()
			if err != nil {
				t.Errorf("Concurrent test %d failed: %v", index, err)
			}
		}(i)
	}

	wg.Wait()
}

func TestStateSchema_validateSchema_Empty(t *testing.T) {
	schema := &StateSchema{
		Fields: map[string]StateField{},
	}

	err := schema.validateSchema()
	if err != nil {
		t.Errorf("Empty schema should be valid, got error: %v", err)
	}
}

func TestStateSchema_validateSchema_FieldNameInError(t *testing.T) {
	fieldName := "mySpecialField"
	schema := &StateSchema{
		Fields: map[string]StateField{
			fieldName: {
				Type:    nil,
				Reducer: dummyReducer,
			},
		},
	}

	err := schema.validateSchema()
	if err == nil {
		t.Error("Expected error for nil type, got nil")
		return
	}

	if !strings.Contains(err.Error(), fieldName) {
		t.Errorf("Error message should contain field name '%s', got: %s", fieldName, err.Error())
	}
}

// TestStateGraph_NodeOptionSetters covers several option helpers that were missing coverage.
func TestStateGraph_NodeOptionSetters(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())

	// Prepare callbacks and mappers
	var toolCB tool.Callbacks
	var modelCB model.Callbacks
	inMapper := func(parent State) State { return State{"x": 1} }
	outMapper := func(parent State, _ SubgraphResult) State { return State{"y": 2} }

	// Add node with various options set
	sg.AddNode(
		"opt",
		func(ctx context.Context, s State) (any, error) { return s, nil },
		WithCacheKeyFields("a", "b"),
		WithToolCallbacks(&toolCB),
		WithSubgraphInputMapper(inMapper),
		WithSubgraphOutputMapper(outMapper),
		WithSubgraphIsolatedMessages(true),
		WithSubgraphInputFromLastResponse(),
		WithSubgraphEventScope("scope/x"),
		WithModelCallbacks(&modelCB),
	)

	n := sg.graph.nodes["opt"]
	if n == nil {
		t.Fatalf("node not added")
	}
	if n.cacheKeySelector == nil {
		t.Fatalf("cacheKeySelector not set by WithCacheKeyFields")
	}
	// Verify subset selection from WithCacheKeyFields by invoking selector on a map
	sel := n.cacheKeySelector(map[string]any{"a": 1, "b": 2, "c": 3})
	if got, ok := sel.(map[string]any); !ok || len(got) != 2 || got["a"] != 1 || got["b"] != 2 {
		t.Fatalf("unexpected cacheKeySelector result: %#v", sel)
	}
	if n.toolCallbacks != &toolCB {
		t.Fatalf("toolCallbacks not set")
	}
	if n.agentInputMapper == nil || n.agentOutputMapper == nil {
		t.Fatalf("subgraph mappers not set")
	}
	if !n.agentIsolatedMessages {
		t.Fatalf("agentIsolatedMessages not set true")
	}
	if !n.agentInputFromLastResponse {
		t.Fatalf("agentInputFromLastResponse not set true")
	}
	if n.agentEventScope != "scope/x" {
		t.Fatalf("agentEventScope not set: %q", n.agentEventScope)
	}
	if n.modelCallbacks != &modelCB {
		t.Fatalf("modelCallbacks not set")
	}
}

// TestStateGraph_WithCacheKeySelector_OverridesSelector verifies the custom selector assignment.
func TestStateGraph_WithCacheKeySelector_OverridesSelector(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("opt2", func(ctx context.Context, s State) (any, error) { return s, nil },
		WithCacheKeySelector(func(m map[string]any) any { return m["a"] }),
	)
	n := sg.graph.nodes["opt2"]
	if n == nil || n.cacheKeySelector == nil {
		t.Fatalf("cacheKeySelector not set")
	}
	v := n.cacheKeySelector(map[string]any{"a": 123, "b": 9})
	if v != 123 {
		t.Fatalf("expected selector to pick 'a' value, got %#v", v)
	}
}

// TestStateGraph_AddSubgraphNode ensures it adds an agent-typed node.
func TestStateGraph_AddSubgraphNode(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	sg.AddSubgraphNode("agentX")
	n := sg.graph.nodes["agentX"]
	if n == nil {
		t.Fatalf("subgraph node not added")
	}
	if n.Type != NodeTypeAgent {
		t.Fatalf("expected NodeTypeAgent, got %v", n.Type)
	}
}

func TestBuildAgentInvocationWithStateAndScope_PreservesRequestID(
	t *testing.T,
) {
	const (
		parentKey = "test_app"
		requestID = "req-123"
		userInput = "hi"
	)

	sess := &session.Session{}
	parentInv := agent.NewInvocation(
		agent.WithInvocationAgent(&messageEchoAgent{name: "parent"}),
		agent.WithInvocationMessage(model.NewUserMessage(userInput)),
		agent.WithInvocationRunOptions(agent.RunOptions{
			ModelName: "model-x",
			RequestID: requestID,
			Resume:    true,
		}),
		agent.WithInvocationEventFilterKey(parentKey),
		agent.WithInvocationSession(sess),
	)
	evt := event.NewResponseEvent(
		parentInv.InvocationID,
		"user",
		&model.Response{
			Done: false,
			Choices: []model.Choice{
				{
					Index:   0,
					Message: model.NewUserMessage(userInput),
				},
			},
		},
	)
	agent.InjectIntoEvent(parentInv, evt)
	sess.Events = []event.Event{*evt}

	parentState := State{
		StateKeyUserInput: userInput,
		StateKeySession:   sess,
	}
	runtime := State{"x": 1}

	ctx := agent.NewInvocationContext(context.Background(), parentInv)
	childInv := buildAgentInvocationWithStateAndScope(
		ctx,
		parentState,
		runtime,
		&messageEchoAgent{name: "child"},
		"",
		"",
	)
	require.Equal(t, requestID, childInv.RunOptions.RequestID)
	require.Equal(t, "model-x", childInv.RunOptions.ModelName)
	require.True(t, childInv.RunOptions.Resume)
	require.Equal(t, map[string]any(runtime), childInv.RunOptions.RuntimeState)
	require.Equal(t, userInput, childInv.Message.Content)
	require.Equal(t, "child", childInv.AgentName)

	require.NotEmpty(t, childInv.GetEventFilterKey())
}

func TestOptionBranches_EmptyPoliciesAndCallbacks(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	// WithRetryPolicy with no policies should not crash or modify
	sg.AddNode("n", func(ctx context.Context, s State) (any, error) { return s, nil }, WithRetryPolicy())
	n := sg.graph.nodes["n"]
	if n == nil {
		t.Fatalf("node missing")
	}
	if len(n.retryPolicies) != 0 {
		t.Fatalf("retry policies should be empty")
	}

	// WithPostNodeCallback and WithNodeErrorCallback allocate callbacks holder
	sg.AddNode("cbs", func(ctx context.Context, s State) (any, error) { return s, nil },
		WithPostNodeCallback(func(context.Context, *NodeCallbackContext, State, any, error) (any, error) { return nil, nil }),
		WithNodeErrorCallback(func(context.Context, *NodeCallbackContext, State, error) {}),
		WithAgentNodeEventCallback(func(context.Context, *NodeCallbackContext, State, *event.Event) {}),
	)
	if sg.graph.nodes["cbs"].callbacks == nil {
		t.Fatalf("callbacks should be allocated")
	}
}

// TestMustCompile_PanicsOnInvalid covers the panic path (no entry point).
func TestMustCompile_PanicsOnInvalid(t *testing.T) {
	defer func() { _ = recover() }()
	sg := NewStateGraph(NewStateSchema())
	_ = sg.MustCompile() // no entry point triggers panic
	t.Fatalf("expected panic")
}

// TestProcessModelResponse_ErrorPassing tests that modelErr is correctly passed to callbacks
// when config.Response.Error is not nil.
func TestProcessModelResponse_ErrorPassing(t *testing.T) {
	tests := []struct {
		name       string
		response   *model.Response
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "response with API error",
			response: &model.Response{
				Error: &model.ResponseError{
					Type:    model.ErrorTypeAPIError,
					Message: "API key invalid",
				},
			},
			wantErr:    true,
			wantErrMsg: "api_error: API key invalid",
		},
		{
			name: "response with stream error",
			response: &model.Response{
				Error: &model.ResponseError{
					Type:    model.ErrorTypeStreamError,
					Message: "stream interrupted",
				},
			},
			wantErr:    true,
			wantErrMsg: "stream_error: stream interrupted",
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
			name: "response with nil error field",
			response: &model.Response{
				Error:   nil,
				Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}},
			},
			wantErr:    false,
			wantErrMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedErr error
			cbs := model.NewCallbacks().RegisterAfterModel(
				func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
					receivedErr = modelErr
					return nil, nil
				},
			)

			dummyModel := &mockModel{name: "test-model"}

			_, _, err := processModelResponse(context.Background(), modelResponseConfig{
				Response:       tt.response,
				ModelCallbacks: cbs,
				EventChan:      make(chan *event.Event, 1),
				InvocationID:   "test-inv",
				SessionID:      "test-session",
				LLMModel:       dummyModel,
				Request:        &model.Request{Messages: []model.Message{model.NewUserMessage("test")}},
				Span:           oteltrace.SpanFromContext(context.Background()),
			})

			// processModelResponse returns error when response.Error is not nil.
			if tt.response != nil && tt.response.Error != nil {
				if err == nil {
					t.Errorf("expected processModelResponse to return error, but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("expected processModelResponse to return nil, but got: %v", err)
				}
			}

			// Check callback received correct error.
			if tt.wantErr {
				if receivedErr == nil {
					t.Errorf("expected callback to receive error, but got nil")
				} else if receivedErr.Error() != tt.wantErrMsg {
					t.Errorf("expected error message %q, got %q", tt.wantErrMsg, receivedErr.Error())
				}
			} else {
				if receivedErr != nil {
					t.Errorf("expected callback to receive nil error, but got: %v", receivedErr)
				}
			}
		})
	}
}

// mockModel is a simple mock implementation of model.Model for testing.
type mockModel struct {
	name string
}

func (m *mockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("test")}}}
	close(ch)
	return ch, nil
}

func (m *mockModel) Info() model.Info {
	return model.Info{Name: m.name}
}

// stubModel returns a single response then closes channel.
type stubModel struct {
	resp *model.Response
}

func (s *stubModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- s.resp
	close(ch)
	return ch, nil
}

func (s *stubModel) Info() model.Info {
	return model.Info{Name: "stub"}
}

func newRunner(respID string) *llmRunner {
	return &llmRunner{
		llmModel: &stubModel{
			resp: &model.Response{
				ID: respID,
				Choices: []model.Choice{{
					Message: model.Message{Role: model.RoleAssistant, Content: "ok"},
				}},
				Done: true,
			},
		},
		generationConfig: model.GenerationConfig{Stream: true},
	}
}

type streamRecordingModel struct {
	lastStream bool
}

type testSubAgentProvider struct {
	sub agent.Agent
}

func (p *testSubAgentProvider) FindSubAgent(name string) agent.Agent {
	if p.sub == nil {
		return nil
	}
	if name == p.sub.Info().Name {
		return p.sub
	}
	return nil
}

func (m *streamRecordingModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.lastStream = req.GenerationConfig.Stream
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("ok"),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *streamRecordingModel) Info() model.Info {
	return model.Info{Name: "stream-recording"}
}

func TestLLMRunnerSetsLastResponseID(t *testing.T) {
	ctx, span := trace.Tracer.Start(context.Background(), "test")
	defer span.End()

	tests := []struct {
		name   string
		run    func(r *llmRunner) (State, error)
		expect string
	}{
		{
			name: "one-shot",
			run: func(r *llmRunner) (State, error) {
				res, err := r.executeOneShotStage(
					ctx,
					State{},
					[]model.Message{model.NewUserMessage("hi")},
					span,
					nil,
				)
				require.NoError(t, err)
				return res.(State), nil
			},
			expect: "resp-one",
		},
		{
			name: "user-input",
			run: func(r *llmRunner) (State, error) {
				res, err := r.executeUserInputStage(
					ctx,
					State{StateKeyUserInput: "hi"},
					StateKeyUserInput,
					"hi",
					span,
				)
				require.NoError(t, err)
				return res.(State), nil
			},
			expect: "resp-user",
		},
		{
			name: "history",
			run: func(r *llmRunner) (State, error) {
				res, err := r.executeHistoryStage(ctx, State{StateKeyMessages: []model.Message{}}, span)
				require.NoError(t, err)
				return res.(State), nil
			},
			expect: "resp-hist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := newRunner(tt.expect)
			state, err := tt.run(runner)
			require.NoError(t, err)
			require.Equal(t, tt.expect, state[StateKeyLastResponseID])
		})
	}
}

func TestLLMRunner_CustomUserInputKey(t *testing.T) {
	const (
		testCustomInputKey = "custom_input"
		testCustomInput    = "from-custom"
	)

	ctx, span := trace.Tracer.Start(context.Background(), "test")
	defer span.End()

	runner := newRunner("resp-custom")
	runner.userInputKey = testCustomInputKey

	res, err := runner.execute(ctx, State{
		StateKeyMessages:   []model.Message{},
		StateKeyUserInput:  "from-user",
		testCustomInputKey: testCustomInput,
	}, span)
	require.NoError(t, err)

	state, ok := res.(State)
	require.True(t, ok)
	require.Equal(t, "", state[testCustomInputKey])
	_, hasDefaultKey := state[StateKeyUserInput]
	require.False(t, hasDefaultKey)

	merged := MessageReducer([]model.Message{}, state[StateKeyMessages])
	msgs, ok := merged.([]model.Message)
	require.True(t, ok)
	require.Len(t, msgs, 2)
	require.Equal(t, model.RoleUser, msgs[0].Role)
	require.Equal(t, testCustomInput, msgs[0].Content)
	require.Equal(t, model.RoleAssistant, msgs[1].Role)
	require.Equal(t, "ok", msgs[1].Content)
}

func TestLLMRunner_OverridesStreamFromRunOptions(t *testing.T) {
	ctx, span := trace.Tracer.Start(context.Background(), "test")
	defer span.End()

	rm := &streamRecordingModel{}
	runner := &llmRunner{
		llmModel:         rm,
		generationConfig: model.GenerationConfig{Stream: true},
	}

	stream := false
	inv := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.RunOptions{Stream: &stream}),
	)
	ctx = agent.NewInvocationContext(ctx, inv)

	_, err := runner.executeModel(
		ctx,
		State{},
		[]model.Message{model.NewUserMessage("hi")},
		span,
		"",
	)
	require.NoError(t, err)
	require.False(t, rm.lastStream)
}

func TestAgentNodeFunc_OverridesStreamFromRunOptions(t *testing.T) {
	const (
		testInvocationID = "inv-1"
		testNodeID       = "node-1"
		testAgentName    = "agent-1"
	)
	target := &dummyAgent{name: testAgentName}
	provider := &testSubAgentProvider{sub: target}

	stream := false
	parentInv := agent.NewInvocation(
		agent.WithInvocationID(testInvocationID),
		agent.WithInvocationRunOptions(agent.RunOptions{Stream: &stream}),
	)
	ctx := agent.NewInvocationContext(context.Background(), parentInv)

	eventCh := make(chan *event.Event, 8)
	exec := &ExecutionContext{
		InvocationID: testInvocationID,
		EventChan:    eventCh,
	}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: testNodeID,
		StateKeyParentAgent:   provider,
		StateKeyUserInput:     "hi",
	}

	nodeFn := NewAgentNodeFunc(testAgentName)
	_, err := nodeFn(ctx, state)
	require.NoError(t, err)
}

func TestLLMRunner_OneShotMessagesByNode_TakesPrecedence(t *testing.T) {
	ctx, span := trace.Tracer.Start(context.Background(), "test")
	defer span.End()

	runner := newRunner("resp")
	runner.nodeID = "llm1"

	state := State{
		StateKeyOneShotMessagesByNode: map[string][]model.Message{
			"llm1": {model.NewUserMessage("by-node")},
		},
		StateKeyOneShotMessages: []model.Message{
			model.NewUserMessage("global"),
		},
	}

	res, err := runner.execute(ctx, state, span)
	require.NoError(t, err)

	out := res.(State)
	_, hasGlobalClear := out[StateKeyOneShotMessages]
	require.False(t, hasGlobalClear)

	byNode, ok := out[StateKeyOneShotMessagesByNode].(map[string][]model.Message)
	require.True(t, ok)
	_, exists := byNode["llm1"]
	require.True(t, exists)
	require.Len(t, byNode["llm1"], 0)
}

func TestLLMRunner_OneShotMessagesByNode_FallsBackToGlobal(t *testing.T) {
	ctx, span := trace.Tracer.Start(context.Background(), "test")
	defer span.End()

	runner := newRunner("resp")
	runner.nodeID = "llm1"

	state := State{
		StateKeyOneShotMessagesByNode: map[string][]model.Message{
			"llm2": {model.NewUserMessage("by-node-other")},
		},
		StateKeyOneShotMessages: []model.Message{
			model.NewUserMessage("global"),
		},
	}

	res, err := runner.execute(ctx, state, span)
	require.NoError(t, err)

	out := res.(State)
	_, hasByNodeClear := out[StateKeyOneShotMessagesByNode]
	require.False(t, hasByNodeClear)

	_, hasGlobalClear := out[StateKeyOneShotMessages]
	require.True(t, hasGlobalClear)
}

func TestExecuteSingleToolCallPropagatesResponseID(t *testing.T) {
	tests := []struct {
		name   string
		state  State
		expect string
	}{
		{
			name: "with-response-id",
			state: State{
				StateKeyCurrentNodeID:  "node-1",
				StateKeyLastResponseID: "resp-123",
			},
			expect: "resp-123",
		},
		{
			name:   "missing-response-id",
			state:  State{StateKeyCurrentNodeID: "node-2"},
			expect: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, span := trace.Tracer.Start(context.Background(), "tool")
			defer span.End()

			ch := make(chan *event.Event, 2)
			_, err := executeSingleToolCall(ctx, singleToolCallConfig{
				ToolCall: model.ToolCall{
					ID: "call-1",
					Function: model.FunctionDefinitionParam{
						Name:      "echo",
						Arguments: []byte(`{"x":1}`),
					},
				},
				Tools: map[string]tool.Tool{
					"echo": &blockingTool{name: "echo", result: map[string]int{"x": 1}},
				},
				InvocationID: "inv-1",
				EventChan:    ch,
				Span:         span,
				State:        tt.state,
			})
			require.NoError(t, err)

			events := []*event.Event{<-ch, <-ch}
			if len(ch) != 0 {
				t.Fatalf("expected channel to be drained")
			}

			seenPhases := map[ToolExecutionPhase]bool{}
			for _, evt := range events {
				raw := evt.StateDelta[MetadataKeyTool]
				require.NotEmpty(t, raw)

				var meta ToolExecutionMetadata
				require.NoError(t, json.Unmarshal(raw, &meta))
				require.Equal(t, tt.expect, meta.ResponseID)
				seenPhases[meta.Phase] = true
			}
			require.True(t, seenPhases[ToolExecutionPhaseStart])
			require.True(t, seenPhases[ToolExecutionPhaseComplete])
		})
	}
}

func TestExecuteSingleToolCall_InterruptWithCustomResultEmitsOutput(t *testing.T) {
	ctx, span := trace.Tracer.Start(context.Background(), "tool")
	defer span.End()

	ch := make(chan *event.Event, 2)

	callbacks := tool.NewCallbacks()
	customResult := map[string]any{"reason": "need_user_confirm"}
	callbacks.RegisterAfterTool(func(
		_ context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{CustomResult: customResult}, NewInterruptError("pause")
	})

	_, err := executeSingleToolCall(ctx, singleToolCallConfig{
		ToolCall: model.ToolCall{
			ID: "call-1",
			Function: model.FunctionDefinitionParam{
				Name:      "echo",
				Arguments: []byte(`{"x":1}`),
			},
		},
		Tools: map[string]tool.Tool{
			"echo": &blockingTool{name: "echo", result: map[string]int{"x": 1}},
		},
		InvocationID:  "inv-1",
		EventChan:     ch,
		Span:          span,
		ToolCallbacks: callbacks,
		State:         State{StateKeyCurrentNodeID: "node-1"},
	})
	require.Error(t, err)
	var interruptErr *InterruptError
	require.ErrorAs(t, err, &interruptErr)

	events := []*event.Event{<-ch, <-ch}

	seenPhases := map[ToolExecutionPhase]bool{}
	var complete ToolExecutionMetadata
	for _, evt := range events {
		raw := evt.StateDelta[MetadataKeyTool]
		require.NotEmpty(t, raw)

		var meta ToolExecutionMetadata
		require.NoError(t, json.Unmarshal(raw, &meta))
		seenPhases[meta.Phase] = true
		if meta.Phase == ToolExecutionPhaseComplete {
			complete = meta
		}
	}

	require.True(t, seenPhases[ToolExecutionPhaseStart])
	require.True(t, seenPhases[ToolExecutionPhaseComplete])
	require.Empty(t, complete.Error)
	require.JSONEq(t, `{"reason":"need_user_confirm"}`, complete.Output)
}

func TestExecuteSingleToolCall_InterruptFromToolEmitsOutput(t *testing.T) {
	ctx, span := trace.Tracer.Start(context.Background(), "tool")
	defer span.End()

	ch := make(chan *event.Event, 2)

	_, err := executeSingleToolCall(ctx, singleToolCallConfig{
		ToolCall: model.ToolCall{
			ID: "call-1",
			Function: model.FunctionDefinitionParam{
				Name:      "echo",
				Arguments: []byte(`{"x":1}`),
			},
		},
		Tools: map[string]tool.Tool{
			"echo": &resultWithErrorTool{
				name:   "echo",
				result: map[string]int{"x": 1},
				err:    NewInterruptError("pause"),
			},
		},
		InvocationID: "inv-1",
		EventChan:    ch,
		Span:         span,
		State:        State{StateKeyCurrentNodeID: "node-1"},
	})
	require.Error(t, err)
	var interruptErr *InterruptError
	require.ErrorAs(t, err, &interruptErr)

	events := []*event.Event{<-ch, <-ch}

	seenPhases := map[ToolExecutionPhase]bool{}
	var complete ToolExecutionMetadata
	for _, evt := range events {
		raw := evt.StateDelta[MetadataKeyTool]
		require.NotEmpty(t, raw)

		var meta ToolExecutionMetadata
		require.NoError(t, json.Unmarshal(raw, &meta))
		seenPhases[meta.Phase] = true
		if meta.Phase == ToolExecutionPhaseComplete {
			complete = meta
		}
	}

	require.True(t, seenPhases[ToolExecutionPhaseStart])
	require.True(t, seenPhases[ToolExecutionPhaseComplete])
	require.Empty(t, complete.Error)
	require.JSONEq(t, `{"x":1}`, complete.Output)
}

func TestExecuteSingleToolCall_InterruptBeforeCallbackWithCustomResultEmitsOutputAndSkipsTool(t *testing.T) {
	ctx, span := trace.Tracer.Start(context.Background(), "tool")
	defer span.End()

	ch := make(chan *event.Event, 2)

	callbacks := tool.NewCallbacks()
	customResult := map[string]any{"reason": "need_user_confirm"}
	callbacks.RegisterBeforeTool(func(
		_ context.Context,
		_ *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		return &tool.BeforeToolResult{CustomResult: customResult}, NewInterruptError("pause")
	})

	tl := &captureTool{name: "echo", result: map[string]int{"x": 1}}

	_, err := executeSingleToolCall(ctx, singleToolCallConfig{
		ToolCall: model.ToolCall{
			ID: "call-1",
			Function: model.FunctionDefinitionParam{
				Name:      "echo",
				Arguments: []byte(`{"x":1}`),
			},
		},
		Tools: map[string]tool.Tool{
			"echo": tl,
		},
		InvocationID:  "inv-1",
		EventChan:     ch,
		Span:          span,
		ToolCallbacks: callbacks,
		State:         State{StateKeyCurrentNodeID: "node-1"},
	})
	require.Error(t, err)
	var interruptErr *InterruptError
	require.ErrorAs(t, err, &interruptErr)
	require.False(t, tl.called)

	events := []*event.Event{<-ch, <-ch}

	seenPhases := map[ToolExecutionPhase]bool{}
	var complete ToolExecutionMetadata
	for _, evt := range events {
		raw := evt.StateDelta[MetadataKeyTool]
		require.NotEmpty(t, raw)

		var meta ToolExecutionMetadata
		require.NoError(t, json.Unmarshal(raw, &meta))
		seenPhases[meta.Phase] = true
		if meta.Phase == ToolExecutionPhaseComplete {
			complete = meta
		}
	}

	require.True(t, seenPhases[ToolExecutionPhaseStart])
	require.True(t, seenPhases[ToolExecutionPhaseComplete])
	require.Empty(t, complete.Error)
	require.JSONEq(t, `{"reason":"need_user_confirm"}`, complete.Output)
}

func TestExtractResponseIDNonResponse(t *testing.T) {
	require.Equal(t, "", extractResponseID(struct{}{}))
	require.Equal(t, "", extractResponseID(nil))
}

func TestMessagesStateSchemaIncludesLastResponseID(t *testing.T) {
	schema := MessagesStateSchema()
	field, ok := schema.Fields[StateKeyLastResponseID]
	require.True(t, ok, "StateKeyLastResponseID should be present")
	require.Equal(t, reflect.TypeOf(""), field.Type)
	require.NotNil(t, field.Reducer)
}

const (
	testToolSetName       = "set"
	testToolBaseName      = "echo"
	testNamespacedToolKey = testToolSetName + "_" + testToolBaseName
)

type simpleTool struct {
	name string
}

func (s *simpleTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name}
}

func (s *simpleTool) Call(ctx context.Context, _ []byte) (any, error) {
	return map[string]any{"ok": true}, nil
}

type countingToolSet struct {
	name  string
	calls int
}

func (s *countingToolSet) Tools(ctx context.Context) []tool.Tool {
	s.calls++
	return []tool.Tool{&simpleTool{name: testToolBaseName}}
}

func (s *countingToolSet) Close() error { return nil }

func (s *countingToolSet) Name() string { return s.name }

type recordingModel struct {
	lastTools    map[string]tool.Tool
	lastMessages []model.Message
}

func (m *recordingModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.lastTools = req.Tools
	if len(req.Messages) == 0 {
		m.lastMessages = nil
	} else {
		m.lastMessages = append([]model.Message(nil), req.Messages...)
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("ok"),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *recordingModel) Info() model.Info {
	return model.Info{Name: "recording"}
}

func TestAddLLMNode_StaticToolSets(t *testing.T) {
	schema := MessagesStateSchema()
	rm := &recordingModel{}
	sg := NewStateGraph(schema)
	ts := &countingToolSet{name: testToolSetName}

	sg.AddLLMNode(
		"llm",
		rm,
		"inst",
		nil,
		WithToolSets([]tool.ToolSet{ts}),
	)

	n := sg.graph.nodes["llm"]
	require.NotNil(t, n)

	state := State{StateKeyUserInput: "hi"}

	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)

	_, err = n.Function(context.Background(), state)
	require.NoError(t, err)

	require.Equal(t, 1, ts.calls)
}

func TestAddLLMNode_RefreshToolSetsOnRun(t *testing.T) {
	schema := MessagesStateSchema()
	rm := &recordingModel{}
	sg := NewStateGraph(schema)
	ts := &countingToolSet{name: testToolSetName}

	sg.AddLLMNode(
		"llm",
		rm,
		"inst",
		nil,
		WithToolSets([]tool.ToolSet{ts}),
		WithRefreshToolSetsOnRun(true),
	)

	n := sg.graph.nodes["llm"]
	require.NotNil(t, n)

	state := State{StateKeyUserInput: "hi"}

	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)
	firstTools := rm.lastTools
	require.NotNil(t, firstTools)
	require.NotEmpty(t, firstTools)

	_, err = n.Function(context.Background(), state)
	require.NoError(t, err)
	secondTools := rm.lastTools
	require.NotNil(t, secondTools)
	require.NotEmpty(t, secondTools)

	require.Equal(t, 2, ts.calls)
}

func TestAddLLMNode_PlaceholderInvocationState(t *testing.T) {
	const (
		nodeID        = "llm"
		userInput     = "hi"
		invocationID  = "inv"
		agentName     = "agent"
		invStateKey   = "case"
		invStateValue = "case-1"
		instruction   = "Case: {invocation:case}"
		wantSystem    = "Case: " + invStateValue
	)

	schema := MessagesStateSchema()
	rm := &recordingModel{}
	sg := NewStateGraph(schema)
	sg.AddLLMNode(nodeID, rm, instruction, nil)

	g, err := sg.
		SetEntryPoint(nodeID).
		SetFinishPoint(nodeID).
		Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	inv := agent.NewInvocation(agent.WithInvocationID(invocationID))
	inv.AgentName = agentName
	inv.SetState(invStateKey, invStateValue)

	eventCh, err := exec.Execute(
		context.Background(),
		State{StateKeyUserInput: userInput},
		inv,
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.NotEmpty(t, rm.lastMessages)
	require.Equal(t, model.RoleSystem, rm.lastMessages[0].Role)
	require.Equal(t, wantSystem, rm.lastMessages[0].Content)
}

func TestNewToolsNodeFunc_StaticToolSets(t *testing.T) {
	schema := MessagesStateSchema()
	sg := NewStateGraph(schema)
	ts := &countingToolSet{name: testToolSetName}

	sg.AddToolsNode(
		"tools",
		nil,
		WithToolSets([]tool.ToolSet{ts}),
	)

	n := sg.graph.nodes["tools"]
	require.NotNil(t, n)

	messages := []model.Message{
		model.NewUserMessage("hi"),
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				ID:   "call-1",
				Function: model.FunctionDefinitionParam{
					Name:      testNamespacedToolKey,
					Arguments: []byte(`{}`),
				},
			}},
		},
	}

	state := State{StateKeyMessages: messages}

	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)

	_, err = n.Function(context.Background(), state)
	require.NoError(t, err)

	require.Equal(t, 1, ts.calls)
}

func TestNewToolsNodeFunc_RefreshToolSetsOnRun(t *testing.T) {
	schema := MessagesStateSchema()
	sg := NewStateGraph(schema)
	ts := &countingToolSet{name: testToolSetName}

	sg.AddToolsNode(
		"tools",
		nil,
		WithToolSets([]tool.ToolSet{ts}),
		WithRefreshToolSetsOnRun(true),
	)

	n := sg.graph.nodes["tools"]
	require.NotNil(t, n)

	messages := []model.Message{
		model.NewUserMessage("hi"),
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				ID:   "call-1",
				Function: model.FunctionDefinitionParam{
					Name:      testNamespacedToolKey,
					Arguments: []byte(`{}`),
				},
			}},
		},
	}

	state := State{StateKeyMessages: messages}

	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)

	_, err = n.Function(context.Background(), state)
	require.NoError(t, err)

	require.Equal(t, 2, ts.calls)
}

func TestStateGraph_StaticInterruptNodes_SetFlags(t *testing.T) {
	const (
		nodeA = "a"
		nodeB = "b"
	)

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.AddNode(nodeB, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})

	sg.WithInterruptBeforeNodes(nodeA)
	sg.WithInterruptAfterNodes(nodeB)

	sg.SetEntryPoint(nodeA)
	sg.AddEdge(nodeA, nodeB)
	sg.SetFinishPoint(nodeB)

	_, err := sg.Compile()
	require.NoError(t, err)

	a := sg.graph.nodes[nodeA]
	b := sg.graph.nodes[nodeB]
	require.NotNil(t, a)
	require.NotNil(t, b)
	require.True(t, a.interruptBefore)
	require.True(t, b.interruptAfter)
}

func TestStateGraph_StaticInterruptNodes_UnknownNodeBuildError(t *testing.T) {
	const (
		nodeA         = "a"
		nodeB         = "b"
		missingBefore = "missing_before"
		missingAfter  = "missing_after"
	)

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode(nodeA, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})
	sg.AddNode(nodeB, func(ctx context.Context, state State) (any, error) {
		return nil, nil
	})

	sg.WithInterruptBeforeNodes(missingBefore)
	sg.WithInterruptAfterNodes(missingAfter)

	sg.SetEntryPoint(nodeA)
	sg.AddEdge(nodeA, nodeB)
	sg.SetFinishPoint(nodeB)

	_, err := sg.Compile()
	require.Error(t, err)
}

type latestCheckpointAgent struct {
	name string
	exec *Executor
}

func (a *latestCheckpointAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *latestCheckpointAgent) Tools() []tool.Tool { return nil }

func (a *latestCheckpointAgent) SubAgents() []agent.Agent { return nil }

func (a *latestCheckpointAgent) FindSubAgent(_ string) agent.Agent {
	return nil
}

func (a *latestCheckpointAgent) Executor() *Executor { return a.exec }

func (a *latestCheckpointAgent) Run(
	_ context.Context,
	_ *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func TestStateStringOr(t *testing.T) {
	const (
		key      = "k"
		fallback = "fb"
		value    = "v"
	)

	require.Equal(t, fallback, stateStringOr(nil, key, fallback))
	require.Equal(
		t,
		fallback,
		stateStringOr(State{key: testEmptyString}, key, fallback),
	)
	require.Equal(t, value, stateStringOr(State{key: value}, key, fallback))
}

func TestResumeCommandForSubgraph(t *testing.T) {
	const (
		taskID      = "task"
		otherTaskID = "other"
		resumeValue = "ok"
	)

	require.Nil(t, resumeCommandForSubgraph(nil, taskID))

	cmd := resumeCommandForSubgraph(
		State{ResumeChannel: resumeValue},
		taskID,
	)
	require.NotNil(t, cmd)
	require.Equal(t, resumeValue, cmd.Resume)

	stateMap := map[string]any{
		taskID:      resumeValue,
		otherTaskID: resumeValue,
	}
	cmd = resumeCommandForSubgraph(
		State{StateKeyResumeMap: stateMap},
		taskID,
	)
	require.NotNil(t, cmd)
	require.Equal(t, map[string]any{taskID: resumeValue}, cmd.ResumeMap)

	cmd = resumeCommandForSubgraph(
		State{StateKeyResumeMap: stateMap},
		testEmptyString,
	)
	require.NotNil(t, cmd)
	require.Equal(t, stateMap, cmd.ResumeMap)
	stateMap["new"] = "x"
	_, ok := cmd.ResumeMap["new"]
	require.False(t, ok)

	cmd = resumeCommandForSubgraph(
		State{StateKeyResumeMap: map[string]any{otherTaskID: "x"}},
		taskID,
	)
	require.Nil(t, cmd)
}

func TestExtractPregelInterrupt(t *testing.T) {
	const (
		invocationID = "inv"
		author       = "a"
		nodeID       = "n"
		interruptKey = "k"
		interruptVal = "prompt"
	)

	_, ok := extractPregelInterrupt(nil)
	require.False(t, ok)

	e := event.New(invocationID, author, event.WithObject("other"))
	_, ok = extractPregelInterrupt(e)
	require.False(t, ok)

	e = event.New(
		invocationID,
		author,
		event.WithObject(ObjectTypeGraphPregelStep),
	)
	_, ok = extractPregelInterrupt(e)
	require.False(t, ok)

	e = event.New(
		invocationID,
		author,
		event.WithObject(ObjectTypeGraphPregelStep),
		event.WithStateDelta(map[string][]byte{}),
	)
	_, ok = extractPregelInterrupt(e)
	require.False(t, ok)

	e = event.New(
		invocationID,
		author,
		event.WithObject(ObjectTypeGraphPregelStep),
		event.WithStateDelta(map[string][]byte{
			MetadataKeyPregel: []byte("{"),
		}),
	)
	_, ok = extractPregelInterrupt(e)
	require.False(t, ok)

	meta := PregelStepMetadata{
		NodeID:         nodeID,
		InterruptValue: interruptVal,
	}
	b, err := json.Marshal(meta)
	require.NoError(t, err)
	e = event.New(
		invocationID,
		author,
		event.WithObject(ObjectTypeGraphPregelStep),
		event.WithStateDelta(map[string][]byte{
			MetadataKeyPregel: b,
		}),
	)
	intr, ok := extractPregelInterrupt(e)
	require.True(t, ok)
	require.NotNil(t, intr)
	require.Equal(t, nodeID, intr.NodeID)
	require.Equal(t, nodeID, intr.TaskID)
	require.Equal(t, nodeID, intr.Key)
	require.Equal(t, interruptVal, intr.Value)

	meta = PregelStepMetadata{
		NodeID:         nodeID,
		InterruptKey:   interruptKey,
		InterruptValue: interruptVal,
	}
	b, err = json.Marshal(meta)
	require.NoError(t, err)
	e = event.New(
		invocationID,
		author,
		event.WithObject(ObjectTypeGraphPregelStep),
		event.WithStateDelta(map[string][]byte{
			MetadataKeyPregel: b,
		}),
	)
	intr, ok = extractPregelInterrupt(e)
	require.True(t, ok)
	require.NotNil(t, intr)
	require.Equal(t, nodeID, intr.NodeID)
	require.Equal(t, interruptKey, intr.TaskID)
	require.Equal(t, interruptKey, intr.Key)
	require.Equal(t, interruptVal, intr.Value)

	meta = PregelStepMetadata{
		NodeID:         testEmptyString,
		InterruptValue: interruptVal,
	}
	b, err = json.Marshal(meta)
	require.NoError(t, err)
	e = event.New(
		invocationID,
		author,
		event.WithObject(ObjectTypeGraphPregelStep),
		event.WithStateDelta(map[string][]byte{
			MetadataKeyPregel: b,
		}),
	)
	_, ok = extractPregelInterrupt(e)
	require.False(t, ok)

	meta = PregelStepMetadata{NodeID: nodeID}
	b, err = json.Marshal(meta)
	require.NoError(t, err)
	e = event.New(
		invocationID,
		author,
		event.WithObject(ObjectTypeGraphPregelStep),
		event.WithStateDelta(map[string][]byte{
			MetadataKeyPregel: b,
		}),
	)
	_, ok = extractPregelInterrupt(e)
	require.False(t, ok)
}

func TestLatestInterruptedCheckpointID(t *testing.T) {
	ctx := context.Background()

	targetAgent := &inspectAgent{name: "no_exec"}
	id, err := latestInterruptedCheckpointID(ctx, targetAgent, "ln", "ns")
	require.NoError(t, err)
	require.Equal(t, testEmptyString, id)

	targetAgent2 := &latestCheckpointAgent{name: "nil_exec"}
	id, err = latestInterruptedCheckpointID(ctx, targetAgent2, "ln", "ns")
	require.NoError(t, err)
	require.Equal(t, testEmptyString, id)

	execWithNilCM := &Executor{
		checkpointManager: nil,
	}
	targetAgent3 := &latestCheckpointAgent{
		name: "nil_cm",
		exec: execWithNilCM,
	}
	id, err = latestInterruptedCheckpointID(ctx, targetAgent3, "ln", "ns")
	require.NoError(t, err)
	require.Equal(t, testEmptyString, id)

	execWithBrokenCM := &Executor{
		checkpointManager: &CheckpointManager{},
	}
	targetAgent4 := &latestCheckpointAgent{
		name: "err_latest",
		exec: execWithBrokenCM,
	}
	_, err = latestInterruptedCheckpointID(ctx, targetAgent4, "ln", "ns")
	require.Error(t, err)

	saver := newSubgraphTestSaver()
	graphSchema := NewStateSchema()
	g, err := NewStateGraph(graphSchema).
		AddNode("n", func(_ context.Context, s State) (any, error) {
			return s, nil
		}).
		SetEntryPoint("n").
		SetFinishPoint("n").
		Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)
	targetAgent5 := &latestCheckpointAgent{
		name: "with_saver",
		exec: exec,
	}

	id, err = latestInterruptedCheckpointID(ctx, targetAgent5, "ln", "ns")
	require.NoError(t, err)
	require.Equal(t, testEmptyString, id)

	cfg := CreateCheckpointConfig("ln", "ck1", "ns")
	_, err = saver.Put(ctx, PutRequest{
		Config: cfg,
		Checkpoint: &Checkpoint{
			ID:            "ck1",
			ChannelValues: map[string]any{},
		},
		Metadata: NewCheckpointMetadata("test", 0),
	})
	require.NoError(t, err)
	id, err = latestInterruptedCheckpointID(ctx, targetAgent5, "ln", "ns")
	require.NoError(t, err)
	require.Equal(t, testEmptyString, id)

	cfg = CreateCheckpointConfig("ln", "ck2", "ns")
	_, err = saver.Put(ctx, PutRequest{
		Config: cfg,
		Checkpoint: &Checkpoint{
			ID: "ck2",
			InterruptState: &InterruptState{
				NodeID: "n",
			},
		},
		Metadata: NewCheckpointMetadata("test", 1),
	})
	require.NoError(t, err)
	id, err = latestInterruptedCheckpointID(ctx, targetAgent5, "ln", "ns")
	require.NoError(t, err)
	require.Equal(t, "ck2", id)
}
