//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package diffparser

import "testing"

// TestParseUnifiedDiff verifies hunk, line, and metadata parsing of a unified diff.
func TestParseUnifiedDiff(t *testing.T) {
	diff := []byte(`diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package foo
 
+func Bar() {}
`)
	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files=%d, want 1", len(files))
	}
	if files[0].NewPath != "foo.go" {
		t.Fatalf("path=%q", files[0].NewPath)
	}
	if files[0].PackageName != "foo" {
		t.Fatalf("package=%q", files[0].PackageName)
	}
	if got := files[0].Hunks[0].Lines[2].NewLine; got != 3 {
		t.Fatalf("added line=%d, want 3", got)
	}
}

// TestParseUnifiedDiffPlainMultiFile verifies plain diffs without
// "diff --git" headers still split into one entry per file.
func TestParseUnifiedDiffPlainMultiFile(t *testing.T) {
	diff := []byte(`--- a/foo.go
+++ b/foo.go
@@ -1,1 +1,2 @@
 package foo
+func Foo() {}
--- a/bar.go
+++ b/bar.go
@@ -1,1 +1,2 @@
 package bar
+func Bar() {}
`)
	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files=%d, want 2", len(files))
	}
	if files[0].NewPath != "foo.go" || files[1].NewPath != "bar.go" {
		t.Fatalf("paths=%q,%q", files[0].NewPath, files[1].NewPath)
	}
	if len(files[0].Hunks) != 1 || len(files[1].Hunks) != 1 {
		t.Fatalf("hunks=%d,%d, want 1,1", len(files[0].Hunks), len(files[1].Hunks))
	}
}

// TestParseUnifiedDiffGitStyleDeletion verifies a git-style deletion
// keeps the file with its old path instead of vanishing from the diff.
func TestParseUnifiedDiffGitStyleDeletion(t *testing.T) {
	diff := []byte(`diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package gone
-
-func Gone() {}
`)
	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files=%d, want 1", len(files))
	}
	f := files[0]
	if !f.Deleted {
		t.Fatalf("file not marked deleted: %+v", f)
	}
	if f.NewPath != "gone.go" || f.OldPath != "gone.go" {
		t.Fatalf("paths=%q/%q, want gone.go display path", f.OldPath, f.NewPath)
	}
	if f.Language != "go" {
		t.Fatalf("language=%q, want go", f.Language)
	}
	if len(f.Hunks) != 1 || len(f.Hunks[0].Lines) != 3 {
		t.Fatalf("deleted hunk lines lost: %+v", f.Hunks)
	}
}

// TestParseUnifiedDiffPlainDeletion verifies a plain unified deletion
// followed by another file still yields both entries.
func TestParseUnifiedDiffPlainDeletion(t *testing.T) {
	diff := []byte(`--- gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package gone
-func Gone() {}
--- a/kept.go
+++ b/kept.go
@@ -1,1 +1,2 @@
 package kept
+func Kept() {}
`)
	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files=%d, want 2", len(files))
	}
	if !files[0].Deleted || files[0].NewPath != "gone.go" {
		t.Fatalf("deletion entry wrong: %+v", files[0])
	}
	if files[1].Deleted || files[1].NewPath != "kept.go" {
		t.Fatalf("kept entry wrong: %+v", files[1])
	}
}
