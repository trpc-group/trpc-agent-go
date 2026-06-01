//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openviking

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// okEnvelope writes a successful OpenViking response envelope.
func okEnvelope(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": result})
}

// errEnvelope writes an error OpenViking response envelope.
func errEnvelope(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "error",
		"error":  map[string]any{"code": code, "message": message},
	})
}

// callTool finds a tool by name and invokes it with the given args.
func callTool(t *testing.T, ts *ToolSet, name string, args any) any {
	t.Helper()
	var target tool.CallableTool
	for _, tl := range ts.Tools(context.Background()) {
		if tl.Declaration().Name == name {
			ct, ok := tl.(tool.CallableTool)
			if !ok {
				t.Fatalf("tool %s is not callable", name)
			}
			target = ct
			break
		}
	}
	if target == nil {
		t.Fatalf("tool %s not found", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := target.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return out
}

func TestProfileToolSelection(t *testing.T) {
	cases := []struct {
		profile Profile
		want    []string
	}{
		{ProfileRetrieval, []string{toolFind, toolSearch, toolBrowse, toolRead, toolGrep, toolHealth}},
		{ProfileAgent, []string{toolFind, toolSearch, toolBrowse, toolRead, toolGrep, toolHealth, toolStore, toolAddResource, toolAddSkill}},
		{ProfileAdmin, []string{toolFind, toolSearch, toolBrowse, toolRead, toolGrep, toolHealth, toolStore, toolAddResource, toolAddSkill, toolForget}},
	}
	for _, tc := range cases {
		ts, err := NewToolSet(WithProfile(tc.profile))
		if err != nil {
			t.Fatalf("NewToolSet: %v", err)
		}
		got := toolNames(ts)
		if !equalStringSlices(got, tc.want) {
			t.Errorf("profile %s tools = %v, want %v", tc.profile, got, tc.want)
		}
		_ = ts.Close()
	}
}

func TestRetrievalProfileExcludesForget(t *testing.T) {
	ts, _ := NewToolSet(WithProfile(ProfileRetrieval), WithAllowForget(true))
	if !contains(toolNames(ts), toolForget) {
		t.Errorf("WithAllowForget should add viking_forget even for retrieval profile")
	}
}

func TestWithToolNamesOverride(t *testing.T) {
	ts, _ := NewToolSet(WithToolNames(toolRead))
	got := toolNames(ts)
	if !equalStringSlices(got, []string{toolRead}) {
		t.Errorf("tool names = %v, want [viking_read]", got)
	}
}

func TestWithToolNamesRejectsUnknown(t *testing.T) {
	if _, err := NewToolSet(WithToolNames("viking_bogus")); err == nil {
		t.Error("NewToolSet should fail fast on an unknown tool name")
	}
}

func TestWithToolNamesGatesForget(t *testing.T) {
	gated, err := NewToolSet(WithToolNames(toolFind, toolForget))
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	defer gated.Close()
	if contains(toolNames(gated), toolForget) {
		t.Error("viking_forget must not be exposed without admin profile or WithAllowForget")
	}

	allowed, err := NewToolSet(WithToolNames(toolFind, toolForget), WithAllowForget(true))
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	defer allowed.Close()
	if !contains(toolNames(allowed), toolForget) {
		t.Error("viking_forget should be exposed when WithAllowForget is set")
	}
}

func TestSearchTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/search/search" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["query"] != "hello" {
			t.Errorf("query = %v, want hello", body["query"])
		}
		okEnvelope(w, map[string]any{
			"memories": []map[string]any{
				{"uri": "viking://user/memories/x", "score": 0.9, "abstract": "a memory", "level": 1, "context_type": "memory"},
			},
			"resources": []map[string]any{},
			"skills":    []map[string]any{},
		})
	}))
	defer srv.Close()

	ts, _ := NewToolSet(WithBaseURL(srv.URL))
	defer ts.Close()

	out := callTool(t, ts, toolSearch, searchArgs{Query: "hello"}).(retrievalOutput)
	if len(out.Hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(out.Hits))
	}
	if out.Hits[0].URI != "viking://user/memories/x" || out.Hits[0].Type != "memory" {
		t.Errorf("unexpected hit %+v", out.Hits[0])
	}
	if !strings.Contains(out.Hint, "viking_read") {
		t.Errorf("hint should guide to viking_read, got %q", out.Hint)
	}
}

func TestReadToolContentModesAndTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/content/read":
			okEnvelope(w, "0123456789abcdef")
		case "/api/v1/content/overview":
			okEnvelope(w, "the overview")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	ts, _ := NewToolSet(WithBaseURL(srv.URL))
	defer ts.Close()

	full := callTool(t, ts, toolRead, readArgs{URI: "viking://resources/a", ContentMode: "read"}).(readOutput)
	if full.Content != "0123456789abcdef" || full.Truncated {
		t.Errorf("full read = %+v", full)
	}

	truncated := callTool(t, ts, toolRead, readArgs{URI: "viking://resources/a", ContentMode: "read", MaxChars: 4}).(readOutput)
	if truncated.Content != "0123" || !truncated.Truncated {
		t.Errorf("truncated read = %+v", truncated)
	}

	ov := callTool(t, ts, toolRead, readArgs{URI: "viking://resources/a", ContentMode: "overview"}).(readOutput)
	if ov.Content != "the overview" || ov.ContentMode != "overview" {
		t.Errorf("overview read = %+v", ov)
	}
}

func TestStoreToolCreatesSessionAndCommits(t *testing.T) {
	var createdSession, addedMessage, committed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/sessions" && r.Method == http.MethodPost:
			createdSession.Store(true)
			okEnvelope(w, map[string]any{"session_id": "sess-1"})
		case r.URL.Path == "/api/v1/sessions/sess-1/messages":
			addedMessage.Store(true)
			okEnvelope(w, map[string]any{"ok": true})
		case r.URL.Path == "/api/v1/sessions/sess-1/commit":
			committed.Store(true)
			okEnvelope(w, map[string]any{"task_id": "t1"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	ts, _ := NewToolSet(WithBaseURL(srv.URL))
	defer ts.Close()

	out := callTool(t, ts, toolStore, storeArgs{Content: "remember this", Commit: true}).(storeOutput)
	if out.SessionID != "sess-1" || !out.Committed {
		t.Errorf("store output = %+v", out)
	}
	if !createdSession.Load() || !addedMessage.Load() || !committed.Load() {
		t.Errorf("expected create+add+commit, got %v/%v/%v", createdSession.Load(), addedMessage.Load(), committed.Load())
	}
}

func TestStoreToolRejectsInvalidRole(t *testing.T) {
	ts, _ := NewToolSet(WithBaseURL("http://127.0.0.1:1"))
	defer ts.Close()

	var store tool.CallableTool
	for _, tl := range ts.Tools(context.Background()) {
		if tl.Declaration().Name == toolStore {
			store = tl.(tool.CallableTool)
			break
		}
	}
	if store == nil {
		t.Fatal("viking_store tool not found")
	}

	// Supply a session id so the call reaches role validation without first
	// creating a session over HTTP; an invalid role must error, not coerce.
	raw, _ := json.Marshal(storeArgs{Content: "hi", Role: "system", SessionID: "s1"})
	if _, err := store.Call(context.Background(), raw); err == nil {
		t.Error("viking_store should reject an unsupported role instead of rewriting it")
	}
}

func TestBrowseGrepHealthTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/fs/ls":
			if got := r.URL.Query().Get("uri"); got != "viking://resources" {
				t.Errorf("ls uri = %q", got)
			}
			okEnvelope(w, []map[string]any{{"uri": "viking://resources/a", "isDir": true}})
		case "/api/v1/search/grep":
			okEnvelope(w, []map[string]any{{"uri": "viking://resources/a/x.go", "line": 12}})
		case "/api/v1/system/status":
			okEnvelope(w, map[string]any{"status": "healthy"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	ts, _ := NewToolSet(WithBaseURL(srv.URL))
	defer ts.Close()

	browse := callTool(t, ts, toolBrowse, browseArgs{URI: "viking://resources"}).(string)
	if !strings.Contains(browse, "viking://resources/a") {
		t.Errorf("browse output = %q", browse)
	}
	grep := callTool(t, ts, toolGrep, grepArgs{URI: "viking://resources", Pattern: "func"}).(string)
	if !strings.Contains(grep, "x.go") {
		t.Errorf("grep output = %q", grep)
	}
	health := callTool(t, ts, toolHealth, healthArgs{}).(string)
	if !strings.Contains(health, "healthy") {
		t.Errorf("health output = %q", health)
	}
}

func TestFindToolRetriesOnUnavailable(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			errEnvelope(w, "UNAVAILABLE", "temporarily down")
			return
		}
		okEnvelope(w, map[string]any{"memories": []any{}, "resources": []any{}, "skills": []any{}})
	}))
	defer srv.Close()

	ts, _ := NewToolSet(WithBaseURL(srv.URL))
	defer ts.Close()

	out := callTool(t, ts, toolFind, findArgs{Query: "q"}).(retrievalOutput)
	if calls.Load() != 2 {
		t.Errorf("expected 2 calls (1 retry), got %d", calls.Load())
	}
	if len(out.Hits) != 0 {
		t.Errorf("expected no hits, got %d", len(out.Hits))
	}
}

func toolNames(ts *ToolSet) []string {
	var names []string
	for _, tl := range ts.Tools(context.Background()) {
		names = append(names, tl.Declaration().Name)
	}
	return names
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
