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

	ex, closer, err := newWorkspaceExecutor(opts)
	if err != nil {
		return nil, nil, err
	}
	closeExec := func() {
		if closer != nil {
			_ = closer()
		}
	}
	ws, err := ex.CreateWorkspace(ctx, opts.TaskID, codeexecutor.WorkspacePolicy{Isolated: true})
	if err != nil {
		closeExec()
		return nil, nil, fmt.Errorf("create workspace: %w", err)
	}
	cleanup := makeCleanup(closer, func() { _ = ex.Cleanup(ctx, ws) })
	if err := stageWorkspace(ctx, ex, ws, opts); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("stage workspace: %w", err)
	}
	return &runEnv{exec: ex, ws: ws, ready: true}, cleanup, nil
}

func makeCleanup(closer func() error, cleanupWS func()) func() {
	return func() {
		if cleanupWS != nil {
			cleanupWS()
		}
		if closer != nil {
			_ = closer()
		}
	}
}

func newWorkspaceExecutor(opts Options) (workspaceExecutor, func() error, error) {
	switch opts.Runtime {
	case RuntimeContainer:
		ex, err := newContainerCodeExecutor(opts.SkillsRoot)
		if err != nil {
			return nil, nil, err
		}
		we, err := asWorkspaceExecutor(ex)
		if err != nil {
			_ = ex.Close()
			return nil, nil, err
		}
		return we, ex.Close, nil
	case RuntimeE2B:
		ex, err := e2bexec.New()
		if err != nil {
			return nil, nil, fmt.Errorf("e2b sandbox: %w (set E2B_API_KEY)", err)
		}
		we, err := asWorkspaceExecutor(ex)
		if err != nil {
			_ = ex.Close()
			return nil, nil, err
		}
		return we, ex.Close, nil
	case RuntimeLocal:
		ex := localexec.New(
			localexec.WithTimeout(opts.Timeout),
			localexec.WithWorkspaceAutoInputs(false),
		)
		return ex, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported runtime: %s", opts.Runtime)
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

// workspaceGoEnv returns a clean Go env for sandbox checks.
// When module deps cannot be resolved offline (no vendor / cache), ok is false
// and diag explains why go vet/test should be skipped.
func workspaceGoEnv(ctx context.Context, env *runEnv, opts Options) (map[string]string, bool, string) {
	base := sandboxEnv()
	base["GOPROXY"] = "off"
	base["GOSUMDB"] = "off"

	tryList := func(extra map[string]string) (codeexecutor.RunResult, error) {
		merged := map[string]string{}
		for k, v := range base {
			merged[k] = v
		}
		for k, v := range extra {
			merged[k] = v
		}
		return env.exec.RunProgram(ctx, env.ws, codeexecutor.RunProgramSpec{
			Cmd:      "go",
			Args:     []string{"list", "./..."},
			Cwd:      "work/repo",
			Timeout:  opts.Timeout,
			CleanEnv: true,
			Env:      merged,
		})
	}

	if result, err := tryList(map[string]string{"GOFLAGS": "-mod=vendor"}); err == nil && result.ExitCode == 0 {
		base["GOFLAGS"] = "-mod=vendor"
		return base, true, ""
	}
	if result, err := tryList(nil); err == nil && result.ExitCode == 0 {
		return base, true, ""
	}

	result, err := tryList(nil)
	diag := "go module dependencies unavailable in network-isolated sandbox; vendor the module or stage a module cache"
	if result.Stderr != "" {
		diag = diag + ": " + truncate(result.Stderr)
	} else if result.Stdout != "" {
		diag = diag + ": " + truncate(result.Stdout)
	} else if err != nil {
		diag = diag + ": " + err.Error()
	}
	return base, false, diag
}

func cleanHostEnv() []string {
	env := sandboxEnv()
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// ResolveSkillsRoot finds the skills directory containing code-review.
func ResolveSkillsRoot(root string) string {
	if isSafeSkillDir(filepath.Join(root, skillName)) {
		return root
	}
	cwd, err := os.Getwd()
	if err != nil {
		return root
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "skills")
		if isSafeSkillDir(filepath.Join(candidate, skillName)) {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return root
}

func isSafeSkillDir(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

// CloseCodeExecutor releases container or E2B backends when they support Close.
func CloseCodeExecutor(exec codeexecutor.CodeExecutor) error {
	if exec == nil {
		return nil
	}
	if c, ok := exec.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

// Ensure container.CodeExecutor is a workspaceExecutor at compile time.
var _ workspaceExecutor = (*containerexec.CodeExecutor)(nil)
