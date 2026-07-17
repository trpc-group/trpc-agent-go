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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

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
	// Fail closed on per-run ResourceLimits: OpenSandbox only accepts
	// resource caps at sandbox create time (WithResourceLimits). Silently
	// ignoring non-zero Limits would look like a cgroup policy was applied.
	if err := validateRunProgramLimits(spec.Limits); err != nil {
		return codeexecutor.RunResult{}, err
	}
	sb, err := r.sandbox()
	if err != nil {
		return codeexecutor.RunResult{}, err
	}

	timeout, err := r.resolveRunTimeout(spec.Timeout)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	cwd, err := r.resolveRunCwd(ctx, ws, spec.Cwd)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}

	skillsDir := path.Join(ws.Path, codeexecutor.DirSkills)
	workDir := path.Join(ws.Path, codeexecutor.DirWork)
	outDir := path.Join(ws.Path, codeexecutor.DirOut)
	runsDir := path.Join(ws.Path, codeexecutor.DirRuns)
	runDir := path.Join(
		runsDir,
		fmt.Sprintf("run_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&r.runSeq, 1)),
	)
	// Reject layout directories that are symlinks outside the workspace
	// before mkdir/upload. In PerSession mode a previous turn can leave
	// skills/work/runs/out as symlinks; mkdir -p and UploadFiles would
	// otherwise follow them.
	if err := r.ensureLayoutDirs(ctx, ws, skillsDir, workDir, outDir, runsDir); err != nil {
		return codeexecutor.RunResult{}, err
	}
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

	stdinRedir, err := r.prepareStdinRedirect(ctx, sb, ws, runDir, spec.Stdin)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	command := buildRunCommand(cwd, runDir, outDir, envAssign, spec, stdinRedir)

	req := osb.RunCommandRequest{
		Command: command,
		Cwd:     "", // cwd is already handled by `cd` in the command
		Timeout: int64(timeout / time.Millisecond),
	}
	return r.executeRunCommand(ctx, sb, req)
}

// resolveRunTimeout normalizes and validates spec.Timeout against the
// HTTP request budget.
func (r *workspaceRuntime) resolveRunTimeout(timeout time.Duration) (time.Duration, error) {
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}
	// Reject sub-millisecond timeouts explicitly. The OpenSandbox API
	// accepts timeout in integer milliseconds; a value like 500µs would
	// be truncated to 0, then silently fall back to defaultRunTimeout.
	if timeout > 0 && timeout < time.Millisecond {
		return 0, fmt.Errorf(
			"opensandbox: spec.Timeout %s is below the 1ms API granularity; "+
				"the OpenSandbox RunCommand timeout is an integer number of "+
				"milliseconds and sub-millisecond values would be truncated to 0",
			timeout,
		)
	}
	// The SDK applies ConnectionConfig.RequestTimeout to ALL HTTP
	// requests including streaming /command.
	if r.ce.requestTimeout > 0 {
		maxRun := r.ce.requestTimeout - requestTimeoutBuffer
		if maxRun > 0 && timeout > maxRun {
			return 0, fmt.Errorf(
				"opensandbox: spec.Timeout %s exceeds the request timeout budget %s "+
					"(HTTP client timeout %s - %s buffer); raise WithRequestTimeout "+
					"(or WithExecutionTimeout, which sets the floor) to allow longer runs",
				timeout, maxRun, r.ce.requestTimeout, requestTimeoutBuffer,
			)
		}
	}
	return timeout, nil
}

// prepareStdinRedirect uploads stdin to runDir/stdin when non-empty and
// returns a shell redirect fragment (or "").
func (r *workspaceRuntime) prepareStdinRedirect(
	ctx context.Context,
	sb *osb.Sandbox,
	ws codeexecutor.Workspace,
	runDir, stdin string,
) (string, error) {
	if stdin == "" {
		return "", nil
	}
	stdinPath := path.Join(runDir, "stdin")
	// runDir may not exist yet; create it and strip a leaf symlink.
	if err := r.ensureLayoutDirs(ctx, ws, runDir); err != nil {
		return "", err
	}
	if err := r.removeSymlinksBatch(ctx, []string{stdinPath}, ws.Path); err != nil {
		return "", err
	}
	if err := sb.UploadFiles(ctx, []osb.UploadFileEntry{{
		File: strings.NewReader(stdin),
		Options: osb.UploadFileOptions{
			FileName: "stdin",
			Metadata: osb.FileMetadata{
				Path: stdinPath,
				Mode: osb.OctalMode(0o600),
			},
		},
	}}); err != nil {
		return "", fmt.Errorf("opensandbox: upload stdin: %w", err)
	}
	return " < " + shellQuote(stdinPath), nil
}

// buildRunCommand assembles the remote shell command for RunProgram.
func buildRunCommand(
	cwd, runDir, outDir, envAssign string,
	spec codeexecutor.RunProgramSpec,
	stdinRedir string,
) string {
	quotedCmd := shellQuote(spec.Cmd)
	var quotedArgs strings.Builder
	for _, a := range spec.Args {
		quotedArgs.WriteByte(' ')
		quotedArgs.WriteString(shellQuote(a))
	}
	// mkdir -p the runDir and outDir so the spawned program can write
	// scratch/output files. Layout dirs were already stripped/verified
	// above; mkdir here is for the per-run subdirectory.
	command := fmt.Sprintf(
		"mkdir -p %s %s && cd %s && %s%s%s%s",
		shellQuote(runDir), shellQuote(outDir),
		shellQuote(cwd),
		envAssign, quotedCmd, quotedArgs.String(),
		stdinRedir,
	)
	// Best-effort CleanEnv prefix. OpenSandbox execd still merges
	// os.Environ() into the outer shell, so SupportsCleanEnv is false;
	// this prefix only helps the inner command string.
	if spec.CleanEnv {
		return "env -i PATH=" + shellQuote(minimalCleanPATH) +
			" bash --norc --noprofile -c " + shellQuote(command)
	}
	return "bash -c " + shellQuote(command)
}

// executeRunCommand runs a prepared RunCommandRequest with capped
// stdout/stderr handlers and maps timeout/exit semantics.
func (r *workspaceRuntime) executeRunCommand(
	ctx context.Context,
	sb *osb.Sandbox,
	req osb.RunCommandRequest,
) (codeexecutor.RunResult, error) {
	start := time.Now()
	// Use ExecutionHandlers with SkipAccumulation to prevent the SDK
	// from accumulating unbounded stdout/stderr in the Execution struct.
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
	res := codeexecutor.RunResult{
		Duration: time.Since(start),
		Stdout:   stdoutBuf.string(),
		Stderr:   stderrBuf.string(),
	}
	if exec != nil {
		// SDK may return nil Go error while Execution.Error is set after
		// an SSE error event (exit code often nil). Never treat that as
		// exit 0: put details in stderr and set a non-zero ExitCode.
		// Do NOT convert this into runErr — ExecuteCode aggregates via
		// ExitCode/stderr and must continue subsequent blocks.
		if exec.Error != nil {
			res.Stderr = formatExecutionError(exec.Error, res.Stderr)
			if exec.ExitCode != nil {
				res.ExitCode = *exec.ExitCode
			} else {
				res.ExitCode = -1
			}
		} else if exec.ExitCode != nil {
			res.ExitCode = *exec.ExitCode
		} else if runErr == nil {
			// Incomplete stream (e.g. mock noComplete): not success.
			res.ExitCode = -1
		}
	}
	if runErr != nil {
		if isTimeoutErr(runErr) {
			res.TimedOut = true
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
		// Use cappedOutputBuffer (not strings.Builder) so a long
		// sequence of verbose blocks cannot exhaust host memory.
		// Consistent with ExecuteCode, which uses the same cap.
		//
		// allOut and allErr share a single byte budget
		// (sharedUsed) so that the combined output across both
		// streams is capped at maxAggregateOutputBytes, not
		// 2 × maxAggregateOutputBytes.
		sharedUsed int
		allOut     = cappedOutputBuffer{sharedUsed: &sharedUsed}
		allErr     = cappedOutputBuffer{sharedUsed: &sharedUsed}
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
	// Reject sub-millisecond timeouts (see RunProgram for rationale).
	if timeout > 0 && timeout < time.Millisecond {
		return "", fmt.Errorf(
			"opensandbox: runBash timeout %s is below the 1ms API granularity",
			timeout,
		)
	}
	req := osb.RunCommandRequest{
		Command: "bash -c " + shellQuote(script),
		Timeout: int64(timeout / time.Millisecond),
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
	// Surface structured SSE errors even when the Go error is nil (SDK
	// quirk). Prefer the historical "bash exit N" form when a non-zero
	// exit code is present so existing tests and logs stay stable.
	if exec != nil && exec.Error != nil {
		if exec.ExitCode != nil && *exec.ExitCode != 0 {
			return stdoutBuf.string(), fmt.Errorf(
				"opensandbox: bash exit %d: %s",
				*exec.ExitCode, formatExecutionError(exec.Error, stderrBuf.string()),
			)
		}
		return stdoutBuf.string(), fmt.Errorf(
			"opensandbox: bash exit %d: %s",
			-1, formatExecutionError(exec.Error, stderrBuf.string()),
		)
	}
	if exec != nil && exec.ExitCode != nil && *exec.ExitCode != 0 {
		return stdoutBuf.string(), fmt.Errorf(
			"opensandbox: bash exit %d: %s",
			*exec.ExitCode, stderrBuf.string(),
		)
	}
	// Nil ExitCode without Error: treat as success for infrastructure
	// helpers. Mock streams used by layout/mkdir often omit exit_code
	// on non-RunProgram commands; failing closed here breaks CreateWorkspace.
	return stdoutBuf.string(), nil
}

// ensureLayoutDirs strips symlink hijacks on the given absolute paths
// under ws, recreates them as real directories, and verifies each
// resolved path stays under ws.Path. Used by RunProgram so PerSession
// workspaces cannot redirect mkdir/upload via skills/work/runs/out
// symlinks planted by earlier turns.
func (r *workspaceRuntime) ensureLayoutDirs(
	ctx context.Context, ws codeexecutor.Workspace, dirs ...string,
) error {
	if len(dirs) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("set -e; ")
	for _, d := range dirs {
		if !pathUnder(d, ws.Path) && d != ws.Path {
			return fmt.Errorf("opensandbox: layout path %q escapes workspace", d)
		}
		sb.WriteString("if [ -L ")
		sb.WriteString(shellQuote(d))
		sb.WriteString(" ]; then rm -f -- ")
		sb.WriteString(shellQuote(d))
		sb.WriteString(" || exit; fi; mkdir -p ")
		sb.WriteString(shellQuote(d))
		sb.WriteString("; r=$(readlink -f -- ")
		sb.WriteString(shellQuote(d))
		sb.WriteString(" 2>/dev/null || true); case \"$r\" in ")
		sb.WriteString(shellQuote(ws.Path))
		sb.WriteString("|")
		sb.WriteString(shellQuote(ws.Path))
		sb.WriteString("/*) ;; *) echo \"opensandbox: path escapes workspace: \" \"$r\" >&2; exit 1 ;; esac; ")
	}
	if _, err := r.runBash(ctx, sb.String(), defaultCreateTimeout); err != nil {
		return fmt.Errorf("opensandbox: ensure layout dirs: %w", err)
	}
	return nil
}

// isTimeoutErr reports whether err represents a command execution
// timeout (as opposed to an infrastructure/network failure).
//
// Only the SDK's structured APIError with code "timeout" is recognized
// as a program execution timeout. This is the signal sent by the
// OpenSandbox server when the per-command Timeout (in the
// RunCommandRequest) is exceeded.
//
// The following are deliberately NOT classified as program timeouts,
// even though they are "timeout-like" errors:
//
//   - context.DeadlineExceeded: fires when the *caller's* context
//     deadline is hit (e.g. agent-level turn timeout, gRPC RPC
//     deadline). This is a caller-side cancellation, not a sandbox
//     program timeout. Treating it as TimedOut would mask
//     infrastructure-level cancellations and mislead the agent into
//     retrying as if the program simply ran too long.
//   - net.Error.Timeout(): fires on HTTP client request deadlines,
//     connection dial timeouts, TLS handshake timeouts, etc. These
//     are infrastructure failures between the agent and the
//     OpenSandbox server/proxy, not program execution timeouts.
//     Treating a 504 gateway timeout or a connection-refused timeout
//     as TimedOut would hide real infrastructure problems.
//
// The SDK's RunCommandWithOpts uses the req.Timeout field (milliseconds)
// for per-command execution timeout, NOT context.WithTimeout. So a
// genuine program execution timeout surfaces as an APIError with
// code "timeout", not as context.DeadlineExceeded.
func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	// Only the SDK's structured APIError with code "timeout" is a
	// genuine program execution timeout. The mock server returns
	// {"code":"timeout",...} for command execution timeouts; a 504
	// gateway timeout or connection dial timeout would have a
	// different code (or not be an APIError at all) and must NOT be
	// classified as a command timeout.
	var apiErr *osb.APIError
	if errors.As(err, &apiErr) {
		if strings.EqualFold(apiErr.Response.Code, "timeout") {
			return true
		}
	}
	return false
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

// cappedOutputBuffer accumulates string data up to maxAggregateOutputBytes,
// then drops further writes and appends a truncation marker. Used by
// ExecuteCode (single buffer for stdout+stderr) and ExecuteInline (two
// buffers that share a budget) to bound the total aggregated output
// across all code blocks. Implements io.Writer so fmt.Fprintf can write
// directly to it.
//
// When sharedUsed is non-nil, the cap is checked against the shared
// counter instead of buf.Len(). This lets ExecuteInline allocate one
// budget across both stdout and stderr (preventing 2 × maxAggregateOutputBytes
// total), while ExecuteCode (which uses a single buffer) leaves
// sharedUsed nil and uses buf.Len() directly.
type cappedOutputBuffer struct {
	buf       strings.Builder
	truncated bool
	// sharedUsed, when non-nil, tracks the total bytes written across
	// a group of buffers that share one budget. The owner is
	// responsible for ensuring *sharedUsed is only mutated through
	// this buffer's methods.
	sharedUsed *int
}

// Write implements io.Writer.
func (b *cappedOutputBuffer) Write(p []byte) (int, error) {
	b.WriteString(string(p))
	return len(p), nil
}

// used returns the current byte count against which the cap is
// enforced: *sharedUsed when sharing a budget, buf.Len() otherwise.
func (b *cappedOutputBuffer) used() int {
	if b.sharedUsed != nil {
		return *b.sharedUsed
	}
	return b.buf.Len()
}

// addUsed increments the shared counter (if active) so peer buffers
// see the updated total.
func (b *cappedOutputBuffer) addUsed(n int) {
	if b.sharedUsed != nil {
		*b.sharedUsed += n
	}
}

// WriteString appends s to the buffer unless the cap has been reached.
func (b *cappedOutputBuffer) WriteString(s string) {
	if b.truncated {
		return
	}
	used := b.used()
	if used+len(s) > maxAggregateOutputBytes {
		remaining := maxAggregateOutputBytes - used
		if remaining > 0 {
			b.buf.WriteString(s[:remaining])
			b.addUsed(remaining)
		}
		fmt.Fprintf(&b.buf, "\n[output truncated: exceeded %d bytes]\n", maxAggregateOutputBytes)
		b.truncated = true
		return
	}
	b.buf.WriteString(s)
	b.addUsed(len(s))
}

// WriteByte appends a single byte to the buffer.
func (b *cappedOutputBuffer) WriteByte(c byte) error {
	b.WriteString(string(c))
	return nil
}

func (b *cappedOutputBuffer) String() string {
	return b.buf.String()
}
