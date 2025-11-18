//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package huggingface

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
)

// TestEmbedderInterface verifies that our Embedder implements the interface.
func TestEmbedderInterface(t *testing.T) {
	var _ embedder.Embedder = (*Embedder)(nil)
}

// TestNew tests the constructor with various options.
func TestNew(t *testing.T) {
	const (
		testURL    = "http://test.com"
		testDims   = 2048
		testEmbed  = EmbedAll
		testPrompt = "testPrompt"
	)
	testClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	type args struct {
		opts []Option
	}
	tests := []struct {
		name string
		args args
		want *Embedder
	}{
		{
			name: "default initialization",
			args: args{opts: nil},
			want: &Embedder{
				baseURL:             DefaultBaseURL,
				dimensions:          DefaultDimensions,
				truncationDirection: TruncateRight,
				embedRoute:          EmbedDefault,
				client:              http.DefaultClient,
			},
		},
		{
			name: "single option - base URL",
			args: args{opts: []Option{WithBaseURL(testURL)}},
			want: &Embedder{
				baseURL:             testURL,
				dimensions:          DefaultDimensions,
				truncationDirection: TruncateRight,
				embedRoute:          EmbedDefault,
				client:              http.DefaultClient,
			},
		},
		{
			name: "multiple options combination",
			args: args{opts: []Option{
				WithBaseURL(testURL),
				WithDimensions(testDims),
				WithNormalize(true),
				WithPromptName(testPrompt),
				WithTruncate(true),
				WithTruncationDirection(TruncateLeft),
				WithEmbedRoute(testEmbed),
				WithClient(testClient),
			}},
			want: &Embedder{
				baseURL:             testURL,
				dimensions:          testDims,
				normalize:           true,
				promptName:          testPrompt,
				truncate:            true,
				truncationDirection: TruncateLeft,
				embedRoute:          testEmbed,
				client:              testClient,
			},
		},
		{
			name: "override options order",
			args: args{opts: []Option{
				WithTruncationDirection(TruncateRight),
				WithTruncationDirection(TruncateLeft),
			}},
			want: &Embedder{
				baseURL:             DefaultBaseURL,
				dimensions:          DefaultDimensions,
				truncationDirection: TruncateLeft,
				embedRoute:          EmbedDefault,
				client:              http.DefaultClient,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(tt.args.opts...)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("New() = %+v\nwant %+v", got, tt.want)

				valGot := reflect.ValueOf(got).Elem()
				valWant := reflect.ValueOf(tt.want).Elem()
				typeGot := valGot.Type()

				for i := 0; i < valGot.NumField(); i++ {
					fieldName := typeGot.Field(i).Name
					gotField := valGot.Field(i).Interface()
					wantField := valWant.Field(i).Interface()

					if !reflect.DeepEqual(gotField, wantField) {
						t.Errorf("Field %s mismatch: got %v, want %v",
							fieldName, gotField, wantField)
					}
				}
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
	// Prepare fake server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Respond only to embeddings endpoint.
		if strings.HasSuffix(r.URL.Path, string(EmbedDefault)) {
			w.Header().Set("Content-Type", "application/json")
			rsp := [][]float64{{0.1, 0.2, 0.3}}
			_ = json.NewEncoder(w).Encode(rsp)
		} else if strings.HasSuffix(r.URL.Path, string(EmbedAll)) {
			w.Header().Set("Content-Type", "application/json")
			rsp := [][][]float64{{{0.1, 0.2, 0.3}}}
			_ = json.NewEncoder(w).Encode(rsp)
		}
	}))
	defer srv.Close()

	emb := New(
		WithBaseURL(srv.URL),
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

	emb2 := New(
		WithBaseURL(srv.URL),
		WithEmbedRoute(EmbedAll),
	)
	if _, err := emb2.GetEmbedding(context.Background(), "test"); err != nil {
		t.Fatalf("ada embedding failed: %v", err)
	}

	// Test alternate encoding format path.
	emb3 := New(
		WithBaseURL(srv.URL),
		WithEmbedRoute("/error_path"),
	)
	if _, err := emb3.GetEmbedding(context.Background(), "world"); err == nil {
		t.Fatal("error path not failed")
	}

}

func TestGetEmbeddingResponseValidation(t *testing.T) {
	// Prepare fake server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Respond only to embeddings endpoint.
		if strings.HasSuffix(r.URL.Path, string(EmbedDefault)) {
			w.Header().Set("Content-Type", "application/json")
			var rsp [][]float64
			_ = json.NewEncoder(w).Encode(rsp)
		} else if strings.HasSuffix(r.URL.Path, string(EmbedAll)) {
			w.Header().Set("Content-Type", "application/json")
			rsp := [][][]float64{{{}}}
			_ = json.NewEncoder(w).Encode(rsp)
		}
	}))
	defer srv.Close()
	emb := New(
		WithBaseURL(srv.URL),
	)
	if _, err := emb.GetEmbedding(context.Background(), "hello"); err != nil {
		t.Fatalf("expected error for empty response")
	}

	emb2 := New(
		WithBaseURL(srv.URL),
		WithEmbedRoute(EmbedAll),
	)
	if _, err := emb2.GetEmbedding(context.Background(), "hello"); err != nil {
		t.Fatalf("received empty embedding vector from text embedding interface API")
	}
}
