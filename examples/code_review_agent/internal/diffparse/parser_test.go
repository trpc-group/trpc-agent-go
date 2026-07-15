//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package diffparse

import (
	"os"
	"strings"
	"testing"
)

func TestParseUnifiedDiff(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/fixtures/security_secret.diff")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	files, err := Parse(string(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	file := files[0]
	if file.NewPath != "pkg/config.go" {
		t.Fatalf("NewPath = %q, want pkg/config.go", file.NewPath)
	}
	if !file.IsNew {
		t.Fatalf("IsNew = false, want true")
	}
	if file.PackageDir != "pkg" {
		t.Fatalf("PackageDir = %q, want pkg", file.PackageDir)
	}
	if got := file.Hunks[0].Lines[3].NewLine; got != 4 {
		t.Fatalf("secret line NewLine = %d, want 4", got)
	}
	if got := file.Hunks[0].Lines[3].Content; got != "\treturn \"api_key=sk-live1234567890abcdef\"" {
		t.Fatalf("secret line Content = %q", got)
	}
}

func TestParseRawUnifiedDiff(t *testing.T) {
	raw := `--- pkg/config.go	2026-07-06
+++ pkg/config.go	2026-07-06
@@ -1,2 +1,3 @@
 package pkg
+const token = "token=supersecretvalue"
 func ok() {}
`
	files, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	file := files[0]
	if file.OldPath != "pkg/config.go" || file.NewPath != "pkg/config.go" {
		t.Fatalf("paths = %q/%q, want pkg/config.go/pkg/config.go", file.OldPath, file.NewPath)
	}
	if file.PackageDir != "pkg" {
		t.Fatalf("PackageDir = %q, want pkg", file.PackageDir)
	}
	if got := file.Hunks[0].Lines[1].Content; got != `const token = "token=supersecretvalue"` {
		t.Fatalf("added content = %q", got)
	}
}

func TestParseRawUnifiedNewFile(t *testing.T) {
	raw := `--- /dev/null
+++ pkg/new.go
@@ -0,0 +1,2 @@
+package pkg
+const answer = 42
`
	files, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	if !files[0].IsNew {
		t.Fatalf("IsNew = false, want true")
	}
	if files[0].NewPath != "pkg/new.go" {
		t.Fatalf("NewPath = %q, want pkg/new.go", files[0].NewPath)
	}
}

func TestParseMultipleRawUnifiedDiffFiles(t *testing.T) {
	raw := `--- pkg/a.go
+++ pkg/a.go
@@ -1 +1,2 @@
 package pkg
+const a = true
--- pkg/b.go
+++ pkg/b.go
@@ -1 +1,2 @@
 package pkg
+const b = true
`
	files, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
	if files[0].NewPath != "pkg/a.go" || files[1].NewPath != "pkg/b.go" {
		t.Fatalf("paths = %q/%q, want pkg/a.go/pkg/b.go", files[0].NewPath, files[1].NewPath)
	}
}

func TestParseHunkLinesThatLookLikeFileHeaders(t *testing.T) {
	raw := `--- pkg/config.go
+++ pkg/config.go
@@ -1,3 +1,3 @@
 package pkg
--- old marker
+++ new marker
 func ok() {}
--- pkg/next.go
+++ pkg/next.go
@@ -1 +1,2 @@
 package pkg
+const next = true
`
	files, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
	firstHunkLines := files[0].Hunks[0].Lines
	if len(firstHunkLines) != 4 {
		t.Fatalf("len(firstHunkLines) = %d, want 4", len(firstHunkLines))
	}
	if got := firstHunkLines[1]; got.Kind != "delete" || got.OldLine != 2 || got.Content != "-- old marker" {
		t.Fatalf("delete line = %#v, want old line 2 content %q", got, "-- old marker")
	}
	if got := firstHunkLines[2]; got.Kind != "add" || got.NewLine != 2 || got.Content != "++ new marker" {
		t.Fatalf("add line = %#v, want new line 2 content %q", got, "++ new marker")
	}
	if files[1].NewPath != "pkg/next.go" {
		t.Fatalf("second NewPath = %q, want pkg/next.go", files[1].NewPath)
	}
}

func TestParseIgnoresBlankSeparatorsInsideHunk(t *testing.T) {
	raw := `--- pkg/config.go
+++ pkg/config.go
@@ -1,2 +1,3 @@
 package pkg

+const token = "token=supersecretvalue"
 func ok() {}
`
	files, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	if got := files[0].Hunks[0].Lines[1].NewLine; got != 2 {
		t.Fatalf("added line NewLine = %d, want 2", got)
	}
	if got := files[0].Hunks[0].Lines[1].Content; got != `const token = "token=supersecretvalue"` {
		t.Fatalf("added line Content = %q", got)
	}
}

func TestParseGitQuotedRenamePaths(t *testing.T) {
	raw := "diff --git \"a/pkg/my file\\t\\346\\226\\207.go\" \"b/pkg/new file\\t\\346\\226\\207.go\"\n" +
		"similarity index 80%\n" +
		"rename from pkg/my file\\t\\346\\226\\207.go\n" +
		"rename to pkg/new file\\t\\346\\226\\207.go\n" +
		"--- \"a/pkg/my file\\t\\346\\226\\207.go\"\n" +
		"+++ \"b/pkg/new file\\t\\346\\226\\207.go\"\n" +
		"@@ -1 +1 @@\n" +
		"-package old\n" +
		"+package new\n"
	files, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	wantOld := "pkg/my file\t文.go"
	wantNew := "pkg/new file\t文.go"
	if files[0].OldPath != wantOld || files[0].NewPath != wantNew {
		t.Fatalf("paths = %q/%q, want %q/%q", files[0].OldPath, files[0].NewPath, wantOld, wantNew)
	}
	if files[0].PackageDir != "pkg" {
		t.Fatalf("PackageDir = %q, want pkg", files[0].PackageDir)
	}

	decoded, _, err := parseGitPathToken("\"a/pkg/dir\\\\name.go\"")
	if err != nil {
		t.Fatalf("parseGitPathToken() error = %v", err)
	}
	if decoded != "a/pkg/dir\\name.go" {
		t.Fatalf("decoded backslash path = %q, want %q", decoded, "a/pkg/dir\\name.go")
	}
}

func TestParseRejectsMalformedGitPathHeaders(t *testing.T) {
	for name, raw := range map[string]string{
		"one path":    "diff --git a/pkg/a.go\n",
		"three paths": "diff --git a/pkg/a.go b/pkg/a.go c/pkg/a.go\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(raw)
			if err == nil {
				t.Fatal("Parse() error = nil, want malformed path header error")
			}
			if !strings.Contains(err.Error(), "expected exactly 2 paths") {
				t.Fatalf("Parse() error = %q, want exact path count error", err)
			}
		})
	}
}

func TestParsePropagatesMalformedQuotedFileHeader(t *testing.T) {
	raw := "--- a/pkg/a.go\n+++ \"b/pkg/bad\\q.go\"\n@@ -1 +1 @@\n package pkg\n"
	_, err := Parse(raw)
	if err == nil {
		t.Fatal("Parse() error = nil, want quoted path decoding error")
	}
	if !strings.Contains(err.Error(), "parse new path header") || !strings.Contains(err.Error(), "unsupported Git path escape") {
		t.Fatalf("Parse() error = %q, want wrapped path decoding error", err)
	}
}
