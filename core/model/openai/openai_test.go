package openai

import (
	"context"
	"os"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

func TestMain(m *testing.M) {
	// Setup.
	os.Exit(m.Run())
}

func TestNew(t *testing.T) {
	tests := []struct {
		name        string
		modelName   string
		opts        Options
		expectError bool
	}{
		{
			name:      "valid openai model",
			modelName: "gpt-3.5-turbo",
			opts: Options{
				APIKey: "test-key",
			},
			expectError: false,
		},
		{
			name:      "valid model with base url",
			modelName: "custom-model",
			opts: Options{
				APIKey:  "test-key",
				BaseURL: "https://api.custom.com",
			},
			expectError: false,
		},
		{
			name:      "empty api key",
			modelName: "gpt-3.5-turbo",
			opts: Options{
				APIKey: "",
			},
			expectError: false, // Should still create model, but may fail on actual calls
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(tt.modelName, tt.opts)
			if m == nil {
				t.Fatal("expected model to be created, got nil")
			}

			if m.name != tt.modelName {
				t.Errorf("expected model name %s, got %s", tt.modelName, m.name)
			}

			if m.apiKey != tt.opts.APIKey {
				t.Errorf("expected api key %s, got %s", tt.opts.APIKey, m.apiKey)
			}

			if m.baseURL != tt.opts.BaseURL {
				t.Errorf("expected base url %s, got %s", tt.opts.BaseURL, m.baseURL)
			}
		})
	}
}

func TestModel_GenerateContent_NilRequest(t *testing.T) {
	m := New("test-model", Options{APIKey: "test-key"})

	ctx := context.Background()
	_, err := m.GenerateContent(ctx, nil)

	if err == nil {
		t.Fatal("expected error for nil request, got nil")
	}

	if err.Error() != "request cannot be nil" {
		t.Errorf("expected 'request cannot be nil', got %s", err.Error())
	}
}

func TestModel_GenerateContent_ValidRequest(t *testing.T) {
	// Skip this test if no API key is provided.
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping integration test")
	}

	m := New("gpt-3.5-turbo", Options{APIKey: apiKey})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	temperature := 0.7
	maxTokens := 50

	request := &model.Request{
		Model: "gpt-3.5-turbo",
		Messages: []model.Message{
			model.NewSystemMessage("You are a helpful assistant."),
			model.NewUserMessage("Say hello in exactly 3 words."),
		},
		Temperature: &temperature,
		MaxTokens:   &maxTokens,
		Stream:      false,
	}

	responseChan, err := m.GenerateContent(ctx, request)
	if err != nil {
		t.Fatalf("failed to generate content: %v", err)
	}

	var responses []*model.Response
	for response := range responseChan {
		responses = append(responses, response)
		if response.Done {
			break
		}
	}

	if len(responses) == 0 {
		t.Fatal("expected at least one response, got none")
	}
}

func TestModel_GenerateContent_CustomBaseURL(t *testing.T) {
	// This test creates a model with custom base URL but doesn't make actual calls.
	// It's mainly to test the configuration.

	customBaseURL := "https://api.custom-openai.com"
	m := New("custom-model", Options{
		APIKey:  "test-key",
		BaseURL: customBaseURL,
	})

	if m.baseURL != customBaseURL {
		t.Errorf("expected base URL %s, got %s", customBaseURL, m.baseURL)
	}

	// Test that the model can be created without errors.
	ctx := context.Background()
	request := &model.Request{
		Model: "custom-model",
		Messages: []model.Message{
			model.NewUserMessage("test"),
		},
		Stream: false,
	}

	// This will likely fail due to invalid API key/URL, but should not panic.
	responseChan, err := m.GenerateContent(ctx, request)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Just consume one response to test the channel setup.
	select {
	case response := <-responseChan:
		if response != nil && response.Error == nil {
			t.Log("Unexpected success with test credentials")
		}
	case <-time.After(5 * time.Second):
		t.Log("Request timed out as expected with test credentials")
	}
}

func TestOptions_Validation(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{
			name: "empty options",
			opts: Options{},
		},
		{
			name: "only api key",
			opts: Options{
				APIKey: "test-key",
			},
		},
		{
			name: "only base url",
			opts: Options{
				BaseURL: "https://api.example.com",
			},
		},
		{
			name: "both api key and base url",
			opts: Options{
				APIKey:  "test-key",
				BaseURL: "https://api.example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New("test-model", tt.opts)
			if m == nil {
				t.Fatal("expected model to be created")
			}

			if m.apiKey != tt.opts.APIKey {
				t.Errorf("expected api key %s, got %s", tt.opts.APIKey, m.apiKey)
			}

			if m.baseURL != tt.opts.BaseURL {
				t.Errorf("expected base url %s, got %s", tt.opts.BaseURL, m.baseURL)
			}
		})
	}
}
