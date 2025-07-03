package openai

import (
	"context"
	"os"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/embedder"
)

// TestEmbedderInterface verifies that our Embedder implements the interface.
func TestEmbedderInterface(t *testing.T) {
	var _ embedder.Embedder = (*Embedder)(nil)
}

// TestNewEmbedder tests the constructor with various options.
func TestNewEmbedder(t *testing.T) {
	tests := []struct {
		name     string
		opts     []Option
		expected *Embedder
	}{
		{
			name: "default options",
			opts: []Option{},
			expected: &Embedder{
				model:          DefaultModel,
				dimensions:     DefaultDimensions,
				encodingFormat: DefaultEncodingFormat,
			},
		},
		{
			name: "custom options",
			opts: []Option{
				WithModel(ModelTextEmbedding3Large),
				WithDimensions(3072),
				WithEncodingFormat(EncodingFormatFloat),
				WithUser("test-user"),
				WithAPIKey("test-key"),
			},
			expected: &Embedder{
				model:          ModelTextEmbedding3Large,
				dimensions:     3072,
				encodingFormat: EncodingFormatFloat,
				user:           "test-user",
				apiKey:         "test-key",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(tt.opts...)

			if e.model != tt.expected.model {
				t.Errorf("expected model %s, got %s", tt.expected.model, e.model)
			}
			if e.dimensions != tt.expected.dimensions {
				t.Errorf("expected dimensions %d, got %d", tt.expected.dimensions, e.dimensions)
			}
			if e.encodingFormat != tt.expected.encodingFormat {
				t.Errorf("expected encoding format %s, got %s", tt.expected.encodingFormat, e.encodingFormat)
			}
			if e.user != tt.expected.user {
				t.Errorf("expected user %s, got %s", tt.expected.user, e.user)
			}
			if e.apiKey != tt.expected.apiKey {
				t.Errorf("expected apiKey %s, got %s", tt.expected.apiKey, e.apiKey)
			}
		})
	}
}

// TestGetDimensions tests the GetDimensions method.
func TestGetDimensions(t *testing.T) {
	tests := []struct {
		name       string
		dimensions int
	}{
		{"default dimensions", DefaultDimensions},
		{"custom dimensions", 512},
		{"large dimensions", 3072},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(WithDimensions(tt.dimensions))
			if got := e.GetDimensions(); got != tt.dimensions {
				t.Errorf("GetDimensions() = %d, want %d", got, tt.dimensions)
			}
		})
	}
}

// TestIsTextEmbedding3Model tests the helper function.
func TestIsTextEmbedding3Model(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{ModelTextEmbedding3Small, true},
		{ModelTextEmbedding3Large, true},
		{ModelTextEmbeddingAda002, false},
		{"text-davinci-003", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := isTextEmbedding3Model(tt.model); got != tt.expected {
				t.Errorf("isTextEmbedding3Model(%s) = %v, want %v", tt.model, got, tt.expected)
			}
		})
	}
}

// TestGetEmbeddingValidation tests input validation.
func TestGetEmbeddingValidation(t *testing.T) {
	e := New()
	ctx := context.Background()

	// Test empty text.
	_, err := e.GetEmbedding(ctx, "")
	if err == nil {
		t.Error("expected error for empty text, got nil")
	}

	// Test empty text with usage.
	_, _, err = e.GetEmbeddingWithUsage(ctx, "")
	if err == nil {
		t.Error("expected error for empty text with usage, got nil")
	}
}

// TestGetEmbeddingIntegration is an integration test that requires an API key.
func TestGetEmbeddingIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping integration test")
	}

	e := New(
		WithAPIKey(apiKey),
		WithModel(ModelTextEmbedding3Small),
	)

	ctx := context.Background()
	text := "Hello, world! This is a test embedding."

	// Test GetEmbedding.
	embedding, err := e.GetEmbedding(ctx, text)
	if err != nil {
		t.Fatalf("GetEmbedding failed: %v", err)
	}

	if len(embedding) == 0 {
		t.Error("expected non-empty embedding")
	}

	if len(embedding) != DefaultDimensions {
		t.Errorf("expected embedding dimension %d, got %d", DefaultDimensions, len(embedding))
	}

	// Test GetEmbeddingWithUsage.
	embedding2, usage, err := e.GetEmbeddingWithUsage(ctx, text)
	if err != nil {
		t.Fatalf("GetEmbeddingWithUsage failed: %v", err)
	}

	if len(embedding2) == 0 {
		t.Error("expected non-empty embedding")
	}

	if len(embedding2) != len(embedding) {
		t.Error("expected same embedding dimensions from both methods")
	}

	// Usage should be present for OpenAI API.
	if usage == nil {
		t.Error("expected usage information")
	} else {
		if _, ok := usage["prompt_tokens"]; !ok {
			t.Error("expected prompt_tokens in usage")
		}
		if _, ok := usage["total_tokens"]; !ok {
			t.Error("expected total_tokens in usage")
		}
	}
}
