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

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
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

// AddToolsConditionalEdges adds conditional routing from a LLM node to a tools node.
// If the last message has tool calls, route to the tools node.
// Otherwise, route to the fallback node.
func (sg *StateGraph) AddToolsConditionalEdges(
	fromLLMNode string,
	toToolsNode string,
	fallbackNode string,
) *StateGraph {
	condition := func(ctx context.Context, state State) (string, error) {
		if msgs, ok := state[StateKeyMessages].([]model.Message); ok {
			if len(msgs) > 0 {
				if len(msgs[len(msgs)-1].ToolCalls) > 0 {
					return toToolsNode, nil
				}
			}
		}
		return fallbackNode, nil
	}
	condEdge := &ConditionalEdge{
		From:      fromLLMNode,
		Condition: condition,
		PathMap: map[string]string{
			toToolsNode:  toToolsNode,
			fallbackNode: fallbackNode,
		},
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
		ctx, span := trace.Tracer.Start(ctx, "llm_node_execution")
		defer span.End()

		// Build messages from state.
		messages := buildMessagesFromState(state, instruction)

		// Extract execution context.
		invocationID, eventChan := extractExecutionContext(state)
		modelCallbacks, _ := state[StateKeyModelCallbacks].(*model.Callbacks)

		// Create request.
		request := &model.Request{
			Messages: messages,
			Tools:    tools,
			GenerationConfig: model.GenerationConfig{
				Stream: true,
			},
		}

		responseChan, err := runModel(ctx, modelCallbacks, llmModel, request)
		if err != nil {
			span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return nil, fmt.Errorf("failed to run model: %w", err)
		}

		// Process response.
		var finalResponse *model.Response
		var toolCalls []model.ToolCall
		for response := range responseChan {
			if err := processModelResponse(ctx, response, modelCallbacks, eventChan, invocationID, llmModel, request, span); err != nil {
				return nil, err
			}

			if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
				toolCalls = append(toolCalls, response.Choices[0].Message.ToolCalls...)
			}
			finalResponse = response
		}

		if finalResponse == nil {
			span.SetAttributes(attribute.String("trpc.go.agent.error", "no response received from model"))
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

// buildMessagesFromState extracts and builds messages from the state.
func buildMessagesFromState(state State, instruction string) []model.Message {
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
	return messages
}

// extractExecutionContext extracts execution context from state.
func extractExecutionContext(state State) (string, chan<- *event.Event) {
	var invocationID string
	var eventChan chan<- *event.Event
	if execCtx, exists := state[StateKeyExecContext]; exists {
		execContext, ok := execCtx.(*ExecutionContext)
		if ok {
			eventChan = execContext.EventChan
			invocationID = execContext.InvocationID
		}
	}
	return invocationID, eventChan
}

// processModelResponse processes a single model response.
func processModelResponse(
	ctx context.Context,
	response *model.Response,
	modelCallbacks *model.Callbacks,
	eventChan chan<- *event.Event,
	invocationID string,
	llmModel model.Model,
	request *model.Request,
	span oteltrace.Span,
) error {
	if modelCallbacks != nil {
		customResponse, err := modelCallbacks.RunAfterModel(ctx, response, nil)
		if err != nil {
			span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return fmt.Errorf("callback after model error: %w", err)
		}
		if customResponse != nil {
			response = customResponse
		}
	}
	if eventChan != nil && !response.Done {
		llmEvent := event.NewResponseEvent(invocationID, llmModel.Info().Name, response)
		// Trace the LLM call using the telemetry package.
		itelemetry.TraceCallLLM(span, &agent.Invocation{
			InvocationID: invocationID,
			Model:        llmModel,
		}, request, response, llmEvent.ID)
		select {
		case eventChan <- llmEvent:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if response.Error != nil {
		span.SetAttributes(attribute.String("trpc.go.agent.error", response.Error.Message))
		return fmt.Errorf("model API error: %s", response.Error.Message)
	}
	return nil
}

func runModel(
	ctx context.Context,
	modelCallbacks *model.Callbacks,
	llmModel model.Model,
	request *model.Request,
) (<-chan *model.Response, error) {
	ctx, span := trace.Tracer.Start(ctx, "run_model")
	defer span.End()

	// Set span attributes for model execution.
	span.SetAttributes(
		attribute.String("trpc.go.agent.model_name", llmModel.Info().Name),
	)

	if modelCallbacks != nil {
		customResponse, err := modelCallbacks.RunBeforeModel(ctx, request)
		if err != nil {
			span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return nil, fmt.Errorf("callback before model error: %w", err)
		}
		if customResponse != nil {
			responseChan := make(chan *model.Response, 1)
			responseChan <- customResponse
			close(responseChan)
			return responseChan, nil
		}
	}
	// Generate content.
	responseChan, err := llmModel.GenerateContent(ctx, request)
	if err != nil {
		span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}
	return responseChan, nil
}

// NewToolsNodeFunc creates a NodeFunc that uses the tools package directly.
// This implements tools node functionality using the tools package interface.
func NewToolsNodeFunc(tools map[string]tool.Tool) NodeFunc {
	return func(ctx context.Context, state State) (any, error) {
		ctx, span := trace.Tracer.Start(ctx, "tools_node_execution")
		defer span.End()

		var messages []model.Message
		if msgData, exists := state[StateKeyMessages]; exists {
			if msgs, ok := msgData.([]model.Message); ok {
				messages = msgs
			}
		}
		toolCallbacks, _ := state[StateKeyToolCallbacks].(*tool.Callbacks)
		if len(messages) == 0 {
			span.SetAttributes(attribute.String("trpc.go.agent.error", "no messages in state"))
			return nil, errors.New("no messages in state")
		}
		lastMessage := messages[len(messages)-1]
		if lastMessage.Role != model.RoleAssistant {
			span.SetAttributes(attribute.String("trpc.go.agent.error", "last message is not an assistant message"))
			return nil, errors.New("last message is not an assistant message")
		}
		toolCalls := lastMessage.ToolCalls
		newMessages := make([]model.Message, 0, len(toolCalls))
		for _, toolCall := range toolCalls {
			id, name := toolCall.ID, toolCall.Function.Name
			t := tools[name]
			if t == nil {
				span.SetAttributes(attribute.String("trpc.go.agent.error", fmt.Sprintf("tool %s not found", name)))
				return nil, fmt.Errorf("tool %s not found", name)
			}
			result, err := runTool(ctx, toolCall, toolCallbacks, t)
			if err != nil {
				span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
				return nil, fmt.Errorf("tool %s call failed: %w", name, err)
			}
			content, err := json.Marshal(result)
			if err != nil {
				span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
				return nil, fmt.Errorf("failed to marshal tool result: %w", err)
			}
			newMessages = append(newMessages, model.NewToolMessage(id, name, string(content)))
		}
		return State{
			StateKeyMessages: newMessages,
		}, nil
	}
}

func runTool(
	ctx context.Context,
	toolCall model.ToolCall,
	toolCallbacks *tool.Callbacks,
	t tool.Tool,
) (any, error) {
	ctx, span := trace.Tracer.Start(ctx, fmt.Sprintf("execute_tool %s", toolCall.Function.Name))
	defer span.End()

	// Set span attributes for tool execution.
	span.SetAttributes(
		attribute.String("trpc.go.agent.tool_name", toolCall.Function.Name),
		attribute.String("trpc.go.agent.tool_id", toolCall.ID),
		attribute.String("trpc.go.agent.tool_description", t.Declaration().Description),
	)

	if toolCallbacks != nil {
		customResult, err := toolCallbacks.RunBeforeTool(
			ctx, toolCall.Function.Name, t.Declaration(), toolCall.Function.Arguments)
		if err != nil {
			span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return nil, fmt.Errorf("callback before tool error: %w", err)
		}
		if customResult != nil {
			return customResult, nil
		}
	}
	if callableTool, ok := t.(tool.CallableTool); ok {
		result, err := callableTool.Call(ctx, toolCall.Function.Arguments)
		if err != nil {
			span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return nil, fmt.Errorf("tool %s call failed: %w", toolCall.Function.Name, err)
		}
		if toolCallbacks != nil {
			customResult, err := toolCallbacks.RunAfterTool(
				ctx, toolCall.Function.Name, t.Declaration(), toolCall.Function.Arguments, result, err)
			if err != nil {
				span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
				return nil, fmt.Errorf("callback after tool error: %w", err)
			}
			if customResult != nil {
				return customResult, nil
			}
		}
		return result, nil
	}
	span.SetAttributes(attribute.String("trpc.go.agent.error", fmt.Sprintf("tool %s is not callable", toolCall.Function.Name)))
	return nil, fmt.Errorf("tool %s is not callable", toolCall.Function.Name)
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
