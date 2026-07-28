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

// TestBuildPromptNeverExceedsCap feeds a megabyte-sized diff line and
// asserts the prompt stays within the documented byte cap.
func TestBuildPromptNeverExceedsCap(t *testing.T) {
	huge := strings.Repeat("payload ", 1<<17) // ~1 MiB single line
	files := []review.ChangedFile{{
		NewPath: "big/big.go",
		Hunks: []review.Hunk{{Lines: []review.DiffLine{
			{Kind: "added", NewLine: 1, Content: huge},
			{Kind: "added", NewLine: 2, Content: "never reached"},
		}}},
	}}
	prompt := BuildPrompt(files)
	if len(prompt) > promptByteCap {
		t.Fatalf("prompt length %d exceeds cap %d", len(prompt), promptByteCap)
	}
	if !strings.Contains(prompt, "[diff truncated]") {
		t.Fatalf("oversized prompt missing truncation marker:\n%s", prompt[:200])
	}
	if strings.Contains(prompt, "never reached") {
		t.Fatal("content after the cap leaked into the prompt")
	}
}

// TestReviewRedactsRemoteModelBoundary places secrets in both the line
// content and the file path, then verifies the prompt built inside
// Review and the model echo never carry the raw values.
func TestReviewRedactsRemoteModelBoundary(t *testing.T) {
	files := []review.ChangedFile{{
		NewPath: "cfg/AKIA1234567890ABCDEF.go",
		Hunks: []review.Hunk{{Lines: []review.DiffLine{{
			Kind: "added", NewLine: 1,
			Content: `password = "hunter2-super-secret"`,
		}}}},
	}}
	// The exact pipeline Review uses before the model boundary.
	prompt := BuildPrompt(redactFiles(files))
	for _, raw := range []string{"AKIA1234567890ABCDEF", "hunter2-super-secret"} {
		if strings.Contains(prompt, raw) {
			t.Fatalf("prompt leaks %q:\n%s", raw, prompt)
		}
	}
	if !strings.Contains(prompt, "[REDACTED_SECRET]") {
		t.Fatalf("prompt missing redaction placeholder:\n%s", prompt)
	}
	// End to end: the fake model echoes the first added line back as
	// evidence, so a leak would surface in the finding.
	out, err := Review(context.Background(), Config{
		Mode:    ModeFakeModel,
		TaskID:  "cr-redact",
		Timeout: 10 * time.Second,
	}, files)
	if err != nil {
		t.Fatalf("fake-model review failed: %v", err)
	}
	if len(out.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(out.Findings))
	}
	if strings.Contains(out.Findings[0].Evidence, "hunter2-super-secret") {
		t.Fatalf("model evidence leaks the secret: %q", out.Findings[0].Evidence)
	}
}
