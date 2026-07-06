//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inputsource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestReadDiffFile(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "fixtures", "security_secret.diff")
	src, err := Read(context.Background(), Options{DiffFile: path})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if src.Type != review.InputTypeDiffFile {
		t.Fatalf("Type = %q, want %q", src.Type, review.InputTypeDiffFile)
	}
	if !strings.Contains(src.Diff, "diff --git") {
		t.Fatalf("Diff did not contain unified diff content")
	}
}

func TestReadFileList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(path, []byte("pkg/a.go\n# comment\npkg/b_test.go\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	src, err := Read(context.Background(), Options{FileList: path})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if src.Type != review.InputTypeFileList {
		t.Fatalf("Type = %q, want %q", src.Type, review.InputTypeFileList)
	}
	if got, want := strings.Join(src.FileList, ","), "pkg/a.go,pkg/b_test.go"; got != want {
		t.Fatalf("FileList = %q, want %q", got, want)
	}
}

func TestReadRejectsMultipleInputSources(t *testing.T) {
	_, err := Read(context.Background(), Options{
		DiffFile: "a.diff",
		RepoPath: ".",
	})
	if err == nil || !strings.Contains(err.Error(), "choose only one input source") {
		t.Fatalf("Read() error = %v, want multiple input source error", err)
	}
}
