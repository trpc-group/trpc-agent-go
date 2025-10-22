//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"time"
)

// Error type constants for ResponseError.Type field.
const (
	ErrorTypeStreamError = "stream_error"
	ErrorTypeAPIError    = "api_error"
	ErrorTypeFlowError   = "flow_error"
)

// Object type constants for Response.Object field.
const (
	ObjectTypeError = "error"
	// ObjectTypeToolResponse is the object type for tool response events.
	ObjectTypeToolResponse = "tool.response"
	// ObjectTypePreprocessingBasic is the object type for basic preprocessing events.
	ObjectTypePreprocessingBasic = "preprocessing.basic"
	// ObjectTypePreprocessingContent is the object type for content preprocessing events.
	ObjectTypePreprocessingContent = "preprocessing.content"
	// ObjectTypePreprocessingIdentity is the object type for identity preprocessing events.
	ObjectTypePreprocessingIdentity = "preprocessing.identity"
	// ObjectTypePreprocessingInstruction is the object type for instruction preprocessing events.
	ObjectTypePreprocessingInstruction = "preprocessing.instruction"
	// ObjectTypePreprocessingPlanning is the object type for planning preprocessing events.
	ObjectTypePreprocessingPlanning = "preprocessing.planning"
	// ObjectTypePostprocessingPlanning is the object type for planning postprocessing events.
	ObjectTypePostprocessingPlanning = "postprocessing.planning"

	ObjectTypePostprocessingCodeExecution = "postprocessing.code_execution"
	// ObjectTypeTransfer is the object type for agent transfer events.
	ObjectTypeTransfer = "agent.transfer"
	// ObjectTypeRunnerCompletion is the object type for runner completion events.
	ObjectTypeRunnerCompletion = "runner.completion"
	// ObjectTypeStateUpdate is the object type for state update events.
	ObjectTypeStateUpdate = "state.update"

	// ObjectTypeChatCompletionChunk is the object type for chat completion chunk events.
	ObjectTypeChatCompletionChunk = "chat.completion.chunk"
	// ObjectTypeChatCompletion is the object type for chat completion events.
	ObjectTypeChatCompletion = "chat.completion"
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
//
// Error Handling Note:
// The Error field in this struct represents API-level errors that occur
// after successful communication with the model service. This is different
// from function-level errors returned by GenerateContent(), which indicate
// system-level failures that prevent communication entirely.
//
// Examples of Response.Error:
// - API rate limit exceeded
// - Content filtered by safety systems
// - Model-specific processing errors
// - Streaming connection errors
//
// Examples of function-level errors:
// - Invalid request parameters
// - Network connectivity issues
// - Authentication failures
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

	// Error contains API-level error information if the request failed.
	// This is nil for successful responses.
	// Note: This is different from function-level errors returned by GenerateContent().
	Error *ResponseError `json:"error,omitempty"`

	// Timestamp when this response chunk was received (for streaming).
	Timestamp time.Time `json:"timestamp"`

	// Done indicates if the llm flow should stop.
	Done bool `json:"done"`

	// IsPartial indicates if this is a partial response.
	IsPartial bool `json:"is_partial"`
}

// Clone creates a deep copy of the response.
func (rsp *Response) Clone() *Response {
	if rsp == nil {
		return nil
	}
	clone := *rsp
	clone.Choices = make([]Choice, len(rsp.Choices))
	copy(clone.Choices, rsp.Choices)
	if rsp.Usage != nil {
		clone.Usage = &Usage{
			PromptTokens:     rsp.Usage.PromptTokens,
			CompletionTokens: rsp.Usage.CompletionTokens,
			TotalTokens:      rsp.Usage.TotalTokens,
		}
	}
	// Deep copy Error if present.
	if rsp.Error != nil {
		clone.Error = &ResponseError{
			Message: rsp.Error.Message,
			Type:    rsp.Error.Type,
			Param:   rsp.Error.Param,
			Code:    rsp.Error.Code,
		}
	}
	// Deep copy SystemFingerprint if present.
	if rsp.SystemFingerprint != nil {
		fp := *rsp.SystemFingerprint
		clone.SystemFingerprint = &fp
	}
	return &clone
}

// IsValidContent checks if the response has valid content for message generation.
func (rsp *Response) IsValidContent() bool {
	if rsp == nil {
		return false
	}

	if rsp.IsToolCallResponse() || rsp.IsToolResultResponse() {
		return true
	}
	// Check if event has choices with content.
	for _, choice := range rsp.Choices {
		if choice.Message.Content != "" {
			return true
		}
		if choice.Delta.Content != "" {
			return true
		}
	}
	return false
}

// IsToolResultResponse  checks if the response is a tool call result response.
func (rsp *Response) IsToolResultResponse() bool {
	return rsp != nil && len(rsp.Choices) > 0 && rsp.Choices[0].Message.ToolID != ""
}

// IsToolCallResponse checks if the response is related to tool calls.
func (rsp *Response) IsToolCallResponse() bool {
	return rsp != nil && len(rsp.Choices) > 0 && len(rsp.Choices[0].Message.ToolCalls) > 0
}

// GetToolCallIDs gets the IDs of tool calls from the response.
func (rsp *Response) GetToolCallIDs() []string {
	ids := make([]string, 0)
	if rsp == nil || len(rsp.Choices) <= 0 {
		return ids
	}
	for _, choice := range rsp.Choices {
		for _, toolCall := range choice.Message.ToolCalls {
			ids = append(ids, toolCall.ID)
		}
	}
	return ids
}

// GetToolResultIDs gets the IDs of tool results from the response.
func (rsp *Response) GetToolResultIDs() []string {
	ids := make([]string, 0)
	if rsp == nil || len(rsp.Choices) <= 0 {
		return ids
	}
	for _, choice := range rsp.Choices {
		if choice.Message.ToolID != "" {
			ids = append(ids, choice.Message.ToolID)
		}
	}
	return ids
}

// IsFinalResponse checks if the Response is a final response.
func (rsp *Response) IsFinalResponse() bool {
	if rsp == nil {
		return true
	}

	if rsp.IsPartial || rsp.IsToolCallResponse() {
		return false
	}

	// Consider response final if it's marked as done and has content or error.
	return rsp.Done && (len(rsp.Choices) > 0 || rsp.Error != nil)
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
