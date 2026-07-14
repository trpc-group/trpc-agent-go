//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

// workspaceExecutor runs programs inside an isolated workspace.
type workspaceExecutor interface {
	CreateWorkspace(ctx context.Context, execID string, pol codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error)
	Cleanup(ctx context.Context, ws codeexecutor.Workspace) error
	PutFiles(ctx context.Context, ws codeexecutor.Workspace, files []codeexecutor.PutFile) error
	PutDirectory(ctx context.Context, ws codeexecutor.Workspace, hostPath, to string) error
	RunProgram(ctx context.Context, ws codeexecutor.Workspace, spec codeexecutor.RunProgramSpec) (codeexecutor.RunResult, error)
}

// NewCodeExecutor builds the sandbox backend for the selected runtime.
func NewCodeExecutor(opts Options) (codeexecutor.CodeExecutor, error) {
	switch opts.Runtime {
	case RuntimeContainer:
		return newContainerCodeExecutor(opts.SkillsRoot)
	case RuntimeE2B:
		return e2bexec.New()
	case RuntimeSkip, RuntimeLocal:
		return localexec.New(
			localexec.WithTimeout(opts.Timeout),
			localexec.WithWorkspaceAutoInputs(false),
		), nil
	default:
		return nil, fmt.Errorf("unsupported runtime: %s", opts.Runtime)
	}
}

type runEnv struct {
	exec  workspaceExecutor
	ws    codeexecutor.Workspace
	ready bool
}

func prepareRunEnv(ctx context.Context, opts Options) (*runEnv, func(), error) {
	if !isWorkspaceRuntime(opts.Runtime) {
		return &runEnv{}, func() {}, nil
	}

	ex, err := newWorkspaceExecutor(opts)
	if err != nil {
		return nil, nil, err
	}
	ws, err := ex.CreateWorkspace(ctx, opts.TaskID, codeexecutor.WorkspacePolicy{Isolated: true})
	if err != nil {
		return nil, nil, fmt.Errorf("create workspace: %w", err)
	}
	cleanup := func() { _ = ex.Cleanup(ctx, ws) }
	if err := stageWorkspace(ctx, ex, ws, opts); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("stage workspace: %w", err)
	}
	return &runEnv{exec: ex, ws: ws, ready: true}, cleanup, nil
}

func newWorkspaceExecutor(opts Options) (workspaceExecutor, error) {
	switch opts.Runtime {
	case RuntimeContainer:
		ex, err := newContainerCodeExecutor(opts.SkillsRoot)
		if err != nil {
			return nil, err
		}
		return asWorkspaceExecutor(ex)
	case RuntimeE2B:
		ex, err := e2bexec.New()
		if err != nil {
			return nil, fmt.Errorf("e2b sandbox: %w (set E2B_API_KEY)", err)
		}
		return asWorkspaceExecutor(ex)
	case RuntimeLocal:
		return localexec.New(
			localexec.WithTimeout(opts.Timeout),
			localexec.WithWorkspaceAutoInputs(false),
		), nil
	default:
		return nil, fmt.Errorf("unsupported runtime: %s", opts.Runtime)
	}
}

func isWorkspaceRuntime(r Runtime) bool {
	return r == RuntimeLocal || r == RuntimeContainer || r == RuntimeE2B
}

func isIsolatedRuntime(r Runtime) bool {
	return r == RuntimeContainer || r == RuntimeE2B
}

func sandboxEnv() map[string]string {
	return map[string]string{
		"PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
		"LANG": "C.UTF-8",
	}
}

// ResolveSkillsRoot finds the skills directory containing code-review.
func ResolveSkillsRoot(root string) string {
	if stat, err := os.Stat(filepath.Join(root, skillName)); err == nil && stat.IsDir() {
		return root
	}
	cwd, err := os.Getwd()
	if err != nil {
		return root
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "skills")
		if stat, err := os.Stat(filepath.Join(candidate, skillName)); err == nil && stat.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return root
}

// Ensure container.CodeExecutor is a workspaceExecutor at compile time.
var _ workspaceExecutor = (*containerexec.CodeExecutor)(nil)

