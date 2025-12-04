//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	InputsHostBase      string
	AutoInputs          bool
}

// NewRuntime creates a new local Runtime. When workRoot is empty, a
// temporary directory will be used per workspace.
func NewRuntime(workRoot string) *Runtime {
	return &Runtime{
		WorkRoot:   workRoot,
		AutoInputs: true,
	}
}

// RuntimeOption customizes the local Runtime behavior.
type RuntimeOption func(*Runtime)

// WithReadOnlyStagedSkill toggles making staged skill trees read-only.
func WithReadOnlyStagedSkill(readOnly bool) RuntimeOption {
	return func(r *Runtime) { r.ReadOnlyStagedSkill = readOnly }
}

// WithInputsHostBase sets the host directory that will be exposed
// under work/inputs inside each workspace when auto inputs are
// enabled.
func WithInputsHostBase(host string) RuntimeOption {
	return func(r *Runtime) { r.InputsHostBase = host }
}

// WithAutoInputs enables or disables automatic mapping of the host
// inputs directory (when configured) into work/inputs for each
// workspace.
func WithAutoInputs(enable bool) RuntimeOption {
	return func(r *Runtime) { r.AutoInputs = enable }
}

// NewRuntimeWithOptions creates a Runtime with optional settings.
func NewRuntimeWithOptions(
	workRoot string, opts ...RuntimeOption,
) *Runtime {
	r := &Runtime{
		WorkRoot:   workRoot,
		AutoInputs: true,
	}
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

	// Make workspace path unique to avoid collisions between runs.
	suf := time.Now().UnixNano()
	wsPath := filepath.Join(base, fmt.Sprintf("ws_%s_%d", safe, suf))
	if err := os.MkdirAll(wsPath, 0o777); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return codeexecutor.Workspace{}, err
	}
	_ = os.Chmod(wsPath, 0o777)

	// Persist is respected by callers deciding whether to call Cleanup.
	_ = pol

	// Ensure standard layout and metadata.json.
	if _, err := codeexecutor.EnsureLayout(wsPath); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return codeexecutor.Workspace{}, err
	}
	ws := codeexecutor.Workspace{ID: execID, Path: wsPath}
	if r.AutoInputs && r.InputsHostBase != "" {
		specs := []codeexecutor.InputSpec{{
			From: "host://" + r.InputsHostBase,
			To: filepath.Join(
				codeexecutor.DirWork, "inputs",
			),
			Mode: "link",
		}}
		if err := r.StageInputs(ctx, ws, specs); err != nil {
			span.SetStatus(codes.Error, err.Error())
			return codeexecutor.Workspace{}, err
		}
	}
	return ws, nil
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

// StageDirectory stages a host directory into the workspace.
// Behavior depends on options, e.g., making the tree read-only.
func (r *Runtime) StageDirectory(
	ctx context.Context,
	ws codeexecutor.Workspace,
	src string,
	to string,
	opt codeexecutor.StageOptions,
) error {
	if err := r.PutDirectory(ctx, ws, src, to); err != nil {
		return err
	}
	ro := opt.ReadOnly || r.ReadOnlyStagedSkill
	if ro {
		dest := ws.Path
		if to != "" {
			dest = filepath.Join(ws.Path, filepath.Clean(to))
		}
		if err := makeTreeReadOnly(dest); err != nil {
			return err
		}
	}
	return nil
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

	// Build environment. Start with current env, then inject
	// workspace vars, then overlay user-provided.
	env := os.Environ()

	// Ensure layout exists and compute run dir.
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		return codeexecutor.RunResult{}, err
	}
	runDir := filepath.Join(
		ws.Path, codeexecutor.DirRuns,
		"run_"+time.Now().Format("20060102T150405.000"),
	)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return codeexecutor.RunResult{}, err
	}

	// Inject well-known variables if not set.
	baseEnv := map[string]string{
		codeexecutor.WorkspaceEnvDirKey: ws.Path,
		codeexecutor.EnvSkillsDir: filepath.Join(
			ws.Path, codeexecutor.DirSkills,
		),
		codeexecutor.EnvWorkDir: filepath.Join(
			ws.Path, codeexecutor.DirWork,
		),
		codeexecutor.EnvOutputDir: filepath.Join(
			ws.Path, codeexecutor.DirOut,
		),
		codeexecutor.EnvRunDir: runDir,
	}
	for k, v := range baseEnv {
		// If user already set, respect it.
		if _, ok := spec.Env[k]; ok {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
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
	patterns = codeexecutor.NormalizeGlobs(patterns)
	// Canonicalize root to make prefix checks robust on platforms
	// where different paths may refer to the same location.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil || realRoot == "" {
		realRoot = root
	}
	seen := map[string]bool{}

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
			// Collapse symlinks to canonical path and dedupe.
			realp, err := filepath.EvalSymlinks(mAbs)
			if err != nil {
				realp = mAbs
			}
			// Re-check containment against canonical root.
			if !strings.HasPrefix(
				realp, realRoot+string(os.PathSeparator),
			) && realp != realRoot {
				continue
			}
			name := strings.TrimPrefix(
				realp, realRoot+string(os.PathSeparator),
			)
			if seen[name] {
				continue
			}
			seen[name] = true
			content, mime, err := readLimited(realp)
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

// StageInputs maps external inputs into the workspace.
func (r *Runtime) StageInputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		return err
	}
	md, _ := codeexecutor.LoadMetadata(ws.Path)
	for _, sp := range specs {
		mode := strings.ToLower(strings.TrimSpace(sp.Mode))
		if mode == "" {
			mode = "copy"
		}
		to := sp.To
		if strings.TrimSpace(to) == "" {
			base := inputDefaultName(sp.From)
			to = filepath.Join(
				codeexecutor.DirWork, "inputs", base,
			)
		}
		var err error
		var resolved string
		var ver *int
		switch {
		case strings.HasPrefix(sp.From, "artifact://"):
			name := strings.TrimPrefix(sp.From, "artifact://")
			aname, aver, perr := codeexecutor.ParseArtifactRef(name)
			if perr != nil {
				return perr
			}
			data, _, actual, lerr := codeexecutor.LoadArtifactHelper(
				ctx, aname, aver,
			)
			if lerr != nil {
				return lerr
			}
			resolved = aname
			if aver != nil {
				v := *aver
				ver = &v
			} else {
				v := actual
				ver = &v
			}
			err = r.writeFileSafe(ws.Path, codeexecutor.PutFile{
				Path:    to,
				Content: data,
				Mode:    defaultFileMode,
			})
		case strings.HasPrefix(sp.From, "host://"):
			host := strings.TrimPrefix(sp.From, "host://")
			resolved = host
			if mode == "link" {
				err = makeSymlink(ws.Path, to, host)
			} else {
				err = r.PutDirectory(ctx, ws, host,
					filepath.Dir(to))
			}
		case strings.HasPrefix(sp.From, "workspace://"):
			rel := strings.TrimPrefix(sp.From, "workspace://")
			src := filepath.Join(ws.Path, filepath.Clean(rel))
			resolved = rel
			if mode == "link" {
				err = makeSymlink(ws.Path, to, src)
			} else {
				err = copyPath(src,
					filepath.Join(ws.Path, filepath.Clean(to)))
			}
		case strings.HasPrefix(sp.From, "skill://"):
			rest := strings.TrimPrefix(sp.From, "skill://")
			src := filepath.Join(
				ws.Path, codeexecutor.DirSkills, filepath.Clean(rest),
			)
			resolved = src
			if mode == "link" {
				err = makeSymlink(ws.Path, to, src)
			} else {
				err = copyPath(src,
					filepath.Join(ws.Path, filepath.Clean(to)))
			}
		default:
			return fmt.Errorf("unsupported input: %s", sp.From)
		}
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
	return codeexecutor.SaveMetadata(ws.Path, md)
}

// CollectOutputs implements the declarative collector with limits.
func (r *Runtime) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
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
	leftTotal := maxTotal
	globs := codeexecutor.NormalizeGlobs(spec.Globs)
	out := codeexecutor.OutputManifest{}
	var savedNames []string
	var savedVers []int
	count := 0
	for _, g := range globs {
		abs := filepath.Join(ws.Path, g)
		pattern := strings.TrimPrefix(abs, "/")
		matches, err := ds.Glob(os.DirFS("/"), pattern)
		if err != nil {
			return codeexecutor.OutputManifest{}, err
		}
		for _, m := range matches {
			if count >= maxFiles {
				out.LimitsHit = true
				break
			}
			mAbs := "/" + strings.TrimPrefix(m, "/")
			if !strings.HasPrefix(
				mAbs, ws.Path+string(os.PathSeparator),
			) && mAbs != ws.Path {
				continue
			}
			name := strings.TrimPrefix(
				mAbs, ws.Path+string(os.PathSeparator),
			)
			// Respect both per-file and total byte limits.
			limit := int(maxFileBytes)
			if int64(limit) > leftTotal {
				limit = int(leftTotal)
			}
			data, mime, err := readLimitedWithCap(mAbs, limit)
			if err != nil {
				return codeexecutor.OutputManifest{}, err
			}
			// Mark limits hit when a file reached per-file cap.
			if int64(len(data)) >= maxFileBytes {
				out.LimitsHit = true
			}
			leftTotal -= int64(len(data))
			count++
			ref := codeexecutor.FileRef{
				Name:     name,
				MIMEType: mime,
			}
			if spec.Inline {
				ref.Content = string(data)
			}
			if spec.Save {
				saveName := name
				if spec.NameTemplate != "" {
					// Minimal template: support prefix only.
					saveName = spec.NameTemplate + name
				}
				ver, err := codeexecutor.SaveArtifactHelper(
					ctx, saveName, data, mime,
				)
				if err != nil {
					return codeexecutor.OutputManifest{}, err
				}
				ref.SavedAs = saveName
				ref.Version = ver
				savedNames = append(savedNames, saveName)
				savedVers = append(savedVers, ver)
			}
			out.Files = append(out.Files, ref)
			if leftTotal <= 0 {
				out.LimitsHit = true
				break
			}
		}
	}
	md, _ := codeexecutor.LoadMetadata(ws.Path)
	md.Outputs = append(md.Outputs, codeexecutor.OutputRecord{
		Globs:     spec.Globs,
		SavedAs:   savedNames,
		Versions:  savedVers,
		LimitsHit: out.LimitsHit,
		Timestamp: time.Now(),
	})
	_ = codeexecutor.SaveMetadata(ws.Path, md)
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
			Path:    filepath.Join(codeexecutor.InlineSourceDir, fn),
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
			Cwd:     codeexecutor.InlineSourceDir,
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

func readLimitedWithCap(path string, capBytes int) ([]byte, string, error) {
	if capBytes <= 0 {
		return []byte{}, "", nil
	}
	if capBytes > maxReadSizeBytes {
		capBytes = maxReadSizeBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	buf := make([]byte, capBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) &&
		!errors.Is(err, io.EOF) {
		return nil, "", err
	}
	data := buf[:n]
	mime := http.DetectContentType(data)
	return data, mime, nil
}

func makeSymlink(root, toRel, target string) error {
	dst := filepath.Join(root, filepath.Clean(toRel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// Remove existing path if present.
	_ = os.RemoveAll(dst)
	return os.Symlink(target, dst)
}

func copyPath(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return copyDir(src, dst)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, st.Mode())
}

func inputDefaultName(from string) string {
	// Strip scheme and keep tail element as default name.
	i := strings.LastIndex(from, "/")
	if i >= 0 && i+1 < len(from) {
		return from[i+1:]
	}
	return from
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
