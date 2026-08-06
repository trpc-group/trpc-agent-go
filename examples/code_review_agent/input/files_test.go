//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input_test

import (
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
)

// TestParseFileList verifies related behavior.
func TestParseFileList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	src := "package sample\n\nfunc Hello() {}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := input.ParseFileList([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if b.Kind != "file_list" {
		t.Fatalf("kind=%s", b.Kind)
	}
	if len(b.Files) != 1 {
		t.Fatalf("files=%d", len(b.Files))
	}
	if b.Files[0].Package != "sample" {
		t.Fatalf("pkg=%s", b.Files[0].Package)
	}
	if len(b.Files[0].Hunks) == 0 || len(b.Files[0].Hunks[0].Lines) == 0 {
		t.Fatal("expected added lines")
	}
}

// TestParseFilesFlag_ListFile verifies related behavior.
func TestParseFilesFlag_ListFile(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "files.txt")
	a := filepath.Join(dir, "a.go")
	if err := os.WriteFile(a, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(list, []byte(a+"\n# comment\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := input.ParseFilesFlag("@" + list)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != a {
		t.Fatalf("paths=%v", paths)
	}
}
