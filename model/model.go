// Package model provides interfaces and implementations for working with LLMs.
package model

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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

	// FunctionCallingMode indicates how to handle function calls:
	// - "auto": Model decides whether to call a function
	// - "required": Force the model to call a function
	// - "none": Disable function calling
	FunctionCallingMode string `json:"function_calling_mode,omitempty"`
}

// DefaultOptions returns default generation options.
func DefaultOptions() GenerationOptions {
	return GenerationOptions{
		Temperature:         0.7,
		MaxTokens:           1024,
		TopP:                1.0,
		PresencePenalty:     0.0,
		FrequencyPenalty:    0.0,
		FunctionCallingMode: "auto",
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

	// GeminiContent contains Gemini-specific response data.
	GeminiContent *message.GeminiContent `json:"gemini_content,omitempty"`

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

	// SupportsFunctionCalling indicates if the model supports function calling API.
	SupportsFunctionCalling bool `json:"supports_function_calling,omitempty"`
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

	// SupportsToolCalls returns true if the model supports tool calls.
	SupportsToolCalls() bool
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
	// Deprecated: Use RegisterTools instead.
	SetTools(tools []ToolDefinition) error

	// RegisterTools registers tools with the model using the new schema-based API.
	RegisterTools(tools []*tool.ToolDefinition) error
}

// ToolRegistrar abstracts the registration of tools with a model.
type ToolRegistrar interface {
	// RegisterTools registers tools with the target.
	RegisterTools(tools []*tool.ToolDefinition) error
}

// BaseModel provides common functionality for model implementations.
type BaseModel struct {
	name            string
	provider        string
	defaultOptions  GenerationOptions
	toolCallSupport bool
}

// NewBaseModel creates a new BaseModel.
func NewBaseModel(name, provider string, options GenerationOptions) *BaseModel {
	return &BaseModel{
		name:           name,
		provider:       provider,
		defaultOptions: options,
	}
}

// Name returns the name of the model.
func (m *BaseModel) Name() string {
	return m.name
}

// Provider returns the provider of the model.
func (m *BaseModel) Provider() string {
	return m.provider
}

// MergeOptions merges default options with provided options.
func (m *BaseModel) MergeOptions(options GenerationOptions) GenerationOptions {
	// Start with default options
	merged := m.defaultOptions

	// Override if non-zero values are provided
	if options.Temperature > 0 {
		merged.Temperature = options.Temperature
	}

	if options.MaxTokens > 0 {
		merged.MaxTokens = options.MaxTokens
	}

	if options.TopP > 0 {
		merged.TopP = options.TopP
	}

	if options.TopK > 0 {
		merged.TopK = options.TopK
	}

	if options.PresencePenalty != 0 {
		merged.PresencePenalty = options.PresencePenalty
	}

	if options.FrequencyPenalty != 0 {
		merged.FrequencyPenalty = options.FrequencyPenalty
	}

	if len(options.StopSequences) > 0 {
		merged.StopSequences = options.StopSequences
	}

	// These boolean options just override
	merged.EnableToolCalls = options.EnableToolCalls

	// Function calling mode has a default of "auto"
	if options.FunctionCallingMode != "" {
		merged.FunctionCallingMode = options.FunctionCallingMode
	}

	return merged
}

// SupportsToolCalls returns true if the model supports tool calls.
func (m *BaseModel) SupportsToolCalls() bool {
	return m.toolCallSupport
}

// SetSupportsToolCalls sets whether the model supports tool calls.
func (m *BaseModel) SetSupportsToolCalls(supports bool) {
	m.toolCallSupport = supports
}

// GeminiCompatibleModel is the interface for models that support Google Gemini content format.
type GeminiCompatibleModel interface {
	Model

	// SupportsGeminiFormat indicates if the model supports Gemini content format.
	SupportsGeminiFormat() bool

	// GenerateWithGeminiMessages generates a response for messages in Gemini format.
	GenerateWithGeminiMessages(ctx context.Context, messages []*message.GeminiContent, options GenerationOptions) (Response, error)
}

// GeminiCompatibleStreamingModel is the interface for streaming models that support Google Gemini content format.
type GeminiCompatibleStreamingModel interface {
	StreamingModel

	// SupportsGeminiFormat indicates if the model supports Gemini content format.
	SupportsGeminiFormat() bool

	// GenerateStreamWithGeminiMessages generates a streaming response for messages in Gemini format.
	GenerateStreamWithGeminiMessages(ctx context.Context, messages []*message.GeminiContent, options GenerationOptions) (<-chan Response, error)
}

// MockModel is a simple implementation for testing purposes.
type MockModel struct {
	ResponseText string
}

// NewMockModel creates a new mock model.
func NewMockModel(responseText string) *MockModel {
	return &MockModel{
		ResponseText: responseText,
	}
}

// GenerateWithMessages generates a response for the given messages.
func (m *MockModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, options GenerationOptions) (Response, error) {
	// Just return the predefined response
	return Response{
		Text: m.ResponseText,
		Messages: []*message.Message{
			message.NewAssistantMessage(m.ResponseText),
		},
	}, nil
}

// GenerateWithText generates a response for the given text.
func (m *MockModel) GenerateWithText(ctx context.Context, text string, options GenerationOptions) (Response, error) {
	return m.GenerateWithMessages(ctx, []*message.Message{message.NewUserMessage(text)}, options)
}
