package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
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
		func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
			text := args["text"].(string)
			wordCount := len(strings.Fields(text))
			var level string
			if wordCount > 100 {
				level = "complex"
			} else {
				level = "simple"
			}
			return map[string]interface{}{
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
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Create executor
	executor, err := NewExecutor(graph)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Test with complex document
	t.Run("Complex_Document", func(t *testing.T) {
		complexDoc := strings.Repeat("This is a complex document with many words. ", 50)
		initialState := State{StateKeyUserInput: complexDoc}
		invocation := &agent.Invocation{InvocationID: "test-complex-doc"}

		eventChan, err := executor.Execute(context.Background(), initialState, invocation)
		if err != nil {
			t.Fatalf("Failed to execute graph: %v", err)
		}

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
					var value interface{}
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
		if finalState == nil {
			t.Fatal("No final state received")
		}

		if result, ok := finalState[StateKeyLastResponse].(string); !ok {
			t.Error("Expected final response")
		} else if !strings.Contains(result, "FINAL OUTPUT:") {
			t.Errorf("Expected formatted output, got: %s", result)
		}

		if wordCount, ok := finalState["word_count"].(float64); !ok {
			t.Error("Expected word count")
		} else if wordCount < 100 {
			t.Errorf("Expected high word count, got: %f", wordCount)
		}
	})

	// Test with simple document
	t.Run("Simple_Document", func(t *testing.T) {
		simpleDoc := "This is a simple document."
		initialState := State{StateKeyUserInput: simpleDoc}
		invocation := &agent.Invocation{InvocationID: "test-simple-doc"}

		eventChan, err := executor.Execute(context.Background(), initialState, invocation)
		if err != nil {
			t.Fatalf("Failed to execute graph: %v", err)
		}

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
					var value interface{}
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
		if finalState == nil {
			t.Fatal("No final state received")
		}

		if result, ok := finalState[StateKeyLastResponse].(string); !ok {
			t.Error("Expected final response")
		} else if !strings.Contains(result, "FINAL OUTPUT:") {
			t.Errorf("Expected formatted output, got: %s", result)
		}

		if wordCount, ok := finalState["word_count"].(float64); !ok {
			t.Error("Expected word count")
		} else if wordCount > 50 {
			t.Errorf("Expected low word count, got: %f", wordCount)
		}
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
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Create executor
	executor, err := NewExecutor(graph)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Execute with initial state
	initialState := State{
		"counter":  0,
		"messages": []string{},
	}

	invocation := &agent.Invocation{
		InvocationID: "test-basic-workflow",
	}

	eventChan, err := executor.Execute(context.Background(), initialState, invocation)
	if err != nil {
		t.Fatalf("Failed to execute graph: %v", err)
	}

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
					var value interface{}
					if err := json.Unmarshal(valueBytes, &value); err == nil {
						finalState[key] = value
					}
				}
			}
			break
		}
	}

	// Verify results
	if finalState == nil {
		t.Fatal("No final state received")
	}

	if result, ok := finalState["result"].(string); !ok {
		t.Error("Expected result field in final state")
	} else if result != "Final counter: 1, Steps: 1" {
		t.Errorf("Expected result 'Final counter: 1, Steps: 1', got '%s'", result)
	}
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
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Test short path
	t.Run("Short Path", func(t *testing.T) {
		executor, err := NewExecutor(graph)
		if err != nil {
			t.Fatalf("Failed to create executor: %v", err)
		}

		initialState := State{"input": "hi"}
		invocation := &agent.Invocation{InvocationID: "test-short"}

		eventChan, err := executor.Execute(context.Background(), initialState, invocation)
		if err != nil {
			t.Fatalf("Failed to execute graph: %v", err)
		}

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
						var value interface{}
						if err := json.Unmarshal(valueBytes, &value); err == nil {
							finalState[key] = value
						}
					}
				}
				break
			}
		}

		if finalState == nil {
			t.Fatal("No final state received")
		}

		if result, ok := finalState["result"].(string); !ok {
			t.Error("Expected result field")
		} else if result != "Processed as short text" {
			t.Errorf("Expected 'Processed as short text', got '%s'", result)
		}

		if path, ok := finalState["path"].(string); !ok {
			t.Error("Expected path field")
		} else if path != "short" {
			t.Errorf("Expected path 'short', got '%s'", path)
		}
	})

	// Test long path
	t.Run("Long Path", func(t *testing.T) {
		executor, err := NewExecutor(graph)
		if err != nil {
			t.Fatalf("Failed to create executor: %v", err)
		}

		initialState := State{"input": "this is a very long input text"}
		invocation := &agent.Invocation{InvocationID: "test-long"}

		eventChan, err := executor.Execute(context.Background(), initialState, invocation)
		if err != nil {
			t.Fatalf("Failed to execute graph: %v", err)
		}

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
						var value interface{}
						if err := json.Unmarshal(valueBytes, &value); err == nil {
							finalState[key] = value
						}
					}
				}
				break
			}
		}

		if finalState == nil {
			t.Fatal("No final state received")
		}

		if result, ok := finalState["result"].(string); !ok {
			t.Error("Expected result field")
		} else if result != "Processed as long text" {
			t.Errorf("Expected 'Processed as long text', got '%s'", result)
		}

		if path, ok := finalState["path"].(string); !ok {
			t.Error("Expected path field")
		} else if path != "long" {
			t.Errorf("Expected path 'long', got '%s'", path)
		}
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
		AddEdge("decision", "finish"). // Add edge to make finish reachable
		SetFinishPoint("finish")

	// Compile graph
	graph, err := stateGraph.Compile()
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Create executor
	executor, err := NewExecutor(graph)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Execute
	initialState := State{"counter": 0}
	invocation := &agent.Invocation{InvocationID: "test-command"}

	eventChan, err := executor.Execute(context.Background(), initialState, invocation)
	if err != nil {
		t.Fatalf("Failed to execute graph: %v", err)
	}

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
				var value interface{}
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
	if finalState == nil {
		t.Fatal("No final state received")
	}

	if result, ok := finalState["result"].(string); !ok {
		t.Error("Expected result field")
	} else if result != "Workflow completed" {
		t.Errorf("Expected 'Workflow completed', got '%s'", result)
	}

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

	if counter != 3 {
		t.Errorf("Expected counter 3, got %d", counter)
	}
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
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Create executor
	executor, err := NewExecutor(graph)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Execute
	initialState := State{"step": 0}
	invocation := &agent.Invocation{InvocationID: "test-command-simple"}

	eventChan, err := executor.Execute(context.Background(), initialState, invocation)
	if err != nil {
		t.Fatalf("Failed to execute graph: %v", err)
	}

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
					var value interface{}
					if err := json.Unmarshal(valueBytes, &value); err == nil {
						finalState[key] = value
					}
				}
			}
			break
		}
	}

	// Verify results
	if finalState == nil {
		t.Fatal("No final state received")
	}

	if result, ok := finalState["result"].(string); !ok {
		t.Error("Expected result field")
	} else if result != "completed" {
		t.Errorf("Expected result 'completed', got '%s'", result)
	}

	if step, ok := finalState["step"].(float64); !ok { // JSON unmarshals numbers as float64
		t.Error("Expected step field")
	} else if step != 2 {
		t.Errorf("Expected step 2, got %v", step)
	}
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
	if err != nil {
		t.Fatalf("Failed to compile graph: %v", err)
	}

	// Create executor
	executor, err := NewExecutor(graph)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

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
			if err != nil {
				t.Fatalf("Failed to execute graph: %v", err)
			}

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
						var value interface{}
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
			if finalState == nil {
				t.Fatal("No final state received")
			}

			if routedTo, ok := finalState["routed_to"].(string); !ok {
				t.Error("Expected routed_to field")
			} else if routedTo != tc.expectedRoute {
				t.Errorf("Expected route %s, got %s", tc.expectedRoute, routedTo)
			}

			if result, ok := finalState[StateKeyLastResponse].(string); !ok {
				t.Error("Expected final response")
			} else if !strings.Contains(result, "ISSUE ROUTED:") {
				t.Errorf("Expected routing message, got: %s", result)
			}
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
