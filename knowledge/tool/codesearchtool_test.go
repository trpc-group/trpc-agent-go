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

	"trpc.group/trpc-go/trpc-agent-go/agent"
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

type captureSourceKnowledge struct {
	*captureKnowledge
	sources []source.Source
}

func (s *captureSourceKnowledge) Sources() []source.Source {
	return s.sources
}

type stubRepoDescriptorSource struct {
	name        string
	description string
	ok          bool
}

func (s *stubRepoDescriptorSource) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	return nil, nil
}

func (s *stubRepoDescriptorSource) Name() string {
	return s.name
}

func (s *stubRepoDescriptorSource) Type() string {
	return source.TypeRepo
}

func (s *stubRepoDescriptorSource) GetMetadata() map[string]any {
	return nil
}

func (s *stubRepoDescriptorSource) RepositoryDescriptor() (name, description string, ok bool) {
	return s.name, s.description, s.ok
}

type stubPlainSource struct {
	name string
}

func (s *stubPlainSource) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	return nil, nil
}

func (s *stubPlainSource) Name() string {
	return s.name
}

func (s *stubPlainSource) Type() string {
	return source.TypeFile
}

func (s *stubPlainSource) GetMetadata() map[string]any {
	return nil
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

func TestCodeSearchOptionHelpersAndDerivation(t *testing.T) {
	t.Run("option helpers populate code search options", func(t *testing.T) {
		opts := &codeSearchOptions{dedupEnabled: true}
		condition := &searchfilter.UniversalFilterCondition{
			Field:    "metadata.trpc_ast_scope",
			Operator: searchfilter.OperatorEqual,
			Value:    "code",
		}

		WithCodeSearchToolDescription("custom description")(opts)
		WithCodeSearchConditionedFilter(condition)(opts)
		WithCodeSearchExtraFilterFields(map[string][]any{"metadata.custom": {"x"}})(opts)
		WithCodeSearchExtraExcludeMetadataKeys()(opts)
		WithCodeSearchExtraExcludeMetadataKeys("internal_a", "internal_b")(opts)
		WithCodeSearchDedup(false)(opts)
		WithCodeSearchMaxDedupKeysPerInvocation(2)(opts)

		require.Equal(t, "custom description", opts.toolDescription)
		require.Same(t, condition, opts.conditionedFilter)
		require.Equal(t, []any{"x"}, opts.extraFields["metadata.custom"])
		require.Equal(t, []string{"internal_a", "internal_b"}, opts.extraExcludeMetadataKeys)
		require.False(t, opts.dedupEnabled)
		require.Equal(t, 2, opts.maxDedupKeysPerInvocation)
	})

	t.Run("derive repo infos skips duplicates blanks and non-repo sources", func(t *testing.T) {
		kb := &captureSourceKnowledge{
			captureKnowledge: &captureKnowledge{},
			sources: []source.Source{
				&stubRepoDescriptorSource{name: "repo-a", description: " core ", ok: true},
				&stubRepoDescriptorSource{name: "repo-a", description: "duplicate", ok: true},
				&stubRepoDescriptorSource{name: "", description: "blank", ok: true},
				&stubRepoDescriptorSource{name: "repo-b", description: "", ok: true},
				&stubRepoDescriptorSource{name: "repo-c", description: "ignored", ok: false},
				&stubPlainSource{name: "plain"},
			},
		}

		infos := deriveCodeRepoInfos(kb)
		require.Equal(t, []CodeRepoInfo{
			{Name: "repo-a", Description: "core"},
			{Name: "repo-b", Description: ""},
		}, infos)
	})

	t.Run("derive repo infos returns nil when knowledge does not expose sources", func(t *testing.T) {
		require.Nil(t, deriveCodeRepoInfos(&captureKnowledge{}))
	})

	t.Run("build repo section omits blank descriptions", func(t *testing.T) {
		section := buildCodeRepoSection([]CodeRepoInfo{
			{Name: "repo-a", Description: "core"},
			{Name: "repo-b", Description: "   "},
		})
		require.Contains(t, section, "- repo-a: core")
		require.Contains(t, section, "- repo-b\n")
	})
}

func TestCodeSearchToolAdvancedOptions(t *testing.T) {
	t.Run("custom description is used verbatim", func(t *testing.T) {
		searchTool := NewCodeSearchTool(&captureKnowledge{}, WithCodeSearchToolDescription("custom code search"))
		require.Contains(t, searchTool.Declaration().Description, "custom code search")
		require.Contains(t, searchTool.Declaration().Description, "== FILTER GUIDANCE ==")
		require.NotContains(t, searchTool.Declaration().Description, "tool description:")
	})

	t.Run("conditioned filter extra fields and metadata exclusions are applied", func(t *testing.T) {
		kb := &captureKnowledge{
			result: &knowledge.SearchResult{
				Documents: []*knowledge.Result{{
					Document: &document.Document{
						Content: "func NewClient() {}",
						Metadata: map[string]any{
							"trpc_ast_type":      "Function",
							"trpc_ast_scope":     "code",
							"trpc_ast_full_name": "example.com/project.NewClient",
							"private_note":       "redact-me",
						},
					},
					Score: 0.91,
				}},
			},
		}
		condition := &searchfilter.UniversalFilterCondition{
			Field:    "metadata.trpc_ast_scope",
			Operator: searchfilter.OperatorEqual,
			Value:    "code",
		}
		searchTool := NewCodeSearchTool(
			kb,
			WithCodeSearchConditionedFilter(condition),
			WithCodeSearchExtraFilterFields(map[string][]any{
				"metadata.custom_scope":       {"internal"},
				"metadata.trpc_ast_repo_name": {"repo-override"},
			}),
			WithCodeSearchExtraExcludeMetadataKeys("private_note"),
		)

		decl := searchTool.Declaration()
		require.Contains(t, decl.Description, "metadata.custom_scope")
		require.Contains(t, decl.Description, "repo-override")

		reqFilter := &searchfilter.UniversalFilterCondition{
			Field:    "metadata.trpc_ast_full_name",
			Operator: searchfilter.OperatorEqual,
			Value:    "example.com/project.NewClient",
		}
		res, err := searchTool.(ctool.CallableTool).Call(context.Background(), marshalCodeSearchFilterArgs(t, "new client", reqFilter))
		require.NoError(t, err)

		rsp := res.(*KnowledgeSearchResponse)
		require.Len(t, rsp.Documents, 1)
		require.NotContains(t, rsp.Documents[0].Metadata, "trpc_ast_type")
		require.NotContains(t, rsp.Documents[0].Metadata, "private_note")
		require.NotNil(t, kb.lastRequest)
		require.NotNil(t, kb.lastRequest.SearchFilter)
		require.Equal(t, searchfilter.OperatorAnd, kb.lastRequest.SearchFilter.FilterCondition.Operator)
	})

	t.Run("dedup disabled returns repeated results within one invocation", func(t *testing.T) {
		kb := &captureKnowledge{
			result: &knowledge.SearchResult{
				Documents: []*knowledge.Result{{
					Document: &document.Document{
						Content: "func Shared() {}",
						Metadata: map[string]any{
							"trpc_ast_full_name": "example.com/project.Shared",
						},
					},
					Score: 0.8,
				}},
			},
		}
		searchTool := NewCodeSearchTool(kb, WithCodeSearchDedup(false))
		ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{InvocationID: "code-search-no-dedup"})

		for i := 0; i < 2; i++ {
			res, err := searchTool.(ctool.CallableTool).Call(ctx, marshalCodeSearchFilterArgs(t, "shared", nil))
			require.NoError(t, err)
			rsp := res.(*KnowledgeSearchResponse)
			require.Len(t, rsp.Documents, 1)
		}
	})

	t.Run("dedup cap eviction allows old result to reappear", func(t *testing.T) {
		kb := &captureKnowledge{}
		searchTool := NewCodeSearchTool(kb, WithCodeSearchMaxDedupKeysPerInvocation(1))
		ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{InvocationID: "code-search-cap"})

		kb.result = &knowledge.SearchResult{Documents: []*knowledge.Result{{
			Document: &document.Document{
				Content: "func A() {}",
				Metadata: map[string]any{
					"trpc_ast_full_name": "example.com/project.A",
				},
			},
			Score: 0.8,
		}}}
		res, err := searchTool.(ctool.CallableTool).Call(ctx, marshalCodeSearchFilterArgs(t, "A", nil))
		require.NoError(t, err)
		require.Len(t, res.(*KnowledgeSearchResponse).Documents, 1)

		kb.result = &knowledge.SearchResult{Documents: []*knowledge.Result{{
			Document: &document.Document{
				Content: "func B() {}",
				Metadata: map[string]any{
					"trpc_ast_full_name": "example.com/project.B",
				},
			},
			Score: 0.8,
		}}}
		res, err = searchTool.(ctool.CallableTool).Call(ctx, marshalCodeSearchFilterArgs(t, "B", nil))
		require.NoError(t, err)
		require.Len(t, res.(*KnowledgeSearchResponse).Documents, 1)

		kb.result = &knowledge.SearchResult{Documents: []*knowledge.Result{{
			Document: &document.Document{
				Content: "func A() {}",
				Metadata: map[string]any{
					"trpc_ast_full_name": "example.com/project.A",
				},
			},
			Score: 0.8,
		}}}
		res, err = searchTool.(ctool.CallableTool).Call(ctx, marshalCodeSearchFilterArgs(t, "A", nil))
		require.NoError(t, err)
		rsp := res.(*KnowledgeSearchResponse)
		require.Len(t, rsp.Documents, 1)
		require.Equal(t, "example.com/project.A", rsp.Documents[0].Metadata["trpc_ast_full_name"])
	})
}
