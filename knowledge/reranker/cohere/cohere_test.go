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

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/internal/httpclient"
)

func TestCohereReranker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		var req httpclient.RerankRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "rerank-english-v3.0", req.Model)

		// Mock response following the internal struct structure indirectly
		resp := map[string]interface{}{
			"results": []map[string]interface{}{
				{"index": 1, "relevance_score": 0.9},
				{"index": 0, "relevance_score": 0.5},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Hack: override endpoint for test is not directly possible via public API
	// So we create a custom Reranker by struct initialization for testing purpose
	// Or we can add WithEndpoint option (which is internal in current design but useful for testing)
	// Let's modify New to accept options that can override endpoint if needed, or just create struct.
	// Since endpoint is private in struct, we need to add an Option to expose it or use reflection?
	// Better approach: Add WithEndpoint option to cohere package.

	r := New(WithAPIKey("test-key"), WithEndpoint(server.URL))

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
