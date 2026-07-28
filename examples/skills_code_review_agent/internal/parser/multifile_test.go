//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package parser_test

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/parser"
)

func TestParseMultiFileDiff(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
index abc..def 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,3 @@
 ctx
-old_foo
+new_foo
diff --git a/bar.go b/bar.go
index 123..456 100644
--- a/bar.go
+++ b/bar.go
@@ -5,3 +5,3 @@
 ctx
-old_bar
+new_bar
`
	files, err := parser.Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d: %+v", len(files), files)
	}
	if files[0].NewPath != "foo.go" || files[1].NewPath != "bar.go" {
		t.Errorf("wrong paths: %q %q", files[0].NewPath, files[1].NewPath)
	}
	if len(files[1].Hunks) == 0 {
		t.Error("bar.go has no hunks - multi-file parsing broken")
	}
}

func TestParseGitQuotedPath(t *testing.T) {
	// Git C-quotes paths with non-ASCII bytes; \303\251 is UTF-8 "é".
	diff := "diff --git \"a/\\303\\251.go\" \"b/\\303\\251.go\"\n" +
		"--- \"a/\\303\\251.go\"\n" +
		"+++ \"b/\\303\\251.go\"\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-old\n" +
		"+new\n"
	files, err := parser.Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].NewPath != "é.go" {
		t.Errorf("quoted path not decoded: got %q, want %q", files[0].NewPath, "é.go")
	}
}

func TestParseConcatenatedDiffWithoutGitHeader(t *testing.T) {
	// Two `diff -u` outputs concatenated without `diff --git`, each header path
	// carrying a tab-separated timestamp.
	diff := "--- a/foo.go\t2024-01-01 12:00:00 +0000\n" +
		"+++ b/foo.go\t2024-01-01 12:00:01 +0000\n" +
		"@@ -1,1 +1,1 @@\n-old\n+new\n" +
		"--- a/bar.go\t2024-01-01 12:00:00 +0000\n" +
		"+++ b/bar.go\t2024-01-01 12:00:02 +0000\n" +
		"@@ -1,1 +1,1 @@\n-x\n+y\n"
	files, err := parser.Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d: %+v", len(files), files)
	}
	// Timestamps must be stripped, else the .go suffix is lost and the file skipped.
	if files[0].NewPath != "foo.go" || files[1].NewPath != "bar.go" {
		t.Errorf("timestamp not stripped: %q %q", files[0].NewPath, files[1].NewPath)
	}
	// The second file's header must not be swallowed as content of the first hunk.
	if len(files[0].Hunks) != 1 || len(files[1].Hunks) != 1 {
		t.Fatalf("hunks not flushed on new header: %d %d", len(files[0].Hunks), len(files[1].Hunks))
	}
	for _, l := range files[0].Hunks[0].Lines {
		if strings.Contains(l, "bar.go") {
			t.Errorf("first hunk swallowed the next file header: %q", l)
		}
	}
}

// A removed line whose content begins with "-- " followed by an added "++ " line
// (legal inside a Go raw string) encodes as "--- "/"+++ ". While the hunk still
// has declared lines to consume, these must stay hunk content, not a new header.
func TestParseHeaderLikeContentInsideHunk(t *testing.T) {
	diff := "--- a/q.go\n" +
		"+++ b/q.go\n" +
		"@@ -1,2 +1,2 @@\n" +
		" s := `x`\n" +
		"--- foo\n" +
		"+++ bar\n"
	files, err := parser.Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("header-like hunk content split into %d files, want 1", len(files))
	}
	if files[0].NewPath != "q.go" {
		t.Errorf("wrong file: got %q, want q.go", files[0].NewPath)
	}
	if len(files[0].Hunks) != 1 || len(files[0].Hunks[0].Lines) != 3 {
		t.Fatalf("hunk body not preserved: %+v", files[0].Hunks)
	}
}
