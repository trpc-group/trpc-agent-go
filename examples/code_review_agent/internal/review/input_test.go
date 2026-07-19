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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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

func TestParseUnifiedDiffSupportsSpacesAndTracksDeletion(t *testing.T) {
	raw := "diff --git a/pkg/old name.go b/pkg/old name.go\n--- a/pkg/old name.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-package pkg\n" +
		"diff --git a/pkg/new name.go b/pkg/new name.go\n--- /dev/null\n+++ b/pkg/new name.go\n@@ -0,0 +1 @@\n+package pkg\n"
	got, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 2 || got.Files[0] != "pkg/new name.go" || got.Files[1] != "pkg/old name.go" {
		t.Fatalf("unexpected files: %#v", got.Files)
	}
	if got.Statuses["pkg/old name.go"] != fileDeleted || got.Statuses["pkg/new name.go"] != fileAdded {
		t.Fatalf("unexpected statuses: %#v", got.Statuses)
	}
}

func TestParseUnifiedDiffTracksRenameAndBinaryChanges(t *testing.T) {
	raw := "diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go\n" +
		"diff --git a/assets/logo.bin b/assets/logo.bin\nindex 1..2 100644\nBinary files a/assets/logo.bin and b/assets/logo.bin differ\n"
	got, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 2 || got.Files[0] != "assets/logo.bin" || got.Files[1] != "new.go" {
		t.Fatalf("rename or binary change disappeared: %#v", got.Files)
	}
}

func TestDiffHeaderTargetSupportsQuotedPaths(t *testing.T) {
	if got := diffHeaderTarget(`diff --git "a/pkg/old name.go" "b/pkg/new name.go"`); got != "pkg/new name.go" {
		t.Fatalf("quoted header target = %q", got)
	}
}

func TestFileLinesCountsEmptyAndTerminatedFiles(t *testing.T) {
	for input, want := range map[string]int{"": 0, "one\n": 1, "one": 1, "one\n\n": 2} {
		if got := len(fileLines(input)); got != want {
			t.Errorf("fileLines(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestBoundedReadersRejectOversizedStreams(t *testing.T) {
	if _, err := readBoundedReader(strings.NewReader(strings.Repeat("x", maxInputBytes+1)), "test stream"); err == nil {
		t.Fatal("oversized stream was accepted")
	}
	if _, err := readBoundedReader(errorReader{}, "broken stream"); err == nil {
		t.Fatal("reader error was ignored")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestCommandOutputBounded(t *testing.T) {
	command := func(mode string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestCommandOutputHelper", "--", mode)
		cmd.Env = append(os.Environ(), "GO_WANT_COMMAND_OUTPUT_HELPER=1")
		return cmd
	}
	if got, err := commandOutputBounded(command("ok"), "helper"); err != nil || !strings.HasPrefix(got, "ok") {
		t.Fatalf("unexpected helper output %q: %v", got, err)
	}
	if _, err := commandOutputBounded(command("fail"), "helper"); err == nil {
		t.Fatal("command failure was ignored")
	}
	if _, err := commandOutputBounded(command("large"), "helper"); err == nil {
		t.Fatal("oversized command output was accepted")
	}
}

func TestLoadInputTrimsModeValues(t *testing.T) {
	dir := t.TempDir()
	diff := filepath.Join(dir, "change.diff")
	if err := os.WriteFile(diff, []byte("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n+package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, mode, err := loadInput(context.Background(), Config{DiffFile: "  " + diff + "  "}, dir); err != nil || mode != "diff_file" {
		t.Fatalf("trimmed diff path failed: mode=%q err=%v", mode, err)
	}
}

func TestCleanDiffPathHandlesQuotedAndUnsafePaths(t *testing.T) {
	if got := cleanDiffPath(`"b/pkg/a b.go"`); got != "pkg/a b.go" {
		t.Fatalf("quoted path = %q", got)
	}
	if got := cleanDiffPath("b/pkg/a.go\t2026-01-01"); got != "pkg/a.go" {
		t.Fatalf("timestamped path = %q", got)
	}
	for _, path := range []string{"/dev/null", "../escape.go", "/absolute.go"} {
		if got := cleanDiffPath(path); got != "" {
			t.Fatalf("unsafe path %q became %q", path, got)
		}
	}
}

func TestCommandOutputHelper(t *testing.T) {
	if os.Getenv("GO_WANT_COMMAND_OUTPUT_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "ok":
		_, _ = io.WriteString(os.Stdout, "ok")
	case "large":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", maxInputBytes+1))
	default:
		os.Exit(2)
	}
}
