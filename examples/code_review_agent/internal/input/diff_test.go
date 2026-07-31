//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseUnifiedDiffExtractsHunksContextCandidatesAndPackage(t *testing.T) {
	diff := "diff --git a/pkg/service.go b/pkg/service.go\n" +
		"--- a/pkg/service.go\n" +
		"+++ b/pkg/service.go\n" +
		"@@ -1,4 +1,5 @@\n" +
		" package service\n" +
		" func existing() {}\n" +
		"+func added() {}\n" +
		" func tail() {}\n"
	parsed, err := ParseUnifiedDiffString(diff, Limits{MaxBytes: 1 << 20, MaxLines: 1000})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed.Files) != 1 || len(parsed.Files[0].Hunks) != 1 {
		t.Fatalf("hunks not extracted: %#v", parsed.Files)
	}
	h := parsed.Files[0].Hunks[0]
	if h.OldStart != 1 || h.NewStart != 1 || h.OldLines != 4 || h.NewLines != 5 {
		t.Fatalf("hunk header = %#v", h)
	}
	if len(h.Context) != 3 || h.Context[0].Text != "package service" || h.Context[0].NewLine != 1 {
		t.Fatalf("context not preserved: %#v", h.Context)
	}
	if len(h.Candidates) != 1 || h.Candidates[0].Line != 3 || h.Candidates[0].Package != "service" {
		t.Fatalf("candidate metadata = %#v", h.Candidates)
	}
	if parsed.Files[0].Package != "service" {
		t.Fatalf("file package = %q", parsed.Files[0].Package)
	}
}

func TestParseUnifiedDiffAdversarialEdges(t *testing.T) {
	diff := "diff --git \"a/path with space.go\" \"b/path with space.go\"\n" +
		"--- \"a/path with space.go\"\n" +
		"+++ \"b/path with space.go\"\n" +
		"@@ -1,2 +1,4 @@\n" +
		" package main\n" +
		"+// --- not a header\n" +
		"+// +++ not a header\n" +
		" func main() {}\n" +
		"\\ No newline at end of file\n" +
		"diff --git a/old.go b/new.go\n" +
		"similarity index 90%\n" +
		"rename from old.go\n" +
		"rename to new.go\n" +
		"--- a/old.go\n" +
		"+++ b/new.go\n" +
		"@@ -1 +1 @@\n" +
		"-package old\n" +
		"+package new\n" +
		"diff --git a/image.png b/image.png\n" +
		"Binary files a/image.png and b/image.png differ\n"
	parsed, err := ParseUnifiedDiffString(diff, Limits{MaxBytes: 1 << 20, MaxLines: 1000})
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !parsed.Complete {
		t.Fatalf("expected complete parse, warnings: %v", parsed.Warnings)
	}
	if len(parsed.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(parsed.Files))
	}
	if got := parsed.Files[0].NewPath; got != "path with space.go" {
		t.Fatalf("quoted path = %q", got)
	}
	if got := parsed.Files[0].Added[0].Line; got != 2 {
		t.Fatalf("first added line = %d, want 2", got)
	}
	if !parsed.Files[2].Binary {
		t.Fatalf("binary file not marked")
	}
}

func TestParseUnifiedDiffDetectsTruncatedHunk(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,3 @@\n package a\n+func A() {}\n"
	parsed, err := ParseUnifiedDiffString(diff, Limits{MaxBytes: 1 << 20, MaxLines: 100})
	if err != nil {
		t.Fatalf("parse returned hard error: %v", err)
	}
	if parsed.Complete {
		t.Fatalf("truncated hunk marked complete")
	}
}

func TestChangedPackagesUseNearestModule(t *testing.T) {
	files := []FileDiff{{NewPath: "cmd/app/main.go"}, {NewPath: "internal/lib/lib_test.go"}}
	got := ChangedPackages(files, []string{"go.mod", "cmd/app/go.mod"})
	want := []string{"./internal/lib", "."}
	if len(got) != len(want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("packages = %v, want %v", got, want)
		}
	}
}

func TestBuildGitSnapshotCopiesOnlyTrackedSafeFiles(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	writeFile(t, repo, ".gitignore", "*.log\n.cache/\n")
	writeFile(t, repo, "go.mod", "module example.com/repo\n")
	writeFile(t, repo, "tracked.go", "package repo\n")
	writeFile(t, repo, "ignored.log", "must not copy\n")
	writeFile(t, repo, ".cache/build", "must not copy\n")
	writeFile(t, repo, "untracked.go", "must not copy\n")
	if err := os.Symlink("tracked.go", filepath.Join(repo, "link.go")); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gitignore", "go.mod", "tracked.go")
	runGit(t, repo, "commit", "-m", "initial")

	snap, cleanup, err := BuildGitSnapshot(repo, 128<<20)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer cleanup()
	assertExists(t, filepath.Join(snap.Path, "tracked.go"))
	assertMissing(t, filepath.Join(snap.Path, ".git"))
	assertMissing(t, filepath.Join(snap.Path, "ignored.log"))
	assertMissing(t, filepath.Join(snap.Path, ".cache"))
	assertMissing(t, filepath.Join(snap.Path, "untracked.go"))
	assertMissing(t, filepath.Join(snap.Path, "link.go"))

	before, err := os.ReadFile(filepath.Join(snap.Path, "tracked.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "tracked.go", "package repo\nconst changed = true\n")
	after, err := os.ReadFile(filepath.Join(snap.Path, "tracked.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("snapshot mutated after source repo changed")
	}
}

func TestBuildGitSnapshotNormalizesFileModesForContainerReadOnlyStage(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	writeFile(t, repo, "go.mod", "module example.com/repo\n")
	if err := os.Chmod(filepath.Join(repo, "go.mod"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "go.mod")
	runGit(t, repo, "commit", "-m", "initial")

	snap, cleanup, err := BuildGitSnapshot(repo, 128<<20)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer cleanup()
	st, err := os.Stat(filepath.Join(snap.Path, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o644 {
		t.Fatalf("snapshot file mode = %o, want 0644", got)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_EXTERNAL_DIFF=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s missing: %v", path, err)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists or stat failed unexpectedly: %v", path, err)
	}
}
