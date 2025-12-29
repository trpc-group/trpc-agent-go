//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package huggingface

// ChatCompletionRequest represents a request to the HuggingFace chat completion API.
type ChatCompletionRequest struct {
	Model            string          `json:"model"`
	Messages         []ChatMessage   `json:"messages"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	N                *int            `json:"n,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	Stop             []string        `json:"stop,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	Tools            []Tool          `json:"tools,omitempty"`
	ToolChoice       any             `json:"tool_choice,omitempty"`
	ResponseFormat   *ResponseFormat `json:"response_format,omitempty"`
	Seed             *int            `json:"seed,omitempty"`
	ExtraFields      map[string]any  `json:"-"` // Extra fields to be merged into the request
}

// ChatMessage represents a message in the chat completion request.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"` // Can be string or []ContentPart
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Refusal    string     `json:"refusal,omitempty"`
}

// ContentPart represents a part of the message content (text or image).
type ContentPart struct {
	Type     string    `json:"type"` // "text" or "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL represents an image URL in the message content.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "auto", "low", or "high"
}

// Tool represents a tool that can be called by the model.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function FunctionTool `json:"function"`
}

// FunctionTool represents a function tool definition.
type FunctionTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ToolCall represents a tool call made by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function call made by the model.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ResponseFormat specifies the format of the response.
type ResponseFormat struct {
	Type string `json:"type"` // "text" or "json_object"
}

// ChatCompletionResponse represents a response from the HuggingFace chat completion API.
type ChatCompletionResponse struct {
	ID                string                 `json:"id"`
	Object            string                 `json:"object"`
	Created           int64                  `json:"created"`
	Model             string                 `json:"model"`
	Choices           []ChatCompletionChoice `json:"choices"`
	Usage             *UsageInfo             `json:"usage,omitempty"`
	SystemFingerprint string                 `json:"system_fingerprint,omitempty"`
}

// ChatCompletionChoice represents a choice in the chat completion response.
type ChatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"` // "stop", "length", "tool_calls", "content_filter"
	Logprobs     *Logprobs   `json:"logprobs,omitempty"`
}

// ChatCompletionChunk represents a chunk in the streaming chat completion response.
type ChatCompletionChunk struct {
	ID                string                      `json:"id"`
	Object            string                      `json:"object"`
	Created           int64                       `json:"created"`
	Model             string                      `json:"model"`
	Choices           []ChatCompletionChunkChoice `json:"choices"`
	Usage             *UsageInfo                  `json:"usage,omitempty"`
	SystemFingerprint string                      `json:"system_fingerprint,omitempty"`
}

// ChatCompletionChunkChoice represents a choice in the streaming chat completion chunk.
type ChatCompletionChunkChoice struct {
	Index        int         `json:"index"`
	Delta        ChatMessage `json:"delta"`
	FinishReason string      `json:"finish_reason,omitempty"`
	Logprobs     *Logprobs   `json:"logprobs,omitempty"`
}

// UsageInfo represents token usage information.
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Logprobs represents log probabilities information.
type Logprobs struct {
	Content []TokenLogprob `json:"content,omitempty"`
}

// TokenLogprob represents log probability for a token.
type TokenLogprob struct {
	Token       string       `json:"token"`
	Logprob     float64      `json:"logprob"`
	Bytes       []int        `json:"bytes,omitempty"`
	TopLogprobs []TopLogprob `json:"top_logprobs,omitempty"`
}

// TopLogprob represents a top log probability alternative.
type TopLogprob struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
	Bytes   []int   `json:"bytes,omitempty"`
}

// ErrorResponse represents an error response from the HuggingFace API.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail represents the error detail.
type ErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}
