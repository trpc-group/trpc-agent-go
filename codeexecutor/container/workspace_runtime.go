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

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	archive "github.com/moby/go-archive"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	maxReadSizeBytes        = 4 * 1024 * 1024 // 4 MiB
	defaultCreateTimeoutSec = 5
	defaultRmTimeoutSec     = 10
	defaultStageTimeoutSec  = 10
	defaultSkillsContainer  = "/mnt/skills"
	defaultRunContainerBase = "/mnt/run"
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

// newWorkspaceRuntime creates a new container workspace runtime.
// Default behavior mounts a writable host temp directory at
// /mnt/run and, when SKILLS_ROOT is set, mounts it read-only at
// /mnt/skills.
func newWorkspaceRuntime() (*workspaceRuntime, error) {
	cfg := runtimeConfig{
		runContainerBase:    defaultRunContainerBase,
		skillsContainerBase: defaultSkillsContainer,
	}
	if root := os.Getenv(skill.EnvSkillsRoot); root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			if st, err2 := os.Stat(abs); err2 == nil && st.IsDir() {
				cfg.skillsHostBase = abs
			}
		}
	}
	// Create host workspace base under temp.
	hostRun, err := os.MkdirTemp("", "trpc-agent-run-")
	if err != nil {
		return nil, fmt.Errorf("create host run base: %w", err)
	}
	cfg.runHostBase = hostRun

	// Build hostConfig with binds.
	binds := []string{fmt.Sprintf("%s:%s:rw",
		cfg.runHostBase, cfg.runContainerBase)}
	if cfg.skillsHostBase != "" {
		binds = append(binds, fmt.Sprintf("%s:%s:ro",
			cfg.skillsHostBase, cfg.skillsContainerBase))
	}
	hc := container.HostConfig{
		AutoRemove:  true,
		Privileged:  false,
		NetworkMode: "none",
		Binds:       binds,
	}

	ce, err := New(WithHostConfig(hc))
	if err != nil {
		return nil, err
	}
	return &workspaceRuntime{ce: ce, cfg: cfg}, nil
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
	// Ensure host workspace dir exists under mounted base.
	if r.cfg.runHostBase != "" {
		_ = os.MkdirAll(filepath.Join(r.cfg.runHostBase,
			"ws_"+safe), 0o755)
	}
	wsPath := path.Join(r.cfg.runContainerBase, "ws_"+safe)

	// mkdir -p workspace path
	cmd := []string{"/bin/bash", "-lc", "mkdir -p '" + wsPath + "'"}
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
	cmd := []string{"/bin/bash", "-lc", "rm -rf '" + ws.Path + "'"}
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
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceStageFiles)
	span.SetAttributes(attribute.Int(codeexecutor.AttrCount, len(files)))
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
		container.CopyToContainerOptions{},
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
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceStageDir)
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
	// Fast path: hostPath within skills mount; copy inside container.
	if r.cfg.skillsHostBase != "" {
		if strings.HasPrefix(
			abs, r.cfg.skillsHostBase+string(os.PathSeparator),
		) || abs == r.cfg.skillsHostBase {
			rel, _ := filepath.Rel(r.cfg.skillsHostBase, abs)
			src := path.Join(r.cfg.skillsContainerBase,
				filepath.ToSlash(rel))
			dest := ws.Path
			if to != "" {
				dest = path.Join(ws.Path, to)
			}
			cmd := []string{
				"/bin/bash", "-lc",
				"mkdir -p '" + dest +
					"' && cp -a '" + src + "/.' '" + dest + "'",
			}
			_, _, _, _, err := r.execCmd(ctx, cmd,
				time.Duration(defaultStageTimeoutSec)*time.Second)
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				return err
			}
			span.SetAttributes(attribute.Bool(codeexecutor.AttrMountUsed, true))
			return nil
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
	mk := []string{"/bin/bash", "-lc", "mkdir -p '" + dest + "'"}
	if _, _, _, _, err = r.execCmd(
		ctx, mk, time.Duration(defaultStageTimeoutSec)*time.Second,
	); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	err = r.ce.client.CopyToContainer(
		ctx, r.ce.container.ID, dest, rd,
		container.CopyToContainerOptions{},
	)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.SetAttributes(attribute.Bool(codeexecutor.AttrMountUsed, false))
	return err
}

// PutSkill is PutDirectory alias.
func (r *workspaceRuntime) PutSkill(
	ctx context.Context,
	ws codeexecutor.Workspace,
	skillRoot string,
	to string,
) error {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceStageSkill)
	span.SetAttributes(
		attribute.String(codeexecutor.AttrRoot, skillRoot),
		attribute.String(codeexecutor.AttrTo, to),
	)
	defer span.End()
	// Prefer mount-first when skills root is bind-mounted.
	if r.cfg.skillsHostBase != "" {
		absHost, _ := filepath.Abs(skillRoot)
		if strings.HasPrefix(
			absHost, r.cfg.skillsHostBase+string(os.PathSeparator),
		) || absHost == r.cfg.skillsHostBase {
			rel, _ := filepath.Rel(r.cfg.skillsHostBase, absHost)
			src := path.Join(r.cfg.skillsContainerBase,
				filepath.ToSlash(rel))
			dest := ws.Path
			if to != "" {
				dest = path.Join(ws.Path, to)
			}
			cmd := []string{
				"/bin/bash", "-lc",
				"mkdir -p '" + dest +
					"' && cp -a '" + src + "/.' '" + dest +
					"' && chmod -R a-w '" + dest + "'",
			}
			_, _, _, _, err := r.execCmd(ctx, cmd,
				time.Duration(defaultStageTimeoutSec)*time.Second)
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				return err
			}
			span.SetAttributes(attribute.Bool(codeexecutor.AttrMountUsed, true))
			return nil
		}
	}
	err := r.PutDirectory(ctx, ws, skillRoot, to)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.SetAttributes(attribute.Bool(codeexecutor.AttrMountUsed, false))
	return err
}

// RunProgram runs a command in the workspace with timeout.
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
	t := spec.Timeout
	if t <= 0 {
		t = 10 * time.Second
	}

	// Build shell command: cd to workspace/cwd and run program.
	cwd := ws.Path
	if spec.Cwd != "" {
		// Use POSIX path join.
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

// Collect finds files via shell glob expansion and copies content.
func (r *workspaceRuntime) Collect(
	ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceCollect)
	span.SetAttributes(attribute.Int(codeexecutor.AttrPatterns, len(patterns)))
	defer span.End()
	if len(patterns) == 0 {
		return nil, nil
	}
	// Expand patterns inside container using bash.
	var sb strings.Builder
	sb.WriteString("shopt -s globstar dotglob nullglob; ")
	sb.WriteString("for p in ")
	for _, p := range patterns {
		sb.WriteString(shellQuote(path.Join(ws.Path, p)))
		sb.WriteString(" ")
	}
	sb.WriteString("; do if [ -f \"$p\" ]; then echo \"$p\"; fi; done")

	argv := []string{"/bin/bash", "-lc", sb.String()}
	out, _, _, _, err := r.execCmd(ctx, argv, 10*time.Second)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var files []codeexecutor.File
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel := strings.TrimPrefix(line, ws.Path+"/")
		content, mime, err := r.copyFileOut(ctx, line)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		files = append(files, codeexecutor.File{
			Name:     rel,
			Content:  string(content),
			MIMEType: mime,
		})
	}
	span.SetAttributes(attribute.Int(codeexecutor.AttrCount, len(files)))
	return files, nil
}

// ExecuteInline writes blocks then runs them via RunProgram.
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

	var outB, errB strings.Builder
	start := time.Now()
	for i, b := range blocks {
		fn, mode, cmd, args, err := codeexecutor.BuildBlockSpec(i, b)
		if err != nil {
			errB.WriteString(err.Error())
			errB.WriteString("\n")
			continue
		}
		pf := codeexecutor.PutFile{
			Path:    fn,
			Content: []byte(b.Code),
			Mode:    mode,
		}
		if err := r.PutFiles(ctx, ws, []codeexecutor.PutFile{pf}); err != nil {
			errB.WriteString(err.Error())
			errB.WriteString("\n")
			continue
		}
		argv := make([]string, 0, len(args)+1)
		argv = append(argv, args...)
		argv = append(argv, "./"+fn)
		rr, err := r.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     cmd,
			Args:    argv,
			Timeout: timeout,
		})
		if err != nil {
			errB.WriteString(err.Error())
			errB.WriteString("\n")
		}
		if rr.Stdout != "" {
			outB.WriteString(rr.Stdout)
		}
		if rr.Stderr != "" {
			errB.WriteString(rr.Stderr)
		}
	}
	return codeexecutor.RunResult{
		Stdout:   outB.String(),
		Stderr:   errB.String(),
		ExitCode: 0,
		Duration: time.Since(start),
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

	ec := container.ExecOptions{
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
		tctx, ex.ID, container.ExecStartOptions{},
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
		// If context timed out during inspect, surface TimedOut=true.
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
	// Replace ' with '\'' pattern.
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
) ([]byte, string, error) {
	rc, _, err := r.ce.client.CopyFromContainer(
		ctx, r.ce.container.ID, fullPath,
	)
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()

	tr := tar.NewReader(rc)
	// The first header is the file or a containing directory.
	for {
		hdr, err := tr.Next()
		if err != nil {
			return nil, "", err
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		// Limit read size for safety.
		var buf bytes.Buffer
		if _, err := io.CopyN(&buf, tr, maxReadSizeBytes); err != nil &&
			!errors.Is(err, io.EOF) {
			return nil, "", err
		}
		data := buf.Bytes()
		mime := http.DetectContentType(data)
		return data, mime, nil
	}
}
