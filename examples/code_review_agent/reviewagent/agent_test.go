//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package reviewagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// testFiles returns a minimal changed-file set for agent tests.
func testFiles() []review.ChangedFile {
	return []review.ChangedFile{{
		NewPath:     "pkg/service/service.go",
		Language:    "go",
		PackageName: "service",
		Hunks: []review.Hunk{{
			NewStart: 10,
			Lines: []review.DiffLine{
				{Kind: "context", NewLine: 10, Content: "func Do() {"},
				{Kind: "added", NewLine: 11, Content: "	go func() { work() }()"},
				{Kind: "added", NewLine: 12, Content: "}"},
			},
		}},
	}}
}

// TestReviewFakeModelEndToEnd runs the full agent chain with the offline model.
func TestReviewFakeModelEndToEnd(t *testing.T) {
	out, err := Review(context.Background(), Config{
		Mode:    ModeFakeModel,
		TaskID:  "cr-test",
		Timeout: 10 * time.Second,
	}, testFiles())
	if err != nil {
		t.Fatalf("fake-model review failed: %v", err)
	}
	if out.ModelCalls != 1 {
		t.Fatalf("model calls = %d, want 1", out.ModelCalls)
	}
	if out.Summary == "" {
		t.Fatal("fake-model review returned empty summary")
	}
	if len(out.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(out.Findings))
	}
	f := out.Findings[0]
	if f.RuleID != "FAKE001" || f.Source != ModeFakeModel {
		t.Fatalf("unexpected finding: rule=%q source=%q", f.RuleID, f.Source)
	}
	if f.File != "pkg/service/service.go" || f.Line != 11 {
		t.Fatalf("unexpected location: %s:%d", f.File, f.Line)
	}
}

// TestReviewUnsupportedMode verifies unknown modes fail fast.
func TestReviewUnsupportedMode(t *testing.T) {
	if _, err := Review(context.Background(), Config{Mode: "bogus"}, testFiles()); err == nil {
		t.Fatal("expected error for unsupported mode")
	}
}

// TestReviewLLMModeRequiresAPIKey verifies llm mode demands an API key.
func TestReviewLLMModeRequiresAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := Review(context.Background(), Config{Mode: ModeLLM, ModelName: "any"}, testFiles())
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

// TestReviewLLMModeRequiresModelName verifies empty model names fail before a request.
func TestReviewLLMModeRequiresModelName(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	_, err := Review(context.Background(), Config{Mode: ModeLLM}, testFiles())
	if err == nil || !strings.Contains(err.Error(), "model name") {
		t.Fatalf("expected missing model name error, got %v", err)
	}
}

// TestReviewLLMModeRejectsMalformedContent verifies a successful HTTP response
// still fails model review when it violates the JSON contract.
func TestReviewLLMModeRejectsMalformedContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeChatCompletion(t, w, "not a JSON review")
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	out, err := Review(context.Background(), Config{
		Mode: ModeLLM, ModelName: "test-model", TaskID: "malformed", Timeout: time.Second,
	}, testFiles())
	if err == nil {
		t.Fatal("expected malformed model content error")
	}
	if out.ModelCalls != 1 {
		t.Fatalf("model calls = %d, want 1", out.ModelCalls)
	}
}

// TestReviewLLMModeHonorsTimeout verifies a stalled provider is canceled.
func TestReviewLLMModeHonorsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(250 * time.Millisecond)
		writeChatCompletion(t, w, `{"summary":"late response","findings":[]}`)
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	start := time.Now()
	_, err := Review(context.Background(), Config{
		Mode: ModeLLM, ModelName: "test-model", TaskID: "timeout", Timeout: 50 * time.Millisecond,
	}, testFiles())
	if err == nil {
		t.Fatal("expected model timeout error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("model timeout took %v, expected prompt cancellation", elapsed)
	}
}

// TestBuildPromptContainsFileAndLines verifies prompts embed the diff context.
func TestBuildPromptContainsFileAndLines(t *testing.T) {
	prompt := BuildPrompt(testFiles())
	for _, want := range []string{
		"FILE: pkg/service/service.go (package service)",
		"+ 11: \tgo func() { work() }()",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, want := range []string{
		"hypothetical caller misuse are not findings",
		"environment variable or secret manager is not itself a leak",
		"time.Ticker.Stop does not require draining ticker.C",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing %q", want)
		}
	}
}

// TestBuildPromptEnforcesHardByteCap verifies one untrusted line cannot exceed
// the outbound request limit.
func TestBuildPromptEnforcesHardByteCap(t *testing.T) {
	files := testFiles()
	files[0].Hunks[0].Lines[0].Content = strings.Repeat("x", promptByteCap*2)
	prompt := BuildPrompt(files)
	if len(prompt) > promptByteCap {
		t.Fatalf("prompt bytes = %d, cap = %d", len(prompt), promptByteCap)
	}
	if !strings.Contains(prompt, "[diff truncated]") {
		t.Fatal("oversized prompt is missing truncation marker")
	}
}

// writeChatCompletion writes the minimal non-streaming OpenAI response used by
// endpoint behavior tests.
func writeChatCompletion(t *testing.T, w http.ResponseWriter, content string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion", "created": 1,
		"model": "test-model", "choices": []any{map[string]any{
			"index": 0, "message": map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
	}); err != nil {
		t.Errorf("write response: %v", err)
	}
}
