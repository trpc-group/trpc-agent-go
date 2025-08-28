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
				Role:    model.RoleUser,
				Content: "Hello world",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("Hello world"),
					},
				},
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
				Role:    model.RoleUser,
				Content: "Hello world",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: stringPtr("Hello "),
					},
					{
						Type: model.ContentTypeText,
						Text: stringPtr("world"),
					},
				},
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
						Data: map[string]interface{}{"key": "value"},
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
						Text: stringPtr("Text: "),
					},
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
		expected *protocol.Message
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
			expected: &protocol.Message{
				Role: protocol.MessageRoleAgent,
				Kind: protocol.KindMessage,
				Parts: []protocol.Part{
					protocol.NewTextPart("Hello from agent"),
				},
			},
			wantErr: false,
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
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "Calling tool",
								ToolCalls: []model.ToolCall{
									{
										Type: "function",
										Function: model.FunctionDefinitionParam{
											Name: "test_tool",
										},
									},
								},
							},
						},
					},
				},
			},
			expected: nil,
			wantErr:  false,
		},
		{
			name: "event with tool role",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleTool,
								Content: "Tool response",
							},
						},
					},
				},
			},
			expected: nil,
			wantErr:  false,
		},
		{
			name: "event with tool ID",
			event: &event.Event{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "Tool response",
								ToolID:  "tool123",
							},
						},
					},
				},
			},
			expected: nil,
			wantErr:  false,
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
			result, err := converter.ConvertToA2AMessage(ctx, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertToA2AMessage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !compareProtocolMessages(result, tt.expected) {
				t.Errorf("ConvertToA2AMessage() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestDefaultEventToA2AMessage_ConvertStreamingToA2AMessage(t *testing.T) {
	tests := []struct {
		name     string
		event    *event.Event
		expected *protocol.Message
		wantErr  bool
	}{
		{
			name: "streaming event with delta content",
			event: &event.Event{
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
			expected: &protocol.Message{
				Role: protocol.MessageRoleAgent,
				Kind: protocol.KindMessage,
				Parts: []protocol.Part{
					protocol.NewTextPart("Hello"),
				},
			},
			wantErr: false,
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
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolCalls: []model.ToolCall{
									{
										Type: "function",
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
			expected: nil,
			wantErr:  false,
		},
	}

	converter := &defaultEventToA2AMessage{}
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := converter.ConvertStreamingToA2AMessage(ctx, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertStreamingToA2AMessage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !compareProtocolMessages(result, tt.expected) {
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

func compareProtocolMessages(a, b *protocol.Message) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// Only compare essential fields: Role, Kind, and Parts
	if a.Role != b.Role {
		return false
	}
	// Compare Kind if both are set
	if b.Kind != "" && a.Kind != b.Kind {
		return false
	}
	// Compare Parts using deep equal
	return reflect.DeepEqual(a.Parts, b.Parts)
}