//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUnifiedDiff_SingleFile(t *testing.T) {
	content := `diff --git a/pkg/user.go b/pkg/user.go
--- a/pkg/user.go
+++ b/pkg/user.go
@@ -10,3 +10,4 @@ func Load() error {
 	return nil
+	return fmt.Errorf("oops")
 }
`
	d, err := ParseUnifiedDiff(content)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff failed: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(d.Files))
	}
	if d.Files[0].NewPath != "pkg/user.go" {
		t.Fatalf("new path = %q", d.Files[0].NewPath)
	}
	if len(d.Files[0].Hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(d.Files[0].Hunks))
	}
	h := d.Files[0].Hunks[0]
	if h.StartLine != 10 {
		t.Fatalf("start line = %d, want 10", h.StartLine)
	}
	if len(h.AddedLines) != 1 || h.AddedLines[0].Line != 11 {
		t.Fatalf("added lines = %+v", h.AddedLines)
	}
}

func TestParseUnifiedDiff_MultiFile(t *testing.T) {
	content := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,1 +1,2 @@
 package a
+var X = 1
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -2,1 +3,2 @@ package b
+var Y = 2
 func B() {}
`
	d, err := ParseUnifiedDiff(content)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff failed: %v", err)
	}
	if len(d.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(d.Files))
	}
	if got := len(d.AllHunks()); got != 2 {
		t.Fatalf("hunks = %d, want 2", got)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.diff")
	if err := os.WriteFile(path, []byte(`diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -1,1 +1,2 @@
 package x
+var N = 1
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	d, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(d.Files))
	}
}

func TestChangedFilesAndSummary(t *testing.T) {
	d, err := ParseUnifiedDiff(`diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,1 +1,2 @@
 package a
+x := 1
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -1,1 +1,2 @@
 package b
+y := 2
`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	files := d.ChangedFiles()
	if len(files) != 2 {
		t.Fatalf("changed files = %v", files)
	}
	if !strings.Contains(d.Summary(), "a.go") {
		t.Fatalf("summary = %q", d.Summary())
	}
}

func TestInferGoPackage(t *testing.T) {
	got := InferGoPackage("internal/service/handler.go", "")
	want := filepath.ToSlash("internal/service")
	if got != want {
		t.Fatalf("package = %q, want %q", got, want)
	}
}

func TestInferGoPackageRejectsPathEscape(t *testing.T) {
	if got := InferGoPackage("b/../outside.go", t.TempDir()); got != "" {
		t.Fatalf("package = %q, want empty for escaped path", got)
	}
}
