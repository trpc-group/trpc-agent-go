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
)

func TestReadFromDirectoryReturnsErrorWhenAnyFileFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.py"), []byte("class Good:\n    pass\n"), 0644); err != nil {
		t.Fatalf("write good.py: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.py"), []byte("def broken(:\n"), 0644); err != nil {
		t.Fatalf("write bad.py: %v", err)
	}

	r := New().(*Reader)
	_, err := r.ReadFromDirectory(dir)
	if err == nil {
		t.Fatal("ReadFromDirectory() error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "parse ") {
		t.Fatalf("ReadFromDirectory() error = %v, want parse context", err)
	}
}
