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

package gemini

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"google.golang.org/genai"
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
			opts: []Option{WithAPIKey("test-key")},
			expected: &Embedder{
				model:      DefaultModel,
				dimensions: DefaultDimensions,
				taskType:   DefaultTaskType,
				role:       DefaultRole,
				apiKey:     "test-key",
			},
		},
		{
			name: "custom options",
			opts: []Option{
				WithModel(ModelGeminiEmbeddingExp0307),
				WithDimensions(3072),
				WithTaskType(TaskTypeSemanticSimilarity),
				WithRole(genai.RoleUser),
				WithAPIKey("test-key"),
			},
			expected: &Embedder{
				model:      ModelGeminiEmbeddingExp0307,
				dimensions: 3072,
				taskType:   TaskTypeSemanticSimilarity,
				role:       genai.RoleUser,
				apiKey:     "test-key",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			e, err := New(ctx, tt.opts...)
			if err != nil {
				t.Fatalf("New failed: %v", err)
			}
			if e.model != tt.expected.model {
				t.Errorf("expected model %s, got %s", tt.expected.model, e.model)
			}
			if e.dimensions != tt.expected.dimensions {
				t.Errorf("expected dimensions %d, got %d", tt.expected.dimensions, e.dimensions)
			}
			if e.taskType != tt.expected.taskType {
				t.Errorf("expected taskType %s, got %s", tt.expected.taskType, e.taskType)
			}
			if e.role != tt.expected.role {
				t.Errorf("expected role %s, got %s", tt.expected.role, e.role)
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
			e, err := New(
				context.Background(),
				WithAPIKey("test-key"),
				WithDimensions(tt.dimensions),
			)
			if err != nil {
				t.Fatalf("New failed: %v", err)
			}
			if got := e.GetDimensions(); got != tt.dimensions {
				t.Errorf("GetDimensions() = %d, want %d", got, tt.dimensions)
			}
		})
	}
}

// TestGetEmbeddingValidation tests input validation.
func TestGetEmbeddingValidation(t *testing.T) {
	ctx := context.Background()
	e, err := New(ctx, WithAPIKey("test-key"))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Test empty text.
	_, err = e.GetEmbedding(ctx, "")
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
	// Prepare fake Gemini server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Respond only to embeddings endpoint.
		if !strings.HasPrefix(r.URL.Path, "/embeddings") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		rsp := map[string]any{
			"embeddings": []map[string]any{
				{
					"values": []float64{0.1, 0.2, 0.3},
				},
			},
			"metadata": map[string]any{"billable_character_count": 10},
		}
		_ = json.NewEncoder(w).Encode(rsp)
	}))
	defer srv.Close()

	emb, err := New(
		context.Background(),
		WithAPIKey("dummy"),
		WithModel(ModelGeminiEmbeddingExp0307),
		WithDimensions(3),
		WithClientOptions(&genai.ClientConfig{
			HTTPOptions: genai.HTTPOptions{
				BaseURL: srv.URL + "/embeddings",
			},
		}),
	)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	vec, err := emb.GetEmbedding(context.Background(), "hello")
	if err != nil {
		t.Fatalf("GetEmbedding err: %v", err)
	}
	if len(vec) != 3 || math.Abs(vec[0]-0.1) > 1e-3 {
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
}

func TestAPIKeyPriority(t *testing.T) {
	ctx := context.Background()
	t.Run("WithClientOptions", func(t *testing.T) {
		os.Setenv(GoogleAPIKeyEnv, "key1")
		defer os.Unsetenv(GoogleAPIKeyEnv)

		e, err := New(
			ctx,
			WithAPIKey("key2"),
			WithClientOptions(&genai.ClientConfig{APIKey: "key3"}),
		)
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}
		if e.clientOptions.APIKey != "key3" {
			t.Errorf("expected apiKey %s, got %s", "key3", e.clientOptions.APIKey)
		}
	})
	t.Run("WithAPIKey", func(t *testing.T) {
		os.Setenv(GoogleAPIKeyEnv, "key1")
		defer os.Unsetenv(GoogleAPIKeyEnv)

		e, err := New(
			ctx,
			WithAPIKey("key2"),
		)
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}
		if e.apiKey != "key2" {
			t.Errorf("expected apiKey %s, got %s", "key2", e.apiKey)
		}
	})
	t.Run("Env", func(t *testing.T) {
		os.Setenv(GoogleAPIKeyEnv, "key1")
		defer os.Unsetenv(GoogleAPIKeyEnv)

		e, err := New(ctx)
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}
		if e.apiKey != "key1" {
			t.Errorf("expected apiKey %s, got %s", "key1", e.apiKey)
		}
	})
}
