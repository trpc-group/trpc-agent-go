//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inputsource

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// sampleDiff builds a single-file diff for path name with one replaced line.
func sampleDiff(name string) string {
	return "diff --git a/" + name + " b/" + name + "\n" +
		"--- a/" + name + "\n" +
		"+++ b/" + name + "\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-old\n" +
		"+new\n"
}

// createSymlinkOrSkip creates a symlink at link pointing to target and
// returns its Lstat result. On Windows (and other platforms lacking the
// privilege), os.Symlink may report no error yet fail to create a usable
// link; the test is skipped in that case rather than failing, since the
// symlink-skipping behavior can only be asserted when symlinks are real.
func createSymlinkOrSkip(t *testing.T, target, link string) os.FileInfo {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink (needs admin/developer mode on Windows): %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Skipf("symlink reported success but is not usable: %v", err)
	}
	return info
}

// TestLoad_FixtureDir creates a temp dir with two .diff files and verifies
// that Load concatenates and parses them into two files.
func TestLoad_FixtureDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.diff"), []byte(sampleDiff("foo.go")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.diff"), []byte(sampleDiff("bar.go")), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := Load(context.Background(), SourceFixtureDir, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(in.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(in.Files))
	}
	if in.Source != SourceFixtureDir {
		t.Fatalf("source = %q, want %q", in.Source, SourceFixtureDir)
	}
}

// TestLoad_FixtureDir_SymlinkSkipped adds a symlink with a .diff extension
// alongside a real .diff file and verifies the symlink is not read.
func TestLoad_FixtureDir_SymlinkSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.diff"), []byte(sampleDiff("foo.go")), 0o644); err != nil {
		t.Fatal(err)
	}
	// Target file whose contents would parse as an extra file if read.
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte(sampleDiff("evil.go")), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.diff")
	info := createSymlinkOrSkip(t, target, link)
	// shouldUploadFile must reject the symlink outright.
	if shouldUploadFile(info) {
		t.Fatal("shouldUploadFile should return false for a symlink")
	}
	in, err := Load(context.Background(), SourceFixtureDir, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(in.Files) != 1 {
		t.Fatalf("expected 1 file (symlink skipped), got %d", len(in.Files))
	}
	if in.Files[0].NewPath != "foo.go" {
		t.Fatalf("NewPath = %q, want foo.go", in.Files[0].NewPath)
	}
}

// TestLoad_DiffFile loads a single .diff file and verifies parsing.
func TestLoad_DiffFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "single.diff")
	if err := os.WriteFile(f, []byte(sampleDiff("baz.go")), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := Load(context.Background(), SourceDiffFile, f)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(in.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(in.Files))
	}
	if in.Files[0].NewPath != "baz.go" {
		t.Fatalf("NewPath = %q, want baz.go", in.Files[0].NewPath)
	}
	if in.DiffText == "" {
		t.Fatal("DiffText should be populated")
	}
}

// TestLoad_FileList creates a list of two source files and verifies the
// synthetic diff parses into two files. File-list entries must be
// repo-relative; the repo root anchors them.
func TestLoad_FileList(t *testing.T) {
	dir := t.TempDir()
	src1 := filepath.Join(dir, "a.go")
	src2 := filepath.Join(dir, "b.go")
	if err := os.WriteFile(src1, []byte("package a\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Entries are repo-relative (not absolute) so they resolve under dir.
	list := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(list, []byte("a.go\nb.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := Load(context.Background(), SourceFileList, list, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(in.Files) != 2 {
		t.Fatalf("expected 2 synthetic files, got %d", len(in.Files))
	}
	// Each synthetic file should have added lines.
	var added int
	for _, f := range in.Files {
		added += len(f.AddedLines())
	}
	if added == 0 {
		t.Fatal("expected added lines from synthetic diff")
	}
}

// TestLoad_FileList_RejectTraversal verifies that absolute paths and
// ../ traversal entries are rejected before reading.
func TestLoad_FileList_RejectTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Absolute path entry — must be rejected.
	list := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(list, []byte(filepath.Join(dir, "a.go")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), SourceFileList, list, dir); err == nil {
		t.Fatal("expected error for absolute path in file-list")
	}
	// Traversal entry — must be rejected.
	if err := os.WriteFile(list, []byte("../a.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), SourceFileList, list, dir); err == nil {
		t.Fatal("expected error for traversal path in file-list")
	}
	// Missing repo root — must be rejected.
	if _, err := Load(context.Background(), SourceFileList, list); err == nil {
		t.Fatal("expected error for file-list without repo root")
	}
}

// TestLoad_RepoPath_UnbornHEAD initializes an empty git repo (no commits)
// and verifies Load returns no error and no files.
func TestLoad_RepoPath_UnbornHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not found on PATH: %v", err)
	}
	repo := t.TempDir()
	if err := exec.Command("git", "init", repo).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	in, err := Load(context.Background(), SourceRepoPath, repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if in == nil {
		t.Fatal("nil input")
	}
	if in.Source != SourceRepoPath {
		t.Fatalf("source = %q, want %q", in.Source, SourceRepoPath)
	}
	if in.RepoPath != repo {
		t.Fatalf("RepoPath = %q, want %q", in.RepoPath, repo)
	}
	if len(in.Files) != 0 {
		t.Fatalf("expected 0 files for empty repo, got %d", len(in.Files))
	}
}

// TestPathUnder verifies valid child paths are accepted and traversal
// attempts are rejected.
func TestPathUnder(t *testing.T) {
	parent := t.TempDir()

	// Valid child under parent.
	child := filepath.Join(parent, "sub", "file.txt")
	got, err := pathUnder(parent, child)
	if err != nil {
		t.Fatalf("pathUnder valid: %v", err)
	}
	if got != filepath.Clean(child) {
		t.Fatalf("got %q, want %q", got, filepath.Clean(child))
	}

	// Child equal to parent is allowed.
	if _, err := pathUnder(parent, parent); err != nil {
		t.Fatalf("pathUnder equal: %v", err)
	}

	// Traversal via joined .. components.
	traversal := filepath.Join(parent, "..", "..", "etc", "passwd")
	if _, err := pathUnder(parent, traversal); err == nil {
		t.Fatal("expected error for joined traversal, got nil")
	}

	// Literal traversal form from the spec.
	if _, err := pathUnder(parent, "../../../etc/passwd"); err == nil {
		t.Fatal("expected error for ../../../etc/passwd, got nil")
	}
}

// TestShouldUploadFile verifies regular files are accepted and symlinks
// are rejected.
func TestShouldUploadFile(t *testing.T) {
	dir := t.TempDir()

	// Regular file.
	reg := filepath.Join(dir, "regular.txt")
	if err := os.WriteFile(reg, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(reg)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldUploadFile(info) {
		t.Fatal("regular file should be uploaded")
	}

	// Nil info is rejected.
	if shouldUploadFile(nil) {
		t.Fatal("nil info should not be uploaded")
	}

	// Symlink is rejected.
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	linfo := createSymlinkOrSkip(t, target, link)
	if shouldUploadFile(linfo) {
		t.Fatal("symlink should not be uploaded")
	}
}
