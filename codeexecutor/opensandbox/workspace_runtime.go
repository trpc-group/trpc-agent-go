//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package opensandbox

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// Compile-time checks that workspaceRuntime satisfies the workspace
// interfaces required by codeexecutor.NewEngineWithCapabilities.
var (
	_ codeexecutor.WorkspaceManager = (*workspaceRuntime)(nil)
	_ codeexecutor.WorkspaceFS      = (*workspaceRuntime)(nil)
	_ codeexecutor.ProgramRunner    = (*workspaceRuntime)(nil)
)

const (
	// Base directory inside the OpenSandbox sandbox where per-execution
	// workspaces are created. /tmp is writable in the default
	// code-interpreter image.
	defaultSandboxRunBase = "/tmp/run"

	defaultCreateTimeout = 15 * time.Second
	defaultRmTimeout     = 15 * time.Second
	// defaultStageTimeout must stay within the default requestTimeout
	// budget (minRequestTimeout = executionTimeout + requestTimeoutBuffer
	// = 30s + 10s = 40s). The SDK applies requestTimeout to the HTTP
	// request, so a runBash timeout exceeding it would be killed by
	// the HTTP client with an unclear infrastructure error rather than
	// a clean budget rejection. 30s leaves headroom while still allowing
	// chmod -R on large directory trees.
	defaultStageTimeout   = 30 * time.Second
	defaultCollectTimeout = 30 * time.Second
	defaultRunTimeout     = 30 * time.Second

	// Maximum bytes read back from the sandbox for a single file when
	// collecting outputs.
	maxReadSizeBytes = 4 * 1024 * 1024 // 4 MiB

	// Aggregate limits for Collect: at most maxCollectFiles files and
	// maxCollectTotalBytes total content are returned, preventing
	// model-generated code from creating thousands of matching files
	// and exhausting host memory. Consistent with other executors
	// (container, e2b, local) which use the same defaults.
	maxCollectFiles      = 100
	maxCollectTotalBytes = 64 * 1024 * 1024 // 64 MiB

	// Maximum bytes of stdout/stderr accumulated in host memory per
	// RunProgram call. Without this, a continuously-printing remote
	// command can consume unbounded host memory even with an execution
	// timeout.
	maxCommandOutputBytes = 1024 * 1024 // 1 MiB each for stdout and stderr

	// Maximum total bytes of aggregated output across all code blocks
	// in a single ExecuteCode call. Each block's RunProgram already
	// caps stdout/stderr at maxCommandOutputBytes, but with N blocks
	// the aggregate could reach N * 2 * maxCommandOutputBytes. This
	// limit prevents a long sequence of verbose blocks from consuming
	// unbounded host memory.
	maxAggregateOutputBytes = 4 * 1024 * 1024 // 4 MiB total
)

// workspaceRuntime implements WorkspaceManager / WorkspaceFS /
// ProgramRunner for the OpenSandbox sandbox.
//
// The runtime's method set is split across multiple files for
// readability:
//
//   - workspace_runtime.go: struct, constructor, CreateWorkspace,
//     Cleanup, validateWorkspace, sandbox accessor, cleanupContext
//   - workspace_files.go:   PutFiles, PutDirectory, walkAndUpload,
//     StageDirectory, symlink/path helpers (pathUnder,
//     resolveSandboxPath, resolveSandboxAncestor, removeSymlinkIfExists)
//   - workspace_collect.go: Collect, resolveSandboxPaths, readFile,
//     listFilesByGlob, StageInputs, CollectOutputs
//   - workspace_run.go:     RunProgram, resolveRunCwd, ExecuteInline,
//     runBash, cappedBuffer/cappedOutputBuffer, shellQuote, sanitize,
//     stableWorkspaceHash, isTimeoutErr, formatExecutionError
type workspaceRuntime struct {
	ce  *CodeExecutor
	cfg runtimeConfig

	// runSeq generates monotonically increasing run-directory IDs to
	// guarantee uniqueness even when two RunProgram calls land in the
	// same nanosecond. Uses atomic for concurrent safety.
	runSeq uint64
}

type runtimeConfig struct {
	runBase              string
	workspacePersistence WorkspacePersistenceMode
}

func newWorkspaceRuntime(c *CodeExecutor) *workspaceRuntime {
	base := strings.TrimSpace(c.sandboxRunBase)
	if base == "" {
		base = defaultSandboxRunBase
	}
	return &workspaceRuntime{ce: c, cfg: runtimeConfig{
		runBase:              path.Clean(base),
		workspacePersistence: c.workspacePersistence,
	}}
}

// validateRunBase enforces that the sandbox runBase is an absolute
// POSIX path that is not root and does not contain ".." escape
// components. This prevents a misconfigured runBase (e.g.
// "/tmp/run/../../etc") from allowing workspace paths to be created
// outside the intended directory. An empty base is valid (the default
// is applied by newWorkspaceRuntime).
func validateRunBase(base string) error {
	if base == "" {
		return nil
	}
	if !path.IsAbs(base) {
		return fmt.Errorf("opensandbox: runBase %q is not an absolute path", base)
	}
	if path.Clean(base) == "/" {
		return errors.New("opensandbox: runBase must not be \"/\"")
	}
	for _, part := range strings.Split(base, "/") {
		if part == ".." {
			return fmt.Errorf("opensandbox: runBase %q contains \"..\" escape", base)
		}
	}
	return nil
}

// cleanupContext returns a context detached from the parent's
// cancellation signal, with a short timeout. Deferred workspace
// cleanup (rm -rf) must use this instead of the original context:
// if the parent context is already cancelled/timed out, cleanup
// using the same context would fail immediately and leave per-turn
// workspace directories behind in the sandbox.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), defaultRmTimeout)
}

// sandbox returns the underlying OpenSandbox sandbox, or an error if
// the executor has not been initialized.
func (r *workspaceRuntime) sandbox() (*osb.Sandbox, error) {
	if r.ce == nil || r.ce.sbx == nil {
		return nil, errors.New("opensandbox: sandbox not initialized")
	}
	return r.ce.sbx, nil
}

// CreateWorkspace creates a per-execution directory inside the sandbox.
func (r *workspaceRuntime) CreateWorkspace(
	ctx context.Context,
	execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	_ = pol

	if _, err := r.sandbox(); err != nil {
		return codeexecutor.Workspace{}, err
	}

	if r.cfg.workspacePersistence == WorkspacePersistencePerSession && execID == "" {
		return codeexecutor.Workspace{}, errors.New(
			"opensandbox: execID must not be empty when using WorkspacePersistencePerSession",
		)
	}

	safe := sanitize(execID)
	var wsPath string
	if r.cfg.workspacePersistence == WorkspacePersistencePerSession {
		// Use a stable hash of the raw exec ID to avoid collisions
		// from sanitize() (e.g. "a/b" and "a_b" both sanitize to
		// "a_b").
		h := stableWorkspaceHash(execID)
		wsPath = path.Join(r.cfg.runBase, fmt.Sprintf("ws_%s", h))
	} else {
		suf := time.Now().UnixNano()
		wsPath = path.Join(r.cfg.runBase, fmt.Sprintf("ws_%s_%d", safe, suf))
	}

	var sb2 strings.Builder
	sb2.WriteString("set -e; mkdir -p ")
	for _, d := range []string{
		wsPath,
		path.Join(wsPath, codeexecutor.DirSkills),
		path.Join(wsPath, codeexecutor.DirWork),
		path.Join(wsPath, codeexecutor.DirRuns),
		path.Join(wsPath, codeexecutor.DirOut),
	} {
		sb2.WriteString(shellQuote(d))
		sb2.WriteByte(' ')
	}
	// Guard against symlink hijack on meta.json: if a previous run (or
	// a malicious actor with write access to the workspace) replaced
	// meta.json with a symlink pointing to an external file (e.g.
	// /etc/cron.d/payload), the `[ -f meta.json ]` test would follow
	// the symlink and see the target's content as existing, so `echo
	// '{}' > meta.json` would never fire — but a subsequent write to
	// meta.json would go through the symlink and clobber the external
	// target. Remove any pre-existing symlink before the existence
	// check so `echo '{}' >` always creates a fresh regular file.
	metaPath := path.Join(wsPath, codeexecutor.MetaFileName)
	sb2.WriteString("; [ -L ")
	sb2.WriteString(shellQuote(metaPath))
	sb2.WriteString(" ] && rm -f ")
	sb2.WriteString(shellQuote(metaPath))
	sb2.WriteString("; [ -f ")
	sb2.WriteString(shellQuote(metaPath))
	sb2.WriteString(" ] || echo '{}' > ")
	sb2.WriteString(shellQuote(metaPath))

	if _, err := r.runBash(ctx, sb2.String(), defaultCreateTimeout); err != nil {
		return codeexecutor.Workspace{}, err
	}
	return codeexecutor.Workspace{ID: execID, Path: wsPath}, nil
}

// validateWorkspace enforces that ws.Path is a directory created under
// the configured runBase. Without this a caller that hand-constructs a
// codeexecutor.Workspace could point Cleanup/RunProgram/Collect at an
// arbitrary sandbox path (e.g. "/" or "/tmp"). runBase itself is also
// rejected, since removing it would wipe all workspaces.
func (r *workspaceRuntime) validateWorkspace(
	ws codeexecutor.Workspace,
) error {
	if ws.Path == "" {
		return errors.New("opensandbox: workspace path is empty")
	}
	base := path.Clean(r.cfg.runBase)
	p := path.Clean(ws.Path)
	if p == base {
		return fmt.Errorf(
			"opensandbox: workspace path %q must not equal runBase %q",
			ws.Path, r.cfg.runBase,
		)
	}
	if !pathUnder(p, base) {
		return fmt.Errorf(
			"opensandbox: workspace path %q escapes runBase %q",
			ws.Path, r.cfg.runBase,
		)
	}
	return nil
}

// Cleanup removes the workspace directory from the sandbox.
func (r *workspaceRuntime) Cleanup(
	ctx context.Context,
	ws codeexecutor.Workspace,
) error {
	if err := r.validateWorkspace(ws); err != nil {
		return err
	}
	script := "rm -rf " + shellQuote(ws.Path)
	_, err := r.runBash(ctx, script, defaultRmTimeout)
	return err
}
