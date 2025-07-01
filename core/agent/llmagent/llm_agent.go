package llmagent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/core/tool/transfer"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/planner"
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

// WithSystemPrompt sets the system prompt of the agent.
func WithSystemPrompt(systemPrompt string) Option {
	return func(opts *Options) {
		opts.SystemPrompt = systemPrompt
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
	// SystemPrompt is the system prompt for the agent.
	SystemPrompt string
	// GenerationConfig contains the generation configuration.
	GenerationConfig model.GenerationConfig
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	// Tools is the list of tools available to the agent.
	Tools []tool.Tool
	// Planner is the planner to use for planning instructions.
	Planner planner.Planner
	// SubAgents is the list of sub-agents available to the agent.
	SubAgents []agent.Agent
}

// LLMAgent is an agent that uses an LLM to generate responses.
type LLMAgent struct {
	name         string
	model        model.Model
	description  string
	instruction  string
	systemPrompt string
	genConfig    model.GenerationConfig
	flow         flow.Flow
	tools        []tool.Tool // Tools supported by the agent
	planner      planner.Planner
	subAgents    []agent.Agent // Sub-agents that can be delegated to
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
	if options.Instruction != "" || options.SystemPrompt != "" {
		instructionProcessor := processor.NewInstructionRequestProcessor(options.Instruction, options.SystemPrompt)
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

	return &LLMAgent{
		name:         name,
		model:        options.Model,
		description:  options.Description,
		instruction:  options.Instruction,
		systemPrompt: options.SystemPrompt,
		genConfig:    options.GenerationConfig,
		flow:         llmFlow,
		tools:        options.Tools,
		planner:      options.Planner,
		subAgents:    options.SubAgents,
	}
}

// Run implements the agent.Agent interface.
// It executes the LLM agent flow and returns a channel of events.
func (a *LLMAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Ensure the invocation has a model set.
	if invocation.Model == nil && a.model != nil {
		invocation.Model = a.model
	}

	// Ensure the agent name is set.
	if invocation.AgentName == "" {
		invocation.AgentName = a.name
	}

	// Use the underlying flow to execute the agent logic.
	return a.flow.Run(ctx, invocation)
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
