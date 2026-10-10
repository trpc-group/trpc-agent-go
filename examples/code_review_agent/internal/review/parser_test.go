//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUnifiedDiffExtractsFileAndHunk(t *testing.T) {
	diff := "" +
		"diff --git a/main.go b/main.go\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/main.go\n" +
		"+++ b/main.go\n" +
		"@@ -1 +1,2 @@\n" +
		" package main\n" +
		"+func main() {}\n"

	parsed, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff returned error: %v", err)
	}
	if len(parsed.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(parsed.Files))
	}
	if parsed.Files[0].Path != "main.go" {
		t.Fatalf("expected file main.go, got %q", parsed.Files[0].Path)
	}
	if len(parsed.Files[0].Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(parsed.Files[0].Hunks))
	}
	hunk := parsed.Files[0].Hunks[0]
	if hunk.OldLines != 1 || hunk.NewLines != 2 {
		t.Fatalf("hunk line counts = old:%d new:%d, want old:1 new:2", hunk.OldLines, hunk.NewLines)
	}
}

func TestParseUnifiedDiffHandlesLongAddedLine(t *testing.T) {
	longLine := strings.Repeat("x", 128*1024)
	diff := "" +
		"diff --git a/long.go b/long.go\n" +
		"--- a/long.go\n" +
		"+++ b/long.go\n" +
		"@@ -1,1 +1,2 @@\n" +
		" package main\n" +
		"+const payload = \"" + longLine + "\"\n"

	parsed, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff returned error for long line: %v", err)
	}
	if got := parsed.Files[0].Hunks[0].Lines[1].NewLine; got != 2 {
		t.Fatalf("expected long added line at new line 2, got %d", got)
	}
}

func TestParseUnifiedDiffTracksNewLineNumbersAcrossMixedHunk(t *testing.T) {
	diff := "" +
		"diff --git a/service.go b/service.go\n" +
		"--- a/service.go\n" +
		"+++ b/service.go\n" +
		"@@ -10,4 +10,5 @@ func handle() {\n" +
		" \tsetup()\n" +
		"-\toldCall()\n" +
		"+\tfirstNewCall()\n" +
		" \tmiddle()\n" +
		"+\tsecondNewCall()\n" +
		" \tfinish()\n"

	parsed, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff returned error: %v", err)
	}
	var added []int
	for _, line := range parsed.Files[0].Hunks[0].Lines {
		if line.Kind == "add" {
			added = append(added, line.NewLine)
		}
	}
	if want := []int{11, 13}; len(added) != len(want) || added[0] != want[0] || added[1] != want[1] {
		t.Fatalf("added line numbers = %v, want %v", added, want)
	}
}

func TestParseUnifiedDiffDecodesQuotedPathsAndInfersLanguage(t *testing.T) {
	diff := "diff --git \"a/docs/\\346\\261\\211\\t.md\" \"b/docs/\\346\\261\\211\\t.md\"\n" +
		"--- \"a/docs/\\346\\261\\211\\t.md\"\n" +
		"+++ \"b/docs/\\346\\261\\211\\t.md\"\n" +
		"@@ -0,0 +1 @@\n" +
		"+TODO(example): keep docs aligned\n"
	parsed, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff returned error: %v", err)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Path != "docs/汉\t.md" || parsed.Files[0].Language != "markdown" {
		t.Fatalf("quoted path parsing = %+v, want decoded markdown file", parsed.Files)
	}
}

func TestParseUnifiedDiffStripsTraditionalHeaderTimestamps(t *testing.T) {
	diff, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "timestamped-unified.diff"))
	if err != nil {
		t.Fatalf("read timestamped unified diff fixture: %v", err)
	}
	parsed, err := ParseUnifiedDiff(string(diff))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff returned error: %v", err)
	}
	if len(parsed.Files) != 1 {
		t.Fatalf("files = %+v, want one file", parsed.Files)
	}
	file := parsed.Files[0]
	if file.Path != "service.go" || file.Language != "go" {
		t.Fatalf("timestamped file = %+v, want service.go detected as Go", file)
	}
	if len(file.Hunks) != 1 || len(file.Hunks[0].CandidateLines) != 1 || file.Hunks[0].CandidateLines[0] != 3 {
		t.Fatalf("timestamped hunk = %+v, want candidate service.go:3", file.Hunks)
	}
}

func TestParseUnifiedDiffPreservesRepositoryPathsStartingWithSidePrefixes(t *testing.T) {
	for _, path := range []string{"a/handler.go", "b/handler.go"} {
		diff := "diff --git a/" + path + " b/" + path + "\n" +
			"--- a/" + path + "\n+++ b/" + path + "\n" +
			"@@ -0,0 +1 @@\n+package main\n"
		parsed, err := ParseUnifiedDiff(diff)
		if err != nil {
			t.Fatalf("ParseUnifiedDiff(%q) returned error: %v", path, err)
		}
		if got := parsed.Files[0].Path; got != path {
			t.Errorf("path = %q, want %q", got, path)
		}
	}
}

func TestParseUnifiedDiffRejectsHunksWithoutTargetFileHeader(t *testing.T) {
	tests := []struct {
		name string
		diff string
	}{
		{
			name: "leading hunk",
			diff: "@@ -1 +1 @@\n+line\n",
		},
		{
			name: "missing target header",
			diff: "diff --git a/main.go b/main.go\n--- a/main.go\n@@ -1 +1 @@\n+line\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseUnifiedDiff(tt.diff)
			if err == nil || !strings.Contains(err.Error(), "target file header") {
				t.Fatalf("ParseUnifiedDiff error = %v, want target file header error", err)
			}
		})
	}
}

func TestParseUnifiedDiffIgnoresNoNewlineMarkers(t *testing.T) {
	diff := "diff --git a/main.go b/main.go\n" +
		"--- a/main.go\n" +
		"+++ b/main.go\n" +
		"@@ -4 +4 @@\n" +
		"-old value\n" +
		"\\ No newline at end of file\n" +
		"+new value\n" +
		"\\ No newline at end of file\n"

	parsed, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff returned error: %v", err)
	}
	hunk := parsed.Files[0].Hunks[0]
	if len(hunk.Lines) != 2 {
		t.Fatalf("parsed lines = %d, want 2", len(hunk.Lines))
	}
	if got := hunk.CandidateLines; len(got) != 1 || got[0] != 4 {
		t.Fatalf("candidate lines = %v, want [4]", got)
	}
	if got := hunk.Lines[1].NewLine; got != 4 {
		t.Fatalf("added line number = %d, want 4", got)
	}
}

func TestParseUnifiedDiffParsesOmittedAndZeroHunkLineCounts(t *testing.T) {
	diff := "diff --git a/main.go b/main.go\n" +
		"--- a/main.go\n" +
		"+++ b/main.go\n" +
		"@@ -1 +1,0 @@\n" +
		"-removed\n"

	parsed, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff returned error: %v", err)
	}
	hunk := parsed.Files[0].Hunks[0]
	if hunk.OldLines != 1 || hunk.NewLines != 0 {
		t.Fatalf("hunk line counts = old:%d new:%d, want old:1 new:0", hunk.OldLines, hunk.NewLines)
	}
}

func TestParseUnifiedDiffSeparatesHeaderOnlyMultiFileDiff(t *testing.T) {
	diff := "--- a/first.go\n" +
		"+++ b/first.go\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+first\n" +
		"--- a/second.go\n" +
		"+++ b/second.go\n" +
		"@@ -5 +5 @@\n" +
		"-old\n" +
		"+second\n"
	parsed, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff returned error: %v", err)
	}
	if len(parsed.Files) != 2 {
		t.Fatalf("files = %+v, want two files", parsed.Files)
	}
	for index, want := range []struct {
		path string
		line int
	}{{"first.go", 1}, {"second.go", 5}} {
		file := parsed.Files[index]
		if file.Path != want.path || len(file.Hunks) != 1 || len(file.Hunks[0].CandidateLines) != 1 || file.Hunks[0].CandidateLines[0] != want.line {
			t.Fatalf("file %d = %+v, want path %q candidate line %d", index, file, want.path, want.line)
		}
	}
}

func TestParseUnifiedDiffTreatsHeaderLikeContentAsAddedLines(t *testing.T) {
	diff := "--- a/notes.md\n" +
		"+++ b/notes.md\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+++ not-a-file-header\n" +
		"+TODO(follow-up): retain line numbers\n"
	parsed, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff returned error: %v", err)
	}
	hunk := parsed.Files[0].Hunks[0]
	if got := hunk.CandidateLines; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("candidate lines = %v, want [1 2]", got)
	}
	if hunk.Lines[0].Text != "++ not-a-file-header" {
		t.Fatalf("header-like added content = %q", hunk.Lines[0].Text)
	}
}

func TestParseUnifiedDiffRejectsIncompleteAndNonDiffInput(t *testing.T) {
	tests := []string{
		"this is not a unified diff\n",
		"--- a/sample.go\n+++ b/sample.go\n@@ -0,0 +1,2 @@\n+package sample\n",
	}
	for _, diff := range tests {
		if _, err := ParseUnifiedDiff(diff); err == nil {
			t.Fatalf("ParseUnifiedDiff(%q) succeeded, want validation error", diff)
		}
	}
}
