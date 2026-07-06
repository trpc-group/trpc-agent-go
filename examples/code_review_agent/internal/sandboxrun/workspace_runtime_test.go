//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandboxrun

import (
	"context"
	"reflect"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestWorkspaceRuntimeBuildsCleanShellSpec(t *testing.T) {
	runner := &recordingRunner{
		result: codeexecutor.RunResult{
			Stdout:   "ok\n",
			ExitCode: 0,
		},
	}
	rt := WorkspaceRuntime{
		RuntimeName: "test-runtime",
		Engine: codeexecutor.NewEngine(
			nil,
			nil,
			runner,
		),
		Workspace: codeexecutor.Workspace{ID: "ws-1"},
		Cwd:       ".",
		Timeout:   7 * time.Second,
		Env: map[string]string{
			"PATH":           "/custom/bin",
			"GOPROXY":        "https://proxy.example",
			"OPENAI_API_KEY": "should-not-pass",
		},
	}

	res, err := rt.Run(context.Background(), "go test ./...")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Stdout != "ok\n" {
		t.Fatalf("Stdout = %q, want ok", res.Stdout)
	}
	if runner.spec.Cmd != "sh" {
		t.Fatalf("Cmd = %q, want sh", runner.spec.Cmd)
	}
	if !reflect.DeepEqual(runner.spec.Args, []string{"-c", "go test ./..."}) {
		t.Fatalf("Args = %#v, want shell command", runner.spec.Args)
	}
	if !runner.spec.CleanEnv {
		t.Fatal("CleanEnv = false, want true")
	}
	if runner.spec.Timeout != 7*time.Second {
		t.Fatalf("Timeout = %v, want 7s", runner.spec.Timeout)
	}
	if got := runner.spec.Env["GOPROXY"]; got != "https://proxy.example" {
		t.Fatalf("GOPROXY = %q, want allowlisted value", got)
	}
	if _, ok := runner.spec.Env["OPENAI_API_KEY"]; ok {
		t.Fatalf("OPENAI_API_KEY unexpectedly passed through env: %#v", runner.spec.Env)
	}
}

func TestWorkspaceRuntimeMapsTimedOutResultToDeadline(t *testing.T) {
	runner := &recordingRunner{
		result: codeexecutor.RunResult{
			TimedOut: true,
		},
	}
	rt := WorkspaceRuntime{
		RuntimeName: "test-runtime",
		Engine:      codeexecutor.NewEngine(nil, nil, runner),
		Workspace:   codeexecutor.Workspace{ID: "ws-1"},
	}

	_, err := rt.Run(context.Background(), "go test ./...")
	if err != context.DeadlineExceeded {
		t.Fatalf("Run() error = %v, want context deadline", err)
	}
}

type recordingRunner struct {
	spec   codeexecutor.RunProgramSpec
	result codeexecutor.RunResult
	err    error
}

func (r *recordingRunner) RunProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	r.spec = spec
	return r.result, r.err
}
