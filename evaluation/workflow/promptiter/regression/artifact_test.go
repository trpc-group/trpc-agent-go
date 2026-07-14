//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileArtifactWriterRejectsUnsafePaths(t *testing.T) {
	writer, err := NewFileArtifactWriter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "../escape", `..\escape`, "/absolute", `C:\absolute`, `\\server\share`} {
		if err := writer.Write(path, []byte("bad")); err == nil {
			t.Errorf("Write(%q) succeeded", path)
		}
	}
}

func TestFileArtifactWriterAtomicallyReplacesFile(t *testing.T) {
	root := t.TempDir()
	writer, err := NewFileArtifactWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Write("round_1/gate.json", []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(`round_1\gate.json`, []byte("new")); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(root, "round_1", "gate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "new" {
		t.Fatalf("payload = %q", payload)
	}
	matches, err := filepath.Glob(filepath.Join(root, "round_1", ".artifact-*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files = %#v, err=%v", matches, err)
	}
}

func TestFileArtifactWriterRejectsOutputContainingInput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "data", "train.evalset.json")
	if _, err := NewFileArtifactWriterWithInputs(root, input); err == nil {
		t.Fatal("writer accepted output directory containing an input")
	}
}
