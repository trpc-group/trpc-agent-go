//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package reviewagent

import (
	"context"
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
}
