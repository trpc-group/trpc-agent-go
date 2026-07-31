//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package diff

import (
	"testing"
)

func TestParseSimpleDiff(t *testing.T) {
	diffText := `diff --git a/foo.go b/foo.go
index 1234567..abcdefg 100644
--- a/foo.go
+++ b/foo.go
@@ -10,3 +10,4 @@ package foo
 func bar() {
 	old := "x"
+	new := "y"
+	fmt.Println(new)
 	return
 }
`
	changes, err := Parse(diffText)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(changes))
	}
	fc := changes[0]
	if fc.NewPath != "foo.go" {
		t.Errorf("NewPath = %q, want %q", fc.NewPath, "foo.go")
	}
	if fc.Status != "modified" {
		t.Errorf("Status = %q, want %q", fc.Status, "modified")
	}
	if fc.AddedLines != 2 {
		t.Errorf("AddedLines = %d, want 2", fc.AddedLines)
	}
	if fc.DeletedLines != 0 {
		t.Errorf("DeletedLines = %d, want 0", fc.DeletedLines)
	}
	if len(fc.AddedLineNumbers) != 2 {
		t.Fatalf("len(AddedLineNumbers) = %d, want 2", len(fc.AddedLineNumbers))
	}
	if fc.AddedLineNumbers[0] != 12 {
		t.Errorf("AddedLineNumbers[0] = %d, want 12", fc.AddedLineNumbers[0])
	}
	if fc.AddedLineNumbers[1] != 13 {
		t.Errorf("AddedLineNumbers[1] = %d, want 13", fc.AddedLineNumbers[1])
	}
}

func TestParseNewFile(t *testing.T) {
	diffText := `diff --git a/new.go b/new.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package main
+
+func main() {}
`
	changes, err := Parse(diffText)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(changes))
	}
	fc := changes[0]
	if fc.Status != "added" {
		t.Errorf("Status = %q, want %q", fc.Status, "added")
	}
	if fc.OldPath != "" {
		t.Errorf("OldPath = %q, want empty", fc.OldPath)
	}
}

func TestParseDeletedFile(t *testing.T) {
	diffText := `diff --git a/old.go b/old.go
deleted file mode 100644
index 1234567..0000000
--- a/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package main
-
-func main() {}
`
	changes, err := Parse(diffText)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(changes))
	}
	fc := changes[0]
	if fc.Status != "deleted" {
		t.Errorf("Status = %q, want %q", fc.Status, "deleted")
	}
	if fc.NewPath != "" {
		t.Errorf("NewPath = %q, want empty", fc.NewPath)
	}
}

func TestParseMultipleFiles(t *testing.T) {
	diffText := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
-old := 1
+new := 1
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -1,3 +1,3 @@
-old := 2
+new := 2
`
	changes, err := Parse(diffText)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected 2 file changes, got %d", len(changes))
	}
}

func TestAddedContent(t *testing.T) {
	fc := FileChange{
		Hunks: []Hunk{
			{
				NewStart: 1,
				Lines:    []string{"+hello", "+world"},
			},
		},
	}
	content := fc.AddedContent()
	want := "hello\nworld\n"
	if content != want {
		t.Errorf("AddedContent() = %q, want %q", content, want)
	}
}
