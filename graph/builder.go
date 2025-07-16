//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package graph

import (
	"context"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// StateGraph provides a fluent interface for building graphs.
// This is the primary public API for creating executable graphs.
//
// StateGraph provides:
//   - Type-safe state management with schemas and reducers
//   - Conditional routing and dynamic node execution
//   - Command support for combined state updates and routing
//
// Example usage:
//
//	schema := NewStateSchema().AddField("counter", StateField{...})
//	graph, err := NewStateGraph(schema).
//	  AddNode("increment", incrementFunc).
//	  SetEntryPoint("increment").
//	  SetFinishPoint("increment").
//	  Compile()
//
// The compiled Graph can then be executed with NewExecutor(graph).
type StateGraph struct {
	graph *Graph
}

// NewStateGraph creates a new graph builder with the given state schema.
func NewStateGraph(schema *StateSchema) *StateGraph {
	return &StateGraph{
		graph: New(schema),
	}
}

// AddNode adds a node to the graph with the given ID and function.
func (sg *StateGraph) AddNode(id string, function NodeFunc) *StateGraph {
	node := &Node{
		ID:       id,
		Name:     id, // Default name to ID, can be overridden
		Function: function,
	}
	sg.graph.AddNode(node)
	return sg
}

// AddNodeWithName adds a node with a custom name and description.
func (sg *StateGraph) AddNodeWithName(id, name, description string, function NodeFunc) *StateGraph {
	node := &Node{
		ID:          id,
		Name:        name,
		Description: description,
		Function:    function,
	}
	sg.graph.AddNode(node)
	return sg
}

// AddEdge adds a normal edge between two nodes.
func (sg *StateGraph) AddEdge(from, to string) *StateGraph {
	edge := &Edge{
		From: from,
		To:   to,
	}
	sg.graph.AddEdge(edge)
	return sg
}

// AddConditionalEdges adds conditional routing from a node.
func (sg *StateGraph) AddConditionalEdges(from string, condition ConditionalFunc, pathMap map[string]string) *StateGraph {
	condEdge := &ConditionalEdge{
		From:      from,
		Condition: condition,
		PathMap:   pathMap,
	}
	sg.graph.AddConditionalEdge(condEdge)
	return sg
}

// SetEntryPoint sets the entry point of the graph.
// This is equivalent to addEdge(Start, nodeId).
func (sg *StateGraph) SetEntryPoint(nodeID string) *StateGraph {
	sg.graph.SetEntryPoint(nodeID)
	// Also add an edge from Start to make it explicit
	sg.AddEdge(Start, nodeID)
	return sg
}

// SetFinishPoint adds an edge from the node to End.
// This is equivalent to addEdge(nodeId, End).
func (sg *StateGraph) SetFinishPoint(nodeID string) *StateGraph {
	sg.AddEdge(nodeID, End)
	return sg
}

// Compile compiles the graph and returns it for execution.
func (sg *StateGraph) Compile() (*Graph, error) {
	if err := sg.graph.Validate(); err != nil {
		return nil, fmt.Errorf("invalid graph: %w", err)
	}
	return sg.graph, nil
}

// MustCompile compiles the graph or panics if invalid.
func (sg *StateGraph) MustCompile() *Graph {
	graph, err := sg.Compile()
	if err != nil {
		panic(err)
	}
	return graph
}

// Builder provides convenience methods for creating common node types.
// This is a convenience wrapper around StateGraph for simpler usage patterns.
//
// Builder is useful when you need:
//   - Quick prototyping with less configuration
//   - Common node patterns (function nodes, LLM nodes)
//   - Backward compatibility with simpler APIs
//
// For full compatibility and advanced features, use StateGraph directly.
type Builder struct {
	stateGraph *StateGraph
}

// NewBuilder creates a new builder with default state schema.
func NewBuilder() *Builder {
	schema := NewStateSchema()
	return &Builder{
		stateGraph: NewStateGraph(schema),
	}
}

// NewBuilderWithSchema creates a new builder with the given state schema.
func NewBuilderWithSchema(schema *StateSchema) *Builder {
	return &Builder{
		stateGraph: NewStateGraph(schema),
	}
}

// AddFunctionNode adds a function node to the graph.
func (b *Builder) AddFunctionNode(id, name, description string, fn NodeFunc) *Builder {
	b.stateGraph.AddNodeWithName(id, name, description, fn)
	return b
}

// AddLLMNode adds an LLM node using the model package directly.
// This follows the user's decision to use model package instead of agent package.
func (b *Builder) AddLLMNode(id, name string, model model.Model, instruction string, tools map[string]tool.Tool) *Builder {
	llmFunc := NewLLMNodeFunc(model, instruction, tools)
	b.stateGraph.AddNodeWithName(id, name, fmt.Sprintf("LLM node: %s", name), llmFunc)
	return b
}

// AddConditionalNode adds a conditional routing node.
func (b *Builder) AddConditionalNode(
	id, name, description string,
	condition ConditionalFunc,
	pathMap map[string]string,
) *Builder {
	// Add a simple pass-through node
	conditionFunc := func(ctx context.Context, state State) (any, error) {
		// Conditional nodes just pass through state, routing happens via edges
		return State(state), nil
	}

	b.stateGraph.AddNodeWithName(id, name, description, conditionFunc)
	b.stateGraph.AddConditionalEdges(id, condition, pathMap)
	return b
}

// AddEdge adds an edge between two nodes.
func (b *Builder) AddEdge(from, to string) *Builder {
	b.stateGraph.AddEdge(from, to)
	return b
}

// SetEntryPoint sets the entry point of the graph.
func (b *Builder) SetEntryPoint(nodeID string) *Builder {
	b.stateGraph.SetEntryPoint(nodeID)
	return b
}

// SetFinishPoint sets the finish point of the graph.
func (b *Builder) SetFinishPoint(nodeID string) *Builder {
	b.stateGraph.SetFinishPoint(nodeID)
	return b
}

// Build compiles and returns the graph.
func (b *Builder) Build() (*Graph, error) {
	return b.stateGraph.Compile()
}

// MustBuild compiles and returns the graph or panics.
func (b *Builder) MustBuild() *Graph {
	return b.stateGraph.MustCompile()
}

// GetStateGraph returns the underlying StateGraph for advanced usage.
func (b *Builder) GetStateGraph() *StateGraph {
	return b.stateGraph
}

// NewLLMNodeFunc creates a NodeFunc that uses the model package directly.
// This implements LLM node functionality using the model package interface.
func NewLLMNodeFunc(llmModel model.Model, instruction string, tools map[string]tool.Tool) NodeFunc {
	return func(ctx context.Context, state State) (any, error) {
		// Extract messages from state or create new ones
		var messages []model.Message
		if msgData, exists := state["messages"]; exists {
			if msgs, ok := msgData.([]model.Message); ok {
				messages = msgs
			}
		}

		// Add system prompt if provided and not already present
		if instruction != "" && (len(messages) == 0 || messages[0].Role != model.RoleSystem) {
			messages = append([]model.Message{model.NewSystemMessage(instruction)}, messages...)
		}

		// Add user input if available
		if userInput, exists := state["user_input"]; exists {
			if input, ok := userInput.(string); ok && input != "" {
				messages = append(messages, model.NewUserMessage(input))
			}
		}

		// Create request
		request := &model.Request{
			Messages: messages,
			Tools:    tools,
			GenerationConfig: model.GenerationConfig{
				Stream: false, // For now, use non-streaming
			},
		}

		// Generate content
		responseChan, err := llmModel.GenerateContent(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("failed to generate content: %w", err)
		}

		// Process response
		var finalResponse *model.Response
		for response := range responseChan {
			if response.Error != nil {
				return nil, fmt.Errorf("model API error: %s", response.Error.Message)
			}
			finalResponse = response
		}

		if finalResponse == nil {
			return nil, fmt.Errorf("no response received from model")
		}

		// Update state with response
		updatedMessages := append(messages, model.Message{
			Role:    model.RoleAssistant,
			Content: finalResponse.Choices[0].Message.Content,
		})

		return State{
			"messages":      updatedMessages,
			"last_response": finalResponse.Choices[0].Message.Content,
		}, nil
	}
}

// Helper functions for creating common state schemas.

// MessagesStateSchema creates a state schema optimized for message-based workflows.
func MessagesStateSchema() *StateSchema {
	schema := NewStateSchema()

	// Add messages field with message reducer
	schema.AddField("messages", StateField{
		Type:    reflect.TypeOf([]model.Message{}),
		Reducer: MessageReducer,
		Default: func() any { return []model.Message{} },
	})
	return schema
}

// ExtendedMessagesStateSchema creates a state schema with messages plus common fields.
func ExtendedMessagesStateSchema() *StateSchema {
	schema := MessagesStateSchema()

	// Add common fields
	schema.AddField("user_input", StateField{
		Type:    reflect.TypeOf(""),
		Reducer: DefaultReducer,
	})

	schema.AddField("result", StateField{
		Type:    reflect.TypeOf(""),
		Reducer: DefaultReducer,
	})

	schema.AddField("metadata", StateField{
		Type:    reflect.TypeOf(map[string]any{}),
		Reducer: MergeReducer,
		Default: func() any { return make(map[string]any) },
	})
	return schema
}
