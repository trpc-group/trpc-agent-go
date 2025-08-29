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
	"errors"
	"fmt"
	"reflect"
	"time"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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

// WithNodeType sets the type of the node.
func WithNodeType(nodeType NodeType) Option {
	return func(node *Node) {
		node.Type = nodeType
	}
}

// WithPreNodeCallback sets a callback that will be executed before this specific node.
// This callback is specific to this node and will be executed in addition to any global callbacks.
func WithPreNodeCallback(callback BeforeNodeCallback) Option {
	return func(node *Node) {
		if node.callbacks == nil {
			node.callbacks = NewNodeCallbacks()
		}
		node.callbacks.RegisterBeforeNode(callback)
	}
}

// WithPostNodeCallback sets a callback that will be executed after this specific node.
// This callback is specific to this node and will be executed in addition to any global callbacks.
func WithPostNodeCallback(callback AfterNodeCallback) Option {
	return func(node *Node) {
		if node.callbacks == nil {
			node.callbacks = NewNodeCallbacks()
		}
		node.callbacks.RegisterAfterNode(callback)
	}
}

// WithNodeErrorCallback sets a callback that will be executed when this specific node fails.
// This callback is specific to this node and will be executed in addition to any global callbacks.
func WithNodeErrorCallback(callback OnNodeErrorCallback) Option {
	return func(node *Node) {
		if node.callbacks == nil {
			node.callbacks = NewNodeCallbacks()
		}
		node.callbacks.RegisterOnNodeError(callback)
	}
}

// WithNodeCallbacks sets multiple callbacks for this specific node.
// This allows setting multiple callbacks at once for convenience.
func WithNodeCallbacks(callbacks *NodeCallbacks) Option {
	return func(node *Node) {
		if node.callbacks == nil {
			node.callbacks = NewNodeCallbacks()
		}
		// Merge the provided callbacks with existing ones
		if callbacks != nil {
			node.callbacks.BeforeNode = append(node.callbacks.BeforeNode, callbacks.BeforeNode...)
			node.callbacks.AfterNode = append(node.callbacks.AfterNode, callbacks.AfterNode...)
			node.callbacks.OnNodeError = append(node.callbacks.OnNodeError, callbacks.OnNodeError...)
		}
	}
}

// WithAgentNodeEventCallback sets a callback that will be executed when an agent event is emitted.
// This callback is specific to this node and will be executed in addition to any global callbacks.
func WithAgentNodeEventCallback(callback AgentEventCallback) Option {
	return func(node *Node) {
		if node.callbacks == nil {
			node.callbacks = NewNodeCallbacks()
		}
		node.callbacks.AgentEvent = append(node.callbacks.AgentEvent, callback)
	}
}

// AddNode adds a node with the given ID and function.
// The name and description of the node can be set with the options.
// This automatically sets up Pregel-style channel configuration.
func (sg *StateGraph) AddNode(id string, function NodeFunc, opts ...Option) *StateGraph {
	node := &Node{
		ID:       id,
		Name:     id,
		Function: function,
		Type:     NodeTypeFunction, // Default to function type
	}
	for _, opt := range opts {
		opt(node)
	}
	sg.graph.addNode(node)

	// Automatically set up Pregel-style configuration
	// Create a trigger channel for this node
	triggerChannel := fmt.Sprintf("trigger:%s", id)
	sg.graph.addChannel(triggerChannel, channel.BehaviorLastValue)
	sg.graph.addNodeTriggerChannel(id, triggerChannel)

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
	// Add LLM node type option
	llmOpts := append([]Option{WithNodeType(NodeTypeLLM)}, opts...)
	sg.AddNode(id, llmNodeFunc, llmOpts...)
	return sg
}

// AddToolsNode adds a node that uses the tools package directly.
func (sg *StateGraph) AddToolsNode(
	id string,
	tools map[string]tool.Tool,
	opts ...Option,
) *StateGraph {
	toolsNodeFunc := NewToolsNodeFunc(tools)
	// Add tool node type option
	toolOpts := append([]Option{WithNodeType(NodeTypeTool)}, opts...)
	sg.AddNode(id, toolsNodeFunc, toolOpts...)
	return sg
}

// AddAgentNode adds a node that uses a sub-agent by name.
// The agent name should correspond to a sub-agent in the GraphAgent's sub-agent list.
func (sg *StateGraph) AddAgentNode(
	id string,
	opts ...Option,
) *StateGraph {
	agentNodeFunc := NewAgentNodeFunc(id, opts...)
	// Add agent node type option.
	agentOpts := append([]Option{WithNodeType(NodeTypeAgent)}, opts...)
	sg.AddNode(id, agentNodeFunc, agentOpts...)
	return sg
}

// channelUpdateMarker value for marking channel updates.
const channelUpdateMarker = "update"

// AddEdge adds a normal edge between two nodes.
// This automatically sets up Pregel-style channel configuration.
func (sg *StateGraph) AddEdge(from, to string) *StateGraph {
	edge := &Edge{
		From: from,
		To:   to,
	}
	sg.graph.addEdge(edge)
	// Automatically set up Pregel-style channel for the edge.
	channelName := fmt.Sprintf("branch:to:%s", to)
	sg.graph.addChannel(channelName, channel.BehaviorLastValue)
	// Set up trigger relationship (node subscribes) and trigger mapping.
	sg.graph.addNodeTriggerChannel(to, channelName)
	sg.graph.addNodeTrigger(channelName, to)
	// Add writer to source node.
	writer := channelWriteEntry{
		Channel: channelName,
		Value:   channelUpdateMarker, // Non-nil sentinel to mark update.
	}
	sg.graph.addNodeWriter(from, writer)
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

// WithNodeCallbacks adds node callbacks to the graph state schema.
// This allows users to register callbacks that will be executed during node execution.
func (sg *StateGraph) WithNodeCallbacks(callbacks *NodeCallbacks) *StateGraph {
	sg.graph.schema.AddField(StateKeyNodeCallbacks, StateField{
		Type:    reflect.TypeOf(&NodeCallbacks{}),
		Reducer: DefaultReducer,
		Default: func() any { return callbacks },
	})
	return sg
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
		// Extract execution context and model information.
		invocationID, sessionID, eventChan := extractExecutionContext(state)
		modelCallbacks, _ := state[StateKeyModelCallbacks].(*model.Callbacks)
		// Extract current node ID from state.
		var nodeID string
		if nodeIDData, exists := state[StateKeyCurrentNodeID]; exists {
			if id, ok := nodeIDData.(string); ok {
				nodeID = id
			}
		}
		// Build messages from state.
		messages := buildMessagesFromState(state, instruction)

		// Create request.
		request := &model.Request{
			Messages: messages,
			Tools:    tools,
			GenerationConfig: model.GenerationConfig{
				Stream: true,
			},
		}

		// Extract model input for event emission.
		modelInput := extractModelInput(state, instruction)

		// Emit model execution start event.
		startTime := time.Now()
		modelName := getModelName(llmModel)
		emitModelStartEvent(eventChan, invocationID, modelName, nodeID, modelInput, startTime)

		// Execute the model.
		result, err := executeModelWithEvents(ctx, modelExecutionConfig{
			ModelCallbacks: modelCallbacks,
			LLMModel:       llmModel,
			Request:        request,
			EventChan:      eventChan,
			InvocationID:   invocationID,
			SessionID:      sessionID,
			Span:           span,
			NodeID:         nodeID,
		})

		// Emit model execution complete event.
		endTime := time.Now()
		var modelOutput string
		if err == nil && result != nil {
			if finalResponse, ok := result.(*model.Response); ok && len(finalResponse.Choices) > 0 {
				modelOutput = finalResponse.Choices[0].Message.Content
			}
		}
		emitModelCompleteEvent(eventChan, invocationID, modelName, nodeID, modelInput, modelOutput, startTime, endTime, err)

		if err != nil {
			span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return nil, fmt.Errorf("failed to run model: %w", err)
		}

		return result, nil
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
	// Check if the last message is from assistant, and if so, append current user input.
	// This is required by some APIs that enforce the last message must be from user.
	if len(messages) > 0 && (messages[len(messages)-1].Role == model.RoleAssistant ||
		messages[len(messages)-1].Role == model.RoleSystem) {
		if userInput, exists := state[StateKeyUserInput]; exists {
			if input, ok := userInput.(string); ok && input != "" {
				messages = append(messages, model.NewUserMessage(input))
			}
		}
	}
	return messages
}

// extractExecutionContext extracts execution context from state.
func extractExecutionContext(state State) (invocationID string, sessionID string, eventChan chan<- *event.Event) {
	if execCtx, exists := state[StateKeyExecContext]; exists {
		execContext, ok := execCtx.(*ExecutionContext)
		if ok {
			eventChan = execContext.EventChan
			invocationID = execContext.InvocationID
		}
	}
	if sess, ok := state[StateKeySession]; ok {
		if s, ok := sess.(*session.Session); ok && s != nil {
			sessionID = s.ID
		}

	}
	return invocationID, sessionID, eventChan
}

// modelResponseConfig contains configuration for processing model responses.
type modelResponseConfig struct {
	Response       *model.Response
	ModelCallbacks *model.Callbacks
	EventChan      chan<- *event.Event
	InvocationID   string
	SessionID      string
	LLMModel       model.Model
	Request        *model.Request
	Span           oteltrace.Span
	// NodeID, when provided, is used as the event author.
	NodeID string
}

// processModelResponse processes a single model response.
func processModelResponse(ctx context.Context, config modelResponseConfig) error {
	if config.ModelCallbacks != nil {
		customResponse, err := config.ModelCallbacks.RunAfterModel(ctx, config.Request, config.Response, nil)
		if err != nil {
			config.Span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return fmt.Errorf("callback after model error: %w", err)
		}
		if customResponse != nil {
			config.Response = customResponse
		}
	}
	if config.EventChan != nil && !config.Response.Done {
		author := config.LLMModel.Info().Name
		if config.NodeID != "" {
			author = config.NodeID
		}
		llmEvent := event.NewResponseEvent(config.InvocationID, author, config.Response)
		// Trace the LLM call using the telemetry package.
		itelemetry.TraceCallLLM(config.Span, &agent.Invocation{
			InvocationID: config.InvocationID,
			Model:        config.LLMModel,
			Session:      &session.Session{ID: config.SessionID},
		}, config.Request, config.Response, llmEvent.ID)
		select {
		case config.EventChan <- llmEvent:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if config.Response.Error != nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", config.Response.Error.Message))
		return fmt.Errorf("model API error: %s", config.Response.Error.Message)
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

		// Extract and validate messages from state.
		toolCalls, err := extractToolCallsFromState(state, span)
		if err != nil {
			return nil, err
		}

		// Extract execution context for event emission.
		invocationID, _, eventChan := extractExecutionContext(state)

		// Process all tool calls and collect results.
		newMessages, err := processToolCalls(ctx, toolCallsConfig{
			ToolCalls:    toolCalls,
			Tools:        tools,
			InvocationID: invocationID,
			EventChan:    eventChan,
			Span:         span,
			State:        state,
		})
		if err != nil {
			return nil, err
		}
		return State{
			StateKeyMessages: newMessages,
		}, nil
	}
}

// NewAgentNodeFunc creates a NodeFunc that looks up and uses a sub-agent by name.
// The agent name should correspond to a sub-agent in the parent GraphAgent's sub-agent list.
func NewAgentNodeFunc(agentName string, opts ...Option) NodeFunc {
	dummyNode := &Node{}
	for _, opt := range opts {
		opt(dummyNode)
	}
	nodeCallbacks := dummyNode.callbacks
	return func(ctx context.Context, state State) (any, error) {
		ctx, span := trace.Tracer.Start(ctx, "agent_node_execution")
		defer span.End()

		// Extract execution context for event emission.
		invocationID, _, eventChan := extractExecutionContext(state)

		// Extract current node ID from state.
		var nodeID string
		if nodeIDData, exists := state[StateKeyCurrentNodeID]; exists {
			if id, ok := nodeIDData.(string); ok {
				nodeID = id
			}
		}

		// Extract parent agent from state to find the sub-agent.
		parentAgent, parentExists := state[StateKeyParentAgent]
		if !parentExists {
			return nil, fmt.Errorf("parent agent not found in state for agent node %s", agentName)
		}

		// Look up the target agent by name from the parent's sub-agents.
		targetAgent := findSubAgentByName(parentAgent, agentName)
		if targetAgent == nil {
			return nil, fmt.Errorf("sub-agent '%s' not found in parent agent's sub-agent list", agentName)
		}

		// Extract agent callbacks from state.
		agentCallbacks, _ := state[StateKeyAgentCallbacks].(*agent.Callbacks)

		// Build invocation for the target agent.
		invocation := buildAgentInvocation(state, targetAgent, agentCallbacks)

		// Emit agent execution start event.
		startTime := time.Now()
		emitAgentStartEvent(eventChan, invocationID, nodeID, startTime)

		// Execute the target agent.
		agentEventChan, err := targetAgent.Run(ctx, invocation)
		if err != nil {
			// Emit agent execution error event.
			endTime := time.Now()
			emitAgentErrorEvent(eventChan, invocationID, agentName, nodeID, startTime, endTime, err)
			span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return nil, fmt.Errorf("failed to run agent %s: %w", agentName, err)
		}

		// Forward all events from the target agent.
		var lastResponse string
		for agentEvent := range agentEventChan {
			if nodeCallbacks != nil {
				for _, callback := range nodeCallbacks.AgentEvent {
					callback(ctx, &NodeCallbackContext{
						NodeID:   nodeID,
						NodeName: agentName,
					}, state, agentEvent)
				}
			}
			// Forward the event to the parent event channel.
			select {
			case eventChan <- agentEvent:
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			// Track the last response for state update.
			if agentEvent.Response != nil && len(agentEvent.Response.Choices) > 0 &&
				agentEvent.Response.Choices[0].Message.Content != "" {
				lastResponse = agentEvent.Response.Choices[0].Message.Content
			}
		}
		// Emit agent execution complete event.
		endTime := time.Now()
		emitAgentCompleteEvent(eventChan, invocationID, agentName, nodeID, startTime, endTime)
		// Update state with the agent's response.
		stateUpdate := State{}
		stateUpdate[StateKeyLastResponse] = lastResponse
		return stateUpdate, nil
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

// extractModelInput extracts the model input from state and instruction.
func extractModelInput(state State, instruction string) string {
	var input string
	// Get user input if available.
	if userInput, exists := state[StateKeyUserInput]; exists {
		if inputStr, ok := userInput.(string); ok && inputStr != "" {
			input = inputStr
		}
	}
	// Add instruction if provided.
	if instruction != "" {
		if input != "" {
			input = instruction + "\n\n" + input
		} else {
			input = instruction
		}
	}
	return input
}

// getModelName extracts the model name from the model instance.
func getModelName(llmModel model.Model) string {
	return llmModel.Info().Name
}

// emitModelStartEvent emits a model execution start event.
func emitModelStartEvent(
	eventChan chan<- *event.Event,
	invocationID, modelName, nodeID, modelInput string,
	startTime time.Time,
) {
	if eventChan == nil {
		return
	}

	modelStartEvent := NewModelExecutionEvent(
		WithModelEventInvocationID(invocationID),
		WithModelEventModelName(modelName),
		WithModelEventNodeID(nodeID),
		WithModelEventPhase(ModelExecutionPhaseStart),
		WithModelEventStartTime(startTime),
		WithModelEventInput(modelInput),
	)

	select {
	case eventChan <- modelStartEvent:
	default:
	}
}

// emitModelCompleteEvent emits a model execution complete event.
func emitModelCompleteEvent(
	eventChan chan<- *event.Event,
	invocationID, modelName, nodeID, modelInput, modelOutput string,
	startTime, endTime time.Time,
	err error,
) {
	if eventChan == nil {
		return
	}

	modelCompleteEvent := NewModelExecutionEvent(
		WithModelEventInvocationID(invocationID),
		WithModelEventModelName(modelName),
		WithModelEventNodeID(nodeID),
		WithModelEventPhase(ModelExecutionPhaseComplete),
		WithModelEventStartTime(startTime),
		WithModelEventEndTime(endTime),
		WithModelEventInput(modelInput),
		WithModelEventOutput(modelOutput),
		WithModelEventError(err),
	)

	select {
	case eventChan <- modelCompleteEvent:
	default:
	}
}

// modelExecutionConfig contains configuration for model execution with events.
type modelExecutionConfig struct {
	ModelCallbacks *model.Callbacks
	LLMModel       model.Model
	Request        *model.Request
	EventChan      chan<- *event.Event
	InvocationID   string
	SessionID      string
	Span           oteltrace.Span
	// NodeID, when provided, is used as the event author.
	NodeID string
}

// executeModelWithEvents executes the model with event processing.
func executeModelWithEvents(ctx context.Context, config modelExecutionConfig) (any, error) {
	responseChan, err := runModel(ctx, config.ModelCallbacks, config.LLMModel, config.Request)
	if err != nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
		return nil, fmt.Errorf("failed to run model: %w", err)
	}
	// Process response.
	var finalResponse *model.Response
	var toolCalls []model.ToolCall
	for response := range responseChan {
		if err := processModelResponse(ctx, modelResponseConfig{
			Response:       response,
			ModelCallbacks: config.ModelCallbacks,
			EventChan:      config.EventChan,
			InvocationID:   config.InvocationID,
			SessionID:      config.SessionID,
			LLMModel:       config.LLMModel,
			Request:        config.Request,
			Span:           config.Span,
			NodeID:         config.NodeID,
		}); err != nil {
			return nil, err
		}

		if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
			toolCalls = append(toolCalls, response.Choices[0].Message.ToolCalls...)
		}
		finalResponse = response
	}
	if finalResponse == nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", "no response received from model"))
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

// extractToolCallsFromState extracts and validates tool calls from the state.
func extractToolCallsFromState(state State, span oteltrace.Span) ([]model.ToolCall, error) {
	var messages []model.Message
	if msgData, exists := state[StateKeyMessages]; exists {
		if msgs, ok := msgData.([]model.Message); ok {
			messages = msgs
		}
	}

	if len(messages) == 0 {
		span.SetAttributes(attribute.String("trpc.go.agent.error", "no messages in state"))
		return nil, errors.New("no messages in state")
	}

	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != model.RoleAssistant {
		span.SetAttributes(attribute.String("trpc.go.agent.error", "last message is not an assistant message"))
		return nil, errors.New("last message is not an assistant message")
	}

	return lastMessage.ToolCalls, nil
}

// toolCallsConfig contains configuration for processing tool calls.
type toolCallsConfig struct {
	ToolCalls    []model.ToolCall
	Tools        map[string]tool.Tool
	InvocationID string
	EventChan    chan<- *event.Event
	Span         oteltrace.Span
	State        State
}

// processToolCalls executes all tool calls and returns the resulting messages.
func processToolCalls(ctx context.Context, config toolCallsConfig) ([]model.Message, error) {
	toolCallbacks, _ := extractToolCallbacks(config.State)
	newMessages := make([]model.Message, 0, len(config.ToolCalls))

	for _, toolCall := range config.ToolCalls {
		toolMessage, err := executeSingleToolCall(ctx, singleToolCallConfig{
			ToolCall:      toolCall,
			Tools:         config.Tools,
			InvocationID:  config.InvocationID,
			EventChan:     config.EventChan,
			Span:          config.Span,
			ToolCallbacks: toolCallbacks,
			State:         config.State,
		})
		if err != nil {
			return nil, err
		}
		newMessages = append(newMessages, toolMessage)
	}

	return newMessages, nil
}

// singleToolCallConfig contains configuration for executing a single tool call.
type singleToolCallConfig struct {
	ToolCall      model.ToolCall
	Tools         map[string]tool.Tool
	InvocationID  string
	EventChan     chan<- *event.Event
	Span          oteltrace.Span
	ToolCallbacks *tool.Callbacks
	State         State
}

// executeSingleToolCall executes a single tool call with event emission.
func executeSingleToolCall(ctx context.Context, config singleToolCallConfig) (model.Message, error) {
	id, name := config.ToolCall.ID, config.ToolCall.Function.Name
	t := config.Tools[name]
	if t == nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", fmt.Sprintf("tool %s not found", name)))
		return model.Message{}, fmt.Errorf("tool %s not found", name)
	}

	startTime := time.Now()

	// Extract current node ID from state for event authoring.
	var nodeID string
	if state := config.State; state != nil {
		if nodeIDData, exists := state[StateKeyCurrentNodeID]; exists {
			if id, ok := nodeIDData.(string); ok {
				nodeID = id
			}
		}
	}

	// Emit tool execution start event.
	emitToolStartEvent(
		config.EventChan, config.InvocationID, name, id, nodeID,
		startTime, config.ToolCall.Function.Arguments,
	)

	// Execute the tool.
	result, err := runTool(ctx, config.ToolCall, config.ToolCallbacks, t)

	// Emit tool execution complete event.
	emitToolCompleteEvent(toolCompleteEventConfig{
		EventChan:    config.EventChan,
		InvocationID: config.InvocationID,
		ToolName:     name,
		ToolID:       id,
		NodeID:       nodeID,
		StartTime:    startTime,
		Result:       result,
		Error:        err,
		Arguments:    config.ToolCall.Function.Arguments,
	})

	if err != nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
		return model.Message{}, fmt.Errorf("tool %s call failed: %w", name, err)
	}

	// Marshal result to JSON.
	content, err := json.Marshal(result)
	if err != nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
		return model.Message{}, fmt.Errorf("failed to marshal tool result: %w", err)
	}

	return model.NewToolMessage(id, name, string(content)), nil
}

// emitToolStartEvent emits a tool execution start event.
func emitToolStartEvent(
	eventChan chan<- *event.Event,
	invocationID, toolName, toolID, nodeID string,
	startTime time.Time,
	arguments []byte,
) {
	if eventChan == nil {
		return
	}

	toolStartEvent := NewToolExecutionEvent(
		WithToolEventInvocationID(invocationID),
		WithToolEventToolName(toolName),
		WithToolEventToolID(toolID),
		WithToolEventNodeID(nodeID),
		WithToolEventPhase(ToolExecutionPhaseStart),
		WithToolEventStartTime(startTime),
		WithToolEventInput(string(arguments)),
	)

	select {
	case eventChan <- toolStartEvent:
	default:
	}
}

// toolCompleteEventConfig contains configuration for tool complete events.
type toolCompleteEventConfig struct {
	EventChan    chan<- *event.Event
	InvocationID string
	ToolName     string
	ToolID       string
	NodeID       string
	StartTime    time.Time
	Result       any
	Error        error
	Arguments    []byte
}

// emitToolCompleteEvent emits a tool execution complete event.
func emitToolCompleteEvent(config toolCompleteEventConfig) {
	if config.EventChan == nil {
		return
	}

	endTime := time.Now()
	var outputStr string
	if config.Error == nil && config.Result != nil {
		if outputBytes, marshalErr := json.Marshal(config.Result); marshalErr == nil {
			outputStr = string(outputBytes)
		}
	}

	toolCompleteEvent := NewToolExecutionEvent(
		WithToolEventInvocationID(config.InvocationID),
		WithToolEventToolName(config.ToolName),
		WithToolEventToolID(config.ToolID),
		WithToolEventNodeID(config.NodeID),
		WithToolEventPhase(ToolExecutionPhaseComplete),
		WithToolEventStartTime(config.StartTime),
		WithToolEventEndTime(endTime),
		WithToolEventInput(string(config.Arguments)),
		WithToolEventOutput(outputStr),
		WithToolEventError(config.Error),
	)

	select {
	case config.EventChan <- toolCompleteEvent:
	default:
	}
}

// extractToolCallbacks extracts tool callbacks from the state.
func extractToolCallbacks(state State) (*tool.Callbacks, bool) {
	if toolCallbacks, exists := state[StateKeyToolCallbacks]; exists {
		if callbacks, ok := toolCallbacks.(*tool.Callbacks); ok {
			return callbacks, true
		}
	}
	return nil, false
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

// buildAgentInvocation builds an invocation for the target agent.
func buildAgentInvocation(state State, targetAgent agent.Agent, agentCallbacks *agent.Callbacks) *agent.Invocation {
	// Extract user input from state.
	var userInput string
	if input, exists := state[StateKeyUserInput]; exists {
		if inputStr, ok := input.(string); ok {
			userInput = inputStr
		}
	}
	// Extract session from state.
	var sessionData *session.Session
	if sess, exists := state[StateKeySession]; exists {
		if sessData, ok := sess.(*session.Session); ok {
			sessionData = sessData
		}
	}
	// Extract execution context for invocation ID.
	var invocationID string
	if execCtx, exists := state[StateKeyExecContext]; exists {
		if execContext, ok := execCtx.(*ExecutionContext); ok {
			invocationID = execContext.InvocationID
		}
	}
	// Create the invocation.
	invocation := &agent.Invocation{
		Agent:          targetAgent,
		AgentName:      targetAgent.Info().Name,
		Message:        model.NewUserMessage(userInput),
		Session:        sessionData,
		InvocationID:   invocationID,
		AgentCallbacks: agentCallbacks,
	}
	return invocation
}

// emitAgentStartEvent emits an agent execution start event.
func emitAgentStartEvent(
	eventChan chan<- *event.Event,
	invocationID, nodeID string,
	startTime time.Time,
) {
	if eventChan == nil {
		return
	}

	agentStartEvent := NewNodeStartEvent(
		WithNodeEventInvocationID(invocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(NodeTypeAgent),
		WithNodeEventStartTime(startTime),
	)

	select {
	case eventChan <- agentStartEvent:
	default:
	}
}

// emitAgentCompleteEvent emits an agent execution complete event.
func emitAgentCompleteEvent(
	eventChan chan<- *event.Event,
	invocationID, agentName, nodeID string,
	startTime, endTime time.Time,
) {
	if eventChan == nil {
		return
	}

	agentCompleteEvent := NewNodeCompleteEvent(
		WithNodeEventInvocationID(invocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(NodeTypeAgent),
		WithNodeEventStartTime(startTime),
		WithNodeEventEndTime(endTime),
	)

	select {
	case eventChan <- agentCompleteEvent:
	default:
	}
}

// emitAgentErrorEvent emits an agent execution error event.
func emitAgentErrorEvent(
	eventChan chan<- *event.Event,
	invocationID, agentName, nodeID string,
	startTime, endTime time.Time,
	err error,
) {
	if eventChan == nil {
		return
	}

	agentErrorEvent := NewNodeErrorEvent(
		WithNodeEventInvocationID(invocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(NodeTypeAgent),
		WithNodeEventStartTime(startTime),
		WithNodeEventEndTime(endTime),
		WithNodeEventError(err.Error()),
	)

	select {
	case eventChan <- agentErrorEvent:
	default:
	}
}

// findSubAgentByName looks up a sub-agent by name from the parent agent.
func findSubAgentByName(parentAgent any, agentName string) agent.Agent {
	// Try to cast to an interface that has SubAgents method.
	type SubAgentProvider interface {
		FindSubAgent(name string) agent.Agent
	}
	if provider, ok := parentAgent.(SubAgentProvider); ok {
		return provider.FindSubAgent(agentName)
	}
	return nil
}
