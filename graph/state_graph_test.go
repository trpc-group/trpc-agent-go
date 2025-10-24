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

	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
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
