//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package model

import "trpc.group/trpc-go/trpc-agent-go/tool"

// Role represents the role of a message author.
type Role string

// Role constants for message authors.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Thinking parameter keys used in API requests.
const (
	// ThinkingEnabledKey is the key used for enabling thinking mode in API requests.
	ThinkingEnabledKey = "thinking_enabled"
	// ThinkingTokensKey is the key used for thinking tokens configuration in API requests.
	ThinkingTokensKey = "thinking_tokens"
)

// String returns the string representation of the role.
func (r Role) String() string {
	return string(r)
}

// IsValid checks if the role is one of the defined constants.
func (r Role) IsValid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant:
		return true
	default:
		return false
	}
}

// Message represents a single message in a conversation.
type Message struct {
	Role      Role       `json:"role"`                 // The role of the message author
	Content   string     `json:"content"`              // The message content
	ToolID    string     `json:"tool_id,omitempty"`    // Used by tool response
	ToolCalls []ToolCall `json:"tool_calls,omitempty"` // Optional tool calls for the message
}

// NewSystemMessage creates a new system message.
func NewSystemMessage(content string) Message {
	return Message{
		Role:    RoleSystem,
		Content: content,
	}
}

// NewUserMessage creates a new user message.
func NewUserMessage(content string) Message {
	return Message{
		Role:    RoleUser,
		Content: content,
	}
}

// NewAssistantMessage creates a new assistant message.
func NewAssistantMessage(content string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: content,
	}
}

// GenerationConfig contains configuration for text generation.
type GenerationConfig struct {
	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens *int `json:"max_tokens,omitempty"`

	// Temperature controls randomness (0.0 to 2.0).
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP controls nucleus sampling (0.0 to 1.0).
	TopP *float64 `json:"top_p,omitempty"`

	// Stream indicates whether to stream the response.
	Stream bool `json:"stream"`

	// Stop sequences where the API will stop generating further tokens.
	Stop []string `json:"stop,omitempty"`

	// PresencePenalty penalizes new tokens based on their existing frequency.
	PresencePenalty *float64 `json:"presence_penalty,omitempty"`

	// FrequencyPenalty penalizes new tokens based on their frequency in the text so far.
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`

	// ReasoningEffort limits the reasoning effort for reasoning models.
	// Supported values: "low", "medium", "high".
	// Only effective for OpenAI o-series models.
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`

	// ThinkingEnabled enables thinking mode for Claude and Gemini models via OpenAI API.
	ThinkingEnabled *bool `json:"thinking_enabled,omitempty"`

	// ThinkingTokens controls the length of thinking for Claude and Gemini models via OpenAI API.
	ThinkingTokens *int `json:"thinking_tokens,omitempty"`
}

// Request is the request to the model.
type Request struct {
	// Messages is the conversation history.
	Messages []Message `json:"messages"`

	// GenerationConfig contains the generation parameters.
	GenerationConfig `json:",inline"`

	Tools map[string]tool.Tool `json:"-"` // Tools are not serialized, handled separately
}

// ToolCall represents a call to a tool (function) in the model response.
type ToolCall struct {
	// Type of the tool. Currently, only `function` is supported.
	Type string `json:"type"`
	// Function definition for the tool
	Function FunctionDefinitionParam `json:"function,omitempty"`
	// The ID of the tool call returned by the model.
	ID string `json:"id,omitempty"`

	// Index is the index of the tool call in the message for streaming responses.
	Index *int `json:"index,omitempty"`
}

type FunctionDefinitionParam struct {
	// The name of the function to be called. Must be a-z, A-Z, 0-9, or contain
	// underscores and dashes, with a maximum length of 64.
	Name string `json:"name"`
	// Whether to enable strict schema adherence when generating the function call. If
	// set to true, the model will follow the exact schema defined in the `parameters`
	// field. Only a subset of JSON Schema is supported when `strict` is `true`. Learn
	// more about Structured Outputs in the
	// [function calling guide](docs/guides/function-calling).
	Strict bool `json:"strict,omitempty"`
	// A description of what the function does, used by the model to choose when and
	// how to call the function.
	Description string `json:"description,omitempty"`

	// Optional arguments to pass to the function, json-encoded.
	Arguments []byte `json:"arguments,omitempty"`
}
