package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

func TestOpenAIStreamingModel_Name(t *testing.T) {
	m := NewOpenAIStreamingModel("gpt-4")
	if m.Name() != "gpt-4" {
		t.Errorf("Expected name to be gpt-4, got %s", m.Name())
	}
}

func TestOpenAIStreamingModel_Provider(t *testing.T) {
	m := NewOpenAIStreamingModel("gpt-4")
	if m.Provider() != "openai" {
		t.Errorf("Expected provider to be openai, got %s", m.Provider())
	}
}

func TestOpenAIStreamingModel_GenerateStreamWithoutAPIKey(t *testing.T) {
	m := NewOpenAIStreamingModel("gpt-4")
	_, err := m.GenerateStream(context.Background(), "Hello", DefaultOptions())
	if err == nil {
		t.Error("Expected error when API key is not set")
	}
}

func TestOpenAIStreamingModel_GenerateStreamWithMessagesWithoutAPIKey(t *testing.T) {
	m := NewOpenAIStreamingModel("gpt-4")
	_, err := m.GenerateStreamWithMessages(context.Background(), []*message.Message{
		message.NewUserMessage("Hello"),
	}, DefaultOptions())
	if err == nil {
		t.Error("Expected error when API key is not set")
	}
}

func TestOpenAIStreamingModel_GenerateStreamWithMockServer(t *testing.T) {
	// Mock SSE response chunks
	chunks := []string{
		`data: {"choices":[{"text":"Hello","finish_reason":""}]}`,
		`data: {"choices":[{"text":" world","finish_reason":""}]}`,
		`data: {"choices":[{"text":"!","finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}

	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Expected Authorization header to be 'Bearer test-key', got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Expected Accept header to be 'text/event-stream', got %s", r.Header.Get("Accept"))
		}

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		// Write chunks with a small delay
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("Expected ResponseWriter to be a Flusher")
			return
		}

		for _, chunk := range chunks {
			_, _ = w.Write([]byte(chunk + "\n\n"))
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()

	// Create a model with the mock server URL
	client := &http.Client{Timeout: 5 * time.Second}
	m := NewOpenAIStreamingModel("gpt-4",
		WithOpenAIAPIKey("test-key"),
		WithOpenAIBaseURL(server.URL),
		WithOpenAIClient(client),
	)

	// Generate a streaming response
	respCh, err := m.GenerateStream(context.Background(), "Hello", DefaultOptions())
	if err != nil {
		t.Fatalf("Unexpected error generating response: %v", err)
	}

	// Collect the chunks
	var responseText string
	var finishReason string
	for resp := range respCh {
		responseText += resp.Text
		if resp.FinishReason != "" {
			finishReason = resp.FinishReason
		}
	}

	// Verify the response
	expectedText := "Hello world!"
	if responseText != expectedText {
		t.Errorf("Expected response text to be '%s', got '%s'", expectedText, responseText)
	}
	if finishReason != "stop" {
		t.Errorf("Expected finish reason to be 'stop', got '%s'", finishReason)
	}
}

func TestOpenAIStreamingModel_GenerateStreamWithMessagesWithMockServer(t *testing.T) {
	// Mock SSE response chunks
	chunks := []string{
		`data: {"choices":[{"delta":{"role":"assistant","content":""},"finish_reason":""}]}`,
		`data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":""}]}`,
		`data: {"choices":[{"delta":{"content":" world"},"finish_reason":""}]}`,
		`data: {"choices":[{"delta":{"content":"!"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}

	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Expected Authorization header to be 'Bearer test-key', got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Expected Accept header to be 'text/event-stream', got %s", r.Header.Get("Accept"))
		}

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		// Write chunks with a small delay
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("Expected ResponseWriter to be a Flusher")
			return
		}

		for _, chunk := range chunks {
			_, _ = w.Write([]byte(chunk + "\n\n"))
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()

	// Create a model with the mock server URL
	client := &http.Client{Timeout: 5 * time.Second}
	m := NewOpenAIStreamingModel("gpt-4",
		WithOpenAIAPIKey("test-key"),
		WithOpenAIBaseURL(server.URL),
		WithOpenAIClient(client),
	)

	// Generate a streaming response
	respCh, err := m.GenerateStreamWithMessages(
		context.Background(),
		[]*message.Message{message.NewUserMessage("Hello")},
		DefaultOptions(),
	)
	if err != nil {
		t.Fatalf("Unexpected error generating response: %v", err)
	}

	// Collect the chunks
	var responseText strings.Builder
	var finishReason string
	for resp := range respCh {
		responseText.WriteString(resp.Text)
		if resp.FinishReason != "" {
			finishReason = resp.FinishReason
		}
	}

	// Verify the response
	expectedText := "Hello world!"
	if responseText.String() != expectedText {
		t.Errorf("Expected response text to be '%s', got '%s'", expectedText, responseText.String())
	}
	if finishReason != "stop" {
		t.Errorf("Expected finish reason to be 'stop', got '%s'", finishReason)
	}
}
