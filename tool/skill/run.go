//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package skill provides skill-related tools (function calls)
// for executing skill scripts without inlining code into prompts.
package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// RunTool lets the LLM execute commands inside a skill workspace.
// It stages the entire skill directory and runs a single command.
type RunTool struct {
	repo skill.Repository
	exec codeexecutor.CodeExecutor
	reg  *codeexecutor.WorkspaceRegistry
}

// NewRunTool creates a new RunTool.
func NewRunTool(repo skill.Repository,
	exec codeexecutor.CodeExecutor) *RunTool {
	return &RunTool{
		repo: repo,
		exec: exec,
		reg:  codeexecutor.NewWorkspaceRegistry(),
	}
}

// runInput is the JSON schema for skill_run.
type runInput struct {
	Skill          string            `json:"skill"`
	Command        string            `json:"command"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	OutputFiles    []string          `json:"output_files,omitempty"`
	Timeout        int               `json:"timeout,omitempty"`
	SaveArtifacts  bool              `json:"save_as_artifacts,omitempty"`
	OmitInline     bool              `json:"omit_inline_content,omitempty"`
	ArtifactPrefix string            `json:"artifact_prefix,omitempty"`

	Inputs  []codeexecutor.InputSpec `json:"inputs,omitempty"`
	Outputs *codeexecutor.OutputSpec `json:"outputs,omitempty"`
}

// runOutput is the structured result returned by skill_run.
type runOutput struct {
	Stdout        string              `json:"stdout"`
	Stderr        string              `json:"stderr"`
	ExitCode      int                 `json:"exit_code"`
	TimedOut      bool                `json:"timed_out"`
	Duration      int64               `json:"duration_ms"`
	OutputFiles   []codeexecutor.File `json:"output_files"`
	ArtifactFiles []artifactRef       `json:"artifact_files,omitempty"`
}

type artifactRef struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// Declaration implements tool.Tool.
func (t *RunTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "skill_run",
		Description: "Run a command inside a skill workspace",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Run command input",
			Required:    []string{"skill", "command"},
			Properties: map[string]*tool.Schema{
				"skill":   {Type: "string", Description: "Skill name"},
				"command": {Type: "string", Description: "Shell command"},
				"cwd":     {Type: "string", Description: "Working dir"},
				"env": {Type: "object", Description: "Env vars",
					AdditionalProperties: &tool.Schema{Type: "string"}},
				"output_files": {Type: "array",
					Items:       &tool.Schema{Type: "string"},
					Description: "Glob patterns to collect"},
				"timeout":             {Type: "integer", Description: "Seconds"},
				"save_as_artifacts":   {Type: "boolean"},
				"omit_inline_content": {Type: "boolean"},
				"artifact_prefix":     {Type: "string"},
			},
		},
		OutputSchema: &tool.Schema{Type: "object",
			Description: "Run result with output files"},
	}
}

// Call executes the run request.
func (t *RunTool) Call(
	ctx context.Context, args []byte,
) (any, error) {
	in, err := t.parseRunArgs(args)
	if err != nil {
		return nil, err
	}
	root, err := t.repo.Path(in.Skill)
	if err != nil {
		return nil, err
	}
	eng := t.ensureEngine()
	ws, err := t.createWorkspace(ctx, eng, in.Skill)
	if err != nil {
		return nil, err
	}
	if err := t.stageSkill(ctx, eng, ws, root, in.Skill); err != nil {
		return nil, err
	}
	// Prepare IO context and stage declared inputs.
	ctxIO := withArtifactContext(ctx)
	if len(in.Inputs) > 0 {
		if err := eng.FS().StageInputs(ctxIO, ws, in.Inputs); err != nil {
			return nil, err
		}
	}
	// Compute CWD and execute program.
	cwd := resolveCWD(in.Cwd, in.Skill)
	rr, err := t.runProgram(ctx, eng, ws, cwd, in)
	if err != nil {
		return nil, err
	}
	// Collect outputs via spec or legacy globs.
	files, manifest, err := t.prepareOutputs(
		ctxIO, eng, ws, in,
	)
	if err != nil {
		return nil, err
	}
	out := buildRunOutput(rr, files)
	if err := t.attachArtifactsIfRequested(
		ctx, &out, files, in.ArtifactPrefix, in.SaveArtifacts,
		in.OmitInline,
	); err != nil {
		return nil, err
	}
	mergeManifestArtifactRefs(manifest, &out)
	return out, nil
}

var _ tool.Tool = (*RunTool)(nil)
var _ tool.CallableTool = (*RunTool)(nil)

// parseRunArgs validates and decodes input args.
func (t *RunTool) parseRunArgs(args []byte) (runInput, error) {
	var in runInput
	if err := json.Unmarshal(args, &in); err != nil {
		return runInput{}, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(in.Skill) == "" ||
		strings.TrimSpace(in.Command) == "" {
		return runInput{}, fmt.Errorf(
			"skill and command are required",
		)
	}
	if t.exec == nil {
		return runInput{}, fmt.Errorf("executor is not configured")
	}
	return in, nil
}

// ensureEngine gets engine from executor or builds a local one.
func (t *RunTool) ensureEngine() codeexecutor.Engine {
	if ep, ok := t.exec.(codeexecutor.EngineProvider); ok && ep != nil {
		if e := ep.Engine(); e != nil {
			return e
		}
	}
	log.Warnf(
		"skill_run: falling back to local engine; " +
			"no EngineProvider on executor",
	)
	rt := localexec.NewRuntime("")
	return codeexecutor.NewEngine(rt, rt, rt)
}

func (t *RunTool) createWorkspace(
	ctx context.Context, eng codeexecutor.Engine, name string,
) (codeexecutor.Workspace, error) {
	// Acquire a session-scoped workspace using a persistent registry.
	reg := t.reg
	if reg == nil {
		reg = codeexecutor.NewWorkspaceRegistry()
		t.reg = reg
	}
	// Prefer session ID from invocation context; otherwise fallback
	// to skill name.
	sid := name
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
		if inv.Session != nil && inv.Session.ID != "" {
			sid = inv.Session.ID
		}
	}
	return reg.Acquire(ctx, eng.Manager(), sid)
}

func (t *RunTool) stageSkill(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	root string,
	name string,
) error {
	// Compute digest of the skill directory on host.
	dg, err := codeexecutor.DirDigest(root)
	if err != nil {
		return err
	}
	// Ensure layout + load metadata to decide if staging is needed.
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		return err
	}
	md, err := codeexecutor.LoadMetadata(ws.Path)
	if err != nil {
		return err
	}
	if md.Skills == nil {
		md.Skills = map[string]codeexecutor.SkillMeta{}
	}
	// Stage into /skills/<name> inside workspace.
	dest := path.Join(codeexecutor.DirSkills, name)
	// If metadata has same digest, skip staging.
	if s, ok := md.Skills[name]; ok && s.Digest == dg {
		return nil
	}
	// Stage as a regular directory first (no read-only mount). We will
	// add convenience links and then make files read-only except those
	// links. This enables relative paths like "scripts/..." and
	// "out/..." to work from the skill root.
	if err := eng.FS().StageDirectory(
		ctx, ws, root, dest,
		codeexecutor.StageOptions{ReadOnly: false, AllowMount: false},
	); err != nil {
		return err
	}

	// Link workspace-level dirs under the skill root: out, work, inputs.
	if err := t.linkWorkspaceDirs(ctx, eng, ws, name); err != nil {
		return err
	}
	// Make everything under skill root read-only while keeping symlinks
	// untouched so writes land in workspace-level targets.
	if err := t.readOnlyExceptSymlinks(ctx, eng, ws, dest); err != nil {
		return err
	}
	md.Skills[name] = codeexecutor.SkillMeta{
		Name:     name,
		RelPath:  dest,
		Digest:   dg,
		Mounted:  true,
		StagedAt: time.Now(),
	}
	return codeexecutor.SaveMetadata(ws.Path, md)
}

// linkWorkspaceDirs creates convenience symlinks under the staged
// skill root so commands that write to "out/" (relative to the skill
// CWD) resolve to the workspace output directory. It also links work
// and inputs for consistency.
func (t *RunTool) linkWorkspaceDirs(
	ctx context.Context, eng codeexecutor.Engine,
	ws codeexecutor.Workspace, name string,
) error {
	skillRoot := path.Join(codeexecutor.DirSkills, name)
	// Relative links from skills/<name> to workspace dirs.
	toOut := path.Join("..", "..", codeexecutor.DirOut)
	toWork := path.Join("..", "..", codeexecutor.DirWork)
	toInputs := path.Join(
		"..", "..", codeexecutor.DirWork, "inputs",
	)
	var sb strings.Builder
	sb.WriteString("set -e; cd ")
	sb.WriteString(shellQuote(skillRoot))
	sb.WriteString("; mkdir -p ")
	sb.WriteString(shellQuote(toInputs))
	sb.WriteString("; ln -sfn ")
	sb.WriteString(shellQuote(toOut))
	sb.WriteString(" out; ln -sfn ")
	sb.WriteString(shellQuote(toWork))
	sb.WriteString(" work; ln -sfn ")
	sb.WriteString(shellQuote(toInputs))
	sb.WriteString(" inputs")
	_, err := eng.Runner().RunProgram(
		ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", sb.String()},
			Env:     map[string]string{},
			Cwd:     ".",
			Timeout: 5 * time.Second,
		},
	)
	return err
}

// readOnlyExceptSymlinks removes write bits on all regular files and
// directories under the staged skill root while skipping symlinks to
// avoid changing workspace-level targets like out/.
func (t *RunTool) readOnlyExceptSymlinks(
	ctx context.Context, eng codeexecutor.Engine,
	ws codeexecutor.Workspace, dest string,
) error {
	var sb strings.Builder
	// Use find to skip symlinks and chmod others.
	sb.WriteString("set -e; find ")
	sb.WriteString(shellQuote(dest))
	sb.WriteString(" -type l -prune -o -exec chmod a-w {} +")
	_, err := eng.Runner().RunProgram(
		ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", sb.String()},
			Env:     map[string]string{},
			Cwd:     ".",
			Timeout: 5 * time.Second,
		},
	)
	return err
}

// shellQuote wraps a string for safe single-quoted usage in a
// POSIX shell. It escapes embedded single quotes by closing,
// inserting an escaped quote, and reopening.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	q := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + q + "'"
}

func resolveCWD(cwd string, name string) string {
	// Default: run at the skill root. Relative cwd resolves under the
	// skill root. Absolute cwd is respected as-is.
	base := path.Join(codeexecutor.DirSkills, name)
	s := strings.TrimSpace(cwd)
	if s == "" {
		return base
	}
	if strings.HasPrefix(s, "/") {
		return s
	}
	return path.Join(base, s)
}

// filepathBase returns the last element of a path, trimming trailing
// separators. It avoids importing path/filepath at top-level.
func filepathBase(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (t *RunTool) runProgram(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	cwd string,
	in runInput,
) (codeexecutor.RunResult, error) {
	timeout := time.Duration(in.Timeout) * time.Second
	env := in.Env
	if env == nil {
		env = map[string]string{}
	}
	if _, ok := env[codeexecutor.EnvSkillName]; !ok {
		env[codeexecutor.EnvSkillName] = in.Skill
	}
	return eng.Runner().RunProgram(
		ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", in.Command},
			Env:     env,
			Cwd:     cwd,
			Timeout: timeout,
		},
	)
}

// withArtifactContext returns a context augmented with artifact
// service and session info when available from the invocation.
func withArtifactContext(ctx context.Context) context.Context {
	ctxIO := ctx
	if inv, ok := agent.InvocationFromContext(ctx); ok &&
		inv != nil && inv.ArtifactService != nil &&
		inv.Session != nil {
		ctxIO = codeexecutor.WithArtifactService(
			ctxIO, inv.ArtifactService,
		)
		ctxIO = codeexecutor.WithArtifactSession(
			ctxIO, artifact.SessionInfo{
				AppName:   inv.Session.AppName,
				UserID:    inv.Session.UserID,
				SessionID: inv.Session.ID,
			},
		)
	}
	return ctxIO
}

// prepareOutputs collects files either through OutputSpec or legacy
// output_files patterns. It returns collected files and optional
// manifest.
func (t *RunTool) prepareOutputs(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	in runInput,
) ([]codeexecutor.File, *codeexecutor.OutputManifest, error) {
	var files []codeexecutor.File
	var manifest *codeexecutor.OutputManifest
	if in.Outputs != nil && len(in.OutputFiles) == 0 {
		m, err := eng.FS().CollectOutputs(ctx, ws, *in.Outputs)
		if err != nil {
			return nil, nil, err
		}
		manifest = &m
		if in.Outputs.Inline {
			for _, fr := range m.Files {
				files = append(files, codeexecutor.File{
					Name:     fr.Name,
					Content:  fr.Content,
					MIMEType: fr.MIMEType,
				})
			}
		}
		return files, manifest, nil
	}
	fs, err := t.collectFiles(ctx, eng, ws, in.OutputFiles)
	if err != nil {
		return nil, nil, err
	}
	return fs, nil, nil
}

// buildRunOutput converts a RunResult and files into runOutput.
func buildRunOutput(
	rr codeexecutor.RunResult, files []codeexecutor.File,
) runOutput {
	return runOutput{
		Stdout:      rr.Stdout,
		Stderr:      rr.Stderr,
		ExitCode:    rr.ExitCode,
		TimedOut:    rr.TimedOut,
		Duration:    rr.Duration.Milliseconds(),
		OutputFiles: files,
	}
}

// attachArtifactsIfRequested saves files as artifacts when requested
// and optionally omits inline content in the returned files.
func (t *RunTool) attachArtifactsIfRequested(
	ctx context.Context,
	out *runOutput,
	files []codeexecutor.File,
	prefix string,
	save bool,
	omitInline bool,
) error {
	if len(files) == 0 {
		return nil
	}
	// Only act when caller requests artifact persistence.
	if save {
		refs, err := t.saveArtifacts(ctx, files, prefix)
		if err != nil {
			return err
		}
		out.ArtifactFiles = refs
		if omitInline {
			for i := range out.OutputFiles {
				out.OutputFiles[i].Content = ""
			}
		}
	}
	return nil
}

// mergeManifestArtifactRefs appends artifact refs derived from a
// manifest when inline files were not already saved.
func mergeManifestArtifactRefs(
	manifest *codeexecutor.OutputManifest, out *runOutput,
) {
	if manifest == nil || len(out.ArtifactFiles) > 0 {
		return
	}
	for _, fr := range manifest.Files {
		if fr.SavedAs != "" {
			out.ArtifactFiles = append(
				out.ArtifactFiles,
				artifactRef{Name: fr.SavedAs, Version: fr.Version},
			)
		}
	}
}

func (t *RunTool) collectFiles(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	return eng.FS().Collect(ctx, ws, patterns)
}

func (t *RunTool) saveArtifacts(
	ctx context.Context,
	files []codeexecutor.File,
	prefix string,
) ([]artifactRef, error) {
	cb, err := agent.NewCallbackContext(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"artifact save requested but no invocation: %w", err,
		)
	}
	var refs []artifactRef
	for _, f := range files {
		name := f.Name
		if prefix != "" {
			name = prefix + name
		}
		ver, err := cb.SaveArtifact(name, &artifact.Artifact{
			Data:     []byte(f.Content),
			MimeType: f.MIMEType,
			Name:     name,
		})
		if err != nil {
			return nil, fmt.Errorf("save artifact %s: %w", name, err)
		}
		refs = append(refs, artifactRef{Name: name, Version: ver})
	}
	return refs, nil
}
