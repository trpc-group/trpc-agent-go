//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hunyuan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {

	client := NewClient(
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl("https://hunyuan.tencentcloudapi.com"),
		WithHost("hunyuan.tencentcloudapi.com"),
		WithHttpClient(http.DefaultClient),
	)

	if client == nil {
		t.Fatal("NewClient returned nil")
	}

	if client.config.secretId != "test-secret-id" {
		t.Errorf("Expected secretId %s, got %s", "test-secret-id", client.config.secretId)
	}

	if client.config.secretKey != "test-secret-key" {
		t.Errorf("Expected secretKey %s, got %s", "test-secret-key", client.config.secretKey)
	}
	if client.config.baseUrl != "https://hunyuan.tencentcloudapi.com" {
		t.Errorf("Expected baseUrl %s, got %s", "https://hunyuan.tencentcloudapi.com", client.config.baseUrl)
	}
	if client.config.host != "hunyuan.tencentcloudapi.com" {
		t.Errorf("Expected host %s, got %s", "hunyuan.tencentcloudapi.com", client.config.host)
	}

	if client.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
}

func TestGetAuthorization(t *testing.T) {

	client := NewClient(WithSecretId("test-secret-id"), WithSecretKey("test-secret-key"))
	payload := `{"Model":"hunyuan-lite","Messages":[{"Role":"user","Content":"hello"}]}`

	authorization := client.getAuthorization(payload, time.Now().Unix())

	if authorization == "" {
		t.Error("Authorization should not be empty")
	}

	if len(authorization) < 50 {
		t.Errorf("Authorization seems too short: %s", authorization)
	}

	if authorization[:16] != "TC3-HMAC-SHA256 " {
		t.Errorf("Authorization should start with 'TC3-HMAC-SHA256 ', got: %s", authorization[:16])
	}
}

func TestChatCompletionNewParams(t *testing.T) {
	params := &ChatCompletionNewParams{
		Model: "hunyuan-lite",
		Messages: []*ChatCompletionMessageParam{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Temperature: 0.7,
		TopP:        0.9,
	}

	if params.Model != "hunyuan-lite" {
		t.Errorf("Expected Model 'hunyuan-lite', got %s", params.Model)
	}

	if len(params.Messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(params.Messages))
	}

	if params.Messages[0].Role != "user" {
		t.Errorf("Expected Role 'user', got %s", params.Messages[0].Role)
	}

	if params.Messages[0].Content != "Hello" {
		t.Errorf("Expected Content 'Hello', got %s", params.Messages[0].Content)
	}
}

func TestChatCompletionWithContext(t *testing.T) {

	client := NewClient(WithSecretId("test-secret-id"), WithSecretKey("test-secret-key"))
	ctx := context.Background()

	params := &ChatCompletionNewParams{
		Model: "hunyuan-lite",
		Messages: []*ChatCompletionMessageParam{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
	}

	_, err := client.ChatCompletion(ctx, params)

	if err == nil {
		t.Log("Note: This test uses mock credentials and is expected to fail in real API calls")
	}
}

func TestSha256hex(t *testing.T) {
	input := "test"
	expected := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	result := sha256hex(input)

	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestHmacSha256(t *testing.T) {
	input := "test"
	key := "key"

	result := hmacSha256(input, key)

	if result == "" {
		t.Error("hmacSha256 should not return empty string")
	}

	result2 := hmacSha256(input, key)
	if result != result2 {
		t.Error("hmacSha256 should return consistent results")
	}
}

func TestChatCompletionMessageParam(t *testing.T) {
	msg := &ChatCompletionMessageParam{
		Role:    "user",
		Content: "Hello",
	}

	if msg.Role != "user" {
		t.Errorf("Expected Role 'user', got %s", msg.Role)
	}

	toolCall := &ChatCompletionMessageToolCall{
		Type: "function",
		Function: &ChatCompletionMessageToolCallFunction{
			Name:      "get_weather",
			Arguments: `{"location":"Beijing"}`,
		},
	}

	msgWithTools := &ChatCompletionMessageParam{
		Role:      "assistant",
		ToolCalls: []*ChatCompletionMessageToolCall{toolCall},
	}

	if len(msgWithTools.ToolCalls) != 1 {
		t.Errorf("Expected 1 tool call, got %d", len(msgWithTools.ToolCalls))
	}

	if msgWithTools.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("Expected function name 'get_weather', got %s", msgWithTools.ToolCalls[0].Function.Name)
	}
}

func TestChatCompletionWithMockServer(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}

		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		if r.Header.Get("Authorization") == "" {
			t.Error("Authorization header should not be empty")
		}

		mockResponse := chatCompletionResponseData{
			Response: ChatCompletionResponse{
				Id:      "test-id-123",
				Created: 1234567890,
				Choices: []*ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: "stop",
						Message: &ChatCompletionMessageParam{
							Role:    "assistant",
							Content: "Hello! I'm a test response from mock server.",
						},
					},
				},
				Usage: ChatCompletionResponseUsage{
					PromptTokens:     10,
					CompletionTokens: 20,
					TotalTokens:      30,
				},
				RequestId: "test-request-id",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer mockServer.Close()

	client := NewClient(
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl(mockServer.URL),
		WithHost("test-host"),
	)

	ctx := context.Background()
	params := &ChatCompletionNewParams{
		Model: "hunyuan-lite",
		Messages: []*ChatCompletionMessageParam{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Temperature: 0.7,
	}

	response, err := client.ChatCompletion(ctx, params)
	if err != nil {
		t.Fatalf("ChatCompletion failed: %v", err)
	}

	if response.Id != "test-id-123" {
		t.Errorf("Expected Id 'test-id-123', got %s", response.Id)
	}

	if len(response.Choices) != 1 {
		t.Fatalf("Expected 1 choice, got %d", len(response.Choices))
	}

	if response.Choices[0].Message.Content != "Hello! I'm a test response from mock server." {
		t.Errorf("Unexpected message content: %s", response.Choices[0].Message.Content)
	}

	if response.Usage.TotalTokens != 30 {
		t.Errorf("Expected TotalTokens 30, got %d", response.Usage.TotalTokens)
	}
}

func TestChatCompletionWithMockServerError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockResponse := chatCompletionResponseData{
			Response: ChatCompletionResponse{
				RequestId: "test-request-id",
				Error: &ChatCompletionErrorInfo{
					Code:    "400",
					Message: "Invalid parameter: Temperature must be 2 or less",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer mockServer.Close()

	client := NewClient(
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl(mockServer.URL),
		WithHost("test-host"),
	)

	ctx := context.Background()
	params := &ChatCompletionNewParams{
		Model:       "hunyuan-lite",
		Messages:    []*ChatCompletionMessageParam{{Role: "user", Content: "Hello"}},
		Temperature: 3.0,
	}

	_, err := client.ChatCompletion(ctx, params)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if !strings.Contains(err.Error(), "Invalid parameter") {
		t.Errorf("Expected error message to contain 'Invalid parameter', got: %v", err)
	}
}

func TestChatCompletionWithMockServerHttpError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockServer.Close()

	client := NewClient(
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl("invalid"),
		WithHost("test-host"),
	)

	ctx := context.Background()
	params := &ChatCompletionNewParams{
		Model:       "hunyuan-lite",
		Messages:    []*ChatCompletionMessageParam{{Role: "user", Content: "Hello"}},
		Temperature: 3.0,
	}

	_, err := client.ChatCompletion(ctx, params)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	client = NewClient(WithBaseUrl(mockServer.URL))
	_, err = client.ChatCompletion(ctx, params)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
}

func TestChatCompletionStreamWithMockServer(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}

		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Expected Accept text/event-stream, got %s", r.Header.Get("Accept"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		chunks := []ChatCompletionResponse{
			{
				Id:      "stream-id-1",
				Created: 1234567890,
				Choices: []*ChatCompletionResponseChoice{
					{
						Index: 0,
						Delta: &ChatCompletionResponseDelta{
							Role:    "assistant",
							Content: "Hello",
						},
					},
				},
			},
			{
				Id:      "stream-id-2",
				Created: 1234567891,
				Choices: []*ChatCompletionResponseChoice{
					{
						Index: 0,
						Delta: &ChatCompletionResponseDelta{
							Content: " from",
						},
					},
				},
			},
			{
				Id:      "stream-id-3",
				Created: 1234567892,
				Choices: []*ChatCompletionResponseChoice{
					{
						Index: 0,
						Delta: &ChatCompletionResponseDelta{
							Content: " mock server!",
						},
						FinishReason: "stop",
					},
				},
			},
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("Expected http.ResponseWriter to be an http.Flusher")
		}

		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer mockServer.Close()

	client := NewClient(
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl(mockServer.URL),
		WithHost("test-host"),
	)

	ctx := context.Background()
	params := &ChatCompletionNewParams{
		Model: "hunyuan-lite",
		Messages: []*ChatCompletionMessageParam{
			{
				Role:    "user",
				Content: "Hello",
			},
		},
		Stream: true,
	}

	var chunks []*ChatCompletionResponse
	var fullContent string

	err := client.ChatCompletionStream(ctx, params, func(chunk *ChatCompletionResponse) error {
		chunks = append(chunks, chunk)
		if len(chunk.Choices) > 0 {
			fullContent += chunk.Choices[0].Delta.Content
		}
		return nil
	})

	if err != nil {
		t.Fatalf("ChatCompletionStream failed: %v", err)
	}

	if len(chunks) != 3 {
		t.Errorf("Expected 3 chunks, got %d", len(chunks))
	}

	expectedContent := "Hello from mock server!"
	if fullContent != expectedContent {
		t.Errorf("Expected content '%s', got '%s'", expectedContent, fullContent)
	}

	if len(chunks) > 0 && len(chunks[len(chunks)-1].Choices) > 0 {
		lastChunk := chunks[len(chunks)-1]
		if lastChunk.Choices[0].FinishReason != "stop" {
			t.Errorf("Expected finish reason 'stop', got '%s'", lastChunk.Choices[0].FinishReason)
		}
	}
}

func TestChatCompletionStreamWithMockServerError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		errorResponse := chatCompletionResponseData{
			Response: ChatCompletionResponse{
				RequestId: "test-request-id",
				Error: &ChatCompletionErrorInfo{
					Code:    "500",
					Message: "Internal server error",
				},
			},
		}

		data, _ := json.Marshal(errorResponse)
		fmt.Fprintf(w, "%s\n", string(data))

		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer mockServer.Close()

	client := NewClient(
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl(mockServer.URL),
		WithHost("test-host"),
	)

	ctx := context.Background()
	params := &ChatCompletionNewParams{
		Model:    "hunyuan-lite",
		Messages: []*ChatCompletionMessageParam{{Role: "user", Content: "Hello"}},
		Stream:   true,
	}

	err := client.ChatCompletionStream(ctx, params, func(chunk *ChatCompletionResponse) error {
		return nil
	})

	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if !strings.Contains(err.Error(), "Internal server error") {
		t.Errorf("Expected error message to contain 'Internal server error', got: %v", err)
	}
}

func TestChatCompletionStreamContextCancellation(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		chunk := ChatCompletionResponse{
			Id:      "stream-id-1",
			Created: 1234567890,
			Choices: []*ChatCompletionResponseChoice{
				{
					Index: 0,
					Delta: &ChatCompletionResponseDelta{
						Content: "Hello",
					},
				},
			},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		flusher.Flush()

		time.Sleep(2 * time.Second)
	}))
	defer mockServer.Close()

	client := NewClient(
		WithSecretId("test-secret-id"),
		WithSecretKey("test-secret-key"),
		WithBaseUrl(mockServer.URL),
		WithHost("test-host"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	params := &ChatCompletionNewParams{
		Model:    "hunyuan-lite",
		Messages: []*ChatCompletionMessageParam{{Role: "user", Content: "Hello"}},
		Stream:   true,
	}

	err := client.ChatCompletionStream(ctx, params, func(chunk *ChatCompletionResponse) error {
		return nil
	})

	if err == nil {
		t.Fatal("Expected context cancellation error, got nil")
	}

	if !strings.Contains(err.Error(), "context") {
		t.Logf("Got error: %v", err)
	}
}
