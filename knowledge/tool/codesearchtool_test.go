//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	reposource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/repo"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	ctool "trpc.group/trpc-go/trpc-agent-go/tool"
)

type captureKnowledge struct {
	result      *knowledge.SearchResult
	err         error
	lastRequest *knowledge.SearchRequest
}

func (s *captureKnowledge) Search(ctx context.Context, req *knowledge.SearchRequest) (*knowledge.SearchResult, error) {
	s.lastRequest = req
	return s.result, s.err
}

func marshalCodeSearchFilterArgs(t *testing.T, query string, filter *searchfilter.UniversalFilterCondition) []byte {
	t.Helper()
	bts, err := json.Marshal(&KnowledgeSearchRequestWithFilter{Query: query, Filter: filter})
	require.NoError(t, err)
	return bts
}

func TestCodeSearchTool(t *testing.T) {
	t.Run("reuses agentic filter input schema", func(t *testing.T) {
		kb := &captureKnowledge{}
		searchTool := NewCodeSearchTool(kb)
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalCodeSearchFilterArgs(t, "", nil))
		require.Error(t, err)
		require.Contains(t, err.Error(), "at least one of query or filter must be provided")
	})

	t.Run("empty query uses filter mode", func(t *testing.T) {
		kb := &captureKnowledge{result: &knowledge.SearchResult{Documents: []*knowledge.Result{{Document: &document.Document{Content: "func NewClient() {}"}, Score: 0.8}}}}
		searchTool := NewCodeSearchTool(kb)
		filter := &searchfilter.UniversalFilterCondition{Field: "metadata.trpc_ast_full_name", Operator: "eq", Value: "example.com/project/client.NewClient"}
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalCodeSearchFilterArgs(t, "", filter))
		require.NoError(t, err)
		require.NotNil(t, kb.lastRequest)
		require.Equal(t, vectorstore.SearchModeFilter, kb.lastRequest.SearchMode)
	})

	t.Run("returns normal knowledge search response", func(t *testing.T) {
		kb := &captureKnowledge{result: &knowledge.SearchResult{Documents: []*knowledge.Result{{
			Document: &document.Document{Content: "func NewClient() *Client { return &Client{} }", Metadata: map[string]any{
				"trpc_ast_type":      "Function",
				"trpc_ast_scope":     "code",
				"trpc_ast_full_name": "example.com/project/client.NewClient",
			}},
			Score: 0.93,
		}}}}
		searchTool := NewCodeSearchTool(kb)
		filter := &searchfilter.UniversalFilterCondition{Field: "metadata.trpc_ast_scope", Operator: "eq", Value: "code"}
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalCodeSearchFilterArgs(t, "create client", filter))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Len(t, rsp.Documents, 1)
		require.Equal(t, "func NewClient() *Client { return &Client{} }", rsp.Documents[0].Text)
		require.Equal(t, "example.com/project/client.NewClient", rsp.Documents[0].Metadata["trpc_ast_full_name"])
		require.NotContains(t, rsp.Documents[0].Metadata, "trpc_ast_type")
		require.NotContains(t, rsp.Documents[0].Metadata, "trpc_ast_comment")
		require.Equal(t, 0.93, rsp.Documents[0].Score)
	})

	t.Run("wraps underlying search error", func(t *testing.T) {
		kb := &captureKnowledge{err: errors.New("boom")}
		searchTool := NewCodeSearchTool(kb)
		filter := &searchfilter.UniversalFilterCondition{Field: "metadata.trpc_ast_scope", Operator: "eq", Value: "code"}
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalCodeSearchFilterArgs(t, "client", filter))
		require.Error(t, err)
		require.Contains(t, err.Error(), "search failed")
	})

	t.Run("declaration metadata includes code prompt and repo info", func(t *testing.T) {
		kb := &captureKnowledge{}
		searchTool := NewCodeSearchTool(
			kb,
			WithCodeSearchToolName("repo_code_search"),
			WithCodeSearchRepoInfos([]CodeRepoInfo{{Name: "repo-a", Description: "core framework"}}),
		)
		decl := searchTool.Declaration()
		require.Equal(t, "repo_code_search", decl.Name)
		require.Contains(t, decl.Description, "metadata.trpc_ast_scope")
		require.Contains(t, decl.Description, "repo-a")
		require.NotNil(t, decl.InputSchema)
		require.NotNil(t, decl.OutputSchema)
	})

	t.Run("declaration auto-derives repo descriptions from repo sources", func(t *testing.T) {
		kb := knowledge.New(knowledge.WithSources([]source.Source{
			reposource.New(reposource.WithRepository(reposource.Repository{
				URL:         "https://example.com/repo-a.git",
				RepoName:    "repo-a",
				Description: "core framework",
			})),
		}))
		searchTool := NewCodeSearchTool(kb)
		decl := searchTool.Declaration()
		require.Contains(t, decl.Description, "repo-a")
		require.Contains(t, decl.Description, "core framework")
	})

	t.Run("passes configured max score and static filter through wrapper", func(t *testing.T) {
		kb := &captureKnowledge{result: &knowledge.SearchResult{Documents: []*knowledge.Result{{Document: &document.Document{Content: "func NewClient() {}"}, Score: 0.8}}}}
		searchTool := NewCodeSearchTool(
			kb,
			WithCodeSearchMaxResults(5),
			WithCodeSearchMinScore(0.6),
			WithCodeSearchFilter(map[string]any{"metadata.trpc_ast_scope": "code"}),
		)
		filter := &searchfilter.UniversalFilterCondition{Field: "metadata.trpc_ast_type", Operator: "eq", Value: "Function"}
		_, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalCodeSearchFilterArgs(t, "client", filter))
		require.NoError(t, err)
		require.Equal(t, 5, kb.lastRequest.MaxResults)
		require.Equal(t, 0.6, kb.lastRequest.MinScore)
		require.NotNil(t, kb.lastRequest.SearchFilter)
		require.NotNil(t, kb.lastRequest.SearchFilter.FilterCondition)
		require.Equal(t, searchfilter.OperatorAnd, kb.lastRequest.SearchFilter.FilterCondition.Operator)
	})
}
