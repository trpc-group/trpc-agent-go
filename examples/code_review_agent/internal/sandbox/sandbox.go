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
// a cleaned environment, client-side bounded output, and advisory
// resource limits.
//
// The Executor never silently degrades to an unsafe backend: container/e2b
// construction failures are returned to the caller, and the local backend is
// only used when explicitly requested with UnsafeLocal=true. Run refuses to
// execute on any backend that does not honor CleanEnv (see
// Capabilities.SupportsCleanEnv) so review tools never inherit the host
// process environment.
//
// Resource limits (CPUPercent, MemoryMB, MaxPIDs) are passed through
// RunProgramSpec.Limits but are advisory: as of the current codeexecutor
// backends (container, e2b, local), none enforce these values. The real
// resource controls are the sandbox image (fixed toolchain), the permission
// policy (blocks resource-exhausting commands), and the per-command Timeout.
//
// Output bounding is client-side only: backends return Stdout/Stderr as
// fully-buffered strings, so MaxStdoutBytes/MaxStderrBytes cap what is
// retained in RunResult (and persisted to the report/DB) but do not
// prevent the backend from allocating the full output in memory. Redaction
// is applied before truncation so secrets split across the byte boundary
// are still caught.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	tcontainer "github.com/docker/docker/api/types/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
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
	// StatusSkipped indicates the sandbox check was intentionally not run
	// (e.g. diff-only mode has no staged repo to vet). Skipped runs do not
	// force the report conclusion to needs_human_review.
	StatusSkipped = "skipped"
)

// Default tuning used when Config fields are zero.
const (
	DefaultTimeout = 120 * time.Second

	DefaultMaxStdoutBytes int64 = 1 << 20 // 1 MiB
	DefaultMaxStderrBytes int64 = 1 << 20 // 1 MiB

	// Resource limit defaults applied to every RunProgram call. These are
	// advisory: current codeexecutor backends do not enforce them (see the
	// package doc comment). They are still passed through RunProgramSpec
	// so backends that add support in the future will pick them up.
	defaultCPUPercent = 100 // one full CPU, expressed in percent units
	defaultMemoryMB   = 1024
	defaultMaxPIDs    = 256

	// defaultContainerImage is the image used when Config.ContainerBaseImage
	// is empty. It must ship a Go toolchain so `go vet` and `go test` work
	// out of the box. staticcheck is NOT pre-installed here — reviewers
	// who need staticcheck should build the project's Dockerfile (which
	// bakes staticcheck in) and pass --container-base-image. The container
	// backend defaults to python:3.9-slim, which has no Go toolchain, so
	// overriding is required for the sandbox to be useful.
	defaultContainerImage = "golang:1.25-bookworm"

	// repoStageDir is the workspace-relative location the repository is
	// staged into. Keeping it in a subdirectory avoids colliding with the
	// skill scripts and output directories.
	repoStageDir = "repo"

	// SkillStageDir is the workspace-relative location skill scripts are
	// staged into so they are visible inside the sandbox filesystem.
	SkillStageDir = "skills"

	// workspaceExecID labels workspace spans/metadata for this agent.
	workspaceExecID = "code-review-agent"
)

// Config configures the sandbox Executor.
type Config struct {
	Backend Backend
	// UnsafeLocal must be true to use BackendLocal. It is ignored for
	// the container and e2b backends.
	UnsafeLocal bool
	// UnsafeAllowE2BNetwork must be true to use BackendE2B. E2B sandboxes
	// have network access by default and the SDK exposes no option to
	// disable it, so `go test` running untrusted code could exfiltrate
	// staged repo contents or finding evidence. Fail-closed: New returns
	// an error when Backend == BackendE2B and this field is false. The
	// container backend is unaffected (it defaults to NetworkMode=none).
	UnsafeAllowE2BNetwork bool
	// RepoPath is a host path that is staged read-only into each
	// workspace. May be empty to skip staging.
	RepoPath string
	// WorkDir configures the local backend's work root. Ignored for
	// container/e2b.
	WorkDir string
	// Timeout is the per-command timeout. Defaults to 120s.
	Timeout time.Duration
	// MaxStdoutBytes / MaxStderrBytes bound captured output. The limit is
	// applied client-side after the backend returns: it caps what is
	// retained in RunResult (and persisted downstream) but does not
	// prevent the backend from allocating the full output in memory.
	// Output past the limit is dropped and RunResult.Truncated is set.
	MaxStdoutBytes int64
	MaxStderrBytes int64
	// ContainerBaseImage overrides the Docker image used by the container
	// backend. When empty, defaultContainerImage (golang:1.25-bookworm)
	// is used so `go vet`/`go test` work without a custom build. Pass the
	// project's code-review-sandbox:latest image (built from the
	// Dockerfile) to also get staticcheck. Ignored for e2b/local.
	// Borrowed from competitor PR #2243.
	ContainerBaseImage string
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
	eng    codeexecutor.Engine
	closer io.Closer // may be nil (local backend has no Close)
	cfg    Config
}

// New constructs an Executor for the configured backend.
//
// New is fail-closed: if the selected backend is unavailable (for example
// Docker is not running for the container backend) the error is returned to
// the caller. New never falls back from container/e2b to local. The local
// backend is only used when cfg.Backend == BackendLocal and cfg.UnsafeLocal
// is true. The e2b backend is only used when cfg.UnsafeAllowE2BNetwork is
// true (e2b sandboxes have network access and the SDK exposes no way to
// disable it).
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

	eng, closer, err := buildEngine(cfg)
	if err != nil {
		return nil, err
	}
	if eng == nil {
		return nil, fmt.Errorf("sandbox: %s backend produced a nil engine", cfg.Backend)
	}
	return &Executor{eng: eng, closer: closer, cfg: cfg}, nil
}

// buildEngine constructs the Engine for the configured backend and returns
// the backend's Closer so the caller can release Docker/E2B resources when
// the Executor is shut down. The closer is nil for the local backend (it
// has no Close method). It returns an error (never a silent local fallback)
// when the requested backend fails.
func buildEngine(cfg Config) (codeexecutor.Engine, io.Closer, error) {
	switch cfg.Backend {
	case BackendContainer:
		image := cfg.ContainerBaseImage
		if image == "" {
			// Default to a Go-enabled image so `go vet`/`go test` work
			// without requiring the user to build the project's Dockerfile.
			// The container backend's own default (python:3.9-slim) has no
			// Go toolchain, which would make every sandbox run fail.
			image = defaultContainerImage
		}
		copts := []containerexec.Option{
			containerexec.WithContainerConfig(tcontainer.Config{
				Image:      image,
				WorkingDir: "/",
				Cmd:        []string{"tail", "-f", "/dev/null"},
				Tty:        true,
			}),
		}
		ce, err := containerexec.New(copts...)
		if err != nil {
			return nil, nil, fmt.Errorf("sandbox: container backend unavailable: %w", err)
		}
		return ce.Engine(), ce, nil
	case BackendE2B:
		if !cfg.UnsafeAllowE2BNetwork {
			return nil, nil, errors.New(
				"sandbox: e2b backend requires UnsafeAllowE2BNetwork=true " +
					"(e2b sandboxes have network access and the SDK exposes no option to disable it; " +
					"use the container backend for network-isolated review)")
		}
		ce, err := e2bexec.New()
		if err != nil {
			return nil, nil, fmt.Errorf("sandbox: e2b backend unavailable: %w", err)
		}
		return ce.Engine(), ce, nil
	case BackendLocal:
		if !cfg.UnsafeLocal {
			return nil, nil, errors.New("sandbox: local backend requires UnsafeLocal=true")
		}
		var lopts []localexec.CodeExecutorOption
		if cfg.WorkDir != "" {
			lopts = append(lopts, localexec.WithWorkDir(cfg.WorkDir))
		}
		// localexec.CodeExecutor has no Close method; return nil closer.
		return localexec.New(lopts...).Engine(), nil, nil
	default:
		return nil, nil, fmt.Errorf("sandbox: unknown backend %q", cfg.Backend)
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

// StageDirectory stages a host directory into the workspace at the given
// workspace-relative path. It is used to stage skill scripts alongside the
// read-only repo so the sandbox can execute skill-defined commands.
func (e *Executor) StageDirectory(ctx context.Context, ws codeexecutor.Workspace, src, to string, readOnly bool) error {
	fs := e.eng.FS()
	if fs == nil {
		return errors.New("sandbox: engine has no filesystem interface")
	}
	return fs.StageDirectory(ctx, ws, src, to, codeexecutor.StageOptions{ReadOnly: readOnly})
}

// Run executes a command via the framework Engine with a cleaned
// environment, client-side bounded output, and advisory resource limits.
// It never panics on command failure; failures are reflected in
// RunResult.Status.
//
// Resource limits are advisory: RunProgramSpec.Limits is populated but
// current codeexecutor backends (container, e2b, local) do not enforce
// CPUPercent/MemoryMB/MaxPIDs. The real resource controls are the sandbox
// image (fixed toolchain), the permission policy (blocks resource-
// exhausting commands like `find /` or `yes`), and the per-command
// Timeout.
//
// Output bounding is client-side: the backend returns Stdout/Stderr as
// fully-buffered strings, so MaxStdoutBytes/MaxStderrBytes cap only what
// is retained in RunResult (and persisted downstream). To avoid an extra
// full-string copy of the backend output, redaction and truncation
// operate directly on the []byte returned by redact.TextBytes via
// bytes.Reader. Redaction runs before truncation so a secret split across
// the byte boundary is still caught.
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
		if res.TimedOut {
			status = StatusTimeout
		}
		return RunResult{
			Status:   status,
			ExitCode: -1,
			Stderr:   []byte(err.Error()),
		}, nil
	}

	// Redact sensitive patterns from captured output before truncation so
	// secrets split across the byte boundary are caught. This is
	// defense-in-depth: the permission layer should block exfiltration
	// commands, but a tool may print secrets that exist in the staged repo.
	// Operate on []byte via bytes.Reader to avoid an extra full-string copy
	// (res.Stdout is already a string; redact.TextBytes returns []byte).
	stdoutBytes, _ := redact.TextBytes([]byte(res.Stdout))
	stderrBytes, _ := redact.TextBytes([]byte(res.Stderr))

	stdout, outTrunc := limitedRead(bytes.NewReader(stdoutBytes), e.cfg.MaxStdoutBytes)
	stderr, errTrunc := limitedRead(bytes.NewReader(stderrBytes), e.cfg.MaxStderrBytes)

	status := StatusSuccess
	if res.TimedOut {
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

// Close releases the workspace resources and shuts down the backend
// (Docker client / E2B client). It must be called exactly once when the
// Executor is no longer needed; calling it twice may return an error from
// the backend but is otherwise safe. Workspace cleanup errors take
// precedence over backend-close errors so callers see the more actionable
// failure first. The context is used only for workspace cleanup; backend
// close is synchronous and does not honour cancellation (the underlying
// HTTP clients close immediately).
func (e *Executor) Close(ctx context.Context, ws codeexecutor.Workspace) error {
	var firstErr error
	mgr := e.eng.Manager()
	if mgr != nil {
		if err := mgr.Cleanup(ctx, ws); err != nil {
			firstErr = fmt.Errorf("sandbox: cleanup workspace: %w", err)
		}
	}
	// Close the backend so Docker/E2B clients release their connections.
	// Without this the *CodeExecutor is garbage-collected eventually but
	// may hold open HTTP keep-alive sockets or sandbox slots until then.
	if e.closer != nil {
		if err := e.closer.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sandbox: close backend: %w", err)
		}
	}
	return firstErr
}

// buildSandboxEnv constructs the minimal, allowlisted environment for a
// spawned process. Only PATH, GOPATH, GOCACHE, GOPROXY and WORKSPACE_DIR are
// injected; os.Environ is never called. When host GOPATH or GOCACHE is empty,
// they default to workspace-local cache paths so Go commands work in a clean
// sandbox. GOPROXY defaults to "off" when unset by the user, enforcing the
// skill's offline-friendly safety claim; an explicit user value always wins.
// Caller-supplied extra values are merged on top.
func buildSandboxEnv(ws codeexecutor.Workspace, extra map[string]string) map[string]string {
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		gopath = filepath.Join(ws.Path, ".gopath")
	}
	gocache := os.Getenv("GOCACHE")
	if gocache == "" {
		gocache = filepath.Join(ws.Path, ".gocache")
	}
	goproxy := os.Getenv("GOPROXY")
	if goproxy == "" {
		goproxy = "off"
	}
	env := map[string]string{
		"PATH":                          os.Getenv("PATH"),
		"GOPATH":                        gopath,
		"GOCACHE":                       gocache,
		"GOPROXY":                       goproxy,
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
