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
	"bytes"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
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
							Metadata: map[string]any{
								ia2a.MessageMetadataObjectTypeKey: "graph.node.start",
								ia2a.MessageMetadataStateDeltaKey: ia2a.EncodeStateDeltaMetadata(map[string][]byte{
									"_node_metadata": []byte(`{"nodeId":"planner","phase":"start"}`),
								}),
							},
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
				if event.Response.Object != "graph.node.start" {
					t.Errorf("expected response object graph.node.start, got %s", event.Response.Object)
				}
				if event.StateDelta == nil {
					t.Fatal("expected state delta restored from artifact metadata")
				}
				assertStateDeltaBytesEqual(t, map[string][]byte{
					"_node_metadata": []byte(`{"nodeId":"planner","phase":"start"}`),
				}, event.StateDelta)
			},
		},
		{
			name: "task result without artifact metadata keeps default object",
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
			events, err := converter.ConvertToEvents(tc.result, tc.agentName, tc.invocation)
			// For backward compatibility, pass the first event to validateFunc
			var evt *event.Event
			if len(events) > 0 {
				evt = events[len(events)-1] // Use last event (final response)
			}
			tc.validateFunc(t, evt, err)
		})
	}
}

func TestDefaultA2AEventConverter_ConvertStreamingToEvents(t *testing.T) {
	type testCase struct {
		name         string
		result       protocol.StreamingMessageEvent
		agentName    string
		invocation   *agent.Invocation
		setupFunc    func(tc *testCase)
		validateFunc func(t *testing.T, events []*event.Event, err error)
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
			validateFunc: func(t *testing.T, events []*event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(events) == 0 {
					t.Fatal("expected at least one event, got none")
				}
				evt := events[0]
				if evt.Author != "test-agent" {
					t.Errorf("expected author 'test-agent', got %s", evt.Author)
				}
				if evt.InvocationID != "test-id" {
					t.Errorf("expected invocation ID 'test-id', got %s", evt.InvocationID)
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
			validateFunc: func(t *testing.T, events []*event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(events) == 0 {
					t.Fatal("expected at least one event, got none")
				}
				evt := events[0]
				if evt.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if !evt.Response.IsPartial {
					t.Error("expected IsPartial to be true for streaming")
				}
				if evt.Response.Done {
					t.Error("expected Done to be false for streaming")
				}
				if len(evt.Response.Choices) != 1 {
					t.Errorf("expected 1 choice, got %d", len(evt.Response.Choices))
				}
				if evt.Response.ID != "stream-1" {
					t.Errorf("expected response ID 'stream-1', got %s", evt.Response.ID)
				}
				if evt.Response.Object != model.ObjectTypeChatCompletionChunk {
					t.Errorf("expected response object %s, got %s", model.ObjectTypeChatCompletionChunk, evt.Response.Object)
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
			validateFunc: func(t *testing.T, events []*event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(events) == 0 {
					t.Fatal("expected at least one event, got none")
				}
				evt := events[0]
				if evt.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if evt.Response.ID != "artifact-1" {
					t.Errorf("expected response ID 'artifact-1', got %s", evt.Response.ID)
				}
				if evt.Response.Object != model.ObjectTypeChatCompletionChunk {
					t.Errorf("expected response object %s, got %s", model.ObjectTypeChatCompletionChunk, evt.Response.Object)
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
			validateFunc: func(t *testing.T, events []*event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(events) != 0 {
					t.Fatalf("expected no events (status updates are filtered), got %d", len(events))
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
			validateFunc: func(t *testing.T, events []*event.Event, err error) {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(events) == 0 {
					t.Fatal("expected at least one event, got none")
				}
				evt := events[0]
				if evt.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if evt.Response.ID != "artifact-99" {
					t.Errorf("expected response ID 'artifact-99', got %s", evt.Response.ID)
				}
				if evt.Response.Object != model.ObjectTypeChatCompletionChunk {
					t.Errorf("expected response object %s, got %s", model.ObjectTypeChatCompletionChunk, evt.Response.Object)
				}
			},
		},
	}

	converter := &defaultA2AEventConverter{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFunc(&tc)
			events, err := converter.ConvertStreamingToEvents(tc.result, tc.agentName, tc.invocation)
			tc.validateFunc(t, events, err)
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
				filePart, ok := msg.Parts[0].(*protocol.FilePart)
				if !ok {
					t.Error("expected *FilePart")
					return
				}
				if got := filePart.Metadata[ia2a.FilePartMetadataContentTypeKey]; got != ia2a.FilePartMetadataContentTypeImage {
					t.Errorf("expected content_type %q, got %#v", ia2a.FilePartMetadataContentTypeImage, got)
				}
				fileBytes, ok := filePart.File.(*protocol.FileWithBytes)
				if !ok {
					t.Fatalf("expected *protocol.FileWithBytes, got %T", filePart.File)
				}
				if fileBytes.MimeType == nil || *fileBytes.MimeType != "png" {
					t.Errorf("expected mime type png, got %#v", fileBytes.MimeType)
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
				filePart, ok := msg.Parts[0].(*protocol.FilePart)
				if !ok {
					t.Error("expected *FilePart")
					return
				}
				if got := filePart.Metadata[ia2a.FilePartMetadataContentTypeKey]; got != ia2a.FilePartMetadataContentTypeImage {
					t.Errorf("expected content_type %q, got %#v", ia2a.FilePartMetadataContentTypeImage, got)
				}
				fileURI, ok := filePart.File.(*protocol.FileWithURI)
				if !ok {
					t.Fatalf("expected *protocol.FileWithURI, got %T", filePart.File)
				}
				if fileURI.URI != "https://example.com/image.jpg" {
					t.Errorf("expected URI https://example.com/image.jpg, got %s", fileURI.URI)
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
				filePart, ok := msg.Parts[0].(*protocol.FilePart)
				if !ok {
					t.Error("expected *FilePart")
					return
				}
				if got := filePart.Metadata[ia2a.FilePartMetadataContentTypeKey]; got != ia2a.FilePartMetadataContentTypeAudio {
					t.Errorf("expected content_type %q, got %#v", ia2a.FilePartMetadataContentTypeAudio, got)
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
				filePart, ok := msg.Parts[0].(*protocol.FilePart)
				if !ok {
					t.Error("expected *FilePart")
					return
				}
				if got := filePart.Metadata[ia2a.FilePartMetadataContentTypeKey]; got != ia2a.FilePartMetadataContentTypeFile {
					t.Errorf("expected content_type %q, got %#v", ia2a.FilePartMetadataContentTypeFile, got)
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

func TestDefaultA2AEventConverter_ConvertStreamingToEvents_FailedStatus(
	t *testing.T,
) {
	converter := &defaultA2AEventConverter{}
	code := "A2A_500"
	metadata := ia2a.WithResponseErrorMetadata(nil, &model.ResponseError{
		Type:    model.ErrorTypeFlowError,
		Message: "task failed",
		Code:    &code,
	})

	events, err := converter.ConvertStreamingToEvents(
		protocol.StreamingMessageEvent{
			Result: &protocol.TaskStatusUpdateEvent{
				TaskID:    "task-1",
				ContextID: "ctx-1",
				Metadata:  metadata,
				Status: protocol.TaskStatus{
					State: protocol.TaskStateFailed,
				},
			},
		},
		"test-agent",
		&agent.Invocation{InvocationID: "inv"},
	)
	if err != nil {
		t.Fatalf("ConvertStreamingToEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Response == nil || events[0].Response.Error == nil {
		t.Fatal("expected Response.Error to be populated")
	}
	if events[0].Response.Error.Code == nil ||
		*events[0].Response.Error.Code != code {
		t.Fatalf("code = %v, want %q", events[0].Response.Error.Code, code)
	}
	if !events[0].Response.Done {
		t.Fatal("expected terminal error event")
	}
}

func TestDefaultA2AEventConverter_ConvertToEvents_FailedTask(
	t *testing.T,
) {
	converter := &defaultA2AEventConverter{}
	metadata := ia2a.WithResponseErrorMetadata(nil, &model.ResponseError{
		Type:    model.ErrorTypeFlowError,
		Message: "task failed",
	})

	events, err := converter.ConvertToEvents(
		protocol.MessageResult{
			Result: &protocol.Task{
				ID:        "task-1",
				ContextID: "ctx-1",
				Metadata:  metadata,
				Status: protocol.TaskStatus{
					State: protocol.TaskStateFailed,
				},
			},
		},
		"test-agent",
		&agent.Invocation{InvocationID: "inv"},
	)
	if err != nil {
		t.Fatalf("ConvertToEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Response == nil || events[0].Response.Error == nil {
		t.Fatal("expected Response.Error to be populated")
	}
	if events[0].Response.Error.Message != "task failed" {
		t.Fatalf(
			"message = %q, want %q",
			events[0].Response.Error.Message,
			"task failed",
		)
	}
}

func TestTaskResponseError_EdgeCases(t *testing.T) {
	if taskResponseError(nil) != nil {
		t.Fatal("expected nil response error for nil result")
	}

	if taskResponseError(&parseResult{
		taskState: protocol.TaskStateSubmitted,
	}) != nil {
		t.Fatal("expected nil response error for non-failure state")
	}

	passthrough := &model.ResponseError{
		Type:    model.ErrorTypeFlowError,
		Message: "structured",
	}
	if got := taskResponseError(&parseResult{
		taskState:     protocol.TaskStateFailed,
		responseError: passthrough,
		textContent:   "ignored",
	}); got != passthrough {
		t.Fatal("expected structured response error to be reused")
	}
}

func TestTaskResponseError_FallbackMessages(t *testing.T) {
	tests := []struct {
		name  string
		state protocol.TaskState
		want  string
	}{
		{
			name:  "failed",
			state: protocol.TaskStateFailed,
			want:  "remote task failed",
		},
		{
			name:  "rejected",
			state: protocol.TaskStateRejected,
			want:  "remote task rejected",
		},
		{
			name:  "canceled",
			state: protocol.TaskStateCanceled,
			want:  "remote task canceled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			respErr := taskResponseError(&parseResult{
				taskState: tt.state,
			})
			if respErr == nil {
				t.Fatal("expected response error, got nil")
			}
			if respErr.Type != model.ErrorTypeFlowError {
				t.Fatalf("unexpected error type: %s", respErr.Type)
			}
			if respErr.Message != tt.want {
				t.Fatalf(
					"message = %q, want %q",
					respErr.Message,
					tt.want,
				)
			}
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

// TestBuildRespEvent_ToolScenarios covers tool call/response flows in both streaming and non-streaming modes.
func TestBuildRespEvent_ToolScenarios(t *testing.T) {
	converter := &defaultA2AEventConverter{}
	invocation := &agent.Invocation{InvocationID: "test-id"}

	cases := []struct {
		name        string
		isStreaming bool
		msg         *protocol.Message
		validate    func(t *testing.T, evt *event.Event)
	}{
		{
			name:        "tool call non-streaming with text",
			isStreaming: false,
			msg: &protocol.Message{
				MessageID: "msg-1",
				Role:      protocol.MessageRoleAgent,
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "I'll check the weather for you."},
					&protocol.DataPart{
						Data: map[string]any{
							"id":   "call-123",
							"type": "function",
							"name": "get_weather",
							"args": `{"location": "New York"}`,
						},
						Metadata: map[string]any{"type": "function_call"},
					},
				},
			},
			validate: func(t *testing.T, evt *event.Event) {
				choice := evt.Response.Choices[0]
				if choice.Message.Role != model.RoleAssistant {
					t.Errorf("expected role assistant, got %s", choice.Message.Role)
				}
				if choice.Message.Content != "I'll check the weather for you." {
					t.Errorf("unexpected content: %s", choice.Message.Content)
				}
				if len(choice.Message.ToolCalls) != 1 {
					t.Fatalf("expected 1 tool call, got %d", len(choice.Message.ToolCalls))
				}
				tc := choice.Message.ToolCalls[0]
				if tc.ID != "call-123" || tc.Function.Name != "get_weather" || string(tc.Function.Arguments) != `{"location": "New York"}` {
					t.Fatalf("unexpected tool call: %+v", tc)
				}
			},
		},
		{
			name:        "tool response non-streaming",
			isStreaming: false,
			msg: &protocol.Message{
				MessageID: "msg-2",
				Role:      protocol.MessageRoleAgent,
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{
							"id":       "call-123",
							"name":     "get_weather",
							"response": "The weather in New York is sunny, 72°F",
						},
						Metadata: map[string]any{"type": "function_response"},
					},
				},
			},
			validate: func(t *testing.T, evt *event.Event) {
				choice := evt.Response.Choices[0]
				if choice.Message.Role != model.RoleTool {
					t.Fatalf("expected role tool, got %s", choice.Message.Role)
				}
				if choice.Message.ToolID != "call-123" || choice.Message.ToolName != "get_weather" {
					t.Fatalf("unexpected tool id/name: %s %s", choice.Message.ToolID, choice.Message.ToolName)
				}
				if choice.Message.Content != "The weather in New York is sunny, 72°F" {
					t.Fatalf("unexpected content: %s", choice.Message.Content)
				}
			},
		},
		{
			name:        "tool call streaming",
			isStreaming: true,
			msg: &protocol.Message{
				MessageID: "msg-3",
				Role:      protocol.MessageRoleAgent,
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{
							"id":   "call-456",
							"type": "function",
							"name": "calculate",
							"args": `{"x": 10, "y": 20}`,
						},
						Metadata: map[string]any{"type": "function_call"},
					},
				},
			},
			validate: func(t *testing.T, evt *event.Event) {
				if evt.Response.IsPartial {
					t.Fatal("expected IsPartial to be false")
				}
				if evt.Response.Done {
					t.Fatal("expected Done to be false")
				}
				choice := evt.Response.Choices[0]
				if len(choice.Message.ToolCalls) != 1 {
					t.Fatalf("expected 1 tool call, got %d", len(choice.Message.ToolCalls))
				}
			},
		},
		{
			name:        "mixed text and tool call",
			isStreaming: false,
			msg: &protocol.Message{
				MessageID: "msg-4",
				Role:      protocol.MessageRoleAgent,
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "Let me search for that."},
					&protocol.DataPart{
						Data: map[string]any{
							"id":   "call-789",
							"type": "function",
							"name": "search",
							"args": `{"query": "golang"}`,
						},
						Metadata: map[string]any{"type": "function_call"},
					},
				},
			},
			validate: func(t *testing.T, evt *event.Event) {
				choice := evt.Response.Choices[0]
				if choice.Message.Content != "Let me search for that." {
					t.Fatalf("unexpected content: %s", choice.Message.Content)
				}
				if len(choice.Message.ToolCalls) != 1 {
					t.Fatalf("expected 1 tool call, got %d", len(choice.Message.ToolCalls))
				}
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			evt := converter.buildRespEvent(tt.isStreaming, tt.msg, "test-agent", invocation)
			if evt == nil || evt.Response == nil {
				t.Fatalf("expected event/response, got nil")
			}
			tt.validate(t, evt)
		})
	}
}

// TestProcessFunctionResponse tests the function response processing
func TestProcessFunctionResponse(t *testing.T) {
	type testCase struct {
		name         string
		dataPart     *protocol.DataPart
		validateFunc func(t *testing.T, content, id, name string)
	}

	unmarshalableResponse := struct {
		Ch chan int
	}{
		Ch: make(chan int),
	}

	tests := []testCase{
		{
			name: "valid tool response",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"id":       "call-1",
					"name":     "get_weather",
					"response": "Sunny, 72°F",
				},
			},
			validateFunc: func(t *testing.T, content, id, name string) {
				if content != "Sunny, 72°F" {
					t.Errorf("expected 'Sunny, 72°F', got %s", content)
				}
				if id != "call-1" {
					t.Errorf("expected id 'call-1', got %s", id)
				}
				if name != "get_weather" {
					t.Errorf("expected name 'get_weather', got %s", name)
				}
			},
		},
		{
			name: "missing response field",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"id":   "call-2",
					"name": "get_weather",
				},
			},
			validateFunc: func(t *testing.T, content, id, name string) {
				if content != "" {
					t.Errorf("expected empty content, got %s", content)
				}
				if id != "call-2" {
					t.Errorf("expected id 'call-2', got %s", id)
				}
			},
		},
		{
			name: "invalid data type",
			dataPart: &protocol.DataPart{
				Data: "not a map",
			},
			validateFunc: func(t *testing.T, content, id, name string) {
				if content != "" {
					t.Errorf("expected empty content, got %s", content)
				}
				if id != "" {
					t.Errorf("expected empty id, got %s", id)
				}
			},
		},
		{
			name: "numeric response marshals to json",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"response": 12345,
				},
			},
			validateFunc: func(t *testing.T, content, id, name string) {
				if content != "12345" {
					t.Errorf("expected JSON number content, got %s", content)
				}
			},
		},
		{
			name: "object response marshals to json",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"response": map[string]any{
						"city": "Beijing",
						"temp": 26,
					},
				},
			},
			validateFunc: func(t *testing.T, content, id, name string) {
				var got map[string]any
				if err := json.Unmarshal([]byte(content), &got); err != nil {
					t.Fatalf("expected valid JSON object content, got %q: %v", content, err)
				}
				if got["city"] != "Beijing" {
					t.Errorf("expected city Beijing, got %#v", got["city"])
				}
				if got["temp"] != float64(26) {
					t.Errorf("expected temp 26, got %#v", got["temp"])
				}
			},
		},
		{
			name: "unmarshalable response is skipped",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"response": unmarshalableResponse,
				},
			},
			validateFunc: func(t *testing.T, content, id, name string) {
				if content != "" {
					t.Errorf("expected empty content for unmarshalable response, got %q", content)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content, id, name := processFunctionResponse(tc.dataPart)
			tc.validateFunc(t, content, id, name)
		})
	}
}

// TestProcessDataPartInto_ADKMetadataKey tests handling of ADK-compatible metadata keys
func TestProcessDataPartInto_ADKMetadataKey(t *testing.T) {
	type testCase struct {
		name         string
		dataPart     protocol.Part
		validateFunc func(t *testing.T, result *parseResult)
	}

	tests := []testCase{
		{
			name: "function call with adk_type metadata",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"id":   "call-adk-1",
					"type": "function",
					"name": "get_weather",
					"args": `{"location": "Beijing"}`,
				},
				Metadata: map[string]any{
					"adk_type": "function_call", // Using ADK-compatible key
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if len(result.toolCalls) == 0 {
					t.Fatal("expected tool call, got none")
				}
				toolCall := result.toolCalls[0]
				if toolCall.ID != "call-adk-1" {
					t.Errorf("expected ID 'call-adk-1', got %s", toolCall.ID)
				}
				if toolCall.Function.Name != "get_weather" {
					t.Errorf("expected name 'get_weather', got %s", toolCall.Function.Name)
				}
				if len(result.toolResponses) > 0 {
					t.Errorf("expected no tool response, got %v", result.toolResponses)
				}
			},
		},
		{
			name: "function response with adk_type metadata",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"id":       "call-adk-2",
					"name":     "get_weather",
					"response": "Beijing: Sunny, 20°C",
				},
				Metadata: map[string]any{
					"adk_type": "function_response", // Using ADK-compatible key
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if len(result.toolResponses) == 0 {
					t.Fatal("expected tool response, got nil")
				}
				if result.toolResponses[0].content != "Beijing: Sunny, 20°C" {
					t.Errorf("expected content 'Beijing: Sunny, 20°C', got %s", result.toolResponses[0].content)
				}
				if result.toolResponses[0].id != "call-adk-2" {
					t.Errorf("expected tool response ID 'call-adk-2', got %s", result.toolResponses[0].id)
				}
				if result.toolResponses[0].name != "get_weather" {
					t.Errorf("expected tool response name 'get_weather', got %s", result.toolResponses[0].name)
				}
				if len(result.toolCalls) > 0 {
					t.Errorf("expected no tool calls, got %v", result.toolCalls)
				}
			},
		},
		{
			name: "no type metadata",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"id":   "call-no-type",
					"name": "some_function",
				},
				Metadata: map[string]any{
					"other_key": "other_value",
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.textContent != "" {
					t.Errorf("expected empty content, got %s", result.textContent)
				}
				if len(result.toolCalls) > 0 {
					t.Errorf("expected no tool calls, got %v", result.toolCalls)
				}
				if len(result.toolResponses) > 0 {
					t.Errorf("expected no tool response, got %v", result.toolResponses)
				}
			},
		},
		{
			name: "nil metadata",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"id":   "call-nil-meta",
					"name": "some_function",
				},
				Metadata: nil,
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.textContent != "" {
					t.Errorf("expected empty content, got %s", result.textContent)
				}
				if len(result.toolCalls) > 0 {
					t.Errorf("expected no tool calls, got %v", result.toolCalls)
				}
				if len(result.toolResponses) > 0 {
					t.Errorf("expected no tool response, got %v", result.toolResponses)
				}
			},
		},
		{
			name: "non-string type value",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"id":   "call-bad-type",
					"name": "some_function",
				},
				Metadata: map[string]any{
					"type": 12345, // Non-string type value
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.textContent != "" {
					t.Errorf("expected empty content, got %s", result.textContent)
				}
				if len(result.toolCalls) > 0 {
					t.Errorf("expected no tool calls, got %v", result.toolCalls)
				}
				if len(result.toolResponses) > 0 {
					t.Errorf("expected no tool response, got %v", result.toolResponses)
				}
			},
		},
		{
			name: "adk_type takes precedence over standard type key",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"id":       "resp-precedence",
					"name":     "test_func",
					"response": `{"result": "ok"}`,
				},
				Metadata: map[string]any{
					"type":     "function_call",     // Standard key (should be ignored)
					"adk_type": "function_response", // ADK key (takes precedence)
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				// Should process as function_response, not function_call
				// This matches Python trpc-agent behavior where adk_type is checked first
				if len(result.toolCalls) > 0 {
					t.Errorf("expected no tool calls (adk_type should take precedence), got %v", result.toolCalls)
				}
				if len(result.toolResponses) == 0 {
					t.Fatal("expected tool response, got nil")
				}
				if result.toolResponses[0].name != "test_func" {
					t.Errorf("expected name 'test_func', got %s", result.toolResponses[0].name)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := &parseResult{}
			processDataPart(tc.dataPart, result)
			tc.validateFunc(t, result)
		})
	}
}

// TestProcessExecutableCode tests processing of executable code DataParts
func TestProcessExecutableCode(t *testing.T) {
	tests := []struct {
		name     string
		dataPart *protocol.DataPart
		expected string
	}{
		{
			name: "ADK mode - code field",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"code":     "print('hello')",
					"language": "python",
				},
			},
			expected: "print('hello')",
		},
		{
			name: "non-ADK mode - content field",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"content": "console.log('hello')",
				},
			},
			expected: "console.log('hello')",
		},
		{
			name: "both fields - code takes precedence",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"code":    "primary code",
					"content": "fallback content",
				},
			},
			expected: "primary code",
		},
		{
			name: "invalid data type",
			dataPart: &protocol.DataPart{
				Data: "not a map",
			},
			expected: "",
		},
		{
			name: "empty data",
			dataPart: &protocol.DataPart{
				Data: map[string]any{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := processExecutableCode(tt.dataPart)
			if result != tt.expected {
				t.Errorf("processExecutableCode() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestProcessCodeExecutionResult tests processing of code execution result DataParts
func TestProcessCodeExecutionResult(t *testing.T) {
	tests := []struct {
		name     string
		dataPart *protocol.DataPart
		expected string
	}{
		{
			name: "ADK mode - output field",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"output":  "hello world",
					"outcome": "OUTCOME_OK",
				},
			},
			expected: "hello world",
		},
		{
			name: "non-ADK mode - content field",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"content": "execution result",
				},
			},
			expected: "execution result",
		},
		{
			name: "both fields - output takes precedence",
			dataPart: &protocol.DataPart{
				Data: map[string]any{
					"output":  "primary output",
					"content": "fallback content",
				},
			},
			expected: "primary output",
		},
		{
			name: "invalid data type",
			dataPart: &protocol.DataPart{
				Data: "not a map",
			},
			expected: "",
		},
		{
			name: "empty data",
			dataPart: &protocol.DataPart{
				Data: map[string]any{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := processCodeExecutionResult(tt.dataPart)
			if result != tt.expected {
				t.Errorf("processCodeExecutionResult() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestBuildRespEvent_CodeExecution tests handling of code execution DataParts
func TestBuildRespEvent_CodeExecution(t *testing.T) {
	converter := &defaultA2AEventConverter{}
	invocation := &agent.Invocation{
		InvocationID: "test-id",
	}

	tests := []struct {
		name         string
		msg          *protocol.Message
		isStreaming  bool
		validateFunc func(t *testing.T, evt *event.Event)
	}{
		{
			name: "executable code - non-streaming",
			msg: &protocol.Message{
				MessageID: "msg-ce-1",
				Role:      protocol.MessageRoleAgent,
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{
							"code":     "print('test')",
							"language": "python",
						},
						Metadata: map[string]any{
							"type": "executable_code",
						},
					},
				},
			},
			isStreaming: false,
			validateFunc: func(t *testing.T, evt *event.Event) {
				if evt == nil {
					t.Fatal("expected event, got nil")
				}
				if evt.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if len(evt.Response.Choices) != 1 {
					t.Errorf("expected 1 choice, got %d", len(evt.Response.Choices))
				}
				choice := evt.Response.Choices[0]
				if choice.Message.Content != "print('test')" {
					t.Errorf("expected content 'print('test')', got %s", choice.Message.Content)
				}
				if evt.Response.Object != model.ObjectTypePostprocessingCodeExecution {
					t.Errorf("expected object type %s, got %s", model.ObjectTypePostprocessingCodeExecution, evt.Response.Object)
				}
			},
		},
		{
			name: "code execution result - non-streaming",
			msg: &protocol.Message{
				MessageID: "msg-cer-1",
				Role:      protocol.MessageRoleAgent,
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{
							"output":  "test output",
							"outcome": "OUTCOME_OK",
						},
						Metadata: map[string]any{
							"type": "code_execution_result",
						},
					},
				},
			},
			isStreaming: false,
			validateFunc: func(t *testing.T, evt *event.Event) {
				if evt == nil {
					t.Fatal("expected event, got nil")
				}
				if evt.Response == nil {
					t.Fatal("expected response, got nil")
				}
				choice := evt.Response.Choices[0]
				if choice.Message.Content != "test output" {
					t.Errorf("expected content 'test output', got %s", choice.Message.Content)
				}
				// Both code execution and result use the same ObjectType
				if evt.Response.Object != model.ObjectTypePostprocessingCodeExecution {
					t.Errorf("expected object type %s, got %s", model.ObjectTypePostprocessingCodeExecution, evt.Response.Object)
				}
			},
		},
		{
			name: "executable code - streaming",
			msg: &protocol.Message{
				MessageID: "msg-ce-stream",
				Role:      protocol.MessageRoleAgent,
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{
							"content": "streaming code",
						},
						Metadata: map[string]any{
							"type": "executable_code",
						},
					},
				},
			},
			isStreaming: true,
			validateFunc: func(t *testing.T, evt *event.Event) {
				if evt == nil {
					t.Fatal("expected event, got nil")
				}
				if evt.Response == nil {
					t.Fatal("expected response, got nil")
				}
				if !evt.Response.IsPartial {
					t.Error("expected IsPartial to be true for streaming")
				}
				choice := evt.Response.Choices[0]
				if choice.Delta.Content != "streaming code" {
					t.Errorf("expected delta content 'streaming code', got %s", choice.Delta.Content)
				}
			},
		},
		{
			name: "code execution with ADK metadata key",
			msg: &protocol.Message{
				MessageID: "msg-ce-adk",
				Role:      protocol.MessageRoleAgent,
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{
							"code":     "adk code",
							"language": "python",
						},
						Metadata: map[string]any{
							"adk_type": "executable_code",
						},
					},
				},
			},
			isStreaming: false,
			validateFunc: func(t *testing.T, evt *event.Event) {
				if evt == nil {
					t.Fatal("expected event, got nil")
				}
				choice := evt.Response.Choices[0]
				if choice.Message.Content != "adk code" {
					t.Errorf("expected content 'adk code', got %s", choice.Message.Content)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			evt := converter.buildRespEvent(tc.isStreaming, tc.msg, "test-agent", invocation)
			tc.validateFunc(t, evt)
		})
	}
}

// TestExtractObjectType tests the object type extraction logic
func TestExtractObjectType(t *testing.T) {
	tests := []struct {
		name     string
		result   *parseResult
		expected string
	}{
		{
			name: "with objectType from metadata",
			result: &parseResult{
				objectType: "custom.object.type",
			},
			expected: "custom.object.type",
		},
		{
			name: "with tool calls",
			result: &parseResult{
				toolCalls: []model.ToolCall{{ID: "call-1"}},
			},
			expected: model.ObjectTypeChatCompletion,
		},
		{
			name: "with code execution",
			result: &parseResult{
				codeExecution: "some code",
			},
			expected: model.ObjectTypePostprocessingCodeExecution,
		},
		{
			name: "with code execution result",
			result: &parseResult{
				codeExecutionResult: "some output",
			},
			// Both code execution and result use the same ObjectType now
			expected: model.ObjectTypePostprocessingCodeExecution,
		},
		{
			name: "objectType takes precedence over inferred type",
			result: &parseResult{
				objectType:    "explicit.type",
				codeExecution: "some code",
			},
			expected: "explicit.type",
		},
		{
			name:     "empty result",
			result:   &parseResult{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractObjectType(tt.result)
			if result != tt.expected {
				t.Errorf("extractObjectType() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestParseA2AMessageParts_CodeExecution tests parsing of code execution parts
func TestParseA2AMessageParts_CodeExecution(t *testing.T) {
	tests := []struct {
		name         string
		msg          *protocol.Message
		validateFunc func(t *testing.T, result *parseResult)
	}{
		{
			name: "executable code part",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{
							"code":     "print('hello')",
							"language": "python",
						},
						Metadata: map[string]any{
							"type": "executable_code",
						},
					},
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.codeExecution != "print('hello')" {
					t.Errorf("expected codeExecution 'print('hello')', got %s", result.codeExecution)
				}
				if result.codeExecutionResult != "" {
					t.Errorf("expected empty codeExecutionResult, got %s", result.codeExecutionResult)
				}
			},
		},
		{
			name: "code execution result part",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{
							"output":  "hello world",
							"outcome": "OUTCOME_OK",
						},
						Metadata: map[string]any{
							"type": "code_execution_result",
						},
					},
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.codeExecutionResult != "hello world" {
					t.Errorf("expected codeExecutionResult 'hello world', got %s", result.codeExecutionResult)
				}
				if result.codeExecution != "" {
					t.Errorf("expected empty codeExecution, got %s", result.codeExecution)
				}
			},
		},
		{
			name: "mixed text and code execution",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "Here is the code: "},
					&protocol.DataPart{
						Data: map[string]any{
							"code": "x = 1 + 1",
						},
						Metadata: map[string]any{
							"type": "executable_code",
						},
					},
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.textContent != "Here is the code: " {
					t.Errorf("expected textContent 'Here is the code: ', got %s", result.textContent)
				}
				if result.codeExecution != "x = 1 + 1" {
					t.Errorf("expected codeExecution 'x = 1 + 1', got %s", result.codeExecution)
				}
			},
		},
		{
			name: "object type from message metadata",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "content"},
				},
				Metadata: map[string]any{
					"object_type": "postprocessing.code_execution",
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.objectType != "postprocessing.code_execution" {
					t.Errorf("expected objectType 'postprocessing.code_execution', got %s", result.objectType)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseA2AMessageParts(tt.msg)
			tt.validateFunc(t, result)
		})
	}
}

func TestParseA2AMessageParts_ResponseErrorFallsBackToText(t *testing.T) {
	code := "A2A_500"
	msg := &protocol.Message{
		Parts: []protocol.Part{
			&protocol.TextPart{Text: "task failed from content"},
		},
		Metadata: map[string]any{
			ia2a.MessageMetadataErrorCodeKey: code,
		},
	}

	result := parseA2AMessageParts(msg)
	if result.responseError == nil {
		t.Fatal("expected responseError, got nil")
	}
	if result.responseError.Message != "task failed from content" {
		t.Fatalf(
			"expected fallback message from text content, got %q",
			result.responseError.Message,
		)
	}
}

// TestConvertToEvents_WithHistory tests conversion of Task with history containing code execution
func TestConvertToEvents_WithHistory(t *testing.T) {
	converter := &defaultA2AEventConverter{}
	invocation := &agent.Invocation{
		InvocationID: "test-id",
	}

	task := &protocol.Task{
		ID:        "task-1",
		ContextID: "ctx-1",
		History: []protocol.Message{
			{
				MessageID: "hist-1",
				Role:      protocol.MessageRoleAgent,
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{
							"id":   "call-1",
							"type": "function",
							"name": "execute_code",
							"args": `{"code": "print('hi')"}`,
						},
						Metadata: map[string]any{
							"type": "function_call",
						},
					},
				},
			},
			{
				MessageID: "hist-2",
				Role:      protocol.MessageRoleAgent,
				Parts: []protocol.Part{
					&protocol.DataPart{
						Data: map[string]any{
							"id":       "call-1",
							"name":     "execute_code",
							"response": "hi",
						},
						Metadata: map[string]any{
							"type": "function_response",
						},
					},
				},
			},
		},
		Artifacts: []protocol.Artifact{
			{
				ArtifactID: "artifact-1",
				Parts:      []protocol.Part{&protocol.TextPart{Text: "Final response"}},
			},
		},
	}

	result := protocol.MessageResult{Result: task}
	events, err := converter.ConvertToEvents(result, "test-agent", invocation)

	if err != nil {
		t.Fatalf("ConvertToEvents() error: %v", err)
	}

	// Should have 3 events: 2 from history + 1 from artifact
	if len(events) != 3 {
		t.Errorf("expected 3 events, got %d", len(events))
	}

	// First event should be tool call
	if len(events[0].Response.Choices) > 0 && len(events[0].Response.Choices[0].Message.ToolCalls) == 0 {
		t.Error("expected first event to have tool calls")
	}

	// Second event should be tool response
	if len(events[1].Response.Choices) > 0 && events[1].Response.Choices[0].Message.Role != model.RoleTool {
		t.Errorf("expected second event to be tool response, got role %s", events[1].Response.Choices[0].Message.Role)
	}

	// Last event should be marked as done
	if !events[2].Done {
		t.Error("expected last event to be marked as done")
	}
}

// TestParseA2AMessageParts_Tag tests parsing of tag field from A2A message metadata
func TestParseA2AMessageParts_Tag(t *testing.T) {
	tests := []struct {
		name         string
		msg          *protocol.Message
		expectedTag  string
		validateFunc func(t *testing.T, result *parseResult)
	}{
		{
			name: "message with tag in metadata",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "content"},
				},
				Metadata: map[string]any{
					"tag": "code_execution_code",
				},
			},
			expectedTag: "code_execution_code",
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.tag != "code_execution_code" {
					t.Errorf("expected tag 'code_execution_code', got %s", result.tag)
				}
			},
		},
		{
			name: "message with tag and object_type in metadata",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "content"},
				},
				Metadata: map[string]any{
					"object_type": "postprocessing.code_execution",
					"tag":         "code_execution_result",
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.tag != "code_execution_result" {
					t.Errorf("expected tag 'code_execution_result', got %s", result.tag)
				}
				if result.objectType != "postprocessing.code_execution" {
					t.Errorf("expected objectType 'postprocessing.code_execution', got %s", result.objectType)
				}
			},
		},
		{
			name: "message without tag in metadata",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "content"},
				},
				Metadata: map[string]any{
					"other_key": "other_value",
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.tag != "" {
					t.Errorf("expected empty tag, got %s", result.tag)
				}
			},
		},
		{
			name: "message with nil metadata",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "content"},
				},
				Metadata: nil,
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.tag != "" {
					t.Errorf("expected empty tag, got %s", result.tag)
				}
			},
		},
		{
			name: "message with non-string tag value",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "content"},
				},
				Metadata: map[string]any{
					"tag": 12345,
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.tag != "" {
					t.Errorf("expected empty tag for non-string value, got %s", result.tag)
				}
			},
		},
		{
			name: "message with semicolon-delimited tags",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{Text: "content"},
				},
				Metadata: map[string]any{
					"tag": "code_execution_code;custom_tag",
				},
			},
			validateFunc: func(t *testing.T, result *parseResult) {
				if result.tag != "code_execution_code;custom_tag" {
					t.Errorf("expected tag 'code_execution_code;custom_tag', got %s", result.tag)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseA2AMessageParts(tt.msg)
			tt.validateFunc(t, result)
		})
	}
}

func TestParseA2AMessageParts_StateDeltaMetadata(t *testing.T) {
	originalStateDelta := map[string][]byte{
		"_node_metadata": []byte(`{"nodeId":"planner","phase":"start"}`),
		"json_string":    []byte(`"hello"`),
		"plain_text":     []byte("raw-text"),
		"empty_value":    []byte{},
		"deleted":        nil,
	}
	msg := &protocol.Message{
		Parts: nil,
		Metadata: map[string]any{
			"object_type":                     "graph.node.start",
			ia2a.MessageMetadataStateDeltaKey: ia2a.EncodeStateDeltaMetadata(originalStateDelta),
		},
	}

	result := parseA2AMessageParts(msg)
	if result.objectType != "graph.node.start" {
		t.Fatalf("expected objectType graph.node.start, got %s", result.objectType)
	}
	if result.stateDelta == nil {
		t.Fatal("expected stateDelta to be restored from metadata")
	}
	assertStateDeltaBytesEqual(t, originalStateDelta, result.stateDelta)
}

func assertStateDeltaBytesEqual(t *testing.T, want, got map[string][]byte) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("expected %d state_delta entries, got %d", len(want), len(got))
	}
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			t.Fatalf("missing state_delta key %q", key)
		}
		if wantValue == nil {
			if gotValue != nil {
				t.Fatalf("expected nil state_delta for key %q, got %v", key, gotValue)
			}
			continue
		}
		if gotValue == nil {
			t.Fatalf("expected non-nil state_delta for key %q", key)
		}
		if !bytes.Equal(wantValue, gotValue) {
			t.Fatalf("unexpected state_delta value for key %q: got %q want %q", key, gotValue, wantValue)
		}
	}
}

func TestConvertToEvents_StateDeltaMetadata(t *testing.T) {
	converter := &defaultA2AEventConverter{}
	invocation := &agent.Invocation{InvocationID: "inv-state-delta"}
	originalStateDelta := map[string][]byte{
		"_node_metadata": []byte(`{"nodeId":"planner","phase":"start"}`),
		"json_string":    []byte(`"hello"`),
		"deleted":        nil,
	}

	events, err := converter.ConvertToEvents(protocol.MessageResult{
		Result: &protocol.Message{
			Kind:      protocol.KindMessage,
			MessageID: "msg-state-delta",
			Role:      protocol.MessageRoleAgent,
			Metadata: map[string]any{
				"object_type":                     "graph.node.start",
				ia2a.MessageMetadataStateDeltaKey: ia2a.EncodeStateDeltaMetadata(originalStateDelta),
			},
		},
	}, "test-agent", invocation)
	if err != nil {
		t.Fatalf("ConvertToEvents() error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Object != "graph.node.start" {
		t.Fatalf("expected object graph.node.start, got %s", events[0].Object)
	}
	if events[0].StateDelta == nil {
		t.Fatal("expected StateDelta on converted event")
	}
	assertStateDeltaBytesEqual(t, originalStateDelta, events[0].StateDelta)
}

func TestConvertToEvents_GraphExecutionMarksDone(t *testing.T) {
	converter := &defaultA2AEventConverter{}
	invocation := &agent.Invocation{InvocationID: "inv-graph-exec"}

	events, err := converter.ConvertToEvents(protocol.MessageResult{
		Result: &protocol.Message{
			Kind:      protocol.KindMessage,
			MessageID: "msg-graph-exec",
			Role:      protocol.MessageRoleAgent,
			Metadata: map[string]any{
				ia2a.MessageMetadataObjectTypeKey: graph.ObjectTypeGraphExecution,
				ia2a.MessageMetadataStateDeltaKey: ia2a.EncodeStateDeltaMetadata(map[string][]byte{
					"child_done": []byte("true"),
				}),
			},
		},
	}, "test-agent", invocation)
	if err != nil {
		t.Fatalf("ConvertToEvents() error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if !events[0].Done {
		t.Fatal("expected graph.execution event to be marked done")
	}
	if events[0].IsPartial {
		t.Fatal("expected graph.execution event to be non-partial")
	}
	if events[0].Object != graph.ObjectTypeGraphExecution {
		t.Fatalf("expected object %q, got %q", graph.ObjectTypeGraphExecution, events[0].Object)
	}
}

func TestConvertStreamingToEvents_GraphExecutionMarksDone(t *testing.T) {
	converter := &defaultA2AEventConverter{}
	invocation := &agent.Invocation{InvocationID: "inv-graph-exec-stream"}

	events, err := converter.ConvertStreamingToEvents(protocol.StreamingMessageEvent{
		Result: &protocol.TaskArtifactUpdateEvent{
			TaskID:    "task-graph-exec",
			ContextID: "ctx-graph-exec",
			Artifact: protocol.Artifact{
				ArtifactID: "artifact-graph-exec",
			},
			Metadata: map[string]any{
				ia2a.MessageMetadataObjectTypeKey: graph.ObjectTypeGraphExecution,
				ia2a.MessageMetadataStateDeltaKey: ia2a.EncodeStateDeltaMetadata(map[string][]byte{
					"child_done": []byte("true"),
				}),
			},
		},
	}, "test-agent", invocation)
	if err != nil {
		t.Fatalf("ConvertStreamingToEvents() error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if !events[0].Done {
		t.Fatal("expected streaming graph.execution event to be marked done")
	}
	if events[0].IsPartial {
		t.Fatal("expected streaming graph.execution event to be non-partial")
	}
	if events[0].Object != graph.ObjectTypeGraphExecution {
		t.Fatalf("expected object %q, got %q", graph.ObjectTypeGraphExecution, events[0].Object)
	}
}

// TestBuildEventResponse_WithTag tests that buildEventResponse correctly sets tag on event
func TestBuildEventResponse_WithTag(t *testing.T) {
	invocation := &agent.Invocation{
		InvocationID: "test-id",
	}

	tests := []struct {
		name        string
		result      *parseResult
		isStreaming bool
		expectedTag string
	}{
		{
			name: "event with code execution tag",
			result: &parseResult{
				textContent: "print('hello')",
				objectType:  model.ObjectTypePostprocessingCodeExecution,
				tag:         event.CodeExecutionTag,
			},
			isStreaming: false,
			expectedTag: event.CodeExecutionTag,
		},
		{
			name: "event with code execution result tag",
			result: &parseResult{
				textContent: "hello world",
				objectType:  model.ObjectTypePostprocessingCodeExecution,
				tag:         event.CodeExecutionResultTag,
			},
			isStreaming: false,
			expectedTag: event.CodeExecutionResultTag,
		},
		{
			name: "streaming event with tag",
			result: &parseResult{
				textContent: "streaming content",
				tag:         "custom_tag",
			},
			isStreaming: true,
			expectedTag: "custom_tag",
		},
		{
			name: "event without tag",
			result: &parseResult{
				textContent: "plain content",
			},
			isStreaming: false,
			expectedTag: "",
		},
		{
			name: "event with multiple tags (semicolon-delimited)",
			result: &parseResult{
				textContent: "content",
				tag:         "tag1;tag2;tag3",
			},
			isStreaming: false,
			expectedTag: "tag1;tag2;tag3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := buildEventResponse(tt.isStreaming, "msg-id", tt.result, invocation, "test-agent", protocol.MessageRoleAgent)

			if evt == nil {
				t.Fatal("expected event, got nil")
			}

			if evt.Tag != tt.expectedTag {
				t.Errorf("expected tag '%s', got '%s'", tt.expectedTag, evt.Tag)
			}

			// Verify tag is correctly restored for code execution events
			if tt.expectedTag == event.CodeExecutionTag {
				if !evt.ContainsTag(event.CodeExecutionTag) {
					t.Error("expected event to contain CodeExecutionTag")
				}
			}
			if tt.expectedTag == event.CodeExecutionResultTag {
				if !evt.ContainsTag(event.CodeExecutionResultTag) {
					t.Error("expected event to contain CodeExecutionResultTag")
				}
			}
		})
	}
}

// TestConvertStreamingToEvents_CodeExecutionWithTag tests streaming conversion preserves tag
func TestConvertStreamingToEvents_CodeExecutionWithTag(t *testing.T) {
	converter := &defaultA2AEventConverter{}
	invocation := &agent.Invocation{
		InvocationID: "test-id",
	}

	tests := []struct {
		name        string
		result      protocol.StreamingMessageEvent
		expectedTag string
	}{
		{
			name: "streaming code execution with tag",
			result: protocol.StreamingMessageEvent{
				Result: &protocol.Message{
					Kind:      protocol.KindMessage,
					MessageID: "stream-ce-1",
					Role:      protocol.MessageRoleAgent,
					Parts: []protocol.Part{
						&protocol.DataPart{
							Data: map[string]any{
								"code":     "print('hello')",
								"language": "python",
							},
							Metadata: map[string]any{
								"type": "executable_code",
							},
						},
					},
					Metadata: map[string]any{
						"object_type": model.ObjectTypePostprocessingCodeExecution,
						"tag":         event.CodeExecutionTag,
					},
				},
			},
			expectedTag: event.CodeExecutionTag,
		},
		{
			name: "streaming code execution result with tag",
			result: protocol.StreamingMessageEvent{
				Result: &protocol.Message{
					Kind:      protocol.KindMessage,
					MessageID: "stream-cer-1",
					Role:      protocol.MessageRoleAgent,
					Parts: []protocol.Part{
						&protocol.DataPart{
							Data: map[string]any{
								"output":  "hello world",
								"outcome": "OUTCOME_OK",
							},
							Metadata: map[string]any{
								"type": "code_execution_result",
							},
						},
					},
					Metadata: map[string]any{
						"object_type": model.ObjectTypePostprocessingCodeExecution,
						"tag":         event.CodeExecutionResultTag,
					},
				},
			},
			expectedTag: event.CodeExecutionResultTag,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := converter.ConvertStreamingToEvents(tt.result, "test-agent", invocation)
			if err != nil {
				t.Fatalf("ConvertStreamingToEvents() error: %v", err)
			}

			if len(events) == 0 {
				t.Fatal("expected at least one event, got none")
			}

			evt := events[0]
			if evt.Tag != tt.expectedTag {
				t.Errorf("expected tag '%s', got '%s'", tt.expectedTag, evt.Tag)
			}
		})
	}
}

// TestConvertToEvents_CodeExecutionWithTag tests non-streaming conversion preserves tag
func TestConvertToEvents_CodeExecutionWithTag(t *testing.T) {
	converter := &defaultA2AEventConverter{}
	invocation := &agent.Invocation{
		InvocationID: "test-id",
	}

	tests := []struct {
		name        string
		result      protocol.MessageResult
		expectedTag string
	}{
		{
			name: "code execution message with tag",
			result: protocol.MessageResult{
				Result: &protocol.Message{
					Kind:      protocol.KindMessage,
					MessageID: "msg-ce-tag",
					Role:      protocol.MessageRoleAgent,
					Parts: []protocol.Part{
						&protocol.DataPart{
							Data: map[string]any{
								"code": "x = 1 + 1",
							},
							Metadata: map[string]any{
								"type": "executable_code",
							},
						},
					},
					Metadata: map[string]any{
						"object_type": model.ObjectTypePostprocessingCodeExecution,
						"tag":         event.CodeExecutionTag,
					},
				},
			},
			expectedTag: event.CodeExecutionTag,
		},
		{
			name: "code execution result message with tag",
			result: protocol.MessageResult{
				Result: &protocol.Message{
					Kind:      protocol.KindMessage,
					MessageID: "msg-cer-tag",
					Role:      protocol.MessageRoleAgent,
					Parts: []protocol.Part{
						&protocol.DataPart{
							Data: map[string]any{
								"output": "2",
							},
							Metadata: map[string]any{
								"type": "code_execution_result",
							},
						},
					},
					Metadata: map[string]any{
						"object_type": model.ObjectTypePostprocessingCodeExecution,
						"tag":         event.CodeExecutionResultTag,
					},
				},
			},
			expectedTag: event.CodeExecutionResultTag,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := converter.ConvertToEvents(tt.result, "test-agent", invocation)
			if err != nil {
				t.Fatalf("ConvertToEvents() error: %v", err)
			}

			if len(events) == 0 {
				t.Fatal("expected at least one event, got none")
			}

			// Check the last event (final response)
			evt := events[len(events)-1]
			if evt.Tag != tt.expectedTag {
				t.Errorf("expected tag '%s', got '%s'", tt.expectedTag, evt.Tag)
			}
		})
	}
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}

// TestProcessTextAndDataPart_EdgeCases covers ignored/non-text parts.
func TestProcessTextAndDataPart_EdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		part  protocol.Part
		check func(t *testing.T, res *parseResult, text string)
	}{
		{
			name: "non-text in text processor",
			part: &protocol.DataPart{},
			check: func(t *testing.T, res *parseResult, text string) {
				if text != "" {
					t.Fatalf("expected empty string for non text part, got %q", text)
				}
				if res.textContent != "" || len(res.toolCalls) > 0 || len(res.toolResponses) > 0 || res.codeExecution != "" || res.codeExecutionResult != "" {
					t.Fatalf("expected parseResult to remain empty, got %+v", res)
				}
			},
		},
		{
			name: "text part ignored by data processor",
			part: &protocol.TextPart{Text: "hi"},
			check: func(t *testing.T, res *parseResult, text string) {
				if text != "hi" {
					t.Fatalf("expected text 'hi', got %q", text)
				}
				if res.textContent != "" || len(res.toolCalls) > 0 || len(res.toolResponses) > 0 || res.codeExecution != "" || res.codeExecutionResult != "" {
					t.Fatalf("expected parseResult unchanged for text part, got %+v", res)
				}
			},
		},
		{
			name: "unknown data type",
			part: &protocol.DataPart{Metadata: map[string]any{"type": "unknown"}, Data: map[string]any{}},
			check: func(t *testing.T, res *parseResult, text string) {
				if text != "" {
					t.Fatalf("expected empty text for data part in text processor, got %q", text)
				}
				if res.textContent != "" || len(res.toolCalls) > 0 || len(res.toolResponses) > 0 || res.codeExecution != "" || res.codeExecutionResult != "" {
					t.Fatalf("expected no parsed content for unknown type: %+v", res)
				}
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			res := &parseResult{}
			text, _ := processTextPart(tt.part)
			processDataPart(tt.part, res)
			tt.check(t, res, text)
		})
	}
}

func TestProcessFunctionCall_MissingName(t *testing.T) {
	call := processFunctionCall(&protocol.DataPart{
		Data: map[string]any{
			ia2a.ToolCallFieldID: "id",
		},
		Metadata: map[string]any{
			"type": ia2a.DataPartMetadataTypeFunctionCall,
		},
	})
	if call != nil {
		t.Fatalf("expected nil when function name missing, got %+v", call)
	}
}

func TestBuildStreamingResponse_ToolResponses(t *testing.T) {
	resp := buildStreamingResponse("msg-123", &parseResult{
		toolResponses: []toolResponseData{{id: "tool-1", name: "tool", content: "resp"}},
	}, protocol.MessageRoleAgent)
	if resp == nil {
		t.Fatalf("expected response, got nil")
	}
	if resp.Object != model.ObjectTypeToolResponse {
		t.Fatalf("unexpected object type: %s", resp.Object)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected one choice, got %d", len(resp.Choices))
	}
	msg := resp.Choices[0].Message
	if msg.Role != model.RoleTool || msg.ToolID != "tool-1" || msg.ToolName != "tool" || msg.Content != "resp" {
		t.Fatalf("unexpected tool response message: %+v", msg)
	}
}

func TestProcessTextPart_WithThoughtMetadata(t *testing.T) {
	tests := []struct {
		name            string
		part            protocol.Part
		expectedText    string
		expectedThought bool
	}{
		{
			name: "regular text part",
			part: &protocol.TextPart{
				Text: "Hello world",
			},
			expectedText:    "Hello world",
			expectedThought: false,
		},
		{
			name: "text part with thought=true",
			part: &protocol.TextPart{
				Text: "Let me think...",
				Metadata: map[string]any{
					ia2a.TextPartMetadataThoughtKey: true,
				},
			},
			expectedText:    "Let me think...",
			expectedThought: true,
		},
		{
			name: "text part with adk_thought=true",
			part: &protocol.TextPart{
				Text: "ADK thinking...",
				Metadata: map[string]any{
					ia2a.GetADKMetadataKey(ia2a.TextPartMetadataThoughtKey): true,
				},
			},
			expectedText:    "ADK thinking...",
			expectedThought: true,
		},
		{
			name: "text part with thought=false",
			part: &protocol.TextPart{
				Text: "Not a thought",
				Metadata: map[string]any{
					ia2a.TextPartMetadataThoughtKey: false,
				},
			},
			expectedText:    "Not a thought",
			expectedThought: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, isThought := processTextPart(tt.part)
			if text != tt.expectedText {
				t.Errorf("expected text %q, got %q", tt.expectedText, text)
			}
			if isThought != tt.expectedThought {
				t.Errorf("expected isThought=%v, got %v", tt.expectedThought, isThought)
			}
		})
	}
}

func TestParseA2AMessageParts_WithReasoningContent(t *testing.T) {
	tests := []struct {
		name              string
		msg               *protocol.Message
		expectedText      string
		expectedReasoning string
	}{
		{
			name: "message with reasoning and content",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{
						Text: "Thinking about this...",
						Metadata: map[string]any{
							ia2a.TextPartMetadataThoughtKey: true,
						},
					},
					&protocol.TextPart{
						Text: "Here is the answer",
					},
				},
			},
			expectedText:      "Here is the answer",
			expectedReasoning: "Thinking about this...",
		},
		{
			name: "message with only reasoning",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{
						Text: "Just thinking...",
						Metadata: map[string]any{
							ia2a.TextPartMetadataThoughtKey: true,
						},
					},
				},
			},
			expectedText:      "",
			expectedReasoning: "Just thinking...",
		},
		{
			name: "message with only content",
			msg: &protocol.Message{
				Parts: []protocol.Part{
					&protocol.TextPart{
						Text: "Just content",
					},
				},
			},
			expectedText:      "Just content",
			expectedReasoning: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseA2AMessageParts(tt.msg)
			if result.textContent != tt.expectedText {
				t.Errorf("expected textContent %q, got %q", tt.expectedText, result.textContent)
			}
			if result.reasoningContent != tt.expectedReasoning {
				t.Errorf("expected reasoningContent %q, got %q", tt.expectedReasoning, result.reasoningContent)
			}
		})
	}
}

func TestBuildStreamingResponse_WithReasoningContent(t *testing.T) {
	resp := buildStreamingResponse("msg-123", &parseResult{
		textContent:      "Answer",
		reasoningContent: "Thinking...",
	}, protocol.MessageRoleAgent)
	if resp == nil {
		t.Fatalf("expected response, got nil")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected one choice, got %d", len(resp.Choices))
	}
	delta := resp.Choices[0].Delta
	if delta.Content != "Answer" {
		t.Errorf("expected content %q, got %q", "Answer", delta.Content)
	}
	if delta.ReasoningContent != "Thinking..." {
		t.Errorf("expected reasoningContent %q, got %q", "Thinking...", delta.ReasoningContent)
	}
}

func TestBuildNonStreamingResponse_WithReasoningContent(t *testing.T) {
	resp := buildNonStreamingResponse("msg-123", &parseResult{
		textContent:      "Answer",
		reasoningContent: "Thinking...",
	}, protocol.MessageRoleAgent)
	if resp == nil {
		t.Fatalf("expected response, got nil")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected one choice, got %d", len(resp.Choices))
	}
	msg := resp.Choices[0].Message
	if msg.Content != "Answer" {
		t.Errorf("expected content %q, got %q", "Answer", msg.Content)
	}
	if msg.ReasoningContent != "Thinking..." {
		t.Errorf("expected reasoningContent %q, got %q", "Thinking...", msg.ReasoningContent)
	}
}

func TestConvertA2ARoleToModelRole(t *testing.T) {
	tests := []struct {
		name     string
		role     protocol.MessageRole
		expected model.Role
	}{
		{
			name:     "MessageRoleUser converts to RoleUser",
			role:     protocol.MessageRoleUser,
			expected: model.RoleUser,
		},
		{
			name:     "MessageRoleAgent converts to RoleAssistant",
			role:     protocol.MessageRoleAgent,
			expected: model.RoleAssistant,
		},
		{
			name:     "unknown role defaults to RoleAssistant",
			role:     protocol.MessageRole("unknown"),
			expected: model.RoleAssistant,
		},
		{
			name:     "empty role defaults to RoleAssistant",
			role:     protocol.MessageRole(""),
			expected: model.RoleAssistant,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertA2ARoleToModelRole(tt.role)
			if result != tt.expected {
				t.Errorf("convertA2ARoleToModelRole(%v) = %v, want %v", tt.role, result, tt.expected)
			}
		})
	}
}

func TestAppendContentPart_UnknownType(t *testing.T) {
	parts := appendContentPart(nil, model.ContentPart{Type: "unknown"})
	if len(parts) != 0 {
		t.Fatalf("expected empty parts for unknown content type, got %d", len(parts))
	}
}

func TestAppendTextPart_NilText(t *testing.T) {
	parts := appendTextPart(nil, model.ContentPart{Type: model.ContentTypeText, Text: nil})
	if len(parts) != 0 {
		t.Fatalf("expected empty parts for nil Text, got %d", len(parts))
	}
}

func TestAppendImagePart_NilImage(t *testing.T) {
	parts := appendImagePart(nil, model.ContentPart{Type: model.ContentTypeImage, Image: nil})
	if len(parts) != 0 {
		t.Fatalf("expected empty parts for nil Image, got %d", len(parts))
	}
}

func TestAppendImagePart_NoDataNoURL(t *testing.T) {
	parts := appendImagePart(nil, model.ContentPart{
		Type:  model.ContentTypeImage,
		Image: &model.Image{},
	})
	if len(parts) != 0 {
		t.Fatalf("expected empty parts for Image with no data and no URL, got %d", len(parts))
	}
}

func TestAppendImagePart_WithURL(t *testing.T) {
	parts := appendImagePart(nil, model.ContentPart{
		Type: model.ContentTypeImage,
		Image: &model.Image{
			URL:    "https://example.com/img.png",
			Format: "image/png",
		},
	})
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].GetKind() != protocol.KindFile {
		t.Fatalf("expected file kind, got %s", parts[0].GetKind())
	}
}

func TestAppendAudioPart_NilAudio(t *testing.T) {
	parts := appendAudioPart(nil, model.ContentPart{Type: model.ContentTypeAudio, Audio: nil})
	if len(parts) != 0 {
		t.Fatalf("expected empty parts for nil Audio, got %d", len(parts))
	}
}

func TestAppendAudioPart_NilData(t *testing.T) {
	parts := appendAudioPart(nil, model.ContentPart{
		Type:  model.ContentTypeAudio,
		Audio: &model.Audio{Data: nil, Format: "wav"},
	})
	if len(parts) != 0 {
		t.Fatalf("expected empty parts for Audio with nil Data, got %d", len(parts))
	}
}

func TestAppendAudioPart_WithData(t *testing.T) {
	audioData := []byte("fake-audio-data")
	parts := appendAudioPart(nil, model.ContentPart{
		Type:  model.ContentTypeAudio,
		Audio: &model.Audio{Data: audioData, Format: "wav"},
	})
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].GetKind() != protocol.KindFile {
		t.Fatalf("expected file kind, got %s", parts[0].GetKind())
	}
}

func TestAppendFilePart_NilFile(t *testing.T) {
	parts := appendFilePart(nil, model.ContentPart{Type: model.ContentTypeFile, File: nil})
	if len(parts) != 0 {
		t.Fatalf("expected empty parts for nil File, got %d", len(parts))
	}
}

func TestAppendFilePart_EmptyData(t *testing.T) {
	parts := appendFilePart(nil, model.ContentPart{
		Type: model.ContentTypeFile,
		File: &model.File{Name: "test.txt", Data: nil},
	})
	if len(parts) != 0 {
		t.Fatalf("expected empty parts for File with empty Data, got %d", len(parts))
	}
}

func TestAppendFilePart_WithFileIDAndDefaultName(t *testing.T) {
	parts := appendFilePart(nil, model.ContentPart{
		Type: model.ContentTypeFile,
		File: &model.File{
			FileID:   "file-id",
			MimeType: "text/plain",
		},
	})
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}

	filePart, ok := parts[0].(*protocol.FilePart)
	if !ok {
		t.Fatalf("expected *protocol.FilePart, got %T", parts[0])
	}

	fileWithURI, ok := filePart.File.(*protocol.FileWithURI)
	if !ok {
		t.Fatalf(
			"expected *protocol.FileWithURI, got %T",
			filePart.File,
		)
	}
	if fileWithURI.Name == nil || *fileWithURI.Name != "file" {
		t.Fatalf("unexpected file name: %+v", fileWithURI.Name)
	}
	if fileWithURI.URI != "file-id" {
		t.Fatalf("unexpected file URI: %s", fileWithURI.URI)
	}
}

func TestProcessFunctionCall_NonMapData(t *testing.T) {
	dataPart := protocol.NewDataPart("not-a-map")
	result := processFunctionCall(&dataPart)
	if result != nil {
		t.Fatalf("expected nil for non-map data, got %+v", result)
	}
}

func TestProcessFunctionCall_ArgsAsMap(t *testing.T) {
	dataPart := protocol.NewDataPart(map[string]any{
		ia2a.ToolCallFieldID:   "call-1",
		ia2a.ToolCallFieldType: "function",
		ia2a.ToolCallFieldName: "my_tool",
		ia2a.ToolCallFieldArgs: map[string]any{"key": "value"},
	})
	result := processFunctionCall(&dataPart)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Function.Name != "my_tool" {
		t.Errorf("unexpected function name: %s", result.Function.Name)
	}
	if string(result.Function.Arguments) != `{"key":"value"}` {
		t.Errorf("unexpected arguments: %s", string(result.Function.Arguments))
	}
}

func TestProcessFunctionCall_ArgsUnexpectedType(t *testing.T) {
	dataPart := protocol.NewDataPart(map[string]any{
		ia2a.ToolCallFieldName: "my_tool",
		ia2a.ToolCallFieldArgs: 12345,
	})
	result := processFunctionCall(&dataPart)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Function.Arguments) != 0 {
		t.Errorf("expected empty arguments for unexpected type, got %s", string(result.Function.Arguments))
	}
}

func TestBuildNonStreamingResponse_EmptyParseResult(t *testing.T) {
	result := &parseResult{}
	resp := buildNonStreamingResponse("msg-1", result, protocol.MessageRoleAgent)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Role != model.RoleAssistant {
		t.Errorf("expected RoleAssistant, got %s", resp.Choices[0].Message.Role)
	}
	if resp.Choices[0].Message.Content != "" {
		t.Errorf("expected empty content, got %q", resp.Choices[0].Message.Content)
	}
}
