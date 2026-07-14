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
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

// workspaceExecutor runs programs inside an isolated workspace.
type workspaceExecutor interface {
	CreateWorkspace(ctx context.Context, execID string, pol codeexecutor.WorkspacePolicy) (codeexecutor.Workspace, error)
	Cleanup(ctx context.Context, ws codeexecutor.Workspace) error
	PutFiles(ctx context.Context, ws codeexecutor.Workspace, files []codeexecutor.PutFile) error
	RunProgram(ctx context.Context, ws codeexecutor.Workspace, spec codeexecutor.RunProgramSpec) (codeexecutor.RunResult, error)
}

type runEnv struct {
	exec  workspaceExecutor
	ws    codeexecutor.Workspace
	ready bool
}

func prepareRunEnv(ctx context.Context, opts Options) (*runEnv, func(), error) {
	if opts.Runtime != RuntimeLocal && opts.Runtime != RuntimeContainer {
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
		skillsRoot, err := filepath.Abs(opts.SkillsRoot)
		if err != nil {
			return nil, err
		}
		return containerexec.New(
			containerexec.WithBindMount(skillsRoot, "/opt/trpc-agent/skills", "ro"),
			containerexec.WithAutoInputs(true),
		)
	case RuntimeLocal:
		return localexec.New(
			localexec.WithTimeout(opts.Timeout),
			localexec.WithWorkspaceAutoInputs(false),
		), nil
	default:
		return nil, fmt.Errorf("unsupported runtime: %s", opts.Runtime)
	}
}

func sandboxEnv() map[string]string {
	return map[string]string{
		"PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
		"LANG": "C.UTF-8",
	}
}

func resolveSkillsRoot(root string) string {
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
