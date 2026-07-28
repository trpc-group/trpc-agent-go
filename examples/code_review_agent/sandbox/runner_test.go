//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandbox"
)

// TestLocalRunner_Timeout verifies related behavior.
func TestLocalRunner_Timeout(t *testing.T) {
	r := sandbox.LocalRunner{}
	limits := safety.DefaultLimits()
	limits.Timeout = 200 * time.Millisecond
	res := r.Run(context.Background(), sandbox.Spec{
		Command: "sleep 2",
	}, limits)
	if res.Summary.Status != "timeout" {
		t.Fatalf("status=%s err=%s", res.Summary.Status, res.Summary.Error)
	}
}

// TestLocalRunner_ExitOne records non-zero exit as failed (not ok/0).
func TestLocalRunner_ExitOne(t *testing.T) {
	r := sandbox.LocalRunner{}
	res := r.Run(context.Background(), sandbox.Spec{
		Command: "false",
	}, safety.DefaultLimits())
	if res.Summary.Status != "failed" {
		t.Fatalf("status=%s", res.Summary.Status)
	}
	if res.Summary.ExitCode == 0 {
		t.Fatalf("exit_code=%d want non-zero", res.Summary.ExitCode)
	}
}

// TestLocalRunner_OutputLimit verifies related behavior.
func TestLocalRunner_OutputLimit(t *testing.T) {
	r := sandbox.LocalRunner{}
	limits := safety.DefaultLimits()
	limits.MaxStdoutBytes = 32
	// printf is portable and does not depend on python3 being installed.
	res := r.Run(context.Background(), sandbox.Spec{
		Command: "printf %02000d 0",
	}, limits)
	if res.Summary.Status != "ok" && res.Summary.Status != "truncated" {
		t.Fatalf("probe failed: status=%s err=%s", res.Summary.Status, res.Summary.Error)
	}
	if res.Summary.StdoutBytes > 32 {
		t.Fatalf("stdout exceeded limit: %d", res.Summary.StdoutBytes)
	}
	if res.Summary.StdoutBytes == 0 {
		t.Fatal("expected truncated non-empty stdout from probe")
	}
}

// TestLocalRunner_ForbiddenEnvNotForwarded verifies CleanEnv + whitelist.
func TestLocalRunner_ForbiddenEnvNotForwarded(t *testing.T) {
	r := sandbox.LocalRunner{}
	res := r.Run(context.Background(), sandbox.Spec{
		Command: "env",
		Env:     []string{"SECRET_API_KEY=should-not-leak", "PATH=" + os.Getenv("PATH")},
	}, safety.DefaultLimits())
	if res.Summary.Status != "ok" {
		t.Fatalf("env probe failed: status=%s err=%s stderr=%q",
			res.Summary.Status, res.Summary.Error, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "PATH=") {
		t.Fatalf("probe did not run (missing PATH=): stdout=%q", res.Stdout)
	}
	if strings.Contains(res.Stdout, "should-not-leak") || strings.Contains(res.Stdout, "SECRET_API_KEY=") {
		t.Fatalf("forbidden env leaked: stdout=%q", res.Stdout)
	}
}

// TestLocalRunner_StagedDiffReadable proves run_checks.sh can read staged diff.
func TestLocalRunner_StagedDiffReadable(t *testing.T) {
	root := moduleRoot(t)
	diff := `diff --git a/pkg/worker/worker.go b/pkg/worker/worker.go
--- a/pkg/worker/worker.go
+++ b/pkg/worker/worker.go
@@ -1,3 +1,5 @@
 package worker
 
-func Start() {}
+func Start() {
+	go func() { doWork() }()
+}
`
	r := sandbox.LocalRunner{SkillsRoot: filepath.Join(root, "skills")}
	res := r.Run(context.Background(), sandbox.Spec{
		Command:    "skills/code-review/scripts/run_checks.sh",
		DiffText:   diff,
		SkillsRoot: filepath.Join(root, "skills"),
	}, safety.DefaultLimits())
	if res.Summary.Status != "ok" {
		t.Fatalf("status=%s err=%s stderr=%s", res.Summary.Status, res.Summary.Error, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "CR-CON-001") && !strings.Contains(res.Stdout, "goroutine") {
		t.Fatalf("expected sandbox finding from staged diff, stdout=%q", res.Stdout)
	}
}

// TestFailingRunner_DoesNotPanic verifies related behavior.
func TestFailingRunner_DoesNotPanic(t *testing.T) {
	r := sandbox.FailingRunner{Inner: sandbox.LocalRunner{}}
	res := r.Run(context.Background(), sandbox.Spec{
		Command: "skills/code-review/scripts/run_checks.sh",
	}, safety.DefaultLimits())
	if res.Summary.Status != "failed" {
		t.Fatalf("status=%s", res.Summary.Status)
	}
	if !strings.Contains(res.Stderr, "forced sandbox failure") {
		t.Fatalf("stderr=%q", res.Stderr)
	}
}

// TestNewRunner removed: Create is the single ownership-aware entry point.

// TestCreate_FakeHasNoCloser verifies fake Create owns nothing to close.
func TestCreate_FakeHasNoCloser(t *testing.T) {
	res, err := sandbox.Create(sandbox.CreateOptions{Name: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Closer != nil {
		t.Fatal("fake Create should not return a Closer")
	}
	if res.Runner.Name() != "fake" {
		t.Fatalf("name=%s", res.Runner.Name())
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(wd) == "sandbox" {
		return filepath.Dir(wd)
	}
	return wd
}
