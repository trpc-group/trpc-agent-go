//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package workspaceio exposes Workspace, a thin Go-facing facade over
// codeexecutor.WorkspaceFS + ProgramRunner bound to the current
// invocation's workspace. It is the entry point business code uses
// from agent callbacks (and custom tools) to read, write, persist,
// stage, and run programs against workspace files without taking a
// dependency on a specific executor backend.
//
// Naming note: the package is workspaceio because the project's
// existing convention is workspace<action-class> (workspaceinput,
// workspaceprep, workspacesession, workspacefacade, workspaceexec);
// the type is Workspace because industry sandbox SDKs (E2B / Modal /
// Daytona / Vercel) all reserve the resource name for the
// behavior-bearing handle. codeexecutor.Workspace (descriptor) and
// workspaceio.Workspace (facade) coexist permanently — the descriptor
// type is a v1-published value object that cannot be renamed under
// Go module compatibility rules. The two types are disambiguated by
// import path; business code rarely references both in the same file.
package workspaceio

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspacefacade"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspacesession"
)

// File is an in-memory snapshot of a workspace file produced by
// Workspace.Collect. It mirrors the fields of codeexecutor.File but
// uses []byte for Data so callers can mutate it freely. Truncated
// reports whether the backend hit its internal read cap; the framework
// does not act on it — callers should check the flag themselves and
// decide whether to retry, fail, or accept the partial bytes.
type File struct {
	Path      string
	Data      []byte
	MIMEType  string
	SizeBytes int64
	Truncated bool
}

// ArtifactRef describes a workspace file that has been persisted via
// Workspace.SaveArtifact. Field names mirror the workspace_save_artifact
// LLM tool output schema so callers can move between the two without a
// translation layer.
type ArtifactRef struct {
	// SavedAs is the artifact key returned by the artifact service.
	SavedAs   string
	Version   int
	Ref       string
	MIMEType  string
	SizeBytes int64
	// Path is the workspace-relative source path that was persisted.
	Path string
}

// Workspace is a thin facade over codeexecutor.WorkspaceFS plus
// ProgramRunner, bound to the current invocation's workspace. Use
// WorkspaceFromContext from inside agent callbacks (BeforeAgent /
// AfterAgent / BeforeTool / AfterTool / BeforeModel / AfterModel /
// your own tool's Run) to obtain one; tests can also construct one
// directly via New.
//
// The methods are deliberately thin pass-throughs to the underlying
// codeexecutor backend — the framework does not impose extra budgets,
// truncation policies, or batch loops on top. Callers that want those
// policies write them in the callback.
//
// Type shape:
//
// Workspace is intentionally a concrete type rather than an interface.
// Backend variation lives one layer below at codeexecutor.WorkspaceFS
// and codeexecutor.ProgramRunner; Workspace is a single user-facing
// facade on top of any engine. Promoting it to an interface should
// wait until a second user-facing implementation actually exists (for
// example, a remote workspace client speaking RPC instead of going
// through Engine).
//
// The zero value is not usable; obtain a *Workspace via
// WorkspaceFromContext (or New from tests / framework wiring).
//
// Concurrency:
//
// Workspace has no internal locking. The struct simply forwards each
// call to the underlying codeexecutor backend, so concurrent callers
// inherit whatever guarantees that backend offers. The framework does
// not standardize those guarantees; in practice callbacks should
// serialize their workspace operations when ordering matters (a
// PutFiles followed by a Collect against the same path is the
// canonical example).
//
// Scope and backend caveats:
//
//   - Workspace always operates on the current invocation's workspace.
//     Path arguments are interpreted relative to the workspace root
//     (or to the workspace ${WORK} / ${OUT} / ${RUNS} env paths).
//     Workspace never reaches outside the workspace, never touches
//     the skill / profile store, and never references arbitrary
//     remote filesystems.
//   - Whether two nodes can read the same workspace depends entirely
//     on the executor backend. local/container executors are
//     single-node by construction; pcg123 + CFS can persist if the
//     backend is configured to share the same workspace id, and
//     cube-style sandboxes depend on whether the runtime exposes a
//     stable workspace handle. Mirror state into your own shared store
//     from a callback when you need cross-node visibility.
type Workspace struct {
	exec     codeexecutor.CodeExecutor
	resolver *workspacesession.Resolver
}

// New creates a Workspace backed by exec. When reg is nil the
// Workspace allocates a private workspace registry; pass the agent's
// shared registry to reuse the same invocation workspace as
// workspace_exec.
//
// Returns nil when exec is nil so callers can detect "no executor
// configured" without a panic.
func New(
	exec codeexecutor.CodeExecutor,
	reg *codeexecutor.WorkspaceRegistry,
) *Workspace {
	if exec == nil {
		return nil
	}
	return &Workspace{
		exec:     exec,
		resolver: workspacesession.NewResolver(exec, reg),
	}
}

// SaveArtifactOptions controls Workspace.SaveArtifact behavior.
type SaveArtifactOptions struct {
	MaxBytes int64
}

// SaveArtifactOption mutates SaveArtifactOptions.
type SaveArtifactOption func(*SaveArtifactOptions)

// WithSaveArtifactMaxBytes caps the size of files persisted via
// SaveArtifact. The cap is forwarded to the backend's
// CollectOutputs(MaxFileBytes/MaxTotalBytes) so it gates the read
// itself, not just a post-check. Files larger than this cap are
// rejected by the backend.
func WithSaveArtifactMaxBytes(n int64) SaveArtifactOption {
	return func(o *SaveArtifactOptions) { o.MaxBytes = n }
}

// Collect reads every workspace file that matches one of the supplied
// patterns and returns them as Files. Pattern syntax is identical to
// codeexecutor.WorkspaceFS.Collect (e.g. "skills/echoer/SKILL.md",
// "skills/*/SKILL.md", "out/**/*.json"); a single literal path is a
// valid pattern. An empty pattern list returns an empty slice.
//
// Collect does not enforce any per-file or aggregate budget — callers
// that need one apply it on the returned slice. Each file's Truncated
// flag is preserved from the backend.
func (w *Workspace) Collect(
	ctx context.Context,
	patterns ...string,
) ([]*File, error) {
	if w == nil {
		return nil, errors.New("workspaceio: workspace is nil")
	}
	if len(patterns) == 0 {
		return []*File{}, nil
	}
	eng, ws, err := w.bindWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := eng.FS().Collect(ctx, ws, patterns)
	if err != nil {
		return nil, err
	}
	out := make([]*File, len(raw))
	for i := range raw {
		out[i] = toFile(raw[i])
	}
	return out, nil
}

// PutFiles writes a batch of workspace files in one engine call. Each
// PutFile carries its own path / content / mode; parent directories
// are created automatically by the engine. An empty list is a no-op.
//
// Single-file writes use the same entry point — pass one PutFile.
// When PutFile.Mode is 0 the local backend falls back to its default
// mode (0o644); use codeexecutor.DefaultScriptFileMode (0o644) or
// codeexecutor.DefaultExecFileMode (0o755) when you want to be
// explicit.
func (w *Workspace) PutFiles(
	ctx context.Context,
	files ...codeexecutor.PutFile,
) error {
	if w == nil {
		return errors.New("workspaceio: workspace is nil")
	}
	if len(files) == 0 {
		return nil
	}
	eng, ws, err := w.bindWorkspace(ctx)
	if err != nil {
		return err
	}
	return eng.FS().PutFiles(ctx, ws, files)
}

// SaveArtifact persists an existing workspace file as an artifact via
// the invocation's artifact service. The path must reference a real
// file inside one of the publish-allowed workspace roots
// (work/, out/, runs/).
func (w *Workspace) SaveArtifact(
	ctx context.Context,
	relPath string,
	opts ...SaveArtifactOption,
) (*ArtifactRef, error) {
	if w == nil {
		return nil, errors.New("workspaceio: workspace is nil")
	}
	rel, err := workspacefacade.NormalizeArtifactPath(relPath)
	if err != nil {
		return nil, err
	}
	if reason := workspacefacade.ArtifactSaveSkipReason(ctx); reason != "" {
		return nil, fmt.Errorf(
			"workspaceio: artifact persistence requires artifact service and session: %s",
			reason,
		)
	}
	cfg := SaveArtifactOptions{MaxBytes: workspacefacade.DefaultArtifactMaxBytes}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = workspacefacade.DefaultArtifactMaxBytes
	}
	ctxIO := workspacefacade.WithArtifactContext(ctx)
	eng, ws, err := w.bindWorkspace(ctxIO)
	if err != nil {
		return nil, err
	}
	manifest, err := eng.FS().CollectOutputs(ctxIO, ws, codeexecutor.OutputSpec{
		Globs:         []string{rel},
		MaxFiles:      1,
		MaxFileBytes:  cfg.MaxBytes,
		MaxTotalBytes: cfg.MaxBytes,
		Save:          true,
		Inline:        false,
	})
	// ErrPartialOutputCommit means the artifact already landed in the
	// service but some non-fatal post-commit work failed (e.g. cleanup
	// or secondary inline read). Mirror tool/workspaceexec.SaveArtifact
	// and continue so the caller still gets the persisted ref. Any
	// other error is fatal.
	if err != nil && !errors.Is(err, codeexecutor.ErrPartialOutputCommit) {
		return nil, err
	}
	if len(manifest.Files) == 0 {
		return nil, fmt.Errorf(
			"workspaceio: artifact file not found: %s", rel,
		)
	}
	if len(manifest.Files) > 1 {
		return nil, fmt.Errorf(
			"workspaceio: artifact path matched %d files: %s",
			len(manifest.Files), rel,
		)
	}
	ref := manifest.Files[0]
	if ref.SavedAs == "" {
		return nil, fmt.Errorf(
			"workspaceio: artifact was not persisted: %s", rel,
		)
	}
	return &ArtifactRef{
		SavedAs:   ref.SavedAs,
		Version:   ref.Version,
		Ref:       fileref.ArtifactPrefix + ref.SavedAs + "@" + strconv.Itoa(ref.Version),
		MIMEType:  ref.MIMEType,
		SizeBytes: ref.SizeBytes,
		Path:      rel,
	}, nil
}

// StageInputs maps external inputs (artifact://, host://, workspace://,
// skill://) into the workspace using the engine's StageInputs primitive.
//
// The artifact service and session metadata are forwarded through ctx
// (matching SaveArtifact) so artifact:// inputs can resolve against the
// invocation's artifact service. When the invocation does not carry
// that info ctx is forwarded unchanged.
func (w *Workspace) StageInputs(
	ctx context.Context,
	specs []codeexecutor.InputSpec,
) error {
	if w == nil {
		return errors.New("workspaceio: workspace is nil")
	}
	if len(specs) == 0 {
		return nil
	}
	ctxIO := workspacefacade.WithArtifactContext(ctx)
	eng, ws, err := w.bindWorkspace(ctxIO)
	if err != nil {
		return err
	}
	return eng.FS().StageInputs(ctxIO, ws, specs)
}

// RunProgram executes a program inside the current invocation's
// workspace via the engine's ProgramRunner. spec.Cwd is interpreted
// relative to the workspace root and cannot escape it.
//
// The returned error is non-nil only for framework-level failures
// (no executor configured, backend rejection, launch error, internal
// timeout, or an out-of-workspace Cwd). A non-zero exit code is
// signaled via RunResult.ExitCode and is NOT an error — callers
// inspect ExitCode / TimedOut on the returned RunResult and decide
// whether to fail, retry, or accept the outcome themselves. This
// matches the convention used by Go's os/exec.Cmd.Run and by the
// workspace_exec LLM tool.
func (w *Workspace) RunProgram(
	ctx context.Context,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	if w == nil {
		return codeexecutor.RunResult{}, errors.New(
			"workspaceio: workspace is nil",
		)
	}
	// Normalize Cwd through the same containment policy used by the
	// workspace_exec LLM tool so the godoc claim "cannot escape the
	// workspace" is actually enforced, not left to each backend. The
	// local runtime in particular happily joins ws.Path with
	// filepath.Clean(spec.Cwd) and would otherwise let "../.." run
	// outside the workspace.
	cwd, err := workspacefacade.NormalizeWorkspaceCWD(spec.Cwd)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	spec.Cwd = cwd
	eng, ws, err := w.bindWorkspace(ctx)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	runner := eng.Runner()
	if runner == nil {
		return codeexecutor.RunResult{}, errors.New(
			"workspaceio: executor does not expose a program runner",
		)
	}
	return runner.RunProgram(ctx, ws, spec)
}

// bindWorkspace resolves the engine and acquires the invocation workspace.
func (w *Workspace) bindWorkspace(
	ctx context.Context,
) (codeexecutor.Engine, codeexecutor.Workspace, error) {
	if w == nil || w.resolver == nil {
		return nil, codeexecutor.Workspace{}, errors.New(
			"workspaceio: workspace is not initialized",
		)
	}
	eng := w.resolver.EnsureEngine()
	if eng == nil || eng.FS() == nil || eng.Manager() == nil {
		return nil, codeexecutor.Workspace{}, errors.New(
			"workspaceio: executor does not expose a live workspace engine",
		)
	}
	ws, err := w.resolver.CreateWorkspace(ctx, eng, "workspace")
	if err != nil {
		return nil, codeexecutor.Workspace{}, err
	}
	return eng, ws, nil
}

// toFile converts a codeexecutor.File into the public File type.
// The Truncated flag is forwarded as-is so callers can decide whether
// to retry, fail, or accept the partial bytes.
func toFile(in codeexecutor.File) *File {
	size := in.SizeBytes
	if size <= 0 {
		size = int64(len(in.Content))
	}
	return &File{
		Path:      in.Name,
		Data:      []byte(in.Content),
		MIMEType:  in.MIMEType,
		SizeBytes: size,
		Truncated: in.Truncated,
	}
}
