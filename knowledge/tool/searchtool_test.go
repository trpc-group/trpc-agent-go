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

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	ctool "trpc.group/trpc-go/trpc-agent-go/tool"
)

// stubKnowledge implements the knowledge.Knowledge interface for testing.
// It can be configured to return a predetermined result or error.

type stubKnowledge struct {
	result *knowledge.SearchResult
	err    error
}

func (s stubKnowledge) Search(ctx context.Context, req *knowledge.SearchRequest) (*knowledge.SearchResult, error) {
	return s.result, s.err
}

func marshalArgs(t *testing.T, query string) []byte {
	t.Helper()
	bts, err := json.Marshal(KnowledgeSearchRequest{Query: query})
	require.NoError(t, err)
	return bts
}

func TestKnowledgeSearchTool(t *testing.T) {
	t.Run("empty query", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewKnowledgeSearchTool(kb)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, ""))
		require.NoError(t, err)
		rsp := res.(KnowledgeSearchResponse)
		require.False(t, rsp.Success)
		require.Contains(t, rsp.Message, "Query cannot be empty")
	})

	t.Run("search error", func(t *testing.T) {
		kb := stubKnowledge{err: errors.New("boom")}
		searchTool := NewKnowledgeSearchTool(kb)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.NoError(t, err)
		rsp := res.(KnowledgeSearchResponse)
		require.False(t, rsp.Success)
		require.Contains(t, rsp.Message, "Search failed")
	})

	t.Run("no result", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewKnowledgeSearchTool(kb)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.NoError(t, err)
		rsp := res.(KnowledgeSearchResponse)
		require.True(t, rsp.Success)
		require.Contains(t, rsp.Message, "No relevant information")
	})

	t.Run("success", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{Text: "foo", Score: 0.9}}
		searchTool := NewKnowledgeSearchTool(kb)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.NoError(t, err)
		rsp := res.(KnowledgeSearchResponse)
		require.True(t, rsp.Success)
		require.Equal(t, "foo", rsp.Text)
		require.Equal(t, 0.9, rsp.Score)
	})

	// Verify Declaration metadata is populated.
	kb := stubKnowledge{}
	ttool := NewKnowledgeSearchTool(kb)
	decl := ttool.Declaration()
	require.NotEmpty(t, decl.Name)
	require.NotEmpty(t, decl.Description)
	require.NotNil(t, decl.InputSchema)
	require.NotNil(t, decl.OutputSchema)
}
