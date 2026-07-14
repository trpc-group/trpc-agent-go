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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/log"
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

	defaultCreateTimeout  = 15 * time.Second
	defaultRmTimeout      = 15 * time.Second
	defaultStageTimeout   = 60 * time.Second
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
)

// workspaceRuntime implements WorkspaceManager / WorkspaceFS /
// ProgramRunner for the OpenSandbox sandbox.
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
	sb2.WriteString("; [ -f ")
	sb2.WriteString(shellQuote(path.Join(wsPath, codeexecutor.MetaFileName)))
	sb2.WriteString(" ] || echo '{}' > ")
	sb2.WriteString(shellQuote(path.Join(wsPath, codeexecutor.MetaFileName)))

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

// PutFiles writes files into the sandbox workspace using the SDK's
// native multipart UploadFiles API. Each PutFile is uploaded with its
// declared POSIX mode via FileMetadata.Mode.
func (r *workspaceRuntime) PutFiles(
	ctx context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	if len(files) == 0 {
		return nil
	}
	if err := r.validateWorkspace(ws); err != nil {
		return err
	}
	sb, err := r.sandbox()
	if err != nil {
		return err
	}

	entries := make([]osb.UploadFileEntry, 0, len(files))
	for _, f := range files {
		clean := path.Clean(filepath.ToSlash(f.Path))
		if clean == "." || clean == "/" || clean == "" {
			return fmt.Errorf("invalid file path: %s", f.Path)
		}
		finalPath := path.Join(ws.Path, clean)
		if !pathUnder(finalPath, ws.Path) {
			return fmt.Errorf("opensandbox: path %q escapes workspace", f.Path)
		}
		// Resolve symlinks in the parent directory to prevent a
		// symlink inside the workspace from redirecting writes
		// outside. Use resolveSandboxAncestor (not resolveSandboxPath)
		// because the parent may not yet exist — e.g. uploading
		// a/b/c/file.txt where a, b, and c are all new directories.
		// resolveSandboxAncestor walks up to the nearest existing
		// ancestor, resolves it with readlink -f, and appends the
		// non-existent tail back.
		resolvedParent, err := r.resolveSandboxAncestor(ctx, path.Dir(finalPath), ws.Path)
		if err != nil {
			return err
		}
		finalPath = path.Join(resolvedParent, path.Base(finalPath))
		// Ensure the parent directory exists. The SDK's UploadFiles
		// creates intermediate directories, but we create them
		// explicitly to be safe across server versions.
		parent := resolvedParent
		if parent != "." && parent != "/" && parent != ws.Path {
			if err := sb.CreateDirectory(ctx, parent, osb.OctalMode(0o755)); err != nil {
				return fmt.Errorf("create directory %s: %w", parent, err)
			}
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		entries = append(entries, osb.UploadFileEntry{
			File: bytes.NewReader(f.Content),
			Options: osb.UploadFileOptions{
				FileName: path.Base(clean),
				Metadata: osb.FileMetadata{
					Path: finalPath,
					Mode: osb.OctalMode(os.FileMode(mode)),
				},
			},
		})
	}
	return sb.UploadFiles(ctx, entries)
}

// PutDirectory packs a host directory into tar.gz then uploads and
// extracts it in the sandbox under ws.Path/to. We use the SDK's
// UploadFiles API with one entry per file, walking the host tree with
// filepath.WalkDir and skipping non-regular entries (symlinks,
// devices, etc.) to prevent following symlinks outside hostPath.
func (r *workspaceRuntime) PutDirectory(
	ctx context.Context,
	ws codeexecutor.Workspace,
	hostPath string,
	to string,
) error {
	if strings.TrimSpace(hostPath) == "" {
		return errors.New("hostPath is empty")
	}
	if err := r.validateWorkspace(ws); err != nil {
		return err
	}
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("hostPath is not a directory: %s", abs)
	}

	dest := ws.Path
	if to != "" {
		dest = path.Join(ws.Path, filepath.ToSlash(to))
	}
	if !pathUnder(dest, ws.Path) {
		return fmt.Errorf("opensandbox: destination %q escapes workspace", to)
	}
	// Resolve symlinks in dest to prevent a symlink inside the
	// workspace from redirecting directory uploads outside. Use
	// resolveSandboxAncestor because dest may not yet exist.
	resolvedDest, err := r.resolveSandboxAncestor(ctx, dest, ws.Path)
	if err != nil {
		return err
	}
	dest = resolvedDest

	sb, err := r.sandbox()
	if err != nil {
		return err
	}
	if err := sb.CreateDirectory(ctx, dest, osb.OctalMode(0o755)); err != nil {
		return fmt.Errorf("create directory %s: %w", dest, err)
	}

	return r.walkAndUpload(ctx, sb, abs, dest)
}

// uploadBatchSize is the maximum number of files uploaded in a single
// UploadFiles call during directory staging. Bounding the batch keeps
// the number of simultaneously open file descriptors well below the
// typical ulimit -n (1024), preventing StageDirectory from failing on
// large workspaces. Each batch's file handles are closed as soon as
// UploadFiles returns, before the next batch is opened.
const uploadBatchSize = 64

// walkAndUpload walks hostRoot with filepath.WalkDir and uploads files
// to destRoot in batches of uploadBatchSize. Empty subdirectories are
// created explicitly via sb.CreateDirectory so they survive in the
// sandbox even when they contain no files (matches the e2b adapter's
// tar TypeDir behaviour).
//
// Non-regular entries (symlinks, devices, sockets, fifos) are skipped:
// d.Info() reports Lstat semantics (it does not follow symlinks), so
// a symlink inside hostRoot cannot cause files outside hostRoot to be
// uploaded. This matches the e2b adapter's behaviour.
//
// Files are opened with os.Open and streamed via the io.Reader
// interface rather than buffered in memory with os.ReadFile, so
// staging a directory with large files does not materialize the full
// tree in the agent process. File handles are closed after each batch
// is uploaded, keeping the open-fd count bounded by uploadBatchSize
// rather than the total file count in the tree.
func (r *workspaceRuntime) walkAndUpload(
	ctx context.Context,
	sb *osb.Sandbox,
	hostRoot, destRoot string,
) error {
	var (
		entries   []osb.UploadFileEntry
		openFiles []*os.File
	)
	// flushBatch uploads the current batch and closes all its file
	// handles. Called when the batch reaches uploadBatchSize and once
	// more after the walk finishes.
	flushBatch := func() error {
		if len(entries) == 0 {
			return nil
		}
		err := sb.UploadFiles(ctx, entries)
		for _, f := range openFiles {
			_ = f.Close()
		}
		entries = entries[:0]
		openFiles = openFiles[:0]
		return err
	}
	walkErr := filepath.WalkDir(hostRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(hostRoot, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		remotePath := path.Join(destRoot, filepath.ToSlash(rel))

		if d.IsDir() {
			if err := sb.CreateDirectory(ctx, remotePath, osb.OctalMode(0o755)); err != nil {
				return fmt.Errorf("create directory %s: %w", remotePath, err)
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if !shouldUploadFile(info) {
			return nil
		}
		parent := path.Dir(remotePath)
		if parent != "." && parent != "/" && parent != destRoot {
			if err := sb.CreateDirectory(ctx, parent, osb.OctalMode(0o755)); err != nil {
				return fmt.Errorf("create directory %s: %w", parent, err)
			}
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		openFiles = append(openFiles, f)
		entries = append(entries, osb.UploadFileEntry{
			File: f,
			Options: osb.UploadFileOptions{
				FileName: path.Base(remotePath),
				Metadata: osb.FileMetadata{
					Path: remotePath,
					Mode: osb.OctalMode(mode),
				},
			},
		})
		// Upload and close this batch once it reaches the size limit.
		if len(entries) >= uploadBatchSize {
			return flushBatch()
		}
		return nil
	})
	if walkErr != nil {
		// Close any pending handles on error.
		for _, f := range openFiles {
			_ = f.Close()
		}
		return walkErr
	}
	// Flush any remaining entries after the walk completes.
	return flushBatch()
}

// StageDirectory stages a directory with options (ReadOnly).
func (r *workspaceRuntime) StageDirectory(
	ctx context.Context,
	ws codeexecutor.Workspace,
	src string,
	to string,
	opt codeexecutor.StageOptions,
) error {
	if err := r.PutDirectory(ctx, ws, src, to); err != nil {
		return err
	}
	if opt.ReadOnly {
		// Re-resolve dest to ensure chmod operates on the real path
		// (after symlink resolution), not a symlink that might point
		// outside the workspace. PutDirectory already validated this,
		// but we resolve again in case the filesystem changed.
		dest := ws.Path
		if to != "" {
			dest = path.Join(ws.Path, filepath.ToSlash(to))
		}
		resolvedDest, err := r.resolveSandboxAncestor(ctx, dest, ws.Path)
		if err != nil {
			return err
		}
		script := "chmod -R a-w " + shellQuote(resolvedDest)
		if _, err := r.runBash(ctx, script, defaultStageTimeout); err != nil {
			return err
		}
	}
	return nil
}

// Collect returns files in the workspace that match the supplied
// globs. Files are read back through the SDK's DownloadFile API and
// sized against maxReadSizeBytes.
func (r *workspaceRuntime) Collect(
	ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	patterns = codeexecutor.NormalizeGlobs(patterns)
	if len(patterns) == 0 {
		return nil, nil
	}
	if err := r.validateWorkspace(ws); err != nil {
		return nil, err
	}
	sb, err := r.sandbox()
	if err != nil {
		return nil, err
	}

	paths, err := r.listFilesByGlob(ctx, ws.Path, patterns)
	if err != nil {
		return nil, err
	}

	// Resolve symlinks for all collected paths in a single round-trip
	// to prevent a symlink inside the workspace from causing Collect
	// to read files outside the workspace. A path that resolves
	// outside ws.Path is skipped.
	resolvedPaths, err := r.resolveSandboxPaths(ctx, paths, ws.Path)
	if err != nil {
		return nil, err
	}

	out := make([]codeexecutor.File, 0, len(resolvedPaths))
	seen := map[string]bool{}
	var totalBytes int64
	for _, fr := range resolvedPaths {
		// Stop when the aggregate file-count or total-byte budget is
		// reached. Without this, model-generated code can create
		// thousands of matching files and exhaust host memory.
		if len(out) >= maxCollectFiles || totalBytes >= maxCollectTotalBytes {
			break
		}
		rel := strings.TrimPrefix(fr.path, ws.Path+"/")
		if rel == fr.path {
			rel = filepath.ToSlash(fr.path)
		}
		if codeexecutor.IsRootMetadataTempPath(rel) {
			continue
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		// Cap the per-file read against the remaining total budget
		// so a single large file cannot consume the entire budget.
		remaining := maxCollectTotalBytes - totalBytes
		if remaining <= 0 {
			// Budget exhausted — stop to avoid passing limit=0 to
			// readFile, whose <= 0 fallback would read up to
			// maxReadSizeBytes (4 MiB) beyond the budget.
			break
		}
		if remaining > maxReadSizeBytes {
			remaining = maxReadSizeBytes
		}
		data, size, truncated, err := r.readFile(ctx, sb, fr.path, remaining, fr.size)
		if err != nil {
			return nil, err
		}
		totalBytes += int64(len(data))
		mime := http.DetectContentType(data)
		out = append(out, codeexecutor.File{
			Name:      rel,
			Content:   string(data),
			MIMEType:  mime,
			SizeBytes: size,
			Truncated: truncated,
		})
	}
	return out, nil
}

// resolveSandboxPaths resolves the real paths of multiple targets
// inside the sandbox in a single bash invocation, then filters out
// any that resolve outside wsBase. This is the batch version of
// resolveSandboxPath, used by Collect to avoid one round-trip per
// search result.
func (r *workspaceRuntime) resolveSandboxPaths(
	ctx context.Context, results []fileSearchResult, wsBase string,
) ([]fileSearchResult, error) {
	if len(results) == 0 {
		return results, nil
	}
	// Use printf with a NUL-separated format to avoid ambiguity from
	// readlink's own newline output. Each result is on exactly one
	// line, with no extra echo that would create blank lines.
	var script strings.Builder
	script.WriteString("for p in")
	for _, fr := range results {
		script.WriteByte(' ')
		script.WriteString(shellQuote(fr.path))
	}
	script.WriteString(`; do r=$(readlink -f -- "$p" 2>/dev/null) || r=""; printf '%s\n' "$r"; done`)
	out, err := r.runBash(ctx, script.String(), defaultCollectTimeout)
	if err != nil {
		return nil, fmt.Errorf(
			"opensandbox: resolve paths: %w", err,
		)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(results) {
		// Fallback: if the batch script returned unexpected output,
		// resolve each path individually.
		filtered := make([]fileSearchResult, 0, len(results))
		for _, fr := range results {
			resolved, err := r.resolveSandboxPath(ctx, fr.path, wsBase)
			if err != nil {
				continue // skip paths that escape
			}
			filtered = append(filtered, fileSearchResult{
				path: resolved, size: fr.size,
			})
		}
		return filtered, nil
	}
	filtered := make([]fileSearchResult, 0, len(results))
	for i, line := range lines {
		resolved := strings.TrimSpace(line)
		if resolved == "" || !pathUnder(resolved, wsBase) {
			continue
		}
		filtered = append(filtered, fileSearchResult{
			path: resolved, size: results[i].size,
		})
	}
	return filtered, nil
}

// StageInputs maps external inputs into the sandbox workspace.
//
// Not implemented in v1; returns errNotImplementedV1.
func (r *workspaceRuntime) StageInputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	_ = ctx
	_ = ws
	_ = specs
	return errNotImplementedV1
}

// CollectOutputs applies the declarative output spec in the sandbox.
//
// Not implemented in v1; returns errNotImplementedV1.
func (r *workspaceRuntime) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	_ = ctx
	_ = ws
	_ = spec
	return codeexecutor.OutputManifest{}, errNotImplementedV1
}

// RunProgram runs an arbitrary command inside the sandbox workspace.
//
// Environment injection: workspace base variables and spec.Env are
// spliced into the command string via envToken() (producing `env ...`
// or `env -i ...`). RunCommandRequest.Envs is left nil because Envs
// is additive and cannot express `env -i`.
//
// Timeout is expressed in milliseconds (RunCommandRequest.Timeout is
// int64 milliseconds per the OpenSandbox SDK).
//
// Timeout budget: the OpenSandbox SDK applies
// ConnectionConfig.RequestTimeout to ALL HTTP requests including the
// streaming /command endpoint, so spec.Timeout cannot exceed
// requestTimeout - requestTimeoutBuffer. If spec.Timeout exceeds this
// budget RunProgram returns an error (rather than silently clamping,
// which would violate the ProgramRunner contract that other runtimes
// honor spec.Timeout verbatim); raise WithRequestTimeout (or
// WithExecutionTimeout, which sets the floor) to allow longer runs.
func (r *workspaceRuntime) RunProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	if err := r.validateWorkspace(ws); err != nil {
		return codeexecutor.RunResult{}, err
	}
	sb, err := r.sandbox()
	if err != nil {
		return codeexecutor.RunResult{}, err
	}

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}

	// The SDK applies ConnectionConfig.RequestTimeout to ALL HTTP
	// requests including streaming /command. If spec.Timeout exceeds
	// the request timeout budget the command would be killed by the
	// HTTP client before finishing. Rather than silently clamping
	// (which would violate the ProgramRunner contract that other
	// runtimes honor spec.Timeout verbatim), return an error so the
	// caller can raise WithRequestTimeout or lower spec.Timeout.
	if r.ce.requestTimeout > 0 {
		maxRun := r.ce.requestTimeout - requestTimeoutBuffer
		if maxRun > 0 && timeout > maxRun {
			return codeexecutor.RunResult{}, fmt.Errorf(
				"opensandbox: spec.Timeout %s exceeds the request timeout budget %s "+
					"(HTTP client timeout %s - %s buffer); raise WithRequestTimeout "+
					"(or WithExecutionTimeout, which sets the floor) to allow longer runs",
				timeout, maxRun, r.ce.requestTimeout, requestTimeoutBuffer,
			)
		}
	}

	cwd, err := r.resolveRunCwd(ctx, ws, spec.Cwd)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}

	skillsDir := path.Join(ws.Path, codeexecutor.DirSkills)
	workDir := path.Join(ws.Path, codeexecutor.DirWork)
	outDir := path.Join(ws.Path, codeexecutor.DirOut)
	runDir := path.Join(
		ws.Path, codeexecutor.DirRuns,
		fmt.Sprintf("run_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&r.runSeq, 1)),
	)
	baseEnv := map[string]string{
		codeexecutor.WorkspaceEnvDirKey: ws.Path,
		codeexecutor.EnvSkillsDir:       skillsDir,
		codeexecutor.EnvWorkDir:         workDir,
		codeexecutor.EnvOutputDir:       outDir,
		codeexecutor.EnvRunDir:          runDir,
	}
	envAssign, err := envToken(baseEnv, spec.Env, spec.CleanEnv)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}

	quotedCmd := shellQuote(spec.Cmd)
	var quotedArgs strings.Builder
	for _, a := range spec.Args {
		quotedArgs.WriteByte(' ')
		quotedArgs.WriteString(shellQuote(a))
	}

	var stdinRedir string
	if spec.Stdin != "" {
		// Pipe stdin from a base64-decoded inline payload (no mktemp).
		b64 := b64encode(spec.Stdin)
		stdinRedir = " < <(printf %s " + shellQuote(b64) + " | base64 -d)"
	}

	// mkdir -p the runDir and outDir so the spawned program can write
	// scratch/output files without having to create them.
	// Wrap in `bash -c` because stdinRedir uses bash process
	// substitution (<(...)) which is not available in /bin/sh.
	command := fmt.Sprintf(
		"mkdir -p %s %s && cd %s && %s%s%s%s",
		shellQuote(runDir), shellQuote(outDir),
		shellQuote(cwd),
		envAssign, quotedCmd, quotedArgs.String(),
		stdinRedir,
	)
	// When CleanEnv is requested, the outer wrapper bash must also
	// start with a minimal environment. Without this, BASH_ENV (if set
	// in the sandbox env) would cause bash to source an arbitrary file
	// before the inner `env -i` command runs, and LD_PRELOAD would be
	// loaded by the dynamic linker before bash starts. Using
	// `env -i PATH=... bash --norc --noprofile` ensures the wrapper
	// shell inherits nothing from the sandbox environment and skips
	// startup files. SupportsCleanEnv: true is a security gate for
	// command-policy mode, so this boundary must be real.
	if spec.CleanEnv {
		command = "env -i PATH=" + shellQuote(minimalCleanPATH) +
			" bash --norc --noprofile -c " + shellQuote(command)
	} else {
		command = "bash -c " + shellQuote(command)
	}

	req := osb.RunCommandRequest{
		Command: command,
		Cwd:     "", // cwd is already handled by `cd` in the command
		Timeout: int64(timeout / time.Millisecond),
	}
	if req.Timeout <= 0 {
		req.Timeout = int64(defaultRunTimeout / time.Millisecond)
	}

	start := time.Now()
	// Use ExecutionHandlers with SkipAccumulation to prevent the SDK
	// from accumulating unbounded stdout/stderr in the Execution
	// struct. Instead, we copy into our own capped buffers and stop
	// accepting data once the cap is reached. This bounds host memory
	// even when a remote command prints continuously.
	var (
		stdoutBuf cappedBuffer
		stderrBuf cappedBuffer
	)
	handlers := &osb.ExecutionHandlers{
		OnStdout: func(m osb.OutputMessage) error {
			stdoutBuf.write(m.Text)
			return nil
		},
		OnStderr: func(m osb.OutputMessage) error {
			stderrBuf.write(m.Text)
			return nil
		},
		SkipAccumulation: true,
	}
	exec, runErr := sb.RunCommandWithOpts(ctx, req, handlers)
	dur := time.Since(start)

	res := codeexecutor.RunResult{
		Duration: dur,
	}
	res.Stdout = stdoutBuf.string()
	res.Stderr = stderrBuf.string()
	if exec != nil {
		// exec.Error carries structured error information (exception
		// name, value, traceback) from SSE error events. Without this,
		// a non-numeric evalue would leave ExitCode nil and Stderr
		// empty, causing ExecuteCode to report only "[exit -1]" and
		// discard the actual error details.
		if exec.Error != nil {
			res.Stderr = formatExecutionError(exec.Error, res.Stderr)
		}
		if exec.ExitCode != nil {
			res.ExitCode = *exec.ExitCode
		} else {
			// ExitCode is nil when the command was killed by a signal
			// or did not complete. Use -1 to make the failure visible
			// to callers.
			res.ExitCode = -1
		}
	}
	if runErr != nil {
		if isTimeoutErr(runErr) {
			res.TimedOut = true
			// Don't return the error; surface timeout via RunResult.
			return res, nil
		}
		return res, runErr
	}
	return res, nil
}

// resolveRunCwd resolves the working directory for a RunProgram call.
// If specCwd is empty, ws.Path is used. Otherwise the path is joined
// under ws.Path, lexically validated against workspace escape, and
// resolved through the sandbox to defeat symlinks pointing outside.
func (r *workspaceRuntime) resolveRunCwd(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specCwd string,
) (string, error) {
	if specCwd == "" {
		return ws.Path, nil
	}
	cwd := path.Join(ws.Path, filepath.ToSlash(specCwd))
	// Reject a Cwd that escapes the workspace before emitting `cd`.
	// Without this a direct RunProgram caller could run anywhere
	// inside the sandbox by passing spec.Cwd = "../../etc".
	if !pathUnder(cwd, ws.Path) {
		return "", fmt.Errorf(
			"opensandbox: spec.Cwd %q escapes workspace", specCwd,
		)
	}
	// Also resolve symlinks: a symlink inside the workspace
	// pointing to an external directory would pass the lexical
	// check above but cause `cd` to land outside the workspace.
	resolved, err := r.resolveSandboxPath(ctx, cwd, ws.Path)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// formatExecutionError renders an SDK ExecutionError (exception name,
// value, traceback from SSE error events) into stderr text, preserving
// any stderr already captured from the streaming output. Without this,
// a non-numeric evalue leaves ExitCode nil and Stderr empty, causing
// ExecuteCode to report only "[exit -1]" and discard the actual error
// details.
func formatExecutionError(e *osb.ExecutionError, existingStderr string) string {
	var eb strings.Builder
	if existingStderr != "" {
		eb.WriteString(existingStderr)
		eb.WriteByte('\n')
	}
	if e.Name != "" {
		eb.WriteString(e.Name)
		if e.Value != "" {
			eb.WriteString(": ")
			eb.WriteString(e.Value)
		}
	} else if e.Value != "" {
		eb.WriteString(e.Value)
	}
	if len(e.Traceback) > 0 {
		eb.WriteByte('\n')
		eb.WriteString(strings.Join(e.Traceback, "\n"))
	}
	return eb.String()
}

// ExecuteInline writes each code block into the sandbox workspace and
// runs it, aggregating stdout/stderr from all blocks.
func (r *workspaceRuntime) ExecuteInline(
	ctx context.Context,
	execID string,
	blocks []codeexecutor.CodeBlock,
	timeout time.Duration,
) (codeexecutor.RunResult, error) {
	ws, err := r.CreateWorkspace(
		ctx, execID, codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	// In PerSession mode the workspace is reused across turns; the
	// caller owns cleanup. In PerTurn mode we clean up automatically.
	// Use a context detached from the parent's cancellation so cleanup
	// still runs after the parent context is cancelled/timed out.
	if r.cfg.workspacePersistence != WorkspacePersistencePerSession {
		defer func() {
			cleanupCtx, cancel := cleanupContext(ctx)
			defer cancel()
			if err := r.Cleanup(cleanupCtx, ws); err != nil {
				log.Errorf("opensandbox: cleanup workspace %q: %v", ws.Path, err)
			}
		}()
	}

	var (
		allOut, allErr strings.Builder
		// Aggregate the last non-zero exit code across blocks so the
		// caller can detect a failed block via RunResult.ExitCode.
		// 0 means "no block reported a non-zero exit".
		aggExit int
		// OR-fold TimedOut across blocks: if any block timed out, the
		// aggregated result reports TimedOut = true.
		aggTimedOut bool
	)
	start := time.Now()
	for i, b := range blocks {
		fn, mode, cmd, args, err := codeexecutor.BuildBlockSpec(i, b)
		if err != nil {
			allErr.WriteString(err.Error())
			allErr.WriteString("\n")
			// Build failure is a non-execution failure; surface as
			// exit 1 so the caller sees the block did not succeed.
			if aggExit == 0 {
				aggExit = 1
			}
			continue
		}
		pf := codeexecutor.PutFile{
			Path:    path.Join(codeexecutor.InlineSourceDir, fn),
			Content: []byte(b.Code),
			Mode:    mode,
		}
		if err := r.PutFiles(ctx, ws, []codeexecutor.PutFile{pf}); err != nil {
			allErr.WriteString(err.Error())
			allErr.WriteString("\n")
			if aggExit == 0 {
				aggExit = 1
			}
			continue
		}
		argv := append([]string{}, args...)
		argv = append(argv, path.Join(".", fn))
		res, err := r.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     cmd,
			Args:    argv,
			Cwd:     codeexecutor.InlineSourceDir,
			Timeout: timeout,
		})
		if err != nil {
			allErr.WriteString(err.Error())
			allErr.WriteString("\n")
			// RunProgram returned an error (not a non-zero exit).
			// Surface as exit 1 so the caller sees failure.
			if aggExit == 0 {
				aggExit = 1
			}
		}
		if res.Stdout != "" {
			allOut.WriteString(res.Stdout)
		}
		if res.Stderr != "" {
			allErr.WriteString(res.Stderr)
		}
		if res.ExitCode != 0 {
			aggExit = res.ExitCode
		}
		if res.TimedOut {
			aggTimedOut = true
		}
	}
	dur := time.Since(start)
	return codeexecutor.RunResult{
		Stdout:   allOut.String(),
		Stderr:   allErr.String(),
		ExitCode: aggExit,
		Duration: dur,
		TimedOut: aggTimedOut,
	}, nil
}

// runBash runs a bash snippet in the sandbox via RunCommandWithOpts
// and returns the captured stdout. The script is wrapped in `bash -c`
// so the caller can pass a multi-line script with redirects/pipes
// without worrying about the shell's top-level parsing rules.
func (r *workspaceRuntime) runBash(
	ctx context.Context, script string, timeout time.Duration,
) (string, error) {
	sb, err := r.sandbox()
	if err != nil {
		return "", err
	}
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}
	req := osb.RunCommandRequest{
		Command: "bash -c " + shellQuote(script),
		Timeout: int64(timeout / time.Millisecond),
	}
	if req.Timeout <= 0 {
		req.Timeout = int64(defaultRunTimeout / time.Millisecond)
	}
	// Use ExecutionHandlers with SkipAccumulation to prevent the SDK
	// from accumulating unbounded stdout/stderr in the Execution
	// struct. runBash is used by infrastructure commands (mkdir,
	// chmod -R, rm -rf, readlink -f) that can produce large output on
	// pathological filesystems (e.g. chmod -R on a workspace with
	// millions of files). Without this, the SDK's Execution struct
	// would accumulate all output in memory.
	var (
		stdoutBuf cappedBuffer
		stderrBuf cappedBuffer
	)
	handlers := &osb.ExecutionHandlers{
		OnStdout: func(m osb.OutputMessage) error {
			stdoutBuf.write(m.Text)
			return nil
		},
		OnStderr: func(m osb.OutputMessage) error {
			stderrBuf.write(m.Text)
			return nil
		},
		SkipAccumulation: true,
	}
	exec, err := sb.RunCommandWithOpts(ctx, req, handlers)
	if err != nil {
		if exec != nil {
			return stdoutBuf.string(), err
		}
		return "", err
	}
	if exec.ExitCode != nil && *exec.ExitCode != 0 {
		return stdoutBuf.string(), fmt.Errorf(
			"opensandbox: bash exit %d: %s",
			*exec.ExitCode, stderrBuf.string(),
		)
	}
	return stdoutBuf.string(), nil
}

// readFile reads up to limit bytes from a remote path via the SDK's
// DownloadFile API. Returns the data, the file's full size (which
// may exceed len(data) when truncated), and a truncated flag. knownSize
// is the real file size from SearchFiles metadata; when positive it is
// used as the returned size so callers get an accurate SizeBytes even
// for files larger than limit. When knownSize is non-positive, the size
// falls back to the number of bytes actually read (capped at limit+1).
//
// The returned size is always at least len(data) to prevent stale
// metadata from making SizeBytes smaller than the actual content read.
//
// Truncated is true only when the read actually hit the limit+1 cap
// (proving the file is at least limit+1 bytes). This avoids false
// positives when the file shrank between SearchFiles and DownloadFile:
// in that case readBytes < limit, so the read reached EOF and the file
// was not truncated — even though stale knownSize may exceed len(data).
func (r *workspaceRuntime) readFile(
	ctx context.Context, sb *osb.Sandbox, full string, limit int64,
	knownSize int64,
) ([]byte, int64, bool, error) {
	if limit <= 0 {
		limit = maxReadSizeBytes
	}
	// Request one extra byte to detect truncation: if the server
	// returns limit+1 bytes, the file exceeds the cap.
	rangeHeader := fmt.Sprintf("bytes=0-%d", limit)
	rc, err := sb.DownloadFile(ctx, full, rangeHeader)
	if err != nil {
		return nil, 0, false, err
	}
	defer rc.Close()
	// Cap the read at limit+1 bytes regardless of whether the server
	// honors the Range header. Without this, a server/proxy that
	// ignores Range would stream the entire file into memory before
	// the truncation check below fires.
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, 0, false, err
	}
	readBytes := int64(len(data))
	// Truncated iff we read the full limit+1 bytes, proving the file
	// is at least limit+1 bytes long. This is the only reliable signal:
	// comparing knownSize to len(data) produces false positives when
	// the file shrank between SearchFiles and DownloadFile.
	truncated := readBytes > limit
	if truncated {
		data = data[:limit]
	}
	// Prefer the real size from SearchFiles metadata; fall back to
	// the byte count we actually read when metadata is unavailable.
	// Use max(knownSize, readBytes) — readBytes (before truncation)
	// so hitting the limit+1 detection is reflected in size even when
	// knownSize is stale (file grew between SearchFiles and Download).
	size := knownSize
	if size < readBytes {
		size = readBytes
	}
	return data, size, truncated, nil
}

// fileSearchResult carries a file path and its real size as reported
// by the SearchFiles API. The size is used by Collect to set
// File.SizeBytes accurately even when the file content is truncated
// by readFile's byte cap.
type fileSearchResult struct {
	path string
	size int64
}

// listFilesByGlob resolves the provided patterns inside the sandbox
// using the SDK's SearchFiles API and returns absolute file paths
// with their real sizes. SearchFiles matches a single glob per call,
// so we iterate over patterns and dedup results.
func (r *workspaceRuntime) listFilesByGlob(
	ctx context.Context, wsPath string, patterns []string,
) ([]fileSearchResult, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	sb, err := r.sandbox()
	if err != nil {
		return nil, err
	}
	var out []fileSearchResult
	seen := map[string]bool{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		infos, err := sb.SearchFiles(ctx, wsPath, p)
		if err != nil {
			return nil, fmt.Errorf("opensandbox: search files %q: %w", p, err)
		}
		for _, fi := range infos {
			// Skip directories — only collect regular files.
			if fi.Type == "dir" || fi.Type == "directory" {
				continue
			}
			clean := path.Clean(fi.Path)
			if !pathUnder(clean, wsPath) {
				continue
			}
			if seen[clean] {
				continue
			}
			seen[clean] = true
			out = append(out, fileSearchResult{path: clean, size: fi.Size})
		}
	}
	return out, nil
}

// shouldUploadFile returns true if the directory entry should be
// uploaded to the sandbox. Non-regular files (symlinks, devices,
// sockets, fifos) are skipped to prevent following symlinks outside
// hostPath, matching the e2b adapter's behaviour.
func shouldUploadFile(info os.FileInfo) bool {
	return info.Mode().IsRegular()
}

// pathUnder reports whether p is equal to base or nested below it.
// Both arguments are expected to be absolute POSIX paths.
func pathUnder(p, base string) bool {
	if base == "" || p == "" {
		return false
	}
	base = strings.TrimRight(base, "/")
	if p == base {
		return true
	}
	return strings.HasPrefix(p, base+"/")
}

// resolveSandboxPath resolves the real path of a target inside the
// sandbox using `readlink -f`, then verifies the resolved path is
// still under the workspace base. This prevents symlink-based escape:
// a symlink inside the workspace pointing to an external directory
// (e.g. /tmp/outside) would pass the lexical pathUnder check but
// cause writes/reads to land outside the workspace.
//
// The target must exist (readlink -f fails on non-existent paths when
// any component other than the last is missing). For targets that may
// not yet exist (e.g. a file about to be created, possibly under
// multiple new directories), use resolveSandboxAncestor instead.
//
// Returns the resolved path if it is under wsBase, or an error
// otherwise.
func (r *workspaceRuntime) resolveSandboxPath(
	ctx context.Context, target, wsBase string,
) (string, error) {
	script := "readlink -f " + shellQuote(target)
	out, err := r.runBash(ctx, script, defaultCreateTimeout)
	if err != nil {
		return "", fmt.Errorf(
			"opensandbox: resolve path %q: %w", target, err,
		)
	}
	resolved := strings.TrimSpace(out)
	if resolved == "" {
		return "", fmt.Errorf(
			"opensandbox: readlink -f returned empty for %q", target,
		)
	}
	if !pathUnder(resolved, wsBase) {
		return "", fmt.Errorf(
			"opensandbox: resolved path %q escapes workspace %q (symlink?)",
			resolved, wsBase,
		)
	}
	return resolved, nil
}

// resolveSandboxAncestor resolves the real path of a target that may
// not yet exist (e.g. a/b/c/file.txt where a, b, and c are all new).
// It walks up the path until it finds an existing ancestor, resolves
// that ancestor with readlink -f to follow symlinks, verifies the
// resolved ancestor is still under wsBase, then appends the
// non-existent tail components back.
//
// This prevents symlink escape via a parent directory that is a
// symlink to an external location, while still allowing uploads to
// multi-level new directories (which readlink -f alone would reject
// because intermediate components don't exist).
func (r *workspaceRuntime) resolveSandboxAncestor(
	ctx context.Context, target, wsBase string,
) (string, error) {
	target = path.Clean(target)
	// If target exists, resolve it directly.
	if out, err := r.runBash(ctx,
		"test -e "+shellQuote(target)+" && echo yes || echo no",
		defaultCreateTimeout); err == nil && strings.TrimSpace(out) == "yes" {
		return r.resolveSandboxPath(ctx, target, wsBase)
	}
	// Walk up to find the nearest existing ancestor.
	dir := path.Dir(target)
	tail := path.Base(target)
	for dir != "/" && dir != "." {
		out, err := r.runBash(ctx,
			"test -e "+shellQuote(dir)+" && echo yes || echo no",
			defaultCreateTimeout)
		if err != nil {
			return "", fmt.Errorf(
				"opensandbox: check ancestor %q: %w", dir, err,
			)
		}
		if strings.TrimSpace(out) == "yes" {
			// Found existing ancestor; resolve it.
			resolved, err := r.resolveSandboxPath(ctx, dir, wsBase)
			if err != nil {
				return "", err
			}
			return path.Join(resolved, tail), nil
		}
		tail = path.Join(path.Base(dir), tail)
		dir = path.Dir(dir)
	}
	// No existing ancestor found; the workspace root itself should
	// exist. Resolve wsBase directly.
	resolved, err := r.resolveSandboxPath(ctx, wsBase, wsBase)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(wsBase, target)
	if err != nil {
		return "", err
	}
	return path.Join(resolved, filepath.ToSlash(rel)), nil
}

// isTimeoutErr reports whether err represents a command execution
// timeout (as opposed to an infrastructure/network failure). It uses
// structured type assertions rather than string matching to avoid
// misclassifying unrelated errors that happen to contain "timeout" in
// their message (e.g. "504 gateway timeout", "upstream timeout").
func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	// context.DeadlineExceeded is the canonical signal for a
	// context.Timeout cancellation, including our own execution timeout.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// net.Error.Timeout() covers network-level timeouts from the HTTP
	// client (e.g. request deadline). This is narrower than string
	// matching because it only returns true for errors that implement
	// net.Error AND report Timeout() == true.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// For SDK APIError, check the structured code field rather than
	// searching the error message. The mock server returns
	// {"code":"timeout",...} for command execution timeouts; we match
	// that specific code here. A 504 gateway timeout would have a
	// different code and should NOT be classified as a command timeout.
	var apiErr *osb.APIError
	if errors.As(err, &apiErr) {
		if strings.EqualFold(apiErr.Response.Code, "timeout") {
			return true
		}
	}
	return false
}

// b64encode is a thin wrapper around base64 encoding used for stdin
// redirection. The decoded payload is piped to the spawned program
// via `base64 -d`, so this must use base64 (not hex).
func b64encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// sanitize replaces every character outside [A-Za-z0-9_-] with an
// underscore, producing a safe path component.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// stableWorkspaceHash returns a short stable hash of the exec ID,
// used for PerSession workspace paths.
func stableWorkspaceHash(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:8])
}

// shellQuote single-quotes a string for safe inclusion in a shell
// command.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	q := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + q + "'"
}

// cappedBuffer accumulates string data up to maxCommandOutputBytes,
// then drops further writes and appends a truncation marker. Used by
// RunProgram's ExecutionHandlers to bound stdout/stderr memory.
type cappedBuffer struct {
	buf       strings.Builder
	truncated bool
}

func (b *cappedBuffer) write(s string) {
	if b.truncated {
		return
	}
	if b.buf.Len()+len(s) > maxCommandOutputBytes {
		remaining := maxCommandOutputBytes - b.buf.Len()
		if remaining > 0 {
			b.buf.WriteString(s[:remaining])
		}
		fmt.Fprintf(&b.buf, "\n[output truncated: exceeded %d bytes]\n", maxCommandOutputBytes)
		b.truncated = true
		return
	}
	b.buf.WriteString(s)
}

func (b *cappedBuffer) string() string {
	return b.buf.String()
}
