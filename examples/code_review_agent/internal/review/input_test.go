//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUnifiedDiffTracksNewLineNumbers(t *testing.T) {
	raw := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -8,2 +8,3 @@\n old\n+first\n+second\n"
	got, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 2 || got.Lines[0].Line != 9 || got.Lines[1].Line != 10 {
		t.Fatalf("unexpected lines: %+v", got.Lines)
	}
	if got.Summary.FilesChanged != 1 || got.Summary.GoFiles != 1 || got.Summary.AddedLines != 2 {
		t.Fatalf("unexpected summary: %+v", got.Summary)
	}
}

func TestParseUnifiedDiffRejectsUnsupportedInput(t *testing.T) {
	if _, err := ParseUnifiedDiff("not a diff"); err == nil {
		t.Fatal("expected invalid diff error")
	}
}

func TestParseUnifiedDiffCountsDeletedFile(t *testing.T) {
	raw := "diff --git a/old.go b/old.go\n--- a/old.go\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-package old\n-var value = 1\n"
	got, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.FilesChanged != 1 || got.Summary.DeletedLines != 2 || got.Files[0] != "old.go" {
		t.Fatalf("unexpected deleted-file summary: %+v files=%v", got.Summary, got.Files)
	}
}

func TestFileListRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(list, []byte("../secret.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := diffFromFileList(dir, list); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestLoadInputRequiresOnePrimaryMode(t *testing.T) {
	_, _, err := loadInput(context.Background(), Config{DiffFile: "a", Fixture: "b"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("unexpected error: %v", err)
	}
}
