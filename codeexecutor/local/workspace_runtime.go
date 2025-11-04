//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local

// Workspace runtime provides workspace-based execution on local host.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	ds "github.com/bmatcuk/doublestar/v4"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	defaultTimeoutSec = 10
	defaultFileMode   = 0o644
	maxReadSizeBytes  = 4 * 1024 * 1024 // 4 MiB per output file
)

// Runtime implements the workspace-based executor using local processes.
type Runtime struct {
	WorkRoot            string
	ReadOnlyStagedSkill bool
}

// NewRuntime creates a new local Runtime. When workRoot is empty, a
// temporary directory will be used per workspace.
func NewRuntime(workRoot string) *Runtime {
	return &Runtime{WorkRoot: workRoot}
}

// RuntimeOption customizes the local Runtime behavior.
type RuntimeOption func(*Runtime)

// WithReadOnlyStagedSkill toggles making staged skill trees read-only.
func WithReadOnlyStagedSkill(readOnly bool) RuntimeOption {
	return func(r *Runtime) { r.ReadOnlyStagedSkill = readOnly }
}

// NewRuntimeWithOptions creates a Runtime with optional settings.
func NewRuntimeWithOptions(
	workRoot string, opts ...RuntimeOption,
) *Runtime {
	r := &Runtime{WorkRoot: workRoot}
	for _, o := range opts {
		o(r)
	}
	return r
}

// CreateWorkspace creates an execution workspace directory.
func (r *Runtime) CreateWorkspace(
	ctx context.Context,
	execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceCreate)
	span.SetAttributes(attribute.String(codeexecutor.AttrExecID, execID))
	defer span.End()
	var base string
	if r.WorkRoot != "" {
		base = r.WorkRoot
	} else {
		base = os.TempDir()
	}

	// Sanitize execID to be filesystem friendly.
	safe := strings.Map(func(r rune) rune {
		switch r {
		case 'a', 'b', 'c', 'd', 'e', 'f', 'g',
			'h', 'i', 'j', 'k', 'l', 'm', 'n',
			'o', 'p', 'q', 'r', 's', 't', 'u',
			'v', 'w', 'x', 'y', 'z',
			'A', 'B', 'C', 'D', 'E', 'F', 'G',
			'H', 'I', 'J', 'K', 'L', 'M', 'N',
			'O', 'P', 'Q', 'R', 'S', 'T', 'U',
			'V', 'W', 'X', 'Y', 'Z',
			'0', '1', '2', '3', '4', '5', '6',
			'7', '8', '9', '-', '_':
			return r
		default:
			return '_' // replace others
		}
	}, execID)

	wsPath := filepath.Join(base, "ws_"+safe)
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return codeexecutor.Workspace{}, err
	}

	// Persist is respected by callers deciding whether to call Cleanup.
	_ = pol

	return codeexecutor.Workspace{ID: execID, Path: wsPath}, nil
}

// Cleanup removes workspace directory if it exists.
func (r *Runtime) Cleanup(
	ctx context.Context,
	ws codeexecutor.Workspace,
) error {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceCleanup)
	span.SetAttributes(attribute.String(codeexecutor.AttrPath, ws.Path))
	defer span.End()
	if ws.Path == "" {
		return nil
	}
	err := os.RemoveAll(ws.Path)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

// PutFiles writes file blobs under the workspace root.
func (r *Runtime) PutFiles(
	ctx context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceStageFiles)
	span.SetAttributes(attribute.Int(codeexecutor.AttrCount, len(files)))
	defer span.End()
	for _, f := range files {
		if err := r.writeFileSafe(ws.Path, f); err != nil {
			span.SetStatus(codes.Error, err.Error())
			return err
		}
	}
	return nil
}

// PutDirectory copies an entire directory from host into workspace.
func (r *Runtime) PutDirectory(
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
	src, err := filepath.Abs(hostPath)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	dst := filepath.Join(ws.Path, filepath.Clean(to))
	err = copyDir(src, dst)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

// PutSkill copies a skill directory into workspace. Same as PutDirectory.
func (r *Runtime) PutSkill(
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
	err := r.PutDirectory(ctx, ws, skillRoot, to)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	if err == nil && r.ReadOnlyStagedSkill {
		dest := ws.Path
		if to != "" {
			dest = filepath.Join(ws.Path, filepath.Clean(to))
		}
		if e2 := makeTreeReadOnly(dest); e2 != nil {
			span.SetStatus(codes.Error, e2.Error())
			return e2
		}
	}
	return err
}

// RunProgram runs a command inside the workspace.
func (r *Runtime) RunProgram(
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
	// Resolve cwd under workspace.
	cwd := filepath.Join(ws.Path, filepath.Clean(spec.Cwd))

	// Ensure cwd exists.
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return codeexecutor.RunResult{}, err
	}

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout()
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(tctx, spec.Cmd, spec.Args...) //nolint:gosec
	cmd.Dir = cwd

	// Build environment. Start with current env, then overlay.
	env := os.Environ()
	for k, v := range spec.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)

	exitCode := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else if errors.Is(runErr, context.DeadlineExceeded) {
			// Keep exitCode as 0 when killed by timeout.
		} else {
			// Map other errors to exitCode -1 for visibility.
			exitCode = -1
		}
	}

	res := codeexecutor.RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: dur,
		TimedOut: errors.Is(tctx.Err(), context.DeadlineExceeded),
	}
	span.SetAttributes(
		attribute.Int(codeexecutor.AttrExitCode, res.ExitCode),
		attribute.Bool(codeexecutor.AttrTimedOut, res.TimedOut),
	)
	if runErr != nil {
		span.SetStatus(codes.Error, runErr.Error())
	}

	return res, nil
}

// Collect finds output files by glob patterns relative to workspace root.
func (r *Runtime) Collect(
	ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	_, span := atrace.Tracer.Start(ctx, codeexecutor.SpanWorkspaceCollect)
	span.SetAttributes(attribute.Int(codeexecutor.AttrPatterns, len(patterns)))
	defer span.End()
	var out []codeexecutor.File
	root := ws.Path

	for _, p := range patterns {
		// Use doublestar to support ** patterns.
		abs := filepath.Join(root, p)
		// Doublestar on os.DirFS("/") expects patterns relative to "/".
		pattern := strings.TrimPrefix(abs, "/")
		matches, err := ds.Glob(os.DirFS("/"), pattern)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			// Convert match back to absolute path.
			mAbs := "/" + strings.TrimPrefix(m, "/")
			// Ensure it is within root.
			if !strings.HasPrefix(
				mAbs, root+string(os.PathSeparator),
			) && mAbs != root {
				continue
			}
			// Trim root prefix for Name.
			name := strings.TrimPrefix(
				mAbs, root+string(os.PathSeparator),
			)
			content, mime, err := readLimited(mAbs)
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				return nil, err
			}
			out = append(out, codeexecutor.File{
				Name:     name,
				Content:  string(content),
				MIMEType: mime,
			})
		}
	}
	span.SetAttributes(attribute.Int(codeexecutor.AttrCount, len(out)))
	return out, nil
}

// ExecuteInline writes temp files for code blocks and runs them.
func (r *Runtime) ExecuteInline(
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
			Path:    fn,
			Content: []byte(b.Code),
			Mode:    mode,
		}
		if err := r.PutFiles(ctx, ws, []codeexecutor.PutFile{pf}); err != nil {
			allErr.WriteString(err.Error())
			allErr.WriteString("\n")
			continue
		}
		argv := make([]string, 0, len(args)+1)
		argv = append(argv, args...)
		argv = append(argv, filepath.Join(".", fn))
		spec := codeexecutor.RunProgramSpec{
			Cmd:     cmd,
			Args:    argv,
			Timeout: timeout,
		}
		res, err := r.RunProgram(ctx, ws, spec)
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

// Helpers

func defaultTimeout() time.Duration {
	return defaultTimeoutSec * time.Second
}

func (r *Runtime) writeFileSafe(root string, f codeexecutor.PutFile) error {
	if f.Path == "" {
		return errors.New("empty file path")
	}
	// Resolve and ensure inside root.
	dst := filepath.Join(root, filepath.Clean(f.Path))
	if !strings.HasPrefix(dst, root+string(os.PathSeparator)) && dst != root {
		return fmt.Errorf("path escapes workspace: %s", f.Path)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	mode := fs.FileMode(f.Mode)
	if mode == 0 {
		mode = defaultFileMode
	}
	return os.WriteFile(dst, f.Content, mode)
}

func copyDir(src, dst string) error {
	// Ensure destination exists.
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(
		src,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode())
		})
}

func readLimited(path string) ([]byte, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	buf := make([]byte, maxReadSizeBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) &&
		!errors.Is(err, io.EOF) {
		return nil, "", err
	}
	data := buf[:n]
	mime := http.DetectContentType(data)
	return data, mime, nil
}

// makeTreeReadOnly removes write bits from the entire tree.
func makeTreeReadOnly(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry,
		err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		// Clear write bits: owner/group/other.
		newMode := mode &^ 0o222
		return os.Chmod(p, newMode)
	})
}
