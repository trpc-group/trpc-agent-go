//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pipeline

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/sandbox"
)

func TestSandboxOutputRedactedForReportAndStorage(t *testing.T) {
	items := []sandbox.RunRecord{{
		Command: "go test ./...",
		Stdout:  "api_key=sk-abcdefghijklmnopqrstuvwxyz123456\ntoken: bearer-secret-value",
		Stderr:  "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc password=hunter2",
	}}
	summaries := toSandboxSummaries(items)
	storage := toStorageSandboxRuns(items)
	blob := summaries[0].Stdout + summaries[0].Stderr + storage[0].Stdout + storage[0].Stderr
	for _, secret := range []string{
		"sk-abcdefghijklmnopqrstuvwxyz123456",
		"bearer-secret-value",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc",
		"hunter2",
	} {
		if strings.Contains(blob, secret) {
			t.Fatalf("plaintext secret %q leaked into sandbox summaries/storage", secret)
		}
	}
	if !strings.Contains(blob, "<redacted>") {
		t.Fatal("expected redaction markers")
	}
}

func TestLoadInputRequiresExactlyOneSource(t *testing.T) {
	_, err := loadInput(Options{DiffFile: "a.diff", RepoPath: "/tmp/repo"})
	if err == nil {
		t.Fatal("expected error when both diff-file and repo-path are set")
	}
	_, err = loadInput(Options{})
	if err == nil {
		t.Fatal("expected error when no input source is set")
	}
}
