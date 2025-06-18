package model

import (
	"time"
)

// Error type constants for ResponseError.Type field.
const (
	ErrorTypeStreamError = "stream_error"
	ErrorTypeAPIError    = "api_error"
)

// Choice represents a single completion choice.
type Choice struct {
	// Index is the index of the choice.
	Index int `json:"index"`

	// Message is the message content.
	Message Message `json:"message,omitempty"`

	// Delta is the delta message content.
	Delta Message `json:"delta,omitempty"`

	// FinishReason is the reason the choice was finished.
	// "stop", "length", "content_filter", etc.
	FinishReason *string `json:"finish_reason,omitempty"`
}

// Usage represents token usage information.
type Usage struct {
	// PromptTokens is the number of tokens in the prompt.
	PromptTokens int `json:"prompt_tokens"`

	// CompletionTokens is the number of tokens in the completion.
	CompletionTokens int `json:"completion_tokens"`

	// TotalTokens is the total number of tokens in the response.
	TotalTokens int `json:"total_tokens"`
}

// Response is the response from the model.
type Response struct {
	// ID is the unique identifier for this response.
	ID string `json:"id"`

	// Object describes the type of object returned (e.g., "chat.completion").
	Object string `json:"object"`

	// Created is the Unix timestamp when the response was created.
	Created int64 `json:"created"`

	// Model is the model used to generate the response.
	Model string `json:"model"`

	// Choices contains the completion choices.
	Choices []Choice `json:"choices"`

	// Usage contains token usage information (may be nil for streaming responses).
	Usage *Usage `json:"usage,omitempty"`

	// SystemFingerprint is a unique identifier for the backend configuration.
	SystemFingerprint *string `json:"system_fingerprint,omitempty"`

	// Error contains error information if the request failed.
	Error *ResponseError `json:"error,omitempty"`

	// Timestamp when this response chunk was received (for streaming).
	Timestamp time.Time `json:"-"`

	// Done indicates if this is the final chunk in a stream.
	Done bool `json:"-"`
}

// ResponseError represents an error response from the API.
type ResponseError struct {
	// Message is the error message.
	Message string `json:"message"`

	// Type is the type of error.
	Type string `json:"type"`

	// Param is the parameter that caused the error.
	Param *string `json:"param,omitempty"`

	// Code is the error code.
	Code *string `json:"code,omitempty"`
}
