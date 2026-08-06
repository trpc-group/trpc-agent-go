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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
)

// TestParseUnifiedDiff_HunksAndPackages verifies related behavior.
func TestParseUnifiedDiff_HunksAndPackages(t *testing.T) {
	raw := `diff --git a/pkg/worker/worker.go b/pkg/worker/worker.go
--- a/pkg/worker/worker.go
+++ b/pkg/worker/worker.go
@@ -1,5 +1,8 @@
 package worker
 
-func Start() {}
+func Start() {
+	go func() { doWork() }()
+}
`
	b, err := input.ParseUnifiedDiff("diff_file", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 1 {
		t.Fatalf("files=%d", len(b.Files))
	}
	f := b.Files[0]
	if f.Path != "pkg/worker/worker.go" {
		t.Fatalf("path=%s", f.Path)
	}
	if f.Language != "go" {
		t.Fatalf("lang=%s", f.Language)
	}
	if f.Package != "worker" {
		t.Fatalf("pkg=%s", f.Package)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("hunks=%d", len(f.Hunks))
	}
	var added int
	for _, l := range f.Hunks[0].Lines {
		if l.Kind == '+' {
			added++
			if l.NewLineNo <= 0 {
				t.Fatalf("missing new line no: %+v", l)
			}
		}
	}
	if added < 2 {
		t.Fatalf("added=%d", added)
	}
	if !strings.Contains(b.Summary, "1 files") {
		t.Fatalf("summary=%s", b.Summary)
	}
	if b.Digest == "" {
		t.Fatal("empty digest")
	}
}

// TestParseUnifiedDiff_Empty verifies related behavior.
func TestParseUnifiedDiff_Empty(t *testing.T) {
	b, err := input.ParseUnifiedDiff("diff_file", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 0 {
		t.Fatalf("want 0 files, got %d", len(b.Files))
	}
}

// TestParseUnifiedDiff_RedactsSecrets ensures RawRedacted never holds plaintext secrets.
func TestParseUnifiedDiff_RedactsSecrets(t *testing.T) {
	raw := `diff --git a/cfg.go b/cfg.go
--- a/cfg.go
+++ b/cfg.go
@@ -1,3 +1,4 @@
 package cfg
 
+password = "SuperSecretPassword123"
`
	b, err := input.ParseUnifiedDiff("diff_file", raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.RawRedacted, "SuperSecretPassword123") {
		t.Fatalf("secret leaked in RawRedacted: %s", b.RawRedacted)
	}
	if !strings.Contains(b.RawRedacted, "[REDACTED]") {
		t.Fatalf("expected redaction marker: %s", b.RawRedacted)
	}
}

// TestParseUnifiedDiff_QuotedPathsWithSpaces covers Git-quoted pathnames.
func TestParseUnifiedDiff_QuotedPathsWithSpaces(t *testing.T) {
	raw := `diff --git "a/pkg/my worker/file.go" "b/pkg/my worker/file.go"
--- "a/pkg/my worker/file.go"
+++ "b/pkg/my worker/file.go"
@@ -1,3 +1,5 @@
 package worker
 
-func Start() {}
+func Start() {
+	go func() { doWork() }()
+}
`
	b, err := input.ParseUnifiedDiff("diff_file", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 1 {
		t.Fatalf("files=%d", len(b.Files))
	}
	if b.Files[0].Path != "pkg/my worker/file.go" {
		t.Fatalf("path=%q", b.Files[0].Path)
	}
	if b.Files[0].Language != "go" {
		t.Fatalf("lang=%s", b.Files[0].Language)
	}
	if len(b.Files[0].Hunks) == 0 || b.Files[0].Hunks[0].NewStart != 1 {
		t.Fatalf("expected hunk location for finding attribution: %+v", b.Files[0].Hunks)
	}
}

// TestParseUnifiedDiff_QuotedRenameAndDelete covers rename/delete with spaces.
func TestParseUnifiedDiff_QuotedRenameAndDelete(t *testing.T) {
	raw := `diff --git "a/old dir/a.go" "b/new dir/a.go"
rename from "old dir/a.go"
rename to "new dir/a.go"
--- "a/old dir/a.go"
+++ "b/new dir/a.go"
@@ -1,3 +1,3 @@
 package a
 
-func A() {}
+func A() { go func(){}() }
diff --git "a/gone dir/b.go" "b/gone dir/b.go"
deleted file mode 100644
--- "a/gone dir/b.go"
+++ /dev/null
@@ -1,3 +0,0 @@
-package b
-
-func B() {}
`
	b, err := input.ParseUnifiedDiff("diff_file", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 2 {
		t.Fatalf("files=%d paths=%v", len(b.Files), pathsOf(b))
	}
	if b.Files[0].Path != "new dir/a.go" || b.Files[0].Language != "go" {
		t.Fatalf("rename file=%+v", b.Files[0])
	}
	if b.Files[1].Path != "gone dir/b.go" || b.Files[1].Language != "go" {
		t.Fatalf("delete file=%+v", b.Files[1])
	}
}

func pathsOf(b *input.DiffBundle) []string {
	out := make([]string, 0, len(b.Files))
	for _, f := range b.Files {
		out = append(out, f.Path)
	}
	return out
}

// TestParseRepoDiff_StagedPlusUnstaged verifies ParseRepoDiff uses git diff HEAD
// with external helpers disabled.
func TestParseRepoDiff_StagedPlusUnstaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git helper is a POSIX shell script")
	}
	binDir := t.TempDir()
	argsPath := filepath.Join(binDir, "git.args")
	script := filepath.Join(binDir, "git")
	body := `#!/bin/sh
printf '%s\n' "$@" > "$GIT_FAKE_ARGS"
ok=0
has_no_ext=0
has_no_textconv=0
prev=""
for a in "$@"; do
  if [ "$a" = "--no-ext-diff" ]; then has_no_ext=1; fi
  if [ "$a" = "--no-textconv" ]; then has_no_textconv=1; fi
  if [ "$prev" = "diff" ] || [ "$prev" = "--no-ext-diff" ] || [ "$prev" = "--no-textconv" ]; then
    :
  fi
  if [ "$a" = "HEAD" ]; then ok=1; fi
  if [ "$a" = "--cached" ] || [ "$a" = "--staged" ]; then
    echo "fake-git: unexpected cached/staged diff" >&2
    exit 2
  fi
  prev="$a"
done
if [ "$ok" != 1 ] || [ "$has_no_ext" != 1 ] || [ "$has_no_textconv" != 1 ]; then
  echo "fake-git: expected git ... diff --no-ext-diff --no-textconv HEAD, got: $*" >&2
  exit 2
fi
cat <<'EOF'
diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -1,3 +1,4 @@
 package main
 
 func A() {}
+func Final() {}
EOF
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_FAKE_ARGS", argsPath)

	b, err := input.ParseRepoDiff(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	argsRaw, _ := os.ReadFile(argsPath)
	args := string(argsRaw)
	if !strings.Contains(args, "--no-ext-diff") || !strings.Contains(args, "--no-textconv") {
		t.Fatalf("expected disabled filters, got args=%q", args)
	}
	if strings.Contains(args, "--cached") || strings.Contains(b.RawRedacted, "StagedOnly") {
		t.Fatalf("must not use staged/cached concat; args=%q raw=%s", args, b.RawRedacted)
	}
	if !strings.Contains(b.RawRedacted, "Final") {
		t.Fatalf("expected final worktree content: %s", b.RawRedacted)
	}
}

// TestParseRepoDiff_NoTextconvHelper ensures repository textconv helpers are not run.
func TestParseRepoDiff_NoTextconvHelper(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_TEMPLATE_DIR=",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			if strings.Contains(string(out), "Operation not permitted") {
				t.Skipf("git init blocked: %s", out)
			}
			t.Fatalf("%v: %v (%s)", args, err, out)
		}
	}
	run("git", "-c", "init.templateDir=", "init")
	run("git", "config", "user.email", "t@t")
	run("git", "config", "user.name", "t")

	helper := filepath.Join(dir, "fail-textconv.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\necho textconv-ran >&2\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.go diff=failconv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "config", "diff.failconv.textconv", helper)

	path := filepath.Join(dir, "f.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".")
	run("git", "commit", "-m", "init")
	if err := os.WriteFile(path, []byte("package main\n\nfunc Final() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b, err := input.ParseRepoDiff(dir)
	if err != nil {
		t.Fatalf("ParseRepoDiff should not invoke textconv helper: %v", err)
	}
	if !strings.Contains(b.RawRedacted, "Final") {
		t.Fatalf("expected diff content: %s", b.RawRedacted)
	}
}
