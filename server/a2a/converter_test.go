//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2a

import (
	"context"
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestDefaultA2AMessageToAgentMessage_ConvertToAgentMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  protocol.Message
		expected *model.Message
		wantErr  bool
	}{
		{
			name: "text part only",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "Hello world"},
				},
			},
			expected: &model.Message{
				Role:         model.RoleUser,
				Content:      "Hello world",
				ContentParts: []model.ContentPart{},
			},
			wantErr: false,
		},
		{
			name: "multiple text parts",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "Hello "},
					&protocol.TextPart{Text: "world"},
				},
			},
			expected: &model.Message{
				Role:         model.RoleUser,
				Content:      "Hello world",
				ContentParts: []model.ContentPart{},
			},
			wantErr: false,
		},
		{
			name: "file part with bytes",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.FilePart{
						File: &protocol.FileWithBytes{
							Name:     stringPtr("test.txt"),
							MimeType: stringPtr("text/plain"),
							Bytes:    "file content",
						},
					},
				},
			},
			expected: &model.Message{
				Role:    model.RoleUser,
				Content: "",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeFile,
						File: &model.File{
							Name:     "test.txt",
							Data:     []byte("file content"),
							MimeType: "text/plain",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "file part with URI",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.FilePart{
						File: &protocol.FileWithURI{
							Name:     stringPtr("test.txt"),
							MimeType: stringPtr("text/plain"),
							URI:      "file://test.txt",
						},
					},
				},
			},
			expected: &model.Message{
				Role:    model.RoleUser,
				Content: "",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeFile,
						File: &model.File{
							Name:     "test.txt",
							FileID:   "file://test.txt",
							MimeType: "text/plain",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "data part",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{"key": "value"},
					},
				},
			},
			expected: &model.Message{
				Role:    model.RoleUser,
				Content: "",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("map[key:value]"),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "mixed parts",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "Text: "},
					&protocol.DataPart{Data: "data"},
				},
			},
			expected: &model.Message{
				Role:    model.RoleUser,
				Content: "Text: ",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("data"),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty message",
			message: protocol.Message{
				Parts: []protocol.Part{},
			},
			expected: &model.Message{
				Role:         model.RoleUser,
				Content:      "",
				ContentParts: []model.ContentPart{},
			},
			wantErr: false,
		},
	}

	converter := &defaultA2AMessageToAgentMessage{}
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := converter.ConvertToAgentMessage(ctx, tt.message)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertToAgentMessage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !compareMessages(result, tt.expected) {
				t.Errorf("ConvertToAgentMessage() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestDefaultEventToA2AMessage_ConvertToA2AMessage(t *testing.T) {
	tests := []struct {
		name     string
		event    *event.Event
		expected protocol.UnaryMessageResult
		wantErr  bool
	}{
		{
			name: "event with content",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "Hello from agent",
							},
						},
					},
				},
			},
			expected: func() protocol.UnaryMessageResult {
				msg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{
					protocol.NewTextPart("Hello from agent"),
				})
				return &msg
			}(),
			wantErr: false,
		},
		{
			name: "event with error response",
			event: &event.Event{
				ID: "error-event-123",
				Response: &model.Response{
					Error: &model.ResponseError{
						Message: "Something went wrong",
					},
				},
			},
			expected: nil,
			wantErr:  true,
		},
		{
			name: "event with empty content",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "",
							},
						},
					},
				},
			},
			expected: nil,
			wantErr:  false,
		},
		{
			name: "event with tool calls",
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-tc1",
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "Calling tool",
								ToolCalls: []model.ToolCall{
									{
										ID:   "call-1",
										Type: "function",
										Function: model.FunctionDefinitionParam{
											Name:      "test_tool",
											Arguments: []byte(`{"arg":"value"}`),
										},
									},
								},
							},
						},
					},
				},
			},
			expected: func() protocol.UnaryMessageResult {
				dataPart := protocol.NewDataPart(map[string]any{
					"id":   "call-1",
					"type": "function",
					"name": "test_tool",
					"args": `{"arg":"value"}`,
				})
				dataPart.Metadata = map[string]any{
					"type": "function_call",
				}
				msg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{dataPart})
				return &msg
			}(),
			wantErr: false,
		},
		{
			name: "event with tool role",
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-tr1",
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:     model.RoleTool,
								ToolID:   "call-1",
								ToolName: "test_tool",
								Content:  "Tool response content",
							},
						},
					},
				},
			},
			expected: func() protocol.UnaryMessageResult {
				// Tool responses should be converted to DataPart with function_response metadata
				dataPart := protocol.NewDataPart(map[string]any{
					"name":     "test_tool",
					"id":       "call-1",
					"response": "Tool response content",
				})
				dataPart.Metadata = map[string]any{
					"type": "function_response",
				}
				msg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{dataPart})
				return &msg
			}(),
			wantErr: false,
		},
		{
			name: "event with tool ID",
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-tid1",
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolID:   "tool123",
								ToolName: "tool_func",
								Content:  "Tool response",
							},
						},
					},
				},
			},
			expected: func() protocol.UnaryMessageResult {
				dataPart := protocol.NewDataPart(map[string]any{
					"name":     "tool_func",
					"id":       "tool123",
					"response": "Tool response",
				})
				dataPart.Metadata = map[string]any{
					"type": "function_response",
				}
				msg := protocol.NewMessage(protocol.MessageRoleAgent, []protocol.Part{dataPart})
				return &msg
			}(),
			wantErr: false,
		},
		{
			name: "event with no choices",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{},
				},
			},
			expected: nil,
			wantErr:  false,
		},
		{
			name:     "nil response",
			event:    &event.Event{Response: nil},
			expected: nil,
			wantErr:  false,
		},
	}

	converter := &defaultEventToA2AMessage{}
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := converter.ConvertToA2AMessage(ctx, tt.event, EventToA2AUnaryOptions{CtxID: "test-ctx-id"})
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertToA2AMessage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !compareUnaryMessageResults(result, tt.expected) {
				t.Errorf("ConvertToA2AMessage() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestDefaultEventToA2AMessage_ConvertStreamingToA2AMessage(t *testing.T) {
	tests := []struct {
		name     string
		event    *event.Event
		expected protocol.StreamingMessageResult
		wantErr  bool
	}{
		{
			name: "streaming event with delta content",
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-123",
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Content: "Hello",
							},
						},
					},
				},
			},
			expected: func() protocol.StreamingMessageResult {
				isLastChunk := false
				taskEvent := protocol.NewTaskArtifactUpdateEvent(
					"test-task-id",
					"test-ctx-id",
					protocol.Artifact{
						ArtifactID: "resp-123",
						Parts: []protocol.Part{
							protocol.NewTextPart("Hello"),
						},
					},
					isLastChunk,
				)
				return &taskEvent
			}(),
			wantErr: false,
		},
		{
			name: "streaming event with error response",
			event: &event.Event{
				ID: "error-event-456",
				Response: &model.Response{
					Error: &model.ResponseError{
						Message: "Streaming error",
					},
				},
			},
			expected: nil,
			wantErr:  true,
		},
		{
			name: "streaming event with empty delta",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Content: "",
							},
						},
					},
				},
			},
			expected: nil,
			wantErr:  false,
		},
		{
			name: "streaming event with tool calls",
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-stc1",
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolCalls: []model.ToolCall{
									{
										ID:   "call-1",
										Type: "function",
										Function: model.FunctionDefinitionParam{
											Name:      "test_tool",
											Arguments: []byte(`{"key":"value"}`),
										},
									},
								},
							},
							Delta: model.Message{
								Content: "delta content",
							},
						},
					},
				},
			},
			expected: func() protocol.StreamingMessageResult {
				dataPart := protocol.NewDataPart(map[string]any{
					"id":   "call-1",
					"type": "function",
					"name": "test_tool",
					"args": `{"key":"value"}`,
				})
				dataPart.Metadata = map[string]any{
					"type": "function_call",
				}
				taskEvent := protocol.NewTaskArtifactUpdateEvent(
					"test-task-id",
					"test-ctx-id",
					protocol.Artifact{
						ArtifactID: "resp-stc1",
						Parts:      []protocol.Part{dataPart},
					},
					false,
				)
				return &taskEvent
			}(),
			wantErr: false,
		},
		{
			name: "streaming event with no choices",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{},
				},
			},
			expected: nil,
			wantErr:  false,
		},
		{
			name: "streaming event with nil response",
			event: &event.Event{
				Response: nil,
			},
			expected: nil,
			wantErr:  false,
		},
	}

	converter := &defaultEventToA2AMessage{}
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := converter.ConvertStreamingToA2AMessage(
				ctx, tt.event, EventToA2AStreamingOptions{CtxID: "test-ctx-id", TaskID: "test-task-id"},
			)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertStreamingToA2AMessage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !compareStreamingMessageResults(result, tt.expected) {
				t.Errorf("ConvertStreamingToA2AMessage() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestIsToolCallEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    *event.Event
		expected bool
	}{
		{
			name: "event with tool calls",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolCalls: []model.ToolCall{
									{
										Type: "function",
									},
								},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "event with tool role",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role: model.RoleTool,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "event with tool ID",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolID: "tool123",
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "regular event",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "Hello",
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name:     "nil event",
			event:    nil,
			expected: false,
		},
		{
			name:     "nil response",
			event:    &event.Event{Response: nil},
			expected: false,
		},
		{
			name: "empty choices",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isToolCallEvent(tt.event)
			if result != tt.expected {
				t.Errorf("isToolCallEvent() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func compareMessages(a, b *model.Message) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Role != b.Role || a.Content != b.Content {
		return false
	}
	if len(a.ContentParts) != len(b.ContentParts) {
		return false
	}
	for i, partA := range a.ContentParts {
		partB := b.ContentParts[i]
		if partA.Type != partB.Type {
			return false
		}
		if partA.Text != nil && partB.Text != nil {
			if *partA.Text != *partB.Text {
				return false
			}
		} else if partA.Text != partB.Text {
			return false
		}
		if partA.File != nil && partB.File != nil {
			if partA.File.Name != partB.File.Name ||
				partA.File.MimeType != partB.File.MimeType ||
				partA.File.FileID != partB.File.FileID {
				return false
			}
			if string(partA.File.Data) != string(partB.File.Data) {
				return false
			}
		} else if partA.File != partB.File {
			return false
		}
	}
	return true
}

func compareUnaryMessageResults(a, b protocol.UnaryMessageResult) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Handle protocol.Message comparison
	msgA, okA := a.(*protocol.Message)
	msgB, okB := b.(*protocol.Message)
	if okA && okB {
		return compareProtocolMessages(msgA, msgB)
	}

	// For other types, use deep equal
	return reflect.DeepEqual(a, b)
}

func compareProtocolMessages(a, b *protocol.Message) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Compare fields except MessageID which is dynamically generated
	if a.Role != b.Role || a.Kind != b.Kind {
		return false
	}

	// Compare parts
	if len(a.Parts) != len(b.Parts) {
		return false
	}

	for i, partA := range a.Parts {
		partB := b.Parts[i]
		if !compareProtocolParts(partA, partB) {
			return false
		}
	}

	return true
}

func compareProtocolParts(a, b protocol.Part) bool {
	// Handle nil cases
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Check if both are DataParts
	dataParta, okA := a.(*protocol.DataPart)
	dataPartb, okB := b.(*protocol.DataPart)
	if okA && okB {
		// Compare DataParts by value, not by pointer
		// Note: We don't compare Kind field as it's auto-set by NewDataPart
		if !reflect.DeepEqual(dataParta.Data, dataPartb.Data) {
			return false
		}
		if !reflect.DeepEqual(dataParta.Metadata, dataPartb.Metadata) {
			return false
		}
		return true
	}

	// For other types, use deep equal
	return reflect.DeepEqual(a, b)
}

func compareStreamingMessageResults(a, b protocol.StreamingMessageResult) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Handle TaskArtifactUpdateEvent comparison
	eventA, okA := a.(*protocol.TaskArtifactUpdateEvent)
	eventB, okB := b.(*protocol.TaskArtifactUpdateEvent)
	if okA && okB {
		return compareTaskArtifactUpdateEvents(eventA, eventB)
	}

	// For other types, use deep equal
	return reflect.DeepEqual(a, b)
}

func compareTaskArtifactUpdateEvents(a, b *protocol.TaskArtifactUpdateEvent) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Compare main fields
	if a.TaskID != b.TaskID || a.ContextID != b.ContextID {
		return false
	}

	// Compare LastChunk - handle both nil and non-nil cases
	aLastChunk := false
	if a.LastChunk != nil {
		aLastChunk = *a.LastChunk
	}
	bLastChunk := false
	if b.LastChunk != nil {
		bLastChunk = *b.LastChunk
	}
	if aLastChunk != bLastChunk {
		return false
	}

	// Compare artifacts
	return reflect.DeepEqual(a.Artifact, b.Artifact)
}

func TestConvertToolCallToA2AMessage(t *testing.T) {
	tests := []struct {
		name     string
		event    *event.Event
		wantPart bool
		wantErr  bool
	}{
		{
			name: "tool call request",
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-123",
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolCalls: []model.ToolCall{
									{
										ID:   "call-1",
										Type: "function",
										Function: model.FunctionDefinitionParam{
											Name:      "get_weather",
											Arguments: []byte(`{"location":"Beijing"}`),
										},
									},
								},
							},
						},
					},
				},
			},
			wantPart: true,
			wantErr:  false,
		},
		{
			name: "tool response",
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-124",
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:     model.RoleTool,
								ToolID:   "call-1",
								ToolName: "get_weather",
								Content:  `{"temperature": 25}`,
							},
						},
					},
				},
			},
			wantPart: true,
			wantErr:  false,
		},
		{
			name: "empty choices",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{},
				},
			},
			wantPart: false,
			wantErr:  false,
		},
	}

	converter := &defaultEventToA2AMessage{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := converter.convertToolCallToA2AMessage(tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertToolCallToA2AMessage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantPart {
				if result == nil {
					t.Error("convertToolCallToA2AMessage() returned nil, expected result")
					return
				}
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Error("convertToolCallToA2AMessage() result is not a Message")
					return
				}
				if len(msg.Parts) == 0 {
					t.Error("convertToolCallToA2AMessage() returned message with no parts")
				}
				// Check that all parts are DataParts
				for _, part := range msg.Parts {
					if part.GetKind() != protocol.KindData {
						t.Errorf("Expected DataPart, got %s", part.GetKind())
					}
				}
			} else {
				if result != nil {
					t.Error("convertToolCallToA2AMessage() returned result, expected nil")
				}
			}
		})
	}
}
