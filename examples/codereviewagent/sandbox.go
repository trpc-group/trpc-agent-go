//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
)

const sandboxOutputLimit = 4096

type sandboxRunner interface {
	Run(context.Context, string, []string) sandboxRun
}

type drySandbox struct {
	fail bool
}

func (s drySandbox) Run(_ context.Context, _ string, command []string) sandboxRun {
	if s.fail {
		return sandboxRun{Status: "failed", Command: command, ExitCode: -1, Error: "injected sandbox failure"}
	}
	return sandboxRun{Status: "dry_run", Command: command, Output: "sandbox execution skipped in deterministic dry-run mode"}
}

type managedSandbox struct {
	root string
}

func (s managedSandbox) Run(ctx context.Context, taskID string, command []string) sandboxRun {
	started := time.Now()
	runtime := sandbox.NewRuntime(
		sandbox.WithWorkspaceRoot(s.root),
		sandbox.WithPermissionProfile(sandbox.WorkspaceWriteProfile()),
		sandbox.WithSessionPolicy(sandbox.SessionPolicy{
			Persistence:    sandbox.SessionPersistencePerTurn,
			RunConcurrency: sandbox.SessionRunConcurrencySerial,
		}),
		sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
			Inherit:              sandbox.ShellEnvironmentPolicyInheritCore,
			ApplyDefaultExcludes: true,
			IncludeOnly:          []string{"PATH", "HOME", "TMP", "TEMP", "SYSTEMROOT"},
		}),
		sandbox.WithOutputMaxBytes(sandboxOutputLimit),
		sandbox.WithDefaultTimeout(10*time.Second),
	)
	workspace, err := runtime.CreateWorkspace(ctx, taskID, codeexecutor.WorkspacePolicy{Isolated: true, MaxDiskBytes: 8 << 20})
	if err != nil {
		return failedSandboxRun(command, started, err)
	}
	defer runtime.Cleanup(context.Background(), workspace)
	files := []codeexecutor.PutFile{
		{Path: "work/go.mod", Content: []byte("module reviewprobe\n\ngo 1.24\n"), Mode: 0o644},
		{Path: "work/review_probe_test.go", Content: []byte("package reviewprobe\n\nimport \"testing\"\n\nfunc TestProbe(t *testing.T) {}\n"), Mode: 0o644},
	}
	if err := runtime.PutFiles(ctx, workspace, files); err != nil {
		return failedSandboxRun(command, started, err)
	}
	result, runErr := runtime.RunProgram(ctx, workspace, codeexecutor.RunProgramSpec{
		Cmd: command[0], Args: command[1:], Cwd: "work", Timeout: 10 * time.Second,
		Limits: codeexecutor.ResourceLimits{CPUPercent: 100, MemoryMB: 256, MaxPIDs: 64},
	})
	output := strings.TrimSpace(strings.Join([]string{result.Stdout, result.Stderr}, "\n"))
	run := sandboxRun{
		Status: "passed", Command: append([]string(nil), command...), ExitCode: result.ExitCode,
		TimedOut: result.TimedOut, Output: output, DurationMS: time.Since(started).Milliseconds(),
		OutputCapped: len(result.Stdout) >= sandboxOutputLimit || len(result.Stderr) >= sandboxOutputLimit,
	}
	if runErr != nil {
		run.Status = "failed"
		run.Error = runErr.Error()
	}
	return run
}

func failedSandboxRun(command []string, started time.Time, err error) sandboxRun {
	return sandboxRun{
		Status: "failed", Command: append([]string(nil), command...), ExitCode: -1,
		Error: fmt.Sprintf("sandbox: %v", err), DurationMS: time.Since(started).Milliseconds(),
	}
}

func sandboxRoot(outputDir string) string {
	return filepath.Join(outputDir, "sandbox")
}
