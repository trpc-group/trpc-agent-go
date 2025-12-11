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
	"reflect"
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

func TestResponse_IsValidContent(t *testing.T) {
	tests := []struct {
		name string
		rsp  *Response
		want bool
	}{
		{
			name: "nil response",
			rsp:  nil,
			want: false,
		},
		{
			name: "tool call response",
			rsp: &Response{
				Choices: []Choice{{
					Message: Message{
						ToolCalls: []ToolCall{{ID: "tool1"}},
					},
				}},
			},
			want: true,
		},
		{
			name: "tool result response",
			rsp: &Response{
				Choices: []Choice{{
					Message: Message{
						ToolID: "tool1",
					},
				}},
			},
			want: true,
		},
		{
			name: "valid content in message",
			rsp: &Response{
				Choices: []Choice{{
					Message: Message{
						Content: "Hello, world!",
					},
				}},
			},
			want: true,
		},
		{
			name: "valid content in delta",
			rsp: &Response{
				Choices: []Choice{{
					Delta: Message{
						Content: "Hello, world!",
					},
				}},
			},
			want: true,
		},
		{
			name: "no valid content",
			rsp: &Response{
				Choices: []Choice{{
					Message: Message{},
				}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rsp.IsValidContent(); got != tt.want {
				t.Errorf("IsValidContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsToolResultResponse tests the IsToolResultResponse function with table-driven tests.
func TestResponse_IsToolResultResponse(t *testing.T) {
	type testCase struct {
		name     string
		rsp      *Response
		expected bool
	}

	tests := []testCase{
		{
			name:     "nil response",
			rsp:      nil,
			expected: false,
		},
		{
			name:     "empty choices",
			rsp:      &Response{Choices: []Choice{}},
			expected: false,
		},
		{
			name: "choices with empty ToolID",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{ToolID: ""},
					},
				},
			},
			expected: false,
		},
		{
			name: "choices with non-empty ToolID",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{ToolID: "tool123"},
					},
				},
			},
			expected: true,
		},
		{
			name: "choices with non-empty ToolID in delta",
			rsp: &Response{
				Choices: []Choice{
					{
						Delta: Message{ToolID: "tool123"},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rsp.IsToolResultResponse()
			if got != tt.expected {
				t.Errorf("IsToolResultResponse() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestResponse_GetToolCallIDs(t *testing.T) {
	tests := []struct {
		name     string
		rsp      *Response
		expected []string
	}{
		{
			name:     "nil response",
			rsp:      nil,
			expected: []string{},
		},
		{
			name: "no choices",
			rsp: &Response{
				Choices: []Choice{},
			},
			expected: []string{},
		},
		{
			name: "with tool calls",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{
							ToolCalls: []ToolCall{
								{ID: "tool1"},
								{ID: "tool2"},
							},
						},
					},
				},
			},
			expected: []string{"tool1", "tool2"},
		},
		{
			name: "with tool calls in delta",
			rsp: &Response{
				Choices: []Choice{
					{
						Delta: Message{
							ToolCalls: []ToolCall{
								{ID: "tool1"},
								{ID: "tool2"},
							},
						},
					},
				},
			},
			expected: []string{"tool1", "tool2"},
		},
		{
			name: "no tool calls",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{},
					},
				},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rsp.GetToolCallIDs()
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("GetToolCallIDs() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestResponse_GetToolResultIDs(t *testing.T) {
	tests := []struct {
		name     string
		rsp      *Response
		expected []string
	}{
		{
			name:     "nil response",
			rsp:      nil,
			expected: []string{},
		},
		{
			name: "no choices",
			rsp: &Response{
				Choices: []Choice{},
			},
			expected: []string{},
		},
		{
			name: "with tool IDs",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{
							ToolID: "tool1",
						},
					},
					{
						Message: Message{
							ToolID: "tool2",
						},
					},
				},
			},
			expected: []string{"tool1", "tool2"},
		},
		{
			name: "with tool IDs in delta",
			rsp: &Response{
				Choices: []Choice{
					{
						Delta: Message{
							ToolID: "tool1",
						},
					},
					{
						Delta: Message{
							ToolID: "tool2",
						},
					},
				},
			},
			expected: []string{"tool1", "tool2"},
		},
		{
			name: "no tool IDs",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{},
					},
				},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rsp.GetToolResultIDs()
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("GetToolResultIDs() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestResponse_IsFinalResponse(t *testing.T) {
	tests := []struct {
		name     string
		rsp      *Response
		expected bool
	}{
		{
			name:     "nil response",
			rsp:      nil,
			expected: true,
		},
		{
			name: "tool call response",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{
							ToolCalls: []ToolCall{{ID: "tool1"}},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "done with content",
			rsp: &Response{
				Done: true,
				Choices: []Choice{
					{
						Message: Message{Content: "content"},
					},
				},
			},
			expected: true,
		},
		{
			name: "done with error",
			rsp: &Response{
				Done:  true,
				Error: &ResponseError{Message: "error"},
			},
			expected: true,
		},
		{
			name: "not done",
			rsp: &Response{
				Done: false,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rsp.IsFinalResponse()
			if got != tt.expected {
				t.Errorf("IsFinalResponse() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestResponse_Clone(t *testing.T) {
	tests := []struct {
		name     string
		response *Response
	}{
		{
			name:     "clone nil response",
			response: nil,
		},
		{
			name: "clone simple response",
			response: &Response{
				ID:      "resp-123",
				Object:  "chat.completion",
				Created: 1234567890,
				Model:   "gpt-4",
				Choices: []Choice{
					{
						Index: 0,
						Message: Message{
							Role:    RoleAssistant,
							Content: "Hello!",
						},
					},
				},
			},
		},
		{
			name: "clone response with usage",
			response: &Response{
				ID:    "resp-456",
				Model: "gpt-3.5-turbo",
				Usage: &Usage{
					PromptTokens:     10,
					CompletionTokens: 20,
					TotalTokens:      30,
				},
			},
		},
		{
			name: "clone response with error",
			response: &Response{
				ID: "resp-789",
				Error: &ResponseError{
					Message: "API error",
					Type:    "invalid_request_error",
					Param:   func() *string { s := "messages"; return &s }(),
					Code:    func() *string { s := "invalid_value"; return &s }(),
				},
			},
		},
		{
			name: "clone response with system fingerprint",
			response: &Response{
				ID: "resp-abc",
				SystemFingerprint: func() *string {
					s := "fp_123456"
					return &s
				}(),
			},
		},
		{
			name: "clone response with all fields",
			response: &Response{
				ID:      "resp-full",
				Object:  "chat.completion",
				Created: 9876543210,
				Model:   "gpt-4-turbo",
				Choices: []Choice{
					{
						Index: 0,
						Message: Message{
							Role:    RoleAssistant,
							Content: "First message",
						},
					},
					{
						Index: 1,
						Message: Message{
							Role:    RoleAssistant,
							Content: "Second message",
						},
					},
				},
				Usage: &Usage{
					PromptTokens:     100,
					CompletionTokens: 200,
					TotalTokens:      300,
				},
				Error: &ResponseError{
					Message: "Test error",
					Type:    "test_error",
					Param:   func() *string { s := "test_param"; return &s }(),
					Code:    func() *string { s := "test_code"; return &s }(),
				},
				SystemFingerprint: func() *string {
					s := "fp_abcdef"
					return &s
				}(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clone := tt.response.Clone()

			// Test nil case
			if tt.response == nil {
				if clone != nil {
					t.Errorf("Clone of nil should return nil, got %v", clone)
				}
				return
			}

			// Verify it's a different object
			if tt.response == clone {
				t.Error("Clone should return a different object")
			}

			// Verify all fields are equal
			if clone.ID != tt.response.ID {
				t.Errorf("ID mismatch: got %v, want %v", clone.ID, tt.response.ID)
			}
			if clone.Object != tt.response.Object {
				t.Errorf("Object mismatch: got %v, want %v", clone.Object, tt.response.Object)
			}
			if clone.Created != tt.response.Created {
				t.Errorf("Created mismatch: got %v, want %v", clone.Created, tt.response.Created)
			}
			if clone.Model != tt.response.Model {
				t.Errorf("Model mismatch: got %v, want %v", clone.Model, tt.response.Model)
			}

			// Verify Choices is a deep copy
			if len(clone.Choices) != len(tt.response.Choices) {
				t.Errorf("Choices length mismatch: got %v, want %v", len(clone.Choices), len(tt.response.Choices))
			}
			if len(clone.Choices) > 0 && &clone.Choices[0] == &tt.response.Choices[0] {
				t.Error("Choices should be deep copied")
			}
			for i := range clone.Choices {
				if !reflect.DeepEqual(clone.Choices[i], tt.response.Choices[i]) {
					t.Errorf("Choice %d mismatch: got %+v, want %+v", i, clone.Choices[i], tt.response.Choices[i])
				}
			}

			// Verify Usage is deep copied
			if tt.response.Usage != nil {
				if clone.Usage == nil {
					t.Error("Usage should be copied")
				} else {
					if clone.Usage == tt.response.Usage {
						t.Error("Usage should be deep copied")
					}
					if !reflect.DeepEqual(clone.Usage, tt.response.Usage) {
						t.Errorf("Usage mismatch: got %+v, want %+v", clone.Usage, tt.response.Usage)
					}
				}
			} else if clone.Usage != nil {
				t.Error("Usage should be nil")
			}

			// Verify Error is deep copied
			if tt.response.Error != nil {
				if clone.Error == nil {
					t.Error("Error should be copied")
				} else {
					if clone.Error == tt.response.Error {
						t.Error("Error should be deep copied")
					}
					if !reflect.DeepEqual(clone.Error, tt.response.Error) {
						t.Errorf("Error mismatch: got %+v, want %+v", clone.Error, tt.response.Error)
					}
				}
			} else if clone.Error != nil {
				t.Error("Error should be nil")
			}

			// Verify SystemFingerprint is deep copied
			if tt.response.SystemFingerprint != nil {
				if clone.SystemFingerprint == nil {
					t.Error("SystemFingerprint should be copied")
				} else {
					if clone.SystemFingerprint == tt.response.SystemFingerprint {
						t.Error("SystemFingerprint should be deep copied")
					}
					if *clone.SystemFingerprint != *tt.response.SystemFingerprint {
						t.Errorf("SystemFingerprint mismatch: got %v, want %v", *clone.SystemFingerprint, *tt.response.SystemFingerprint)
					}
				}
			} else if clone.SystemFingerprint != nil {
				t.Error("SystemFingerprint should be nil")
			}

			// Verify modifying clone doesn't affect original
			if len(clone.Choices) > 0 {
				clone.Choices[0].Message.Content = "Modified"
				if tt.response.Choices[0].Message.Content == "Modified" {
					t.Error("Modifying clone should not affect original")
				}
			}
		})
	}
}

// TestResponse_IsToolCallResponse tests the IsToolCallResponse method with additional scenarios.
func TestResponse_IsToolCallResponse(t *testing.T) {
	tests := []struct {
		name     string
		rsp      *Response
		expected bool
	}{
		{
			name:     "nil response",
			rsp:      nil,
			expected: false,
		},
		{
			name: "empty choices",
			rsp: &Response{
				Choices: []Choice{},
			},
			expected: false,
		},
		{
			name: "choices with no tool calls",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{
							Content: "Regular message",
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "choices with tool calls",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{
							ToolCalls: []ToolCall{
								{ID: "tool1"},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "choices with tool calls in delta",
			rsp: &Response{
				Choices: []Choice{
					{
						Delta: Message{
							ToolCalls: []ToolCall{
								{ID: "tool1"},
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rsp.IsToolCallResponse()
			if got != tt.expected {
				t.Errorf("IsToolCallResponse() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestResponse_IsPartialResponse tests the IsPartial field.
func TestResponse_IsPartialResponse(t *testing.T) {
	tests := []struct {
		name     string
		rsp      *Response
		expected bool
	}{
		{
			name: "partial response",
			rsp: &Response{
				IsPartial: true,
			},
			expected: true,
		},
		{
			name: "complete response",
			rsp: &Response{
				IsPartial: false,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.rsp.IsPartial != tt.expected {
				t.Errorf("IsPartial = %v, want %v", tt.rsp.IsPartial, tt.expected)
			}
		})
	}
}

// TestObjectTypeConstants tests all object type constants.
func TestObjectTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{
			name:     "error type",
			constant: ObjectTypeError,
			expected: "error",
		},
		{
			name:     "tool response type",
			constant: ObjectTypeToolResponse,
			expected: "tool.response",
		},
		{
			name:     "preprocessing basic type",
			constant: ObjectTypePreprocessingBasic,
			expected: "preprocessing.basic",
		},
		{
			name:     "preprocessing content type",
			constant: ObjectTypePreprocessingContent,
			expected: "preprocessing.content",
		},
		{
			name:     "preprocessing identity type",
			constant: ObjectTypePreprocessingIdentity,
			expected: "preprocessing.identity",
		},
		{
			name:     "preprocessing instruction type",
			constant: ObjectTypePreprocessingInstruction,
			expected: "preprocessing.instruction",
		},
		{
			name:     "preprocessing planning type",
			constant: ObjectTypePreprocessingPlanning,
			expected: "preprocessing.planning",
		},
		{
			name:     "postprocessing planning type",
			constant: ObjectTypePostprocessingPlanning,
			expected: "postprocessing.planning",
		},
		{
			name:     "postprocessing code execution type",
			constant: ObjectTypePostprocessingCodeExecution,
			expected: "postprocessing.code_execution",
		},
		{
			name:     "transfer type",
			constant: ObjectTypeTransfer,
			expected: "agent.transfer",
		},
		{
			name:     "runner completion type",
			constant: ObjectTypeRunnerCompletion,
			expected: "runner.completion",
		},
		{
			name:     "state update type",
			constant: ObjectTypeStateUpdate,
			expected: "state.update",
		},
		{
			name:     "chat completion chunk type",
			constant: ObjectTypeChatCompletionChunk,
			expected: "chat.completion.chunk",
		},
		{
			name:     "chat completion type",
			constant: ObjectTypeChatCompletion,
			expected: "chat.completion",
		},
		{
			name:     "flow error type",
			constant: ErrorTypeFlowError,
			expected: "flow_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("Constant = %v, want %v", tt.constant, tt.expected)
			}
		})
	}
}

func TestResponse_IsUserMessage(t *testing.T) {
	tests := []struct {
		name     string
		rsp      *Response
		expected bool
	}{
		{
			name:     "nil response",
			rsp:      nil,
			expected: false,
		},
		{
			name:     "empty choices",
			rsp:      &Response{Choices: []Choice{}},
			expected: false,
		},
		{
			name: "user role",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{Content: "content", Role: RoleUser},
					},
				},
			},
			expected: true,
		},
		{
			name: "assistant role",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{Role: RoleAssistant},
					},
				},
			},
			expected: false,
		},
		{
			name: "system role",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{Role: RoleSystem},
					},
				},
			},
			expected: false,
		},
		{
			name: "empty role",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{Role: ""},
					},
				},
			},
			expected: false,
		},
		{
			name: "multiple choices with first as user",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{Role: RoleUser},
					},
					{
						Message: Message{Role: RoleAssistant},
					},
				},
			},
			expected: true,
		},
		{
			name: "multiple choices with first as assistant",
			rsp: &Response{
				Choices: []Choice{
					{
						Message: Message{Role: RoleAssistant},
					},
					{
						Message: Message{Role: RoleUser},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.rsp.IsUserMessage()
			if result != tt.expected {
				t.Errorf("IsUserMessage() = %v, want %v", result, tt.expected)
			}
		})
	}
}
