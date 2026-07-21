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
	if _, err := NewFileArtifactWriter(rootFile); err == nil {
		t.Fatal("writer accepted a file as its output directory")
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

	if err := writeJSON(writer, "bad.json", make(chan int)); err == nil {
		t.Fatal("writeJSON marshaled an unsupported value")
	}
}

func TestFileArtifactWriterRejectsSymlinkParentEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "round_1")); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	writer, err := NewFileArtifactWriter(root)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Write("round_1/candidate_profile.json", []byte("escape")); err == nil {
		t.Fatal("writer followed a symlink outside its root")
	}
	if _, err := os.Stat(filepath.Join(outside, "candidate_profile.json")); !os.IsNotExist(err) {
		t.Fatalf("artifact escaped through symlink: %v", err)
	}
}

func TestFileArtifactWriterRejectsProtectedInputSymlinkAlias(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	input := filepath.Join(outside, "train.evalset.json")
	if err := os.WriteFile(input, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "alias")); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	writer, err := NewFileArtifactWriterWithInputs(root, input)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Write("alias/train.evalset.json", []byte("overwrite")); err == nil {
		t.Fatal("writer followed a protected input symlink alias")
	}
	payload, err := os.ReadFile(input)
	if err != nil || string(payload) != "protected" {
		t.Fatalf("protected input changed: payload=%q err=%v", payload, err)
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
