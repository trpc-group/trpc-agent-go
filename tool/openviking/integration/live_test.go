//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package integration holds live integration tests for the OpenViking tools.
// These tests talk to a real OpenViking server and are NOT meant to be merged
// upstream; they exist only for local verification. They are skipped unless the
// OPENVIKING_* environment variables are set, so they never run in CI.
package integration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/openviking"
)

// TestOpenVikingLive exercises every tool against a real OpenViking server.
// It is skipped unless OPENVIKING_BASE_URL and OPENVIKING_API_KEY are set, so
// it never runs in CI.
//
//	OPENVIKING_BASE_URL=http://localhost:1933 \
//	OPENVIKING_API_KEY=xxx \
//	OPENVIKING_ACCOUNT=default OPENVIKING_USER=default \
//	go test -run TestOpenVikingLive -count=1 -v ./tool/openviking/integration/
func TestOpenVikingLive(t *testing.T) {
	baseURL := os.Getenv("OPENVIKING_BASE_URL")
	apiKey := os.Getenv("OPENVIKING_API_KEY")
	if baseURL == "" || apiKey == "" {
		t.Skip("set OPENVIKING_BASE_URL and OPENVIKING_API_KEY to run the live integration test")
	}

	ts, err := openviking.NewToolSet(
		openviking.WithBaseURL(baseURL),
		openviking.WithAPIKey(apiKey),
		openviking.WithAccount(getenvDefault("OPENVIKING_ACCOUNT", "default")),
		openviking.WithUser(getenvDefault("OPENVIKING_USER", "default")),
		openviking.WithProfile(openviking.ProfileAdmin),
		openviking.WithTimeout(60*time.Second),
	)
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	defer ts.Close()

	ctx := context.Background()
	tools := map[string]tool.CallableTool{}
	for _, tl := range ts.Tools(ctx) {
		ct, ok := tl.(tool.CallableTool)
		if !ok {
			t.Fatalf("tool %s is not callable", tl.Declaration().Name)
		}
		tools[tl.Declaration().Name] = ct
	}

	// call invokes a tool by name with the given args and returns the raw JSON
	// result string. It records a sub-test pass/fail without aborting the rest.
	call := func(t *testing.T, name string, args map[string]any) string {
		t.Helper()
		ct, ok := tools[name]
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		raw, _ := json.Marshal(args)
		out, err := ct.Call(ctx, raw)
		if err != nil {
			t.Fatalf("%s call failed: %v", name, err)
		}
		b, _ := json.Marshal(out)
		s := string(b)
		preview := s
		if len(preview) > 400 {
			preview = preview[:400] + "...(truncated)"
		}
		t.Logf("%s -> %s", name, preview)
		return s
	}

	const codeRoot = "viking://resources/trpc-agent-go"

	t.Run("viking_health", func(t *testing.T) {
		out := call(t, "viking_health", map[string]any{})
		if !strings.Contains(out, "initialized") && !strings.Contains(out, "ok") {
			t.Errorf("unexpected status payload: %s", out)
		}
	})

	t.Run("viking_browse_ls", func(t *testing.T) {
		call(t, "viking_browse", map[string]any{"uri": "viking://resources"})
	})

	t.Run("viking_browse_glob", func(t *testing.T) {
		call(t, "viking_browse", map[string]any{
			"uri":     codeRoot + "/tool",
			"pattern": "*.go",
		})
	})

	t.Run("viking_find", func(t *testing.T) {
		out := call(t, "viking_find", map[string]any{
			"query": "how does the tool ToolSet interface work",
			"limit": 5,
		})
		if !strings.Contains(out, "viking://") {
			t.Errorf("find returned no uris: %s", out)
		}
	})

	t.Run("viking_search", func(t *testing.T) {
		call(t, "viking_search", map[string]any{
			"query": "session service implementation",
			"limit": 5,
		})
	})

	readTarget := codeRoot + "/tool/toolset.go"
	// abstract (L0) and overview (L1) are generated for directory nodes; leaf
	// files only expose read (L2) content.
	t.Run("viking_read_abstract", func(t *testing.T) {
		call(t, "viking_read", map[string]any{"uri": codeRoot + "/tool", "content_mode": "abstract"})
	})
	t.Run("viking_read_overview", func(t *testing.T) {
		call(t, "viking_read", map[string]any{"uri": codeRoot + "/tool", "content_mode": "overview"})
	})
	t.Run("viking_read_full_paged", func(t *testing.T) {
		out := call(t, "viking_read", map[string]any{
			"uri": readTarget, "content_mode": "read", "offset": 0, "limit": 20, "max_chars": 500,
		})
		if !strings.Contains(out, "\"content\"") {
			t.Errorf("read returned no content field: %s", out)
		}
	})

	t.Run("viking_grep", func(t *testing.T) {
		call(t, "viking_grep", map[string]any{
			"uri":     codeRoot + "/tool",
			"pattern": "ToolSet",
		})
	})

	t.Run("viking_store", func(t *testing.T) {
		out := call(t, "viking_store", map[string]any{
			"content": "Integration smoke test note: trpc-agent-go OpenViking tools verified.",
			"role":    "user",
			"commit":  false,
		})
		if !strings.Contains(out, "session_id") {
			t.Errorf("store returned no session_id: %s", out)
		}
	})

	// add_skill accepts inline text; create one, then forget it to also
	// exercise the destructive path and avoid polluting the corpus.
	var skillURI string
	t.Run("viking_add_skill", func(t *testing.T) {
		skillDoc := "---\n" +
			"name: trpc-smoke-test-skill\n" +
			"description: A throwaway skill created by the trpc-agent-go integration test.\n" +
			"---\n# Smoke Test Skill\nUsed only to verify viking_add_skill and viking_forget.\n"
		out := call(t, "viking_add_skill", map[string]any{
			"data": skillDoc,
			"wait": true,
		})
		skillURI = firstVikingURI(out)
		t.Logf("created skill uri: %q", skillURI)
	})

	t.Run("viking_forget", func(t *testing.T) {
		if skillURI == "" {
			t.Skip("no skill uri captured; skipping forget")
		}
		call(t, "viking_forget", map[string]any{"uri": skillURI, "recursive": true})
	})

	// add_resource requires a public remote source; tolerate network failures
	// in sandboxes without outbound internet.
	t.Run("viking_add_resource", func(t *testing.T) {
		ct := tools["viking_add_resource"]
		raw, _ := json.Marshal(map[string]any{
			"path": "https://raw.githubusercontent.com/volcengine/OpenViking/main/README.md",
			"wait": false,
		})
		out, err := ct.Call(ctx, raw)
		if err != nil {
			t.Logf("add_resource reachable but errored (likely no outbound internet): %v", err)
			return
		}
		b, _ := json.Marshal(out)
		t.Logf("add_resource -> %s", string(b))
	})
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// firstVikingURI extracts the first viking:// URI substring from a JSON string.
func firstVikingURI(s string) string {
	i := strings.Index(s, "viking://")
	if i < 0 {
		return ""
	}
	rest := s[i:]
	end := strings.IndexAny(rest, "\"\\ ,]}")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
