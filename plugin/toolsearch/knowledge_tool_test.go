//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestKnowledgeSearcher_RewriteQuery(t *testing.T) {
	t.Parallel()

	t.Run("model_call_error", func(t *testing.T) {
		m := &fakeModel{
			generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
				return nil, errors.New("boom")
			},
		}
		s := newKnowledgeSearcher(m, "", &ToolKnowledge{})
		_, _, _, err := s.rewriteQuery(context.Background(), "q")
		if err == nil || !strings.Contains(err.Error(), "selection model call failed") {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("response_error", func(t *testing.T) {
		m := &fakeModel{
			generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
				return respCh(&model.Response{Error: &model.ResponseError{Message: "nope"}}), nil
			},
		}
		s := newKnowledgeSearcher(m, "", &ToolKnowledge{})
		_, _, _, err := s.rewriteQuery(context.Background(), "q")
		if err == nil || !strings.Contains(err.Error(), "selection model returned error") {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("uses_final_delta_if_message_empty", func(t *testing.T) {
		m := &fakeModel{
			generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
				return respCh(&model.Response{
					IsPartial: false,
					Choices:   []model.Choice{{Message: model.Message{Content: ""}, Delta: model.Message{Content: "rewritten"}}},
				}), nil
			},
		}
		s := newKnowledgeSearcher(m, "", &ToolKnowledge{})
		_, got, _, err := s.rewriteQuery(context.Background(), "q")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != "rewritten" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("empty_final_errors", func(t *testing.T) {
		m := &fakeModel{
			generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
				return respCh(&model.Response{}), nil
			},
		}
		s := newKnowledgeSearcher(m, "", &ToolKnowledge{})
		_, _, _, err := s.rewriteQuery(context.Background(), "q")
		if err == nil || !strings.Contains(err.Error(), "empty response") {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("empty_content_errors", func(t *testing.T) {
		m := &fakeModel{
			generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
				return respCh(&model.Response{
					IsPartial: false,
					Choices:   []model.Choice{{Message: model.Message{Content: "   "}}},
				}), nil
			},
		}
		s := newKnowledgeSearcher(m, "", &ToolKnowledge{})
		_, _, _, err := s.rewriteQuery(context.Background(), "q")
		if err == nil || !strings.Contains(err.Error(), "empty content") {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

func TestKnowledgeSearcher_Search_UpsertsAndSearches(t *testing.T) {
	t.Parallel()

	vs := &fakeVectorStore{searchResultIDs: []string{"b", "a"}}
	emb := &fakeEmbedder{}
	k := &ToolKnowledge{s: vs, e: emb, tools: map[string]tool.Tool{}}

	m := &fakeModel{
		generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
			// rewriteQuery result
			return respCh(&model.Response{Choices: []model.Choice{{Message: model.Message{Content: "tool category"}}}}), nil
		},
	}

	s := newKnowledgeSearcher(m, "", k)
	cands := map[string]tool.Tool{
		"a": fakeTool{decl: &tool.Declaration{Name: "a", Description: "A"}},
		"b": fakeTool{decl: &tool.Declaration{Name: "b", Description: "B"}},
	}
	_, got, err := s.Search(context.Background(), cands, "original query", 2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Fatalf("got %v", got)
	}

	// Upsert should have added both tools once.
	if len(vs.adds) != 2 {
		t.Fatalf("adds = %d, want 2", len(vs.adds))
	}
	// Search should use vector mode + IDs filter.
	if vs.lastSearchQuery == nil || vs.lastSearchQuery.SearchMode != vectorstore.SearchModeVector {
		t.Fatalf("unexpected search query: %#v", vs.lastSearchQuery)
	}
	if vs.lastSearchQuery.Filter == nil || len(vs.lastSearchQuery.Filter.IDs) != 2 {
		t.Fatalf("expected IDs filter, got %#v", vs.lastSearchQuery.Filter)
	}
}

func TestToolToText(t *testing.T) {
	t.Parallel()

	if toolToText(nil) != "" {
		t.Fatalf("nil tool should render empty string")
	}
	if toolToText(fakeTool{decl: nil}) != "" {
		t.Fatalf("nil declaration should render empty string")
	}

	tt := fakeTool{decl: &tool.Declaration{
		Name:        "t",
		Description: "d",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"arr": {Items: &tool.Schema{Type: "string"}, Description: "x"},
				"obj": {Properties: map[string]*tool.Schema{"k": {Type: "string"}}, Description: "y"},
				"str": {Type: "string", Description: "z"},
				"nil": nil,
			},
		},
	}}
	out := toolToText(tt)
	if !strings.Contains(out, "Tool: t") || !strings.Contains(out, "Description: d") {
		t.Fatalf("unexpected output: %q", out)
	}
	// Best-effort type inference for missing Type.
	if !strings.Contains(out, "arr (array): x") {
		t.Fatalf("expected inferred array type, got: %q", out)
	}
	if !strings.Contains(out, "obj (object): y") {
		t.Fatalf("expected inferred object type, got: %q", out)
	}
	if !strings.Contains(out, "str (string): z") {
		t.Fatalf("expected string type, got: %q", out)
	}
}

func TestToolKnowledge_UpsertAndSearch_ErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("upsert_embedder_error", func(t *testing.T) {
		vs := &fakeVectorStore{}
		emb := &fakeEmbedder{embedErr: errors.New("embed fail")}
		k := &ToolKnowledge{s: vs, e: emb, tools: map[string]tool.Tool{}}
		_, err := k.upsert(context.Background(), map[string]tool.Tool{
			"a": fakeTool{decl: &tool.Declaration{Name: "a"}},
		})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("upsert_store_add_error", func(t *testing.T) {
		vs := &fakeVectorStore{addErr: errors.New("add fail")}
		emb := &fakeEmbedder{}
		k := &ToolKnowledge{s: vs, e: emb, tools: map[string]tool.Tool{}}
		_, err := k.upsert(context.Background(), map[string]tool.Tool{
			"a": fakeTool{decl: &tool.Declaration{Name: "a"}},
		})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("search_embedder_error", func(t *testing.T) {
		vs := &fakeVectorStore{}
		emb := &fakeEmbedder{embedErr: errors.New("embed fail")}
		k := &ToolKnowledge{s: vs, e: emb, tools: map[string]tool.Tool{}}
		_, _, _, err := k.search(context.Background(), map[string]tool.Tool{"a": fakeTool{decl: &tool.Declaration{Name: "a"}}}, "q", 1)
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("search_store_error", func(t *testing.T) {
		vs := &fakeVectorStore{searchErr: errors.New("search fail")}
		emb := &fakeEmbedder{}
		k := &ToolKnowledge{s: vs, e: emb, tools: map[string]tool.Tool{}}
		_, _, _, err := k.search(context.Background(), map[string]tool.Tool{"a": fakeTool{decl: &tool.Declaration{Name: "a"}}}, "q", 1)
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}
