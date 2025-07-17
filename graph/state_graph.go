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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/event"
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

// Option is a function that configures a Node.
type Option func(*Node)

// WithName sets the name of the node.
func WithName(name string) Option {
	return func(node *Node) {
		node.Name = name
	}
}

// WithDescription sets the description of the node.
func WithDescription(description string) Option {
	return func(node *Node) {
		node.Description = description
	}
}

// AddNode adds a node with the given ID and function.
// The name and description of the node can be set with the options.
func (sg *StateGraph) AddNode(id string, function NodeFunc, opts ...Option) *StateGraph {
	node := &Node{
		ID:       id,
		Name:     id,
		Function: function,
	}
	for _, opt := range opts {
		opt(node)
	}
	sg.graph.addNode(node)
	return sg
}

// AddLLMNode adds a node that uses the model package directly.
func (sg *StateGraph) AddLLMNode(
	id string,
	model model.Model,
	instruction string,
	tools map[string]tool.Tool,
	opts ...Option,
) *StateGraph {
	llmNodeFunc := NewLLMNodeFunc(model, instruction, tools)
	sg.AddNode(id, llmNodeFunc, opts...)
	return sg
}

// AddToolsNode adds a node that uses the tools package directly.
func (sg *StateGraph) AddToolsNode(
	id string,
	tools map[string]tool.Tool,
	opts ...Option,
) *StateGraph {
	toolsNodeFunc := NewToolsNodeFunc(tools)
	sg.AddNode(id, toolsNodeFunc, opts...)
	return sg
}

// AddEdge adds a normal edge between two nodes.
func (sg *StateGraph) AddEdge(from, to string) *StateGraph {
	edge := &Edge{
		From: from,
		To:   to,
	}
	sg.graph.addEdge(edge)
	return sg
}

// AddConditionalEdges adds conditional routing from a node.
func (sg *StateGraph) AddConditionalEdges(
	from string,
	condition ConditionalFunc,
	pathMap map[string]string,
) *StateGraph {
	condEdge := &ConditionalEdge{
		From:      from,
		Condition: condition,
		PathMap:   pathMap,
	}
	sg.graph.addConditionalEdge(condEdge)
	return sg
}

// SetEntryPoint sets the entry point of the graph.
// This is equivalent to addEdge(Start, nodeId).
func (sg *StateGraph) SetEntryPoint(nodeID string) *StateGraph {
	sg.graph.setEntryPoint(nodeID)
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
	if err := sg.graph.validate(); err != nil {
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

// NewLLMNodeFunc creates a NodeFunc that uses the model package directly.
// This implements LLM node functionality using the model package interface.
func NewLLMNodeFunc(llmModel model.Model, instruction string, tools map[string]tool.Tool) NodeFunc {
	return func(ctx context.Context, state State) (any, error) {
		// Extract messages from state or create new ones
		var messages []model.Message
		if msgData, exists := state[StateKeyMessages]; exists {
			if msgs, ok := msgData.([]model.Message); ok {
				messages = msgs
			}
		}
		// Add system prompt if provided and not already present.
		if instruction != "" && (len(messages) == 0 || messages[0].Role != model.RoleSystem) {
			messages = append([]model.Message{model.NewSystemMessage(instruction)}, messages...)
		}
		// Add user input if available.
		if userInput, exists := state[StateKeyUserInput]; exists {
			if input, ok := userInput.(string); ok && input != "" {
				messages = append(messages, model.NewUserMessage(input))
			}
		}
		var invocationID string
		var eventChan chan<- *event.Event
		if execCtx, exists := state[StateKeyExecContext]; exists {
			execContext, ok := execCtx.(*ExecutionContext)
			if ok {
				eventChan = execContext.EventChan
				invocationID = execContext.InvocationID
			}
		}
		// Create request.
		request := &model.Request{
			Messages: messages,
			Tools:    tools,
			GenerationConfig: model.GenerationConfig{
				Stream: true,
			},
		}
		// Generate content.
		responseChan, err := llmModel.GenerateContent(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("failed to generate content: %w", err)
		}
		// Process response.
		var finalResponse *model.Response
		var toolCalls []model.ToolCall
		for response := range responseChan {
			if eventChan != nil && !response.Done {
				select {
				case eventChan <- event.NewResponseEvent(invocationID, llmModel.Info().Name, response):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if response.Error != nil {
				return nil, fmt.Errorf("model API error: %s", response.Error.Message)
			}
			if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
				toolCalls = append(toolCalls, response.Choices[0].Message.ToolCalls...)
			}
			finalResponse = response
		}
		if finalResponse == nil {
			return nil, errors.New("no response received from model")
		}
		newMessage := model.Message{
			Role:      model.RoleAssistant,
			Content:   finalResponse.Choices[0].Message.Content,
			ToolCalls: toolCalls,
		}
		return State{
			StateKeyMessages:     []model.Message{newMessage}, // The new message will be merged by the executor.
			StateKeyLastResponse: finalResponse.Choices[0].Message.Content,
		}, nil
	}
}

// NewToolsNodeFunc creates a NodeFunc that uses the tools package directly.
// This implements tools node functionality using the tools package interface.
func NewToolsNodeFunc(tools map[string]tool.Tool) NodeFunc {
	return func(ctx context.Context, state State) (any, error) {
		var messages []model.Message
		if msgData, exists := state[StateKeyMessages]; exists {
			if msgs, ok := msgData.([]model.Message); ok {
				messages = msgs
			}
		}
		if len(messages) == 0 {
			return nil, errors.New("no messages in state")
		}
		lastMessage := messages[len(messages)-1]
		if lastMessage.Role != model.RoleAssistant {
			return nil, errors.New("last message is not an assistant message")
		}
		toolCalls := lastMessage.ToolCalls
		newMessages := make([]model.Message, 0, len(toolCalls))
		for _, toolCall := range toolCalls {
			id, name := toolCall.ID, toolCall.Function.Name
			t := tools[name]
			if t == nil {
				return nil, fmt.Errorf("tool %s not found", name)
			}
			if callableTool, ok := t.(tool.CallableTool); ok {
				result, err := callableTool.Call(ctx, toolCall.Function.Arguments)
				if err != nil {
					return nil, fmt.Errorf("tool %s call failed: %w", name, err)
				}
				content, err := json.Marshal(result)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal tool result: %w", err)
				}
				newMessages = append(newMessages, model.NewToolMessage(id, name, string(content)))
			} else {
				return nil, fmt.Errorf("tool %s is not callable", name)
			}
		}
		return State{
			StateKeyMessages: newMessages,
		}, nil
	}
}

// MessagesStateSchema creates a state schema optimized for message-based workflows.
func MessagesStateSchema() *StateSchema {
	schema := NewStateSchema()
	schema.AddField(StateKeyMessages, StateField{
		Type:    reflect.TypeOf([]model.Message{}),
		Reducer: MessageReducer,
		Default: func() any { return []model.Message{} },
	})
	schema.AddField(StateKeyUserInput, StateField{
		Type:    reflect.TypeOf(""),
		Reducer: DefaultReducer,
	})
	schema.AddField(StateKeyLastResponse, StateField{
		Type:    reflect.TypeOf(""),
		Reducer: DefaultReducer,
	})
	schema.AddField(StateKeyMetadata, StateField{
		Type:    reflect.TypeOf(map[string]any{}),
		Reducer: MergeReducer,
		Default: func() any { return make(map[string]any) },
	})
	return schema
}
