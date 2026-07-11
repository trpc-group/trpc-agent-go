//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package parser

import (
	"testing"
)

func TestParseDiff(t *testing.T) {
	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/pkg/utils/helper.go
@@ -0,0 +1,10 @@
+package utils
+
+func Add(a, b int) int {
+    return a + b
+}
+
+func Multiply(a, b int) int {
+    return a * b
+}
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	if len(result.Files) != 1 {
		t.Errorf("Expected 1 file, got %d", len(result.Files))
	}

	if result.Files[0].NewPath != "pkg/utils/helper.go" {
		t.Errorf("Expected path 'pkg/utils/helper.go', got '%s'", result.Files[0].NewPath)
	}

	if result.Files[0].IsNewFile != true {
		t.Error("Expected file to be new")
	}

	if result.TotalAdded != 9 {
		t.Errorf("Expected 9 added lines, got %d", result.TotalAdded)
	}

	if result.TotalRemoved != 0 {
		t.Errorf("Expected 0 removed lines, got %d", result.TotalRemoved)
	}
}

func TestParseDiffWithHunks(t *testing.T) {
	diffContent := `diff --git a/pkg/worker/worker.go b/pkg/worker/worker.go
index abc1234..def5678 100644
--- a/pkg/worker/worker.go
+++ b/pkg/worker/worker.go
@@ -5,7 +5,10 @@ package worker
 
 func StartWorker() {
     for i := 0; i < 10; i++ {
-        go func() {
+        go func(id int) {
+            fmt.Printf("Worker %d started\n", id)
             time.Sleep(time.Second)
+            fmt.Printf("Worker %d finished\n", id)
         }(i)
     }
 }
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	if len(result.Files) != 1 {
		t.Errorf("Expected 1 file, got %d", len(result.Files))
	}

	file := result.Files[0]
	if len(file.Hunks) != 1 {
		t.Errorf("Expected 1 hunk, got %d", len(file.Hunks))
	}

	hunk := file.Hunks[0]
	if hunk.OldStart != 5 {
		t.Errorf("Expected old start 5, got %d", hunk.OldStart)
	}

	if hunk.NewStart != 5 {
		t.Errorf("Expected new start 5, got %d", hunk.NewStart)
	}

	if len(hunk.AddedLines) != 3 {
		t.Errorf("Expected 3 added lines, got %d", len(hunk.AddedLines))
	}

	expectedAddedLines := []int{8, 9, 11}
	for i, expected := range expectedAddedLines {
		if hunk.AddedLines[i] != expected {
			t.Errorf("Expected added line %d to be %d, got %d", i+1, expected, hunk.AddedLines[i])
		}
	}
}

func TestFilterGoFiles(t *testing.T) {
	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
new file mode 100644
--- /dev/null
+++ b/pkg/utils/helper.go
@@ -0,0 +1,5 @@
+package utils
+
diff --git a/README.md b/README.md
index abc1234..def5678 100644
--- a/README.md
+++ b/README.md
@@ -1,3 +1,4 @@
 # Project
+New line
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	goFiles := FilterGoFiles(result.Files)
	if len(goFiles) != 1 {
		t.Errorf("Expected 1 Go file, got %d", len(goFiles))
	}

	if goFiles[0].NewPath != "pkg/utils/helper.go" {
		t.Errorf("Expected 'pkg/utils/helper.go', got '%s'", goFiles[0].NewPath)
	}
}

func TestGetChangedLines(t *testing.T) {
	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
new file mode 100644
--- /dev/null
+++ b/pkg/utils/helper.go
@@ -0,0 +1,5 @@
+package utils
+
+func Add(a, b int) int {
+    return a + b
+}
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	changedLines := GetChangedLines(result)
	if len(changedLines) != 1 {
		t.Errorf("Expected 1 file with changed lines, got %d", len(changedLines))
	}

	lines, ok := changedLines["pkg/utils/helper.go"]
	if !ok {
		t.Error("Expected 'pkg/utils/helper.go' in changed lines")
	}

	if len(lines) != 5 {
		t.Errorf("Expected 5 changed lines, got %d", len(lines))
	}
}

func TestFormatDiffForReview(t *testing.T) {
	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
new file mode 100644
--- /dev/null
+++ b/pkg/utils/helper.go
@@ -0,0 +1,5 @@
+package utils
+
+func Add(a, b int) int {
+    return a + b
+}
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	formatted := FormatDiffForReview(result)
	if formatted == "" {
		t.Error("Expected non-empty formatted output")
	}

	if len(formatted) < 10 {
		t.Errorf("Expected formatted output to be at least 10 chars, got %d", len(formatted))
	}
}

func TestParseDiff_DeletedFile(t *testing.T) {
	diffContent := `diff --git a/old_file.go b/old_file.go
deleted file mode 100644
index 1234567..0000000
--- a/old_file.go
+++ /dev/null
@@ -1,5 +0,0 @@
-package old
-
-func OldFunc() {
-}
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	if len(result.Files) != 1 {
		t.Errorf("Expected 1 file, got %d", len(result.Files))
	}

	file := result.Files[0]
	if !file.IsDeleted {
		t.Error("Expected file to be deleted")
	}

	if file.NewPath != "old_file.go" {
		t.Errorf("Expected path 'old_file.go', got '%s'", file.NewPath)
	}

	if result.TotalRemoved != 4 {
		t.Errorf("Expected 4 removed lines, got %d", result.TotalRemoved)
	}
}

func TestParseDiff_EmptyContent(t *testing.T) {
	result, err := ParseDiff("")
	if err != nil {
		t.Fatalf("Failed to parse empty diff: %v", err)
	}

	if result == nil {
		t.Error("Expected non-nil result")
	}

	if len(result.Files) != 0 {
		t.Errorf("Expected 0 files, got %d", len(result.Files))
	}

	if result.TotalAdded != 0 || result.TotalRemoved != 0 {
		t.Errorf("Expected 0 added/removed lines")
	}
}

func TestParseDiff_InvalidFormat(t *testing.T) {
	testCases := []string{
		"not a diff at all",
		"diff --git",
		"diff --git a/file.go",
	}

	for _, tc := range testCases {
		result, err := ParseDiff(tc)
		if err != nil {
			t.Errorf("Expected no error for invalid format: %v", err)
		}
		if result == nil {
			t.Error("Expected non-nil result")
		}
	}
}

func TestParseDiff_MultipleFiles(t *testing.T) {
	diffContent := `diff --git a/file1.go b/file1.go
new file mode 100644
--- /dev/null
+++ b/file1.go
@@ -0,0 +1,3 @@
+package pkg
+
+func Func1() {}
diff --git a/file2.go b/file2.go
new file mode 100644
--- /dev/null
+++ b/file2.go
@@ -0,0 +1,3 @@
+package pkg
+
+func Func2() {}
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	if len(result.Files) != 2 {
		t.Errorf("Expected 2 files, got %d", len(result.Files))
	}

	if result.TotalAdded != 6 {
		t.Errorf("Expected 6 added lines, got %d", result.TotalAdded)
	}
}

func TestExtractGoPackage(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"pkg/utils/helper.go", "pkg/utils"},
		{"main.go", ""},
		{"helper.go", ""},
		{"internal/api/server.go", "internal/api"},
		{"README.md", ""},
		{"pkg/utils/", ""},
	}

	for _, tc := range testCases {
		result := extractGoPackage(tc.input)
		if result != tc.expected {
			t.Errorf("extractGoPackage(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}

func TestFilterGoFiles_TestFiles(t *testing.T) {
	files := []DiffFile{
		{NewPath: "pkg/utils/helper.go"},
		{NewPath: "pkg/utils/helper_test.go"},
		{NewPath: "pkg/utils/helper_test.go.txt"},
		{NewPath: "README.md"},
	}

	result := FilterGoFiles(files)
	if len(result) != 1 {
		t.Errorf("Expected 1 Go file (excluding test files), got %d", len(result))
	}

	if result[0].NewPath != "pkg/utils/helper.go" {
		t.Errorf("Expected 'pkg/utils/helper.go', got '%s'", result[0].NewPath)
	}
}

func TestParseDiffFile_NonExistent(t *testing.T) {
	_, err := ParseDiffFile("/nonexistent/path/to/file.diff")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}
