//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cohere

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/internal/httpclient"
)

func TestCohereReranker_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		var req httpclient.RerankRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "rerank-english-v3.0", req.Model)

		resp := map[string]any{
			"results": []map[string]any{
				{"index": 1, "relevance_score": 0.9},
				{"index": 0, "relevance_score": 0.5},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	r, err := New(WithAPIKey("test-key"), WithEndpoint(server.URL))
	assert.NoError(t, err)

	query := &reranker.Query{FinalQuery: "test"}
	results := []*reranker.Result{
		{Document: &document.Document{Content: "D0"}},
		{Document: &document.Document{Content: "D1"}},
	}

	reranked, err := r.Rerank(context.Background(), query, results)
	assert.NoError(t, err)
	assert.Len(t, reranked, 2)
	assert.Equal(t, "D1", reranked[0].Document.Content)
	assert.Equal(t, 0.9, reranked[0].Score)
}

func TestCohereReranker_EmptyInput(t *testing.T) {
	r, err := New(WithAPIKey("test-key"))
	assert.NoError(t, err)
	query := &reranker.Query{FinalQuery: "test"}
	reranked, err := r.Rerank(context.Background(), query, []*reranker.Result{})
	assert.NoError(t, err)
	assert.Empty(t, reranked)
}

func TestCohereReranker_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	r, err := New(WithAPIKey("test-key"), WithEndpoint(server.URL))
	assert.NoError(t, err)
	query := &reranker.Query{FinalQuery: "test"}
	results := []*reranker.Result{{Document: &document.Document{Content: "D0"}}}

	_, err = r.Rerank(context.Background(), query, results)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestCohereReranker_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	r, err := New(WithAPIKey("test-key"), WithEndpoint(server.URL))
	assert.NoError(t, err)
	query := &reranker.Query{FinalQuery: "test"}
	results := []*reranker.Result{{Document: &document.Document{Content: "D0"}}}

	_, err = r.Rerank(context.Background(), query, results)
	assert.Error(t, err)
}

func TestCohereReranker_TopN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"results": []map[string]any{
				{"index": 0, "relevance_score": 0.9},
				{"index": 1, "relevance_score": 0.8},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	r, err := New(WithAPIKey("test-key"), WithEndpoint(server.URL), WithTopN(1))
	assert.NoError(t, err)
	query := &reranker.Query{FinalQuery: "test"}
	results := []*reranker.Result{
		{Document: &document.Document{Content: "D0"}},
		{Document: &document.Document{Content: "D1"}},
	}

	reranked, err := r.Rerank(context.Background(), query, results)
	assert.NoError(t, err)
	assert.Len(t, reranked, 1) // Should be truncated to TopN=1
	assert.Equal(t, "D0", reranked[0].Document.Content)
}

func TestCohereReranker_Options(t *testing.T) {
	r, err := New(
		WithAPIKey("key"),
		WithModel("custom-model"),
		WithEndpoint("http://custom"),
		WithTopN(10),
		WithHTTPClient(http.DefaultClient),
	)
	assert.NoError(t, err)

	assert.Equal(t, "key", r.apiKey)
	assert.Equal(t, "custom-model", r.modelName)
	assert.Equal(t, "http://custom", r.endpoint)
	assert.Equal(t, 10, r.topN)
	assert.NotNil(t, r.httpClient)
}

func TestCohereReranker_EmptyEndpoint(t *testing.T) {
	_, err := New(WithAPIKey("test-key"), WithEndpoint(""))
	assert.Error(t, err)
	assert.Equal(t, errEndpointEmpty, err)
}

func TestCohereReranker_ContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	r, err := New(WithAPIKey("test-key"), WithEndpoint(server.URL))
	assert.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	query := &reranker.Query{FinalQuery: "test"}
	results := []*reranker.Result{{Document: &document.Document{Content: "D0"}}}

	_, err = r.Rerank(ctx, query, results)
	assert.Error(t, err)
}
