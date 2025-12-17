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

func TestInfinityReranker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req httpclient.RerankRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "bge-reranker", req.Model)

		resp := map[string]interface{}{
			"results": []map[string]interface{}{
				{"index": 0, "relevance_score": 0.99},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	r := New(WithEndpoint(server.URL), WithModel("bge-reranker"))

	query := &reranker.Query{FinalQuery: "test"}
	results := []*reranker.Result{
		{Document: &document.Document{Content: "D0"}},
	}

	reranked, err := r.Rerank(context.Background(), query, results)
	assert.NoError(t, err)
	assert.Len(t, reranked, 1)
	assert.Equal(t, 0.99, reranked[0].Score)
}
