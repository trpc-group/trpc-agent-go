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
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// TestDocumentProcessingWorkflow tests a comprehensive document processing workflow
// that mimics real-world usage with LLM nodes, tool nodes, and conditional routing.
func TestDocumentProcessingWorkflow(t *testing.T) {
	// Create mock model for testing
	mockModel := &MockModel{
		responses: map[string]string{
			"analyze":   "complex",
			"summarize": "This is a comprehensive summary of the document.",
			"enhance":   "This is enhanced content with improved clarity.",
		},
	}

	// Create analysis tool
	complexityTool := function.NewFunctionTool(
		func(ctx context.Context, args map[string]any) (map[string]any, error) {
			text := args["text"].(string)
			wordCount := len(strings.Fields(text))
			var level string
			if wordCount > 100 {
				level = "complex"
			} else {
				level = "simple"
			}
			return map[string]any{
				"level":      level,
				"word_count": wordCount,
			}, nil
		},
		function.WithName("analyze_complexity"),
		function.WithDescription("Analyzes document complexity level"),
	)

	// Create state schema
	schema := MessagesStateSchema()

	// Build workflow
	stateGraph := NewStateGraph(schema)
	tools := map[string]tool.Tool{
		"analyze_complexity": complexityTool,
	}

	stateGraph.
		AddNode("preprocess", func(ctx context.Context, state State) (any, error) {
			input := state[StateKeyUserInput].(string)
			wordCount := len(strings.Fields(input))
			return State{
				"word_count":      wordCount,
				"document_length": len(input),
				StateKeyUserInput: input,
			}, nil
		}).
		AddLLMNode("analyze", mockModel,
			`Analyze the document and respond with only the complexity level: "simple" or "complex"`,
			tools).
		AddToolsNode("tools", tools).
		AddNode("route_complexity", func(ctx context.Context, state State) (any, error) {
			return State{"routing_complete": true}, nil
		}).
		AddLLMNode("summarize", mockModel,
			`Create a comprehensive summary of the document.`,
			map[string]tool.Tool{}).
		AddLLMNode("enhance", mockModel,
			`Enhance the content for better clarity and readability.`,
			map[string]tool.Tool{}).
		AddNode("format_output", func(ctx context.Context, state State) (any, error) {
			content := state[StateKeyLastResponse].(string)
			return State{
				StateKeyLastResponse: "FINAL OUTPUT: " + content,
			}, nil
		}).
		SetEntryPoint("preprocess").
		SetFinishPoint("format_output")

	// Add edges
	stateGraph.AddEdge("preprocess", "analyze")
	stateGraph.AddToolsConditionalEdges("analyze", "tools", "route_complexity")
	stateGraph.AddEdge("tools", "analyze")
	stateGraph.AddConditionalEdges("route_complexity", func(ctx context.Context, state State) (string, error) {
		if lastResponse, ok := state[StateKeyLastResponse].(string); ok {
			if strings.Contains(strings.ToLower(lastResponse), "complex") {
				return "complex", nil
			}
		}
		return "simple", nil
	}, map[string]string{
		"simple":  "enhance",
		"complex": "summarize",
	})
	stateGraph.AddEdge("enhance", "format_output")
	stateGraph.AddEdge("summarize", "format_output")

	// Compile graph
	graph, err := stateGraph.Compile()
	require.NoError(t, err, "Failed to compile graph")

	// Create executor
	executor, err := NewExecutor(graph)
	require.NoError(t, err, "Failed to create executor")

	// Test with complex document
	t.Run("Complex_Document", func(t *testing.T) {
		complexDoc := strings.Repeat("This is a complex document with many words. ", 50)
		initialState := State{StateKeyUserInput: complexDoc}
		invocation := &agent.Invocation{InvocationID: "test-complex-doc"}

		eventChan, err := executor.Execute(context.Background(), initialState, invocation)
		require.NoError(t, err, "Failed to execute graph")

		var finalState State
		var allEvents []string
		for event := range eventChan {
			// Log all events for debugging
			if event.StateDelta != nil {
				allEvents = append(allEvents, fmt.Sprintf("Event with %d keys", len(event.StateDelta)))
			}

			if event.StateDelta != nil {
				if finalState == nil {
					finalState = make(State)
				}
				for key, valueBytes := range event.StateDelta {
					if key == MetadataKeyNode || key == MetadataKeyPregel ||
						key == MetadataKeyChannel || key == MetadataKeyState ||
						key == MetadataKeyCompletion {
						continue
					}
					var value any
					if err := json.Unmarshal(valueBytes, &value); err == nil {
						finalState[key] = value
					}
				}
			}
			if event.Done {
				break
			}
		}

		// Verify results
		require.NotNil(t, finalState, "No final state received")

		result, ok := finalState[StateKeyLastResponse].(string)
		require.True(t, ok, "Expected final response")
		assert.Contains(t, result, "FINAL OUTPUT:", "Expected formatted output")

		wordCount, ok := finalState["word_count"].(float64)
		require.True(t, ok, "Expected word count")
		assert.GreaterOrEqual(t, wordCount, float64(100), "Expected high word count")
	})

	// Test with simple document
	t.Run("Simple_Document", func(t *testing.T) {
		simpleDoc := "This is a simple document."
		initialState := State{StateKeyUserInput: simpleDoc}
		invocation := &agent.Invocation{InvocationID: "test-simple-doc"}

		eventChan, err := executor.Execute(context.Background(), initialState, invocation)
		require.NoError(t, err, "Failed to execute graph")

		var finalState State
		for event := range eventChan {
			if event.StateDelta != nil {
				if finalState == nil {
					finalState = make(State)
				}
				for key, valueBytes := range event.StateDelta {
					if key == MetadataKeyNode || key == MetadataKeyPregel ||
						key == MetadataKeyChannel || key == MetadataKeyState ||
						key == MetadataKeyCompletion {
						continue
					}
					var value any
					if err := json.Unmarshal(valueBytes, &value); err == nil {
						finalState[key] = value
					}
				}
			}
			if event.Done {
				break
			}
		}

		// Verify results
		require.NotNil(t, finalState, "No final state received")

		result, ok := finalState[StateKeyLastResponse].(string)
		require.True(t, ok, "Expected final response")
		assert.Contains(t, result, "FINAL OUTPUT:", "Expected formatted output")

		wordCount, ok := finalState["word_count"].(float64)
		require.True(t, ok, "Expected word count")
		assert.LessOrEqual(t, wordCount, float64(50), "Expected low word count")
	})
}

// Ensure handleInterrupt still emits the interrupt event even when
// the provided context is canceled, because it uses a fresh background
// context with a timeout for emission.
func TestHandleInterrupt_EmitsEvent_WithCanceledContext(t *testing.T) {
	exec := &Executor{}
	ch := make(chan *event.Event, 1)
	inv := &agent.Invocation{InvocationID: "inv-int"}
	execCtx := &ExecutionContext{InvocationID: "inv-int", EventChan: ch}

	intr := &InterruptError{NodeID: "N1", Value: "ask"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := exec.handleInterrupt(ctx, inv, execCtx, intr, 7, nil)
	require.True(t, IsInterruptError(err))
	// Same interrupt should be propagated.
	require.Same(t, intr, err)

	// We should still receive the interrupt event.
	select {
	case e := <-ch:
		require.NotNil(t, e)
		if e.StateDelta != nil {
			if b, ok := e.StateDelta[MetadataKeyPregel]; ok {
				var meta PregelStepMetadata
				if json.Unmarshal(b, &meta) == nil {
					require.Equal(t, "N1", meta.NodeID)
					require.Equal(t, "ask", meta.InterruptValue)
				}
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected interrupt event emission")
	}
}

// Cover empty WithDefaultRetryPolicy branch ensuring no defaults are set.
func TestExecutor_WithDefaultRetryPolicy_EmptyCoversNoop(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("n", func(ctx context.Context, s State) (any, error) { return s, nil })
	sg.SetEntryPoint("n")
	g, err := sg.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	exec, err := NewExecutor(g, WithDefaultRetryPolicy())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	if len(exec.defaultRetry) != 0 {
		t.Fatalf("expected no default policies")
	}
}

// MockModel implements a mock model for testing
type MockModel struct {
	responses map[string]string
}

func (m *MockModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	// Create a channel for streaming response
	responseChan := make(chan *model.Response, 1)

	// Determine response based on the last message content
	var response string
	if len(request.Messages) > 0 {
		lastMessage := request.Messages[len(request.Messages)-1]
		content := lastMessage.Content

		if strings.Contains(content, "analyze") {
			response = m.responses["analyze"]
		} else if strings.Contains(content, "summarize") {
			response = m.responses["summarize"]
		} else if strings.Contains(content, "enhance") {
			response = m.responses["enhance"]
		} else {
			response = "Default response"
		}
	} else {
		response = "Default response"
	}

	// Send response and close channel
	go func() {
		defer close(responseChan)
		responseChan <- &model.Response{
			Choices: []model.Choice{
				{
					Message: model.NewAssistantMessage(response),
				},
			},
		}
	}()

	return responseChan, nil
}

func (m *MockModel) Info() model.Info {
	return model.Info{
		Name: "mock-model",
	}
}

// TestBasicLinearWorkflow tests a simple linear workflow with state updates
func TestBasicLinearWorkflow(t *testing.T) {
	// Create state schema for a simple counter workflow
	schema := NewStateSchema().
		AddField("counter", StateField{
			Type:    reflect.TypeOf(0),
			Reducer: DefaultReducer,
		}).
		AddField("messages", StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: StringSliceReducer,
		})

	// Define node functions
	incrementNode := func(ctx context.Context, state State) (any, error) {
		counter := state["counter"].(int)
		messages := state["messages"].([]string)
		return State{
			"counter":  counter + 1,
			"messages": append(messages, "Incremented counter"),
		}, nil
	}

	formatNode := func(ctx context.Context, state State) (any, error) {
		counter := state["counter"].(int)
		messages := state["messages"].([]string)
		return State{
			"result": fmt.Sprintf("Final counter: %d, Steps: %d", counter, len(messages)),
		}, nil
	}

	// Build workflow
	stateGraph := NewStateGraph(schema)
	stateGraph.
		AddNode("increment", incrementNode).
		AddNode("format", formatNode).
		SetEntryPoint("increment").
		AddEdge("increment", "format").
		SetFinishPoint("format")

	// Compile graph
	graph, err := stateGraph.Compile()
	require.NoError(t, err, "Failed to compile graph")

	// Create executor
	executor, err := NewExecutor(graph)
	require.NoError(t, err, "Failed to create executor")

	// Execute with initial state
	initialState := State{
		"counter":  0,
		"messages": []string{},
	}

	invocation := &agent.Invocation{
		InvocationID: "test-basic-workflow",
	}

	eventChan, err := executor.Execute(context.Background(), initialState, invocation)
	require.NoError(t, err, "Failed to execute graph")

	// Process events and track state
	var finalState State
	for event := range eventChan {
		if event.Error != nil {
			t.Errorf("Execution error: %v", event.Error)
		}

		if event.Done {
			// Extract final state from completion event
			if event.StateDelta != nil {
				finalState = make(State)
				for key, valueBytes := range event.StateDelta {
					// Skip metadata keys
					if key == MetadataKeyNode || key == MetadataKeyPregel ||
						key == MetadataKeyChannel || key == MetadataKeyState ||
						key == MetadataKeyCompletion {
						continue
					}
					// Unmarshal the value
					var value any
					if err := json.Unmarshal(valueBytes, &value); err == nil {
						finalState[key] = value
					}
				}
			}
			break
		}
	}

	// Verify results
	require.NotNil(t, finalState, "No final state received")

	result, ok := finalState["result"].(string)
	require.True(t, ok, "Expected result field in final state")
	assert.Equal(t, "Final counter: 1, Steps: 1", result, "Expected specific result")
}

// TestBasicConditionalRouting tests conditional edge routing based on state
func TestBasicConditionalRouting(t *testing.T) {
	// Create state schema
	schema := NewStateSchema().
		AddField("input", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField("result", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		}).
		AddField("path", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	// Define conditional function
	routeCondition := func(ctx context.Context, state State) (string, error) {
		input := state["input"].(string)
		if len(input) > 10 {
			return "long", nil
		}
		return "short", nil
	}

	// Define processing nodes
	longProcessor := func(ctx context.Context, state State) (any, error) {
		return State{
			"result": "Processed as long text",
			"path":   "long",
		}, nil
	}

	shortProcessor := func(ctx context.Context, state State) (any, error) {
		return State{
			"result": "Processed as short text",
			"path":   "short",
		}, nil
	}

	// Build workflow
	stateGraph := NewStateGraph(schema)
	stateGraph.
		AddNode("route", func(ctx context.Context, state State) (any, error) {
			return state, nil // Pass through
		}).
		AddNode("long", longProcessor).
		AddNode("short", shortProcessor).
		SetEntryPoint("route").
		AddConditionalEdges("route", routeCondition, map[string]string{
			"long":  "long",
			"short": "short",
		}).
		SetFinishPoint("long").
		SetFinishPoint("short")

	// Compile graph
	graph, err := stateGraph.Compile()
	require.NoError(t, err, "Failed to compile graph")

	// Test short path
	t.Run("Short Path", func(t *testing.T) {
		executor, err := NewExecutor(graph)
		require.NoError(t, err, "Failed to create executor")

		initialState := State{"input": "hi"}
		invocation := &agent.Invocation{InvocationID: "test-short"}

		eventChan, err := executor.Execute(context.Background(), initialState, invocation)
		require.NoError(t, err, "Failed to execute graph")

		var finalState State
		for event := range eventChan {
			if event.Error != nil {
				t.Errorf("Execution error: %v", event.Error)
			}
			if event.Done {
				// Extract final state from completion event
				if event.StateDelta != nil {
					finalState = make(State)
					for key, valueBytes := range event.StateDelta {
						// Skip metadata keys
						if key == MetadataKeyNode || key == MetadataKeyPregel ||
							key == MetadataKeyChannel || key == MetadataKeyState ||
							key == MetadataKeyCompletion {
							continue
						}
						// Unmarshal the value
						var value any
						if err := json.Unmarshal(valueBytes, &value); err == nil {
							finalState[key] = value
						}
					}
				}
				break
			}
		}

		require.NotNil(t, finalState, "No final state received")

		result, ok := finalState["result"].(string)
		require.True(t, ok, "Expected result field")
		assert.Equal(t, "Processed as short text", result, "Expected short processing result")

		path, ok := finalState["path"].(string)
		require.True(t, ok, "Expected path field")
		assert.Equal(t, "short", path, "Expected short path")
	})

	// Test long path
	t.Run("Long Path", func(t *testing.T) {
		executor, err := NewExecutor(graph)
		require.NoError(t, err, "Failed to create executor")

		initialState := State{"input": "this is a very long input text"}
		invocation := &agent.Invocation{InvocationID: "test-long"}

		eventChan, err := executor.Execute(context.Background(), initialState, invocation)
		require.NoError(t, err, "Failed to execute graph")

		var finalState State
		for event := range eventChan {
			if event.Error != nil {
				t.Errorf("Execution error: %v", event.Error)
			}
			if event.Done {
				// Extract final state from completion event
				if event.StateDelta != nil {
					finalState = make(State)
					for key, valueBytes := range event.StateDelta {
						// Skip metadata keys
						if key == MetadataKeyNode || key == MetadataKeyPregel ||
							key == MetadataKeyChannel || key == MetadataKeyState ||
							key == MetadataKeyCompletion {
							continue
						}
						// Unmarshal the value
						var value any
						if err := json.Unmarshal(valueBytes, &value); err == nil {
							finalState[key] = value
						}
					}
				}
				break
			}
		}

		require.NotNil(t, finalState, "No final state received")

		result, ok := finalState["result"].(string)
		require.True(t, ok, "Expected result field")
		assert.Equal(t, "Processed as long text", result, "Expected long processing result")

		path, ok := finalState["path"].(string)
		require.True(t, ok, "Expected path field")
		assert.Equal(t, "long", path, "Expected long path")
	})
}

// Ensure updateStateFromResult drops internal keys like exec_context from
// user-provided State updates to avoid leaking executor-owned data.
func TestExecutor_UpdateStateFromResult_SkipsInternal(t *testing.T) {
	exec := &Executor{}
	execCtx := &ExecutionContext{State: make(State)}

	// Update contains an internal key and a regular key.
	update := State{
		StateKeyExecContext: &ExecutionContext{InvocationID: "x"},
		"foo":               "bar",
	}

	exec.updateStateFromResult(execCtx, update)

	// Internal key must be dropped; regular key must be applied.
	require.NotContains(t, execCtx.State, StateKeyExecContext)
	require.Equal(t, "bar", execCtx.State["foo"])
}

// TestBasicCommandRouting tests routing using Command objects
func TestBasicCommandRouting(t *testing.T) {
	// Create state schema
	schema := NewStateSchema().
		AddField("counter", StateField{
			Type:    reflect.TypeOf(0),
			Reducer: DefaultReducer,
		}).
		AddField("result", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	// Define nodes with Command routing
	decisionNode := func(ctx context.Context, state State) (any, error) {
		counter := state["counter"].(int)
		newCounter := counter + 1
		if newCounter < 3 {
			return &Command{
				Update: State{"counter": newCounter},
				GoTo:   "decision",
			}, nil
		}
		return &Command{
			Update: State{"counter": newCounter},
			GoTo:   "finish",
		}, nil
	}

	finishNode := func(ctx context.Context, state State) (any, error) {
		// Preserve existing state and update result
		result := make(State)
		// Copy existing state first
		for key, value := range state {
			result[key] = value
		}
		// Then set the result (this will override any existing result)
		result["result"] = "Workflow completed"
		return result, nil
	}

	// Build workflow
	stateGraph := NewStateGraph(schema)
	stateGraph.
		AddNode("decision", decisionNode).
		AddNode("finish", finishNode).
		SetEntryPoint("decision").
		SetFinishPoint("finish")

	// Compile graph
	graph, err := stateGraph.Compile()
	require.NoError(t, err, "Failed to compile graph")

	// Create executor
	executor, err := NewExecutor(graph)
	require.NoError(t, err, "Failed to create executor")

	// Execute
	initialState := State{"counter": 0}
	invocation := &agent.Invocation{InvocationID: "test-command"}

	eventChan, err := executor.Execute(context.Background(), initialState, invocation)
	require.NoError(t, err, "Failed to execute graph")

	// Process events and capture final state
	var finalState State
	for event := range eventChan {
		if event.Error != nil {
			t.Errorf("Execution error: %v", event.Error)
		}

		// Capture state from completion event
		if event.Done && event.StateDelta != nil {
			finalState = make(State)
			for key, valueBytes := range event.StateDelta {
				// Skip metadata keys
				if key == MetadataKeyNode || key == MetadataKeyPregel ||
					key == MetadataKeyChannel || key == MetadataKeyState ||
					key == MetadataKeyCompletion {
					continue
				}
				// Unmarshal the value
				var value any
				if err := json.Unmarshal(valueBytes, &value); err == nil {
					finalState[key] = value
				}
			}

		}

		if event.Done {
			break
		}
	}

	// Verify results
	require.NotNil(t, finalState, "No final state received")

	result, ok := finalState["result"].(string)
	require.True(t, ok, "Expected result field")
	assert.Equal(t, "Workflow completed", result, "Expected completion message")

	// Handle both int and float64 (JSON unmarshaling can produce either)
	var counter int
	switch v := finalState["counter"].(type) {
	case int:
		counter = v
	case float64:
		counter = int(v)
	default:
		t.Errorf("Expected counter field to be int or float64, got %T: %v", finalState["counter"], finalState["counter"])
		return
	}

	assert.Equal(t, 3, counter, "Expected counter to be 3")
}

// TestSimpleCommandRouting tests basic command routing functionality
func TestSimpleCommandRouting(t *testing.T) {
	// Create state schema
	schema := NewStateSchema().
		AddField("step", StateField{
			Type:    reflect.TypeOf(0),
			Reducer: DefaultReducer,
		}).
		AddField("result", StateField{
			Type:    reflect.TypeOf(""),
			Reducer: DefaultReducer,
		})

	// Define a simple command routing node
	commandNode := func(ctx context.Context, state State) (any, error) {
		step := state["step"].(int)
		if step < 2 {
			return &Command{
				Update: State{"step": step + 1},
				GoTo:   "command",
			}, nil
		}
		return State{"result": "completed"}, nil
	}

	// Build workflow
	stateGraph := NewStateGraph(schema)
	stateGraph.
		AddNode("command", commandNode).
		SetEntryPoint("command").
		SetFinishPoint("command")

	// Compile graph
	graph, err := stateGraph.Compile()
	require.NoError(t, err, "Failed to compile graph")

	// Create executor
	executor, err := NewExecutor(graph)
	require.NoError(t, err, "Failed to create executor")

	// Execute
	initialState := State{"step": 0}
	invocation := &agent.Invocation{InvocationID: "test-command-simple"}

	eventChan, err := executor.Execute(context.Background(), initialState, invocation)
	require.NoError(t, err, "Failed to execute graph")

	// Process events
	var finalState State
	for event := range eventChan {
		if event.Error != nil {
			t.Errorf("Execution error: %v", event.Error)
		}
		if event.Done {
			// Extract final state from completion event
			if event.StateDelta != nil {
				finalState = make(State)
				for key, valueBytes := range event.StateDelta {
					// Skip metadata keys
					if key == MetadataKeyNode || key == MetadataKeyPregel ||
						key == MetadataKeyChannel || key == MetadataKeyState ||
						key == MetadataKeyCompletion {
						continue
					}
					// Unmarshal the value
					var value any
					if err := json.Unmarshal(valueBytes, &value); err == nil {
						finalState[key] = value
					}
				}
			}
			break
		}
	}

	// Verify results
	require.NotNil(t, finalState, "No final state received")

	result, ok := finalState["result"].(string)
	require.True(t, ok, "Expected result field")
	assert.Equal(t, "completed", result, "Expected completion result")

	step, ok := finalState["step"].(float64) // JSON unmarshals numbers as float64
	require.True(t, ok, "Expected step field")
	assert.Equal(t, float64(2), step, "Expected step to be 2")
}

// TestCustomerSupportIssueClassification tests a simple customer support workflow
// that classifies issues and routes them appropriately.
func TestCustomerSupportIssueClassification(t *testing.T) {
	// Create mock model
	mockModel := &IssueClassificationMockModel{
		responses: map[string]string{
			"technical": "technical",
			"billing":   "billing",
			"general":   "general",
		},
	}

	// Build workflow
	stateGraph := NewStateGraph(MessagesStateSchema())

	stateGraph.
		AddNode("classify_issue", func(ctx context.Context, state State) (any, error) {
			input := state[StateKeyUserInput].(string)
			return State{
				"issue_type":      "unknown",
				StateKeyUserInput: input,
			}, nil
		}).
		AddLLMNode("classify", mockModel,
			`Classify the customer issue and respond with only the type: "technical", "billing", "general"`,
			map[string]tool.Tool{}).
		AddNode("route_technical", func(ctx context.Context, state State) (any, error) {
			return State{
				"routed_to": "technical_support",
				"priority":  "high",
			}, nil
		}).
		AddNode("route_billing", func(ctx context.Context, state State) (any, error) {
			return State{
				"routed_to": "billing_support",
				"priority":  "medium",
			}, nil
		}).
		AddNode("route_general", func(ctx context.Context, state State) (any, error) {
			return State{
				"routed_to": "general_support",
				"priority":  "low",
			}, nil
		}).
		AddNode("finalize", func(ctx context.Context, state State) (any, error) {
			routedTo := state["routed_to"].(string)
			priority := state["priority"].(string)
			return State{
				StateKeyLastResponse: "ISSUE ROUTED: " + routedTo + " (Priority: " + priority + ")",
			}, nil
		}).
		SetEntryPoint("classify_issue").
		SetFinishPoint("finalize")

	// Add edges
	stateGraph.AddEdge("classify_issue", "classify")
	stateGraph.AddConditionalEdges("classify", func(ctx context.Context, state State) (string, error) {
		if lastResponse, ok := state[StateKeyLastResponse].(string); ok {
			response := strings.ToLower(strings.TrimSpace(lastResponse))
			return response, nil
		}
		return "general", nil
	}, map[string]string{
		"technical": "route_technical",
		"billing":   "route_billing",
		"general":   "route_general",
	})
	stateGraph.AddEdge("route_technical", "finalize")
	stateGraph.AddEdge("route_billing", "finalize")
	stateGraph.AddEdge("route_general", "finalize")

	// Compile graph
	graph, err := stateGraph.Compile()
	require.NoError(t, err, "Failed to compile graph")

	// Create executor
	executor, err := NewExecutor(graph)
	require.NoError(t, err, "Failed to create executor")

	// Test scenarios
	testCases := []struct {
		name          string
		input         string
		expectedRoute string
	}{
		{
			name:          "Technical_Issue",
			input:         "I can't log into my account",
			expectedRoute: "technical_support",
		},
		{
			name:          "Billing_Issue",
			input:         "I was charged twice for my subscription",
			expectedRoute: "billing_support",
		},
		{
			name:          "General_Question",
			input:         "What are your business hours?",
			expectedRoute: "general_support",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			initialState := State{StateKeyUserInput: tc.input}
			invocation := &agent.Invocation{InvocationID: "test-" + strings.ToLower(tc.name)}

			eventChan, err := executor.Execute(context.Background(), initialState, invocation)
			require.NoError(t, err, "Failed to execute graph")

			var finalState State
			for event := range eventChan {
				if event.StateDelta != nil {
					if finalState == nil {
						finalState = make(State)
					}
					for key, valueBytes := range event.StateDelta {
						if key == MetadataKeyNode || key == MetadataKeyPregel ||
							key == MetadataKeyChannel || key == MetadataKeyState ||
							key == MetadataKeyCompletion {
							continue
						}
						var value any
						if err := json.Unmarshal(valueBytes, &value); err == nil {
							finalState[key] = value
						}
					}
				}
				if event.Done {
					break
				}
			}

			// Verify results
			require.NotNil(t, finalState, "No final state received")

			routedTo, ok := finalState["routed_to"].(string)
			require.True(t, ok, "Expected routed_to field")
			assert.Equal(t, tc.expectedRoute, routedTo, "Expected correct route")

			result, ok := finalState[StateKeyLastResponse].(string)
			require.True(t, ok, "Expected final response")
			assert.Contains(t, result, "ISSUE ROUTED:", "Expected routing message")
		})
	}
}

// IssueClassificationMockModel implements a mock model for issue classification testing
type IssueClassificationMockModel struct {
	responses map[string]string
}

func (m *IssueClassificationMockModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	responseChan := make(chan *model.Response, 1)

	var response string
	if len(request.Messages) > 0 {
		lastMessage := request.Messages[len(request.Messages)-1]
		content := lastMessage.Content

		if strings.Contains(content, "log") || strings.Contains(content, "password") {
			response = m.responses["technical"]
		} else if strings.Contains(content, "charged") || strings.Contains(content, "billing") {
			response = m.responses["billing"]
		} else {
			response = m.responses["general"]
		}
	} else {
		response = m.responses["general"]
	}

	go func() {
		defer close(responseChan)
		responseChan <- &model.Response{
			Choices: []model.Choice{
				{
					Message: model.NewAssistantMessage(response),
				},
			},
		}
	}()

	return responseChan, nil
}

func (m *IssueClassificationMockModel) Info() model.Info {
	return model.Info{
		Name: "issue-classification-mock-model",
	}
}

// TestBeforeCallbackShortCircuit ensures that a BeforeNode callback returning a
// custom result short-circuits node execution and that the custom result is
// applied to state.
func TestBeforeCallbackShortCircuit(t *testing.T) {
	schema := NewStateSchema().
		AddField("result", StateField{Type: reflect.TypeOf(""), Reducer: DefaultReducer})
	g := NewStateGraph(schema)

	// This node would error if executed; the callback should short-circuit it.
	g.AddNode("danger", func(ctx context.Context, s State) (any, error) {
		return nil, fmt.Errorf("should not run")
	}).SetEntryPoint("danger").SetFinishPoint("danger")

	// Global callbacks: Before returns a custom State to short-circuit.
	cbs := NewNodeCallbacks()
	cbs.RegisterBeforeNode(func(ctx context.Context, cb *NodeCallbackContext, st State) (any, error) {
		if cb.NodeID == "danger" {
			return State{"result": "ok"}, nil
		}
		return nil, nil
	})
	g.WithNodeCallbacks(cbs)

	compiled, err := g.Compile()
	require.NoError(t, err, "compile failed.")
	exec, err := NewExecutor(compiled)
	require.NoError(t, err, "executor failed.")

	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-short"})
	require.NoError(t, err, "execute failed.")

	var final State
	for ev := range ch {
		if ev.Done && ev.StateDelta != nil {
			final = make(State)
			for k, vb := range ev.StateDelta {
				if k == MetadataKeyNode || k == MetadataKeyPregel || k == MetadataKeyChannel || k == MetadataKeyState || k == MetadataKeyCompletion {
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					final[k] = v
				}
			}
		}
	}

	require.NotNil(t, final, "no final state.")
	res, ok := final["result"].(string)
	require.True(t, ok, "expected result field.")
	require.Equal(t, "ok", res, "expected short-circuit result.")
}

// TestCommandGoToRouting ensures Command{GoTo} triggers the target node and
// state Update is applied, without double-triggering via static writes.
func TestCommandGoToRouting(t *testing.T) {
	schema := NewStateSchema().
		AddField("x", StateField{Type: reflect.TypeOf(0), Reducer: DefaultReducer}).
		AddField("routed", StateField{Type: reflect.TypeOf(false), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("start", func(ctx context.Context, s State) (any, error) {
		return &Command{Update: State{"x": 1}, GoTo: "B"}, nil
	})
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) {
		return State{"routed": true}, nil
	})
	sg.SetEntryPoint("start")
	sg.AddEdge("start", "B") // Static edge present; GoTo routing should avoid double.
	sg.SetFinishPoint("B")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-goto"})
	require.NoError(t, err)

	final := make(State)
	for ev := range ch {
		if ev.Done && ev.StateDelta != nil {
			for k, vb := range ev.StateDelta {
				if k == MetadataKeyNode || k == MetadataKeyPregel || k == MetadataKeyChannel || k == MetadataKeyState || k == MetadataKeyCompletion {
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					final[k] = v
				}
			}
		}
	}
	// Expect both x update and routed result.
	if xv, ok := final["x"].(float64); ok {
		require.Equal(t, float64(1), xv)
	} else if xi, ok := final["x"].(int); ok {
		require.Equal(t, 1, xi)
	} else {
		t.Fatalf("expected x numeric, got %T", final["x"])
	}
	rv, ok := final["routed"].(bool)
	require.True(t, ok)
	require.True(t, rv)
}

// TestAfterCallbackOverride ensures AfterNode callback can override node result.
func TestAfterCallbackOverride(t *testing.T) {
	schema := NewStateSchema().
		AddField("result", StateField{Type: reflect.TypeOf(""), Reducer: DefaultReducer})
	g := NewStateGraph(schema)
	g.AddNode("N", func(ctx context.Context, s State) (any, error) { return State{"result": "orig"}, nil })
	g.SetEntryPoint("N").SetFinishPoint("N")

	cbs := NewNodeCallbacks()
	cbs.RegisterAfterNode(func(ctx context.Context, cb *NodeCallbackContext, s State, res any, nodeErr error) (any, error) {
		return State{"result": "override"}, nil
	})
	g.WithNodeCallbacks(cbs)
	compiled, err := g.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(compiled)
	require.NoError(t, err)

	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-after"})
	require.NoError(t, err)
	final := make(State)
	for ev := range ch {
		if ev.Done && ev.StateDelta != nil {
			for k, vb := range ev.StateDelta {
				if k == MetadataKeyNode || k == MetadataKeyPregel || k == MetadataKeyChannel || k == MetadataKeyState || k == MetadataKeyCompletion {
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					final[k] = v
				}
			}
		}
	}
	rv, ok := final["result"].(string)
	require.True(t, ok)
	require.Equal(t, "override", rv)
}

// TestNilResultStillTriggersStaticEdges verifies that a node returning nil
// still triggers its outgoing static edges, so downstream nodes run.
func TestNilResultStillTriggersStaticEdges(t *testing.T) {
	schema := NewStateSchema().
		AddField("a", StateField{Type: reflect.TypeOf(false), Reducer: DefaultReducer}).
		AddField("b", StateField{Type: reflect.TypeOf(false), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("start", func(ctx context.Context, s State) (any, error) {
		// Return nil result intentionally; downstream edges must still trigger.
		return nil, nil
	})
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) {
		return State{"a": true}, nil
	})
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) {
		return State{"b": true}, nil
	})

	sg.SetEntryPoint("start")
	sg.AddEdge("start", "A")
	sg.AddEdge("start", "B")
	sg.SetFinishPoint("A")
	sg.SetFinishPoint("B")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-nil-edges"})
	require.NoError(t, err)

	final := make(State)
	for ev := range ch {
		if ev.Done && ev.StateDelta != nil {
			for k, vb := range ev.StateDelta {
				if k == MetadataKeyNode || k == MetadataKeyPregel || k == MetadataKeyChannel || k == MetadataKeyState || k == MetadataKeyCompletion {
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					final[k] = v
				}
			}
		}
	}

	av, ok := final["a"].(bool)
	require.True(t, ok, "expected key 'a' in final state")
	require.True(t, av, "expected 'a' to be true")

	bv, ok := final["b"].(bool)
	require.True(t, ok, "expected key 'b' in final state")
	require.True(t, bv, "expected 'b' to be true")
}

// TestShouldTriggerNode tests the shouldTriggerNode logic with various scenarios.
func TestShouldTriggerNode(t *testing.T) {
	exec := &Executor{}

	tests := []struct {
		name           string
		nodeID         string
		channelName    string
		currentVersion int64
		lastCheckpoint *Checkpoint
		expected       bool
	}{
		{
			name:           "no_checkpoint_should_trigger",
			nodeID:         "node1",
			channelName:    "channel1",
			currentVersion: 1,
			lastCheckpoint: nil,
			expected:       true,
		},
		{
			name:           "no_versions_seen_should_trigger",
			nodeID:         "node1",
			channelName:    "channel1",
			currentVersion: 1,
			lastCheckpoint: &Checkpoint{VersionsSeen: nil},
			expected:       true,
		},
		{
			name:           "node_never_run_should_trigger",
			nodeID:         "node1",
			channelName:    "channel1",
			currentVersion: 1,
			lastCheckpoint: &Checkpoint{
				VersionsSeen: map[string]map[string]int64{
					"other_node": {"channel1": 1},
				},
			},
			expected: true,
		},
		{
			name:           "node_hasnt_seen_channel_should_trigger",
			nodeID:         "node1",
			channelName:    "channel1",
			currentVersion: 1,
			lastCheckpoint: &Checkpoint{
				VersionsSeen: map[string]map[string]int64{
					"node1": {"other_channel": 1},
				},
			},
			expected: true,
		},
		{
			name:           "newer_version_should_trigger",
			nodeID:         "node1",
			channelName:    "channel1",
			currentVersion: 3,
			lastCheckpoint: &Checkpoint{
				VersionsSeen: map[string]map[string]int64{
					"node1": {"channel1": 2},
				},
			},
			expected: true,
		},
		{
			name:           "same_version_should_not_trigger",
			nodeID:         "node1",
			channelName:    "channel1",
			currentVersion: 2,
			lastCheckpoint: &Checkpoint{
				VersionsSeen: map[string]map[string]int64{
					"node1": {"channel1": 2},
				},
			},
			expected: false,
		},
		{
			name:           "older_version_should_not_trigger",
			nodeID:         "node1",
			channelName:    "channel1",
			currentVersion: 1,
			lastCheckpoint: &Checkpoint{
				VersionsSeen: map[string]map[string]int64{
					"node1": {"channel1": 2},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := exec.shouldTriggerNode(tt.nodeID, tt.channelName, tt.currentVersion, tt.lastCheckpoint)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestFork tests the Fork function with various scenarios.
func TestFork(t *testing.T) {
	// Create a mock checkpoint saver
	saver := &mockCheckpointSaver{
		cfgToTuple: make(map[string]*CheckpointTuple),
	}

	exec := &Executor{checkpointSaver: saver}

	// Test case 1: No checkpoint saver configured
	t.Run("no_checkpoint_saver", func(t *testing.T) {
		execNoSaver := &Executor{checkpointSaver: nil}
		_, err := execNoSaver.Fork(context.Background(), map[string]any{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "checkpoint saver is not configured")
	})

	// Test case 2: Source checkpoint not found
	t.Run("source_checkpoint_not_found", func(t *testing.T) {
		config := map[string]any{"lineage_id": "test", "checkpoint_id": "nonexistent"}
		_, err := exec.Fork(context.Background(), config)
		require.Error(t, err)
		require.Contains(t, err.Error(), "source checkpoint not found")
	})

	// Test case 3: Successful fork
	t.Run("successful_fork", func(t *testing.T) {
		// Create a source checkpoint
		sourceCheckpoint := &Checkpoint{
			ID:              "source-1",
			ChannelVersions: map[string]int64{"channel1": 1},
			NextNodes:       []string{"node1"},
		}
		sourceMetadata := &CheckpointMetadata{
			Step: 5,
		}
		sourceTuple := &CheckpointTuple{
			Checkpoint:    sourceCheckpoint,
			Metadata:      sourceMetadata,
			PendingWrites: []PendingWrite{{TaskID: "task1", Channel: "channel1", Value: []byte("test")}},
		}

		// Mock the saver to return the source checkpoint
		config := map[string]any{
			"configurable": map[string]any{
				"lineage_id":    "test",
				"checkpoint_id": "source-1",
				"namespace":     "ns1",
			},
		}
		saver.cfgToTuple[fmt.Sprintf("%v", config)] = sourceTuple

		// Mock PutFull to return updated config
		saver.putFullFunc = func(ctx context.Context, req PutFullRequest) (map[string]any, error) {
			return map[string]any{
				"configurable": map[string]any{
					"lineage_id":    "test",
					"checkpoint_id": "forked-1",
					"namespace":     "ns1",
				},
			}, nil
		}

		result, err := exec.Fork(context.Background(), config)
		require.NoError(t, err)
		require.NotNil(t, result)
		configurable, ok := result["configurable"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "test", configurable["lineage_id"])
		require.Equal(t, "forked-1", configurable["checkpoint_id"])
		require.Equal(t, "ns1", configurable["namespace"])
	})

	// Test case 4: Fork with no pending writes
	t.Run("fork_no_pending_writes", func(t *testing.T) {
		// Create a source checkpoint with no pending writes
		sourceCheckpoint := &Checkpoint{
			ID:              "source-2",
			ChannelVersions: map[string]int64{"channel1": 1},
			NextNodes:       []string{"node1"},
		}
		sourceMetadata := &CheckpointMetadata{
			Step: 3,
		}
		sourceTuple := &CheckpointTuple{
			Checkpoint:    sourceCheckpoint,
			Metadata:      sourceMetadata,
			PendingWrites: nil, // No pending writes
		}

		config := map[string]any{
			"configurable": map[string]any{
				"lineage_id":    "test2",
				"checkpoint_id": "source-2",
				"namespace":     "ns2",
			},
		}
		saver.cfgToTuple[fmt.Sprintf("%v", config)] = sourceTuple

		saver.putFullFunc = func(ctx context.Context, req PutFullRequest) (map[string]any, error) {
			return map[string]any{
				"configurable": map[string]any{
					"lineage_id":    "test2",
					"checkpoint_id": "forked-2",
					"namespace":     "ns2",
				},
			}, nil
		}

		result, err := exec.Fork(context.Background(), config)
		require.NoError(t, err)
		require.NotNil(t, result)
		configurable, ok := result["configurable"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "test2", configurable["lineage_id"])
		require.Equal(t, "forked-2", configurable["checkpoint_id"])
	})
}

// mockCheckpointSaver is a mock implementation for testing Fork function.
type mockCheckpointSaver struct {
	cfgToTuple  map[string]*CheckpointTuple
	putFullFunc func(ctx context.Context, req PutFullRequest) (map[string]any, error)
}

func (m *mockCheckpointSaver) GetTuple(ctx context.Context, config map[string]any) (*CheckpointTuple, error) {
	key := fmt.Sprintf("%v", config)
	tuple, exists := m.cfgToTuple[key]
	if !exists {
		return nil, nil
	}
	return tuple, nil
}

func (m *mockCheckpointSaver) PutFull(ctx context.Context, req PutFullRequest) (map[string]any, error) {
	if m.putFullFunc != nil {
		return m.putFullFunc(ctx, req)
	}
	return req.Config, nil
}

func (m *mockCheckpointSaver) Get(ctx context.Context, config map[string]any) (*Checkpoint, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) Put(ctx context.Context, req PutRequest) (map[string]any, error) {
	return req.Config, nil
}

func (m *mockCheckpointSaver) List(ctx context.Context, config map[string]any, filter *CheckpointFilter) ([]*CheckpointTuple, error) {
	return nil, nil
}

func (m *mockCheckpointSaver) PutWrites(ctx context.Context, req PutWritesRequest) error {
	return nil
}

func (m *mockCheckpointSaver) DeleteLineage(ctx context.Context, lineageID string) error {
	return nil
}

func (m *mockCheckpointSaver) Close() error {
	return nil
}

// TestParallelFanOutWithCommands verifies that a node returning []*Command
// fan-outs into multiple tasks that execute in parallel with isolated overlays
// and that their results are merged back into the global State via reducers.
func TestParallelFanOutWithCommands(t *testing.T) {
	// Define schema with a results slice using StringSliceReducer for merging.
	schema := NewStateSchema().
		AddField("results", StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: StringSliceReducer,
			Default: func() any { return []string{} },
		})

	// Build graph.
	stateGraph := NewStateGraph(schema)

	// Fan-out node: returns two commands to the same worker with different overlays.
	stateGraph.AddNode("fanout", func(ctx context.Context, state State) (any, error) {
		cmds := []*Command{
			{Update: State{"param": "A"}, GoTo: "worker"},
			{Update: State{"param": "B"}, GoTo: "worker"},
		}
		return cmds, nil
	})

	// Worker node: reads overlay param and appends into results.
	stateGraph.AddNode("worker", func(ctx context.Context, state State) (any, error) {
		p, _ := state["param"].(string)
		if p == "" {
			return State{}, nil
		}
		return State{"results": []string{p}}, nil
	})

	// Entry is fanout; connect fanout -> worker and finish at worker.
	stateGraph.SetEntryPoint("fanout")
	stateGraph.AddEdge("fanout", "worker")
	stateGraph.SetFinishPoint("worker")

	// Compile and execute.
	graph, err := stateGraph.Compile()
	require.NoError(t, err, "Failed to compile graph")

	executor, err := NewExecutor(graph)
	require.NoError(t, err, "Failed to create executor")

	invocation := &agent.Invocation{InvocationID: "test-fanout-commands"}
	eventChan, err := executor.Execute(context.Background(), State{}, invocation)
	require.NoError(t, err, "Failed to execute graph")

	var finalState State
	for event := range eventChan {
		if event.Done && event.StateDelta != nil {
			finalState = make(State)
			for key, valueBytes := range event.StateDelta {
				if key == MetadataKeyNode || key == MetadataKeyPregel ||
					key == MetadataKeyChannel || key == MetadataKeyState ||
					key == MetadataKeyCompletion {
					continue
				}
				var value any
				if err := json.Unmarshal(valueBytes, &value); err == nil {
					finalState[key] = value
				}
			}
		}
		if event.Done {
			break
		}
	}

	// Verify results
	require.NotNil(t, finalState, "No final state received")

	vals, ok := finalState["results"].([]any)
	if !ok {
		// It can also come as []string depending on unmarshalling; handle both.
		if vs, ok2 := finalState["results"].([]string); ok2 {
			assert.Len(t, vs, 2, "Expected 2 results")
			m := map[string]bool{}
			for _, s := range vs {
				m[s] = true
			}
			assert.True(t, m["A"], "Expected result to contain A")
			assert.True(t, m["B"], "Expected result to contain B")
			return
		}
		t.Fatalf("Expected results slice in final state, got %T: %v", finalState["results"], finalState["results"])
	}
	assert.Len(t, vals, 2, "Expected 2 results")
	m := map[string]bool{}
	for _, v := range vals {
		if s, ok := v.(string); ok {
			m[s] = true
		}
	}
	assert.True(t, m["A"], "Expected result to contain A")
	assert.True(t, m["B"], "Expected result to contain B")
}

// TestFanOutWithGlobalStateAccess tests that fan-out branches can access global state.
func TestFanOutWithGlobalStateAccess(t *testing.T) {
	// Define schema with both global and local fields
	schema := NewStateSchema().
		AddField("results", StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: StringSliceReducer,
			Default: func() any { return []string{} },
		})

	// Build graph.
	stateGraph := NewStateGraph(schema)

	// Fan-out node: returns commands with local params but needs global state access.
	stateGraph.AddNode("fanout", func(ctx context.Context, state State) (any, error) {
		cmds := []*Command{
			{Update: State{"local_param": "task1"}, GoTo: "worker"},
			{Update: State{"local_param": "task2"}, GoTo: "worker"},
		}
		return cmds, nil
	})

	// Worker node: should be able to access both global state and local overlay.
	stateGraph.AddNode("worker", func(ctx context.Context, state State) (any, error) {
		// Access local parameter from overlay.
		localParam, _ := state["local_param"].(string)

		// Access global state (should be available).
		globalValue, _ := state["global_value"]

		result := fmt.Sprintf("%s_%v", localParam, globalValue)
		return State{"results": []string{result}}, nil
	})

	// Entry is fanout; connect fanout -> worker and finish at worker.
	stateGraph.SetEntryPoint("fanout")
	stateGraph.AddEdge("fanout", "worker")
	stateGraph.SetFinishPoint("worker")

	// Compile and execute.
	graph, err := stateGraph.Compile()
	require.NoError(t, err, "Failed to compile graph")

	executor, err := NewExecutor(graph)
	require.NoError(t, err, "Failed to create executor")

	// Set initial global state.
	initialState := State{"global_value": "global"}
	invocation := &agent.Invocation{InvocationID: "test-fanout-global-state"}
	eventChan, err := executor.Execute(context.Background(), initialState, invocation)
	require.NoError(t, err, "Failed to execute graph")

	var finalState State
	for event := range eventChan {
		if event.Done && event.StateDelta != nil {
			finalState = make(State)
			for key, valueBytes := range event.StateDelta {
				if key == MetadataKeyNode || key == MetadataKeyPregel ||
					key == MetadataKeyChannel || key == MetadataKeyState ||
					key == MetadataKeyCompletion {
					continue
				}
				var value any
				if err := json.Unmarshal(valueBytes, &value); err == nil {
					finalState[key] = value
				}
			}
		}
		if event.Done {
			break
		}
	}

	// Verify results.
	require.NotNil(t, finalState, "No final state received")

	// Handle both []string and []interface{} types from JSON unmarshalling.
	if vs, ok := finalState["results"].([]string); ok {
		assert.GreaterOrEqual(t, len(vs), 2, "Expected at least 2 results")
		m := map[string]bool{}
		for _, s := range vs {
			m[s] = true
		}
		assert.True(t, m["task1_global"], "Expected result to contain task1_global")
		assert.True(t, m["task2_global"], "Expected result to contain task2_global")
	} else if vals, ok := finalState["results"].([]any); ok {
		assert.GreaterOrEqual(t, len(vals), 2, "Expected at least 2 results")
		m := map[string]bool{}
		for _, v := range vals {
			if s, ok := v.(string); ok {
				m[s] = true
			}
		}
		assert.True(t, m["task1_global"], "Expected result to contain task1_global")
		assert.True(t, m["task2_global"], "Expected result to contain task2_global")
	} else {
		t.Fatalf("Expected results slice in final state, got %T: %v", finalState["results"], finalState["results"])
	}
}

// TestFanOutWithEmptyCommands tests edge case of empty command slice
func TestFanOutWithEmptyCommands(t *testing.T) {
	// Define schema.
	schema := NewStateSchema().
		AddField("results", StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: StringSliceReducer,
			Default: func() any { return []string{} },
		})

	// Build graph.
	stateGraph := NewStateGraph(schema)

	// Fan-out node: returns empty command slice.
	stateGraph.AddNode("fanout", func(ctx context.Context, state State) (any, error) {
		cmds := []*Command{} // Empty slice
		return cmds, nil
	})

	// Worker node: should not be executed.
	stateGraph.AddNode("worker", func(ctx context.Context, state State) (any, error) {
		return State{"results": []string{"should_not_execute"}}, nil
	})

	// Entry is fanout; connect fanout -> worker and finish at worker.
	stateGraph.SetEntryPoint("fanout")
	stateGraph.AddEdge("fanout", "worker")
	stateGraph.SetFinishPoint("worker")

	// Compile and execute.
	graph, err := stateGraph.Compile()
	require.NoError(t, err, "Failed to compile graph")

	executor, err := NewExecutor(graph)
	require.NoError(t, err, "Failed to create executor")

	invocation := &agent.Invocation{InvocationID: "test-fanout-empty-commands"}
	eventChan, err := executor.Execute(context.Background(), State{}, invocation)
	require.NoError(t, err, "Failed to execute graph")

	var finalState State
	for event := range eventChan {
		if event.Done && event.StateDelta != nil {
			finalState = make(State)
			for key, valueBytes := range event.StateDelta {
				if key == MetadataKeyNode || key == MetadataKeyPregel ||
					key == MetadataKeyChannel || key == MetadataKeyState ||
					key == MetadataKeyCompletion {
					continue
				}
				var value any
				if err := json.Unmarshal(valueBytes, &value); err == nil {
					finalState[key] = value
				}
			}
		}
		if event.Done {
			break
		}
	}

	// Verify results - should be empty since no commands were executed.
	require.NotNil(t, finalState, "No final state received")

	// Results should be empty or not present
	if results, exists := finalState["results"]; exists {
		if vs, ok := results.([]string); ok {
			assert.Len(t, vs, 0, "Expected no results for empty commands")
		}
	}
}

// TestFanOutWithNilCommandUpdate tests edge case of nil command update.
func TestFanOutWithNilCommandUpdate(t *testing.T) {
	// Define schema.
	schema := NewStateSchema().
		AddField("results", StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: StringSliceReducer,
			Default: func() any { return []string{} },
		})

	// Build graph.
	stateGraph := NewStateGraph(schema)

	// Fan-out node: returns commands with nil update.
	stateGraph.AddNode("fanout", func(ctx context.Context, state State) (any, error) {
		cmds := []*Command{
			{Update: nil, GoTo: "worker"}, // nil update
			{Update: State{"param": "valid"}, GoTo: "worker"},
		}
		return cmds, nil
	})

	// Worker node: handles both nil and valid updates.
	stateGraph.AddNode("worker", func(ctx context.Context, state State) (any, error) {
		param, _ := state["param"].(string)
		if param == "" {
			param = "nil_update"
		}
		return State{"results": []string{param}}, nil
	})

	// Entry is fanout; connect fanout -> worker and finish at worker.
	stateGraph.SetEntryPoint("fanout")
	stateGraph.AddEdge("fanout", "worker")
	stateGraph.SetFinishPoint("worker")

	// Compile and execute.
	graph, err := stateGraph.Compile()
	require.NoError(t, err, "Failed to compile graph")

	executor, err := NewExecutor(graph)
	require.NoError(t, err, "Failed to create executor")

	invocation := &agent.Invocation{InvocationID: "test-fanout-nil-update"}
	eventChan, err := executor.Execute(context.Background(), State{}, invocation)
	require.NoError(t, err, "Failed to execute graph")

	var finalState State
	for event := range eventChan {
		if event.Done && event.StateDelta != nil {
			finalState = make(State)
			for key, valueBytes := range event.StateDelta {
				if key == MetadataKeyNode || key == MetadataKeyPregel ||
					key == MetadataKeyChannel || key == MetadataKeyState ||
					key == MetadataKeyCompletion {
					continue
				}
				var value any
				if err := json.Unmarshal(valueBytes, &value); err == nil {
					finalState[key] = value
				}
			}
		}
		if event.Done {
			break
		}
	}

	// Verify results.
	require.NotNil(t, finalState, "No final state received")

	// Handle both []string and []interface{} types from JSON unmarshalling
	if vs, ok := finalState["results"].([]string); ok {
		assert.GreaterOrEqual(t, len(vs), 2, "Expected at least 2 results")
		m := map[string]bool{}
		for _, s := range vs {
			m[s] = true
		}
		assert.True(t, m["nil_update"], "Expected result to contain nil_update")
		assert.True(t, m["valid"], "Expected result to contain valid")
	} else if vals, ok := finalState["results"].([]any); ok {
		assert.GreaterOrEqual(t, len(vals), 2, "Expected at least 2 results")
		m := map[string]bool{}
		for _, v := range vals {
			if s, ok := v.(string); ok {
				m[s] = true
			}
		}
		assert.True(t, m["nil_update"], "Expected result to contain nil_update")
		assert.True(t, m["valid"], "Expected result to contain valid")
	} else {
		t.Fatalf("Expected results slice in final state, got %T: %v", finalState["results"], finalState["results"])
	}
}

// TestEmitStateUpdateEventConcurrency ensures no panic when emitting state
// update events while state is being concurrently mutated.
func TestEmitStateUpdateEventConcurrency(t *testing.T) {
	// Build a minimal graph.
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddNode("noop", func(ctx context.Context, s State) (any, error) {
		return State{"ok": true}, nil
	})
	sg.SetEntryPoint("noop")
	sg.SetFinishPoint("noop")
	g, err := sg.Compile()
	require.NoError(t, err, "compile graph failed.")

	exec, err := NewExecutor(g)
	require.NoError(t, err, "create executor failed.")

	// Prepare execution context with nested map to stress JSON encoding.
	evtCh := make(chan *event.Event, 1024)
	execCtx := &ExecutionContext{
		Graph:        g,
		State:        State{"nested": map[string]any{"a": 1}},
		EventChan:    evtCh,
		InvocationID: "test-inv",
	}

	// Concurrently mutate the nested map while emitting events.
	stopCh := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopCh:
				return
			default:
				execCtx.stateMutex.Lock()
				m := execCtx.State["nested"].(map[string]any)
				m[fmt.Sprintf("k%v", time.Now().UnixNano())] = time.Now().Unix()
				execCtx.stateMutex.Unlock()
			}
		}
	}()

	// Rapidly emit state update events; should not panic.
	for i := 0; i < 200; i++ {
		exec.emitStateUpdateEvent(context.Background(), nil, execCtx)
	}
	close(stopCh)
}

// TestProcessConditionalEdgesConcurrency ensures no panic when evaluating
// conditional edges while state is concurrently mutated.
func TestProcessConditionalEdgesConcurrency(t *testing.T) {
	// Build a graph with a conditional edge.
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddNode("start", func(ctx context.Context, s State) (any, error) {
		return State{"x": 1}, nil
	})
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) { return State{"done": true}, nil })
	sg.SetEntryPoint("start")
	sg.SetFinishPoint("A")
	sg.AddConditionalEdges("start", func(ctx context.Context, s State) (string, error) {
		// Read a key that may be mutated concurrently.
		if _, ok := s["flip"].(bool); ok {
			return "A", nil
		}
		return "A", nil
	}, map[string]string{"A": "A"})
	g, err := sg.Compile()
	require.NoError(t, err, "compile graph failed.")

	exec, err := NewExecutor(g)
	require.NoError(t, err, "create executor failed.")

	evtCh := make(chan *event.Event, 1024)
	execCtx := &ExecutionContext{
		Graph:        g,
		State:        State{"flip": false},
		EventChan:    evtCh,
		InvocationID: "test-inv-2",
	}

	// Mutate the state concurrently.
	stopCh := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopCh:
				return
			default:
				execCtx.stateMutex.Lock()
				execCtx.State["flip"] = !execCtx.State["flip"].(bool)
				execCtx.stateMutex.Unlock()
			}
		}
	}()

	// Repeatedly process conditional edges; should not panic.
	for i := 0; i < 200; i++ {
		require.NoError(t, exec.processConditionalEdges(context.Background(), nil, execCtx, "start", i), "conditional processing failed.")
	}
	close(stopCh)
}

// TestProcessConditionalEdges_DedupMulti ensures duplicate branch keys in a
// multi-conditional result do not trigger the same target twice.
func TestProcessConditionalEdges_DedupMulti(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("start", minimalNoopNode)
	sg.AddNode("B", minimalNoopNode)
	sg.AddNode("C", minimalNoopNode)
	sg.SetEntryPoint("start")
	sg.AddMultiConditionalEdges("start", func(ctx context.Context, s State) ([]string, error) {
		return []string{"b", "b", "c"}, nil // duplicate "b"
	}, map[string]string{"b": "B", "c": "C"})
	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)
	execCtx := &ExecutionContext{Graph: g, State: State{}, EventChan: make(chan *event.Event, 16)}

	// Process and verify only one update per target channel.
	require.NoError(t, exec.processConditionalEdges(context.Background(), nil, execCtx, "start", 0))
	chB, _ := g.getChannel("branch:to:B")
	chC, _ := g.getChannel("branch:to:C")
	require.NotNil(t, chB)
	require.NotNil(t, chC)
	require.Equal(t, int64(1), chB.Version)
	require.Equal(t, int64(1), chC.Version)
}

// TestProcessConditionalEdges_Multi_Error covers the error path when the
// MultiCondition function returns an error and ensures the executor
// returns a wrapped error with node information.
func TestProcessConditionalEdges_Multi_Error(t *testing.T) {
	const (
		nodeStart = "start"
		nodeB     = "B"
		msgPrefix = "conditional edge evaluation failed for node "
	)

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode(nodeStart, minimalNoopNode)
	sg.AddNode(nodeB, minimalNoopNode)
	sg.SetEntryPoint(nodeStart)
	// PathMap must reference existing nodes for validation.
	sg.AddMultiConditionalEdges(
		nodeStart,
		func(ctx context.Context, s State) ([]string, error) {
			return nil, fmt.Errorf("mc boom")
		},
		map[string]string{"b": nodeB},
	)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	execCtx := &ExecutionContext{
		Graph:        g,
		State:        State{},
		EventChan:    make(chan *event.Event, 8),
		InvocationID: "inv-multi-err",
	}

	err = exec.processConditionalEdges(
		context.Background(), nil, execCtx, nodeStart, 0,
	)
	require.Error(t, err)
	// Wrapped error should include the node id and original message.
	require.Contains(t, err.Error(), msgPrefix+nodeStart)
	require.Contains(t, err.Error(), "mc boom")
}

// TestProcessConditionalEdges_Condition_Error covers the error path when the
// single Condition function returns an error.
func TestProcessConditionalEdges_Condition_Error(t *testing.T) {
	const (
		nodeStart = "start"
		nodeA     = "A"
		msgPrefix = "conditional edge evaluation failed for node "
	)

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode(nodeStart, minimalNoopNode)
	sg.AddNode(nodeA, minimalNoopNode)
	sg.SetEntryPoint(nodeStart)
	sg.AddConditionalEdges(
		nodeStart,
		func(ctx context.Context, s State) (string, error) {
			return "", fmt.Errorf("sc boom")
		},
		map[string]string{"a": nodeA},
	)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	execCtx := &ExecutionContext{
		Graph:        g,
		State:        State{},
		EventChan:    make(chan *event.Event, 8),
		InvocationID: "inv-cond-err",
	}

	err = exec.processConditionalEdges(
		context.Background(), nil, execCtx, nodeStart, 0,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), msgPrefix+nodeStart)
	require.Contains(t, err.Error(), "sc boom")
}

// TestProcessConditionalEdges_Multi_SkipEmpty verifies that empty strings
// in MultiCondition results are ignored and do not produce channels, and
// duplicates still deduplicate to one trigger.
func TestProcessConditionalEdges_Multi_SkipEmpty(t *testing.T) {
	const (
		nodeStart = "start"
		nodeB     = "B"
		nodeC     = "C"
	)

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode(nodeStart, minimalNoopNode)
	sg.AddNode(nodeB, minimalNoopNode)
	sg.AddNode(nodeC, minimalNoopNode)
	sg.SetEntryPoint(nodeStart)
	sg.AddMultiConditionalEdges(
		nodeStart,
		func(ctx context.Context, s State) ([]string, error) {
			// Includes duplicates and empty keys.
			return []string{"b", "", "b", "c", "", "c"}, nil
		},
		map[string]string{"b": nodeB, "c": nodeC},
	)

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	execCtx := &ExecutionContext{
		Graph:        g,
		State:        State{},
		EventChan:    make(chan *event.Event, 8),
		InvocationID: "inv-multi-skip",
	}

	require.NoError(t, exec.processConditionalEdges(
		context.Background(), nil, execCtx, nodeStart, 0,
	))

	// Expect channels only for B and C, one update each.
	chB, okB := g.getChannel(ChannelBranchPrefix + nodeB)
	chC, okC := g.getChannel(ChannelBranchPrefix + nodeC)
	require.True(t, okB)
	require.True(t, okC)
	require.Equal(t, int64(1), chB.Version)
	require.Equal(t, int64(1), chC.Version)

	// No channel should be created for empty branch key.
	if _, ok := g.getChannel(ChannelBranchPrefix + ""); ok {
		t.Fatalf("unexpected channel for empty branch key created")
	}
}

// minimalNoopNode returns a trivial node function for building test graphs.
func minimalNoopNode(_ context.Context, _ State) (any, error) { return nil, nil }

func TestNewExecutor_InvalidGraph_ReturnsError(t *testing.T) {
	// Graph created without entry point should fail validation in NewExecutor
	g := New(NewStateSchema())
	exec, err := NewExecutor(g)
	require.Nil(t, exec)
	require.Error(t, err)
}

func TestNewExecutor_NodeTimeout_MinimumEnforced(t *testing.T) {
	// Build a minimal valid graph
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("a", minimalNoopNode).SetEntryPoint("a").SetFinishPoint("a")
	g, err := sg.Compile()
	require.NoError(t, err)

	// StepTimeout < 2s -> derived node timeout < 1s -> should clamp to 1s
	exec, err := NewExecutor(g, WithStepTimeout(1500*time.Millisecond))
	require.NoError(t, err)
	require.NotNil(t, exec)
	require.Equal(t, time.Second, exec.nodeTimeout)

	// StepTimeout large -> derived node timeout should be half of step timeout
	exec2, err := NewExecutor(g, WithStepTimeout(5*time.Second))
	require.NoError(t, err)
	require.NotNil(t, exec2)
	require.Equal(t, 2500*time.Millisecond, exec2.nodeTimeout)
}

func TestExecutor_Execute_NilInvocation_Error(t *testing.T) {
	// Build a minimal valid graph
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("a", minimalNoopNode).SetEntryPoint("a").SetFinishPoint("a")
	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	// invocation is nil -> should return error before starting execution
	ch, err := exec.Execute(context.Background(), State{}, nil)
	require.Error(t, err)
	require.Nil(t, ch)
}

func TestDeepCopyAny_Branches(t *testing.T) {
	// []int branch
	ints := []int{1, 2, 3}
	gotIntsAny := deepCopyAny(ints)
	gotInts, ok := gotIntsAny.([]int)
	require.True(t, ok)
	require.Equal(t, ints, gotInts)
	if len(gotInts) > 0 && len(ints) > 0 {
		require.NotSame(t, &ints[0], &gotInts[0])
	}
	gotInts[0] = 99
	require.Equal(t, []int{1, 2, 3}, ints) // original unchanged

	// []float64 branch
	floats := []float64{1.5, 2.5}
	gotFloatsAny := deepCopyAny(floats)
	gotFloats, ok := gotFloatsAny.([]float64)
	require.True(t, ok)
	require.Equal(t, floats, gotFloats)
	if len(gotFloats) > 0 && len(floats) > 0 {
		require.NotSame(t, &floats[0], &gotFloats[0])
	}
	gotFloats[0] = 7.7
	require.Equal(t, []float64{1.5, 2.5}, floats) // original unchanged

	// time.Time branch
	now := time.Now()
	gotTime := deepCopyAny(now)
	// should return the same value
	require.True(t, gotTime.(time.Time).Equal(now))

	// reflect.Map: typed nil map should be returned as typed nil
	var nilMap map[string]int
	gotNilMap := deepCopyAny(nilMap)
	require.Equal(t, reflect.TypeOf(nilMap), reflect.TypeOf(gotNilMap))
	require.True(t, reflect.ValueOf(gotNilMap).IsNil())

	// reflect.Map: non-nil map with non-any value type
	m := map[string]int{"a": 1}
	gotMapAny := deepCopyAny(m)
	gotMap, ok := gotMapAny.(map[string]int)
	require.True(t, ok)
	require.Equal(t, m, gotMap)
	gotMap["a"] = 42
	require.Equal(t, 1, m["a"]) // original unchanged

	// reflect.Slice: typed nil slice should be returned as typed nil
	type pair struct{ A int }
	var nilSlice []pair
	gotNilSlice := deepCopyAny(nilSlice)
	require.Equal(t, reflect.TypeOf(nilSlice), reflect.TypeOf(gotNilSlice))
	require.True(t, reflect.ValueOf(gotNilSlice).IsNil())

	// reflect.Slice: non-nil slice of custom type
	s := []pair{{1}, {2}}
	gotSliceAny := deepCopyAny(s)
	gotSlice, ok := gotSliceAny.([]pair)
	require.True(t, ok)
	require.Equal(t, s, gotSlice)
	if len(gotSlice) > 0 && len(s) > 0 {
		require.NotSame(t, &s[0], &gotSlice[0])
	}
	gotSlice[0].A = 99
	require.Equal(t, []pair{{1}, {2}}, s) // original unchanged
}

// TestExecuteNodeFunction_RecoversFromPanic ensures that panics in user node functions
// are recovered and converted into errors, without crashing the executor.
func TestExecuteNodeFunction_RecoversFromPanic(t *testing.T) {
	// Build graph with a single node that panics
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("boom", func(ctx context.Context, state State) (any, error) {
		panic("kaboom")
	}).SetEntryPoint("boom").SetFinishPoint("boom")
	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	execCtx := &ExecutionContext{
		Graph:        g,
		State:        make(State),
		InvocationID: "panic-test",
	}
	task := &Task{NodeID: "boom", TaskID: "boom-0"}

	res, runErr := exec.executeNodeFunction(context.Background(), execCtx, task)
	require.Error(t, runErr)
	require.Nil(t, res)
	require.Contains(t, runErr.Error(), "kaboom")
	require.Contains(t, runErr.Error(), "node boom panic")
}

// PlanStep should honor StateKeyNextNodes and remove the key after planning.
func TestPlanStep_UsesNextNodesAndDeletesKey(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("A", minimalNoopNode)
	sg.AddNode("B", minimalNoopNode)
	sg.SetEntryPoint("A")
	sg.SetFinishPoint("B")
	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	evCh := make(chan *event.Event, 8)
	ec := &ExecutionContext{
		Graph:        g,
		State:        State{StateKeyNextNodes: []string{"A", "B"}},
		EventChan:    evCh,
		InvocationID: "inv-plan",
	}
	tasks, perr := exec.planStep(context.Background(), nil, ec, 1)
	require.NoError(t, perr)
	require.Len(t, tasks, 2)
	// Key should be removed after consumption.
	require.NotContains(t, ec.State, StateKeyNextNodes)
}

// resolveTargetByEnds should map symbolic names via per-node ends.
func TestResolveTargetByEnds(t *testing.T) {
	g := New(NewStateSchema())
	n := &Node{ID: "X"}
	n.ends = map[string]string{"ok": "B"}
	_ = g.addNode(n)
	exec := &Executor{graph: g}

	// Known mapping
	got := exec.resolveTargetByEnds("X", "ok")
	require.Equal(t, "B", got)
	// Unknown key returns empty
	require.Equal(t, "", exec.resolveTargetByEnds("X", "nope"))
	// Unknown node returns empty
	require.Equal(t, "", exec.resolveTargetByEnds("ZZ", "ok"))
}

func TestExecutor_isDisableDeepCopyKey(t *testing.T) {
	type fields struct {
		graph *Graph
	}
	type args struct {
		key string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "schema is nil",
			fields: fields{
				graph: &Graph{schema: nil},
			},
			args: args{key: "anyKey"},
			want: false,
		},
		{
			name: "key not found in schema",
			fields: fields{
				graph: &Graph{
					schema: &StateSchema{
						Fields: map[string]StateField{},
					},
				},
			},
			args: args{key: "missingKey"},
			want: false,
		},
		{
			name: "key exists with DisableDeepCopy true",
			fields: fields{
				graph: &Graph{
					schema: &StateSchema{
						Fields: map[string]StateField{
							"testKey": {DisableDeepCopy: true},
						},
					},
				},
			},
			args: args{key: "testKey"},
			want: true,
		},
		{
			name: "key exists with DisableDeepCopy false",
			fields: fields{
				graph: &Graph{
					schema: &StateSchema{
						Fields: map[string]StateField{
							"testKey": {DisableDeepCopy: false},
						},
					},
				},
			},
			args: args{key: "testKey"},
			want: false,
		},
		{
			name: "empty key not found",
			fields: fields{
				graph: &Graph{
					schema: &StateSchema{
						Fields: map[string]StateField{},
					},
				},
			},
			args: args{key: ""},
			want: false,
		},
		{
			name: "empty key with DisableDeepCopy true",
			fields: fields{
				graph: &Graph{
					schema: &StateSchema{
						Fields: map[string]StateField{
							"": {DisableDeepCopy: true},
						},
					},
				},
			},
			args: args{key: ""},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Executor{
				graph: tt.fields.graph,

				channelBufferSize:     0,
				maxSteps:              0,
				stepTimeout:           0,
				nodeTimeout:           0,
				checkpointSaveTimeout: 0,
				checkpointSaver:       nil,
				checkpointManager:     nil,
				defaultRetry:          nil,
			}
			if got := e.isDisableDeepCopyKey(tt.args.key); got != tt.want {
				t.Errorf("isDisableDeepCopyKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

type testCase struct {
	Name    string
	Content string
}

func deepCopyNode(_ context.Context, state State) (any, error) {
	DisableDeepCopyMap := state["DisableDeepCopyMap"].(map[string]string)
	DisableDeepCopyMap["a"] = "123"

	deepCopyMap := state["deepCopyMap"].(map[string]string)
	deepCopyMap["a"] = "123"

	DisableDeepCopyPointer := state["DisableDeepCopyPointer"].(*testCase)
	DisableDeepCopyPointer.Content = "content-123"

	deepCopyPointer := state["deepCopyPointer"].(*testCase)
	deepCopyPointer.Content = "content-123"
	return state, nil
}
func TestDisableDeepCopy(t *testing.T) {
	disableDeepCopyMap := map[string]string{
		"a": "111",
		"b": "222",
	}
	disableDeepCopyPointer := &testCase{
		Name:    "test",
		Content: "content",
	}
	deepCopyPointer := &testCase{
		Name:    "test",
		Content: "content",
	}
	deepCopyMap := map[string]string{
		"a": "111",
		"b": "222",
	}
	stateSchema := NewStateSchema().AddField(
		"DisableDeepCopyPointer",
		StateField{
			DisableDeepCopy: true,
			Type:            reflect.TypeOf(disableDeepCopyPointer),
			Reducer:         CoverReducer,
		},
	).AddField(
		"DisableDeepCopyMap",
		StateField{
			DisableDeepCopy: true,
			Type:            reflect.TypeOf(disableDeepCopyMap),
			Reducer:         CoverReducer,
		},
	)
	sg := NewStateGraph(stateSchema)
	sg.AddNode("A", deepCopyNode)
	sg.AddNode("B", deepCopyNode)
	sg.SetEntryPoint("A")
	sg.SetFinishPoint("B")
	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)
	invocation := &agent.Invocation{InvocationID: "test-complex-doc"}
	state := State{
		"DisableDeepCopyMap":     disableDeepCopyMap,
		"DisableDeepCopyPointer": disableDeepCopyPointer,
		"deepCopyMap":            deepCopyMap,
		"deepCopyPointer":        deepCopyPointer,
	}
	eventChan, err := exec.Execute(context.Background(), state, invocation)

	for evt := range eventChan {
		require.NotNil(t, evt)
		continue
	}

	require.Equal(t, "123", disableDeepCopyMap["a"])
	require.Equal(t, "222", disableDeepCopyMap["b"])

	require.Equal(t, "111", deepCopyMap["a"])
	require.Equal(t, "222", deepCopyMap["b"])

	require.Equal(t, "test", disableDeepCopyPointer.Name)
	require.Equal(t, "content-123", disableDeepCopyPointer.Content)

	require.Equal(t, "test", deepCopyPointer.Name)
	require.Equal(t, "content", deepCopyPointer.Content)

	require.NoError(t, err)
}
