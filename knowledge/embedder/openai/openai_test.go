//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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
	"time"

	"github.com/openai/openai-go/option"
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
		{
			name: "with organization and base URL",
			opts: []Option{
				WithOrganization("test-org"),
				WithBaseURL("https://api.example.com"),
			},
			expected: &Embedder{
				model:          DefaultModel,
				dimensions:     DefaultDimensions,
				encodingFormat: DefaultEncodingFormat,
				organization:   "test-org",
				baseURL:        "https://api.example.com",
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
			if e.organization != tt.expected.organization {
				t.Errorf("expected organization %s, got %s", tt.expected.organization, e.organization)
			}
			if e.baseURL != tt.expected.baseURL {
				t.Errorf("expected baseURL %s, got %s", tt.expected.baseURL, e.baseURL)
			}
		})
	}
}

// TestWithRequestOptions tests the WithRequestOptions option function.
func TestWithRequestOptions(t *testing.T) {
	// Test that WithRequestOptions can be called and doesn't panic.
	// We don't need to test the actual OpenAI options behavior here,
	// just ensure the option function works.
	e := New(WithRequestOptions())
	if e == nil {
		t.Fatal("expected non-nil embedder")
	}

	// Verify other fields still have default values.
	if e.model != DefaultModel {
		t.Errorf("expected default model %s, got %s", DefaultModel, e.model)
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

// TestGetEmbedding_EmptyResponse tests handling of empty embedding responses
func TestGetEmbedding_EmptyResponse(t *testing.T) {
	t.Run("empty data array", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			rsp := map[string]any{
				"object": "list",
				"data":   []map[string]any{},
				"model":  "text-embedding-3-small",
			}
			_ = json.NewEncoder(w).Encode(rsp)
		}))
		defer srv.Close()

		emb := New(
			WithBaseURL(srv.URL),
			WithAPIKey("dummy"),
		)

		vec, err := emb.GetEmbedding(context.Background(), "test")
		if err != nil {
			t.Fatalf("GetEmbedding should not return error for empty data: %v", err)
		}
		if len(vec) != 0 {
			t.Errorf("Expected empty embedding, got length %d", len(vec))
		}
	})

	t.Run("empty embedding vector", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			rsp := map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float64{}},
				},
				"model": "text-embedding-3-small",
			}
			_ = json.NewEncoder(w).Encode(rsp)
		}))
		defer srv.Close()

		emb := New(
			WithBaseURL(srv.URL),
			WithAPIKey("dummy"),
		)

		vec, err := emb.GetEmbedding(context.Background(), "test")
		if err != nil {
			t.Fatalf("GetEmbedding should not return error for empty vector: %v", err)
		}
		if len(vec) != 0 {
			t.Errorf("Expected empty embedding, got length %d", len(vec))
		}
	})
}

// TestGetEmbeddingWithUsage_EmptyResponse tests handling of empty responses with usage
func TestGetEmbeddingWithUsage_EmptyResponse(t *testing.T) {
	t.Run("empty data array with usage", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			rsp := map[string]any{
				"object": "list",
				"data":   []map[string]any{},
				"model":  "text-embedding-3-small",
				"usage":  map[string]any{"prompt_tokens": 1, "total_tokens": 1},
			}
			_ = json.NewEncoder(w).Encode(rsp)
		}))
		defer srv.Close()

		emb := New(
			WithBaseURL(srv.URL),
			WithAPIKey("dummy"),
		)

		vec, usage, err := emb.GetEmbeddingWithUsage(context.Background(), "test")
		if err != nil {
			t.Fatalf("GetEmbeddingWithUsage should not return error: %v", err)
		}
		if len(vec) != 0 {
			t.Errorf("Expected empty embedding, got length %d", len(vec))
		}
		// Usage may be nil when data is empty, which is acceptable
		_ = usage
	})

	t.Run("empty embedding vector with usage", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			rsp := map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float64{}},
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
		)

		vec, _, err := emb.GetEmbeddingWithUsage(context.Background(), "test")
		if err != nil {
			t.Fatalf("GetEmbeddingWithUsage should not return error: %v", err)
		}
		if len(vec) != 0 {
			t.Errorf("Expected empty embedding, got length %d", len(vec))
		}
	})
}

// TestGetEmbedding_EmptyDataArray tests the log.WarnContext path for empty data array
func TestGetEmbedding_EmptyDataArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rsp := map[string]any{
			"object": "list",
			"data":   []map[string]any{},
			"model":  "text-embedding-3-small",
		}
		_ = json.NewEncoder(w).Encode(rsp)
	}))
	defer srv.Close()

	emb := New(
		WithBaseURL(srv.URL),
		WithAPIKey("dummy"),
	)

	vec, err := emb.GetEmbedding(context.Background(), "test")
	if err != nil {
		t.Fatalf("GetEmbedding should not return error: %v", err)
	}
	if len(vec) != 0 {
		t.Errorf("Expected empty embedding, got length %d", len(vec))
	}
}

// TestRetryLogic tests the retry logic with rate limit errors.
func TestRetryLogic(t *testing.T) {
	t.Run("retry on rate limit error", func(t *testing.T) {
		attemptCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attemptCount++
			if attemptCount <= 2 {
				// Return rate limit error for first 2 attempts
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"message": "Rate limit exceeded",
						"type":    "rate_limit_error",
						"code":    "429",
					},
				})
				return
			}
			// Success on 3rd attempt
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
			WithMaxRetries(3),
			WithRetryBackoff([]time.Duration{10 * time.Millisecond, 20 * time.Millisecond}),
			// Disable SDK internal retry to test our retry logic
			WithRequestOptions(option.WithMaxRetries(0)),
		)

		vec, err := emb.GetEmbedding(context.Background(), "test")
		if err != nil {
			t.Fatalf("GetEmbedding should succeed after retries: %v", err)
		}
		if len(vec) != 3 {
			t.Errorf("Expected 3 dimensions, got %d", len(vec))
		}
		if attemptCount != 3 {
			t.Errorf("Expected 3 attempts, got %d", attemptCount)
		}
	})

	t.Run("max retries exceeded", func(t *testing.T) {
		attemptCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attemptCount++
			// Always return rate limit error
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "Rate limit exceeded",
					"type":    "rate_limit_error",
					"code":    "429",
				},
			})
		}))
		defer srv.Close()

		emb := New(
			WithBaseURL(srv.URL),
			WithAPIKey("dummy"),
			WithMaxRetries(2),
			WithRetryBackoff([]time.Duration{5 * time.Millisecond}),
			// Disable SDK internal retry to test our retry logic
			WithRequestOptions(option.WithMaxRetries(0)),
		)

		_, err := emb.GetEmbedding(context.Background(), "test")
		if err == nil {
			t.Fatal("Expected error after max retries exceeded")
		}
		// Initial attempt + 2 retries = 3 total attempts
		if attemptCount != 3 {
			t.Errorf("Expected 3 attempts (1 initial + 2 retries), got %d", attemptCount)
		}
	})

	t.Run("retry on any error", func(t *testing.T) {
		attemptCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attemptCount++
			// Return bad request error (400)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "Invalid request",
					"type":    "invalid_request_error",
				},
			})
		}))
		defer srv.Close()

		emb := New(
			WithBaseURL(srv.URL),
			WithAPIKey("dummy"),
			WithMaxRetries(3),
			WithRetryBackoff([]time.Duration{5 * time.Millisecond}),
			// Disable SDK internal retry to test our retry logic
			WithRequestOptions(option.WithMaxRetries(0)),
		)

		_, err := emb.GetEmbedding(context.Background(), "test")
		if err == nil {
			t.Fatal("Expected error for bad request")
		}
		// Should retry on any error: Initial attempt + 3 retries = 4 total attempts
		if attemptCount != 4 {
			t.Errorf("Expected 4 attempts (1 initial + 3 retries), got %d", attemptCount)
		}
	})

	t.Run("no retry when maxRetries is 0", func(t *testing.T) {
		attemptCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attemptCount++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "Rate limit exceeded",
					"type":    "rate_limit_error",
				},
			})
		}))
		defer srv.Close()

		emb := New(
			WithBaseURL(srv.URL),
			WithAPIKey("dummy"),
			WithMaxRetries(0), // Explicitly disable retries
			// Disable SDK internal retry to test our retry logic
			WithRequestOptions(option.WithMaxRetries(0)),
		)

		_, err := emb.GetEmbedding(context.Background(), "test")
		if err == nil {
			t.Fatal("Expected error when retries disabled")
		}
		if attemptCount != 1 {
			t.Errorf("Expected 1 attempt (no retries), got %d", attemptCount)
		}
	})

	t.Run("negative maxRetries treated as 0", func(t *testing.T) {
		attemptCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attemptCount++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "Rate limit exceeded",
					"type":    "rate_limit_error",
				},
			})
		}))
		defer srv.Close()

		emb := New(
			WithBaseURL(srv.URL),
			WithAPIKey("dummy"),
			WithMaxRetries(-5), // Negative value should be treated as 0
			WithRequestOptions(option.WithMaxRetries(0)),
		)

		_, err := emb.GetEmbedding(context.Background(), "test")
		if err == nil {
			t.Fatal("Expected error when retries disabled")
		}
		if attemptCount != 1 {
			t.Errorf("Expected 1 attempt (negative maxRetries treated as 0), got %d", attemptCount)
		}
	})
}

// TestGetBackoffDuration tests the getBackoffDuration method.
func TestGetBackoffDuration(t *testing.T) {
	t.Run("default backoff", func(t *testing.T) {
		emb := New()
		expected := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
		for i, want := range expected {
			if got := emb.getBackoffDuration(i); got != want {
				t.Errorf("Expected %v for attempt %d, got %v", want, i, got)
			}
		}
		// Attempt beyond default slice length should return last element
		if got := emb.getBackoffDuration(10); got != 800*time.Millisecond {
			t.Errorf("Expected 800ms for attempt beyond slice, got %v", got)
		}
	})

	t.Run("empty backoff slice", func(t *testing.T) {
		emb := New(WithRetryBackoff(nil))
		if got := emb.getBackoffDuration(0); got != 0 {
			t.Errorf("Expected 0 for empty backoff, got %v", got)
		}
	})

	t.Run("within backoff slice", func(t *testing.T) {
		emb := New(WithRetryBackoff([]time.Duration{
			100 * time.Millisecond,
			200 * time.Millisecond,
			300 * time.Millisecond,
		}))

		if got := emb.getBackoffDuration(0); got != 100*time.Millisecond {
			t.Errorf("Expected 100ms for attempt 0, got %v", got)
		}
		if got := emb.getBackoffDuration(1); got != 200*time.Millisecond {
			t.Errorf("Expected 200ms for attempt 1, got %v", got)
		}
		if got := emb.getBackoffDuration(2); got != 300*time.Millisecond {
			t.Errorf("Expected 300ms for attempt 2, got %v", got)
		}
	})

	t.Run("exceeds backoff slice length", func(t *testing.T) {
		emb := New(WithRetryBackoff([]time.Duration{
			100 * time.Millisecond,
			200 * time.Millisecond,
		}))

		// Attempt index 5 exceeds slice length, should return last element
		if got := emb.getBackoffDuration(5); got != 200*time.Millisecond {
			t.Errorf("Expected 200ms for attempt beyond slice, got %v", got)
		}
	})
}

// TestRetryWithContextCancellation tests retry behavior when context is cancelled.
func TestRetryWithContextCancellation(t *testing.T) {
	attemptCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Rate limit exceeded",
			},
		})
	}))
	defer srv.Close()

	emb := New(
		WithBaseURL(srv.URL),
		WithAPIKey("dummy"),
		WithMaxRetries(5),
		WithRetryBackoff([]time.Duration{100 * time.Millisecond}),
		// Disable SDK internal retry to test our retry logic
		WithRequestOptions(option.WithMaxRetries(0)),
	)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel context shortly after first request
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := emb.GetEmbedding(ctx, "test")
	if err == nil {
		t.Fatal("Expected error when context is cancelled")
	}
	// Should have made at least 1 attempt but not all 6 (1 + 5 retries)
	if attemptCount == 0 {
		t.Error("Expected at least 1 attempt")
	}
}
