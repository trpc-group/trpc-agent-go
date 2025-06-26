package llmagent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
)

// Options contains configuration options for creating an LLMAgent.
type Options struct {
	// Model is the model to use.
	Model model.Model
	// Description is the description of the agent.
	Description string
	// Instruction is the instruction of the agent.
	Instruction string
	// SystemPrompt is the system prompt of the agent.
	SystemPrompt string
	// GenerationConfig contains the generation configuration.
	GenerationConfig model.GenerationConfig
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	// Tools is the list of tools available to the agent.
	Tools []tool.Tool
}

// LLMAgent is an agent that uses a language model to generate responses.
// It implements the agent.Agent interface.
type LLMAgent struct {
	name         string
	model        model.Model
	description  string
	instruction  string
	systemPrompt string
	genConfig    model.GenerationConfig
	flow         flow.Flow
	tools        []tool.Tool // Tools supported by the agent
}

// New creates a new LLMAgent with the given options.
func New(
	name string,
	opts Options,
) *LLMAgent {
	// Prepare request processors in the correct order.
	var requestProcessors []flow.RequestProcessor

	// 1. Basic processor - handles generation config.
	basicOptions := []processor.BasicOption{
		processor.WithGenerationConfig(opts.GenerationConfig),
	}
	basicProcessor := processor.NewBasicRequestProcessor(basicOptions...)
	requestProcessors = append(requestProcessors, basicProcessor)

	// 2. Instruction processor - adds instruction content and system prompt.
	if opts.Instruction != "" || opts.SystemPrompt != "" {
		instructionProcessor := processor.NewInstructionRequestProcessor(opts.Instruction, opts.SystemPrompt)
		requestProcessors = append(requestProcessors, instructionProcessor)
	}

	// 3. Identity processor - sets agent identity.
	if name != "" || opts.Description != "" {
		identityProcessor := processor.NewIdentityRequestProcessor(name, opts.Description)
		requestProcessors = append(requestProcessors, identityProcessor)
	}

	// 4. Content processor - handles messages from invocation.
	contentProcessor := processor.NewContentRequestProcessor()
	requestProcessors = append(requestProcessors, contentProcessor)

	// Prepare response processors.
	responseProcessors := []flow.ResponseProcessor{}

	// Create flow with the provided processors and options.
	flowOpts := llmflow.Options{
		ChannelBufferSize: opts.ChannelBufferSize,
	}

	llmFlow := llmflow.New(
		requestProcessors, responseProcessors,
		flowOpts,
	)

	return &LLMAgent{
		name:         name,
		model:        opts.Model,
		description:  opts.Description,
		instruction:  opts.Instruction,
		systemPrompt: opts.SystemPrompt,
		genConfig:    opts.GenerationConfig,
		flow:         llmFlow,
		tools:        opts.Tools,
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

// Tools implements the agent.Agent interface.
// It returns the list of tools available to the agent.
func (a *LLMAgent) Tools() []tool.Tool {
	return a.tools
}
