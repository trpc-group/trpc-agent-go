package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestOpenAIModel_Name(t *testing.T) {
	m := NewOpenAIModel("gpt-4")
	if m.Name() != "gpt-4" {
		t.Errorf("Expected name to be gpt-4, got %s", m.Name())
	}
}

func TestOpenAIModel_Provider(t *testing.T) {
	m := NewOpenAIModel("gpt-4")
	if m.Provider() != "openai" {
		t.Errorf("Expected provider to be openai, got %s", m.Provider())
	}
}

func TestOpenAIModel_GenerateWithoutAPIKey(t *testing.T) {
	m := NewOpenAIModel("gpt-4")
	_, err := m.Generate(context.Background(), "Hello", model.DefaultOptions())
	if err == nil {
		t.Error("Expected error when API key is not set")
	}
}

func TestOpenAIModel_GenerateWithMessagesWithoutAPIKey(t *testing.T) {
	m := NewOpenAIModel("gpt-4")
	_, err := m.GenerateWithMessages(context.Background(), []*message.Message{
		message.NewUserMessage("Hello"),
	}, model.DefaultOptions())
	if err == nil {
		t.Error("Expected error when API key is not set")
	}
}

func TestOpenAIModel_MergeOptions(t *testing.T) {
	m := NewOpenAIModel("gpt-4")
	defaultOpts := m.defaultOptions

	// Test with empty options (should get defaults)
	mergedOptions := m.mergeOptions(model.GenerationOptions{})
	if mergedOptions.Temperature != defaultOpts.Temperature {
		t.Errorf("Expected temperature to be %f, got %f", defaultOpts.Temperature, mergedOptions.Temperature)
	}

	// Test overriding values
	customOpts := model.GenerationOptions{
		Temperature: 0.5,
		MaxTokens:   100,
	}
	mergedOptions = m.mergeOptions(customOpts)
	if mergedOptions.Temperature != customOpts.Temperature {
		t.Errorf("Expected temperature to be %f, got %f", customOpts.Temperature, mergedOptions.Temperature)
	}
	if mergedOptions.MaxTokens != customOpts.MaxTokens {
		t.Errorf("Expected max tokens to be %d, got %d", customOpts.MaxTokens, mergedOptions.MaxTokens)
	}
}

func TestOpenAIModel_SetTools(t *testing.T) {
	m := NewOpenAIModel("gpt-4")
	tools := []model.ToolDefinition{
		{
			Name:        "test-tool",
			Description: "A test tool",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"foo": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
	}

	err := m.SetTools(tools)
	if err != nil {
		t.Errorf("Unexpected error setting tools: %v", err)
	}

	// Verify the tools were set
	if len(m.tools) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(m.tools))
	}
	if m.tools[0].Name != "test-tool" {
		t.Errorf("Expected tool name to be test-tool, got %s", m.tools[0].Name)
	}
}

func TestOpenAIModel_GenerateWithMockServer(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Expected Authorization header to be 'Bearer test-key', got %s", r.Header.Get("Authorization"))
		}

		// Send a mock response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [
				{
					"text": "This is a test response.",
					"finish_reason": "stop"
				}
			],
			"usage": {
				"prompt_tokens": 5,
				"completion_tokens": 5,
				"total_tokens": 10
			}
		}`))
	}))
	defer server.Close()

	// Create a model with the mock server URL
	client := &http.Client{Timeout: 5 * time.Second}
	m := NewOpenAIModel("gpt-4",
		WithOpenAIAPIKey("test-key"),
		WithOpenAIBaseURL(server.URL),
		WithOpenAIClient(client),
	)

	// Generate a response
	resp, err := m.Generate(context.Background(), "Hello", model.DefaultOptions())
	if err != nil {
		t.Fatalf("Unexpected error generating response: %v", err)
	}

	// Verify the response
	if resp.Text != "This is a test response." {
		t.Errorf("Expected response text to be 'This is a test response.', got %s", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("Expected finish reason to be 'stop', got %s", resp.FinishReason)
	}
	if resp.Usage.PromptTokens != 5 {
		t.Errorf("Expected prompt tokens to be 5, got %d", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 5 {
		t.Errorf("Expected completion tokens to be 5, got %d", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 10 {
		t.Errorf("Expected total tokens to be 10, got %d", resp.Usage.TotalTokens)
	}
}

func TestOpenAIModel_GenerateWithMessagesWithMockServer(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Expected Authorization header to be 'Bearer test-key', got %s", r.Header.Get("Authorization"))
		}

		// Send a mock response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [
				{
					"message": {
						"role": "assistant",
						"content": "This is a test response."
					},
					"finish_reason": "stop"
				}
			],
			"usage": {
				"prompt_tokens": 5,
				"completion_tokens": 5,
				"total_tokens": 10
			}
		}`))
	}))
	defer server.Close()

	// Create a model with the mock server URL
	client := &http.Client{Timeout: 5 * time.Second}
	m := NewOpenAIModel("gpt-4",
		WithOpenAIAPIKey("test-key"),
		WithOpenAIBaseURL(server.URL),
		WithOpenAIClient(client),
	)

	// Generate a response
	resp, err := m.GenerateWithMessages(
		context.Background(),
		[]*message.Message{message.NewUserMessage("Hello")},
		model.DefaultOptions(),
	)
	if err != nil {
		t.Fatalf("Unexpected error generating response: %v", err)
	}

	// Verify the response
	if resp.Text != "This is a test response." {
		t.Errorf("Expected response text to be 'This is a test response.', got %s", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("Expected finish reason to be 'stop', got %s", resp.FinishReason)
	}
	if len(resp.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(resp.Messages))
	}
	if resp.Messages[0].Role != message.RoleAssistant {
		t.Errorf("Expected message role to be assistant, got %s", resp.Messages[0].Role)
	}
	if resp.Messages[0].Content != "This is a test response." {
		t.Errorf("Expected message content to be 'This is a test response.', got %s", resp.Messages[0].Content)
	}
}
