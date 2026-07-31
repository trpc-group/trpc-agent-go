//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestFakeRuntimeCoversSuccessFailureTimeoutAndTruncation(t *testing.T) {
	rt := NewFakeRuntime()
	rt.Enqueue(Result{CommandID: "go-test", Stdout: "ok", ExitCode: 0})
	rt.Enqueue(Result{CommandID: "go-vet", Stderr: "bad", ExitCode: 1})
	rt.Enqueue(Result{CommandID: "staticcheck", Stdout: "123456789", TimedOut: true})
	if err := rt.Stage(context.Background(), Snapshot{Digest: "snap"}); err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	first, err := rt.Run(context.Background(), Command{ID: "go-test", Timeout: time.Second, MaxStdoutBytes: 4, MaxStderrBytes: 4})
	if err != nil || first.Stdout != "ok" {
		t.Fatalf("first = %#v err=%v", first, err)
	}
	second, _ := rt.Run(context.Background(), Command{ID: "go-vet", Timeout: time.Second})
	if second.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1", second.ExitCode)
	}
	third, _ := rt.Run(context.Background(), Command{ID: "staticcheck", Timeout: time.Nanosecond, MaxStdoutBytes: 4, MaxStderrBytes: 4})
	if !third.TimedOut || third.Stdout != "1234" || !third.Truncated || third.TruncationReason != "stdout_limit" {
		t.Fatalf("timeout/truncation not recorded: %#v", third)
	}
	if rt.StageCount() != 1 || rt.RunCount() != 3 {
		t.Fatalf("counts stage=%d run=%d", rt.StageCount(), rt.RunCount())
	}
}

func TestClassifyStaticcheckDependencyUnavailable(t *testing.T) {
	got := classifyResult(Result{
		CommandID: "staticcheck", ExitCode: 3,
		Stderr: "dependency_unavailable: staticcheck\n",
	})
	if got.Outcome != OutcomeDependencyUnavailable {
		t.Fatalf("outcome = %q, want %q", got.Outcome, OutcomeDependencyUnavailable)
	}
}

func TestContainerScriptPathIsReachableFromRepoWorkdir(t *testing.T) {
	for cwd, want := range map[string]string{
		"":                  "../../skills/code-review/scripts/run_checks.sh",
		"work/repo":         "../../skills/code-review/scripts/run_checks.sh",
		"work/repo/cmd/app": "../../../../skills/code-review/scripts/run_checks.sh",
	} {
		if got := containerScriptPath(cwd); got != want {
			t.Fatalf("cwd %q script path = %q, want %q", cwd, got, want)
		}
	}
}

func TestContainerReadOnlyFixupCommandIsSafe(t *testing.T) {
	spec := containerReadOnlyFixupSpec("work/repo")
	if spec.Cmd != "chmod" {
		t.Fatalf("cmd = %q", spec.Cmd)
	}
	wantArgs := []string{"-R", "a+rX,a-w", "work/repo"}
	if len(spec.Args) != len(wantArgs) {
		t.Fatalf("args = %#v", spec.Args)
	}
	for i := range wantArgs {
		if spec.Args[i] != wantArgs[i] {
			t.Fatalf("args = %#v, want %#v", spec.Args, wantArgs)
		}
	}
	if !spec.CleanEnv || spec.Env["PATH"] == "" || spec.Timeout <= 0 {
		t.Fatalf("unsafe fixup spec: %#v", spec)
	}
}

func TestContainerPutFilesFromDirectoryNormalizesModes(t *testing.T) {
	dir := t.TempDir()
	writeFileMode(t, dir, "go.mod", "module example.com/repo\n", 0o600)
	writeFileMode(t, dir, "scripts/run_checks.sh", "#!/bin/sh\nexit 0\n", 0o700)
	files, err := putFilesFromDirectory(dir, "work/repo")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]uint32{}
	for _, f := range files {
		got[f.Path] = f.Mode
	}
	if got["work/repo/go.mod"] != 0o644 {
		t.Fatalf("go.mod mode = %#o", got["work/repo/go.mod"])
	}
	if got["work/repo/scripts/run_checks.sh"] != 0o755 {
		t.Fatalf("script mode = %#o", got["work/repo/scripts/run_checks.sh"])
	}
}

func writeFileMode(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestContainerRuntimeOwnsAndClosesExecutor(t *testing.T) {
	closed := 0
	rt := &ContainerRuntime{close: func() error {
		closed++
		return nil
	}}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("executor close count = %d, want 1", closed)
	}
}

func TestContainerRuntimeRunReturnsRunnerErrorWithoutClassifyingEmptyResult(t *testing.T) {
	want := errors.New("runner failed")
	rt := &ContainerRuntime{engine: codeexecutor.NewEngine(nil, nil, errorRunner{err: want}), ws: codeexecutor.Workspace{Path: "workspace"}}
	got, err := rt.Run(context.Background(), Command{ID: "go-test", Args: []string{"test", "."}, Timeout: time.Second})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if got != (Result{}) {
		t.Fatalf("result = %#v, want zero result on runner error", got)
	}
}

type errorRunner struct{ err error }

func (r errorRunner) RunProgram(context.Context, codeexecutor.Workspace, codeexecutor.RunProgramSpec) (codeexecutor.RunResult, error) {
	return codeexecutor.RunResult{ExitCode: 127, Stderr: "should not classify"}, r.err
}

func TestRunChecksScriptBoundsEmittedOutputBeforeRuntimeCapture(t *testing.T) {
	script := filepath.Join("..", "..", "skills", "code-review", "scripts", "run_checks.sh")
	cmd := exec.Command("bash", script, "emit-large-output")
	cmd.Env = append(os.Environ(), "CODE_REVIEW_OUTPUT_LIMIT_BYTES=64")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("large output helper unexpectedly succeeded: %s", out)
	}
	text := string(out)
	if len(out) > 512 {
		t.Fatalf("script returned unbounded output: %d bytes", len(out))
	}
	if !strings.Contains(text, "output_truncated: stdout_limit") || !strings.Contains(text, "output_truncated: stderr_limit") {
		t.Fatalf("missing truncation markers in output:\n%s", text)
	}
}
