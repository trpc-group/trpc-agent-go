// Package llmagent provides an LLM agent implementation.
package llmagent

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
	"trpc.group/trpc-go/trpc-agent-go/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

// Option is a function that configures an LLMAgent.
type Option func(*Options)

// WithModel sets the model to use.
func WithModel(model model.Model) Option {
	return func(opts *Options) {
		opts.Model = model
	}
}

// WithDescription sets the description of the agent.
func WithDescription(description string) Option {
	return func(opts *Options) {
		opts.Description = description
	}
}

// WithInstruction sets the instruction of the agent.
func WithInstruction(instruction string) Option {
	return func(opts *Options) {
		opts.Instruction = instruction
	}
}

// WithGlobalInstruction sets the global instruction of the agent.
func WithGlobalInstruction(instruction string) Option {
	return func(opts *Options) {
		opts.GlobalInstruction = instruction
	}
}

// WithGenerationConfig sets the generation configuration.
func WithGenerationConfig(config model.GenerationConfig) Option {
	return func(opts *Options) {
		opts.GenerationConfig = config
	}
}

// WithChannelBufferSize sets the buffer size for event channels.
func WithChannelBufferSize(size int) Option {
	return func(opts *Options) {
		opts.ChannelBufferSize = size
	}
}

// WithTools sets the list of tools available to the agent.
func WithTools(tools []tool.Tool) Option {
	return func(opts *Options) {
		opts.Tools = tools
	}
}

// WithToolSets sets the list of tool sets available to the agent.
func WithToolSets(toolSets []tool.ToolSet) Option {
	return func(opts *Options) {
		opts.ToolSets = toolSets
	}
}

// WithPlanner sets the planner to use for planning instructions.
func WithPlanner(planner planner.Planner) Option {
	return func(opts *Options) {
		opts.Planner = planner
	}
}

// WithSubAgents sets the list of sub-agents available to the agent.
func WithSubAgents(subAgents []agent.Agent) Option {
	return func(opts *Options) {
		opts.SubAgents = subAgents
	}
}

// WithAgentCallbacks sets the agent callbacks.
func WithAgentCallbacks(callbacks *agent.AgentCallbacks) Option {
	return func(opts *Options) {
		opts.AgentCallbacks = callbacks
	}
}

// WithModelCallbacks sets the model callbacks.
func WithModelCallbacks(callbacks *model.ModelCallbacks) Option {
	return func(opts *Options) {
		opts.ModelCallbacks = callbacks
	}
}

// WithToolCallbacks sets the tool callbacks.
func WithToolCallbacks(callbacks *tool.ToolCallbacks) Option {
	return func(opts *Options) {
		opts.ToolCallbacks = callbacks
	}
}

// WithKnowledge sets the knowledge base for the agent.
// If provided, the knowledge search tool will be automatically added to the agent's tools.
func WithKnowledge(kb knowledge.Knowledge) Option {
	return func(opts *Options) {
		opts.Knowledge = kb
	}
}

// Options contains configuration options for creating an LLMAgent.
type Options struct {
	// Name is the name of the agent.
	Name string
	// Model is the model to use for generating responses.
	Model model.Model
	// Description is a description of the agent.
	Description string
	// Instruction is the instruction for the agent.
	Instruction string
	// GlobalInstruction is the global instruction for the agent.
	// It will be used for all agents in the agent tree.
	GlobalInstruction string
	// GenerationConfig contains the generation configuration.
	GenerationConfig model.GenerationConfig
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	// Tools is the list of tools available to the agent.
	Tools []tool.Tool
	// ToolSets is the list of tool sets available to the agent.
	ToolSets []tool.ToolSet
	// Planner is the planner to use for planning instructions.
	Planner planner.Planner
	// SubAgents is the list of sub-agents available to the agent.
	SubAgents []agent.Agent
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *agent.AgentCallbacks
	// ModelCallbacks contains callbacks for model operations.
	ModelCallbacks *model.ModelCallbacks
	// ToolCallbacks contains callbacks for tool operations.
	ToolCallbacks *tool.ToolCallbacks
	// Knowledge is the knowledge base for the agent.
	// If provided, the knowledge search tool will be automatically added.
	Knowledge knowledge.Knowledge
}

// LLMAgent is an agent that uses an LLM to generate responses.
type LLMAgent struct {
	name           string
	model          model.Model
	description    string
	instruction    string
	systemPrompt   string
	genConfig      model.GenerationConfig
	flow           flow.Flow
	tools          []tool.Tool // Tools supported by the agent
	planner        planner.Planner
	subAgents      []agent.Agent // Sub-agents that can be delegated to
	agentCallbacks *agent.AgentCallbacks
	modelCallbacks *model.ModelCallbacks
	toolCallbacks  *tool.ToolCallbacks
}

// New creates a new LLMAgent with the given options.
func New(name string, opts ...Option) *LLMAgent {
	var options Options

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	// Prepare request processors in the correct order.
	var requestProcessors []flow.RequestProcessor

	// 1. Basic processor - handles generation config.
	basicOptions := []processor.BasicOption{
		processor.WithGenerationConfig(options.GenerationConfig),
	}
	basicProcessor := processor.NewBasicRequestProcessor(basicOptions...)
	requestProcessors = append(requestProcessors, basicProcessor)

	// 2. Planning processor - handles planning instructions if planner is configured.
	if options.Planner != nil {
		planningProcessor := processor.NewPlanningRequestProcessor(options.Planner)
		requestProcessors = append(requestProcessors, planningProcessor)
	}

	// 3. Instruction processor - adds instruction content and system prompt.
	if options.Instruction != "" || options.GlobalInstruction != "" {
		instructionProcessor := processor.NewInstructionRequestProcessor(options.Instruction, options.GlobalInstruction)
		requestProcessors = append(requestProcessors, instructionProcessor)
	}

	// 4. Identity processor - sets agent identity.
	if name != "" || options.Description != "" {
		identityProcessor := processor.NewIdentityRequestProcessor(name, options.Description)
		requestProcessors = append(requestProcessors, identityProcessor)
	}

	// 5. Content processor - handles messages from invocation.
	contentProcessor := processor.NewContentRequestProcessor()
	requestProcessors = append(requestProcessors, contentProcessor)

	// Prepare response processors.
	var responseProcessors []flow.ResponseProcessor

	// Add planning response processor if planner is configured.
	if options.Planner != nil {
		planningResponseProcessor := processor.NewPlanningResponseProcessor(options.Planner)
		responseProcessors = append(responseProcessors, planningResponseProcessor)
	}

	// Add transfer response processor if sub-agents are configured.
	if len(options.SubAgents) > 0 {
		transferResponseProcessor := processor.NewTransferResponseProcessor()
		responseProcessors = append(responseProcessors, transferResponseProcessor)
	}

	// Create flow with the provided processors and options.
	flowOpts := llmflow.Options{
		ChannelBufferSize: options.ChannelBufferSize,
	}

	llmFlow := llmflow.New(
		requestProcessors, responseProcessors,
		flowOpts,
	)

	// Register tools from both tools and toolsets, including knowledge search tool if provided.
	tools := registerTools(options.Tools, options.ToolSets, options.Knowledge)

	return &LLMAgent{
		name:           name,
		model:          options.Model,
		description:    options.Description,
		instruction:    options.Instruction,
		systemPrompt:   options.GlobalInstruction,
		genConfig:      options.GenerationConfig,
		flow:           llmFlow,
		tools:          tools,
		planner:        options.Planner,
		subAgents:      options.SubAgents,
		agentCallbacks: options.AgentCallbacks,
		modelCallbacks: options.ModelCallbacks,
		toolCallbacks:  options.ToolCallbacks,
	}
}

func registerTools(tools []tool.Tool, toolSets []tool.ToolSet, kb knowledge.Knowledge) []tool.Tool {
	// Start with direct tools.
	allTools := make([]tool.Tool, 0, len(tools))
	allTools = append(allTools, tools...)

	// Add tools from each toolset.
	ctx := context.Background()
	for _, toolSet := range toolSets {
		setTools := toolSet.Tools(ctx)
		for _, t := range setTools {
			allTools = append(allTools, t)
		}
	}

	// Add knowledge search tool if knowledge base is provided.
	if kb != nil {
		allTools = append(allTools, knowledgetool.NewKnowledgeSearchTool(kb))
	}

	return allTools
}

// Run implements the agent.Agent interface.
// It executes the LLM agent flow and returns a channel of events.
func (a *LLMAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Ensure the invocation can be accessed by downstream components (e.g., tools)
	// by embedding it into the context. This is necessary for tools like
	// transfer_to_agent that rely on agent.InvocationFromContext(ctx).
	ctx = agent.NewContextWithInvocation(ctx, invocation)
	ctx, span := telemetry.Tracer.Start(ctx, fmt.Sprintf("agent_run [%s]", a.name))
	defer span.End()
	// Ensure the invocation has a model set.
	if invocation.Model == nil && a.model != nil {
		invocation.Model = a.model
	}

	// Ensure the agent name is set.
	if invocation.AgentName == "" {
		invocation.AgentName = a.name
	}

	// Set agent callbacks if available.
	if invocation.AgentCallbacks == nil && a.agentCallbacks != nil {
		invocation.AgentCallbacks = a.agentCallbacks
	}

	// Set model callbacks if available.
	if invocation.ModelCallbacks == nil && a.modelCallbacks != nil {
		invocation.ModelCallbacks = a.modelCallbacks
	}

	// Set tool callbacks if available.
	if invocation.ToolCallbacks == nil && a.toolCallbacks != nil {
		invocation.ToolCallbacks = a.toolCallbacks
	}

	// Run before agent callbacks if they exist.
	if invocation.AgentCallbacks != nil {
		customResponse, err := invocation.AgentCallbacks.RunBeforeAgent(ctx, invocation)
		if err != nil {
			return nil, fmt.Errorf("before agent callback failed: %w", err)
		}
		if customResponse != nil {
			// Create a channel that returns the custom response and then closes.
			eventChan := make(chan *event.Event, 1)
			// Create an event from the custom response.
			customEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
			eventChan <- customEvent
			close(eventChan)
			return eventChan, nil
		}
	}

	// Use the underlying flow to execute the agent logic.
	flowEventChan, err := a.flow.Run(ctx, invocation)
	if err != nil {
		return nil, err
	}

	// If we have after agent callbacks, we need to wrap the event channel.
	if invocation.AgentCallbacks != nil {
		return a.wrapEventChannel(ctx, invocation, flowEventChan), nil
	}

	return flowEventChan, nil
}

// wrapEventChannel wraps the event channel to apply after agent callbacks.
func (a *LLMAgent) wrapEventChannel(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
) <-chan *event.Event {
	wrappedChan := make(chan *event.Event, 256) // Use default buffer size

	go func() {
		defer close(wrappedChan)

		// Forward all events from the original channel
		for evt := range originalChan {
			select {
			case wrappedChan <- evt:
			case <-ctx.Done():
				return
			}
		}

		// After all events are processed, run after agent callbacks
		if invocation.AgentCallbacks != nil {
			customResponse, err := invocation.AgentCallbacks.RunAfterAgent(ctx, invocation, nil)
			if err != nil {
				// Send error event.
				errorEvent := event.NewErrorEvent(
					invocation.InvocationID,
					invocation.AgentName,
					"agent_callback_error",
					err.Error(),
				)
				select {
				case wrappedChan <- errorEvent:
				case <-ctx.Done():
					return
				}
				return
			}
			if customResponse != nil {
				// Create an event from the custom response.
				customEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
				select {
				case wrappedChan <- customEvent:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return wrappedChan
}

// Info implements the agent.Agent interface.
// It returns the basic information about this agent.
func (a *LLMAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: a.description,
	}
}

// Tools implements the agent.Agent interface.
// It returns the list of tools available to the agent, including transfer tools.
func (a *LLMAgent) Tools() []tool.Tool {
	if len(a.subAgents) == 0 {
		return a.tools
	}

	// Create agent info for sub-agents.
	agentInfos := make([]agent.Info, len(a.subAgents))
	for i, subAgent := range a.subAgents {
		agentInfos[i] = subAgent.Info()
	}

	transferTool := transfer.New(agentInfos)
	return append(a.tools, transferTool)
}

// SubAgents returns the list of sub-agents for this agent.
func (a *LLMAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

// FindSubAgent finds a sub-agent by name.
// Returns nil if no sub-agent with the given name is found.
func (a *LLMAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range a.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}
