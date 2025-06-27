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
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *agent.AgentCallbacks
	// ModelCallbacks contains callbacks for model operations.
	ModelCallbacks *model.ModelCallbacks
}

// LLMAgent is an agent that uses a language model to generate responses.
// It implements the agent.Agent interface.
type LLMAgent struct {
	name           string
	model          model.Model
	description    string
	instruction    string
	systemPrompt   string
	genConfig      model.GenerationConfig
	flow           flow.Flow
	tools          []tool.Tool // Tools supported by the agent
	agentCallbacks *agent.AgentCallbacks
	modelCallbacks *model.ModelCallbacks
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
		name:           name,
		model:          opts.Model,
		description:    opts.Description,
		instruction:    opts.Instruction,
		systemPrompt:   opts.SystemPrompt,
		genConfig:      opts.GenerationConfig,
		flow:           llmFlow,
		tools:          opts.Tools,
		modelCallbacks: opts.ModelCallbacks,
		agentCallbacks: opts.AgentCallbacks,
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

	// Set agent callbacks if available.
	if invocation.AgentCallbacks == nil && a.agentCallbacks != nil {
		invocation.AgentCallbacks = a.agentCallbacks
	}

	// Set model callbacks if available.
	if invocation.ModelCallbacks == nil && a.modelCallbacks != nil {
		invocation.ModelCallbacks = a.modelCallbacks
	}

	// Run before agent callbacks if they exist.
	if invocation.AgentCallbacks != nil {
		customResponse, skip, err := invocation.AgentCallbacks.RunBeforeAgent(ctx, invocation)
		if err != nil {
			return nil, err
		}
		if customResponse != nil || skip {
			// Create a channel that returns the custom response and then closes.
			eventChan := make(chan *event.Event, 1)
			if customResponse != nil {
				// Create an event from the custom response.
				customEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
				eventChan <- customEvent
			}
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
			customResponse, override, err := invocation.AgentCallbacks.RunAfterAgent(ctx, invocation, nil)
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
			if customResponse != nil && override {
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

// Tools implements the agent.Agent interface.
// It returns the list of tools available to the agent.
func (a *LLMAgent) Tools() []tool.Tool {
	return a.tools
}
