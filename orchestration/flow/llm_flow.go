// Package llmflow provides flows for working with language models.
package flow

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

// LLMFlow is a flow that processes messages using a language model.
// It takes a message, passes it to a model, and returns the model's response.
type LLMFlow struct {
	BaseFlow
	model          model.Model
	streamingModel model.StreamingModel
	streaming      bool
	systemMessage  string
}

// LLMFlowOption is a functional option for configuring an LLMFlow.
type LLMFlowOption func(*LLMFlow)

// WithSystemMessage sets a system message to prepend to the model input.
func WithSystemMessage(systemMsg string) LLMFlowOption {
	return func(f *LLMFlow) {
		f.systemMessage = systemMsg
	}
}

// WithStreaming configures the flow to use streaming mode if available.
func WithStreaming(streaming bool) LLMFlowOption {
	return func(f *LLMFlow) {
		f.streaming = streaming
	}
}

// NewLLMFlow creates a new LLMFlow with the given name, description, and model.
func NewLLMFlow(name, description string, mdl model.Model, options ...LLMFlowOption) *LLMFlow {
	// Check if the model supports streaming
	var streamingModel model.StreamingModel
	if sm, ok := mdl.(model.StreamingModel); ok {
		streamingModel = sm
	}

	flow := &LLMFlow{
		BaseFlow:       *NewBaseFlow(name, description),
		model:          mdl,
		streamingModel: streamingModel,
		streaming:      false, // default to non-streaming
	}

	// Apply options
	for _, option := range options {
		option(flow)
	}

	return flow
}

// Run processes a message with the language model and returns the response.
func (f *LLMFlow) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	// For now, we'll just return a simple response
	// This is a placeholder until we have actual model implementations
	resp := message.NewAssistantMessage(fmt.Sprintf("Response to: %s", msg.Content))
	return resp, nil
}
