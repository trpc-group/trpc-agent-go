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
	"time"

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
func callTool(t *testing.T, ts *ToolSet, name ToolName, args any) any {
	t.Helper()
	var target tool.CallableTool
	for _, tl := range ts.Tools(context.Background()) {
		if tl.Declaration().Name == string(name) {
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
		want    []ToolName
	}{
		{ProfileRetrieval, []ToolName{ToolFind, ToolSearch, ToolBrowse, ToolRead, ToolGrep, ToolHealth}},
		{ProfileAgent, []ToolName{ToolFind, ToolSearch, ToolBrowse, ToolRead, ToolGrep, ToolHealth, ToolStore, ToolAddResource, ToolAddSkill}},
		{ProfileAdmin, []ToolName{ToolFind, ToolSearch, ToolBrowse, ToolRead, ToolGrep, ToolHealth, ToolStore, ToolAddResource, ToolAddSkill, ToolForget}},
	}
	for _, tc := range cases {
		ts, err := NewToolSet(WithProfile(tc.profile))
		if err != nil {
			t.Fatalf("NewToolSet: %v", err)
		}
		got := toolNames(ts)
		if !equalToolSlices(got, tc.want) {
			t.Errorf("profile %s tools = %v, want %v", tc.profile, got, tc.want)
		}
		_ = ts.Close()
	}
}

func TestNonAdminProfilesExcludeForget(t *testing.T) {
	for _, p := range []Profile{ProfileRetrieval, ProfileAgent} {
		ts, _ := NewToolSet(WithProfile(p))
		if containsTool(toolNames(ts), ToolForget) {
			t.Errorf("profile %s must not expose viking_forget", p)
		}
		_ = ts.Close()
	}
}

func TestWithSpecificToolsOverride(t *testing.T) {
	ts, _ := NewToolSet(WithSpecificTools(ToolRead))
	got := toolNames(ts)
	if !equalToolSlices(got, []ToolName{ToolRead}) {
		t.Errorf("tool names = %v, want [viking_read]", got)
	}
}

func TestWithSpecificToolsRejectsUnknown(t *testing.T) {
	if _, err := NewToolSet(WithSpecificTools("viking_bogus")); err == nil {
		t.Error("NewToolSet should fail fast on an unknown tool name")
	}
}

func TestWithSpecificToolsGatesForget(t *testing.T) {
	gated, err := NewToolSet(WithSpecificTools(ToolFind, ToolForget))
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	defer gated.Close()
	if containsTool(toolNames(gated), ToolForget) {
		t.Error("viking_forget must not be exposed without the admin profile")
	}

	allowed, err := NewToolSet(WithSpecificTools(ToolFind, ToolForget), WithProfile(ProfileAdmin))
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	defer allowed.Close()
	if !containsTool(toolNames(allowed), ToolForget) {
		t.Error("viking_forget should be exposed under the admin profile")
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

	out := callTool(t, ts, ToolSearch, searchArgs{Query: "hello"}).(retrievalOutput)
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

	full := callTool(t, ts, ToolRead, readArgs{URI: "viking://resources/a", ContentMode: "read"}).(readOutput)
	if full.Content != "0123456789abcdef" || full.Truncated {
		t.Errorf("full read = %+v", full)
	}

	truncated := callTool(t, ts, ToolRead, readArgs{URI: "viking://resources/a", ContentMode: "read", MaxChars: 4}).(readOutput)
	if truncated.Content != "0123" || !truncated.Truncated {
		t.Errorf("truncated read = %+v", truncated)
	}

	ov := callTool(t, ts, ToolRead, readArgs{URI: "viking://resources/a", ContentMode: "overview"}).(readOutput)
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

	out := callTool(t, ts, ToolStore, storeArgs{Content: "remember this", Commit: true}).(storeOutput)
	if out.SessionID != "sess-1" || !out.Committed {
		t.Errorf("store output = %+v", out)
	}
	if !createdSession.Load() || !addedMessage.Load() || !committed.Load() {
		t.Errorf("expected create+add+commit, got %v/%v/%v", createdSession.Load(), addedMessage.Load(), committed.Load())
	}
}

func TestStoreToolRejectsInvalidRole(t *testing.T) {
	// The role is validated before any remote write, so an invalid role with no
	// session_id must error without auto-creating a (now stray) session.
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		okEnvelope(w, map[string]any{"session_id": "sess-1"})
	}))
	defer srv.Close()

	ts, _ := NewToolSet(WithBaseURL(srv.URL))
	defer ts.Close()

	var store tool.CallableTool
	for _, tl := range ts.Tools(context.Background()) {
		if tl.Declaration().Name == string(ToolStore) {
			store = tl.(tool.CallableTool)
			break
		}
	}
	if store == nil {
		t.Fatal("viking_store tool not found")
	}

	raw, _ := json.Marshal(storeArgs{Content: "hi", Role: "system"})
	if _, err := store.Call(context.Background(), raw); err == nil {
		t.Error("viking_store should reject an unsupported role instead of rewriting it")
	}
	if hits.Load() != 0 {
		t.Errorf("invalid role must not trigger any remote call, got %d", hits.Load())
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

	browse := callTool(t, ts, ToolBrowse, browseArgs{URI: "viking://resources"}).(string)
	if !strings.Contains(browse, "viking://resources/a") {
		t.Errorf("browse output = %q", browse)
	}
	grep := callTool(t, ts, ToolGrep, grepArgs{URI: "viking://resources", Pattern: "func"}).(string)
	if !strings.Contains(grep, "x.go") {
		t.Errorf("grep output = %q", grep)
	}
	health := callTool(t, ts, ToolHealth, healthArgs{}).(string)
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

	out := callTool(t, ts, ToolFind, findArgs{Query: "q"}).(retrievalOutput)
	if calls.Load() != 2 {
		t.Errorf("expected 2 calls (1 retry), got %d", calls.Load())
	}
	if len(out.Hits) != 0 {
		t.Errorf("expected no hits, got %d", len(out.Hits))
	}
}

func TestOptionsConfigureClientAndName(t *testing.T) {
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		okEnvelope(w, map[string]any{"status": "healthy"})
	}))
	defer srv.Close()

	ts, err := NewToolSet(
		WithBaseURL(srv.URL),
		WithAPIKey("k"),
		WithAccount("acct"),
		WithUser("u"),
		WithAgent("ag"),
		WithTimeout(5*time.Second),
		WithProfile(ProfileAdmin),
	)
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	defer ts.Close()

	if ts.Name() != "" {
		t.Errorf("Name() = %q, want empty to avoid the openviking_ prefix", ts.Name())
	}
	_ = callTool(t, ts, ToolHealth, healthArgs{}).(string)
	for h, want := range map[string]string{
		"X-Api-Key":            "k",
		"X-Openviking-Account": "acct",
		"X-Openviking-User":    "u",
		"X-Openviking-Agent":   "ag",
	} {
		if got := headers.Get(h); got != want {
			t.Errorf("header %s = %q, want %q", h, got, want)
		}
	}
}

func TestAdminWriteTools(t *testing.T) {
	var lastPath, lastMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath, lastMethod = r.URL.Path, r.Method
		okEnvelope(w, map[string]any{"accepted": true})
	}))
	defer srv.Close()

	ts, _ := NewToolSet(WithBaseURL(srv.URL), WithProfile(ProfileAdmin))
	defer ts.Close()

	if out := callTool(t, ts, ToolAddResource, addResourceArgs{Path: "https://x", Wait: false}).(string); !strings.Contains(out, "accepted") {
		t.Errorf("add_resource output = %q", out)
	}
	if lastPath != "/api/v1/resources" || lastMethod != http.MethodPost {
		t.Errorf("add_resource hit %s %s", lastMethod, lastPath)
	}
	if out := callTool(t, ts, ToolAddSkill, addSkillArgs{Data: "skill"}).(string); !strings.Contains(out, "accepted") {
		t.Errorf("add_skill output = %q", out)
	}
	if lastPath != "/api/v1/skills" {
		t.Errorf("add_skill hit %s", lastPath)
	}
	if out := callTool(t, ts, ToolForget, forgetArgs{URI: "viking://x", Recursive: true}).(string); !strings.Contains(out, "accepted") {
		t.Errorf("forget output = %q", out)
	}
	if lastPath != "/api/v1/fs" || lastMethod != http.MethodDelete {
		t.Errorf("forget hit %s %s", lastMethod, lastPath)
	}
}

func TestReadModes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/content/abstract":
			okEnvelope(w, "L0 abstract")
		case "/api/v1/content/overview":
			okEnvelope(w, "L1 overview")
		case "/api/v1/content/read":
			okEnvelope(w, "0123456789")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	ts, _ := NewToolSet(WithBaseURL(srv.URL))
	defer ts.Close()

	if out := callTool(t, ts, ToolRead, readArgs{URI: "viking://d", ContentMode: "abstract"}).(readOutput); out.Content != "L0 abstract" {
		t.Errorf("abstract = %q", out.Content)
	}
	if out := callTool(t, ts, ToolRead, readArgs{URI: "viking://d", ContentMode: "overview"}).(readOutput); out.Content != "L1 overview" {
		t.Errorf("overview = %q", out.Content)
	}
	// Default mode is read, and max_chars truncates by rune count.
	out := callTool(t, ts, ToolRead, readArgs{URI: "viking://d/f", MaxChars: 4}).(readOutput)
	if out.Content != "0123" || !out.Truncated {
		t.Errorf("read truncate = %q truncated=%v", out.Content, out.Truncated)
	}

	// An invalid content_mode must error rather than silently default.
	var ct tool.CallableTool
	for _, tl := range ts.Tools(context.Background()) {
		if tl.Declaration().Name == string(ToolRead) {
			ct = tl.(tool.CallableTool)
		}
	}
	raw, _ := json.Marshal(readArgs{URI: "viking://d", ContentMode: "bogus"})
	if _, err := ct.Call(context.Background(), raw); err == nil {
		t.Error("invalid content_mode should error")
	}
}

func TestBrowseGlob(t *testing.T) {
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		okEnvelope(w, []map[string]any{{"uri": "viking://r/x.go"}})
	}))
	defer srv.Close()

	ts, _ := NewToolSet(WithBaseURL(srv.URL))
	defer ts.Close()

	// A pattern routes to glob instead of ls.
	out := callTool(t, ts, ToolBrowse, browseArgs{URI: "viking://r", Pattern: "*.go"}).(string)
	if hitPath != "/api/v1/search/glob" {
		t.Errorf("browse with pattern hit %s, want glob", hitPath)
	}
	if !strings.Contains(out, "x.go") {
		t.Errorf("glob output = %q", out)
	}
}

func TestPureHelpers(t *testing.T) {
	if scorePtr(0) != nil {
		t.Error("scorePtr(0) should be nil")
	}
	if p := scorePtr(0.3); p == nil || *p != 0.3 {
		t.Errorf("scorePtr(0.3) = %v", p)
	}
	if normalizeLimit(0) != defaultRetrievalLimit || normalizeLimit(7) != 7 {
		t.Error("normalizeLimit")
	}
	if firstNonEmpty("", "", "x") != "x" || firstNonEmpty("", "") != "" {
		t.Error("firstNonEmpty")
	}
	if s, trunc := truncateRunes("héllo", 0); s != "héllo" || trunc {
		t.Errorf("truncateRunes no limit = %q %v", s, trunc)
	}
	if s, trunc := truncateRunes("héllo", 2); s != "hé" || !trunc {
		t.Errorf("truncateRunes rune-safe = %q %v", s, trunc)
	}
	if extractSessionID([]byte(`{"session_id":"abc"}`)) != "abc" {
		t.Error("extractSessionID valid")
	}
	if extractSessionID([]byte(`not json`)) != "" {
		t.Error("extractSessionID invalid should be empty")
	}
}

func TestToolsSurfaceServerErrors(t *testing.T) {
	// Every tool must propagate a server error instead of swallowing it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errEnvelope(w, "INTERNAL", "boom")
	}))
	defer srv.Close()

	ts, _ := NewToolSet(WithBaseURL(srv.URL), WithProfile(ProfileAdmin))
	defer ts.Close()

	byName := map[ToolName]tool.CallableTool{}
	for _, tl := range ts.Tools(context.Background()) {
		byName[ToolName(tl.Declaration().Name)] = tl.(tool.CallableTool)
	}

	cases := []struct {
		name ToolName
		args any
	}{
		{ToolFind, findArgs{Query: "q"}},
		{ToolSearch, searchArgs{Query: "q"}},
		{ToolBrowse, browseArgs{URI: "viking://r"}},
		{ToolRead, readArgs{URI: "viking://r"}},
		{ToolGrep, grepArgs{URI: "viking://r", Pattern: "x"}},
		{ToolStore, storeArgs{Content: "hi"}}, // CreateSession fails
		{ToolAddResource, addResourceArgs{Path: "https://x"}},
		{ToolAddSkill, addSkillArgs{Data: "d"}},
		{ToolHealth, healthArgs{}},
		{ToolForget, forgetArgs{URI: "viking://x"}},
	}
	for _, tc := range cases {
		raw, _ := json.Marshal(tc.args)
		if _, err := byName[tc.name].Call(context.Background(), raw); err == nil {
			t.Errorf("%s should surface the server error", tc.name)
		}
	}
}

func toolNames(ts *ToolSet) []ToolName {
	var names []ToolName
	for _, tl := range ts.Tools(context.Background()) {
		names = append(names, ToolName(tl.Declaration().Name))
	}
	return names
}

func equalToolSlices(a, b []ToolName) bool {
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
