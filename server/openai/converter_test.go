//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-group/trpc-go/trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewConverter(t *testing.T) {
	conv := newConverter("gpt-4")
	assert.NotNil(t, conv)
	assert.Equal(t, "gpt-4", conv.modelName)
}

func TestConverter_convertRequest(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	tests := []struct {
		name    string
		req     *openAIRequest
		wantErr bool
		check   func(t *testing.T, messages []model.Message)
	}{
		{
			name: "valid request with single message",
			req: &openAIRequest{
				Messages: []openAIMessage{
					{
						Role:    "user",
						Content: "Hello",
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, messages []model.Message) {
				assert.Len(t, messages, 1)
				assert.Equal(t, model.RoleUser, messages[0].Role)
				assert.Equal(t, "Hello", messages[0].Content)
			},
		},
		{
			name: "valid request with multiple messages",
			req: &openAIRequest{
				Messages: []openAIMessage{
					{
						Role:    "system",
						Content: "You are a helpful assistant",
					},
					{
						Role:    "user",
						Content: "Hello",
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, messages []model.Message) {
				assert.Len(t, messages, 2)
				assert.Equal(t, model.RoleSystem, messages[0].Role)
				assert.Equal(t, model.RoleUser, messages[1].Role)
			},
		},
		{
			name:    "nil request",
			req:     nil,
			wantErr: true,
		},
		{
			name: "empty messages",
			req: &openAIRequest{
				Messages: []openAIMessage{},
			},
			wantErr: true,
		},
		{
			name: "invalid role",
			req: &openAIRequest{
				Messages: []openAIMessage{
					{
						Role:    "invalid",
						Content: "Hello",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "message with tool calls",
			req: &openAIRequest{
				Messages: []openAIMessage{
					{
						Role:    "assistant",
						Content: "",
						ToolCalls: []openAIToolCall{
							{
								ID:   "call-123",
								Type: "function",
								Function: openAIToolCallFunction{
									Name:      "test_function",
									Arguments: `{"arg": "value"}`,
								},
							},
						},
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, messages []model.Message) {
				assert.Len(t, messages, 1)
				assert.Len(t, messages[0].ToolCalls, 1)
				assert.Equal(t, "call-123", messages[0].ToolCalls[0].ID)
				assert.Equal(t, "test_function", messages[0].ToolCalls[0].Function.Name)
			},
		},
		{
			name: "message with tool response",
			req: &openAIRequest{
				Messages: []openAIMessage{
					{
						Role:       "tool",
						Content:    "result",
						ToolCallID: "call-123",
						Name:       "test_function",
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, messages []model.Message) {
				assert.Len(t, messages, 1)
				assert.Equal(t, model.RoleTool, messages[0].Role)
				assert.Equal(t, "call-123", messages[0].ToolID)
				assert.Equal(t, "test_function", messages[0].ToolName)
				assert.Equal(t, "result", messages[0].Content)
			},
		},
		{
			name: "message with multimodal content",
			req: &openAIRequest{
				Messages: []openAIMessage{
					{
						Role: "user",
						Content: []any{
							map[string]any{
								"type": "text",
								"text": "Hello",
							},
							map[string]any{
								"type": "image_url",
								"image_url": map[string]any{
									"url": "https://example.com/image.jpg",
								},
							},
						},
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, messages []model.Message) {
				assert.Len(t, messages, 1)
				assert.Equal(t, "Hello", messages[0].Content)
				// Note: ImageURL handling would need to check the message's image URLs
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := conv.convertRequest(context.Background(), tt.req)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, messages)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, messages)
				if tt.check != nil {
					tt.check(t, messages)
				}
			}
		})
	}
}

func TestConverter_convertRole(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	tests := []struct {
		name    string
		role    string
		want    model.Role
		wantErr bool
	}{
		{
			name:    "system role",
			role:    "system",
			want:    model.RoleSystem,
			wantErr: false,
		},
		{
			name:    "user role",
			role:    "user",
			want:    model.RoleUser,
			wantErr: false,
		},
		{
			name:    "assistant role",
			role:    "assistant",
			want:    model.RoleAssistant,
			wantErr: false,
		},
		{
			name:    "tool role",
			role:    "tool",
			want:    model.RoleTool,
			wantErr: false,
		},
		{
			name:    "invalid role",
			role:    "invalid",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := conv.convertRole(tt.role)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func TestConverter_convertToResponse(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	tests := []struct {
		name  string
		evt   *event.Event
		check func(t *testing.T, resp *openAIResponse)
	}{
		{
			name: "valid event with message",
			evt: &event.Event{
				ID: "event-123",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: "Hello, world!",
							},
						},
					},
					Created: time.Now().Unix(),
				},
			},
			check: func(t *testing.T, resp *openAIResponse) {
				assert.NotNil(t, resp)
				assert.Equal(t, "event-123", resp.ID)
				assert.Equal(t, objectChatCompletion, resp.Object)
				assert.Equal(t, "gpt-3.5-turbo", resp.Model)
				assert.Len(t, resp.Choices, 1)
				assert.Equal(t, "Hello, world!", resp.Choices[0].Message.Content)
			},
		},
		{
			name: "event with usage",
			evt: &event.Event{
				ID: "event-123",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: "Hello",
							},
						},
					},
					Created: time.Now().Unix(),
					Usage: &model.Usage{
						PromptTokens:     10,
						CompletionTokens: 5,
						TotalTokens:      15,
					},
				},
			},
			check: func(t *testing.T, resp *openAIResponse) {
				assert.NotNil(t, resp.Usage)
				assert.Equal(t, 10, resp.Usage.PromptTokens)
				assert.Equal(t, 5, resp.Usage.CompletionTokens)
				assert.Equal(t, 15, resp.Usage.TotalTokens)
			},
		},
		{
			name: "event with finish reason",
			evt: &event.Event{
				ID: "event-123",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: "Hello",
							},
							FinishReason: stringPtr("stop"),
						},
					},
					Created: time.Now().Unix(),
				},
			},
			check: func(t *testing.T, resp *openAIResponse) {
				assert.NotNil(t, resp.Choices[0].FinishReason)
				assert.Equal(t, "stop", *resp.Choices[0].FinishReason)
			},
		},
		{
			name: "event with no choices",
			evt: &event.Event{
				ID: "event-123",
				Response: &model.Response{
					Choices: []model.Choice{},
					Created: time.Now().Unix(),
				},
			},
			check: func(t *testing.T, resp *openAIResponse) {
				assert.Nil(t, resp)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := conv.convertToResponse(tt.evt)
			if tt.check != nil {
				require.NoError(t, err)
				tt.check(t, resp)
			}
		})
	}
}

func TestConverter_convertToChunk(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	tests := []struct {
		name  string
		evt   *event.Event
		check func(t *testing.T, chunk *openAIChunk)
	}{
		{
			name: "valid chunk with delta",
			evt: &event.Event{
				ID: "event-123",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Role:    model.RoleAssistant,
								Content: "Hello",
							},
						},
					},
					Created: time.Now().Unix(),
				},
			},
			check: func(t *testing.T, chunk *openAIChunk) {
				assert.NotNil(t, chunk)
				assert.Equal(t, "event-123", chunk.ID)
				assert.Equal(t, objectChatCompletionChunk, chunk.Object)
				assert.Equal(t, "gpt-3.5-turbo", chunk.Model)
				assert.Len(t, chunk.Choices, 1)
				assert.Equal(t, "Hello", chunk.Choices[0].Delta.Content)
			},
		},
		{
			name: "chunk with finish reason",
			evt: &event.Event{
				ID: "event-123",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Role:    model.RoleAssistant,
								Content: "",
							},
							FinishReason: stringPtr("stop"),
						},
					},
					Created: time.Now().Unix(),
				},
			},
			check: func(t *testing.T, chunk *openAIChunk) {
				assert.NotNil(t, chunk)
				assert.NotNil(t, chunk.Choices[0].FinishReason)
				assert.Equal(t, "stop", *chunk.Choices[0].FinishReason)
			},
		},
		{
			name: "empty delta without finish reason",
			evt: &event.Event{
				ID: "event-123",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Role:    "", // Empty role to ensure delta is truly empty
								Content: "",
							},
							FinishReason: nil,
						},
					},
					Created: time.Now().Unix(),
				},
			},
			check: func(t *testing.T, chunk *openAIChunk) {
				// Empty delta without finish reason should return nil
				assert.Nil(t, chunk)
			},
		},
		{
			name: "chunk with tool calls",
			evt: &event.Event{
				ID: "event-123",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Role: model.RoleAssistant,
								ToolCalls: []model.ToolCall{
									{
										ID:   "call-123",
										Type: "function",
										Function: model.FunctionDefinitionParam{
											Name:      "test_function",
											Arguments: []byte(`{"arg": "value"}`),
										},
									},
								},
							},
						},
					},
					Created: time.Now().Unix(),
				},
			},
			check: func(t *testing.T, chunk *openAIChunk) {
				assert.NotNil(t, chunk)
				assert.Len(t, chunk.Choices[0].Delta.ToolCalls, 1)
				assert.Equal(t, "call-123", chunk.Choices[0].Delta.ToolCalls[0].ID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk, err := conv.convertToChunk(tt.evt)
			if tt.check != nil {
				require.NoError(t, err)
				tt.check(t, chunk)
			}
		})
	}
}

func TestConverter_convertModelMessageToOpenAI(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	tests := []struct {
		name  string
		msg   model.Message
		check func(t *testing.T, openAIMsg *openAIMessage)
	}{
		{
			name: "simple message",
			msg: model.Message{
				Role:    model.RoleUser,
				Content: "Hello",
			},
			check: func(t *testing.T, openAIMsg *openAIMessage) {
				assert.Equal(t, "user", openAIMsg.Role)
				assert.Equal(t, "Hello", openAIMsg.Content)
			},
		},
		{
			name: "message with tool calls",
			msg: model.Message{
				Role:    model.RoleAssistant,
				Content: "",
				ToolCalls: []model.ToolCall{
					{
						ID:   "call-123",
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "test_function",
							Arguments: []byte(`{"arg": "value"}`),
						},
					},
				},
			},
			check: func(t *testing.T, openAIMsg *openAIMessage) {
				assert.Len(t, openAIMsg.ToolCalls, 1)
				assert.Equal(t, "call-123", openAIMsg.ToolCalls[0].ID)
				assert.Equal(t, "test_function", openAIMsg.ToolCalls[0].Function.Name)
				assert.Equal(t, `{"arg": "value"}`, openAIMsg.ToolCalls[0].Function.Arguments)
			},
		},
		{
			name: "tool response message",
			msg: model.Message{
				Role:     model.RoleTool,
				Content:  "result",
				ToolID:   "call-123",
				ToolName: "test_function",
			},
			check: func(t *testing.T, openAIMsg *openAIMessage) {
				assert.Equal(t, "tool", openAIMsg.Role)
				assert.Equal(t, "call-123", openAIMsg.ToolCallID)
				assert.Equal(t, "test_function", openAIMsg.Name)
				assert.Equal(t, "result", openAIMsg.Content)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openAIMsg, err := conv.convertModelMessageToOpenAI(tt.msg)
			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, openAIMsg)
			}
		})
	}
}

func TestConverter_aggregateStreamingEvents(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	tests := []struct {
		name    string
		events  []*event.Event
		wantErr bool
		check   func(t *testing.T, resp *openAIResponse)
	}{
		{
			name: "aggregate streaming events",
			events: []*event.Event{
				{
					ID: "event-1",
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Delta: model.Message{
									Content: "Hello",
								},
							},
						},
					},
				},
				{
					ID: "event-2",
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Delta: model.Message{
									Content: " world",
								},
							},
						},
					},
				},
				{
					ID: "event-3",
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Delta: model.Message{
									Content: "!",
								},
							},
						},
						Usage: &model.Usage{
							PromptTokens:     10,
							CompletionTokens: 3,
							TotalTokens:      13,
						},
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, resp *openAIResponse) {
				assert.NotNil(t, resp)
				assert.Equal(t, "event-3", resp.ID)
				assert.Equal(t, "Hello world!", resp.Choices[0].Message.Content)
				assert.NotNil(t, resp.Usage)
				assert.Equal(t, 10, resp.Usage.PromptTokens)
			},
		},
		{
			name:    "empty events",
			events:  []*event.Event{},
			wantErr: true,
		},
		{
			name: "events with tool calls",
			events: []*event.Event{
				{
					ID: "event-1",
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Delta: model.Message{
									ToolCalls: []model.ToolCall{
										{
											ID:   "call-1",
											Type: "function",
											Function: model.FunctionDefinitionParam{
												Name: "test",
											},
										},
									},
								},
							},
						},
						Usage: &model.Usage{
							PromptTokens:     5,
							CompletionTokens: 2,
							TotalTokens:      7,
						},
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, resp *openAIResponse) {
				assert.NotNil(t, resp)
				assert.Len(t, resp.Choices[0].Message.ToolCalls, 1)
				assert.NotNil(t, resp.Choices[0].FinishReason)
				assert.Equal(t, "tool_calls", *resp.Choices[0].FinishReason)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := conv.aggregateStreamingEvents(tt.events)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, resp)
			} else {
				assert.NoError(t, err)
				if tt.check != nil {
					tt.check(t, resp)
				}
			}
		})
	}
}

func TestGenerateResponseID(t *testing.T) {
	id1 := generateResponseID()
	id2 := generateResponseID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2)
	assert.Contains(t, id1, "chatcmpl-")
	assert.Contains(t, id2, "chatcmpl-")
}

func TestFormatError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		errorType string
		check     func(t *testing.T, errResp *openAIError)
	}{
		{
			name:      "with error type",
			err:       assert.AnError,
			errorType: errorTypeInvalidRequest,
			check: func(t *testing.T, errResp *openAIError) {
				assert.Equal(t, errorTypeInvalidRequest, errResp.Error.Type)
				assert.NotEmpty(t, errResp.Error.Message)
			},
		},
		{
			name:      "empty error type",
			err:       assert.AnError,
			errorType: "",
			check: func(t *testing.T, errResp *openAIError) {
				assert.Equal(t, errorTypeInvalidRequest, errResp.Error.Type)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errResp := formatError(tt.err, tt.errorType)
			if tt.check != nil {
				tt.check(t, errResp)
			}
		})
	}
}

func TestParseFloat64(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		wantErr bool
		check   func(t *testing.T, result *float64)
	}{
		{
			name:    "float64",
			input:   float64(3.14),
			wantErr: false,
			check: func(t *testing.T, result *float64) {
				assert.NotNil(t, result)
				assert.Equal(t, 3.14, *result)
			},
		},
		{
			name:    "float32",
			input:   float32(3.14),
			wantErr: false,
			check: func(t *testing.T, result *float64) {
				assert.NotNil(t, result)
				// Allow small floating point precision differences
				assert.InDelta(t, 3.14, *result, 0.0001)
			},
		},
		{
			name:    "int",
			input:   42,
			wantErr: false,
			check: func(t *testing.T, result *float64) {
				assert.NotNil(t, result)
				assert.Equal(t, float64(42), *result)
			},
		},
		{
			name:    "string",
			input:   "3.14",
			wantErr: false,
			check: func(t *testing.T, result *float64) {
				assert.NotNil(t, result)
				assert.Equal(t, 3.14, *result)
			},
		},
		{
			name:    "nil",
			input:   nil,
			wantErr: false,
			check: func(t *testing.T, result *float64) {
				assert.Nil(t, result)
			},
		},
		{
			name:    "invalid type",
			input:   []string{"invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseFloat64(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.check != nil {
					tt.check(t, result)
				}
			}
		})
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		wantErr bool
		check   func(t *testing.T, result *int)
	}{
		{
			name:    "int",
			input:   42,
			wantErr: false,
			check: func(t *testing.T, result *int) {
				assert.NotNil(t, result)
				assert.Equal(t, 42, *result)
			},
		},
		{
			name:    "int64",
			input:   int64(42),
			wantErr: false,
			check: func(t *testing.T, result *int) {
				assert.NotNil(t, result)
				assert.Equal(t, 42, *result)
			},
		},
		{
			name:    "float64",
			input:   float64(42.0),
			wantErr: false,
			check: func(t *testing.T, result *int) {
				assert.NotNil(t, result)
				assert.Equal(t, 42, *result)
			},
		},
		{
			name:    "string",
			input:   "42",
			wantErr: false,
			check: func(t *testing.T, result *int) {
				assert.NotNil(t, result)
				assert.Equal(t, 42, *result)
			},
		},
		{
			name:    "nil",
			input:   nil,
			wantErr: false,
			check: func(t *testing.T, result *int) {
				assert.Nil(t, result)
			},
		},
		{
			name:    "invalid type",
			input:   []string{"invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseInt(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.check != nil {
					tt.check(t, result)
				}
			}
		})
	}
}

// parseFloat64 parses a float64 from interface{}.
func parseFloat64(v any) (*float64, error) {
	if v == nil {
		return nil, nil
	}
	switch val := v.(type) {
	case float64:
		return &val, nil
	case float32:
		f := float64(val)
		return &f, nil
	case int:
		f := float64(val)
		return &f, nil
	case int64:
		f := float64(val)
		return &f, nil
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, err
		}
		return &f, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to float64", v)
	}
}

// parseInt parses an int from interface{}.
func parseInt(v any) (*int, error) {
	if v == nil {
		return nil, nil
	}
	switch val := v.(type) {
	case int:
		return &val, nil
	case int64:
		i := int(val)
		return &i, nil
	case float64:
		i := int(val)
		return &i, nil
	case string:
		i, err := strconv.Atoi(val)
		if err != nil {
			return nil, err
		}
		return &i, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to int", v)
	}
}

func TestConverter_convertMessage_MultimodalMultipleText(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	msg := openAIMessage{
		Role: "user",
		Content: []any{
			map[string]any{
				"type": "text",
				"text": "First",
			},
			map[string]any{
				"type": "text",
				"text": "Second",
			},
		},
	}

	result, err := conv.convertMessage(msg)
	require.NoError(t, err)
	assert.Equal(t, "First\nSecond", result.Content)
}

func TestConverter_convertMessage_MultimodalWithImageDetail(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	msg := openAIMessage{
		Role: "user",
		Content: []any{
			map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url":    "https://example.com/image.jpg",
					"detail": "high",
				},
			},
		},
	}

	result, err := conv.convertMessage(msg)
	require.NoError(t, err)
	// Note: ImageURL handling would need to check the message's image URLs.
	// This test verifies the code path is executed.
	assert.NotNil(t, result)
}

func TestConverter_convertMessage_MultimodalInvalidPart(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	// Create a part that cannot be marshaled/unmarshaled properly.
	msg := openAIMessage{
		Role: "user",
		Content: []any{
			map[string]any{
				"type": "text",
				"text": "Hello",
			},
			// Invalid part that will be skipped.
			make(chan int),
		},
	}

	result, err := conv.convertMessage(msg)
	require.NoError(t, err)
	assert.Equal(t, "Hello", result.Content)
}

func TestConverter_convertToChunk_WithRole(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	evt := &event.Event{
		ID: "event-123",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Role:    model.RoleAssistant,
						Content: "",
					},
				},
			},
			Created: time.Now().Unix(),
		},
	}

	chunk, err := conv.convertToChunk(evt)
	require.NoError(t, err)
	assert.NotNil(t, chunk)
	assert.Equal(t, "assistant", chunk.Choices[0].Delta.Role)
}

func TestConverter_convertToChunk_WithToolCalls(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	evt := &event.Event{
		ID: "event-123",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								ID:   "call-1",
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name: "test",
								},
							},
						},
					},
				},
			},
			Created: time.Now().Unix(),
		},
	}

	chunk, err := conv.convertToChunk(evt)
	require.NoError(t, err)
	assert.NotNil(t, chunk)
	assert.Len(t, chunk.Choices[0].Delta.ToolCalls, 1)
}

func TestConverter_convertToChunk_ContentNil(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	evt := &event.Event{
		ID: "event-123",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Role: model.RoleAssistant,
					},
					FinishReason: stringPtr("stop"),
				},
			},
			Created: time.Now().Unix(),
		},
	}

	chunk, err := conv.convertToChunk(evt)
	require.NoError(t, err)
	assert.NotNil(t, chunk)
	assert.NotNil(t, chunk.Choices[0].FinishReason)
}

func TestConverter_aggregateStreamingEvents_NoFinalEvent(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	events := []*event.Event{
		{
			ID: "event-1",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Delta: model.Message{
							Content: "Hello",
						},
					},
				},
			},
		},
		{
			ID: "event-2",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Delta: model.Message{
							Content: " world",
						},
					},
				},
			},
		},
	}

	resp, err := conv.aggregateStreamingEvents(events)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	// Should use the last event.
	assert.Equal(t, "event-2", resp.ID)
	assert.Equal(t, "Hello world", resp.Choices[0].Message.Content)
}

func TestConverter_aggregateStreamingEvents_NonStreamingMessageContent(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	events := []*event.Event{
		{
			ID: "event-1",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Content: "Hello world",
						},
					},
				},
			},
		},
	}

	resp, err := conv.aggregateStreamingEvents(events)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "Hello world", resp.Choices[0].Message.Content)
}

func TestConverter_aggregateStreamingEvents_NonStreamingToolCalls(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	events := []*event.Event{
		{
			ID: "event-1",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: []model.ToolCall{
								{
									ID:   "call-1",
									Type: "function",
									Function: model.FunctionDefinitionParam{
										Name: "test",
									},
								},
							},
						},
					},
				},
				Usage: &model.Usage{
					PromptTokens:     5,
					CompletionTokens: 2,
					TotalTokens:      7,
				},
			},
		},
	}

	resp, err := conv.aggregateStreamingEvents(events)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	assert.NotNil(t, resp.Choices[0].FinishReason)
	assert.Equal(t, "tool_calls", *resp.Choices[0].FinishReason)
}

func TestConverter_aggregateStreamingEvents_FrameworkFinishReason(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	finishReason := "length"
	events := []*event.Event{
		{
			ID: "event-1",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Delta: model.Message{
							Content: "Hello",
						},
						FinishReason: &finishReason,
					},
				},
				Usage: &model.Usage{
					PromptTokens:     5,
					CompletionTokens: 2,
					TotalTokens:      7,
				},
			},
		},
	}

	resp, err := conv.aggregateStreamingEvents(events)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.Choices[0].FinishReason)
	assert.Equal(t, "length", *resp.Choices[0].FinishReason)
}

func TestConverter_convertModelMessageToOpenAI_EmptyContent(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	msg := model.Message{
		Role:    model.RoleAssistant,
		Content: "",
	}

	result, err := conv.convertModelMessageToOpenAI(msg)
	require.NoError(t, err)
	assert.Equal(t, "assistant", result.Role)
	assert.Nil(t, result.Content)
}

func TestConverter_convertModelMessageToOpenAI_WithToolID(t *testing.T) {
	conv := newConverter("gpt-3.5-turbo")

	msg := model.Message{
		Role:     model.RoleTool,
		Content:  "result",
		ToolID:   "call-123",
		ToolName: "test_function",
	}

	result, err := conv.convertModelMessageToOpenAI(msg)
	require.NoError(t, err)
	assert.Equal(t, "tool", result.Role)
	assert.Equal(t, "call-123", result.ToolCallID)
	assert.Equal(t, "test_function", result.Name)
	assert.Equal(t, "result", result.Content)
}
