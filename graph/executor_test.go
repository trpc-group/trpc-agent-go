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
	} else if vals, ok := finalState["results"].([]interface{}); ok {
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
	} else if vals, ok := finalState["results"].([]interface{}); ok {
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
		exec.emitStateUpdateEvent(execCtx)
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
		require.NoError(t, exec.processConditionalEdges(context.Background(), execCtx, "start", i), "conditional processing failed.")
	}
	close(stopCh)
}
