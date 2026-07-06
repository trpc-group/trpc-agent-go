//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package diffparse

import (
	"os"
	"testing"
)

func TestParseUnifiedDiff(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/fixtures/security_secret.diff")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	files, err := Parse(string(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	file := files[0]
	if file.NewPath != "pkg/config.go" {
		t.Fatalf("NewPath = %q, want pkg/config.go", file.NewPath)
	}
	if !file.IsNew {
		t.Fatalf("IsNew = false, want true")
	}
	if file.PackageDir != "pkg" {
		t.Fatalf("PackageDir = %q, want pkg", file.PackageDir)
	}
	if got := file.Hunks[0].Lines[3].NewLine; got != 4 {
		t.Fatalf("secret line NewLine = %d, want 4", got)
	}
	if got := file.Hunks[0].Lines[3].Content; got != "\treturn \"api_key=sk-live1234567890abcdef\"" {
		t.Fatalf("secret line Content = %q", got)
	}
}

func TestParseRawUnifiedDiff(t *testing.T) {
	raw := `--- pkg/config.go	2026-07-06
+++ pkg/config.go	2026-07-06
@@ -1,2 +1,3 @@
 package pkg
+const token = "token=supersecretvalue"
 func ok() {}
`
	files, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	file := files[0]
	if file.OldPath != "pkg/config.go" || file.NewPath != "pkg/config.go" {
		t.Fatalf("paths = %q/%q, want pkg/config.go/pkg/config.go", file.OldPath, file.NewPath)
	}
	if file.PackageDir != "pkg" {
		t.Fatalf("PackageDir = %q, want pkg", file.PackageDir)
	}
	if got := file.Hunks[0].Lines[1].Content; got != `const token = "token=supersecretvalue"` {
		t.Fatalf("added content = %q", got)
	}
}

func TestParseRawUnifiedNewFile(t *testing.T) {
	raw := `--- /dev/null
+++ pkg/new.go
@@ -0,0 +1,2 @@
+package pkg
+const answer = 42
`
	files, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	if !files[0].IsNew {
		t.Fatalf("IsNew = false, want true")
	}
	if files[0].NewPath != "pkg/new.go" {
		t.Fatalf("NewPath = %q, want pkg/new.go", files[0].NewPath)
	}
}

func TestParseMultipleRawUnifiedDiffFiles(t *testing.T) {
	raw := `--- pkg/a.go
+++ pkg/a.go
@@ -1 +1,2 @@
 package pkg
+const a = true
--- pkg/b.go
+++ pkg/b.go
@@ -1 +1,2 @@
 package pkg
+const b = true
`
	files, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
	if files[0].NewPath != "pkg/a.go" || files[1].NewPath != "pkg/b.go" {
		t.Fatalf("paths = %q/%q, want pkg/a.go/pkg/b.go", files[0].NewPath, files[1].NewPath)
	}
}
