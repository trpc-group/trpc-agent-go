//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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

import (
	"testing"
	"time"
)

func TestErrorTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{
			name:     "stream error type",
			constant: ErrorTypeStreamError,
			expected: "stream_error",
		},
		{
			name:     "api error type",
			constant: ErrorTypeAPIError,
			expected: "api_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("Error constant = %v, want %v", tt.constant, tt.expected)
			}
		})
	}
}

func TestChoice_Structure(t *testing.T) {
	finishReason := "stop"
	choice := Choice{
		Index: 0,
		Message: Message{
			Role:    RoleAssistant,
			Content: "Hello, how can I help you?",
		},
		Delta: Message{
			Role:    RoleAssistant,
			Content: "Streaming content",
		},
		FinishReason: &finishReason,
	}

	if choice.Index != 0 {
		t.Errorf("Choice.Index = %v, want %v", choice.Index, 0)
	}
	if choice.Message.Role != RoleAssistant {
		t.Errorf("Choice.Message.Role = %v, want %v", choice.Message.Role, RoleAssistant)
	}
	if choice.Delta.Content != "Streaming content" {
		t.Errorf("Choice.Delta.Content = %v, want %v", choice.Delta.Content, "Streaming content")
	}
	if *choice.FinishReason != "stop" {
		t.Errorf("Choice.FinishReason = %v, want %v", *choice.FinishReason, "stop")
	}
}

func TestUsage_Structure(t *testing.T) {
	usage := Usage{
		PromptTokens:     10,
		CompletionTokens: 20,
		TotalTokens:      30,
	}

	if usage.PromptTokens != 10 {
		t.Errorf("Usage.PromptTokens = %v, want %v", usage.PromptTokens, 10)
	}
	if usage.CompletionTokens != 20 {
		t.Errorf("Usage.CompletionTokens = %v, want %v", usage.CompletionTokens, 20)
	}
	if usage.TotalTokens != 30 {
		t.Errorf("Usage.TotalTokens = %v, want %v", usage.TotalTokens, 30)
	}
}

func TestResponse_Structure(t *testing.T) {
	now := time.Now()
	systemFingerprint := "fp_test_123"

	response := Response{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: now.Unix(),
		Model:   "gpt-3.5-turbo",
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    RoleAssistant,
					Content: "Test response",
				},
			},
		},
		Usage: &Usage{
			PromptTokens:     5,
			CompletionTokens: 10,
			TotalTokens:      15,
		},
		SystemFingerprint: &systemFingerprint,
		Timestamp:         now,
		Done:              true,
	}

	if response.ID != "chatcmpl-123" {
		t.Errorf("Response.ID = %v, want %v", response.ID, "chatcmpl-123")
	}
	if response.Object != "chat.completion" {
		t.Errorf("Response.Object = %v, want %v", response.Object, "chat.completion")
	}
	if response.Model != "gpt-3.5-turbo" {
		t.Errorf("Response.Model = %v, want %v", response.Model, "gpt-3.5-turbo")
	}
	if len(response.Choices) != 1 {
		t.Errorf("Response.Choices length = %v, want %v", len(response.Choices), 1)
	}
	if response.Usage.TotalTokens != 15 {
		t.Errorf("Response.Usage.TotalTokens = %v, want %v", response.Usage.TotalTokens, 15)
	}
	if *response.SystemFingerprint != "fp_test_123" {
		t.Errorf("Response.SystemFingerprint = %v, want %v", *response.SystemFingerprint, "fp_test_123")
	}
	if !response.Done {
		t.Errorf("Response.Done = %v, want %v", response.Done, true)
	}
}

func TestResponseError_Structure(t *testing.T) {
	param := "max_tokens"
	code := "invalid_value"

	err := ResponseError{
		Message: "Invalid parameter value",
		Type:    ErrorTypeAPIError,
		Param:   &param,
		Code:    &code,
	}

	if err.Message != "Invalid parameter value" {
		t.Errorf("ResponseError.Message = %v, want %v", err.Message, "Invalid parameter value")
	}
	if err.Type != ErrorTypeAPIError {
		t.Errorf("ResponseError.Type = %v, want %v", err.Type, ErrorTypeAPIError)
	}
	if *err.Param != "max_tokens" {
		t.Errorf("ResponseError.Param = %v, want %v", *err.Param, "max_tokens")
	}
	if *err.Code != "invalid_value" {
		t.Errorf("ResponseError.Code = %v, want %v", *err.Code, "invalid_value")
	}
}

func TestResponse_WithError(t *testing.T) {
	now := time.Now()

	response := Response{
		Error: &ResponseError{
			Message: "API error occurred",
			Type:    ErrorTypeStreamError,
		},
		Timestamp: now,
		Done:      true,
	}

	if response.Error == nil {
		t.Error("Response.Error should not be nil")
		return
	}
	if response.Error.Message != "API error occurred" {
		t.Errorf("Response.Error.Message = %v, want %v", response.Error.Message, "API error occurred")
	}
	if response.Error.Type != ErrorTypeStreamError {
		t.Errorf("Response.Error.Type = %v, want %v", response.Error.Type, ErrorTypeStreamError)
	}
}

func TestResponse_StreamingResponse(t *testing.T) {
	now := time.Now()

	// Simulate a streaming response chunk
	streamChunk := Response{
		ID:      "chatcmpl-stream-123",
		Object:  "chat.completion.chunk",
		Created: now.Unix(),
		Model:   "gpt-3.5-turbo",
		Choices: []Choice{
			{
				Index: 0,
				Delta: Message{
					Role:    RoleAssistant,
					Content: "partial ",
				},
			},
		},
		Timestamp: now,
		Done:      false,
	}

	if streamChunk.Object != "chat.completion.chunk" {
		t.Errorf("Stream chunk Object = %v, want %v", streamChunk.Object, "chat.completion.chunk")
	}
	if streamChunk.Choices[0].Delta.Content != "partial " {
		t.Errorf("Stream chunk Delta.Content = %v, want %v", streamChunk.Choices[0].Delta.Content, "partial ")
	}
	if streamChunk.Done {
		t.Errorf("Stream chunk Done = %v, want %v", streamChunk.Done, false)
	}
}

func TestResponse_EmptyChoices(t *testing.T) {
	response := Response{
		ID:      "chatcmpl-empty",
		Choices: []Choice{},
		Done:    true,
	}

	if len(response.Choices) != 0 {
		t.Errorf("Empty response Choices length = %v, want %v", len(response.Choices), 0)
	}
}

func TestResponse_MultipleChoices(t *testing.T) {
	response := Response{
		ID: "chatcmpl-multi",
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    RoleAssistant,
					Content: "First choice",
				},
			},
			{
				Index: 1,
				Message: Message{
					Role:    RoleAssistant,
					Content: "Second choice",
				},
			},
		},
		Done: true,
	}

	if len(response.Choices) != 2 {
		t.Errorf("Multiple choices length = %v, want %v", len(response.Choices), 2)
	}
	if response.Choices[0].Index != 0 {
		t.Errorf("First choice index = %v, want %v", response.Choices[0].Index, 0)
	}
	if response.Choices[1].Index != 1 {
		t.Errorf("Second choice index = %v, want %v", response.Choices[1].Index, 1)
	}
	if response.Choices[1].Message.Content != "Second choice" {
		t.Errorf("Second choice content = %v, want %v", response.Choices[1].Message.Content, "Second choice")
	}
}
