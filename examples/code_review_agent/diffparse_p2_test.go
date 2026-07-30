//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestHunkRangeSemanticValidation(t *testing.T) {
	tests := []struct {
		name        string
		diff        string
		wantWarning string
	}{
		{
			name:        "zero start with implicit nonzero count",
			diff:        "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -0 +0 @@\n-old\n+new\n",
			wantWarning: "starting at 0 must be empty",
		},
		{
			name:        "overflow",
			diff:        fmt.Sprintf("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -%d,1 +1,1 @@\n-old\n+new\n", maxIntValue()),
			wantWarning: "overflows line numbers",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parseUnifiedDiff([]byte(tt.diff))
			if !parseWarningsContain(parsed.Warnings, tt.wantWarning) {
				t.Fatalf("warnings = %+v, want %q", parsed.Warnings, tt.wantWarning)
			}
			if len(parsed.Files) != 1 || len(parsed.Files[0].Hunks) != 0 {
				t.Fatalf("invalid hunk was accepted: %+v", parsed.Files)
			}
		})
	}
}

func TestHunkOrderingAndOverlapValidation(t *testing.T) {
	t.Run("adjacent nonempty ranges", func(t *testing.T) {
		parsed := parseUnifiedDiff([]byte("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,1 @@\n one\n@@ -2,1 +2,1 @@\n two\n"))
		if len(parsed.Warnings) != 0 || len(parsed.Files[0].Hunks) != 2 {
			t.Fatalf("parsed = %+v, want two valid adjacent hunks", parsed)
		}
	})

	t.Run("overlap", func(t *testing.T) {
		parsed := parseUnifiedDiff([]byte("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,2 @@\n one\n two\n@@ -2,1 +3,1 @@\n two\n"))
		if !parseWarningsContain(parsed.Warnings, "old hunk ranges overlap") ||
			len(parsed.Files[0].Hunks) != 1 {
			t.Fatalf("parsed = %+v, want overlapping hunk rejected", parsed)
		}
	})

	t.Run("duplicate zero anchor", func(t *testing.T) {
		parsed := parseUnifiedDiff([]byte("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -5,0 +5,1 @@\n+one\n@@ -5,0 +6,1 @@\n+two\n"))
		if !parseWarningsContain(parsed.Warnings, "reuse a zero-length anchor") ||
			len(parsed.Files[0].Hunks) != 1 {
			t.Fatalf("parsed = %+v, want duplicate anchor rejected", parsed)
		}
	})
}

func TestDiffFileStatusSemanticValidation(t *testing.T) {
	tests := []struct {
		name        string
		diff        string
		wantWarning string
	}{
		{
			name:        "new file path mismatch",
			diff:        "diff --git a/a.go b/a.go\nnew file mode 100644\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1,1 @@\n+package a\n",
			wantWarning: "new file old path must be /dev/null",
		},
		{
			name:        "new file nonempty old range",
			diff:        "diff --git a/a.go b/a.go\nnew file mode 100644\n--- /dev/null\n+++ b/a.go\n@@ -1,1 +1,1 @@\n-old\n+package a\n",
			wantWarning: "new file old hunk range must be 0,0",
		},
		{
			name:        "deleted file nonempty new range",
			diff:        "diff --git a/a.go b/a.go\ndeleted file mode 100644\n--- a/a.go\n+++ /dev/null\n@@ -1,1 +1,1 @@\n-package a\n+package b\n",
			wantWarning: "deleted file new hunk range must be 0,0",
		},
		{
			name:        "incomplete rename",
			diff:        "diff --git a/a.go b/b.go\nrename from a.go\n",
			wantWarning: "both rename from and rename to",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parseUnifiedDiff([]byte(tt.diff))
			if !parseWarningsContain(parsed.Warnings, tt.wantWarning) {
				t.Fatalf("warnings = %+v, want %q", parsed.Warnings, tt.wantWarning)
			}
		})
	}
}

func TestValidNewFileAndBinaryPatchMetadata(t *testing.T) {
	newFile := parseUnifiedDiff([]byte("diff --git a/a.go b/a.go\nnew file mode 100644\n--- /dev/null\n+++ b/a.go\n@@ -0,0 +1,1 @@\n+package a\n"))
	if len(newFile.Warnings) != 0 {
		t.Fatalf("valid new file warnings = %+v", newFile.Warnings)
	}
	fixture, err := readFixture("rename_and_binary")
	if err != nil {
		t.Fatal(err)
	}
	binary := parseUnifiedDiff([]byte(fixture.Diff))
	if len(binary.Warnings) != 0 {
		t.Fatalf("valid binary patch warnings = %+v", binary.Warnings)
	}
}

func parseWarningsContain(warnings []parseWarning, substring string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning.Message, substring) {
			return true
		}
	}
	return false
}
