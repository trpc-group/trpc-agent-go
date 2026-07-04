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
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
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
)

// workspaceRuntime implements WorkspaceManager / WorkspaceFS /
// ProgramRunner for the OpenSandbox sandbox.
type workspaceRuntime struct {
	ce  *CodeExecutor
	cfg runtimeConfig
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
		runBase:              base,
		workspacePersistence: c.workspacePersistence,
	}}
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

// Cleanup removes the workspace directory from the sandbox.
func (r *workspaceRuntime) Cleanup(
	ctx context.Context,
	ws codeexecutor.Workspace,
) error {
	if ws.Path == "" {
		return nil
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
		// Ensure the parent directory exists. The SDK's UploadFiles
		// creates intermediate directories, but we create them
		// explicitly to be safe across server versions.
		parent := path.Dir(finalPath)
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
// filepath.Walk.
func (r *workspaceRuntime) PutDirectory(
	ctx context.Context,
	ws codeexecutor.Workspace,
	hostPath string,
	to string,
) error {
	if strings.TrimSpace(hostPath) == "" {
		return errors.New("hostPath is empty")
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

	sb, err := r.sandbox()
	if err != nil {
		return err
	}
	if err := sb.CreateDirectory(ctx, dest, osb.OctalMode(0o755)); err != nil {
		return fmt.Errorf("create directory %s: %w", dest, err)
	}

	var entries []osb.UploadFileEntry
	walkErr := filepath.Walk(abs, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(abs, p)
		if err != nil {
			return err
		}
		remotePath := path.Join(dest, filepath.ToSlash(rel))
		// Ensure parent directory exists.
		parent := path.Dir(remotePath)
		if parent != "." && parent != "/" && parent != dest {
			if err := sb.CreateDirectory(ctx, parent, osb.OctalMode(0o755)); err != nil {
				return fmt.Errorf("create directory %s: %w", parent, err)
			}
		}
		mode := fi.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		entries = append(entries, osb.UploadFileEntry{
			File: bytes.NewReader(data),
			Options: osb.UploadFileOptions{
				FileName: path.Base(remotePath),
				Metadata: osb.FileMetadata{
					Path: remotePath,
					Mode: osb.OctalMode(mode),
				},
			},
		})
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	if len(entries) == 0 {
		return nil
	}
	return sb.UploadFiles(ctx, entries)
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
		dest := ws.Path
		if to != "" {
			dest = path.Join(ws.Path, filepath.ToSlash(to))
		}
		script := "chmod -R a-w " + shellQuote(dest)
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
	sb, err := r.sandbox()
	if err != nil {
		return nil, err
	}

	paths, err := r.listFilesByGlob(ctx, ws.Path, patterns)
	if err != nil {
		return nil, err
	}

	out := make([]codeexecutor.File, 0, len(paths))
	seen := map[string]bool{}
	for _, full := range paths {
		rel := strings.TrimPrefix(full, ws.Path+"/")
		if rel == full {
			rel = filepath.ToSlash(full)
		}
		if codeexecutor.IsRootMetadataTempPath(rel) {
			continue
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		data, size, err := r.readFile(ctx, sb, full, maxReadSizeBytes)
		if err != nil {
			return nil, err
		}
		mime := http.DetectContentType(data)
		out = append(out, codeexecutor.File{
			Name:      rel,
			Content:   string(data),
			MIMEType:  mime,
			SizeBytes: size,
			Truncated: size > int64(len(data)),
		})
	}
	return out, nil
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
// Timeout clamping: the OpenSandbox SDK applies
// ConnectionConfig.RequestTimeout to ALL HTTP requests including the
// streaming /command endpoint, so spec.Timeout cannot exceed
// requestTimeout - requestTimeoutBuffer. If spec.Timeout exceeds this
// budget it is clamped and a warning is logged; raise
// WithRequestTimeout (or WithExecutionTimeout, which sets the floor)
// to allow longer runs.
func (r *workspaceRuntime) RunProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
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
	// HTTP client before finishing. Clamp and warn so callers know to
	// raise WithRequestTimeout for longer runs.
	if r.ce.requestTimeout > 0 {
		maxRun := r.ce.requestTimeout - requestTimeoutBuffer
		if maxRun > 0 && timeout > maxRun {
			log.Warnf(
				"opensandbox: spec.Timeout %s exceeds request timeout budget %s (HTTP client timeout %s); clamping to %s",
				timeout, maxRun, r.ce.requestTimeout, maxRun,
			)
			timeout = maxRun
		}
	}

	cwd := ws.Path
	if spec.Cwd != "" {
		cwd = path.Join(ws.Path, filepath.ToSlash(spec.Cwd))
	}

	skillsDir := path.Join(ws.Path, codeexecutor.DirSkills)
	workDir := path.Join(ws.Path, codeexecutor.DirWork)
	outDir := path.Join(ws.Path, codeexecutor.DirOut)
	runDir := path.Join(
		ws.Path, codeexecutor.DirRuns,
		"run_"+time.Now().Format("20060102T150405.000"),
	)
	baseEnv := map[string]string{
		codeexecutor.WorkspaceEnvDirKey: ws.Path,
		codeexecutor.EnvSkillsDir:       skillsDir,
		codeexecutor.EnvWorkDir:         workDir,
		codeexecutor.EnvOutputDir:       outDir,
		codeexecutor.EnvRunDir:          runDir,
	}
	envAssign := envToken(baseEnv, spec.Env, spec.CleanEnv)

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
	command = "bash -c " + shellQuote(command)

	req := osb.RunCommandRequest{
		Command: command,
		Cwd:     "", // cwd is already handled by `cd` in the command
		Timeout: int64(timeout / time.Millisecond),
	}
	if req.Timeout <= 0 {
		req.Timeout = int64(defaultRunTimeout / time.Millisecond)
	}

	start := time.Now()
	exec, runErr := sb.RunCommandWithOpts(ctx, req, nil)
	dur := time.Since(start)

	res := codeexecutor.RunResult{
		Duration: dur,
	}
	if exec != nil {
		res.Stdout = exec.Text()
		var stderrB strings.Builder
		for _, m := range exec.Stderr {
			if stderrB.Len() > 0 {
				stderrB.WriteByte('\n')
			}
			stderrB.WriteString(m.Text)
		}
		res.Stderr = stderrB.String()
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
	defer r.Cleanup(ctx, ws)

	var allOut, allErr strings.Builder
	start := time.Now()
	for i, b := range blocks {
		fn, mode, cmd, args, err := codeexecutor.BuildBlockSpec(i, b)
		if err != nil {
			allErr.WriteString(err.Error())
			allErr.WriteString("\n")
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
		}
		if res.Stdout != "" {
			allOut.WriteString(res.Stdout)
		}
		if res.Stderr != "" {
			allErr.WriteString(res.Stderr)
		}
	}
	dur := time.Since(start)
	return codeexecutor.RunResult{
		Stdout:   allOut.String(),
		Stderr:   allErr.String(),
		ExitCode: 0,
		Duration: dur,
		TimedOut: false,
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
	exec, err := sb.RunCommandWithOpts(ctx, req, nil)
	if err != nil {
		if exec != nil {
			return exec.Text(), err
		}
		return "", err
	}
	if exec.ExitCode != nil && *exec.ExitCode != 0 {
		var stderrB strings.Builder
		for _, m := range exec.Stderr {
			if stderrB.Len() > 0 {
				stderrB.WriteByte('\n')
			}
			stderrB.WriteString(m.Text)
		}
		return exec.Text(), fmt.Errorf(
			"opensandbox: bash exit %d: %s",
			*exec.ExitCode, stderrB.String(),
		)
	}
	return exec.Text(), nil
}

// readFile reads up to limit bytes from a remote path via the SDK's
// DownloadFile API. Returns the data and the file's full size (which
// may exceed len(data) when truncated).
func (r *workspaceRuntime) readFile(
	ctx context.Context, sb *osb.Sandbox, full string, limit int64,
) ([]byte, int64, error) {
	if limit <= 0 {
		limit = maxReadSizeBytes
	}
	// Request one extra byte to detect truncation: if the server
	// returns limit+1 bytes, the file exceeds the cap.
	rangeHeader := fmt.Sprintf("bytes=0-%d", limit)
	rc, err := sb.DownloadFile(ctx, full, rangeHeader)
	if err != nil {
		return nil, 0, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, 0, err
	}
	size := int64(len(data))
	if size > limit {
		// File is at least limit+1 bytes; truncate the payload to
		// limit and keep size > len(data) so callers can detect
		// truncation via size > len(truncatedData).
		data = data[:limit]
	}
	return data, size, nil
}

// listFilesByGlob resolves the provided patterns inside the sandbox
// using the SDK's SearchFiles API and returns absolute file paths.
// SearchFiles matches a single glob per call, so we iterate over
// patterns and dedup results.
func (r *workspaceRuntime) listFilesByGlob(
	ctx context.Context, wsPath string, patterns []string,
) ([]string, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	sb, err := r.sandbox()
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		infos, err := sb.SearchFiles(ctx, wsPath, p)
		if err != nil {
			// SearchFiles may error on unsupported glob syntax;
			// skip the pattern rather than failing the whole call.
			continue
		}
		for _, fi := range infos {
			// Directories returned by SearchFiles are rare for glob
			// patterns; we accept the slight over-inclusion rather
			// than miss files.
			clean := path.Clean(fi.Path)
			if !pathUnder(clean, wsPath) {
				continue
			}
			if seen[clean] {
				continue
			}
			seen[clean] = true
			out = append(out, clean)
		}
	}
	return out, nil
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

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "deadline exceeded")
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
