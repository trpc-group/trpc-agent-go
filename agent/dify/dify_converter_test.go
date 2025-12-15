//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dify

import (
	"context"
	"testing"
	"time"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestDefaultDifyEventConverter_ConvertToEvent(t *testing.T) {
	converter := &defaultDifyEventConverter{}

	t.Run("converts valid response", func(t *testing.T) {
		resp := &dify.ChatMessageResponse{
			Answer: "Hello, world!",
		}

		invocation := &agent.Invocation{
			InvocationID: "inv-123",
		}

		event := converter.ConvertToEvent(resp, "test-agent", invocation)

		if event == nil {
			t.Fatal("expected event, got nil")
		}
		if event.InvocationID != "inv-123" {
			t.Errorf("expected invocation ID 'inv-123', got '%s'", event.InvocationID)
		}
		if event.Author != "test-agent" {
			t.Errorf("expected author 'test-agent', got '%s'", event.Author)
		}
		if event.Response == nil {
			t.Fatal("expected response, got nil")
		}
		if len(event.Response.Choices) != 1 {
			t.Errorf("expected 1 choice, got %d", len(event.Response.Choices))
		}

		choice := event.Response.Choices[0]
		if choice.Message.Content != "Hello, world!" {
			t.Errorf("expected content 'Hello, world!', got '%s'", choice.Message.Content)
		}
		if choice.Message.Role != model.RoleAssistant {
			t.Errorf("expected role assistant, got '%s'", choice.Message.Role)
		}
		if !event.Response.Done {
			t.Error("expected response to be marked as done")
		}
		if event.Response.IsPartial {
			t.Error("expected response to not be marked as partial")
		}
	})

	t.Run("handles nil response", func(t *testing.T) {
		invocation := &agent.Invocation{
			InvocationID: "inv-123",
		}

		event := converter.ConvertToEvent(nil, "test-agent", invocation)

		if event == nil {
			t.Fatal("expected event, got nil")
		}
		if event.Response == nil {
			t.Fatal("expected response, got nil")
		}
		if len(event.Response.Choices) != 1 {
			t.Errorf("expected 1 choice, got %d", len(event.Response.Choices))
		}

		choice := event.Response.Choices[0]
		if choice.Message.Content != "" {
			t.Errorf("expected empty content, got '%s'", choice.Message.Content)
		}
	})

	t.Run("handles empty answer", func(t *testing.T) {
		resp := &dify.ChatMessageResponse{
			Answer: "",
		}

		invocation := &agent.Invocation{
			InvocationID: "inv-123",
		}

		event := converter.ConvertToEvent(resp, "test-agent", invocation)

		if event == nil {
			t.Fatal("expected event, got nil")
		}

		choice := event.Response.Choices[0]
		if choice.Message.Content != "" {
			t.Errorf("expected empty content, got '%s'", choice.Message.Content)
		}
	})
}

func TestDefaultDifyEventConverter_ConvertStreamingToEvent(t *testing.T) {
	converter := &defaultDifyEventConverter{}

	t.Run("converts streaming response", func(t *testing.T) {
		resp := dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer: "Hello",
			},
		}

		invocation := &agent.Invocation{
			InvocationID: "inv-123",
		}

		event := converter.ConvertStreamingToEvent(resp, "test-agent", invocation)

		if event == nil {
			t.Fatal("expected event, got nil")
		}
		if event.InvocationID != "inv-123" {
			t.Errorf("expected invocation ID 'inv-123', got '%s'", event.InvocationID)
		}
		if event.Author != "test-agent" {
			t.Errorf("expected author 'test-agent', got '%s'", event.Author)
		}
		if event.Response == nil {
			t.Fatal("expected response, got nil")
		}
		if event.Response.Object != model.ObjectTypeChatCompletionChunk {
			t.Errorf("expected object type chat completion chunk, got '%s'", event.Response.Object)
		}
		if len(event.Response.Choices) != 1 {
			t.Errorf("expected 1 choice, got %d", len(event.Response.Choices))
		}

		choice := event.Response.Choices[0]
		if choice.Delta.Content != "Hello" {
			t.Errorf("expected delta content 'Hello', got '%s'", choice.Delta.Content)
		}
		if choice.Delta.Role != model.RoleAssistant {
			t.Errorf("expected role assistant, got '%s'", choice.Delta.Role)
		}
		if !event.Response.IsPartial {
			t.Error("expected response to be marked as partial")
		}
		if event.Response.Done {
			t.Error("expected response to not be marked as done")
		}
	})

	t.Run("handles empty answer", func(t *testing.T) {
		resp := dify.ChatMessageStreamChannelResponse{
			ChatMessageStreamResponse: dify.ChatMessageStreamResponse{
				Answer: "",
			},
		}

		invocation := &agent.Invocation{
			InvocationID: "inv-123",
		}

		event := converter.ConvertStreamingToEvent(resp, "test-agent", invocation)

		if event != nil {
			t.Error("expected nil event for empty answer")
		}
	})
}

func TestDefaultDifyRequestConverter_ConvertToDifyRequest(t *testing.T) {
	converter := &defaultEventDifyConverter{}

	t.Run("converts basic message", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Hello, assistant!",
			},
			Session: &session.Session{
				UserID: "user-123",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if req == nil {
			t.Fatal("expected request, got nil")
		}
		if req.Query != "Hello, assistant!" {
			t.Errorf("expected query 'Hello, assistant!', got '%s'", req.Query)
		}
		if req.User != "user-123" {
			t.Errorf("expected user 'user-123', got '%s'", req.User)
		}
		if req.ResponseMode != "" {
			t.Errorf("expected empty response mode for non-streaming, got '%s'", req.ResponseMode)
		}
		if req.Inputs == nil {
			t.Error("expected inputs to be initialized")
		}
	})

	t.Run("converts streaming message", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Stream this!",
			},
			Session: &session.Session{
				UserID: "user-456",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, true)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if req.ResponseMode != "streaming" {
			t.Errorf("expected response mode 'streaming', got '%s'", req.ResponseMode)
		}
	})

	t.Run("handles nil session", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test message",
			},
			Session: nil,
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if req.User != "anonymous" {
			t.Errorf("expected user 'anonymous' for nil session, got '%s'", req.User)
		}
	})

	t.Run("handles empty user ID", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test message",
			},
			Session: &session.Session{
				UserID: "",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if req.User != "anonymous" {
			t.Errorf("expected user 'anonymous' for empty user ID, got '%s'", req.User)
		}
	})

	t.Run("handles content parts", func(t *testing.T) {
		textContent := "Additional text"
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Main content",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: &textContent,
					},
				},
			},
			Session: &session.Session{
				UserID: "user-789",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		expectedQuery := "Main content\nAdditional text"
		if req.Query != expectedQuery {
			t.Errorf("expected query '%s', got '%s'", expectedQuery, req.Query)
		}
	})

	t.Run("handles image content parts", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Check this image",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL: "http://example.com/image.jpg",
						},
					},
				},
			},
			Session: &session.Session{
				UserID: "user-123",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		if req.Inputs["image_url"] != "http://example.com/image.jpg" {
			t.Errorf("expected image_url in inputs, got: %v", req.Inputs["image_url"])
		}
	})

	t.Run("handles file content parts", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Check this file",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeFile,
						File: &model.File{
							Name: "document.pdf",
						},
					},
				},
			},
			Session: &session.Session{
				UserID: "user-123",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		if req.Inputs["file_name"] != "document.pdf" {
			t.Errorf("expected file_name in inputs, got: %v", req.Inputs["file_name"])
		}
	})

	t.Run("handles unknown content parts", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test",
				ContentParts: []model.ContentPart{
					{
						Type: "unknown_type",
					},
				},
			},
			Session: &session.Session{
				UserID: "user-123",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		if val, ok := req.Inputs["other_content_type"].(model.ContentType); !ok || val != "unknown_type" {
			t.Errorf("expected other_content_type to be 'unknown_type', got: %v", req.Inputs["other_content_type"])
		}
	})

	t.Run("handles text content part without main content", func(t *testing.T) {
		textContent := "Only text part"
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: &textContent,
					},
				},
			},
			Session: &session.Session{
				UserID: "user-123",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		if req.Query != "Only text part" {
			t.Errorf("expected query 'Only text part', got '%s'", req.Query)
		}
	})

	t.Run("handles nil text in text content part", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Main",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeText,
						Text: nil,
					},
				},
			},
			Session: &session.Session{
				UserID: "user-123",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		if req.Query != "Main" {
			t.Errorf("expected query 'Main', got '%s'", req.Query)
		}
	})

	t.Run("handles empty image URL", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL: "",
						},
					},
				},
			},
			Session: &session.Session{
				UserID: "user-123",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		if _, exists := req.Inputs["image_url"]; exists {
			t.Error("image_url should not be added for empty URL")
		}
	})

	t.Run("handles empty file name", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test",
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeFile,
						File: &model.File{
							Name: "",
						},
					},
				},
			},
			Session: &session.Session{
				UserID: "user-123",
			},
		}

		req, err := converter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		if _, exists := req.Inputs["file_name"]; exists {
			t.Error("file_name should not be added for empty name")
		}
	})
}

// TestCustomConverters tests custom converter implementations
func TestCustomConverters(t *testing.T) {
	t.Run("custom event converter", func(t *testing.T) {
		customConverter := &customTestEventConverter{}

		resp := &dify.ChatMessageResponse{
			Answer: "Test response",
		}

		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		event := customConverter.ConvertToEvent(resp, "custom-agent", invocation)

		// Custom converter should add prefix
		if event.Response.Choices[0].Message.Content != "CUSTOM: Test response" {
			t.Errorf("expected custom prefix, got: %s", event.Response.Choices[0].Message.Content)
		}
	})

	t.Run("custom request converter", func(t *testing.T) {
		customConverter := &customTestRequestConverter{}

		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test query",
			},
		}

		req, err := customConverter.ConvertToDifyRequest(context.Background(), invocation, false)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		// Custom converter should add prefix
		if req.Query != "CUSTOM: Test query" {
			t.Errorf("expected custom prefix, got: %s", req.Query)
		}

		// Custom converter should add metadata
		if req.Inputs["custom_metadata"] != "added_by_converter" {
			t.Errorf("expected custom metadata, got: %v", req.Inputs["custom_metadata"])
		}
	})
}

// Custom test converters for testing
type customTestEventConverter struct{}

func (c *customTestEventConverter) ConvertToEvent(
	resp *dify.ChatMessageResponse,
	agentName string,
	invocation *agent.Invocation,
) *event.Event {
	var content string
	if resp != nil {
		content = "CUSTOM: " + resp.Answer
	}

	message := model.Message{
		Role:    model.RoleAssistant,
		Content: content,
	}

	return event.New(
		invocation.InvocationID,
		agentName,
		event.WithResponse(&model.Response{
			Choices:   []model.Choice{{Message: message}},
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			Done:      true,
		}),
	)
}

func (c *customTestEventConverter) ConvertStreamingToEvent(
	resp dify.ChatMessageStreamChannelResponse,
	agentName string,
	invocation *agent.Invocation,
) *event.Event {
	if resp.ChatMessageStreamResponse.Answer == "" {
		return nil
	}

	message := model.Message{
		Role:    model.RoleAssistant,
		Content: "CUSTOM: " + resp.ChatMessageStreamResponse.Answer,
	}

	return event.New(
		invocation.InvocationID,
		agentName,
		event.WithResponse(&model.Response{
			Object:    model.ObjectTypeChatCompletionChunk,
			Choices:   []model.Choice{{Delta: message}},
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			IsPartial: true,
		}),
	)
}

type customTestRequestConverter struct{}

func (c *customTestRequestConverter) ConvertToDifyRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	isStream bool,
) (*dify.ChatMessageRequest, error) {
	req := &dify.ChatMessageRequest{
		Query: "CUSTOM: " + invocation.Message.Content,
		Inputs: map[string]any{
			"custom_metadata": "added_by_converter",
		},
	}

	if invocation.Session != nil {
		req.User = invocation.Session.UserID
	}
	if req.User == "" {
		req.User = "anonymous"
	}

	if isStream {
		req.ResponseMode = "streaming"
	}

	return req, nil
}

func TestDefaultDifyEventConverter_BuildRespEvent(t *testing.T) {
	converter := &defaultDifyEventConverter{}

	t.Run("builds non-streaming event", func(t *testing.T) {
		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		// Test with empty message (no parts)
		msg := &protocol.Message{
			Role:  protocol.MessageRoleAgent,
			Parts: []protocol.Part{},
		}

		evt := converter.buildRespEvent(false, msg, "test-agent", invocation)

		if evt == nil {
			t.Fatal("expected event")
		}
		if evt.Response == nil {
			t.Fatal("expected response")
		}
		if evt.Response.Done != true {
			t.Error("expected Done to be true for non-streaming")
		}
		if evt.Response.IsPartial != false {
			t.Error("expected IsPartial to be false for non-streaming")
		}
	})

	t.Run("builds streaming event", func(t *testing.T) {
		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		msg := &protocol.Message{
			Role:  protocol.MessageRoleAgent,
			Parts: []protocol.Part{},
		}

		evt := converter.buildRespEvent(true, msg, "test-agent", invocation)

		if evt == nil {
			t.Fatal("expected event")
		}
		if evt.Response == nil {
			t.Fatal("expected response")
		}
		if evt.Response.Done != false {
			t.Error("expected Done to be false for streaming")
		}
		if evt.Response.IsPartial != true {
			t.Error("expected IsPartial to be true for streaming")
		}
	})

	t.Run("handles text parts", func(t *testing.T) {
		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		textPart := &protocol.TextPart{Text: "Hello, world!"}
		msg := &protocol.Message{
			Role:  protocol.MessageRoleAgent,
			Parts: []protocol.Part{textPart},
		}

		evt := converter.buildRespEvent(false, msg, "test-agent", invocation)

		if evt == nil || evt.Response == nil {
			t.Fatal("expected event with response")
		}
		if len(evt.Response.Choices) == 0 {
			t.Fatal("expected at least one choice")
		}
		if evt.Response.Choices[0].Message.Content != "Hello, world!" {
			t.Errorf("expected content 'Hello, world!', got: %s", evt.Response.Choices[0].Message.Content)
		}
	})

	t.Run("handles multiple text parts", func(t *testing.T) {
		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		textPart1 := &protocol.TextPart{Text: "Hello, "}
		textPart2 := &protocol.TextPart{Text: "world!"}
		msg := &protocol.Message{
			Role:  protocol.MessageRoleAgent,
			Parts: []protocol.Part{textPart1, textPart2},
		}

		evt := converter.buildRespEvent(false, msg, "test-agent", invocation)

		if evt == nil || evt.Response == nil {
			t.Fatal("expected event with response")
		}
		if evt.Response.Choices[0].Message.Content != "Hello, world!" {
			t.Errorf("expected content 'Hello, world!', got: %s", evt.Response.Choices[0].Message.Content)
		}
	})

	t.Run("handles non-text parts gracefully", func(t *testing.T) {
		invocation := &agent.Invocation{
			InvocationID: "test-inv",
		}

		// Create a non-text part (e.g., FilePart)
		filePart := protocol.NewFilePartWithURI("file", "text/plain", "file.txt")

		msg := &protocol.Message{
			Role:  protocol.MessageRoleAgent,
			Parts: []protocol.Part{filePart},
		}

		evt := converter.buildRespEvent(false, msg, "test-agent", invocation)

		if evt == nil || evt.Response == nil {
			t.Fatal("expected event with response")
		}
		// Non-text parts should be skipped, resulting in empty content
		if evt.Response.Choices[0].Message.Content != "" {
			t.Errorf("expected empty content for non-text parts, got: %s", evt.Response.Choices[0].Message.Content)
		}
	})
}

func TestConvertTaskToMessage(t *testing.T) {
	t.Run("converts task with no artifacts", func(t *testing.T) {
		task := &protocol.Task{
			Artifacts: []protocol.Artifact{},
		}

		msg := convertTaskToMessage(task)

		if msg == nil {
			t.Fatal("expected message")
		}
		if msg.Role != protocol.MessageRoleAgent {
			t.Errorf("expected role Agent, got: %s", msg.Role)
		}
		if len(msg.Parts) != 0 {
			t.Errorf("expected no parts, got: %d", len(msg.Parts))
		}
	})

	t.Run("converts task with artifacts", func(t *testing.T) {
		textPart := &protocol.TextPart{Text: "artifact content"}
		task := &protocol.Task{
			Artifacts: []protocol.Artifact{
				{
					Parts: []protocol.Part{textPart},
				},
			},
		}

		msg := convertTaskToMessage(task)

		if msg == nil {
			t.Fatal("expected message")
		}
		if len(msg.Parts) != 1 {
			t.Errorf("expected 1 part, got: %d", len(msg.Parts))
		}
	})
}

func TestConvertTaskStatusToMessage(t *testing.T) {
	t.Run("converts with nil status message", func(t *testing.T) {
		event := &protocol.TaskStatusUpdateEvent{
			Status: protocol.TaskStatus{
				Message: nil,
			},
		}

		msg := convertTaskStatusToMessage(event)

		if msg == nil {
			t.Fatal("expected message")
		}
		if msg.Role != protocol.MessageRoleAgent {
			t.Errorf("expected role Agent, got: %s", msg.Role)
		}
		if msg.Parts != nil {
			t.Error("expected nil parts when status message is nil")
		}
	})

	t.Run("converts with status message", func(t *testing.T) {
		textPart := &protocol.TextPart{Text: "status message"}
		event := &protocol.TaskStatusUpdateEvent{
			Status: protocol.TaskStatus{
				Message: &protocol.Message{
					Parts: []protocol.Part{textPart},
				},
			},
		}

		msg := convertTaskStatusToMessage(event)

		if msg == nil {
			t.Fatal("expected message")
		}
		if len(msg.Parts) != 1 {
			t.Errorf("expected 1 part, got: %d", len(msg.Parts))
		}
	})
}

func TestConvertTaskArtifactToMessage(t *testing.T) {
	t.Run("converts artifact event", func(t *testing.T) {
		textPart := &protocol.TextPart{Text: "artifact content"}
		event := &protocol.TaskArtifactUpdateEvent{
			Artifact: protocol.Artifact{
				Parts: []protocol.Part{textPart},
			},
		}

		msg := convertTaskArtifactToMessage(event)

		if msg == nil {
			t.Fatal("expected message")
		}
		if msg.Role != protocol.MessageRoleAgent {
			t.Errorf("expected role Agent, got: %s", msg.Role)
		}
		if len(msg.Parts) != 1 {
			t.Errorf("expected 1 part, got: %d", len(msg.Parts))
		}
	})
}

func TestExtractTextFromParts(t *testing.T) {
	t.Run("extracts text from single part", func(t *testing.T) {
		textPart := &protocol.TextPart{Text: "Hello"}
		parts := []protocol.Part{textPart}

		content := extractTextFromParts(parts)

		if content != "Hello" {
			t.Errorf("expected 'Hello', got: %s", content)
		}
	})

	t.Run("extracts text from multiple parts", func(t *testing.T) {
		textPart1 := &protocol.TextPart{Text: "Hello, "}
		textPart2 := &protocol.TextPart{Text: "world!"}
		parts := []protocol.Part{textPart1, textPart2}

		content := extractTextFromParts(parts)

		if content != "Hello, world!" {
			t.Errorf("expected 'Hello, world!', got: %s", content)
		}
	})

	t.Run("handles empty parts", func(t *testing.T) {
		parts := []protocol.Part{}

		content := extractTextFromParts(parts)

		if content != "" {
			t.Errorf("expected empty string, got: %s", content)
		}
	})

	t.Run("skips non-text parts", func(t *testing.T) {
		textPart := &protocol.TextPart{Text: "text"}
		filePart := protocol.NewFilePartWithURI("file", "text/plain", "file.txt")
		parts := []protocol.Part{textPart, filePart}

		content := extractTextFromParts(parts)

		if content != "text" {
			t.Errorf("expected 'text', got: %s", content)
		}
	})
}

func TestBuildResponseForEvent(t *testing.T) {
	t.Run("builds streaming response", func(t *testing.T) {
		resp := buildResponseForEvent(true, "streaming content")

		if resp == nil {
			t.Fatal("expected response")
		}
		if !resp.IsPartial {
			t.Error("expected IsPartial to be true")
		}
		if resp.Done {
			t.Error("expected Done to be false")
		}
		if len(resp.Choices) != 1 {
			t.Fatal("expected 1 choice")
		}
		if resp.Choices[0].Delta.Content != "streaming content" {
			t.Errorf("expected delta content 'streaming content', got: %s", resp.Choices[0].Delta.Content)
		}
	})

	t.Run("builds non-streaming response", func(t *testing.T) {
		resp := buildResponseForEvent(false, "complete content")

		if resp == nil {
			t.Fatal("expected response")
		}
		if resp.IsPartial {
			t.Error("expected IsPartial to be false")
		}
		if !resp.Done {
			t.Error("expected Done to be true")
		}
		if len(resp.Choices) != 1 {
			t.Fatal("expected 1 choice")
		}
		if resp.Choices[0].Message.Content != "complete content" {
			t.Errorf("expected message content 'complete content', got: %s", resp.Choices[0].Message.Content)
		}
	})

	t.Run("builds response with empty content", func(t *testing.T) {
		resp := buildResponseForEvent(false, "")

		if resp == nil {
			t.Fatal("expected response")
		}
		if resp.Choices[0].Message.Content != "" {
			t.Errorf("expected empty content, got: %s", resp.Choices[0].Message.Content)
		}
	})
}
