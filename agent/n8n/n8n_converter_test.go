//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package n8n

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestDefaultRequestConverter_ConvertToN8nRequest(t *testing.T) {
	converter := &defaultRequestConverter{}

	t.Run("converts basic message", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Hello, assistant!",
			},
		}

		req, err := converter.ConvertToN8nRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if req == nil {
			t.Fatal("expected request, got nil")
		}
		if req["query"] != "Hello, assistant!" {
			t.Errorf("expected query 'Hello, assistant!', got '%v'", req["query"])
		}
		if req["user"] != "anonymous" {
			t.Errorf("expected user 'anonymous' for nil session, got '%v'", req["user"])
		}
	})

	t.Run("with session user ID", func(t *testing.T) {
		invocation := &agent.Invocation{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "Test message",
			},
			Session: &session.Session{
				UserID: "user-123",
			},
		}

		req, err := converter.ConvertToN8nRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if req["user"] != "user-123" {
			t.Errorf("expected user 'user-123', got '%v'", req["user"])
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

		req, err := converter.ConvertToN8nRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if req["user"] != "anonymous" {
			t.Errorf("expected user 'anonymous' for empty user ID, got '%v'", req["user"])
		}
	})

	t.Run("handles text content parts", func(t *testing.T) {
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

		req, err := converter.ConvertToN8nRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}

		expectedQuery := "Main content\nAdditional text"
		if req["query"] != expectedQuery {
			t.Errorf("expected query '%s', got '%v'", expectedQuery, req["query"])
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

		req, err := converter.ConvertToN8nRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if req["query"] != "Only text part" {
			t.Errorf("expected query 'Only text part', got '%v'", req["query"])
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

		req, err := converter.ConvertToN8nRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if req["query"] != "Main" {
			t.Errorf("expected query 'Main', got '%v'", req["query"])
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

		req, err := converter.ConvertToN8nRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if req["image_url"] != "http://example.com/image.jpg" {
			t.Errorf("expected image_url in request, got: %v", req["image_url"])
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

		req, err := converter.ConvertToN8nRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if _, exists := req["image_url"]; exists {
			t.Error("image_url should not be added for empty URL")
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

		req, err := converter.ConvertToN8nRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if req["file_name"] != "document.pdf" {
			t.Errorf("expected file_name in request, got: %v", req["file_name"])
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

		req, err := converter.ConvertToN8nRequest(context.Background(), invocation)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if _, exists := req["file_name"]; exists {
			t.Error("file_name should not be added for empty name")
		}
	})
}

func TestDefaultResponseConverter_ConvertToEvent(t *testing.T) {
	converter := &defaultResponseConverter{}

	t.Run("json with output field", func(t *testing.T) {
		body := []byte(`{"output": "Hello from n8n"}`)
		invocation := &agent.Invocation{InvocationID: "inv-123"}

		evt := converter.ConvertToEvent(body, "test-agent", invocation)

		if evt == nil {
			t.Fatal("expected event, got nil")
		}
		if evt.InvocationID != "inv-123" {
			t.Errorf("expected invocation ID 'inv-123', got '%s'", evt.InvocationID)
		}
		if evt.Author != "test-agent" {
			t.Errorf("expected author 'test-agent', got '%s'", evt.Author)
		}
		if evt.Response == nil {
			t.Fatal("expected response, got nil")
		}
		if len(evt.Response.Choices) != 1 {
			t.Fatalf("expected 1 choice, got %d", len(evt.Response.Choices))
		}
		if evt.Response.Choices[0].Message.Content != "Hello from n8n" {
			t.Errorf("expected content 'Hello from n8n', got '%s'", evt.Response.Choices[0].Message.Content)
		}
		if evt.Response.Choices[0].Message.Role != model.RoleAssistant {
			t.Errorf("expected role assistant, got '%s'", evt.Response.Choices[0].Message.Role)
		}
		if !evt.Response.Done {
			t.Error("expected response to be marked as done")
		}
		if evt.Response.IsPartial {
			t.Error("expected response to not be marked as partial")
		}
	})

	t.Run("json with answer field", func(t *testing.T) {
		body := []byte(`{"answer": "Answer content"}`)
		invocation := &agent.Invocation{InvocationID: "inv-123"}

		evt := converter.ConvertToEvent(body, "test-agent", invocation)

		if evt == nil {
			t.Fatal("expected event, got nil")
		}
		if evt.Response.Choices[0].Message.Content != "Answer content" {
			t.Errorf("expected content 'Answer content', got '%s'", evt.Response.Choices[0].Message.Content)
		}
	})

	t.Run("json with text field", func(t *testing.T) {
		body := []byte(`{"text": "Text content"}`)
		invocation := &agent.Invocation{InvocationID: "inv-123"}

		evt := converter.ConvertToEvent(body, "test-agent", invocation)

		if evt == nil {
			t.Fatal("expected event, got nil")
		}
		if evt.Response.Choices[0].Message.Content != "Text content" {
			t.Errorf("expected content 'Text content', got '%s'", evt.Response.Choices[0].Message.Content)
		}
	})

	t.Run("json with result field", func(t *testing.T) {
		body := []byte(`{"result": "Result content"}`)
		invocation := &agent.Invocation{InvocationID: "inv-123"}

		evt := converter.ConvertToEvent(body, "test-agent", invocation)

		if evt == nil {
			t.Fatal("expected event, got nil")
		}
		if evt.Response.Choices[0].Message.Content != "Result content" {
			t.Errorf("expected content 'Result content', got '%s'", evt.Response.Choices[0].Message.Content)
		}
	})

	t.Run("json with unknown fields fallback", func(t *testing.T) {
		body := []byte(`{"foo": "bar", "baz": 123}`)
		invocation := &agent.Invocation{InvocationID: "inv-123"}

		evt := converter.ConvertToEvent(body, "test-agent", invocation)

		if evt == nil {
			t.Fatal("expected event, got nil")
		}
		// Should fall back to raw body
		if evt.Response.Choices[0].Message.Content != `{"foo": "bar", "baz": 123}` {
			t.Errorf("expected raw body fallback, got '%s'", evt.Response.Choices[0].Message.Content)
		}
	})

	t.Run("non-json fallback", func(t *testing.T) {
		body := []byte("plain text response")
		invocation := &agent.Invocation{InvocationID: "inv-123"}

		evt := converter.ConvertToEvent(body, "test-agent", invocation)

		if evt == nil {
			t.Fatal("expected event, got nil")
		}
		if evt.Response.Choices[0].Message.Content != "plain text response" {
			t.Errorf("expected content 'plain text response', got '%s'", evt.Response.Choices[0].Message.Content)
		}
	})

	t.Run("field priority output over answer", func(t *testing.T) {
		body := []byte(`{"output": "from output", "answer": "from answer"}`)
		invocation := &agent.Invocation{InvocationID: "inv-123"}

		evt := converter.ConvertToEvent(body, "test-agent", invocation)

		if evt == nil {
			t.Fatal("expected event, got nil")
		}
		if evt.Response.Choices[0].Message.Content != "from output" {
			t.Errorf("expected 'from output' (output takes priority), got '%s'", evt.Response.Choices[0].Message.Content)
		}
	})
}

func TestDefaultResponseConverter_ConvertStreamingToEvent(t *testing.T) {
	converter := &defaultResponseConverter{}

	t.Run("valid chunk", func(t *testing.T) {
		data := []byte(`{"output": "streaming chunk"}`)
		invocation := &agent.Invocation{InvocationID: "inv-123"}

		evt := converter.ConvertStreamingToEvent(data, "test-agent", invocation)

		if evt == nil {
			t.Fatal("expected event, got nil")
		}
		if evt.InvocationID != "inv-123" {
			t.Errorf("expected invocation ID 'inv-123', got '%s'", evt.InvocationID)
		}
		if evt.Author != "test-agent" {
			t.Errorf("expected author 'test-agent', got '%s'", evt.Author)
		}
		if evt.Response == nil {
			t.Fatal("expected response, got nil")
		}
		if evt.Response.Object != model.ObjectTypeChatCompletionChunk {
			t.Errorf("expected object type chat completion chunk, got '%s'", evt.Response.Object)
		}
		if len(evt.Response.Choices) != 1 {
			t.Fatalf("expected 1 choice, got %d", len(evt.Response.Choices))
		}
		if evt.Response.Choices[0].Delta.Content != "streaming chunk" {
			t.Errorf("expected delta content 'streaming chunk', got '%s'", evt.Response.Choices[0].Delta.Content)
		}
		if evt.Response.Choices[0].Delta.Role != model.RoleAssistant {
			t.Errorf("expected role assistant, got '%s'", evt.Response.Choices[0].Delta.Role)
		}
		if !evt.Response.IsPartial {
			t.Error("expected response to be marked as partial")
		}
		if evt.Response.Done {
			t.Error("expected response to not be marked as done")
		}
	})

	t.Run("empty content returns nil", func(t *testing.T) {
		data := []byte(`{"output": ""}`)
		invocation := &agent.Invocation{InvocationID: "inv-123"}

		evt := converter.ConvertStreamingToEvent(data, "test-agent", invocation)

		if evt != nil {
			t.Error("expected nil event for empty content")
		}
	})

	t.Run("empty json returns nil", func(t *testing.T) {
		// Empty JSON object has no known fields, falls back to raw body which is "{}"
		// That's not empty, so it would return an event. Let's test truly empty content.
		data := []byte("")
		invocation := &agent.Invocation{InvocationID: "inv-123"}

		evt := converter.ConvertStreamingToEvent(data, "test-agent", invocation)

		if evt != nil {
			t.Error("expected nil event for empty data")
		}
	})
}

func TestExtractContentFromBody(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		expected string
	}{
		{
			name:     "output field",
			body:     []byte(`{"output": "output value"}`),
			expected: "output value",
		},
		{
			name:     "answer field",
			body:     []byte(`{"answer": "answer value"}`),
			expected: "answer value",
		},
		{
			name:     "text field",
			body:     []byte(`{"text": "text value"}`),
			expected: "text value",
		},
		{
			name:     "result field",
			body:     []byte(`{"result": "result value"}`),
			expected: "result value",
		},
		{
			name:     "unknown fields fallback to raw body",
			body:     []byte(`{"unknown": "value"}`),
			expected: `{"unknown": "value"}`,
		},
		{
			name:     "non-json fallback",
			body:     []byte("just plain text"),
			expected: "just plain text",
		},
		{
			name:     "empty json fallback to raw body",
			body:     []byte(`{}`),
			expected: `{}`,
		},
		{
			name:     "empty output value",
			body:     []byte(`{"output": ""}`),
			expected: "",
		},
		{
			name:     "output takes priority over answer",
			body:     []byte(`{"output": "from output", "answer": "from answer"}`),
			expected: "from output",
		},
		{
			name:     "answer takes priority over text",
			body:     []byte(`{"answer": "from answer", "text": "from text"}`),
			expected: "from answer",
		},
		{
			name:     "empty body",
			body:     []byte(""),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractContentFromBody(tt.body)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}
