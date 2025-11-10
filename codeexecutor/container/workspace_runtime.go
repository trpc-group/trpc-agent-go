//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package container

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	tcontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	archive "github.com/moby/go-archive"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	maxReadSizeBytes        = 4 * 1024 * 1024 // 4 MiB
	defaultCreateTimeoutSec = 5
	defaultRmTimeoutSec     = 10
	defaultStageTimeoutSec  = 10
	defaultRunContainerBase = "/mnt/run"
	defaultSkillsContainer  = "/mnt/skills"
)

// workspaceRuntime provides workspace execution on Docker.
type workspaceRuntime struct {
	ce  *CodeExecutor
	cfg runtimeConfig
}

type runtimeConfig struct {
	skillsHostBase      string
	skillsContainerBase string
	runHostBase         string
	runContainerBase    string
}

// newWorkspaceRuntime builds a runtime bound to the provided executor.
func newWorkspaceRuntime(c *CodeExecutor) (*workspaceRuntime, error) {
	cfg := runtimeConfig{
		runContainerBase:    defaultRunContainerBase,
		skillsContainerBase: defaultSkillsContainer,
	}
	// Infer a host base that is bind-mounted at /mnt/skills when present.
	if c != nil {
		cfg.skillsHostBase = findBindSource(
			c.hostConfig.Binds, defaultSkillsContainer,
		)
	}
	return &workspaceRuntime{ce: c, cfg: cfg}, nil
}

// findBindSource returns the host path whose bind dest equals dest.
// Bind spec is source:dest[:mode]. We parse from right to handle ':'
// that may appear in the source path (Windows not considered here).
func findBindSource(binds []string, dest string) string {
	for _, b := range binds {
		parts := strings.Split(b, ":")
		if len(parts) < 2 {
			continue
		}
		// Last part may be mode; second last is dest.
		d := parts[len(parts)-2]
		if d != dest {
			continue
		}
		// Join all but the last two parts as source.
		src := strings.Join(parts[:len(parts)-2], ":")
		if st, err := os.Stat(src); err == nil && st.IsDir() {
			return src
		}
	}
	return ""
}

// CreateWorkspace ensures a per‑execution directory inside container.
func (r *workspaceRuntime) CreateWorkspace(
	ctx context.Context,
	execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	_, span := atrace.Tracer.Start(ctx, "workspace.create")
	span.SetAttributes(attribute.String("exec_id", execID))
	defer span.End()
	_ = pol
	if r.ce == nil || r.ce.client == nil || r.ce.container == nil {
		return codeexecutor.Workspace{},
			fmt.Errorf("container executor not ready")
	}
	safe := sanitize(execID)
	wsPath := path.Join(r.cfg.runContainerBase, "ws_"+safe)
	cmd := []string{"/bin/bash", "-lc",
		"mkdir -p '" + wsPath + "'"}
	_, _, _, _, err := r.execCmd(
		ctx, cmd, time.Duration(defaultCreateTimeoutSec)*time.Second,
	)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return codeexecutor.Workspace{}, err
	}
	return codeexecutor.Workspace{ID: execID, Path: wsPath}, nil
}

// Cleanup removes the workspace directory.
func (r *workspaceRuntime) Cleanup(
	ctx context.Context,
	ws codeexecutor.Workspace,
) error {
	if ws.Path == "" {
		return nil
	}
	cmd := []string{"/bin/bash", "-lc",
		"rm -rf '" + ws.Path + "'"}
	_, _, _, _, err := r.execCmd(
		ctx, cmd, time.Duration(defaultRmTimeoutSec)*time.Second,
	)
	return err
}

// PutFiles writes files via CopyToContainer.
func (r *workspaceRuntime) PutFiles(
	ctx context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	_, span := atrace.Tracer.Start(ctx,
		codeexecutor.SpanWorkspaceStageFiles)
	span.SetAttributes(attribute.Int(
		codeexecutor.AttrCount, len(files)))
	defer span.End()
	if len(files) == 0 {
		return nil
	}
	tr, err := tarFromFiles(files)
	if err != nil {
		return err
	}
	defer tr.Close()
	err = r.ce.client.CopyToContainer(
		ctx, r.ce.container.ID, ws.Path, tr,
		tcontainer.CopyToContainerOptions{},
	)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

// PutDirectory copies a host directory into the workspace.
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
	if hostPath == "" {
		return errors.New("hostPath is empty")
	}
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return err
	}
	// Fast path: within skills mount; copy inside container.
	if r.cfg.skillsHostBase != "" {
		if strings.HasPrefix(abs,
			r.cfg.skillsHostBase+string(os.PathSeparator)) ||
			abs == r.cfg.skillsHostBase {
			rel, _ := filepath.Rel(r.cfg.skillsHostBase, abs)
			src := path.Join(r.cfg.skillsContainerBase,
				filepath.ToSlash(rel))
			dest := ws.Path
			if to != "" {
				dest = path.Join(ws.Path, to)
			}
			cmd := []string{"/bin/bash", "-lc",
				"mkdir -p '" + dest + "' && cp -a '" + src +
					"/.' '" + dest + "'"}
			_, _, _, _, err := r.execCmd(
				ctx, cmd,
				time.Duration(defaultStageTimeoutSec)*time.Second,
			)
			if err == nil {
				span.SetAttributes(attribute.Bool(
					codeexecutor.AttrMountUsed, true))
				return nil
			}
			// fall through to tar copy on error
		}
	}
	// Pack dir into tar stream.
	rd, err := archive.TarWithOptions(abs, &archive.TarOptions{})
	if err != nil {
		return err
	}
	defer rd.Close()
	dest := ws.Path
	if to != "" {
		dest = path.Join(dest, to)
	}
	// Ensure destination exists in container.
	mk := []string{"/bin/bash", "-lc",
		"mkdir -p '" + dest + "'"}
	if _, _, _, _, err = r.execCmd(
		ctx, mk, time.Duration(defaultStageTimeoutSec)*time.Second,
	); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	err = r.ce.client.CopyToContainer(
		ctx, r.ce.container.ID, dest, rd,
		tcontainer.CopyToContainerOptions{},
	)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.SetAttributes(attribute.Bool(
		codeexecutor.AttrMountUsed, false))
	return err
}

// StageDirectory stages a directory with options.
func (r *workspaceRuntime) StageDirectory(
	ctx context.Context,
	ws codeexecutor.Workspace,
	src string,
	to string,
	opt codeexecutor.StageOptions,
) error {
	_, span := atrace.Tracer.Start(ctx,
		codeexecutor.SpanWorkspaceStageDir)
	span.SetAttributes(
		attribute.String(codeexecutor.AttrHostPath, src),
		attribute.String(codeexecutor.AttrTo, to),
	)
	defer span.End()
	abs, err := filepath.Abs(src)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if opt.AllowMount && r.cfg.skillsHostBase != "" {
		if strings.HasPrefix(abs,
			r.cfg.skillsHostBase+string(os.PathSeparator)) ||
			abs == r.cfg.skillsHostBase {
			rel, _ := filepath.Rel(r.cfg.skillsHostBase, abs)
			csrc := path.Join(r.cfg.skillsContainerBase,
				filepath.ToSlash(rel))
			dest := ws.Path
			if to != "" {
				dest = path.Join(ws.Path, to)
			}
			cmd := []string{"/bin/bash", "-lc",
				"mkdir -p '" + dest + "' && cp -a '" + csrc +
					"/.' '" + dest + "'"}
			if opt.ReadOnly {
				cmd[2] += " && chmod -R a-w '" + dest + "'"
			}
			_, _, _, _, err := r.execCmd(ctx, cmd,
				time.Duration(defaultStageTimeoutSec)*time.Second)
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				return err
			}
			span.SetAttributes(attribute.Bool(
				codeexecutor.AttrMountUsed, true))
			return nil
		}
	}
	if err := r.PutDirectory(ctx, ws, abs, to); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if opt.ReadOnly {
		dest := ws.Path
		if to != "" {
			dest = path.Join(ws.Path, to)
		}
		cmd := []string{"/bin/bash", "-lc",
			"chmod -R a-w '" + dest + "'"}
		if _, _, _, _, err := r.execCmd(
			ctx, cmd, time.Duration(defaultStageTimeoutSec)*time.Second,
		); err != nil {
			span.SetStatus(codes.Error, err.Error())
			return err
		}
	}
	span.SetAttributes(attribute.Bool(
		codeexecutor.AttrMountUsed, false))
	return nil
}

// RunProgram runs a command in the workspace with timeout.
func (r *workspaceRuntime) RunProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	_, span := atrace.Tracer.Start(ctx,
		codeexecutor.SpanWorkspaceRun)
	span.SetAttributes(
		attribute.String(codeexecutor.AttrCmd, spec.Cmd),
		attribute.String(codeexecutor.AttrCwd, spec.Cwd),
	)
	defer span.End()
	t := spec.Timeout
	if t <= 0 {
		t = 10 * time.Second
	}
	cwd := ws.Path
	if spec.Cwd != "" {
		cwd = path.Join(ws.Path, filepath.ToSlash(spec.Cwd))
	}
	var envParts []string
	hasWorkspace := false
	for k, v := range spec.Env {
		if k == codeexecutor.WorkspaceEnvDirKey {
			hasWorkspace = true
		}
		envParts = append(envParts, k+"="+shellQuote(v))
	}
	if !hasWorkspace {
		envParts = append(envParts,
			codeexecutor.WorkspaceEnvDirKey+"="+shellQuote(ws.Path))
	}
	envStr := strings.Join(envParts, " ")
	var cmdline strings.Builder
	cmdline.WriteString("cd ")
	cmdline.WriteString(shellQuote(cwd))
	cmdline.WriteString(" && ")
	if envStr != "" {
		cmdline.WriteString("env ")
		cmdline.WriteString(envStr)
		cmdline.WriteString(" ")
	}
	cmdline.WriteString(shellQuote(spec.Cmd))
	for _, a := range spec.Args {
		cmdline.WriteString(" ")
		cmdline.WriteString(shellQuote(a))
	}
	argv := []string{"/bin/bash", "-lc", cmdline.String()}
	out, errOut, code, timed, err := r.execCmd(ctx, argv, t)
	res := codeexecutor.RunResult{
		Stdout:   out,
		Stderr:   errOut,
		ExitCode: code,
		Duration: t,
		TimedOut: timed,
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

// Collect copies out files by glob patterns (simple exact path here).
func (r *workspaceRuntime) Collect(
	ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	_, span := atrace.Tracer.Start(ctx,
		codeexecutor.SpanWorkspaceCollect)
	defer span.End()
	// List file matches inside container, then copy each.
	var cmd strings.Builder
	cmd.WriteString("cd ")
	cmd.WriteString(shellQuote(ws.Path))
	cmd.WriteString(" && for p in")
	for _, p := range patterns {
		cmd.WriteString(" ")
		cmd.WriteString(shellQuote(filepath.ToSlash(p)))
	}
	// Echo absolute paths so CopyFromContainer sees container-absolute
	// locations, not paths relative to the workspace directory.
	cmd.WriteString("; do if [ -f \"$p\" ]; then ")
	cmd.WriteString("echo \"$(pwd)/$p\"; fi; done")

	argv := []string{"/bin/bash", "-lc", cmd.String()}
	outS, _, _, _, err := r.execCmd(ctx, argv, time.Second*5)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	var out []codeexecutor.File
	for _, line := range strings.Split(outS, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		data, _, mime, err := r.copyFileOut(ctx, line)
		if err != nil {
			return nil, err
		}
		rel := strings.TrimPrefix(line, ws.Path+"/")
		if rel == line {
			rel = filepath.ToSlash(line)
		}
		out = append(out, codeexecutor.File{
			Name:     rel,
			Content:  string(data),
			MIMEType: mime,
		})
	}
	return out, nil
}

// ExecuteInline writes code blocks and runs them.
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
			allErr.WriteString(err.Error() + "\n")
			continue
		}
		pf := codeexecutor.PutFile{
			Path:    path.Join(codeexecutor.InlineSourceDir, fn),
			Content: []byte(b.Code),
			Mode:    mode,
		}
		if err := r.PutFiles(ctx, ws, []codeexecutor.PutFile{pf}); err != nil {
			allErr.WriteString(err.Error() + "\n")
			continue
		}
		argv := append([]string{}, args...)
		argv = append(argv, path.Join(".", fn))
		spec := codeexecutor.RunProgramSpec{
			Cmd:     cmd,
			Args:    argv,
			Cwd:     codeexecutor.InlineSourceDir,
			Timeout: timeout,
		}
		res, err := r.RunProgram(ctx, ws, spec)
		if err != nil {
			allErr.WriteString(err.Error() + "\n")
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

// Internal helpers

func (r *workspaceRuntime) execCmd(
	ctx context.Context,
	argv []string,
	timeout time.Duration,
) (string, string, int, bool, error) {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ec := tcontainer.ExecOptions{
		Cmd:          argv,
		AttachStdout: true,
		AttachStderr: true,
	}
	ex, err := r.ce.client.ContainerExecCreate(
		tctx, r.ce.container.ID, ec,
	)
	if err != nil {
		return "", "", 0, false, err
	}
	hj, err := r.ce.client.ContainerExecAttach(
		tctx, ex.ID, tcontainer.ExecStartOptions{},
	)
	if err != nil {
		return "", "", 0, false, err
	}
	defer hj.Close()
	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, hj.Reader)
	if err != nil {
		return "", "", 0, false, err
	}
	insp, err := r.ce.client.ContainerExecInspect(tctx, ex.ID)
	if err != nil {
		timed := errors.Is(tctx.Err(), context.DeadlineExceeded)
		return stdout.String(), stderr.String(), 0, timed, err
	}
	timed := errors.Is(tctx.Err(), context.DeadlineExceeded)
	return stdout.String(), stderr.String(), insp.ExitCode, timed, nil
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
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

func tarFromFiles(files []codeexecutor.PutFile) (io.ReadCloser, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		name := path.Clean(f.Path)
		if name == "." || name == "/" || name == "" {
			return nil, fmt.Errorf("invalid file path: %s", f.Path)
		}
		hdr := &tar.Header{
			Name:    name,
			Mode:    int64(f.Mode),
			Size:    int64(len(f.Content)),
			ModTime: time.Now(),
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
	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

func (r *workspaceRuntime) copyFileOut(
	ctx context.Context,
	fullPath string,
) ([]byte, string, string, error) {
	rc, _, err := r.ce.client.CopyFromContainer(
		ctx, r.ce.container.ID, fullPath,
	)
	if err != nil {
		return nil, "", "", err
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return nil, "", "", err
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		var buf bytes.Buffer
		_, err = io.CopyN(&buf, tr, maxReadSizeBytes)
		if err != nil && !errors.Is(err, io.EOF) &&
			!errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, "", "", err
		}
		data := buf.Bytes()
		mime := http.DetectContentType(data)
		return data, hdr.Name, mime, nil
	}
}
