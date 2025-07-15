//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
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

func TestEmbedder_GetEmbedding(t *testing.T) {
	// Prepare fake OpenAI server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Respond only to embeddings endpoint.
		if !strings.HasSuffix(r.URL.Path, "/embeddings") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		rsp := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"object": "embedding", "index": 0, "embedding": []float64{0.1, 0.2, 0.3}},
			},
			"model": "text-embedding-3-small",
			"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		}
		_ = json.NewEncoder(w).Encode(rsp)
	}))
	defer srv.Close()

	emb := New(
		WithBaseURL(srv.URL),
		WithAPIKey("dummy"),
		WithModel(ModelTextEmbedding3Small),
		WithDimensions(3),
	)

	vec, err := emb.GetEmbedding(context.Background(), "hello")
	if err != nil {
		t.Fatalf("GetEmbedding err: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.1 {
		t.Fatalf("unexpected embedding vector: %v", vec)
	}

	// Test GetEmbeddingWithUsage.
	vec2, usage, err := emb.GetEmbeddingWithUsage(context.Background(), "hi")
	if err != nil || len(vec2) != 3 || usage == nil {
		t.Fatalf("GetEmbeddingWithUsage failed")
	}

	// Empty text should return error.
	if _, err := emb.GetEmbedding(context.Background(), ""); err == nil {
		t.Fatalf("expected error for empty text")
	}

	// Test alternate encoding format path.
	emb2 := New(
		WithBaseURL(srv.URL),
		WithAPIKey("dummy"),
		WithEncodingFormat(EncodingFormatBase64),
	)
	if _, err := emb2.GetEmbedding(context.Background(), "world"); err != nil {
		t.Fatalf("base64 embedding failed: %v", err)
	}

	emb3 := New(
		WithBaseURL(srv.URL),
		WithAPIKey("dummy"),
		WithModel(ModelTextEmbeddingAda002),
	)
	if _, err := emb3.GetEmbedding(context.Background(), "test"); err != nil {
		t.Fatalf("ada embedding failed: %v", err)
	}
}
