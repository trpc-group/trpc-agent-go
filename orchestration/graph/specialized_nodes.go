package graph

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// ModelNode is a node that processes messages using a model.
type ModelNode struct {
	model       model.Model
	name        string
	description string
	prompt      string
	system      string
	options     model.GenerationOptions
}

// ModelNodeOption is a function that configures a ModelNode.
type ModelNodeOption func(*ModelNode)

// WithPrompt sets the prompt for the model node.
func WithPrompt(prompt string) ModelNodeOption {
	return func(n *ModelNode) {
		n.prompt = prompt
	}
}

// WithSystem sets the system message for the model node.
func WithSystem(system string) ModelNodeOption {
	return func(n *ModelNode) {
		n.system = system
	}
}

// WithOptions sets the generation options for the model node.
func WithOptions(options model.GenerationOptions) ModelNodeOption {
	return func(n *ModelNode) {
		n.options = options
	}
}

// NewModelNode creates a new model node.
func NewModelNode(m model.Model, name, description string, opts ...ModelNodeOption) *ModelNode {
	node := &ModelNode{
		model:       m,
		name:        name,
		description: description,
		options:     model.DefaultOptions(),
	}

	for _, opt := range opts {
		opt(node)
	}

	return node
}

// Process implements the Node interface.
func (n *ModelNode) Process(ctx context.Context, input *message.Message) (*message.Message, error) {
	// Create a conversation
	messages := []*message.Message{}

	// Add system message if provided
	if n.system != "" {
		messages = append(messages, message.NewSystemMessage(n.system))
	}

	// Add the input message
	messages = append(messages, input)

	// Add prompt if provided
	if n.prompt != "" {
		messages = append(messages, message.NewUserMessage(n.prompt))
	}

	// Generate a response
	response, err := n.model.GenerateWithMessages(ctx, messages, n.options)
	if err != nil {
		return nil, fmt.Errorf("model generation failed: %w", err)
	}

	// Get the response message
	if len(response.Messages) > 0 {
		return response.Messages[0], nil
	}

	// Create a new message from the response text if no messages were returned
	return message.NewAssistantMessage(response.Text), nil
}

// ProcessStream implements the Node interface.
func (n *ModelNode) ProcessStream(ctx context.Context, input *message.Message) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 10)

	// Check if model supports streaming
	streamingModel, ok := n.model.(model.StreamingModel)
	if !ok {
		// If model doesn't support streaming, use regular process and simulate streaming
		go func() {
			defer close(eventCh)

			// Signal stream start
			eventCh <- event.NewStreamStartEvent("")

			// Process normally
			result, err := n.Process(ctx, input)
			if err != nil {
				eventCh <- event.NewErrorEvent(err, 500)
				return
			}

			// Send the result as a message event
			eventCh <- event.NewMessageEvent(result)

			// Signal stream end
			eventCh <- event.NewStreamEndEvent(result.Content)
		}()

		return eventCh, nil
	}

	// Create a conversation
	messages := []*message.Message{}

	// Add system message if provided
	if n.system != "" {
		messages = append(messages, message.NewSystemMessage(n.system))
	}

	// Add the input message
	messages = append(messages, input)

	// Add prompt if provided
	if n.prompt != "" {
		messages = append(messages, message.NewUserMessage(n.prompt))
	}

	// Start streaming response generation
	responseCh, err := streamingModel.GenerateStreamWithMessages(ctx, messages, n.options)
	if err != nil {
		return nil, fmt.Errorf("streaming model generation failed: %w", err)
	}

	// Stream the responses as events
	go func() {
		defer close(eventCh)

		// Signal stream start
		eventCh <- event.NewStreamStartEvent("")

		var lastMsg *message.Message
		var sequence int

		for resp := range responseCh {
			// Create a message from the response
			var msg *message.Message
			if len(resp.Messages) > 0 {
				msg = resp.Messages[0]
			} else {
				msg = message.NewAssistantMessage(resp.Text)
			}

			if lastMsg == nil {
				eventCh <- event.NewMessageEvent(msg)
			}

			delta := msg.Content
			if delta != "" {
				eventCh <- event.NewStreamChunkEvent(delta, sequence)
				sequence++
			}

			lastMsg = msg

			// Check for tool calls
			for _, toolCall := range resp.ToolCalls {
				eventCh <- event.NewStreamToolCallEvent(
					toolCall.Function.Name,
					toolCall.Function.Arguments,
					toolCall.ID,
				)
			}
		}

		// Signal stream end
		if lastMsg != nil {
			eventCh <- event.NewStreamEndEvent(lastMsg.Content)
		} else {
			eventCh <- event.NewStreamEndEvent("")
		}
	}()

	return eventCh, nil
}

// SupportsStreaming implements the Node interface.
func (n *ModelNode) SupportsStreaming() bool {
	// Always return true - we'll automatically handle streaming or non-streaming models
	return true
}

// Info implements the Node interface.
func (n *ModelNode) Info() NodeInfo {
	return NodeInfo{
		Name:        n.name,
		Description: n.description,
		Type:        "model",
	}
}

// ToolNode is a node that processes messages using a tool.
type ToolNode struct {
	tool        tool.Tool
	name        string
	description string
	argsBuilder func(ctx context.Context, msg *message.Message) (map[string]interface{}, error)
}

// NewToolNode creates a new tool node.
func NewToolNode(tool tool.Tool, name, description string, argsBuilder func(ctx context.Context, msg *message.Message) (map[string]interface{}, error)) *ToolNode {
	if name == "" {
		name = tool.Name()
	}

	if description == "" {
		description = tool.Description()
	}

	return &ToolNode{
		tool:        tool,
		name:        name,
		description: description,
		argsBuilder: argsBuilder,
	}
}

// Process implements the Node interface.
func (n *ToolNode) Process(ctx context.Context, input *message.Message) (*message.Message, error) {
	// Build arguments for the tool
	var args map[string]interface{}
	var err error

	if n.argsBuilder != nil {
		args, err = n.argsBuilder(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to build tool arguments: %w", err)
		}
	} else {
		// Default implementation: try to parse from message content
		// Assuming the message content is a JSON string that can be parsed into a map
		// This is a simple default - in practice, you'd want more robust parsing
		args = make(map[string]interface{})
	}

	// Execute the tool
	result, err := n.tool.Execute(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("tool execution failed: %w", err)
	}

	// Create a response message
	response := message.NewToolMessage(result.String())

	// Add the result to the metadata
	response.SetMetadata("tool_name", n.tool.Name())
	response.SetMetadata("tool_result", result.Output)

	return response, nil
}

// ProcessStream implements the Node interface.
func (n *ToolNode) ProcessStream(ctx context.Context, input *message.Message) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 10)

	go func() {
		defer close(eventCh)

		// Signal stream start
		eventCh <- event.NewStreamStartEvent("")

		// Build arguments for the tool
		var args map[string]interface{}
		var err error

		if n.argsBuilder != nil {
			args, err = n.argsBuilder(ctx, input)
			if err != nil {
				eventCh <- event.NewErrorEvent(fmt.Errorf("failed to build tool arguments: %w", err), 500)
				return
			}
		} else {
			// Default implementation: try to parse from message content
			args = make(map[string]interface{})
		}

		// Emit tool execution start event
		eventCh <- event.NewCustomEvent("tool_execution_start", map[string]interface{}{
			"tool_name": n.tool.Name(),
			"arguments": args,
		})

		// Execute the tool
		result, err := n.tool.Execute(ctx, args)
		if err != nil {
			eventCh <- event.NewErrorEvent(fmt.Errorf("tool execution failed: %w", err), 500)
			return
		}

		// Create a response message
		response := message.NewToolMessage(result.String())

		// Add the result to the metadata
		response.SetMetadata("tool_name", n.tool.Name())
		response.SetMetadata("tool_result", result.Output)

		// Emit the message event
		eventCh <- event.NewMessageEvent(response)

		// Signal stream end
		eventCh <- event.NewStreamEndEvent(response.Content)
	}()

	return eventCh, nil
}

// SupportsStreaming implements the Node interface.
func (n *ToolNode) SupportsStreaming() bool {
	return true
}

// Info implements the Node interface.
func (n *ToolNode) Info() NodeInfo {
	return NodeInfo{
		Name:        n.name,
		Description: n.description,
		Type:        "tool",
	}
}

// AgentNode is a node that implements a complete agent pattern.
type AgentNode struct {
	name        string
	description string
	model       model.Model
	tools       *tool.ToolSet
	system      string
	maxTurns    int
}

// NewAgentNode creates a new agent node.
func NewAgentNode(m model.Model, tools *tool.ToolSet, name, description, system string, maxTurns int) *AgentNode {
	if maxTurns <= 0 {
		maxTurns = 10 // Default to 10 turns
	}

	return &AgentNode{
		name:        name,
		description: description,
		model:       m,
		tools:       tools,
		system:      system,
		maxTurns:    maxTurns,
	}
}

// Process implements the Node interface.
func (n *AgentNode) Process(ctx context.Context, input *message.Message) (*message.Message, error) {
	// Build the agent graph
	graph := n.buildAgentGraph()

	// Create a runner
	runner, err := NewGraphRunner(graph)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent graph runner: %w", err)
	}

	// Execute the graph
	return runner.Execute(ctx, input)
}

// ProcessStream implements the Node interface.
func (n *AgentNode) ProcessStream(ctx context.Context, input *message.Message) (<-chan *event.Event, error) {
	// Build the agent graph
	graph := n.buildAgentGraph()

	// Create a runner
	runner, err := NewGraphRunner(graph)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent graph runner: %w", err)
	}

	// Execute the graph with streaming
	return runner.streamGraph(ctx, input)
}

// SupportsStreaming implements the Node interface.
func (n *AgentNode) SupportsStreaming() bool {
	return true
}

// Info implements the Node interface.
func (n *AgentNode) Info() NodeInfo {
	return NodeInfo{
		Name:        n.name,
		Description: n.description,
		Type:        "agent",
	}
}

// buildAgentGraph builds the graph for the agent.
func (n *AgentNode) buildAgentGraph() *Graph {
	graph := NewGraph(n.name, n.description)

	// Add nodes for the agent loop
	graph.AddNode("start", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Initialize conversation
		conversationMsgs := []*message.Message{}

		// Add system message
		if n.system != "" {
			conversationMsgs = append(conversationMsgs, message.NewSystemMessage(n.system))
		}

		// Add tool descriptions
		toolsDesc := "You have access to the following tools:\n\n"
		for _, t := range n.tools.List() {
			toolsDesc += fmt.Sprintf("- %s: %s\n", t.Name(), t.Description())
		}

		conversationMsgs = append(conversationMsgs, message.NewSystemMessage(toolsDesc))

		// Add user input
		conversationMsgs = append(conversationMsgs, input)

		// Store conversation in metadata
		input.SetMetadata("agent_conversation", conversationMsgs)
		input.SetMetadata("agent_turn", 0)

		return input, nil
	}).WithInfo("start", "Initializes the agent conversation"))

	graph.AddNode("check_turns", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get current turn
		turnVal, _ := input.GetMetadata("agent_turn")
		turn, _ := turnVal.(int)

		// Check if we've reached max turns
		if turn >= n.maxTurns {
			input.SetMetadata("agent_exit_reason", "max_turns_reached")
		}

		return input, nil
	}).WithInfo("check_turns", "Checks if the agent has reached the maximum number of turns"))

	graph.AddNode("model_think", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get conversation
		convVal, _ := input.GetMetadata("agent_conversation")
		conversation, _ := convVal.([]*message.Message)

		// Generate a response
		response, err := n.model.GenerateWithMessages(ctx, conversation, model.DefaultOptions())
		if err != nil {
			return nil, fmt.Errorf("model generation failed: %w", err)
		}

		// Get response message
		var responseMsg *message.Message
		if len(response.Messages) > 0 {
			responseMsg = response.Messages[0]
		} else {
			responseMsg = message.NewAssistantMessage(response.Text)
		}

		// Add response to conversation
		conversation = append(conversation, responseMsg)

		// Update conversation in metadata
		input.SetMetadata("agent_conversation", conversation)
		input.SetMetadata("agent_last_response", responseMsg)
		input.SetMetadata("agent_tool_calls", response.ToolCalls)

		return input, nil
	}).WithInfo("model_think", "Generates a response from the model"))

	graph.AddNode("check_tool_call", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get tool calls
		toolCallsVal, hasToolCalls := input.GetMetadata("agent_tool_calls")

		// Check if we have tool calls
		if hasToolCalls && toolCallsVal != nil {
			toolCalls, ok := toolCallsVal.([]model.ToolCall)
			if ok && len(toolCalls) > 0 {
				// Set needs tool flag
				input.SetMetadata("agent_needs_tool", true)
				input.SetMetadata("agent_tool_name", toolCalls[0].Function.Name)
				input.SetMetadata("agent_tool_args", toolCalls[0].Function.Arguments)
			}
		}

		return input, nil
	}).WithInfo("check_tool_call", "Checks if the model wants to call a tool"))

	graph.AddNode("call_tool", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get conversation
		convVal, _ := input.GetMetadata("agent_conversation")
		conversation, _ := convVal.([]*message.Message)

		// Get tool name and args - fix linter errors by properly handling the returned values
		toolNameVal, hasToolName := input.GetMetadata("agent_tool_name")
		toolArgsVal, hasToolArgs := input.GetMetadata("agent_tool_args")

		toolName := ""
		if hasToolName {
			if nameStr, ok := toolNameVal.(string); ok {
				toolName = nameStr
			}
		}

		toolArgs := make(map[string]interface{})
		if hasToolArgs {
			if argsStr, ok := toolArgsVal.(string); ok {
				// Parse JSON string to map
				if err := json.Unmarshal([]byte(argsStr), &toolArgs); err != nil {
					// Add error to conversation
					errMsg := message.NewToolMessage(fmt.Sprintf("Error parsing tool arguments: %v", err))
					conversation = append(conversation, errMsg)
					input.SetMetadata("agent_conversation", conversation)
					return input, nil
				}
			} else if argsMap, ok := toolArgsVal.(map[string]interface{}); ok {
				toolArgs = argsMap
			}
		}

		// Get the tool
		toolObj, ok := n.tools.Get(toolName)
		if !ok {
			// Add error to conversation
			errMsg := message.NewToolMessage(fmt.Sprintf("Error: Tool '%s' not found", toolName))
			conversation = append(conversation, errMsg)
			input.SetMetadata("agent_conversation", conversation)
			return input, nil
		}

		// Execute the tool
		result, err := toolObj.Execute(ctx, toolArgs)

		// Add result to conversation
		var resultMsg *message.Message
		if err != nil {
			resultMsg = message.NewToolMessage(fmt.Sprintf("Error: %v", err))
		} else {
			resultMsg = message.NewToolMessage(result.String())
		}

		conversation = append(conversation, resultMsg)
		input.SetMetadata("agent_conversation", conversation)

		return input, nil
	}).WithInfo("call_tool", "Calls a tool and adds the result to the conversation"))

	graph.AddNode("increment_turn", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get current turn
		turnVal, _ := input.GetMetadata("agent_turn")
		turn, _ := turnVal.(int)

		// Increment turn
		input.SetMetadata("agent_turn", turn+1)

		return input, nil
	}).WithInfo("increment_turn", "Increments the turn counter"))

	graph.AddNode("end", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get last response
		respVal, _ := input.GetMetadata("agent_last_response")
		response, _ := respVal.(*message.Message)

		// Return the final response
		return response, nil
	}).WithInfo("end", "Returns the final response"))

	// Connect nodes
	graph.AddEdge("start", "check_turns")

	graph.AddConditionalEdge("check_turns", "model_think", func(ctx context.Context, msg *message.Message) bool {
		_, exists := msg.GetMetadata("agent_exit_reason")
		return !exists
	})

	graph.AddEdge("model_think", "check_tool_call")

	graph.AddConditionalEdge("check_tool_call", "call_tool", func(ctx context.Context, msg *message.Message) bool {
		needsToolVal, hasNeedsTool := msg.GetMetadata("agent_needs_tool")
		if !hasNeedsTool {
			return false
		}
		needsTool, ok := needsToolVal.(bool)
		return ok && needsTool
	})

	graph.AddEdge("call_tool", "increment_turn")

	graph.AddConditionalEdge("check_tool_call", "increment_turn", func(ctx context.Context, msg *message.Message) bool {
		needsToolVal, hasNeedsTool := msg.GetMetadata("agent_needs_tool")
		if !hasNeedsTool {
			return true // Default to taking this path if metadata isn't set
		}
		needsTool, ok := needsToolVal.(bool)
		return !ok || !needsTool
	})

	graph.AddEdge("increment_turn", "check_turns")

	graph.AddConditionalEdge("check_turns", "end", func(ctx context.Context, msg *message.Message) bool {
		_, exists := msg.GetMetadata("agent_exit_reason")
		return exists
	})

	// Set start and end nodes
	graph.SetStartNode("start")
	graph.AddEndNode("end")

	return graph
}

// PromptNode is a node that adds a prompt to a message.
type PromptNode struct {
	name        string
	description string
	prompt      string
	role        message.Role
}

// NewPromptNode creates a new prompt node.
func NewPromptNode(prompt string, role message.Role, name, description string) *PromptNode {
	return &PromptNode{
		name:        name,
		description: description,
		prompt:      prompt,
		role:        role,
	}
}

// Process implements the Node interface.
func (n *PromptNode) Process(ctx context.Context, input *message.Message) (*message.Message, error) {
	return message.NewMessage(n.role, n.prompt), nil
}

// ProcessStream implements the Node interface.
func (n *PromptNode) ProcessStream(ctx context.Context, input *message.Message) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 10)

	go func() {
		defer close(eventCh)

		// Signal stream start
		eventCh <- event.NewStreamStartEvent("")

		// Create message
		response := message.NewMessage(n.role, n.prompt)

		// Emit the message event
		eventCh <- event.NewMessageEvent(response)

		// Signal stream end
		eventCh <- event.NewStreamEndEvent(response.Content)
	}()

	return eventCh, nil
}

// SupportsStreaming implements the Node interface.
func (n *PromptNode) SupportsStreaming() bool {
	return true
}

// Info implements the Node interface.
func (n *PromptNode) Info() NodeInfo {
	return NodeInfo{
		Name:        n.name,
		Description: n.description,
		Type:        "prompt",
	}
}
