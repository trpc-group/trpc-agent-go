//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sandbox provides a fail-closed execution layer over the
// trpc-agent-go codeexecutor Engine API. It provisions an isolated
// workspace, stages the repository read-only, and runs review tools with
// a cleaned environment, bounded output, and resource limits.
//
// The Executor never silently degrades to an unsafe backend: container/e2b
// construction failures are returned to the caller, and the local backend is
// only used when explicitly requested with UnsafeLocal=true. Run refuses to
// execute on any backend that does not honor CleanEnv (see
// Capabilities.SupportsCleanEnv) so review tools never inherit the host
// process environment.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

// Backend selects the execution backend used by the Executor.
type Backend string

const (
	// BackendContainer runs review tools inside a Docker container.
	BackendContainer Backend = "container"
	// BackendE2B runs review tools inside an E2B sandbox.
	BackendE2B Backend = "e2b"
	// BackendLocal runs review tools directly on the host. Only permitted
	// when Config.UnsafeLocal is true.
	BackendLocal Backend = "local"
)

// Run status values reported by Run.
const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusTimeout = "timeout"
)

// Default tuning used when Config fields are zero.
const (
	DefaultTimeout = 120 * time.Second

	DefaultMaxStdoutBytes int64 = 1 << 20 // 1 MiB
	DefaultMaxStderrBytes int64 = 1 << 20 // 1 MiB

	// Resource limit defaults applied to every RunProgram call.
	defaultCPUPercent = 100 // one full CPU, expressed in percent units
	defaultMemoryMB   = 1024
	defaultMaxPIDs    = 256

	// repoStageDir is the workspace-relative location the repository is
	// staged into. Keeping it in a subdirectory avoids colliding with the
	// workspace layout directories (skills/, work/, out/, runs/).
	repoStageDir = "repo"

	// workspaceExecID labels workspace spans/metadata for this agent.
	workspaceExecID = "code-review-agent"
)

// Config configures the sandbox Executor.
type Config struct {
	Backend Backend
	// UnsafeLocal must be true to use BackendLocal. It is ignored for
	// the container and e2b backends.
	UnsafeLocal bool
	// RepoPath is a host path that is staged read-only into each
	// workspace. May be empty to skip staging.
	RepoPath string
	// WorkDir configures the local backend's work root. Ignored for
	// container/e2b.
	WorkDir string
	// Timeout is the per-command timeout. Defaults to 120s.
	Timeout time.Duration
	// MaxStdoutBytes / MaxStderrBytes bound captured output. Output
	// past the limit is dropped and RunResult.Truncated is set.
	MaxStdoutBytes int64
	MaxStderrBytes int64
}

// RunSpec describes a single sandboxed command invocation.
type RunSpec struct {
	Cmd  string
	Args []string
	// Env is merged on top of the whitelisted sandbox env. Only these
	// keys reach the spawned process; os.Environ is never used.
	Env map[string]string
	// Cwd is relative to the workspace root.
	Cwd string
}

// RunResult captures the outcome of a sandboxed run. The pipeline persists
// this to the sandbox_run table.
type RunResult struct {
	Status    string // one of StatusSuccess / StatusFailed / StatusTimeout
	ExitCode  int
	Duration  time.Duration
	TimedOut  bool
	Truncated bool
	Stdout    []byte
	Stderr    []byte
}

// Executor wraps a codeexecutor.Engine with safe defaults.
type Executor struct {
	eng codeexecutor.Engine
	cfg Config
}

// New constructs an Executor for the configured backend.
//
// New is fail-closed: if the selected backend is unavailable (for example
// Docker is not running for the container backend) the error is returned to
// the caller. New never falls back from container/e2b to local. The local
// backend is only used when cfg.Backend == BackendLocal and cfg.UnsafeLocal
// is true.
func New(cfg Config) (*Executor, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxStdoutBytes <= 0 {
		cfg.MaxStdoutBytes = DefaultMaxStdoutBytes
	}
	if cfg.MaxStderrBytes <= 0 {
		cfg.MaxStderrBytes = DefaultMaxStderrBytes
	}

	eng, err := buildEngine(cfg)
	if err != nil {
		return nil, err
	}
	if eng == nil {
		return nil, fmt.Errorf("sandbox: %s backend produced a nil engine", cfg.Backend)
	}
	return &Executor{eng: eng, cfg: cfg}, nil
}

// buildEngine constructs the Engine for the configured backend. It returns
// an error (never a silent local fallback) when the requested backend fails.
func buildEngine(cfg Config) (codeexecutor.Engine, error) {
	switch cfg.Backend {
	case BackendContainer:
		ce, err := containerexec.New()
		if err != nil {
			return nil, fmt.Errorf("sandbox: container backend unavailable: %w", err)
		}
		return ce.Engine(), nil
	case BackendE2B:
		ce, err := e2bexec.New()
		if err != nil {
			return nil, fmt.Errorf("sandbox: e2b backend unavailable: %w", err)
		}
		return ce.Engine(), nil
	case BackendLocal:
		if !cfg.UnsafeLocal {
			return nil, errors.New("sandbox: local backend requires UnsafeLocal=true")
		}
		var lopts []localexec.CodeExecutorOption
		if cfg.WorkDir != "" {
			lopts = append(lopts, localexec.WithWorkDir(cfg.WorkDir))
		}
		return localexec.New(lopts...).Engine(), nil
	default:
		return nil, fmt.Errorf("sandbox: unknown backend %q", cfg.Backend)
	}
}

// CreateWorkspace provisions a workspace and, when Config.RepoPath is set,
// stages the repository read-only into it via the Engine's FS interface.
func (e *Executor) CreateWorkspace(ctx context.Context) (codeexecutor.Workspace, error) {
	mgr := e.eng.Manager()
	if mgr == nil {
		return codeexecutor.Workspace{}, errors.New("sandbox: engine has no workspace manager")
	}
	ws, err := mgr.CreateWorkspace(ctx, workspaceExecID, codeexecutor.WorkspacePolicy{})
	if err != nil {
		return codeexecutor.Workspace{}, fmt.Errorf("sandbox: create workspace: %w", err)
	}
	if e.cfg.RepoPath == "" {
		return ws, nil
	}
	fs := e.eng.FS()
	if fs == nil {
		return ws, errors.New("sandbox: engine has no filesystem interface")
	}
	stageErr := fs.StageDirectory(
		ctx, ws, e.cfg.RepoPath, repoStageDir,
		codeexecutor.StageOptions{ReadOnly: true},
	)
	if stageErr != nil {
		// Best-effort cleanup of the half-provisioned workspace so a
		// staging failure does not leak workspaces.
		_ = mgr.Cleanup(ctx, ws)
		return codeexecutor.Workspace{}, fmt.Errorf("sandbox: stage repo: %w", stageErr)
	}
	return ws, nil
}

// Run executes a command via the framework Engine with a cleaned
// environment, bounded output, and resource limits. It never panics on
// command failure; failures are reflected in RunResult.Status.
func (e *Executor) Run(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec RunSpec,
) (RunResult, error) {
	// Fail closed: backends that do not honor CleanEnv would inherit the
	// host environment, breaking the sandbox contract. Refuse to run.
	if !e.eng.Describe().SupportsCleanEnv {
		return RunResult{}, errors.New(
			"sandbox: backend does not support CleanEnv; refusing to run with inherited env")
	}
	runner := e.eng.Runner()
	if runner == nil {
		return RunResult{}, errors.New("sandbox: engine has no runner")
	}

	progSpec := codeexecutor.RunProgramSpec{
		Cmd:      spec.Cmd,
		Args:     spec.Args,
		Env:      buildSandboxEnv(ws, spec.Env),
		CleanEnv: true,
		Cwd:      spec.Cwd,
		Timeout:  e.cfg.Timeout,
		Limits: codeexecutor.ResourceLimits{
			CPUPercent: defaultCPUPercent,
			MemoryMB:   defaultMemoryMB,
			MaxPIDs:    defaultMaxPIDs,
		},
	}

	res, err := runner.RunProgram(ctx, ws, progSpec)
	if err != nil {
		// Infrastructure error (not a normal non-zero exit). Classify
		// without panicking so the pipeline still records a result.
		status := StatusFailed
		if ctx.Err() != nil {
			status = StatusTimeout
		}
		return RunResult{
			Status:   status,
			ExitCode: -1,
			Stderr:   []byte(err.Error()),
		}, nil
	}

	stdout, outTrunc := limitedRead(strings.NewReader(res.Stdout), e.cfg.MaxStdoutBytes)
	stderr, errTrunc := limitedRead(strings.NewReader(res.Stderr), e.cfg.MaxStderrBytes)

	status := StatusSuccess
	if ctx.Err() != nil || res.TimedOut {
		status = StatusTimeout
	} else if res.ExitCode != 0 {
		status = StatusFailed
	}

	return RunResult{
		Status:    status,
		ExitCode:  res.ExitCode,
		Duration:  res.Duration,
		TimedOut:  res.TimedOut,
		Truncated: outTrunc || errTrunc,
		Stdout:    stdout,
		Stderr:    stderr,
	}, nil
}

// Close releases the workspace resources.
func (e *Executor) Close(ctx context.Context, ws codeexecutor.Workspace) error {
	mgr := e.eng.Manager()
	if mgr == nil {
		return nil
	}
	if err := mgr.Cleanup(ctx, ws); err != nil {
		return fmt.Errorf("sandbox: cleanup workspace: %w", err)
	}
	return nil
}

// buildSandboxEnv constructs the minimal, allowlisted environment for a
// spawned process. Only PATH, GOPATH, GOCACHE, GOPROXY and WORKSPACE_DIR are
// injected; os.Environ is never called. Caller-supplied extra values are
// merged on top.
func buildSandboxEnv(ws codeexecutor.Workspace, extra map[string]string) map[string]string {
	env := map[string]string{
		"PATH":                          os.Getenv("PATH"),
		"GOPATH":                        os.Getenv("GOPATH"),
		"GOCACHE":                       os.Getenv("GOCACHE"),
		"GOPROXY":                       os.Getenv("GOPROXY"),
		codeexecutor.WorkspaceEnvDirKey: ws.Path,
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// limitedRead reads at most max bytes from r. If the source contained more
// data than max, the returned slice is capped to max and truncated is true.
func limitedRead(r io.Reader, max int64) ([]byte, bool) {
	if r == nil {
		return nil, false
	}
	if max < 0 {
		max = 0
	}
	// Read one extra byte to detect truncation without an additional read.
	b, readErr := io.ReadAll(io.LimitReader(r, max+1))
	if readErr != nil {
		return b, false
	}
	if int64(len(b)) > max {
		return b[:max], true
	}
	return b, false
}
