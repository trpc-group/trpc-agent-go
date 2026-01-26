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
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNew_NilModel(t *testing.T) {
	t.Parallel()
	_, err := New(nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "model is nil") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestToolSearch_AsPlugin(t *testing.T) {
	t.Parallel()

	m := &fakeModel{}
	ts, err := New(m)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	if ts.Name() == "" {
		t.Fatalf("expected plugin name")
	}

	pm, err := plugin.NewManager(ts)
	if err != nil {
		t.Fatalf("NewManager err: %v", err)
	}
	cbs := pm.ModelCallbacks()
	if cbs == nil || len(cbs.BeforeModel) == 0 {
		t.Fatalf("expected before model callback")
	}
}

func TestNew_DefaultMaxToolsAndSearcherChoice(t *testing.T) {
	t.Parallel()

	m := &fakeModel{}

	s1, err := New(m, WithMaxTools(0))
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	if s1.maxTools != defaultMaxTools {
		t.Fatalf("maxTools = %d, want %d", s1.maxTools, defaultMaxTools)
	}
	if _, ok := s1.searcher.(*llmSearch); !ok {
		t.Fatalf("searcher type = %T, want *llmSearch", s1.searcher)
	}
	if s1.failOpen {
		t.Fatalf("fallback should default to false")
	}

	// WithToolKnowledge switches to knowledge searcher.
	s2, err := New(m, WithToolKnowledge(&ToolKnowledge{}))
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	if _, ok := s2.searcher.(*knowledgeSearcher); !ok {
		t.Fatalf("searcher type = %T, want *knowledgeSearcher", s2.searcher)
	}

	s3, err := New(m, WithFailOpen())
	if err != nil {
		t.Fatalf("New err: %v", err)
	}
	if !s3.failOpen {
		t.Fatalf("fallback flag not applied")
	}
}

func TestCallback_EarlyReturns(t *testing.T) {
	t.Parallel()

	cb := (&ToolSearch{}).Callback()

	// nil args / nil request: no-op.
	if _, err := cb(context.Background(), nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, err := cb(context.Background(), &model.BeforeModelArgs{}); err != nil {
		t.Fatalf("err: %v", err)
	}

	// no tools: no-op.
	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}, Tools: map[string]tool.Tool{}}
	if _, err := cb(context.Background(), &model.BeforeModelArgs{Request: req}); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestCallback_NoCandidateTools_NoOp(t *testing.T) {
	t.Parallel()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			// Candidate builder filters out nil tools and nil declarations.
			"nilTool": nil,
			"nilDecl": fakeTool{decl: nil},
			// Also filters out always-include tools from candidates.
			"always": fakeTool{decl: &tool.Declaration{Name: "always", Description: "x"}},
		},
	}
	origSnapshot := make(map[string]tool.Tool, len(req.Tools))
	for k, v := range req.Tools {
		origSnapshot[k] = v
	}

	ts := &ToolSearch{
		searcher:      nil, // should not be called
		maxTools:      3,
		alwaysInclude: []string{"always"},
	}

	_, err := ts.Callback()(context.Background(), &model.BeforeModelArgs{Request: req})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(req.Tools) != len(origSnapshot) {
		t.Fatalf("tools map size changed: %d -> %d", len(origSnapshot), len(req.Tools))
	}
	for k, v := range origSnapshot {
		if req.Tools[k] != v {
			t.Fatalf("tools map changed at key %q", k)
		}
	}
}

func TestCallback_AlwaysIncludeMissingErrors(t *testing.T) {
	t.Parallel()

	ts := &ToolSearch{
		searcher:      &fakeSearcher{},
		maxTools:      1,
		alwaysInclude: []string{"missing"},
	}
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"t1": fakeTool{decl: &tool.Declaration{Name: "t1"}},
		},
	}

	_, err := ts.Callback()(context.Background(), &model.BeforeModelArgs{Request: req})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "always include") && !strings.Contains(err.Error(), "always_include") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestCallback_NoUserMessageErrors(t *testing.T) {
	t.Parallel()

	ts := &ToolSearch{
		searcher: &fakeSearcher{},
		maxTools: 2,
	}
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("sys")},
		Tools: map[string]tool.Tool{
			"t1": fakeTool{decl: &tool.Declaration{Name: "t1"}},
		},
	}
	_, err := ts.Callback()(context.Background(), &model.BeforeModelArgs{Request: req})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "no user message") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestCallback_NoUserMessage_FallbackToOriginalTools(t *testing.T) {
	t.Parallel()

	ts := &ToolSearch{
		searcher: &fakeSearcher{},
		maxTools: 2,
		failOpen: true,
	}
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("sys")},
		Tools: map[string]tool.Tool{
			"t1": fakeTool{decl: &tool.Declaration{Name: "t1"}},
		},
	}
	// Snapshot tools before callback.
	origSnapshot := make(map[string]tool.Tool, len(req.Tools))
	for k, v := range req.Tools {
		origSnapshot[k] = v
	}

	_, err := ts.Callback()(context.Background(), &model.BeforeModelArgs{Request: req})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Should not mutate tools.
	if len(req.Tools) != len(origSnapshot) {
		t.Fatalf("tools map size changed: %d -> %d", len(origSnapshot), len(req.Tools))
	}
	for k, v := range origSnapshot {
		if req.Tools[k] != v {
			t.Fatalf("tools map changed at key %q", k)
		}
	}
}

func TestCallback_SearcherErrorPropagates(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	fs := &fakeSearcher{err: wantErr}
	ts := &ToolSearch{
		searcher: fs,
		maxTools: 2,
	}
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"a": fakeTool{decl: &tool.Declaration{Name: "a"}},
		},
	}
	_, err := ts.Callback()(context.Background(), &model.BeforeModelArgs{Request: req})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestCallback_SearcherError_FallbackToOriginalTools(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	fs := &fakeSearcher{err: wantErr}
	ts := &ToolSearch{
		searcher: fs,
		maxTools: 2,
		failOpen: true,
	}
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"a": fakeTool{decl: &tool.Declaration{Name: "a"}},
		},
	}
	origSnapshot := make(map[string]tool.Tool, len(req.Tools))
	for k, v := range req.Tools {
		origSnapshot[k] = v
	}

	_, err := ts.Callback()(context.Background(), &model.BeforeModelArgs{Request: req})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Should not mutate tools.
	if len(req.Tools) != len(origSnapshot) {
		t.Fatalf("tools map size changed: %d -> %d", len(origSnapshot), len(req.Tools))
	}
	for k, v := range origSnapshot {
		if req.Tools[k] != v {
			t.Fatalf("tools map changed at key %q", k)
		}
	}
}

func TestCallback_SelectsAndAlwaysIncludes(t *testing.T) {
	t.Parallel()

	fs := &fakeSearcher{ret: []string{"a"}}
	ts := &ToolSearch{
		searcher:      fs,
		maxTools:      1,
		alwaysInclude: []string{"c"},
	}
	a := fakeTool{decl: &tool.Declaration{Name: "a", Description: "A"}}
	b := fakeTool{decl: &tool.Declaration{Name: "b", Description: "B"}}
	c := fakeTool{decl: &tool.Declaration{Name: "c", Description: "C"}}

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("find something")},
		Tools: map[string]tool.Tool{
			"a": a,
			"b": b,
			"c": c,
		},
	}
	_, err := ts.Callback()(context.Background(), &model.BeforeModelArgs{Request: req})
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Candidate tools exclude always-include.
	if len(fs.gotCandidates) != 2 || fs.gotCandidates["c"] != nil {
		if _, ok := fs.gotCandidates["c"]; ok {
			t.Fatalf("c should not be a candidate")
		}
	}
	if fs.gotTopK != 1 {
		t.Fatalf("topK = %d, want 1", fs.gotTopK)
	}
	if fs.gotQuery != "find something" {
		t.Fatalf("query = %q", fs.gotQuery)
	}

	// Tools map is rebuilt with selected + always include.
	if len(req.Tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(req.Tools))
	}
	if req.Tools["a"] != a {
		t.Fatalf("expected a to be present")
	}
	if req.Tools["c"] != c {
		t.Fatalf("expected c to be present (always include)")
	}
	if _, ok := req.Tools["b"]; ok {
		t.Fatalf("expected b to be removed")
	}
}

func TestHelpers_BuildCandidateToolsAndSelectedTools(t *testing.T) {
	t.Parallel()

	ts := &ToolSearch{alwaysInclude: []string{"keep"}}
	base := map[string]tool.Tool{
		"keep":    fakeTool{decl: &tool.Declaration{Name: "keep"}},
		"good":    fakeTool{decl: &tool.Declaration{Name: "good"}},
		"nilTool": nil,
		"nilDecl": fakeTool{decl: nil},
	}
	cands := ts.buildCandidateTools(base)
	if _, ok := cands["keep"]; ok {
		t.Fatalf("keep should be excluded from candidates")
	}
	if _, ok := cands["good"]; !ok {
		t.Fatalf("good should be included in candidates")
	}
	if _, ok := cands["nilTool"]; ok {
		t.Fatalf("nilTool should be excluded")
	}
	if _, ok := cands["nilDecl"]; ok {
		t.Fatalf("nilDecl should be excluded")
	}

	selected := buildSelectedTools(base, []string{"good"}, []string{"keep"})
	if len(selected) != 2 {
		t.Fatalf("len(selected) = %d, want 2", len(selected))
	}
	if _, ok := selected["good"]; !ok {
		t.Fatalf("good should be selected")
	}
	if _, ok := selected["keep"]; !ok {
		t.Fatalf("keep should be always included")
	}
}
