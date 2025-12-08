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
			name: "tool response with empty content",
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-125",
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:     model.RoleTool,
								ToolID:   "call-2",
								ToolName: "test_tool",
								Content:  "",
							},
						},
					},
				},
			},
			wantPart: true,
			wantErr:  false,
		},
		{
			name: "multiple tool responses",
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-126",
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:     model.RoleTool,
								ToolID:   "call-1",
								ToolName: "tool1",
								Content:  "response1",
							},
						},
						{
							Message: model.Message{
								Role:     model.RoleTool,
								ToolID:   "call-2",
								ToolName: "tool2",
								Content:  "response2",
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

func TestConvertToolCallToA2AStreamingMessage(t *testing.T) {
	tests := []struct {
		name    string
		event   *event.Event
		wantNil bool
		wantErr bool
	}{
		{
			name: "streaming tool call",
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-stream-1",
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolCalls: []model.ToolCall{
									{
										ID:   "call-stream-1",
										Type: "function",
										Function: model.FunctionDefinitionParam{
											Name:      "stream_tool",
											Arguments: []byte(`{"param":"value"}`),
										},
									},
								},
							},
						},
					},
				},
			},
			wantNil: false,
			wantErr: false,
		},
		{
			name: "streaming tool call conversion error",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{},
				},
			},
			wantNil: true,
			wantErr: false,
		},
	}

	converter := &defaultEventToA2AMessage{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := converter.convertToolCallToA2AStreamingMessage(
				tt.event,
				EventToA2AStreamingOptions{CtxID: "test-ctx", TaskID: "test-task"},
			)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertToolCallToA2AStreamingMessage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantNil {
				if result != nil {
					t.Error("convertToolCallToA2AStreamingMessage() expected nil result")
				}
			} else {
				if result == nil {
					t.Error("convertToolCallToA2AStreamingMessage() expected non-nil result")
					return
				}
				taskEvent, ok := result.(*protocol.TaskArtifactUpdateEvent)
				if !ok {
					t.Errorf("Expected TaskArtifactUpdateEvent, got %T", result)
					return
				}
				if taskEvent.TaskID != "test-task" || taskEvent.ContextID != "test-ctx" {
					t.Error("TaskArtifactUpdateEvent has incorrect TaskID or ContextID")
				}
			}
		})
	}
}

func TestDefaultA2AMessageToAgentMessage_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		message protocol.Message
		wantErr bool
	}{
		{
			name: "file part with bytes but missing name and mimetype",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.FilePart{
						File: &protocol.FileWithBytes{
							Name:     nil,
							MimeType: nil,
							Bytes:    "content",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "file part with URI but missing name and mimetype",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.FilePart{
						File: &protocol.FileWithURI{
							Name:     nil,
							MimeType: nil,
							URI:      "file://test",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid text part (wrong type)",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.DataPart{
						Kind: protocol.KindText,
						Data: "not a text part",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid file part (wrong type)",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.DataPart{
						Kind: protocol.KindFile,
						Data: "not a file part",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid data part (wrong type)",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{
						Kind: protocol.KindData,
						Text: "not a data part",
					},
				},
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
			if result == nil {
				t.Error("ConvertToAgentMessage() returned nil result")
			}
		})
	}
}

func TestDefaultEventToA2AMessage_CodeExecution(t *testing.T) {
	tests := []struct {
		name             string
		adkCompatibility bool
		event            *event.Event
		checkResult      func(*testing.T, protocol.UnaryMessageResult)
	}{
		{
			name:             "code execution event - ADK mode",
			adkCompatibility: true,
			event: &event.Event{
				Tag: event.CodeExecutionTag,
				Response: &model.Response{
					ID:     "resp-ce-1",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "print('hello world')",
							},
						},
					},
				},
			},
			checkResult: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("Expected Message type, got %T", result)
					return
				}
				if len(msg.Parts) == 0 {
					t.Error("Expected at least one part")
					return
				}
				part := msg.Parts[0]
				if part.GetKind() != protocol.KindData {
					t.Errorf("Expected DataPart kind, got %s", part.GetKind())
					return
				}
				dataPart, ok := part.(protocol.DataPart)
				if !ok {
					dataPart2, ok2 := part.(*protocol.DataPart)
					if !ok2 {
						t.Errorf("Expected DataPart type, got %T", part)
						return
					}
					dataPart = *dataPart2
				}
				if dataPart.Metadata == nil {
					t.Error("Expected metadata")
					return
				}
				if dataPart.Metadata["adk_type"] != "executable_code" {
					t.Errorf("Expected adk_type 'executable_code', got %v", dataPart.Metadata["adk_type"])
				}
				data, ok := dataPart.Data.(map[string]any)
				if !ok {
					t.Errorf("Expected map data, got %T", dataPart.Data)
					return
				}
				if data["code"] != "print('hello world')" {
					t.Errorf("Expected code content, got %v", data["code"])
				}
			},
		},
		{
			name:             "code execution event - non-ADK mode",
			adkCompatibility: false,
			event: &event.Event{
				Tag: event.CodeExecutionTag,
				Response: &model.Response{
					ID:     "resp-ce-2",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "print('hello')",
							},
						},
					},
				},
			},
			checkResult: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("Expected Message type, got %T", result)
					return
				}
				if len(msg.Parts) == 0 {
					t.Error("Expected at least one part")
					return
				}
				part := msg.Parts[0]
				dataPart, ok := part.(protocol.DataPart)
				if !ok {
					dataPart2, ok2 := part.(*protocol.DataPart)
					if !ok2 {
						t.Errorf("Expected DataPart type, got %T", part)
						return
					}
					dataPart = *dataPart2
				}
				if dataPart.Metadata["type"] != "executable_code" {
					t.Errorf("Expected type 'executable_code', got %v", dataPart.Metadata["type"])
				}
				data, ok := dataPart.Data.(map[string]any)
				if !ok {
					t.Errorf("Expected map data, got %T", dataPart.Data)
					return
				}
				if data["content"] != "print('hello')" {
					t.Errorf("Expected content field in non-ADK mode, got %v", data)
				}
			},
		},
		{
			name:             "code execution result event - ADK mode",
			adkCompatibility: true,
			event: &event.Event{
				Tag: event.CodeExecutionResultTag,
				Response: &model.Response{
					ID:     "resp-cer-1",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "hello world",
							},
						},
					},
				},
			},
			checkResult: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("Expected Message type, got %T", result)
					return
				}
				if len(msg.Parts) == 0 {
					t.Error("Expected at least one part")
					return
				}
				part := msg.Parts[0]
				dataPart, ok := part.(protocol.DataPart)
				if !ok {
					dataPart2, ok2 := part.(*protocol.DataPart)
					if !ok2 {
						t.Errorf("Expected DataPart type, got %T", part)
						return
					}
					dataPart = *dataPart2
				}
				if dataPart.Metadata["adk_type"] != "code_execution_result" {
					t.Errorf("Expected adk_type 'code_execution_result', got %v", dataPart.Metadata["adk_type"])
				}
				data, ok := dataPart.Data.(map[string]any)
				if !ok {
					t.Errorf("Expected map data, got %T", dataPart.Data)
					return
				}
				if data["output"] != "hello world" {
					t.Errorf("Expected output content, got %v", data["output"])
				}
			},
		},
		{
			name:             "code execution result event - non-ADK mode",
			adkCompatibility: false,
			event: &event.Event{
				Tag: event.CodeExecutionResultTag,
				Response: &model.Response{
					ID:     "resp-cer-2",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "execution output",
							},
						},
					},
				},
			},
			checkResult: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("Expected Message type, got %T", result)
					return
				}
				if len(msg.Parts) == 0 {
					t.Error("Expected at least one part")
					return
				}
				part := msg.Parts[0]
				dataPart, ok := part.(protocol.DataPart)
				if !ok {
					dataPart2, ok2 := part.(*protocol.DataPart)
					if !ok2 {
						t.Errorf("Expected DataPart type, got %T", part)
						return
					}
					dataPart = *dataPart2
				}
				if dataPart.Metadata["type"] != "code_execution_result" {
					t.Errorf("Expected type 'code_execution_result', got %v", dataPart.Metadata["type"])
				}
				data, ok := dataPart.Data.(map[string]any)
				if !ok {
					t.Errorf("Expected map data, got %T", dataPart.Data)
					return
				}
				if data["content"] != "execution output" {
					t.Errorf("Expected content field, got %v", data)
				}
			},
		},
		{
			name:             "code execution with empty content",
			adkCompatibility: false,
			event: &event.Event{
				Response: &model.Response{
					ID:     "resp-ce-empty",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "",
							},
						},
					},
				},
			},
			checkResult: func(t *testing.T, result protocol.UnaryMessageResult) {
				if result != nil {
					t.Errorf("Expected nil result for empty content, got %v", result)
				}
			},
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := &defaultEventToA2AMessage{adkCompatibility: tt.adkCompatibility}
			result, err := converter.ConvertToA2AMessage(ctx, tt.event, EventToA2AUnaryOptions{CtxID: "test-ctx"})
			if err != nil {
				t.Errorf("ConvertToA2AMessage() unexpected error: %v", err)
				return
			}
			if tt.checkResult != nil {
				tt.checkResult(t, result)
			}
		})
	}
}

func TestIsCodeExecutionEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    *event.Event
		expected bool
	}{
		{
			name: "code execution event",
			event: &event.Event{
				Response: &model.Response{
					Object: model.ObjectTypePostprocessingCodeExecution,
				},
			},
			expected: true,
		},
		{
			name: "code execution result event (same object type, different tag)",
			event: &event.Event{
				Tag: event.CodeExecutionResultTag,
				Response: &model.Response{
					Object: model.ObjectTypePostprocessingCodeExecution,
				},
			},
			expected: true,
		},
		{
			name: "regular chat completion",
			event: &event.Event{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletion,
				},
			},
			expected: false,
		},
		{
			name: "nil response",
			event: &event.Event{
				Response: nil,
			},
			expected: false,
		},
		{
			name:     "nil event",
			event:    nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCodeExecutionEvent(tt.event)
			if result != tt.expected {
				t.Errorf("isCodeExecutionEvent() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestConvertCodeExecutionToA2AStreamingMessage tests streaming conversion of code execution events
func TestConvertCodeExecutionToA2AStreamingMessage(t *testing.T) {
	tests := []struct {
		name             string
		adkCompatibility bool
		event            *event.Event
		checkResult      func(*testing.T, protocol.StreamingMessageResult)
	}{
		{
			name:             "streaming code execution event",
			adkCompatibility: true,
			event: &event.Event{
				Tag: event.CodeExecutionTag,
				Response: &model.Response{
					ID:     "resp-stream-ce",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "print('streaming')",
							},
						},
					},
				},
			},
			checkResult: func(t *testing.T, result protocol.StreamingMessageResult) {
				if result == nil {
					t.Fatal("expected non-nil result")
				}
				taskEvent, ok := result.(*protocol.TaskArtifactUpdateEvent)
				if !ok {
					t.Errorf("expected TaskArtifactUpdateEvent, got %T", result)
					return
				}
				// Check metadata includes tag
				if taskEvent.Metadata == nil {
					t.Error("expected metadata")
					return
				}
				if taskEvent.Metadata["tag"] != event.CodeExecutionTag {
					t.Errorf("expected tag '%s', got %v", event.CodeExecutionTag, taskEvent.Metadata["tag"])
				}
				if taskEvent.Metadata["object_type"] != model.ObjectTypePostprocessingCodeExecution {
					t.Errorf("expected object_type '%s', got %v", model.ObjectTypePostprocessingCodeExecution, taskEvent.Metadata["object_type"])
				}
			},
		},
		{
			name:             "streaming code execution result event",
			adkCompatibility: true,
			event: &event.Event{
				Tag: event.CodeExecutionResultTag,
				Response: &model.Response{
					ID:     "resp-stream-cer",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "streaming result",
							},
						},
					},
				},
			},
			checkResult: func(t *testing.T, result protocol.StreamingMessageResult) {
				if result == nil {
					t.Fatal("expected non-nil result")
				}
				taskEvent, ok := result.(*protocol.TaskArtifactUpdateEvent)
				if !ok {
					t.Errorf("expected TaskArtifactUpdateEvent, got %T", result)
					return
				}
				if taskEvent.Metadata["tag"] != event.CodeExecutionResultTag {
					t.Errorf("expected tag '%s', got %v", event.CodeExecutionResultTag, taskEvent.Metadata["tag"])
				}
			},
		},
		{
			name:             "streaming code execution with empty content",
			adkCompatibility: false,
			event: &event.Event{
				Tag: event.CodeExecutionTag,
				Response: &model.Response{
					ID:     "resp-stream-empty",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "",
							},
						},
					},
				},
			},
			checkResult: func(t *testing.T, result protocol.StreamingMessageResult) {
				if result != nil {
					t.Errorf("expected nil result for empty content, got %v", result)
				}
			},
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := &defaultEventToA2AMessage{adkCompatibility: tt.adkCompatibility}
			result, err := converter.ConvertStreamingToA2AMessage(
				ctx, tt.event, EventToA2AStreamingOptions{CtxID: "test-ctx", TaskID: "test-task"},
			)
			if err != nil {
				t.Errorf("ConvertStreamingToA2AMessage() unexpected error: %v", err)
				return
			}
			if tt.checkResult != nil {
				tt.checkResult(t, result)
			}
		})
	}
}

// TestMessageMetadataTag tests that tag is correctly set in message metadata
func TestMessageMetadataTag(t *testing.T) {
	tests := []struct {
		name             string
		adkCompatibility bool
		event            *event.Event
		checkMetadata    func(*testing.T, protocol.UnaryMessageResult)
	}{
		{
			name:             "content message with tag",
			adkCompatibility: false,
			event: &event.Event{
				Tag: "custom_tag",
				Response: &model.Response{
					ID:     "resp-tag-1",
					Object: model.ObjectTypeChatCompletion,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "Hello",
							},
						},
					},
				},
			},
			checkMetadata: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("expected Message type, got %T", result)
					return
				}
				if msg.Metadata == nil {
					t.Error("expected metadata")
					return
				}
				if msg.Metadata["tag"] != "custom_tag" {
					t.Errorf("expected tag 'custom_tag', got %v", msg.Metadata["tag"])
				}
				if msg.Metadata["object_type"] != model.ObjectTypeChatCompletion {
					t.Errorf("expected object_type '%s', got %v", model.ObjectTypeChatCompletion, msg.Metadata["object_type"])
				}
			},
		},
		{
			name:             "tool call with tag",
			adkCompatibility: false,
			event: &event.Event{
				Tag: "tool_tag",
				Response: &model.Response{
					ID: "resp-tool-tag",
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolCalls: []model.ToolCall{
									{
										ID:   "call-1",
										Type: "function",
										Function: model.FunctionDefinitionParam{
											Name:      "test",
											Arguments: []byte("{}"),
										},
									},
								},
							},
						},
					},
				},
			},
			checkMetadata: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("expected Message type, got %T", result)
					return
				}
				if msg.Metadata == nil {
					t.Error("expected metadata")
					return
				}
				if msg.Metadata["tag"] != "tool_tag" {
					t.Errorf("expected tag 'tool_tag', got %v", msg.Metadata["tag"])
				}
			},
		},
		{
			name:             "code execution with tag",
			adkCompatibility: false,
			event: &event.Event{
				Tag: event.CodeExecutionTag,
				Response: &model.Response{
					ID:     "resp-ce-meta",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "code content",
							},
						},
					},
				},
			},
			checkMetadata: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("expected Message type, got %T", result)
					return
				}
				if msg.Metadata == nil {
					t.Error("expected metadata")
					return
				}
				if msg.Metadata["tag"] != event.CodeExecutionTag {
					t.Errorf("expected tag '%s', got %v", event.CodeExecutionTag, msg.Metadata["tag"])
				}
			},
		},
		{
			name:             "code execution result with tag",
			adkCompatibility: false,
			event: &event.Event{
				Tag: event.CodeExecutionResultTag,
				Response: &model.Response{
					ID:     "resp-cer-meta",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "result content",
							},
						},
					},
				},
			},
			checkMetadata: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("expected Message type, got %T", result)
					return
				}
				if msg.Metadata == nil {
					t.Error("expected metadata")
					return
				}
				if msg.Metadata["tag"] != event.CodeExecutionResultTag {
					t.Errorf("expected tag '%s', got %v", event.CodeExecutionResultTag, msg.Metadata["tag"])
				}
			},
		},
		{
			name:             "message without tag",
			adkCompatibility: false,
			event: &event.Event{
				Tag: "",
				Response: &model.Response{
					ID: "resp-no-tag",
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "no tag",
							},
						},
					},
				},
			},
			checkMetadata: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("expected Message type, got %T", result)
					return
				}
				if msg.Metadata == nil {
					t.Error("expected metadata")
					return
				}
				// Empty tag should still be present in metadata
				if msg.Metadata["tag"] != "" {
					t.Errorf("expected empty tag, got %v", msg.Metadata["tag"])
				}
			},
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := &defaultEventToA2AMessage{adkCompatibility: tt.adkCompatibility}
			result, err := converter.ConvertToA2AMessage(ctx, tt.event, EventToA2AUnaryOptions{CtxID: "test-ctx"})
			if err != nil {
				t.Errorf("ConvertToA2AMessage() unexpected error: %v", err)
				return
			}
			if tt.checkMetadata != nil {
				tt.checkMetadata(t, result)
			}
		})
	}
}

// TestConvertCodeExecutionToA2AMessage_TagDistinction tests that tag correctly distinguishes
// code execution vs result events (both have same ObjectType)
func TestConvertCodeExecutionToA2AMessage_TagDistinction(t *testing.T) {
	tests := []struct {
		name                 string
		adkCompatibility     bool
		event                *event.Event
		expectedDataPartType string
	}{
		{
			name:             "code execution event with code tag - ADK mode",
			adkCompatibility: true,
			event: &event.Event{
				Tag: event.CodeExecutionTag,
				Response: &model.Response{
					ID:     "resp-ce",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "print('hello')",
							},
						},
					},
				},
			},
			expectedDataPartType: "executable_code",
		},
		{
			name:             "code execution result event with result tag - ADK mode",
			adkCompatibility: true,
			event: &event.Event{
				Tag: event.CodeExecutionResultTag,
				Response: &model.Response{
					ID:     "resp-cer",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "hello",
							},
						},
					},
				},
			},
			expectedDataPartType: "code_execution_result",
		},
		{
			name:             "code execution event with code tag - non-ADK mode",
			adkCompatibility: false,
			event: &event.Event{
				Tag: event.CodeExecutionTag,
				Response: &model.Response{
					ID:     "resp-ce-std",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "code content",
							},
						},
					},
				},
			},
			expectedDataPartType: "executable_code",
		},
		{
			name:             "code execution result event with result tag - non-ADK mode",
			adkCompatibility: false,
			event: &event.Event{
				Tag: event.CodeExecutionResultTag,
				Response: &model.Response{
					ID:     "resp-cer-std",
					Object: model.ObjectTypePostprocessingCodeExecution,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "result content",
							},
						},
					},
				},
			},
			expectedDataPartType: "code_execution_result",
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := &defaultEventToA2AMessage{adkCompatibility: tt.adkCompatibility}
			result, err := converter.ConvertToA2AMessage(ctx, tt.event, EventToA2AUnaryOptions{CtxID: "test-ctx"})
			if err != nil {
				t.Errorf("ConvertToA2AMessage() unexpected error: %v", err)
				return
			}

			if result == nil {
				t.Fatal("expected non-nil result")
			}

			msg, ok := result.(*protocol.Message)
			if !ok {
				t.Errorf("expected Message type, got %T", result)
				return
			}

			if len(msg.Parts) == 0 {
				t.Fatal("expected at least one part")
			}

			part := msg.Parts[0]
			dataPart, ok := part.(protocol.DataPart)
			if !ok {
				dataPart2, ok2 := part.(*protocol.DataPart)
				if !ok2 {
					t.Errorf("expected DataPart type, got %T", part)
					return
				}
				dataPart = *dataPart2
			}

			// Check metadata type key based on ADK compatibility
			var actualType string
			if tt.adkCompatibility {
				if val, ok := dataPart.Metadata["adk_type"].(string); ok {
					actualType = val
				}
			} else {
				if val, ok := dataPart.Metadata["type"].(string); ok {
					actualType = val
				}
			}

			if actualType != tt.expectedDataPartType {
				t.Errorf("expected DataPart type '%s', got '%s'", tt.expectedDataPartType, actualType)
			}
		})
	}
}

func TestDefaultEventToA2AMessage_ADKCompatibility(t *testing.T) {
	tests := []struct {
		name             string
		adkCompatibility bool
		event            *event.Event
		checkMetadata    func(*testing.T, protocol.UnaryMessageResult)
	}{
		{
			name:             "ADK compatibility enabled - tool call",
			adkCompatibility: true,
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-adk-1",
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolCalls: []model.ToolCall{
									{
										ID:   "call-adk",
										Type: "function",
										Function: model.FunctionDefinitionParam{
											Name:      "adk_tool",
											Arguments: []byte(`{}`),
										},
									},
								},
							},
						},
					},
				},
			},
			checkMetadata: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("Expected Message type, got %T", result)
					return
				}
				if len(msg.Parts) == 0 {
					t.Error("Expected at least one part")
					return
				}
				part := msg.Parts[0]
				if part.GetKind() != protocol.KindData {
					t.Errorf("Expected DataPart kind, got %s", part.GetKind())
					return
				}
				dataPart, ok := part.(protocol.DataPart)
				if !ok {
					dataPart2, ok2 := part.(*protocol.DataPart)
					if !ok2 {
						t.Errorf("Expected DataPart type (value or pointer), got %T", part)
						return
					}
					dataPart = *dataPart2
				}
				if dataPart.Metadata == nil {
					t.Error("Expected metadata")
					return
				}
				if _, hasADKType := dataPart.Metadata["adk_type"]; !hasADKType {
					t.Error("Expected adk_type in metadata when ADK compatibility is enabled")
				}
			},
		},
		{
			name:             "ADK compatibility disabled - tool call",
			adkCompatibility: false,
			event: &event.Event{
				Response: &model.Response{
					ID: "resp-std-1",
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolCalls: []model.ToolCall{
									{
										ID:   "call-std",
										Type: "function",
										Function: model.FunctionDefinitionParam{
											Name:      "std_tool",
											Arguments: []byte(`{}`),
										},
									},
								},
							},
						},
					},
				},
			},
			checkMetadata: func(t *testing.T, result protocol.UnaryMessageResult) {
				msg, ok := result.(*protocol.Message)
				if !ok {
					t.Errorf("Expected Message type, got %T", result)
					return
				}
				if len(msg.Parts) == 0 {
					t.Error("Expected at least one part")
					return
				}
				part := msg.Parts[0]
				if part.GetKind() != protocol.KindData {
					t.Errorf("Expected DataPart kind, got %s", part.GetKind())
					return
				}
				dataPart, ok := part.(protocol.DataPart)
				if !ok {
					dataPart2, ok2 := part.(*protocol.DataPart)
					if !ok2 {
						t.Errorf("Expected DataPart type (value or pointer), got %T", part)
						return
					}
					dataPart = *dataPart2
				}
				if dataPart.Metadata == nil {
					t.Error("Expected metadata")
					return
				}
				if _, hasType := dataPart.Metadata["type"]; !hasType {
					t.Error("Expected type in metadata when ADK compatibility is disabled")
				}
				if _, hasADKType := dataPart.Metadata["adk_type"]; hasADKType {
					t.Error("Should not have adk_type in metadata when ADK compatibility is disabled")
				}
			},
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := &defaultEventToA2AMessage{adkCompatibility: tt.adkCompatibility}
			result, err := converter.ConvertToA2AMessage(ctx, tt.event, EventToA2AUnaryOptions{CtxID: "test-ctx"})
			if err != nil {
				t.Errorf("ConvertToA2AMessage() unexpected error: %v", err)
				return
			}
			if tt.checkMetadata != nil {
				tt.checkMetadata(t, result)
			}
		})
	}
}

// TestA2AMessageToAgentMessage_PointerAndValueTypes tests the fix for supporting
// both pointer and value types in protocol parts to prevent silent content loss.
// This addresses the bug where type assertions only checked for pointer types,
// causing value types to be silently skipped.
func TestA2AMessageToAgentMessage_PointerAndValueTypes(t *testing.T) {
	tests := []struct {
		name     string
		message  protocol.Message
		expected *model.Message
		wantErr  bool
	}{
		{
			name: "text part as value type (not pointer)",
			message: protocol.Message{
				Parts: []protocol.Part{
					protocol.TextPart{Text: "Value type text"},
				},
			},
			expected: &model.Message{
				Role:         model.RoleUser,
				Content:      "Value type text",
				ContentParts: []model.ContentPart{},
			},
			wantErr: false,
		},
		{
			name: "text part as pointer type",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "Pointer type text"},
				},
			},
			expected: &model.Message{
				Role:         model.RoleUser,
				Content:      "Pointer type text",
				ContentParts: []model.ContentPart{},
			},
			wantErr: false,
		},
		{
			name: "mixed pointer and value text parts",
			message: protocol.Message{
				Parts: []protocol.Part{
					protocol.TextPart{Text: "Value "},
					&protocol.TextPart{Text: "and "},
					protocol.TextPart{Text: "pointer"},
				},
			},
			expected: &model.Message{
				Role:         model.RoleUser,
				Content:      "Value and pointer",
				ContentParts: []model.ContentPart{},
			},
			wantErr: false,
		},
		{
			name: "file part as value type",
			message: protocol.Message{
				Parts: []protocol.Part{
					protocol.FilePart{
						File: &protocol.FileWithBytes{
							Name:     stringPtr("value_file.txt"),
							MimeType: stringPtr("text/plain"),
							Bytes:    "value type file content",
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
							Name:     "value_file.txt",
							Data:     []byte("value type file content"),
							MimeType: "text/plain",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "file part as pointer type",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.FilePart{
						File: &protocol.FileWithBytes{
							Name:     stringPtr("pointer_file.txt"),
							MimeType: stringPtr("text/plain"),
							Bytes:    "pointer type file content",
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
							Name:     "pointer_file.txt",
							Data:     []byte("pointer type file content"),
							MimeType: "text/plain",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "data part as value type",
			message: protocol.Message{
				Parts: []protocol.Part{
					protocol.DataPart{Data: "value type data"},
				},
			},
			expected: &model.Message{
				Role:    model.RoleUser,
				Content: "",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("value type data"),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "data part as pointer type",
			message: protocol.Message{
				Parts: []protocol.Part{
					&protocol.DataPart{Data: "pointer type data"},
				},
			},
			expected: &model.Message{
				Role:    model.RoleUser,
				Content: "",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("pointer type data"),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "complex mixed types - regression test for silent content loss",
			message: protocol.Message{
				Parts: []protocol.Part{
					protocol.TextPart{Text: "Text as value, "},
					&protocol.TextPart{Text: "text as pointer, "},
					protocol.DataPart{Data: "data as value"},
					&protocol.FilePart{
						File: &protocol.FileWithURI{
							URI:      "https://example.com/file.pdf",
							Name:     stringPtr("document.pdf"),
							MimeType: stringPtr("application/pdf"),
						},
					},
				},
			},
			expected: &model.Message{
				Role:    model.RoleUser,
				Content: "Text as value, text as pointer, ",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("data as value"),
					},
					{
						Type: model.ContentTypeFile,
						File: &model.File{
							Name:     "document.pdf",
							FileID:   "https://example.com/file.pdf",
							MimeType: "application/pdf",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty message with value type part - should not crash",
			message: protocol.Message{
				Parts: []protocol.Part{
					protocol.TextPart{Text: ""},
				},
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

			if tt.wantErr {
				return
			}

			if result == nil {
				t.Fatal("ConvertToAgentMessage() returned nil result")
			}

			// Check role
			if result.Role != tt.expected.Role {
				t.Errorf("Role = %v, expected %v", result.Role, tt.expected.Role)
			}

			// Check content
			if result.Content != tt.expected.Content {
				t.Errorf("Content = %q, expected %q", result.Content, tt.expected.Content)
			}

			// Check content parts length
			if len(result.ContentParts) != len(tt.expected.ContentParts) {
				t.Errorf("ContentParts length = %d, expected %d", len(result.ContentParts), len(tt.expected.ContentParts))
				return
			}

			// Check each content part
			for i, expectedPart := range tt.expected.ContentParts {
				actualPart := result.ContentParts[i]

				if actualPart.Type != expectedPart.Type {
					t.Errorf("ContentParts[%d].Type = %v, expected %v", i, actualPart.Type, expectedPart.Type)
				}

				switch expectedPart.Type {
				case model.ContentTypeText:
					if actualPart.Text == nil || expectedPart.Text == nil {
						if actualPart.Text != expectedPart.Text {
							t.Errorf("ContentParts[%d].Text = %v, expected %v", i, actualPart.Text, expectedPart.Text)
						}
					} else if *actualPart.Text != *expectedPart.Text {
						t.Errorf("ContentParts[%d].Text = %q, expected %q", i, *actualPart.Text, *expectedPart.Text)
					}
				case model.ContentTypeFile:
					if actualPart.File == nil || expectedPart.File == nil {
						if actualPart.File != expectedPart.File {
							t.Errorf("ContentParts[%d].File = %v, expected %v", i, actualPart.File, expectedPart.File)
						}
					} else {
						if actualPart.File.Name != expectedPart.File.Name {
							t.Errorf("ContentParts[%d].File.Name = %q, expected %q", i, actualPart.File.Name, expectedPart.File.Name)
						}
						if actualPart.File.MimeType != expectedPart.File.MimeType {
							t.Errorf("ContentParts[%d].File.MimeType = %q, expected %q", i, actualPart.File.MimeType, expectedPart.File.MimeType)
						}
						if !reflect.DeepEqual(actualPart.File.Data, expectedPart.File.Data) {
							t.Errorf("ContentParts[%d].File.Data = %v, expected %v", i, actualPart.File.Data, expectedPart.File.Data)
						}
						if actualPart.File.FileID != expectedPart.File.FileID {
							t.Errorf("ContentParts[%d].File.FileID = %q, expected %q", i, actualPart.File.FileID, expectedPart.File.FileID)
						}
					}
				}
			}
		})
	}
}
