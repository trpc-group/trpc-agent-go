//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ollama/ollama/api"

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
		fn       func()
		teardown func()
		expected *Embedder
	}{
		{
			name: "default options",
			opts: []Option{},
			expected: &Embedder{
				model:      DefaultModel,
				host:       "http://localhost:11434",
				dimensions: DefaultDimensions,
			},
		},
		{
			name: "custom options",
			opts: []Option{
				WithModel("llama3.2:latest"),
				WithDimensions(3072),
				WithHost("http://localhost:11434"),
				WithTruncate(true),
				WithUseEmbeddings(),
				WithOptions(map[string]any{"temperature": 0.7}),
				WithKeepAlive(30 * time.Second),
			},
			expected: &Embedder{
				model:         "llama3.2:latest",
				host:          "http://localhost:11434",
				dimensions:    3072,
				truncate:      &[]bool{true}[0],
				useEmbeddings: true,
				options:       map[string]any{"temperature": 0.7},
				keepAlive:     30 * time.Second,
			},
		},
		{
			name: "set host from env",
			fn: func() {
				os.Setenv(OllamaHost, "http://ollama.com")
			},
			teardown: func() {
				os.Unsetenv(OllamaHost)
			},
			expected: &Embedder{
				model:      DefaultModel,
				host:       "http://ollama.com:80",
				dimensions: DefaultDimensions,
			},
		},
		{
			name: "set host env but override with option",
			fn: func() {
				os.Setenv(OllamaHost, "http://ollama.com")
			},
			teardown: func() {
				os.Unsetenv(OllamaHost)
			},
			opts: []Option{
				WithHost("https://localhost:443"),
			},
			expected: &Embedder{
				model:      DefaultModel,
				host:       "https://localhost:443",
				dimensions: DefaultDimensions,
			},
		},
		{
			name: "invalid port",
			opts: []Option{
				WithHost("http://localhost:port"),
			},
			expected: &Embedder{
				model:      DefaultModel,
				host:       "http://localhost:80",
				dimensions: DefaultDimensions,
			},
		},
		{
			name: "invalid host",
			opts: []Option{
				WithHost("invalid"),
			},
			expected: &Embedder{
				model:      DefaultModel,
				host:       "http://invalid:11434",
				dimensions: DefaultDimensions,
			},
		},
		{
			name: "ollama host",
			fn: func() {
				os.Setenv(OllamaHost, "ollama.com")
			},
			teardown: func() {
				os.Unsetenv(OllamaHost)
			},
			expected: &Embedder{
				model:      DefaultModel,
				host:       "https://ollama.com:443",
				dimensions: DefaultDimensions,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.fn != nil {
				tt.fn()
			}
			if tt.teardown != nil {
				defer tt.teardown()
			}
			e := New(tt.opts...)
			if e.model != tt.expected.model {
				t.Errorf("expected model %s, got %s", tt.expected.model, e.model)
			}
			if e.host != tt.expected.host {
				t.Errorf("expected host %s, got %s", tt.expected.host, e.host)
			}
			if e.dimensions != tt.expected.dimensions {
				t.Errorf("expected dimensions %d, got %d", tt.expected.dimensions, e.dimensions)
			}
			if tt.expected.truncate != nil {
				if e.truncate == nil || *e.truncate != *tt.expected.truncate {
					t.Errorf("expected truncate %v, got %v", *tt.expected.truncate, *e.truncate)
				}
			}
			if tt.expected.truncate == nil {
				if e.truncate != nil {
					t.Errorf("expected truncate to be nil, got %v", *e.truncate)
				}
			}
			if e.useEmbeddings != tt.expected.useEmbeddings {
				t.Errorf("expected useEmbeddings %v, got %v", tt.expected.useEmbeddings, e.useEmbeddings)
			}
			if !reflect.DeepEqual(e.options, tt.expected.options) {
				t.Errorf("expected options %v, got %v", tt.expected.options, e.options)
			}
			if e.keepAlive != tt.expected.keepAlive {
				t.Errorf("expected keepAlive %v, got %v", tt.expected.keepAlive, e.keepAlive)
			}
		})
	}
}

// Test_withHttpClient tests the withHttpClient option.
func Test_withHttpClient(t *testing.T) {
	client := &http.Client{}
	e := New(withHttpClient(client))
	if e.httpClient != client {
		t.Errorf("expected httpClient %p, got %p", client, e.httpClient)
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
	// Prepare fake Ollama server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Respond only to embeddings endpoint.
		if !strings.HasPrefix(r.URL.Path, "/api/embed") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var rsp map[string]any
		if r.URL.Path == "/api/embed" {
			var req api.EmbedRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			embeddings := [][]float32{{-0.015051961, 0.006847816, -0.025103297, -0.011071755}}
			if req.Input == "invalid" {
				http.Error(w, "invalid input", http.StatusBadRequest)
				return
			}
			rsp = map[string]any{
				"model":             "llama3.2:latest",
				"embeddings":        embeddings,
				"total_duration":    2037755833,
				"prompt_eval_count": 6,
				"load_duration":     1747666042,
			}
		}
		if r.URL.Path == "/api/embeddings" {
			var req api.EmbeddingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			embedding := []float64{-1.3288315534591675, 0.6045454144477844, -2.216193199157715, -0.9774471521377563}
			if req.Prompt == "invalid" {
				embedding = []float64{}
			}
			rsp = map[string]any{
				"embedding": embedding,
			}
		}
		_ = json.NewEncoder(w).Encode(rsp)
	}))
	defer srv.Close()

	e := New(WithHost(srv.URL), WithKeepAlive(10*time.Second), WithTruncate(true))
	ctx := context.Background()

	vec, err := e.GetEmbedding(ctx, "hello")
	if err != nil {
		t.Fatalf("GetEmbedding err: %v", err)
	}
	if len(vec) != 4 || vec[0] != -0.015051960945129395 {
		t.Fatalf("unexpected embedding vector: %v", vec)
	}
	_, err = e.GetEmbedding(ctx, "invalid")
	if err == nil {
		t.Fatalf("GetEmbedding should return an error for invalid input")
	}

	vec2, usage, err := e.GetEmbeddingWithUsage(ctx, "hi")
	if err != nil || len(vec2) != 4 || usage == nil {
		t.Fatalf("GetEmbeddingWithUsage failed")
	}
	if usage["prompt_tokens"] != 6 {
		t.Fatalf("unexpected usage: %v", usage)
	}
	if usage["total_duration"] != time.Duration(2037755833) {
		t.Fatalf("unexpected usage: %v", usage)
	}
	if usage["load_duration"] != time.Duration(1747666042) {
		t.Fatalf("unexpected usage: %v", usage)
	}

	eUsembeddings := New(WithHost(srv.URL), WithUseEmbeddings(), WithKeepAlive(10*time.Second))
	vec, err = eUsembeddings.GetEmbedding(ctx, "hello")
	if err != nil {
		t.Fatalf("GetEmbedding err: %v", err)
	}
	if len(vec) != 4 || vec[0] != -1.3288315534591675 {
		t.Fatalf("unexpected embedding vector: %v", vec)
	}
	vec, err = eUsembeddings.GetEmbedding(ctx, "invalid")
	if err != nil {
		t.Fatalf("GetEmbedding err: %v", err)
	}
	if len(vec) != 0 {
		t.Fatalf("unexpected embedding vector: %v", vec)
	}
	vec2, usage, err = eUsembeddings.GetEmbeddingWithUsage(ctx, "invalid")
	if err != nil || len(vec2) != 0 || usage != nil {
		t.Fatalf("GetEmbeddingWithUsage failed")
	}
}
