package llmagent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
)

// Options contains configuration options for creating an LLMAgent.
type Options struct {
	// Name is the name of the agent.
	Name string
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	// RequestProcessors are the request processors to use.
	RequestProcessors []flow.RequestProcessor
	// ResponseProcessors are the response processors to use.
	ResponseProcessors []flow.ResponseProcessor
}

// LLMAgent is an agent that uses a language model to generate responses.
// It implements the agent.Agent interface.
type LLMAgent struct {
	name string
	flow flow.Flow
}

// New creates a new LLMAgent with the given options.
func New(opts Options) *LLMAgent {
	// Set default name if not provided.
	name := opts.Name
	if name == "" {
		name = "llm-agent"
	}

	// Create flow with the provided processors and options.
	flowOpts := llmflow.Options{
		ChannelBufferSize: opts.ChannelBufferSize,
	}

	llmFlow := llmflow.New(
		opts.RequestProcessors,
		opts.ResponseProcessors,
		flowOpts,
	)

	return &LLMAgent{
		name: name,
		flow: llmFlow,
	}
}

// Run implements the agent.Agent interface.
// It executes the LLM agent flow and returns a channel of events.
func (a *LLMAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Use the underlying flow to execute the agent logic.
	return a.flow.Run(ctx, invocation)
}

// Name returns the name of the agent.
func (a *LLMAgent) Name() string {
	return a.name
}
