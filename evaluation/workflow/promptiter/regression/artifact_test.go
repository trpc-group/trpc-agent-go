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
	"runtime"
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

func TestFileArtifactWriterErrorPaths(t *testing.T) {
	if _, err := NewFileArtifactWriter(""); err == nil {
		t.Fatal("empty output directory was accepted")
	}
	writer, err := NewFileArtifactWriterWithInputs(t.TempDir(), "")
	if err != nil {
		t.Fatalf("blank protected input was not ignored: %v", err)
	}
	var nilWriter *FileArtifactWriter
	if err := nilWriter.Write("report.json", nil); err == nil {
		t.Fatal("nil writer succeeded")
	}

	rootFile := filepath.Join(t.TempDir(), "root-file")
	if err := os.WriteFile(rootFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileRootWriter, err := NewFileArtifactWriter(rootFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := fileRootWriter.Write("nested/report.json", nil); err == nil {
		t.Fatal("writer created a directory below a file")
	}

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	renameWriter, err := NewFileArtifactWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := renameWriter.Write("occupied", []byte("payload")); err == nil {
		t.Fatal("writer replaced a directory with a file")
	}

	if err := syncDirectory(filepath.Join(t.TempDir(), "missing")); runtime.GOOS != "windows" && err == nil {
		t.Fatal("syncDirectory succeeded for a missing directory")
	}
	if err := writeJSON(writer, "bad.json", make(chan int)); err == nil {
		t.Fatal("writeJSON marshaled an unsupported value")
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
