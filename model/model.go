// Package model provides interfaces and implementations for working with LLMs.
package model

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/message"
)

// GenerationOptions contains options for model generation.
type GenerationOptions struct {
	// Temperature controls randomness. Higher values (e.g., 0.8) make output more random,
	// lower values (e.g., 0.2) make it more deterministic.
	Temperature float32 `json:"temperature,omitempty"`

	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens int `json:"max_tokens,omitempty"`

	// TopP controls diversity via nucleus sampling. 0.5 means half of all likelihood-weighted options are considered.
	TopP float32 `json:"top_p,omitempty"`

	// TopK controls diversity by limiting to the top k most likely tokens.
	TopK int `json:"top_k,omitempty"`

	// PresencePenalty penalizes new tokens based on whether they appear in the text so far.
	PresencePenalty float32 `json:"presence_penalty,omitempty"`

	// FrequencyPenalty penalizes new tokens based on their frequency in the text so far.
	FrequencyPenalty float32 `json:"frequency_penalty,omitempty"`

	// StopSequences are sequences of text that will stop generation if encountered.
	StopSequences []string `json:"stop_sequences,omitempty"`

	// EnableToolCalls indicates whether tool calls should be enabled for this request.
	EnableToolCalls bool `json:"enable_tool_calls,omitempty"`
}

// DefaultOptions returns default generation options.
func DefaultOptions() GenerationOptions {
	return GenerationOptions{
		Temperature:      0.7,
		MaxTokens:        1024,
		TopP:             1.0,
		PresencePenalty:  0.0,
		FrequencyPenalty: 0.0,
	}
}

// ToolCall represents a call to a tool by the model.
type ToolCall struct {
	// ID is a unique identifier for the tool call.
	ID string `json:"id"`

	// Type is the type of tool call (e.g., "function").
	Type string `json:"type"`

	// Function contains the details of the function call.
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a call to a function by the model.
type FunctionCall struct {
	// Name is the name of the function.
	Name string `json:"name"`

	// Arguments is a JSON string containing the arguments to the function.
	Arguments string `json:"arguments"`
}

// Response represents a response from a model.
type Response struct {
	// Text is the generated text.
	Text string `json:"text"`

	// Messages is a list of message responses (for chat models).
	Messages []*message.Message `json:"messages,omitempty"`

	// ToolCalls is a list of tool calls in the response.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// FinishReason indicates why the model stopped generating, e.g., "stop", "length", etc.
	FinishReason string `json:"finish_reason,omitempty"`

	// Usage contains token usage information.
	Usage *Usage `json:"usage,omitempty"`
}

// Usage contains token usage information.
type Usage struct {
	// PromptTokens is the number of tokens used in the prompt.
	PromptTokens int `json:"prompt_tokens"`

	// CompletionTokens is the number of tokens used in the completion.
	CompletionTokens int `json:"completion_tokens"`

	// TotalTokens is the total number of tokens used.
	TotalTokens int `json:"total_tokens"`
}

// ToolDefinition represents a tool that can be called by the model.
type ToolDefinition struct {
	// Name is the name of the tool.
	Name string `json:"name"`

	// Description is a description of what the tool does.
	Description string `json:"description"`

	// Parameters is a JSON Schema object describing the parameters of the tool.
	Parameters map[string]interface{} `json:"parameters"`
}

// ModelConfig contains configuration for a model.
type ModelConfig struct {
	// Name is the model name (e.g., "gpt-4", "gemini-pro").
	Name string `json:"name"`

	// Provider is the model provider (e.g., "openai", "google").
	Provider string `json:"provider"`

	// APIKey is the API key for the model provider.
	APIKey string `json:"api_key,omitempty"`

	// BaseURL is the base URL for the model provider API.
	BaseURL string `json:"base_url,omitempty"`

	// DefaultOptions are the default generation options for this model.
	DefaultOptions GenerationOptions `json:"default_options,omitempty"`
}

// Model is the interface for all language models.
type Model interface {
	// Name returns the name of the model.
	Name() string

	// Provider returns the provider of the model.
	Provider() string

	// Generate generates a completion for the given prompt.
	Generate(ctx context.Context, prompt string, options GenerationOptions) (*Response, error)

	// GenerateWithMessages generates a completion for the given messages.
	GenerateWithMessages(ctx context.Context, messages []*message.Message, options GenerationOptions) (*Response, error)
}

// StreamingModel is the interface for models that support streaming.
type StreamingModel interface {
	Model

	// GenerateStream streams a completion for the given prompt.
	GenerateStream(ctx context.Context, prompt string, options GenerationOptions) (<-chan *Response, error)

	// GenerateStreamWithMessages streams a completion for the given messages.
	GenerateStreamWithMessages(ctx context.Context, messages []*message.Message, options GenerationOptions) (<-chan *Response, error)
}

// ToolCallSupportingModel is the interface for models that support tool calls.
type ToolCallSupportingModel interface {
	Model

	// SetTools sets the tools available to the model.
	SetTools(tools []ToolDefinition) error
}
