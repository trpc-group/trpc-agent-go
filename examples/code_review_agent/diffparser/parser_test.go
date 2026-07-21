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
