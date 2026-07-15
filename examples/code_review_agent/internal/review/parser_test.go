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
	"strings"
	"testing"
)

func TestParseUnifiedDiffExtractsFileAndHunk(t *testing.T) {
	diff := "" +
		"diff --git a/main.go b/main.go\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/main.go\n" +
		"+++ b/main.go\n" +
		"@@ -1,2 +1,3 @@\n" +
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
		"@@ -10,5 +10,6 @@ func handle() {\n" +
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
