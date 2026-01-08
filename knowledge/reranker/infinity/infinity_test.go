//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package infinity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/internal/httpclient"
)

func TestInfinityReranker_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req httpclient.RerankRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "bge-reranker", req.Model)

		resp := map[string]any{
			"results": []map[string]any{
				{"index": 0, "relevance_score": 0.99},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	r, err := New(WithEndpoint(server.URL), WithModel("bge-reranker"))
	assert.NoError(t, err)

	query := &reranker.Query{FinalQuery: "test"}
	results := []*reranker.Result{
		{Document: &document.Document{Content: "D0"}},
	}

	reranked, err := r.Rerank(context.Background(), query, results)
	assert.NoError(t, err)
	assert.Len(t, reranked, 1)
	assert.Equal(t, 0.99, reranked[0].Score)
}

func TestInfinityReranker_EmptyInput(t *testing.T) {
	r, err := New(WithEndpoint("http://localhost:7997"))
	assert.NoError(t, err)
	query := &reranker.Query{FinalQuery: "test"}
	reranked, err := r.Rerank(context.Background(), query, []*reranker.Result{})
	assert.NoError(t, err)
	assert.Empty(t, reranked)
}

func TestInfinityReranker_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	r, err := New(WithEndpoint(server.URL))
	assert.NoError(t, err)
	query := &reranker.Query{FinalQuery: "test"}
	results := []*reranker.Result{{Document: &document.Document{Content: "D0"}}}

	_, err = r.Rerank(context.Background(), query, results)
	assert.Error(t, err)
}

func TestInfinityReranker_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid"))
	}))
	defer server.Close()

	r, err := New(WithEndpoint(server.URL))
	assert.NoError(t, err)
	query := &reranker.Query{FinalQuery: "test"}
	results := []*reranker.Result{{Document: &document.Document{Content: "D0"}}}

	_, err = r.Rerank(context.Background(), query, results)
	assert.Error(t, err)
}

func TestInfinityReranker_TopN(t *testing.T) {
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

	r, err := New(WithEndpoint(server.URL), WithTopN(1))
	assert.NoError(t, err)
	query := &reranker.Query{FinalQuery: "test"}
	results := []*reranker.Result{
		{Document: &document.Document{Content: "D0"}},
		{Document: &document.Document{Content: "D1"}},
	}

	reranked, err := r.Rerank(context.Background(), query, results)
	assert.NoError(t, err)
	assert.Len(t, reranked, 1)
	assert.Equal(t, 0.9, reranked[0].Score)
}

func TestInfinityReranker_Options(t *testing.T) {
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

func TestInfinityReranker_EmptyEndpoint(t *testing.T) {
	_, err := New()
	assert.Error(t, err)
	assert.Equal(t, errEndpointEmpty, err)
}
