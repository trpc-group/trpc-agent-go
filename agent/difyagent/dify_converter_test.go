//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package difyagent

import (
	"context"
	"testing"
	"time"

	"github.com/cloudernative/dify-sdk-go"
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
		Inputs: map[string]interface{}{
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
