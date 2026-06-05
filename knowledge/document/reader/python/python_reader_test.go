//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package python

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

type directoryReader interface {
	ReadFromDirectory(string) ([]*document.Document, error)
}

func newDirectoryReader(t *testing.T) directoryReader {
	t.Helper()
	r, ok := New().(directoryReader)
	if !ok {
		t.Fatal("New() reader does not support ReadFromDirectory")
	}
	return r
}

func TestReadFromDirectoryContinuesWhenSomeFilesFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.py"), []byte("class Good:\n    pass\n"), 0644); err != nil {
		t.Fatalf("write good.py: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.py"), []byte("def broken(:\n"), 0644); err != nil {
		t.Fatalf("write bad.py: %v", err)
	}

	r := newDirectoryReader(t)
	docs, err := r.ReadFromDirectory(dir)
	if err != nil {
		t.Fatalf("ReadFromDirectory() error = %v, want nil for partial failure", err)
	}
	if len(docs) == 0 {
		t.Fatal("ReadFromDirectory() returned no docs from successfully parsed file")
	}
}

func TestReadFromDirectoryReturnsErrorWhenAllFilesFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.py"), []byte("def broken(:\n"), 0644); err != nil {
		t.Fatalf("write bad.py: %v", err)
	}

	r := newDirectoryReader(t)
	_, err := r.ReadFromDirectory(dir)
	if err == nil {
		t.Fatal("ReadFromDirectory() error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "all 1 file(s) failed") || !strings.Contains(err.Error(), "bad.py") {
		t.Fatalf("ReadFromDirectory() error = %v, want all-files-failed context with file name", err)
	}
}

func TestFileToModulePathInitFiles(t *testing.T) {
	tests := []struct {
		relPath    string
		baseModule string
		want       string
	}{
		{relPath: "__init__.py", baseModule: "pkg", want: "pkg"},
		{relPath: filepath.Join("sub", "__init__.py"), baseModule: "pkg", want: "pkg.sub"},
		{relPath: filepath.Join("sub", "mod.py"), baseModule: "pkg", want: "pkg.sub.mod"},
		{relPath: "__init__.py", want: ""},
	}
	for _, tt := range tests {
		if got := fileToModulePath(tt.relPath, tt.baseModule); got != tt.want {
			t.Errorf("fileToModulePath(%q, %q) = %q, want %q", tt.relPath, tt.baseModule, got, tt.want)
		}
	}
}
