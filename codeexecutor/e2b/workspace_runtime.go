//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2b

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	ci "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/codeinterpreter"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// Compile-time check that workspaceRuntime satisfies the expected interfaces.
var (
	_ codeexecutor.WorkspaceManager = (*workspaceRuntime)(nil)
	_ codeexecutor.ProgramRunner    = (*workspaceRuntime)(nil)
)

const (
	// Base directory inside the E2B sandbox where per-execution workspaces
	// are created. /tmp is writable in the default template.
	defaultSandboxRunBase = "/tmp/run"

	defaultCreateTimeout  = 15 * time.Second
	defaultRmTimeout      = 15 * time.Second
	defaultStageTimeout   = 60 * time.Second
	defaultCollectTimeout = 30 * time.Second
	defaultRunTimeout     = 30 * time.Second

	// Maximum bytes read back from the sandbox for a single file when
	// collecting outputs.
	maxReadSizeBytes = 4 * 1024 * 1024 // 4 MiB

	// Sentinels used to frame RunProgram stdout / stderr / exit code so
	// wrapper-script noise is stripped before returning to callers.
	sentinelStdoutBegin = "__E2B_STDOUT_BEGIN__"
	sentinelStdoutEnd   = "__E2B_STDOUT_END__"
	sentinelStderrBegin = "__E2B_STDERR_BEGIN__"
	sentinelStderrEnd   = "__E2B_STDERR_END__"
	sentinelExitPrefix  = "__E2B_EXITCODE__="

	metadataFileMode = 0o600
)

// workspaceRuntime implements WorkspaceManager / WorkspaceFS / ProgramRunner
// for the E2B sandbox.
type workspaceRuntime struct {
	ce  *CodeExecutor
	cfg runtimeConfig
}

type runtimeConfig struct {
	runBase string
}

func newWorkspaceRuntime(c *CodeExecutor) *workspaceRuntime {
	base := strings.TrimSpace(c.sandboxRunBase)
	if base == "" {
		base = defaultSandboxRunBase
	}
	return &workspaceRuntime{ce: c, cfg: runtimeConfig{runBase: base}}
}

// CreateWorkspace creates a per-execution directory inside the sandbox.
func (r *workspaceRuntime) CreateWorkspace(
	ctx context.Context,
	execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceCreate)
	span.SetAttributes(attribute.String(codeexecutor.AttrExecID, execID))
	defer span.End()
	_ = pol

	if r.ce == nil || r.ce.sbx == nil {
		return codeexecutor.Workspace{}, errors.New(
			"e2b: sandbox not initialized",
		)
	}

	safe := sanitize(execID)
	suf := time.Now().UnixNano()
	wsPath := path.Join(
		r.cfg.runBase, fmt.Sprintf("ws_%s_%d", safe, suf),
	)

	var sb strings.Builder
	sb.WriteString("set -e; mkdir -p ")
	for _, d := range []string{
		wsPath,
		path.Join(wsPath, codeexecutor.DirSkills),
		path.Join(wsPath, codeexecutor.DirWork),
		path.Join(wsPath, codeexecutor.DirRuns),
		path.Join(wsPath, codeexecutor.DirOut),
	} {
		sb.WriteString(shellQuote(d))
		sb.WriteByte(' ')
	}
	sb.WriteString("; [ -f ")
	sb.WriteString(shellQuote(path.Join(wsPath, codeexecutor.MetaFileName)))
	sb.WriteString(" ] || echo '{}' > ")
	sb.WriteString(shellQuote(path.Join(wsPath, codeexecutor.MetaFileName)))

	if _, _, _, err := r.runBash(ctx, sb.String(), defaultCreateTimeout); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return codeexecutor.Workspace{}, err
	}
	return codeexecutor.Workspace{ID: execID, Path: wsPath}, nil
}

// Cleanup removes the workspace directory from the sandbox.
func (r *workspaceRuntime) Cleanup(
	ctx context.Context,
	ws codeexecutor.Workspace,
) error {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceCleanup)
	span.SetAttributes(attribute.String(codeexecutor.AttrPath, ws.Path))
	defer span.End()
	if ws.Path == "" {
		return nil
	}
	script := "rm -rf " + shellQuote(ws.Path)
	_, _, _, err := r.runBash(ctx, script, defaultRmTimeout)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

// PutFiles writes files into the sandbox workspace. Content is shipped via
// base64 to stay binary-safe.
func (r *workspaceRuntime) PutFiles(
	ctx context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	_, span := atrace.Tracer.Start(ctx,
		codeexecutor.SpanWorkspaceStageFiles)
	span.SetAttributes(attribute.Int(codeexecutor.AttrCount, len(files)))
	defer span.End()
	if len(files) == 0 {
		return nil
	}

	data, err := tarGzFromFiles(files)
	if err != nil {
		return err
	}
	if err := r.uploadTarGzAndExtract(ctx, ws.Path, data); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

// PutDirectory packs a host directory into tar.gz then extracts it in the
// sandbox under ws.Path/to.
func (r *workspaceRuntime) PutDirectory(
	ctx context.Context,
	ws codeexecutor.Workspace,
	hostPath string,
	to string,
) error {
	_, span := atrace.Tracer.Start(ctx,
		codeexecutor.SpanWorkspaceStageDir)
	span.SetAttributes(
		attribute.String(codeexecutor.AttrHostPath, hostPath),
		attribute.String(codeexecutor.AttrTo, to),
	)
	defer span.End()

	if strings.TrimSpace(hostPath) == "" {
		return errors.New("hostPath is empty")
	}
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return err
	}
	data, err := tarGzFromDir(abs)
	if err != nil {
		return err
	}
	dest := ws.Path
	if to != "" {
		dest = path.Join(ws.Path, filepath.ToSlash(to))
	}
	if err := r.uploadTarGzAndExtract(ctx, dest, data); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	span.SetAttributes(attribute.Bool(codeexecutor.AttrMountUsed, false))
	return nil
}

// StageDirectory stages a directory with options (ReadOnly + AllowMount).
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
		if _, _, _, err := r.runBash(
			ctx, script, defaultStageTimeout,
		); err != nil {
			return err
		}
	}
	return nil
}

// Collect returns files in the workspace that match the supplied globs.
func (r *workspaceRuntime) Collect(
	ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceCollect)
	span.SetAttributes(attribute.Int(codeexecutor.AttrPatterns, len(patterns)))
	defer span.End()

	patterns = codeexecutor.NormalizeGlobs(patterns)
	if len(patterns) == 0 {
		return nil, nil
	}
	paths, err := r.listFilesByGlob(ctx, ws.Path, patterns)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	out := make([]codeexecutor.File, 0, len(paths))
	seen := map[string]bool{}
	for _, full := range paths {
		rel := strings.TrimPrefix(full, ws.Path+"/")
		if rel == full {
			rel = filepath.ToSlash(full)
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		data, size, err := r.readFile(ctx, full, maxReadSizeBytes)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
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
	span.SetAttributes(attribute.Int(codeexecutor.AttrCount, len(out)))
	return out, nil
}

// StageInputs maps external inputs into the sandbox workspace.
func (r *workspaceRuntime) StageInputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	if r.ce == nil || r.ce.sbx == nil {
		return errors.New("e2b: sandbox not initialized")
	}
	md, err := r.loadWorkspaceMetadata(ctx, ws)
	if err != nil {
		return err
	}
	for _, sp := range specs {
		mode := strings.ToLower(strings.TrimSpace(sp.Mode))
		if mode == "" {
			mode = "copy"
		}
		to := strings.TrimSpace(sp.To)
		if to == "" {
			base := inputBase(sp.From)
			to = path.Join(codeexecutor.DirWork, "inputs", base)
		}
		resolved, ver, err := r.stageInput(ctx, ws, md, sp, mode, to)
		if err != nil {
			return err
		}
		md.Inputs = append(md.Inputs, codeexecutor.InputRecord{
			From:      sp.From,
			To:        to,
			Resolved:  resolved,
			Version:   ver,
			Mode:      mode,
			Timestamp: time.Now(),
		})
	}
	return r.saveWorkspaceMetadata(ctx, ws, md)
}

func (r *workspaceRuntime) stageInput(
	ctx context.Context,
	ws codeexecutor.Workspace,
	md codeexecutor.WorkspaceMetadata,
	sp codeexecutor.InputSpec,
	mode string,
	to string,
) (string, *int, error) {
	dest := path.Join(ws.Path, filepath.ToSlash(to))
	switch {
	case strings.HasPrefix(sp.From, inputSchemeArtifact):
		return r.stageArtifactInput(ctx, md, sp, to, dest)
	case strings.HasPrefix(sp.From, inputSchemeHost):
		host := strings.TrimPrefix(sp.From, inputSchemeHost)
		err := r.PutDirectory(ctx, ws, host, path.Dir(to))
		return host, nil, err
	case strings.HasPrefix(sp.From, inputSchemeWorkspace):
		return r.stageCopyInsideSandbox(ctx, ws, sp, inputSchemeWorkspace,
			mode, dest, "")
	case strings.HasPrefix(sp.From, inputSchemeSkill):
		return r.stageCopyInsideSandbox(ctx, ws, sp, inputSchemeSkill,
			mode, dest, codeexecutor.DirSkills)
	default:
		return "", nil, fmt.Errorf("unsupported input: %s", sp.From)
	}
}

func (r *workspaceRuntime) stageArtifactInput(
	ctx context.Context,
	md codeexecutor.WorkspaceMetadata,
	sp codeexecutor.InputSpec,
	to string,
	dest string,
) (string, *int, error) {
	name := strings.TrimPrefix(sp.From, inputSchemeArtifact)
	aname, aver, err := codeexecutor.ParseArtifactRef(name)
	if err != nil {
		return "", nil, err
	}
	useVer := aver
	if useVer == nil && sp.Pin {
		useVer = pinnedArtifactVersion(md, aname, to)
	}
	data, _, actual, err := codeexecutor.LoadArtifactHelper(
		ctx, aname, useVer,
	)
	if err != nil {
		return "", nil, err
	}
	ver := useVer
	if ver == nil {
		v := actual
		ver = &v
	} else {
		v := *ver
		ver = &v
	}
	if err := r.writeBytesToSandbox(ctx, dest, data, 0o644); err != nil {
		return "", nil, err
	}
	return aname, ver, nil
}

// stageCopyInsideSandbox resolves a workspace:// or skill:// reference and
// copies/links the target into dest, using only commands inside the sandbox.
func (r *workspaceRuntime) stageCopyInsideSandbox(
	ctx context.Context,
	ws codeexecutor.Workspace,
	sp codeexecutor.InputSpec,
	scheme string,
	mode string,
	dest string,
	rootSub string,
) (string, *int, error) {
	rest := strings.TrimPrefix(sp.From, scheme)
	root := ws.Path
	if rootSub != "" {
		root = path.Join(ws.Path, rootSub)
	}
	src := path.Join(root, filepath.ToSlash(rest))
	var script strings.Builder
	script.WriteString("mkdir -p ")
	script.WriteString(shellQuote(path.Dir(dest)))
	script.WriteString(" && ")
	if mode == "link" {
		script.WriteString("ln -sfn ")
		script.WriteString(shellQuote(src))
		script.WriteByte(' ')
		script.WriteString(shellQuote(dest))
	} else {
		script.WriteString("cp -a ")
		script.WriteString(shellQuote(src))
		script.WriteByte(' ')
		script.WriteString(shellQuote(dest))
	}
	if _, _, _, err := r.runBash(
		ctx, script.String(), defaultStageTimeout,
	); err != nil {
		return "", nil, err
	}
	return src, nil, nil
}

func pinnedArtifactVersion(
	md codeexecutor.WorkspaceMetadata,
	name string,
	to string,
) *int {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(to) == "" {
		return nil
	}
	for i := len(md.Inputs) - 1; i >= 0; i-- {
		rec := md.Inputs[i]
		if rec.To != to {
			continue
		}
		if rec.Version == nil {
			continue
		}
		if rec.Resolved == name {
			return rec.Version
		}
		if !strings.HasPrefix(rec.From, inputSchemeArtifact) {
			continue
		}
		ref := strings.TrimPrefix(rec.From, inputSchemeArtifact)
		rname, _, err := codeexecutor.ParseArtifactRef(ref)
		if err == nil && rname == name {
			return rec.Version
		}
	}
	return nil
}

// CollectOutputs applies sandbox-side globs and optionally persists artifacts.
func (r *workspaceRuntime) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	globs := codeexecutor.NormalizeGlobs(spec.Globs)
	paths, err := r.listFilesByGlob(ctx, ws.Path, globs)
	if err != nil {
		return codeexecutor.OutputManifest{}, err
	}

	maxFiles := spec.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 100
	}
	maxFileBytes := spec.MaxFileBytes
	if maxFileBytes <= 0 {
		maxFileBytes = maxReadSizeBytes
	}
	maxTotal := spec.MaxTotalBytes
	if maxTotal <= 0 {
		maxTotal = 64 * 1024 * 1024
	}
	left := maxTotal

	mf := codeexecutor.OutputManifest{}
	count := 0
	for _, full := range paths {
		if count >= maxFiles || left <= 0 {
			mf.LimitsHit = true
			break
		}
		limit := maxFileBytes
		if int64(limit) > left {
			limit = left
		}
		data, size, err := r.readFile(ctx, full, limit)
		if err != nil {
			return codeexecutor.OutputManifest{}, err
		}
		mime := http.DetectContentType(data)
		truncated := size > int64(len(data))
		if truncated {
			mf.LimitsHit = true
		}
		if truncated && spec.Save {
			rel := strings.TrimPrefix(full, ws.Path+"/")
			return codeexecutor.OutputManifest{}, fmt.Errorf(
				"cannot save truncated output file: %s", rel,
			)
		}
		left -= int64(len(data))
		rel := strings.TrimPrefix(full, ws.Path+"/")
		ref := codeexecutor.FileRef{
			Name:      rel,
			MIMEType:  mime,
			SizeBytes: size,
			Truncated: truncated,
		}
		if spec.Inline {
			ref.Content = string(data)
		}
		if spec.Save {
			saveName := rel
			if spec.NameTemplate != "" {
				saveName = spec.NameTemplate + rel
			}
			ver, err := codeexecutor.SaveArtifactHelper(
				ctx, saveName, data, mime,
			)
			if err != nil {
				return codeexecutor.OutputManifest{}, err
			}
			ref.SavedAs = saveName
			ref.Version = ver
		}
		mf.Files = append(mf.Files, ref)
		count++
	}
	return mf, nil
}

// RunProgram runs an arbitrary command inside the sandbox workspace.
func (r *workspaceRuntime) RunProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceRun)
	span.SetAttributes(
		attribute.String(codeexecutor.AttrCmd, spec.Cmd),
		attribute.String(codeexecutor.AttrCwd, spec.Cwd),
	)
	defer span.End()

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultRunTimeout
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
	var envParts []string
	for k, v := range baseEnv {
		if _, ok := spec.Env[k]; ok {
			continue
		}
		envParts = append(envParts, k+"="+shellQuote(v))
	}
	for k, v := range spec.Env {
		envParts = append(envParts, k+"="+shellQuote(v))
	}

	quotedCmd := shellQuote(spec.Cmd)
	var quotedArgs strings.Builder
	for _, a := range spec.Args {
		quotedArgs.WriteByte(' ')
		quotedArgs.WriteString(shellQuote(a))
	}

	var stdinRedir string
	if spec.Stdin != "" {
		b64 := base64.StdEncoding.EncodeToString([]byte(spec.Stdin))
		stdinRedir = " < <(printf %s " + shellQuote(b64) + " | base64 -d)"
	}

	envAssign := ""
	if len(envParts) > 0 {
		envAssign = "env " + strings.Join(envParts, " ") + " "
	}

	inner := fmt.Sprintf(
		"mkdir -p %s %s && cd %s && %s%s%s%s",
		shellQuote(runDir), shellQuote(outDir),
		shellQuote(cwd),
		envAssign, quotedCmd, quotedArgs.String(),
		stdinRedir,
	)
	script := buildRunWrapper(inner)

	start := time.Now()
	stdoutRaw, stderrRaw, _, err := r.runBashStreaming(ctx, script, timeout)
	dur := time.Since(start)

	stdout, stderr, exit := parseFramedOutput(stdoutRaw, stderrRaw)

	timedOut := false
	if err != nil {
		if isTimeoutErr(err) {
			timedOut = true
			err = nil
		}
	}

	res := codeexecutor.RunResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exit,
		Duration: dur,
		TimedOut: timedOut,
	}
	span.SetAttributes(
		attribute.Int(codeexecutor.AttrExitCode, res.ExitCode),
		attribute.Bool(codeexecutor.AttrTimedOut, res.TimedOut),
	)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return res, err
}

// buildRunWrapper produces a bash script that executes `inner` while framing
// stdout/stderr/exit-code with sentinels, so the driver can parse them out.
func buildRunWrapper(inner string) string {
	var b strings.Builder
	b.WriteString("__ERR=$(mktemp); ")
	b.WriteString("echo " + sentinelStdoutBegin + "; ")
	b.WriteString("{ ")
	b.WriteString(inner)
	b.WriteString("; } 2>\"$__ERR\"; __EC=$?; ")
	b.WriteString("echo " + sentinelStdoutEnd + "; ")
	b.WriteString("echo " + sentinelExitPrefix + "$__EC; ")
	b.WriteString("echo " + sentinelStderrBegin + " >&2; ")
	b.WriteString("cat \"$__ERR\" >&2; ")
	b.WriteString("echo " + sentinelStderrEnd + " >&2; ")
	b.WriteString("rm -f \"$__ERR\"")
	return b.String()
}

// parseFramedOutput extracts the user's stdout/stderr and exit code from the
// framed streaming output produced by buildRunWrapper.
func parseFramedOutput(rawStdout, rawStderr string) (string, string, int) {
	stdout := extractBetween(rawStdout, sentinelStdoutBegin,
		sentinelStdoutEnd)
	stderr := extractBetween(rawStderr, sentinelStderrBegin,
		sentinelStderrEnd)
	exit := 0
	if idx := strings.LastIndex(rawStdout, sentinelExitPrefix); idx >= 0 {
		rest := rawStdout[idx+len(sentinelExitPrefix):]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[:nl]
		}
		rest = strings.TrimSpace(rest)
		if v, err := strconv.Atoi(rest); err == nil {
			exit = v
		}
	}
	return stdout, stderr, exit
}

// extractBetween returns the text between begin and end sentinels. Surrounding
// newlines added by `echo` are trimmed.
func extractBetween(s, begin, end string) string {
	b := strings.Index(s, begin)
	if b < 0 {
		return ""
	}
	start := b + len(begin)
	if start < len(s) && s[start] == '\n' {
		start++
	}
	rest := s[start:]
	e := strings.Index(rest, end)
	if e < 0 {
		return rest
	}
	out := rest[:e]
	out = strings.TrimRight(out, "\n")
	return out
}

// ExecuteInline writes each code block into the sandbox workspace and runs
// it, aggregating stdout/stderr from all blocks.
func (r *workspaceRuntime) ExecuteInline(
	ctx context.Context,
	execID string,
	blocks []codeexecutor.CodeBlock,
	timeout time.Duration,
) (codeexecutor.RunResult, error) {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceInline)
	span.SetAttributes(attribute.Int("blocks", len(blocks)))
	defer span.End()

	ws, err := r.CreateWorkspace(
		ctx, execID, codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
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

// runBash runs a bash snippet in the sandbox and returns the captured
// stdout, stderr and exit code.
func (r *workspaceRuntime) runBash(
	ctx context.Context, script string, timeout time.Duration,
) (string, string, int, error) {
	stdout, stderr, exit, err := r.runBashStreaming(ctx, script, timeout)
	return stdout, stderr, exit, err
}

// runBashStreaming is the low-level primitive: it invokes Sandbox.RunCode
// with LanguageBash and collects the combined stream outputs.
func (r *workspaceRuntime) runBashStreaming(
	ctx context.Context, script string, timeout time.Duration,
) (string, string, int, error) {
	if r.ce == nil || r.ce.sbx == nil {
		return "", "", 0, errors.New("e2b: sandbox not initialized")
	}
	var stdoutB, stderrB strings.Builder
	opts := &ci.RunCodeOpts{
		Language: ci.LanguageBash,
		Timeout:  timeout,
		OnStdout: func(m ci.OutputMessage) { stdoutB.WriteString(m.Line) },
		OnStderr: func(m ci.OutputMessage) { stderrB.WriteString(m.Line) },
	}
	exec, err := r.ce.sbx.RunCode(ctx, script, opts)
	if err != nil {
		return stdoutB.String(), stderrB.String(), -1, err
	}
	if exec.Error != nil {
		return stdoutB.String(), stderrB.String(), -1, fmt.Errorf(
			"bash error: %s: %s", exec.Error.Name, exec.Error.Value,
		)
	}
	return stdoutB.String(), stderrB.String(), 0, nil
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout")
}

func tarGzFromFiles(files []codeexecutor.PutFile) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	now := time.Now()
	dirSeen := map[string]bool{}
	for _, f := range files {
		clean := path.Clean(filepath.ToSlash(f.Path))
		if clean == "." || clean == "/" || clean == "" {
			return nil, fmt.Errorf("invalid file path: %s", f.Path)
		}
		if dir := path.Dir(clean); dir != "." && dir != "/" {
			parts := strings.Split(dir, "/")
			cur := ""
			for _, p := range parts {
				if p == "" {
					continue
				}
				if cur == "" {
					cur = p
				} else {
					cur = cur + "/" + p
				}
				if dirSeen[cur] {
					continue
				}
				dirSeen[cur] = true
				if err := tw.WriteHeader(&tar.Header{
					Name:     cur + "/",
					Mode:     0o755,
					ModTime:  now,
					Typeflag: tar.TypeDir,
				}); err != nil {
					return nil, err
				}
			}
		}
		mode := int64(f.Mode)
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{
			Name:    clean,
			Mode:    mode,
			Size:    int64(len(f.Content)),
			ModTime: now,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func tarGzFromDir(root string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(root,
		func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			rel = filepath.ToSlash(rel)
			info, err := d.Info()
			if err != nil {
				return err
			}
			if d.IsDir() {
				return tw.WriteHeader(&tar.Header{
					Name:     rel + "/",
					Mode:     int64(info.Mode().Perm()),
					ModTime:  info.ModTime(),
					Typeflag: tar.TypeDir,
				})
			}
			if !info.Mode().IsRegular() {
				// Skip symlinks/devices/etc for simplicity.
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if err := tw.WriteHeader(&tar.Header{
				Name:    rel,
				Mode:    int64(info.Mode().Perm()),
				Size:    int64(len(data)),
				ModTime: info.ModTime(),
			}); err != nil {
				return err
			}
			_, err = tw.Write(data)
			return err
		})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (r *workspaceRuntime) uploadTarGzAndExtract(
	ctx context.Context, dest string, data []byte,
) error {
	b64 := base64.StdEncoding.EncodeToString(data)
	var script strings.Builder
	script.WriteString("set -e; mkdir -p ")
	script.WriteString(shellQuote(dest))
	script.WriteString("; printf %s ")
	script.WriteString(shellQuote(b64))
	script.WriteString(" | base64 -d | tar -xzf - -C ")
	script.WriteString(shellQuote(dest))
	_, _, _, err := r.runBash(
		ctx, script.String(), defaultStageTimeout,
	)
	return err
}

// writeBytesToSandbox writes arbitrary bytes to dest inside the sandbox.
func (r *workspaceRuntime) writeBytesToSandbox(
	ctx context.Context, dest string, data []byte, mode os.FileMode,
) error {
	b64 := base64.StdEncoding.EncodeToString(data)
	var script strings.Builder
	script.WriteString("set -e; mkdir -p ")
	script.WriteString(shellQuote(path.Dir(dest)))
	script.WriteString("; printf %s ")
	script.WriteString(shellQuote(b64))
	script.WriteString(" | base64 -d > ")
	script.WriteString(shellQuote(dest))
	if mode != 0 {
		script.WriteString("; chmod ")
		script.WriteString(fmt.Sprintf("%o", mode))
		script.WriteByte(' ')
		script.WriteString(shellQuote(dest))
	}
	_, _, _, err := r.runBash(
		ctx, script.String(), defaultStageTimeout,
	)
	return err
}

func (r *workspaceRuntime) readFile(
	ctx context.Context, full string, limit int64,
) ([]byte, int64, error) {
	if limit <= 0 {
		limit = maxReadSizeBytes
	}
	// Echo size then base64 payload on stdout, framed so we can parse it
	// unambiguously even when file content contains ASCII sentinels.
	var script strings.Builder
	script.WriteString("set -e; F=")
	script.WriteString(shellQuote(full))
	script.WriteString("; SZ=$(stat -c%s \"$F\" 2>/dev/null || wc -c < \"$F\"); ")
	script.WriteString("echo __E2B_SIZE__=$SZ; ")
	script.WriteString("echo __E2B_B64_BEGIN__; ")
	script.WriteString(fmt.Sprintf("head -c %d \"$F\" | base64; ", limit))
	script.WriteString("echo __E2B_B64_END__")
	stdout, _, _, err := r.runBash(
		ctx, script.String(), defaultCollectTimeout,
	)
	if err != nil {
		return nil, 0, err
	}
	var size int64
	if idx := strings.Index(stdout, "__E2B_SIZE__="); idx >= 0 {
		rest := stdout[idx+len("__E2B_SIZE__="):]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[:nl]
		}
		rest = strings.TrimSpace(rest)
		if v, err := strconv.ParseInt(rest, 10, 64); err == nil {
			size = v
		}
	}
	b64 := extractBetween(stdout, "__E2B_B64_BEGIN__",
		"__E2B_B64_END__")
	// Strip newlines inserted by `base64`.
	b64 = strings.ReplaceAll(b64, "\n", "")
	b64 = strings.ReplaceAll(b64, "\r", "")
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, 0, fmt.Errorf("decode base64: %w", err)
	}
	if size == 0 {
		size = int64(len(data))
	}
	return data, size, nil
}

// listFilesByGlob resolves the provided patterns inside the sandbox using
// bash's globstar semantics and returns absolute file paths.
func (r *workspaceRuntime) listFilesByGlob(
	ctx context.Context, wsPath string, patterns []string,
) ([]string, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	// Resolve the workspace root so we can reject matches that escape it
	// (e.g. when patterns contain "..", symlinks, or absolute paths). The
	// resolved base is also emitted on stdout so the Go side can use the
	// identical form for prefix comparison even if wsPath is itself a
	// symlink.
	var cmd strings.Builder
	cmd.WriteString("cd ")
	cmd.WriteString(shellQuote(wsPath))
	cmd.WriteString(" && __e2b_base=$(readlink -f . 2>/dev/null || ")
	cmd.WriteString("realpath . 2>/dev/null || pwd); ")
	cmd.WriteString(`printf '__E2B_BASE__=%s\n' "$__e2b_base"; `)
	cmd.WriteString("shopt -s globstar nullglob dotglob; ")
	cmd.WriteString("for p in")
	for _, p := range patterns {
		cmd.WriteByte(' ')
		cmd.WriteString(shellQuote(filepath.ToSlash(p)))
	}
	cmd.WriteString("; do for f in $p; do ")
	cmd.WriteString(
		"if [ -f \"$f\" ]; then " +
			"__e2b_rp=$(readlink -f \"$f\" 2>/dev/null || " +
			"realpath \"$f\" 2>/dev/null || " +
			"echo \"$(pwd)/$f\"); " +
			"case \"$__e2b_rp\" in " +
			"\"$__e2b_base\"/*|\"$__e2b_base\") " +
			"printf '%s\\n' \"$__e2b_rp\";; " +
			"esac; " +
			"fi; ")
	cmd.WriteString("done; done")

	stdout, _, _, err := r.runBash(
		ctx, cmd.String(), defaultCollectTimeout,
	)
	if err != nil {
		return nil, err
	}
	// The first line carries the resolved workspace root used by the
	// sandbox filter; fall back to wsPath if it is missing.
	resolvedBase := wsPath
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "__E2B_BASE__=") {
			resolvedBase = strings.TrimPrefix(line, "__E2B_BASE__=")
			continue
		}
		// Defence-in-depth: also filter on the Go side against both the
		// caller-supplied wsPath and the sandbox-resolved base, in case
		// the shell filter was ever bypassed. path.Clean collapses any
		// ".." segments so a literal prefix like "/ws/../escape" can't
		// slip through.
		clean := path.Clean(line)
		if !pathUnder(clean, wsPath) && !pathUnder(clean, resolvedBase) {
			continue
		}
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out, nil
}

// pathUnder reports whether p is equal to base or nested below it. Both
// arguments are expected to be absolute POSIX paths.
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

func (r *workspaceRuntime) loadWorkspaceMetadata(
	ctx context.Context, ws codeexecutor.Workspace,
) (codeexecutor.WorkspaceMetadata, error) {
	now := time.Now()
	md := codeexecutor.WorkspaceMetadata{
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
		LastAccess: now,
		Skills:     map[string]codeexecutor.SkillMeta{},
	}
	full := path.Join(ws.Path, codeexecutor.MetaFileName)
	data, _, err := r.readFile(ctx, full, maxReadSizeBytes)
	if err != nil {
		return md, nil // fall back to fresh metadata
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "{}" {
		return md, nil
	}
	if err := json.Unmarshal(data, &md); err != nil {
		return codeexecutor.WorkspaceMetadata{}, err
	}
	if md.Version == 0 {
		md.Version = 1
	}
	if md.CreatedAt.IsZero() {
		md.CreatedAt = now
	}
	md.LastAccess = now
	if md.Skills == nil {
		md.Skills = map[string]codeexecutor.SkillMeta{}
	}
	return md, nil
}

func (r *workspaceRuntime) saveWorkspaceMetadata(
	ctx context.Context, ws codeexecutor.Workspace,
	md codeexecutor.WorkspaceMetadata,
) error {
	now := time.Now()
	if md.Version == 0 {
		md.Version = 1
	}
	if md.CreatedAt.IsZero() {
		md.CreatedAt = now
	}
	md.UpdatedAt = now
	md.LastAccess = now
	if md.Skills == nil {
		md.Skills = map[string]codeexecutor.SkillMeta{}
	}
	buf, err := json.MarshalIndent(md, "", "  ")
	if err != nil {
		return err
	}
	dest := path.Join(ws.Path, codeexecutor.MetaFileName)
	return r.writeBytesToSandbox(ctx, dest, buf, metadataFileMode)
}

// Input scheme prefixes mirrored from the container runtime.
const (
	inputSchemeArtifact  = "artifact://"
	inputSchemeHost      = "host://"
	inputSchemeWorkspace = "workspace://"
	inputSchemeSkill     = "skill://"
)

func inputBase(from string) string {
	s := strings.TrimSpace(from)
	if strings.HasPrefix(s, inputSchemeArtifact) {
		rest := strings.TrimPrefix(s, inputSchemeArtifact)
		name, _, err := codeexecutor.ParseArtifactRef(rest)
		if err == nil {
			base := path.Base(strings.TrimSpace(name))
			if base != "." && base != "/" && base != ".." && base != "" {
				return base
			}
		}
	}
	if i := strings.LastIndex(s, "/"); i >= 0 && i+1 < len(s) {
		return s[i+1:]
	}
	return s
}

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

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	q := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + q + "'"
}
