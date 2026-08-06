//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewinput

import (
	"bytes"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
)

func TestParseReviewDiffExtractsFilesHunksAndCandidateLines(t *testing.T) {
	raw := []byte("diff --git a/internal/foo.go b/internal/foo.go\n" +
		"--- a/internal/foo.go\n" +
		"+++ b/internal/foo.go\n" +
		"@@ -10,2 +10,3 @@ func f() {\n" +
		" old()\n" +
		"-removed()\n" +
		"+apiKey := \"sk-abcdefghijklmnopqrst\"\n" +
		"+added()\n")

	parsed, masked, _, err := parseReviewDiff(raw, nil, true, redact.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.ChangedFiles) != 1 || parsed.ChangedFiles[0].Path != "internal/foo.go" {
		t.Fatalf("changed files = %#v", parsed.ChangedFiles)
	}
	if !parsed.ChangedFiles[0].HasCompleteContext {
		t.Fatal("repo-backed file should have complete context")
	}
	if len(parsed.ChangedHunks) != 1 {
		t.Fatalf("hunk count = %d, want 1", len(parsed.ChangedHunks))
	}
	wantLines := []int{11, 12}
	for i, want := range wantLines {
		if parsed.ChangedHunks[0].CandidateLines[i] != want {
			t.Fatalf("candidate lines = %#v, want %#v", parsed.ChangedHunks[0].CandidateLines, wantLines)
		}
	}
	if len(parsed.SecretSignals) != 1 || parsed.SecretSignals[0].Line != 11 {
		t.Fatalf("secret signals = %#v", parsed.SecretSignals)
	}
	if strings.Contains(string(masked), "sk-abcdefghijklmnopqrst") || strings.Contains(parsed.ChangedHunks[0].Body, "sk-abcdefghijklmnopqrst") {
		t.Fatal("parsed or artifact diff contains plaintext secret")
	}
}

func TestParseReviewDiffMarksDeletedFileAsUnavailableInRepoSnapshot(t *testing.T) {
	raw := []byte("diff --git a/obsolete.go b/obsolete.go\n" +
		"deleted file mode 100644\n" +
		"--- a/obsolete.go\n" +
		"+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n" +
		"-package obsolete\n" +
		"-const value = 1\n")

	parsed, _, _, err := parseReviewDiff(raw, nil, true, redact.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.ChangedFiles) != 1 {
		t.Fatalf("changed files = %#v, want one deleted file", parsed.ChangedFiles)
	}
	file := parsed.ChangedFiles[0]
	if file.Status != "deleted" {
		t.Fatalf("status = %q, want deleted", file.Status)
	}
	if file.HasCompleteContext {
		t.Fatal("deleted file should not be marked available in the post-change repository snapshot")
	}
}

func TestParseReviewDiffAppliesExactPathScope(t *testing.T) {
	raw := []byte("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-package a\n+package aa\n" +
		"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-package b\n+package bb\n")
	parsed, masked, scoped, err := parseReviewDiff(raw, []string{"b.go"}, false, redact.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.ChangedFiles) != 1 || parsed.ChangedFiles[0].Path != "b.go" {
		t.Fatalf("changed files = %#v, want b.go", parsed.ChangedFiles)
	}
	if strings.Contains(string(masked), "a/a.go") || strings.Contains(string(scoped), "a/a.go") {
		t.Fatalf("path-scoped diff still contains a.go: %s", scoped)
	}
}

func TestParseReviewDiffDirectoryScopeIncludesDescendants(t *testing.T) {
	raw := []byte("diff --git a/internal/a.go b/internal/a.go\n" +
		"--- a/internal/a.go\n+++ b/internal/a.go\n@@ -1 +1 @@\n-package a\n+package aa\n" +
		"diff --git a/root.go b/root.go\n--- a/root.go\n+++ b/root.go\n@@ -1 +1 @@\n-package root\n+package changed\n")
	parsed, masked, _, err := parseReviewDiff(raw, []string{"internal"}, true, redact.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.ChangedFiles) != 1 || parsed.ChangedFiles[0].Path != "internal/a.go" {
		t.Fatalf("directory-scoped changed files = %#v", parsed.ChangedFiles)
	}
	if bytes.Contains(masked, []byte("root.go")) {
		t.Fatalf("directory-scoped diff contains an out-of-scope file:\n%s", masked)
	}
}

func TestParseReviewDiffRejectsPartiallyUnmatchedPathScope(t *testing.T) {
	raw := []byte("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-package a\n+package aa\n")
	_, _, _, err := parseReviewDiff(raw, []string{"a.go", "missing.go"}, false, redact.New())
	if err == nil || !strings.Contains(err.Error(), "missing.go") {
		t.Fatalf("parseReviewDiff error = %v, want missing path error", err)
	}
}

func TestParseReviewDiffDetectsPrivateKeyAcrossAddedLines(t *testing.T) {
	raw := []byte("diff --git a/key.pem b/key.pem\n--- /dev/null\n+++ b/key.pem\n@@ -0,0 +1,3 @@\n+-----BEGIN PRIVATE KEY-----\n+abc123-private-material\n+-----END PRIVATE KEY-----\n")
	parsed, masked, _, err := parseReviewDiff(raw, nil, false, redact.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.SecretSignals) != 1 || parsed.SecretSignals[0].Line != 1 {
		t.Fatalf("secret signals = %#v, want private key at line 1", parsed.SecretSignals)
	}
	if strings.Contains(string(masked), "abc123-private-material") || strings.Contains(parsed.ChangedHunks[0].Body, "abc123-private-material") {
		t.Fatal("masked diff or hunk contains private key material")
	}
}

func TestNormalizeDiffPathRejectsEscape(t *testing.T) {
	for _, input := range []string{"../secret", "a/../../secret", "/etc/passwd"} {
		if _, err := normalizeDiffPath(input); err == nil {
			t.Fatalf("normalizeDiffPath(%q) unexpectedly succeeded", input)
		}
	}
}
