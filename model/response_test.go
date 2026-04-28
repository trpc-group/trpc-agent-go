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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type typedCodedError struct{}

const (
	typedCodedErrMsg     = "boom"
	typedCodedErrType    = "fatal"
	typedCodedErrCodeInt = -1
	typedCodedErrCodeStr = "-1"
)

func (typedCodedError) Error() string { return typedCodedErrMsg }

func (typedCodedError) ErrorType() string { return typedCodedErrType }

func (typedCodedError) Code() int { return typedCodedErrCodeInt }

type errorCodeStringError struct{}

const errorCodeStringErrCode = "E_CODE"

func (errorCodeStringError) Error() string { return typedCodedErrMsg }

func (errorCodeStringError) ErrorCode() string { return errorCodeStringErrCode }

type codeStringError struct{}

const codeStringErrCode = "E_STR"

func (codeStringError) Error() string { return typedCodedErrMsg }

func (codeStringError) Code() string { return codeStringErrCode }

type codeInt32Error struct{}

const (
	codeInt32ErrCode    int32 = 32
	codeInt32ErrCodeStr       = "32"
)

func (codeInt32Error) Error() string { return typedCodedErrMsg }

func (codeInt32Error) Code() int32 { return codeInt32ErrCode }

type codeInt64Error struct{}

const (
	codeInt64ErrCode    int64 = 64
	codeInt64ErrCodeStr       = "64"
)

func (codeInt64Error) Error() string { return typedCodedErrMsg }

func (codeInt64Error) Code() int64 { return codeInt64ErrCode }

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
			assert.Equal(t, tt.expected, tt.constant)
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

	assert.Equal(t, 0, choice.Index)
	assert.Equal(t, RoleAssistant, choice.Message.Role)
	assert.Equal(t, "Streaming content", choice.Delta.Content)
	require.NotNil(t, choice.FinishReason)
	assert.Equal(t, "stop", *choice.FinishReason)
}

func TestUsage_Structure(t *testing.T) {
	usage := Usage{
		PromptTokens:     10,
		CompletionTokens: 20,
		TotalTokens:      30,
	}

	assert.Equal(t, 10, usage.PromptTokens)
	assert.Equal(t, 20, usage.CompletionTokens)
	assert.Equal(t, 30, usage.TotalTokens)
}

func TestPromptTokensDetails_Structure(t *testing.T) {
	tests := []struct {
		name    string
		details PromptTokensDetails
	}{
		{
			name: "OpenAI style - cached tokens only",
			details: PromptTokensDetails{
				CachedTokens: 100,
			},
		},
		{
			name: "Anthropic style - all cache fields",
			details: PromptTokensDetails{
				CachedTokens:        200,
				CacheCreationTokens: 150,
				CacheReadTokens:     200,
			},
		},
		{
			name: "zero values",
			details: PromptTokensDetails{
				CachedTokens:        0,
				CacheCreationTokens: 0,
				CacheReadTokens:     0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.GreaterOrEqual(t, tt.details.CachedTokens, 0)
			assert.GreaterOrEqual(t, tt.details.CacheCreationTokens, 0)
			assert.GreaterOrEqual(t, tt.details.CacheReadTokens, 0)
		})
	}
}

func TestCompletionTokensDetails_Structure(t *testing.T) {
	details := CompletionTokensDetails{
		ReasoningTokens: 100,
	}
	assert.Equal(t, 100, details.ReasoningTokens)

	zero := CompletionTokensDetails{}
	assert.Equal(t, 0, zero.ReasoningTokens)
}

func TestUsage_WithPromptTokensDetails(t *testing.T) {
	tests := []struct {
		name  string
		usage Usage
	}{
		{
			name: "usage with OpenAI cache details",
			usage: Usage{
				PromptTokens:     1000,
				CompletionTokens: 500,
				TotalTokens:      1500,
				PromptTokensDetails: PromptTokensDetails{
					CachedTokens: 800,
				},
			},
		},
		{
			name: "usage with Anthropic cache details",
			usage: Usage{
				PromptTokens:     2000,
				CompletionTokens: 300,
				TotalTokens:      2300,
				PromptTokensDetails: PromptTokensDetails{
					CachedTokens:        1500,
					CacheCreationTokens: 500,
					CacheReadTokens:     1500,
				},
			},
		},
		{
			name: "usage without cache details",
			usage: Usage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
				PromptTokensDetails: PromptTokensDetails{
					CachedTokens: 0,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify basic usage fields
			assert.Equal(t, tt.usage.PromptTokens+tt.usage.CompletionTokens, tt.usage.TotalTokens)

			// Verify cache tokens don't exceed prompt tokens
			assert.LessOrEqual(t, tt.usage.PromptTokensDetails.CachedTokens, tt.usage.PromptTokens)
		})
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

	assert.Equal(t, "chatcmpl-123", response.ID)
	assert.Equal(t, "chat.completion", response.Object)
	assert.Equal(t, "gpt-3.5-turbo", response.Model)
	assert.Len(t, response.Choices, 1)
	require.NotNil(t, response.Usage)
	assert.Equal(t, 15, response.Usage.TotalTokens)
	require.NotNil(t, response.SystemFingerprint)
	assert.Equal(t, "fp_test_123", *response.SystemFingerprint)
	assert.True(t, response.Done)
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

	assert.Equal(t, "Invalid parameter value", err.Message)
	assert.Equal(t, ErrorTypeAPIError, err.Type)
	require.NotNil(t, err.Param)
	assert.Equal(t, "max_tokens", *err.Param)
	require.NotNil(t, err.Code)
	assert.Equal(t, "invalid_value", *err.Code)
}

func TestResponseError_ImplementsError(t *testing.T) {
	var respErr *ResponseError
	assert.Empty(t, respErr.Error())

	respErr = &ResponseError{Message: "boom"}
	var err error = respErr
	assert.Equal(t, "boom", err.Error())
}

func TestResponseErrorFromError_PreservesTypeAndCode(t *testing.T) {
	code := "E42"
	in := &ResponseError{
		Type:    "biz_error",
		Code:    &code,
		Message: "bad",
	}

	got := ResponseErrorFromError(in, ErrorTypeFlowError)
	require.NotNil(t, got)
	assert.Equal(t, "biz_error", got.Type)
	require.NotNil(t, got.Code)
	assert.Equal(t, "E42", *got.Code)
	assert.Equal(t, "bad", got.Message)
}

func TestResponseErrorFromError_ExtractsTypeAndCode(t *testing.T) {
	err := errors.New("wrapper: boom")
	err = errors.Join(err, typedCodedError{})

	got := ResponseErrorFromError(err, ErrorTypeFlowError)
	require.NotNil(t, got)
	assert.Equal(t, typedCodedErrType, got.Type)
	require.NotNil(t, got.Code)
	assert.Equal(t, typedCodedErrCodeStr, *got.Code)
	assert.NotEmpty(t, got.Message)
}

func TestResponseErrorFromError_Nil(t *testing.T) {
	got := ResponseErrorFromError(nil, ErrorTypeFlowError)
	require.Nil(t, got)
}

func TestResponseErrorFromError_FallbackTypeAndNoCode(t *testing.T) {
	err := errors.New("boom")

	got := ResponseErrorFromError(err, ErrorTypeFlowError)
	require.NotNil(t, got)
	assert.Equal(t, ErrorTypeFlowError, got.Type)
	assert.Nil(t, got.Code)
	assert.Equal(t, "boom", got.Message)
}

func TestResponseErrorFromError_UsesErrorCodeMethod(t *testing.T) {
	got := ResponseErrorFromError(errorCodeStringError{}, ErrorTypeFlowError)
	require.NotNil(t, got)
	require.NotNil(t, got.Code)
	assert.Equal(t, errorCodeStringErrCode, *got.Code)
}

func TestResponseErrorFromError_UsesCodeStringMethod(t *testing.T) {
	got := ResponseErrorFromError(codeStringError{}, ErrorTypeFlowError)
	require.NotNil(t, got)
	require.NotNil(t, got.Code)
	assert.Equal(t, codeStringErrCode, *got.Code)
}

func TestResponseErrorFromError_UsesCodeInt32Method(t *testing.T) {
	got := ResponseErrorFromError(codeInt32Error{}, ErrorTypeFlowError)
	require.NotNil(t, got)
	require.NotNil(t, got.Code)
	assert.Equal(t, codeInt32ErrCodeStr, *got.Code)
}

func TestResponseErrorFromError_UsesCodeInt64Method(t *testing.T) {
	got := ResponseErrorFromError(codeInt64Error{}, ErrorTypeFlowError)
	require.NotNil(t, got)
	require.NotNil(t, got.Code)
	assert.Equal(t, codeInt64ErrCodeStr, *got.Code)
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

	require.NotNil(t, response.Error)
	assert.Equal(t, "API error occurred", response.Error.Message)
	assert.Equal(t, ErrorTypeStreamError, response.Error.Type)
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

	assert.Equal(t, "chat.completion.chunk", streamChunk.Object)
	require.NotEmpty(t, streamChunk.Choices)
	assert.Equal(t, "partial ", streamChunk.Choices[0].Delta.Content)
	assert.False(t, streamChunk.Done)
}

func TestResponse_EmptyChoices(t *testing.T) {
	response := Response{
		ID:      "chatcmpl-empty",
		Choices: []Choice{},
		Done:    true,
	}

	assert.Empty(t, response.Choices)
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

	require.Len(t, response.Choices, 2)
	assert.Equal(t, 0, response.Choices[0].Index)
	assert.Equal(t, 1, response.Choices[1].Index)
	assert.Equal(t, "Second choice", response.Choices[1].Message.Content)
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
			name: "valid content in content parts",
			rsp: &Response{
				Choices: []Choice{{
					Message: Message{
						ContentParts: []ContentPart{
							{
								Type: ContentTypeText,
								Text: func() *string {
									s := "Hello from part"
									return &s
								}(),
							},
						},
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
			name: "valid content in delta content parts",
			rsp: &Response{
				Choices: []Choice{{
					Delta: Message{
						ContentParts: []ContentPart{
							{
								Type: ContentTypeText,
								Text: func() *string {
									s := "Hello from delta part"
									return &s
								}(),
							},
						},
					},
				}},
			},
			want: true,
		},
		{
			name: "valid reasoning content in message",
			rsp: &Response{
				Choices: []Choice{{
					Message: Message{
						ReasoningContent: "let me think",
					},
				}},
			},
			want: true,
		},
		{
			name: "valid reasoning content in delta",
			rsp: &Response{
				Choices: []Choice{{
					Delta: Message{
						ReasoningContent: "streaming thought",
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
			assert.Equal(t, tt.want, tt.rsp.IsValidContent())
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
			assert.Equal(t, tt.expected, got)
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
			assert.Equal(t, tt.expected, got)
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
			assert.Equal(t, tt.expected, got)
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
			assert.Equal(t, tt.expected, got)
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
				assert.Nil(t, clone)
				return
			}

			// Verify it's a different object
			require.NotSame(t, tt.response, clone)

			// Verify all fields are equal
			assert.Equal(t, tt.response.ID, clone.ID)
			assert.Equal(t, tt.response.Object, clone.Object)
			assert.Equal(t, tt.response.Created, clone.Created)
			assert.Equal(t, tt.response.Model, clone.Model)

			// Verify Choices is a deep copy
			require.Len(t, clone.Choices, len(tt.response.Choices))
			if len(clone.Choices) > 0 {
				assert.NotSame(t, &tt.response.Choices[0], &clone.Choices[0])
			}
			for i := range clone.Choices {
				assert.Equal(t, tt.response.Choices[i], clone.Choices[i])
			}

			// Verify Usage is deep copied
			if tt.response.Usage != nil {
				require.NotNil(t, clone.Usage)
				assert.NotSame(t, tt.response.Usage, clone.Usage)
				assert.Equal(t, tt.response.Usage, clone.Usage)
			} else {
				assert.Nil(t, clone.Usage)
			}

			// Verify Error is deep copied
			if tt.response.Error != nil {
				require.NotNil(t, clone.Error)
				assert.NotSame(t, tt.response.Error, clone.Error)
				assert.Equal(t, tt.response.Error, clone.Error)
			} else {
				assert.Nil(t, clone.Error)
			}

			// Verify SystemFingerprint is deep copied
			if tt.response.SystemFingerprint != nil {
				require.NotNil(t, clone.SystemFingerprint)
				assert.NotSame(t, tt.response.SystemFingerprint, clone.SystemFingerprint)
				assert.Equal(t, *tt.response.SystemFingerprint, *clone.SystemFingerprint)
			} else {
				assert.Nil(t, clone.SystemFingerprint)
			}

			// Verify modifying clone doesn't affect original
			if len(clone.Choices) > 0 {
				clone.Choices[0].Message.Content = "Modified"
				assert.NotEqual(t, "Modified", tt.response.Choices[0].Message.Content)
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
			assert.Equal(t, tt.expected, got)
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
			assert.Equal(t, tt.expected, tt.rsp.IsPartial)
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
			assert.Equal(t, tt.expected, tt.constant)
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
			assert.Equal(t, tt.expected, result)
		})
	}
}
