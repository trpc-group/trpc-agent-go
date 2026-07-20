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
	"sync/atomic"
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
//     resolveSandboxPath, resolveSandboxAncestor, removeSymlinksBatch)
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
	// Fail closed on policy fields this backend does not enforce. Silent
	// ignore would let callers believe Persist/MaxDiskBytes are honored.
	if err := validateWorkspacePolicy(pol); err != nil {
		return codeexecutor.Workspace{}, err
	}

	if _, err := r.sandbox(); err != nil {
		return codeexecutor.Workspace{}, err
	}

	if r.cfg.workspacePersistence == WorkspacePersistencePerSession && execID == "" {
		return codeexecutor.Workspace{}, errors.New(
			"opensandbox: execID must not be empty when using " +
				"WorkspacePersistencePerSession; provide a stable " +
				"session-derived ID, or switch to PerTurn mode " +
				"(the default) which does not require a stable ID",
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
		// Nano + monotonic seq: pure UnixNano can collide under concurrent
		// CreateWorkspace in the same process; seq makes the suffix unique.
		suf := fmt.Sprintf("%d_%d", time.Now().UnixNano(), atomic.AddUint64(&r.runSeq, 1))
		wsPath = path.Join(r.cfg.runBase, fmt.Sprintf("ws_%s_%s", safe, suf))
	}

	dirs := []string{
		wsPath,
		path.Join(wsPath, codeexecutor.DirSkills),
		path.Join(wsPath, codeexecutor.DirWork),
		path.Join(wsPath, codeexecutor.DirRuns),
		path.Join(wsPath, codeexecutor.DirOut),
	}
	var sb2 strings.Builder
	// set -e: any unexpected failure aborts. Symlink checks use if/fi
	// so a non-symlink path never leaves a failing status.
	sb2.WriteString("set -e; ")
	// PerSession reuse: a previous turn may have replaced skills/work/
	// runs/out (or the workspace root) with a symlink outside the
	// workspace. mkdir -p follows such symlinks and would create
	// content outside runBase. Strip them first.
	for _, d := range dirs {
		sb2.WriteString("if [ -L ")
		sb2.WriteString(shellQuote(d))
		sb2.WriteString(" ]; then rm -f -- ")
		sb2.WriteString(shellQuote(d))
		sb2.WriteString(" || exit; fi; ")
	}
	sb2.WriteString("mkdir -p ")
	for _, d := range dirs {
		sb2.WriteString(shellQuote(d))
		sb2.WriteByte(' ')
	}
	// Guard against symlink hijack on meta.json.
	metaPath := path.Join(wsPath, codeexecutor.MetaFileName)
	sb2.WriteString("; if [ -L ")
	sb2.WriteString(shellQuote(metaPath))
	sb2.WriteString(" ]; then rm -f -- ")
	sb2.WriteString(shellQuote(metaPath))
	sb2.WriteString(" || exit; fi; if [ ! -f ")
	sb2.WriteString(shellQuote(metaPath))
	sb2.WriteString(" ]; then echo '{}' > ")
	sb2.WriteString(shellQuote(metaPath))
	sb2.WriteString("; fi")
	// Post-condition: every standard directory must resolve under
	// wsPath. Catches races where a symlink was re-planted between
	// the strip step and mkdir -p.
	for _, d := range dirs {
		sb2.WriteString("; r=$(readlink -f -- ")
		sb2.WriteString(shellQuote(d))
		sb2.WriteString(" 2>/dev/null || true); case \"$r\" in ")
		sb2.WriteString(shellQuote(wsPath))
		sb2.WriteString("|")
		sb2.WriteString(shellQuote(wsPath))
		sb2.WriteString("/*) ;; *) echo \"opensandbox: path escapes workspace: \" \"$r\" >&2; exit 1 ;; esac")
	}

	if _, err := r.runBash(ctx, sb2.String(), defaultCreateTimeout); err != nil {
		return codeexecutor.Workspace{}, err
	}
	return codeexecutor.Workspace{ID: execID, Path: wsPath}, nil
}

// validateWorkspacePolicy rejects WorkspacePolicy fields this backend
// does not implement. Zero-value policy is accepted (callers that do
// not care about these knobs keep working). Non-zero fields fail closed
// so a missing enforcement is never silent.
//
// Isolated is ignored when true: OpenSandbox is always remote-isolated,
// so requesting isolation is a no-op that matches the runtime, not a
// false promise.
//
// Persist / MaxDiskBytes are not enforced: lifecycle persistence is
// controlled by WithWorkspacePersistence, and disk caps are not wired
// to the OpenSandbox API in v1.
func validateWorkspacePolicy(pol codeexecutor.WorkspacePolicy) error {
	if pol.Persist {
		return errors.New(
			"opensandbox: WorkspacePolicy.Persist is not supported; " +
				"use WithWorkspacePersistence(WorkspacePersistencePerSession) " +
				"and call Cleanup when the session ends",
		)
	}
	if pol.MaxDiskBytes != 0 {
		return fmt.Errorf(
			"opensandbox: WorkspacePolicy.MaxDiskBytes (%d) is not supported in v1",
			pol.MaxDiskBytes,
		)
	}
	return nil
}

// validateRunProgramLimits rejects per-invocation ResourceLimits.
// Sandbox-level caps belong on WithResourceLimits at New/NewWithContext.
func validateRunProgramLimits(lim codeexecutor.ResourceLimits) error {
	if lim.CPUPercent != 0 || lim.MemoryMB != 0 || lim.MaxPIDs != 0 {
		return fmt.Errorf(
			"opensandbox: RunProgramSpec.Limits is not supported in v1 "+
				"(cpu=%d%% memory=%dMB pids=%d); set WithResourceLimits when creating the sandbox",
			lim.CPUPercent, lim.MemoryMB, lim.MaxPIDs,
		)
	}
	return nil
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

// resolvePerSessionWorkspace returns the Workspace handle for a
// PerSession execID without creating the remote directory. Used by
// ResolveWorkspace / CleanupExecution so callers can destroy durable
// workspaces via public APIs (INV-LIFE).
func (r *workspaceRuntime) resolvePerSessionWorkspace(
	execID string,
) (codeexecutor.Workspace, error) {
	if r.cfg.workspacePersistence != WorkspacePersistencePerSession {
		return codeexecutor.Workspace{}, errors.New(
			"opensandbox: ResolveWorkspace requires WorkspacePersistencePerSession",
		)
	}
	execID = strings.TrimSpace(execID)
	if execID == "" {
		return codeexecutor.Workspace{}, errors.New(
			"opensandbox: execID must not be empty when resolving a PerSession workspace",
		)
	}
	h := stableWorkspaceHash(execID)
	wsPath := path.Join(r.cfg.runBase, fmt.Sprintf("ws_%s", h))
	ws := codeexecutor.Workspace{ID: execID, Path: wsPath}
	if err := r.validateWorkspace(ws); err != nil {
		return codeexecutor.Workspace{}, err
	}
	return ws, nil
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
