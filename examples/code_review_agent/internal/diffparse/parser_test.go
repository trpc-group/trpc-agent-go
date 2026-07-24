//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package diffparse

import (
	"strings"
	"testing"
)

// TestParseMultiFileNoHunkBleed verifies that hunks do not bleed across
// file boundaries: each file retains its own hunks.
func TestParseMultiFileNoHunkBleed(t *testing.T) {
	input := "diff --git a/foo.go b/foo.go\n" +
		"index 123..456 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-old\n" +
		"+new1\n" +
		"diff --git a/bar.go b/bar.go\n" +
		"--- a/bar.go\n" +
		"+++ b/bar.go\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-old2\n" +
		"+new2\n"
	d, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(d.Files); got != 2 {
		t.Fatalf("expected 2 files, got %d", got)
	}
	assertPaths(t, &d.Files[0], "foo.go", "foo.go")
	assertPaths(t, &d.Files[1], "bar.go", "bar.go")
	assertHunkCount(t, &d.Files[0], 1)
	assertHunkCount(t, &d.Files[1], 1)
	foo := d.Files[0].AddedLines()
	if len(foo) != 1 || foo[0].Content != "new1" {
		t.Errorf("file0 added = %+v, want [new1]", foo)
	}
	bar := d.Files[1].AddedLines()
	if len(bar) != 1 || bar[0].Content != "new2" {
		t.Errorf("file1 added = %+v, want [new2]", bar)
	}
}

// TestParseLineNumbersAcrossContext verifies that AddedLinesNumbered
// returns correct new-file line numbers across context/removed lines.
// For "@@ -1,3 +1,5 @@" with: context / +added / -removed / +added2
// the counter starts at NewStart=1: context→1, added→2, removed→no
// advance, added2→3. So AddedLinesNumbered returns [2, 3].
func TestParseLineNumbersAcrossContext(t *testing.T) {
	input := "diff --git a/foo b/foo\n" +
		"--- a/foo\n" +
		"+++ b/foo\n" +
		"@@ -1,3 +1,5 @@\n" +
		" context\n" +
		"+added\n" +
		"-removed\n" +
		"+added2\n"
	d, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(d.Files))
	}
	got := d.Files[0].AddedLinesNumbered()
	if len(got) != 2 {
		t.Fatalf("expected 2 added lines, got %d", len(got))
	}
	wantLines := []int{2, 3}
	wantContent := []string{"added", "added2"}
	for i, w := range wantLines {
		if got[i].NewFileLine != w {
			t.Errorf("added[%d] NewFileLine = %d, want %d", i, got[i].NewFileLine, w)
		}
		if got[i].Content != wantContent[i] {
			t.Errorf("added[%d] Content = %q, want %q", i, got[i].Content, wantContent[i])
		}
	}
}

// TestParseSuperLongLine verifies that bufio.Reader handles lines
// exceeding the 64KB Scanner token cap.
func TestParseSuperLongLine(t *testing.T) {
	input := "diff --git a/big b/big\n" +
		"--- a/big\n" +
		"+++ b/big\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-short\n" +
		"+" + strings.Repeat("x", 100*1024) + "\n"
	d, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.Files) != 1 || len(d.Files[0].Hunks) != 1 {
		t.Fatalf("expected 1 file with 1 hunk, got %+v", d)
	}
	added := d.Files[0].AddedLines()
	if len(added) != 1 {
		t.Fatalf("expected 1 added line, got %d", len(added))
	}
	want := 100 * 1024
	if len(added[0].Content) != want {
		t.Errorf("added content length = %d, want %d", len(added[0].Content), want)
	}
	if added[0].NewFileLine != 1 {
		t.Errorf("added NewFileLine = %d, want 1", added[0].NewFileLine)
	}
}

// TestParseHeadersInsideHunkAsContent verifies that lines starting with
// "--- " or "+++ " inside an active hunk are treated as hunk content
// (removed/added lines), not as file headers.
func TestParseHeadersInsideHunkAsContent(t *testing.T) {
	input := "diff --git a/foo b/foo\n" +
		"--- a/foo\n" +
		"+++ b/foo\n" +
		"@@ -1,3 +1,3 @@\n" +
		" context\n" +
		"--- old content\n" +
		"+++ new content\n" +
		" context2\n"
	d, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("expected 1 file (no spurious split), got %d", len(d.Files))
	}
	hunks := d.Files[0].Hunks
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	lines := hunks[0].Lines
	if len(lines) != 4 {
		t.Fatalf("expected 4 hunk lines, got %d", len(lines))
	}
	if lines[1].Kind != LineRemoved || lines[1].Content != "-- old content" {
		t.Errorf("line[1] = {Kind:%d, %q}, want {Removed, \"-- old content\"}", lines[1].Kind, lines[1].Content)
	}
	if lines[2].Kind != LineAdded || lines[2].Content != "++ new content" {
		t.Errorf("line[2] = {Kind:%d, %q}, want {Added, \"++ new content\"}", lines[2].Kind, lines[2].Content)
	}
	num := d.Files[0].AddedLinesNumbered()
	if len(num) != 1 || num[0].NewFileLine != 2 || num[0].Content != "++ new content" {
		t.Errorf("AddedLinesNumbered = %+v, want [{2, \"++ new content\"}]", num)
	}
}

// TestParseEmptyInput verifies that an empty input produces an empty
// FileDiff with no error.
func TestParseEmptyInput(t *testing.T) {
	d, err := Parse(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("expected non-nil FileDiff")
	}
	if len(d.Files) != 0 {
		t.Errorf("expected 0 files, got %d", len(d.Files))
	}
}

// TestParseBlankSeparatorsInsideHunk verifies that bare empty lines
// inside a hunk are ignored without breaking parsing.
func TestParseBlankSeparatorsInsideHunk(t *testing.T) {
	input := "diff --git a/foo b/foo\n" +
		"--- a/foo\n" +
		"+++ b/foo\n" +
		"@@ -1,2 +1,2 @@\n" +
		" context\n" +
		"\n" +
		"-removed\n" +
		"\n" +
		"+added\n"
	d, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.Files) != 1 || len(d.Files[0].Hunks) != 1 {
		t.Fatalf("expected 1 file with 1 hunk, got %+v", d)
	}
	lines := d.Files[0].Hunks[0].Lines
	if len(lines) != 3 {
		t.Fatalf("expected 3 hunk lines (blank separators ignored), got %d", len(lines))
	}
	num := d.Files[0].AddedLinesNumbered()
	if len(num) != 1 || num[0].NewFileLine != 2 || num[0].Content != "added" {
		t.Errorf("AddedLinesNumbered = %+v, want [{2, \"added\"}]", num)
	}
}

// TestHunkAddedLinesNumberedRecompute verifies the per-hunk numbering
// algorithm directly, independent of file-level aggregation.
func TestHunkAddedLinesNumberedRecompute(t *testing.T) {
	h := Hunk{
		NewStart: 10,
		Lines: []HunkLine{
			{Kind: LineContext, Content: "ctx"},
			{Kind: LineAdded, Content: "a1"},
			{Kind: LineRemoved, Content: "r"},
			{Kind: LineAdded, Content: "a2"},
			{Kind: LineNoNewline, Content: " No newline at end of file"},
			{Kind: LineAdded, Content: "a3"},
		},
	}
	got := h.AddedLinesNumbered()
	want := []struct {
		content string
		line    int
	}{
		{"a1", 11},
		{"a2", 12},
		{"a3", 13},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d added lines, got %d (%+v)", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i].Content != w.content || got[i].NewFileLine != w.line {
			t.Errorf("got[%d] = {Content:%q, NewFileLine:%d}, want {%q, %d}",
				i, got[i].Content, got[i].NewFileLine, w.content, w.line)
		}
	}
}

// assertPaths checks that a DiffFile has the expected old and new paths.
func assertPaths(t *testing.T, f *DiffFile, oldPath, newPath string) {
	t.Helper()
	if f.OldPath != oldPath || f.NewPath != newPath {
		t.Errorf("paths = {%q, %q}, want {%q, %q}", f.OldPath, f.NewPath, oldPath, newPath)
	}
}

// assertHunkCount checks that a DiffFile has the expected number of hunks.
func assertHunkCount(t *testing.T, f *DiffFile, want int) {
	t.Helper()
	if got := len(f.Hunks); got != want {
		t.Errorf("hunks = %d, want %d", got, want)
	}
}
