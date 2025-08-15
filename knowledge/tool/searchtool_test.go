//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
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
	bts, err := json.Marshal(&KnowledgeSearchRequest{Query: query})
	require.NoError(t, err)
	return bts
}

func TestKnowledgeSearchTool(t *testing.T) {
	t.Run("empty query", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewKnowledgeSearchTool(kb)
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, ""))
		require.Error(t, err)
		require.Contains(t, err.Error(), "query cannot be empty")
	})

	t.Run("search error", func(t *testing.T) {
		kb := stubKnowledge{err: errors.New("boom")}
		searchTool := NewKnowledgeSearchTool(kb)
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "search failed")
	})

	t.Run("no result", func(t *testing.T) {
		kb := stubKnowledge{}
		searchTool := NewKnowledgeSearchTool(kb)
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "no relevant information found")
	})

	t.Run("success", func(t *testing.T) {
		kb := stubKnowledge{result: &knowledge.SearchResult{Text: "foo", Score: 0.9}}
		searchTool := NewKnowledgeSearchTool(kb)
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalArgs(t, "hello"))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Equal(t, "foo", rsp.Text)
		require.Equal(t, 0.9, rsp.Score)
		require.Contains(t, rsp.Message, "Found relevant content")
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
