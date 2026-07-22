//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"fmt"
	"os"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestRunProgramHonorsSpecOutputLimit(t *testing.T) {
	runtime := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
		WithOutputMaxBytes(64),
	)
	workspace, err := runtime.CreateWorkspace(
		context.Background(), "spec-output-limit", codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.RunProgram(context.Background(), workspace, codeexecutor.RunProgramSpec{
		Cmd: os.Args[0], Args: []string{"-test.run=TestRunProgramOutputLimitHelper"},
		Env: map[string]string{"TRPC_SANDBOX_OUTPUT_HELPER": "1"}, MaxOutputBytes: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "abcd\n[truncated]\n" || result.Stderr != "uvwx\n[truncated]\n" ||
		!result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("unexpected bounded result: %+v", result)
	}
}

func TestRunProgramOutputLimitHelper(t *testing.T) {
	if os.Getenv("TRPC_SANDBOX_OUTPUT_HELPER") != "1" {
		return
	}
	_, _ = fmt.Fprint(os.Stdout, "abcdef")
	_, _ = fmt.Fprint(os.Stderr, "uvwxyz")
	os.Exit(0)
}
