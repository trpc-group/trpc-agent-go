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

// TestParseRepoDiff_StagedPlusUnstaged verifies ParseRepoDiff uses `git diff HEAD`
// (final worktree) rather than concatenating staged and unstaged patches.
//
// A fake git on PATH records argv and returns a HEAD-to-worktree patch that
// contains only the final content (Final), not an intermediate staged line
// (StagedOnly). This avoids creating a nested .git which some environments
// block.
func TestParseRepoDiff_StagedPlusUnstaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git helper is a POSIX shell script")
	}
	binDir := t.TempDir()
	argsPath := filepath.Join(binDir, "git.args")
	script := filepath.Join(binDir, "git")
	// Fake git: require -C <dir> diff HEAD, reject --cached / bare "diff".
	body := `#!/bin/sh
printf '%s\n' "$@" > "$GIT_FAKE_ARGS"
# Walk args: accept ... -C <dir> diff HEAD
ok=0
prev=""
for a in "$@"; do
  if [ "$prev" = "diff" ] && [ "$a" = "HEAD" ]; then ok=1; fi
  if [ "$a" = "--cached" ] || [ "$a" = "--staged" ]; then
    echo "fake-git: unexpected cached/staged diff" >&2
    exit 2
  fi
  prev="$a"
done
if [ "$ok" != 1 ]; then
  echo "fake-git: expected git ... diff HEAD, got: $*" >&2
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
	if !strings.Contains(args, "diff") || !strings.Contains(args, "HEAD") {
		t.Fatalf("expected git diff HEAD, got args=%q", args)
	}
	if strings.Contains(args, "--cached") || strings.Contains(b.RawRedacted, "StagedOnly") {
		t.Fatalf("must not use staged/cached concat; args=%q raw=%s", args, b.RawRedacted)
	}
	if !strings.Contains(b.RawRedacted, "Final") {
		t.Fatalf("expected final worktree content: %s", b.RawRedacted)
	}
	if len(b.Files) != 1 {
		t.Fatalf("files=%d want 1", len(b.Files))
	}
	var sawFinal bool
	for _, h := range b.Files[0].Hunks {
		for _, l := range h.Lines {
			if l.Kind == '+' && strings.Contains(l.Text, "Final") {
				sawFinal = true
				if l.NewLineNo <= 0 {
					t.Fatalf("missing new line no for Final: %+v", l)
				}
			}
		}
	}
	if !sawFinal {
		t.Fatal("did not find Final addition with line numbers")
	}
}
