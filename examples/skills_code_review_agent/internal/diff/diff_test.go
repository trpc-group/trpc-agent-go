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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUnifiedDiff_PlainUnifiedDiff(t *testing.T) {
	// 非 git 前缀的普通 unified diff（直接以 --- / +++ 开头）
	content := `--- old.go
+++ new.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {}
`
	d, err := ParseUnifiedDiff(content)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff failed: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(d.Files))
	}
	if d.Files[0].OldPath != "old.go" || d.Files[0].NewPath != "new.go" {
		t.Fatalf("paths = old:%q new:%q", d.Files[0].OldPath, d.Files[0].NewPath)
	}
	if len(d.Files[0].Hunks) != 1 {
		t.Fatalf("hunks = %+v", d.Files[0].Hunks)
	}
	h := d.Files[0].Hunks[0]
	if len(h.AddedLines) != 1 {
		t.Fatalf("added lines = %+v", h.AddedLines)
	}
	added := h.AddedLines[0]
	if added.Line != 2 {
		t.Fatalf("added line number = %d, want 2", added.Line)
	}
	if added.Content != `import "fmt"` {
		t.Fatalf("added content = %q, want import \"fmt\"", added.Content)
	}
}

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

func TestChangedFilesRejectsPathEscape(t *testing.T) {
	d, err := ParseUnifiedDiff(`--- a/../../../outside.go
+++ b/../../../outside.go
@@ -1 +1 @@
 package main
`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	files := d.ChangedFiles()
	if len(files) != 0 {
		t.Fatalf("changed files = %v, want none for escaped path", files)
	}
}

func TestSanitizeRepoRelativePathRejectsAbsolute(t *testing.T) {
	if _, err := SanitizeRepoRelativePath("/etc/passwd"); err == nil {
		t.Fatal("expected error for absolute path")
	}
}

func TestNormalizePathKeepsTopLevelAAndBDirs(t *testing.T) {
	d, err := ParseUnifiedDiff(`diff --git a/a/service.go b/a/service.go
--- a/a/service.go
+++ b/a/service.go
@@ -1,1 +1,2 @@
 package a
+var X = 1
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Files[0].NewPath != "a/service.go" {
		t.Fatalf("NewPath = %q, want a/service.go", d.Files[0].NewPath)
	}
	files := d.ChangedFiles()
	if len(files) != 1 || files[0] != "a/service.go" {
		t.Fatalf("ChangedFiles = %v", files)
	}

	d2, err := ParseUnifiedDiff(`diff --git a/b/handler.go b/b/handler.go
--- a/b/handler.go
+++ b/b/handler.go
@@ -1,1 +1,2 @@
 package b
+var Y = 1
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d2.Files[0].NewPath != "b/handler.go" {
		t.Fatalf("NewPath = %q, want b/handler.go", d2.Files[0].NewPath)
	}
}

func TestSanitizeKeepsLegitimateAAndBPaths(t *testing.T) {
	for _, p := range []string{"a/service.go", "b/handler.go", "pkg/a.go"} {
		got, err := SanitizeRepoRelativePath(p)
		if err != nil {
			t.Fatalf("Sanitize(%q): %v", p, err)
		}
		if got != p {
			t.Fatalf("Sanitize(%q) = %q, want unchanged", p, got)
		}
	}
}

func TestLoadFromRepoUsesHEADWorkingTree(t *testing.T) {
	dir := t.TempDir()
	runGitDiff(t, dir, "init")
	runGitDiff(t, dir, "config", "user.email", "test@example.com")
	runGitDiff(t, dir, "config", "user.name", "test")
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGitDiff(t, dir, "add", "main.go")
	runGitDiff(t, dir, "commit", "-m", "init")

	// Stage one change, then further modify the worktree (partially staged).
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() { println(1) }\n"), 0o644); err != nil {
		t.Fatalf("write staged: %v", err)
	}
	runGitDiff(t, dir, "add", "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() { println(2) }\n"), 0o644); err != nil {
		t.Fatalf("write worktree: %v", err)
	}

	d, err := LoadFromRepo(dir)
	if err != nil {
		t.Fatalf("LoadFromRepo: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("files = %d", len(d.Files))
	}
	found := false
	for _, h := range d.Files[0].Hunks {
		for _, al := range h.AddedLines {
			if strings.Contains(al.Content, "println(2)") {
				found = true
			}
			if strings.Contains(al.Content, "println(1)") {
				t.Fatalf("stale staged-only line still present: %+v", al)
			}
		}
	}
	if !found {
		t.Fatalf("expected worktree addition println(2) in %+v", d.Files[0].Hunks)
	}
}

func runGitDiff(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
