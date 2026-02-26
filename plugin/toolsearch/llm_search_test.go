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

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestSearchTools_ModelCallError(t *testing.T) {
	t.Parallel()

	m := &fakeModel{
		generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
			return nil, errors.New("net down")
		},
	}
	_, _, err := searchTools(context.Background(), m, &model.Request{}, map[string]tool.Tool{"a": fakeTool{decl: &tool.Declaration{Name: "a"}}})
	if err == nil || !strings.Contains(err.Error(), "model call failed") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestSearchTools_ResponseError(t *testing.T) {
	t.Parallel()

	m := &fakeModel{
		generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
			return respCh(&model.Response{Error: &model.ResponseError{Message: "rate limit"}}), nil
		},
	}
	_, _, err := searchTools(context.Background(), m, &model.Request{}, map[string]tool.Tool{"a": fakeTool{decl: &tool.Declaration{Name: "a"}}})
	if err == nil || !strings.Contains(err.Error(), "model returned error") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestSearchTools_EmptyFinalOrEmptyContentErrors(t *testing.T) {
	t.Parallel()

	m1 := &fakeModel{
		generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
			return respCh(&model.Response{}), nil
		},
	}
	_, _, err := searchTools(context.Background(), m1, &model.Request{}, map[string]tool.Tool{"a": fakeTool{decl: &tool.Declaration{Name: "a"}}})
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("unexpected err: %v", err)
	}

	m2 := &fakeModel{
		generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
			return respCh(&model.Response{
				Choices: []model.Choice{{Message: model.Message{Content: "   "}}},
			}), nil
		},
	}
	_, _, err = searchTools(context.Background(), m2, &model.Request{}, map[string]tool.Tool{"a": fakeTool{decl: &tool.Declaration{Name: "a"}}})
	if err == nil || !strings.Contains(err.Error(), "empty content") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestSearchTools_ParsesAndDedupes(t *testing.T) {
	t.Parallel()

	m := &fakeModel{
		generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
			// Ensure we take the final (non-partial) message.
			return respCh(
				&model.Response{IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Content: `{"tools":["a"]}`}}}},
				&model.Response{IsPartial: false, Choices: []model.Choice{{Message: model.Message{Content: `{"tools":["a","a","b"]}`}}}},
			), nil
		},
	}
	tools := map[string]tool.Tool{
		"a": fakeTool{decl: &tool.Declaration{Name: "a"}},
		"b": fakeTool{decl: &tool.Declaration{Name: "b"}},
	}
	_, got, err := searchTools(context.Background(), m, &model.Request{}, tools)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSearchTools_UsesDeltaContentIfMessageEmpty(t *testing.T) {
	t.Parallel()

	m := &fakeModel{
		generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
			return respCh(&model.Response{
				IsPartial: false,
				Choices:   []model.Choice{{Message: model.Message{Content: ""}, Delta: model.Message{Content: `{"tools":["a"]}`}}},
			}), nil
		},
	}
	tools := map[string]tool.Tool{
		"a": fakeTool{decl: &tool.Declaration{Name: "a"}},
	}
	_, got, err := searchTools(context.Background(), m, &model.Request{}, tools)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSearchTools_ParsesJSONFromSurroundingText(t *testing.T) {
	t.Parallel()

	m := &fakeModel{
		generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
			content := "Here you go:\n{\"tools\":[\"a\"]}\nThanks."
			return respCh(&model.Response{Choices: []model.Choice{{Message: model.Message{Content: content}}}}), nil
		},
	}
	tools := map[string]tool.Tool{
		"a": fakeTool{decl: &tool.Declaration{Name: "a"}},
	}
	_, got, err := searchTools(context.Background(), m, &model.Request{}, tools)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSearchTools_InvalidOrUnparsableErrors(t *testing.T) {
	t.Parallel()

	tools := map[string]tool.Tool{
		"a": fakeTool{decl: &tool.Declaration{Name: "a"}},
	}

	t.Run("invalid_tool", func(t *testing.T) {
		m := &fakeModel{
			generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
				return respCh(&model.Response{Choices: []model.Choice{{Message: model.Message{Content: `{"tools":["nope"]}`}}}}), nil
			},
		}
		_, _, err := searchTools(context.Background(), m, &model.Request{}, tools)
		if err == nil || !strings.Contains(err.Error(), "invalid tools") {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("unparsable_json", func(t *testing.T) {
		m := &fakeModel{
			generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
				return respCh(&model.Response{Choices: []model.Choice{{Message: model.Message{Content: "not json at all"}}}}), nil
			},
		}
		_, _, err := searchTools(context.Background(), m, &model.Request{}, tools)
		if err == nil || !strings.Contains(err.Error(), "failed to parse selection JSON") {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

func TestLlmSearch_TopKTruncation(t *testing.T) {
	t.Parallel()

	m := &fakeModel{
		generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
			return respCh(&model.Response{Choices: []model.Choice{{Message: model.Message{Content: `{"tools":["a","b","c"]}`}}}}), nil
		},
	}
	s := newLlmSearch(m, "")
	candidates := map[string]tool.Tool{
		"a": fakeTool{decl: &tool.Declaration{Name: "a"}},
		"b": fakeTool{decl: &tool.Declaration{Name: "b"}},
		"c": fakeTool{decl: &tool.Declaration{Name: "c"}},
	}
	_, got, err := s.Search(context.Background(), candidates, "q", 2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
}

func TestRenderToolListAndSchema(t *testing.T) {
	t.Parallel()

	tools := []tool.Tool{
		fakeTool{decl: &tool.Declaration{Name: "a", Description: "desc a"}},
		fakeTool{decl: &tool.Declaration{Name: "b", Description: "desc b"}},
	}
	list := renderToolList(tools)
	if list != "- a: desc a\n- b: desc b" {
		t.Fatalf("unexpected list:\n%s", list)
	}

	schema := toolSelectionSchema(tools)
	props := schema["properties"].(map[string]any)
	toolsProp := props["tools"].(map[string]any)
	items := toolsProp["items"].(map[string]any)
	enum := items["enum"].([]string)
	if !reflect.DeepEqual(enum, []string{"a", "b"}) {
		t.Fatalf("enum = %v", enum)
	}
}
