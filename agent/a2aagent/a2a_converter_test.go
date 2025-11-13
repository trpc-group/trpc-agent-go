//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestDefaultA2AEventConverter_ConvertToEvent(t *testing.T) {
	type testCase struct {
		name         string
		result       protocol.MessageResult
		agentName    string
		invocation   *agent.Invocation
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, event *event.Event, err error)
	}

	tests := []testCase{
		{
			name: "nil result",
			result: protocol.MessageResult{
				Result: nil,
			},
			agentName: "test-agent",
			invocation: &agent.Invocation{
				InvocationID: "test-id",
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, event *event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if event == nil {
					t.Fatal("expected event, got nil")
				}
				if event.Author != "test-agent" {
					t.Errorf("expected author 'test-agent', got %s", event.Author)
				}
				if event.InvocationID != "test-id" {
					t.Errorf("expected invocation ID 'test-id', got %s", event.InvocationID)
				}
				if event.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if len(event.Response.Choices) != 1 {
					t.Errorf("expected 1 choice, got %d", len(event.Response.Choices))
				}
				if event.Response.Choices[0].Message.Content != "" {
					t.Errorf("expected empty content, got %s", event.Response.Choices[0].Message.Content)
				}
			},
		},
		{
			name: "message result",
			result: protocol.MessageResult{
				Result: &protocol.Message{
					Kind:      protocol.KindMessage,
					MessageID: "msg-123",
					Role:      protocol.MessageRoleAgent,
					Parts:     []protocol.Part{&protocol.TextPart{Kind: protocol.KindText, Text: "Hello, world!"}},
				},
			},
			agentName: "test-agent",
			invocation: &agent.Invocation{
				InvocationID: "test-id",
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, event *event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if event == nil {
					t.Fatal("expected event, got nil")
				}
				if event.Author != "test-agent" {
					t.Errorf("expected author 'test-agent', got %s", event.Author)
				}
				if !event.Done {
					t.Error("expected Done to be true")
				}
				if event.IsPartial {
					t.Error("expected IsPartial to be false")
				}
				if event.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if len(event.Response.Choices) != 1 {
					t.Errorf("expected 1 choice, got %d", len(event.Response.Choices))
				}
				if event.Response.ID != "msg-123" {
					t.Errorf("expected response ID 'msg-123', got %s", event.Response.ID)
				}
				if event.Response.Object != model.ObjectTypeChatCompletion {
					t.Errorf("expected response object %s, got %s", model.ObjectTypeChatCompletion, event.Response.Object)
				}
			},
		},
		{
			name: "task result",
			result: protocol.MessageResult{
				Result: &protocol.Task{
					ID:        "task-1",
					ContextID: "ctx-1",
					Artifacts: []protocol.Artifact{
						{
							ArtifactID: "artifact-1",
							Parts:      []protocol.Part{&protocol.TextPart{Kind: protocol.KindText, Text: "Task content"}},
						},
					},
				},
			},
			agentName: "test-agent",
			invocation: &agent.Invocation{
				InvocationID: "test-id",
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, event *event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if event == nil {
					t.Fatal("expected event, got nil")
				}
				if !event.Done {
					t.Error("expected Done to be true")
				}
				if event.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if len(event.Response.Choices) != 1 {
					t.Errorf("expected 1 choice, got %d", len(event.Response.Choices))
				}
				if event.Response.ID != "artifact-1" {
					t.Errorf("expected response ID 'artifact-1', got %s", event.Response.ID)
				}
				if event.Response.Object != model.ObjectTypeChatCompletion {
					t.Errorf("expected response object %s, got %s", model.ObjectTypeChatCompletion, event.Response.Object)
				}
			},
		},
	}

	converter := &defaultA2AEventConverter{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			event, err := converter.ConvertToEvent(tc.result, tc.agentName, tc.invocation)
			tc.validateFunc(t, event, err)
		})
	}
}

func TestDefaultA2AEventConverter_ConvertStreamingToEvent(t *testing.T) {
	type testCase struct {
		name         string
		result       protocol.StreamingMessageEvent
		agentName    string
		invocation   *agent.Invocation
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, event *event.Event, err error)
	}

	tests := []testCase{
		{
			name: "nil streaming result",
			result: protocol.StreamingMessageEvent{
				Result: nil,
			},
			agentName: "test-agent",
			invocation: &agent.Invocation{
				InvocationID: "test-id",
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, event *event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if event == nil {
					t.Fatal("expected event, got nil")
				}
				if event.Author != "test-agent" {
					t.Errorf("expected author 'test-agent', got %s", event.Author)
				}
				if event.InvocationID != "test-id" {
					t.Errorf("expected invocation ID 'test-id', got %s", event.InvocationID)
				}
			},
		},
		{
			name: "streaming message result",
			result: protocol.StreamingMessageEvent{
				Result: &protocol.Message{
					Kind:      protocol.KindMessage,
					MessageID: "stream-1",
					Role:      protocol.MessageRoleAgent,
					Parts:     []protocol.Part{&protocol.TextPart{Kind: protocol.KindText, Text: "Streaming content"}},
				},
			},
			agentName: "test-agent",
			invocation: &agent.Invocation{
				InvocationID: "test-id",
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, event *event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if event == nil {
					t.Fatal("expected event, got nil")
				}
				if event.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if !event.Response.IsPartial {
					t.Error("expected IsPartial to be true for streaming")
				}
				if event.Response.Done {
					t.Error("expected Done to be false for streaming")
				}
				if len(event.Response.Choices) != 1 {
					t.Errorf("expected 1 choice, got %d", len(event.Response.Choices))
				}
				if event.Response.ID != "stream-1" {
					t.Errorf("expected response ID 'stream-1', got %s", event.Response.ID)
				}
				if event.Response.Object != model.ObjectTypeChatCompletionChunk {
					t.Errorf("expected response object %s, got %s", model.ObjectTypeChatCompletionChunk, event.Response.Object)
				}
			},
		},
		{
			name: "streaming task result",
			result: protocol.StreamingMessageEvent{
				Result: &protocol.Task{
					ID:        "task-1",
					ContextID: "ctx-1",
					Artifacts: []protocol.Artifact{
						{
							ArtifactID: "artifact-1",
							Parts:      []protocol.Part{&protocol.TextPart{Kind: protocol.KindText, Text: "Task streaming content"}},
						},
					},
				},
			},
			agentName: "test-agent",
			invocation: &agent.Invocation{
				InvocationID: "test-id",
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, event *event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if event == nil {
					t.Fatal("expected event, got nil")
				}
				if event.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if event.Response.ID != "artifact-1" {
					t.Errorf("expected response ID 'artifact-1', got %s", event.Response.ID)
				}
				if event.Response.Object != model.ObjectTypeChatCompletionChunk {
					t.Errorf("expected response object %s, got %s", model.ObjectTypeChatCompletionChunk, event.Response.Object)
				}
			},
		},
		{
			name: "task status update event",
			result: protocol.StreamingMessageEvent{
				Result: &protocol.TaskStatusUpdateEvent{
					Kind:      protocol.KindTaskStatusUpdate,
					TaskID:    "task-2",
					ContextID: "ctx-2",
					Status: protocol.TaskStatus{
						Message: &protocol.Message{
							Kind:      protocol.KindMessage,
							MessageID: "status-1",
							Role:      protocol.MessageRoleAgent,
							Parts:     []protocol.Part{&protocol.TextPart{Kind: protocol.KindText, Text: "Status update"}},
						},
					},
				},
			},
			agentName: "test-agent",
			invocation: &agent.Invocation{
				InvocationID: "test-id",
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, event *event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if event == nil {
					t.Fatal("expected event, got nil")
				}
				if event.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if event.Response.ID != "status-1" {
					t.Errorf("expected response ID 'status-1', got %s", event.Response.ID)
				}
				if event.Response.Object != model.ObjectTypeChatCompletionChunk {
					t.Errorf("expected response object %s, got %s", model.ObjectTypeChatCompletionChunk, event.Response.Object)
				}
			},
		},
		{
			name: "task artifact update event",
			result: protocol.StreamingMessageEvent{
				Result: &protocol.TaskArtifactUpdateEvent{
					TaskID:    "task-3",
					ContextID: "ctx-3",
					Artifact: protocol.Artifact{
						ArtifactID: "artifact-99",
						Parts:      []protocol.Part{&protocol.TextPart{Kind: protocol.KindText, Text: "Artifact update"}},
					},
				},
			},
			agentName: "test-agent",
			invocation: &agent.Invocation{
				InvocationID: "test-id",
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, event *event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if event == nil {
					t.Fatal("expected event, got nil")
				}
				if event.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if event.Response.ID != "artifact-99" {
					t.Errorf("expected response ID 'artifact-99', got %s", event.Response.ID)
				}
				if event.Response.Object != model.ObjectTypeChatCompletionChunk {
					t.Errorf("expected response object %s, got %s", model.ObjectTypeChatCompletionChunk, event.Response.Object)
				}
			},
		},
	}

	converter := &defaultA2AEventConverter{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			event, err := converter.ConvertStreamingToEvent(tc.result, tc.agentName, tc.invocation)
			tc.validateFunc(t, event, err)
		})
	}
}

func TestDefaultEventA2AConverter_ConvertToA2AMessage(t *testing.T) {
	type testCase struct {
		name         string
		isStream     bool
		agentName    string
		invocation   *agent.Invocation
		validateFunc func(t *testing.T, msg *protocol.Message, err error)
	}

	tests := []testCase{
		{
			name:      "text content only",
			isStream:  false,
			agentName: "test-agent",
			invocation: &agent.Invocation{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "Hello, world!",
				},
			},
			validateFunc: func(t *testing.T, msg *protocol.Message, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if msg == nil {
					t.Fatal("expected message, got nil")
				}
				if msg.Role != protocol.MessageRoleUser {
					t.Errorf("expected role User, got %s", msg.Role)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part, got %d", len(msg.Parts))
				}
				if textPart, ok := msg.Parts[0].(protocol.TextPart); ok {
					if textPart.Text != "Hello, world!" {
						t.Errorf("expected text 'Hello, world!', got %s", textPart.Text)
					}
				} else {
					t.Error("expected TextPart")
				}
			},
		},
		{
			name:      "content parts with text",
			isStream:  false,
			agentName: "test-agent",
			invocation: &agent.Invocation{
				Message: model.Message{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{
						{
							Type: model.ContentTypeText,
							Text: stringPtr("Text content"),
						},
					},
				},
			},
			validateFunc: func(t *testing.T, msg *protocol.Message, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part, got %d", len(msg.Parts))
				}
				if textPart, ok := msg.Parts[0].(protocol.TextPart); ok {
					if textPart.Text != "Text content" {
						t.Errorf("expected text 'Text content', got %s", textPart.Text)
					}
				} else {
					t.Error("expected TextPart")
				}
			},
		},
		{
			name:      "content parts with image data",
			isStream:  false,
			agentName: "test-agent",
			invocation: &agent.Invocation{
				Message: model.Message{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{
						{
							Type: model.ContentTypeImage,
							Image: &model.Image{
								Format: "png",
								Data:   []byte("image data"),
							},
						},
					},
				},
			},
			validateFunc: func(t *testing.T, msg *protocol.Message, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part, got %d", len(msg.Parts))
				}
				if _, ok := msg.Parts[0].(protocol.FilePart); !ok {
					t.Error("expected FilePart")
				}
			},
		},
		{
			name:      "content parts with image URL",
			isStream:  false,
			agentName: "test-agent",
			invocation: &agent.Invocation{
				Message: model.Message{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{
						{
							Type: model.ContentTypeImage,
							Image: &model.Image{
								Format: "jpg",
								URL:    "https://example.com/image.jpg",
							},
						},
					},
				},
			},
			validateFunc: func(t *testing.T, msg *protocol.Message, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part, got %d", len(msg.Parts))
				}
				if _, ok := msg.Parts[0].(protocol.FilePart); !ok {
					t.Error("expected FilePart")
				}
			},
		},
		{
			name:      "content parts with audio",
			isStream:  false,
			agentName: "test-agent",
			invocation: &agent.Invocation{
				Message: model.Message{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{
						{
							Type: model.ContentTypeAudio,
							Audio: &model.Audio{
								Format: "mp3",
								Data:   []byte("audio data"),
							},
						},
					},
				},
			},
			validateFunc: func(t *testing.T, msg *protocol.Message, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part, got %d", len(msg.Parts))
				}
				if _, ok := msg.Parts[0].(protocol.FilePart); !ok {
					t.Error("expected FilePart")
				}
			},
		},
		{
			name:      "content parts with file",
			isStream:  false,
			agentName: "test-agent",
			invocation: &agent.Invocation{
				Message: model.Message{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{
						{
							Type: model.ContentTypeFile,
							File: &model.File{
								Name:     "test.txt",
								MimeType: "text/plain",
								Data:     []byte("file content"),
							},
						},
					},
				},
			},
			validateFunc: func(t *testing.T, msg *protocol.Message, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part, got %d", len(msg.Parts))
				}
				if _, ok := msg.Parts[0].(protocol.FilePart); !ok {
					t.Error("expected FilePart")
				}
			},
		},
		{
			name:      "empty content",
			isStream:  false,
			agentName: "test-agent",
			invocation: &agent.Invocation{
				Message: model.Message{
					Role: model.RoleUser,
				},
			},
			validateFunc: func(t *testing.T, msg *protocol.Message, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part (empty text), got %d", len(msg.Parts))
				}
				if msg.Parts[0].GetKind() != protocol.KindText {
					t.Errorf("expected text part, got %s", msg.Parts[0].GetKind())
				}
			},
		},
	}

	converter := &defaultEventA2AConverter{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := converter.ConvertToA2AMessage(tc.isStream, tc.agentName, tc.invocation)
			tc.validateFunc(t, msg, err)
		})
	}
}

func TestConvertTaskToMessage(t *testing.T) {
	type testCase struct {
		name         string
		task         *protocol.Task
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, msg *protocol.Message)
	}

	tests := []testCase{
		{
			name: "task with artifacts",
			task: &protocol.Task{
				ID:        "task-1",
				ContextID: "ctx-1",
				Artifacts: []protocol.Artifact{
					{
						ArtifactID: "artifact-1",
						Parts:      []protocol.Part{&protocol.TextPart{Kind: protocol.KindText, Text: "artifact content"}},
					},
				},
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, msg *protocol.Message) {
				if msg.Role != protocol.MessageRoleAgent {
					t.Errorf("expected role Agent, got %s", msg.Role)
				}
				if msg.Kind != protocol.KindMessage {
					t.Errorf("expected kind %s, got %s", protocol.KindMessage, msg.Kind)
				}
				if msg.MessageID != "artifact-1" {
					t.Errorf("expected message ID 'artifact-1', got %s", msg.MessageID)
				}
				if msg.TaskID == nil || *msg.TaskID != "task-1" {
					t.Errorf("expected task ID 'task-1', got %v", msg.TaskID)
				}
				if msg.ContextID == nil || *msg.ContextID != "ctx-1" {
					t.Errorf("expected context ID 'ctx-1', got %v", msg.ContextID)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part, got %d", len(msg.Parts))
				}
				if textPart, ok := msg.Parts[0].(*protocol.TextPart); ok {
					if textPart.Text != "artifact content" {
						t.Errorf("expected text 'artifact content', got %s", textPart.Text)
					}
				} else {
					t.Error("expected TextPart")
				}
			},
		},
		{
			name: "task without artifacts",
			task: &protocol.Task{
				ID:        "task-2",
				ContextID: "ctx-2",
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, msg *protocol.Message) {
				if msg.Role != protocol.MessageRoleAgent {
					t.Errorf("expected role Agent, got %s", msg.Role)
				}
				if msg.Kind != protocol.KindMessage {
					t.Errorf("expected kind %s, got %s", protocol.KindMessage, msg.Kind)
				}
				if msg.MessageID != "" {
					t.Errorf("expected empty message ID, got %s", msg.MessageID)
				}
				if msg.TaskID == nil || *msg.TaskID != "task-2" {
					t.Errorf("expected task ID 'task-2', got %v", msg.TaskID)
				}
				if msg.ContextID == nil || *msg.ContextID != "ctx-2" {
					t.Errorf("expected context ID 'ctx-2', got %v", msg.ContextID)
				}
				if len(msg.Parts) != 0 {
					t.Errorf("expected 0 parts, got %d", len(msg.Parts))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			msg := convertTaskToMessage(tc.task)
			tc.validateFunc(t, msg)
		})
	}
}

func TestConvertTaskStatusToMessage(t *testing.T) {
	type testCase struct {
		name         string
		event        *protocol.TaskStatusUpdateEvent
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, msg *protocol.Message)
	}

	tests := []testCase{
		{
			name: "status with message",
			event: &protocol.TaskStatusUpdateEvent{
				TaskID:    "task-1",
				ContextID: "ctx-1",
				Status: protocol.TaskStatus{
					Message: &protocol.Message{
						Kind:      protocol.KindMessage,
						MessageID: "status-1",
						Role:      protocol.MessageRoleAgent,
						Parts:     []protocol.Part{&protocol.TextPart{Kind: protocol.KindText, Text: "status message"}},
					},
				},
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, msg *protocol.Message) {
				if msg.Role != protocol.MessageRoleAgent {
					t.Errorf("expected role Agent, got %s", msg.Role)
				}
				if msg.Kind != protocol.KindMessage {
					t.Errorf("expected kind %s, got %s", protocol.KindMessage, msg.Kind)
				}
				if msg.MessageID != "status-1" {
					t.Errorf("expected message ID 'status-1', got %s", msg.MessageID)
				}
				if msg.TaskID == nil || *msg.TaskID != "task-1" {
					t.Errorf("expected task ID 'task-1', got %v", msg.TaskID)
				}
				if msg.ContextID == nil || *msg.ContextID != "ctx-1" {
					t.Errorf("expected context ID 'ctx-1', got %v", msg.ContextID)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part, got %d", len(msg.Parts))
				}
				if textPart, ok := msg.Parts[0].(*protocol.TextPart); ok {
					if textPart.Text != "status message" {
						t.Errorf("expected text 'status message', got %s", textPart.Text)
					}
				} else {
					t.Error("expected TextPart")
				}
			},
		},
		{
			name: "status without message",
			event: &protocol.TaskStatusUpdateEvent{
				TaskID:    "task-2",
				ContextID: "ctx-2",
				Status: protocol.TaskStatus{
					Message: nil,
				},
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, msg *protocol.Message) {
				if msg.Role != protocol.MessageRoleAgent {
					t.Errorf("expected role Agent, got %s", msg.Role)
				}
				if msg.Kind != protocol.KindMessage {
					t.Errorf("expected kind %s, got %s", protocol.KindMessage, msg.Kind)
				}
				if msg.MessageID != "" {
					t.Errorf("expected empty message ID, got %s", msg.MessageID)
				}
				if msg.TaskID == nil || *msg.TaskID != "task-2" {
					t.Errorf("expected task ID 'task-2', got %v", msg.TaskID)
				}
				if msg.ContextID == nil || *msg.ContextID != "ctx-2" {
					t.Errorf("expected context ID 'ctx-2', got %v", msg.ContextID)
				}
				if len(msg.Parts) != 0 {
					t.Errorf("expected 0 parts, got %d", len(msg.Parts))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			msg := convertTaskStatusToMessage(tc.event)
			tc.validateFunc(t, msg)
		})
	}
}

func TestConvertTaskArtifactToMessage(t *testing.T) {
	type testCase struct {
		name         string
		event        *protocol.TaskArtifactUpdateEvent
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, msg *protocol.Message)
	}

	tests := []testCase{
		{
			name: "artifact with parts",
			event: &protocol.TaskArtifactUpdateEvent{
				TaskID:    "task-1",
				ContextID: "ctx-1",
				Artifact: protocol.Artifact{
					ArtifactID: "artifact-1",
					Parts:      []protocol.Part{&protocol.TextPart{Kind: protocol.KindText, Text: "artifact content"}},
				},
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, msg *protocol.Message) {
				if msg.Role != protocol.MessageRoleAgent {
					t.Errorf("expected role Agent, got %s", msg.Role)
				}
				if msg.Kind != protocol.KindMessage {
					t.Errorf("expected kind %s, got %s", protocol.KindMessage, msg.Kind)
				}
				if msg.MessageID != "artifact-1" {
					t.Errorf("expected message ID 'artifact-1', got %s", msg.MessageID)
				}
				if msg.TaskID == nil || *msg.TaskID != "task-1" {
					t.Errorf("expected task ID 'task-1', got %v", msg.TaskID)
				}
				if msg.ContextID == nil || *msg.ContextID != "ctx-1" {
					t.Errorf("expected context ID 'ctx-1', got %v", msg.ContextID)
				}
				if len(msg.Parts) != 1 {
					t.Errorf("expected 1 part, got %d", len(msg.Parts))
				}
				if textPart, ok := msg.Parts[0].(*protocol.TextPart); ok {
					if textPart.Text != "artifact content" {
						t.Errorf("expected text 'artifact content', got %s", textPart.Text)
					}
				} else {
					t.Error("expected TextPart")
				}
			},
		},
		{
			name: "artifact without parts",
			event: &protocol.TaskArtifactUpdateEvent{
				TaskID:    "task-2",
				ContextID: "ctx-2",
				Artifact: protocol.Artifact{
					ArtifactID: "artifact-2",
				},
			},
			setupFunc: func(tc *testCase) {},
			validateFunc: func(t *testing.T, msg *protocol.Message) {
				if msg.Role != protocol.MessageRoleAgent {
					t.Errorf("expected role Agent, got %s", msg.Role)
				}
				if msg.Kind != protocol.KindMessage {
					t.Errorf("expected kind %s, got %s", protocol.KindMessage, msg.Kind)
				}
				if msg.MessageID != "artifact-2" {
					t.Errorf("expected message ID 'artifact-2', got %s", msg.MessageID)
				}
				if msg.TaskID == nil || *msg.TaskID != "task-2" {
					t.Errorf("expected task ID 'task-2', got %v", msg.TaskID)
				}
				if msg.ContextID == nil || *msg.ContextID != "ctx-2" {
					t.Errorf("expected context ID 'ctx-2', got %v", msg.ContextID)
				}
				if len(msg.Parts) != 0 {
					t.Errorf("expected 0 parts, got %d", len(msg.Parts))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			msg := convertTaskArtifactToMessage(tc.event)
			tc.validateFunc(t, msg)
		})
	}
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}
