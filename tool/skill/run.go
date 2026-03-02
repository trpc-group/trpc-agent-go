//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
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

	allowedCmds map[string]struct{}
	deniedCmds  map[string]struct{}
}

// NewRunTool creates a new RunTool.
func NewRunTool(
	repo skill.Repository,
	exec codeexecutor.CodeExecutor,
	opts ...func(*RunTool),
) *RunTool {
	rt := &RunTool{
		repo: repo,
		exec: exec,
		reg:  codeexecutor.NewWorkspaceRegistry(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(rt)
		}
	}
	rt.loadAllowedCommandsFromEnv()
	rt.loadDeniedCommandsFromEnv()
	return rt
}

const envAllowedCommands = "TRPC_AGENT_SKILL_RUN_ALLOWED_COMMANDS"
const envDeniedCommands = "TRPC_AGENT_SKILL_RUN_DENIED_COMMANDS"

const defaultSkillRunTimeout = 5 * time.Minute

const (
	defaultAutoExportPattern = codeexecutor.DirOut + "/**"
	defaultAutoExportMax     = 20
)

const (
	skillDirInputs = "inputs"
	skillDirVenv   = ".venv"
)

const (
	envPath       = "PATH"
	envVirtualEnv = "VIRTUAL_ENV"
)

const workspaceMetadataFileMode uint32 = 0o600

const workspaceMetadataTmpFile = ".metadata.tmp"

// WithAllowedCommands restricts skill_run to a single program execution
// whose command name is in the allowlist.
//
// When enabled, shell features (pipes, redirects, separators) are
// rejected and the command is executed without a shell.
func WithAllowedCommands(cmds ...string) func(*RunTool) {
	return func(t *RunTool) {
		t.setAllowedCommands(cmds)
	}
}

// WithDeniedCommands rejects a single program execution whose command name
// matches the denylist.
//
// When enabled, shell features (pipes, redirects, separators) are rejected
// and the command is executed without a shell.
func WithDeniedCommands(cmds ...string) func(*RunTool) {
	return func(t *RunTool) {
		t.setDeniedCommands(cmds)
	}
}

func (t *RunTool) loadAllowedCommandsFromEnv() {
	if len(t.allowedCmds) > 0 {
		return
	}
	raw := os.Getenv(envAllowedCommands)
	parts := splitCommandList(raw)
	if len(parts) == 0 {
		return
	}
	t.setAllowedCommands(parts)
}

func (t *RunTool) setAllowedCommands(cmds []string) {
	if len(cmds) == 0 {
		return
	}
	if t.allowedCmds == nil {
		t.allowedCmds = make(map[string]struct{}, len(cmds))
	}
	for _, c := range cmds {
		s := strings.TrimSpace(c)
		if s == "" {
			continue
		}
		t.allowedCmds[s] = struct{}{}
	}
}

func (t *RunTool) loadDeniedCommandsFromEnv() {
	if len(t.deniedCmds) > 0 {
		return
	}
	raw := os.Getenv(envDeniedCommands)
	parts := splitCommandList(raw)
	if len(parts) == 0 {
		return
	}
	t.setDeniedCommands(parts)
}

func (t *RunTool) setDeniedCommands(cmds []string) {
	if len(cmds) == 0 {
		return
	}
	if t.deniedCmds == nil {
		t.deniedCmds = make(map[string]struct{}, len(cmds))
	}
	for _, c := range cmds {
		s := strings.TrimSpace(c)
		if s == "" {
			continue
		}
		t.deniedCmds[s] = struct{}{}
	}
}

func splitCommandList(raw string) []string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		out = append(out, p)
	}
	return out
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
	OutputFiles   []runFile     `json:"output_files"`
	PrimaryOutput *runFile      `json:"primary_output,omitempty"`
	Stdout        string        `json:"stdout"`
	Stderr        string        `json:"stderr"`
	ExitCode      int           `json:"exit_code"`
	TimedOut      bool          `json:"timed_out"`
	Duration      int64         `json:"duration_ms"`
	ArtifactFiles []artifactRef `json:"artifact_files,omitempty"`
	Warnings      []string      `json:"warnings,omitempty"`
}

type runFile struct {
	codeexecutor.File
	Ref string `json:"ref,omitempty"`
}

type artifactRef struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// Declaration implements tool.Tool.
func (t *RunTool) Declaration() *tool.Declaration {
	desc := "Run a command inside a skill workspace. " +
		"Use it only for commands required by the skill " +
		"docs (not for generic shell tasks). " +
		"Returns stdout/stderr, a primary_output " +
		"(best small text file), and collected output_files " +
		"(text inline by default, with workspace:// refs). " +
		"Non-text outputs omit inline content. " +
		"Prefer primary_output/output_files content; " +
		"use output_files[*].ref when passing a file to " +
		"other tools."
	cmdDesc := "Shell command"
	if len(t.allowedCmds) > 0 || len(t.deniedCmds) > 0 {
		desc += " Restrictions enabled when allowed_commands/denied_commands are set: " +
			"no shell; one executable + args only; no > < | ; && ||."
		cmdDesc = "Command string (no shell syntax when allowed_commands/denied_commands are set)"
		if len(t.allowedCmds) > 0 {
			desc += " Allowed commands: " + formatCommandPreview(t.allowedCmds, 20) + "."
		}
	}
	return &tool.Declaration{
		Name:        "skill_run",
		Description: desc,
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Run command input",
			Required:    []string{"skill", "command"},
			Properties: map[string]*tool.Schema{
				"skill":   skillNameSchema(t.repo, "Skill name"),
				"command": {Type: "string", Description: cmdDesc},
				"cwd":     {Type: "string", Description: "Working dir"},
				"env": {Type: "object", Description: "Env vars",
					AdditionalProperties: &tool.Schema{Type: "string"}},
				"output_files": {Type: "array",
					Items: &tool.Schema{Type: "string"},
					Description: "Workspace-relative paths/globs to " +
						"collect and inline text (e.g. out/*.txt). " +
						"Non-text outputs omit inline content. " +
						"Prefer output_files content; for other " +
						"tools use output_files[*].ref " +
						"(workspace://...). Do not use " +
						"workspace:// or artifact:// here."},
				"timeout": {Type: "integer", Description: "Seconds"},
				"save_as_artifacts": {Type: "boolean", Description: "" +
					"Persist collected files via Artifact service"},
				"omit_inline_content": {Type: "boolean", Description: "" +
					"Omit output_files content (metadata only). " +
					"Non-text outputs are always metadata only. " +
					"Use output_files[*].ref to read later."},
				"artifact_prefix": {Type: "string", Description: "" +
					"With save_as_artifacts, prefix artifact names"},
				"inputs":  inputSpecsSchema(),
				"outputs": outputSpecSchema(),
			},
		},
		OutputSchema: &tool.Schema{Type: "object",
			Description: "Run result with output files"},
	}
}

func formatCommandPreview(cmds map[string]struct{}, max int) string {
	if len(cmds) == 0 {
		return ""
	}
	if max <= 0 {
		max = 1
	}
	items := make([]string, 0, len(cmds))
	for cmd := range cmds {
		items = append(items, cmd)
	}
	sort.Strings(items)
	more := 0
	if len(items) > max {
		more = len(items) - max
		items = items[:max]
	}
	out := strings.Join(items, ", ")
	if more > 0 {
		out += fmt.Sprintf(" (+%d more)", more)
	}
	return out
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

	autoFiles := t.autoExportWorkspaceOut(ctxIO, eng, ws, in)

	// Collect outputs via spec or legacy globs.
	files, manifest, err := t.prepareOutputs(
		ctxIO, eng, ws, in,
	)
	if err != nil {
		return nil, err
	}
	trimTruncatedUTF8TextFiles(files)
	out := buildRunOutput(rr, files)
	mergeAutoPrimaryOutput(autoFiles, &out)
	if len(files) > 0 && !in.SaveArtifacts {
		out.Warnings = append(out.Warnings,
			warnOutputFilesWorkspaceOnly)
	}
	if err := t.attachArtifactsIfRequested(
		ctx, &out, files, in.ArtifactPrefix, in.SaveArtifacts,
		in.OmitInline,
	); err != nil {
		return nil, err
	}
	mergeManifestArtifactRefs(manifest, &out)
	applyOmitInlineContent(ctx, &out, in.OmitInline)
	toolcache.StoreSkillRunOutputFilesFromContext(ctx, files)
	return out, nil
}

var _ tool.Tool = (*RunTool)(nil)
var _ tool.CallableTool = (*RunTool)(nil)

func (t *RunTool) autoExportWorkspaceOut(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	in runInput,
) []codeexecutor.File {
	if eng == nil || in.Outputs != nil || len(in.OutputFiles) > 0 {
		return nil
	}
	files, err := eng.FS().Collect(ctx, ws,
		[]string{defaultAutoExportPattern})
	if err != nil || len(files) == 0 {
		return nil
	}
	if len(files) > defaultAutoExportMax {
		files = files[:defaultAutoExportMax]
	}
	trimTruncatedUTF8TextFiles(files)
	toolcache.StoreSkillRunOutputFilesFromContext(ctx, files)
	return files
}

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
	md, err := t.loadWorkspaceMetadata(ctx, eng, ws)
	if err != nil {
		return err
	}
	// Stage into /skills/<name> inside workspace.
	dest := path.Join(codeexecutor.DirSkills, name)
	// If metadata has same digest, skip staging.
	if s, ok := md.Skills[name]; ok &&
		s.Digest == dg && s.Mounted {
		ok, err := t.skillLinksPresent(ctx, eng, ws, name)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	if err := t.removeWorkspacePath(ctx, eng, ws, dest); err != nil {
		return err
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
	return t.saveWorkspaceMetadata(ctx, eng, ws, md)
}

func (t *RunTool) loadWorkspaceMetadata(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
) (codeexecutor.WorkspaceMetadata, error) {
	now := time.Now()
	md := codeexecutor.WorkspaceMetadata{
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
		LastAccess: now,
		Skills:     map[string]codeexecutor.SkillMeta{},
	}
	if eng == nil || eng.FS() == nil {
		return md, fmt.Errorf("workspace fs is not configured")
	}
	files, err := eng.FS().Collect(
		ctx, ws, []string{codeexecutor.MetaFileName},
	)
	if err != nil {
		return md, err
	}
	if len(files) == 0 || strings.TrimSpace(files[0].Content) == "" {
		return md, nil
	}
	if err := json.Unmarshal([]byte(files[0].Content), &md); err != nil {
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

func (t *RunTool) saveWorkspaceMetadata(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	md codeexecutor.WorkspaceMetadata,
) error {
	if eng == nil || eng.FS() == nil {
		return fmt.Errorf("workspace fs is not configured")
	}
	if eng.Runner() == nil {
		return fmt.Errorf("workspace runner is not configured")
	}
	if md.Version == 0 {
		md.Version = 1
	}
	now := time.Now()
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
	if err := eng.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    workspaceMetadataTmpFile,
		Content: buf,
		Mode:    workspaceMetadataFileMode,
	}}); err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("set -e; mv -f ")
	sb.WriteString(shellQuote(workspaceMetadataTmpFile))
	sb.WriteString(" ")
	sb.WriteString(shellQuote(codeexecutor.MetaFileName))
	_, err = eng.Runner().RunProgram(
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

func (t *RunTool) skillLinksPresent(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	name string,
) (bool, error) {
	skillName := strings.TrimSpace(name)
	if skillName == "" {
		return false, nil
	}
	if eng == nil || eng.Runner() == nil {
		return false, fmt.Errorf("workspace runner is not configured")
	}
	base := path.Join(codeexecutor.DirSkills, skillName)
	var sb strings.Builder
	sb.WriteString("test -L ")
	sb.WriteString(shellQuote(path.Join(base, codeexecutor.DirOut)))
	sb.WriteString(" && test -L ")
	sb.WriteString(shellQuote(path.Join(base, codeexecutor.DirWork)))
	sb.WriteString(" && test -L ")
	sb.WriteString(shellQuote(path.Join(base, skillDirInputs)))
	rr, err := eng.Runner().RunProgram(
		ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", sb.String()},
			Env:     map[string]string{},
			Cwd:     ".",
			Timeout: 5 * time.Second,
		},
	)
	if err != nil {
		return false, err
	}
	if rr.ExitCode == 0 {
		return true, nil
	}
	return false, nil
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
	sb.WriteString("; rm -rf out work inputs ")
	sb.WriteString(shellQuote(skillDirVenv))
	sb.WriteString("; mkdir -p ")
	sb.WriteString(shellQuote(toInputs))
	sb.WriteString(" ")
	sb.WriteString(shellQuote(skillDirVenv))
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

func (t *RunTool) removeWorkspacePath(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
) error {
	target := strings.TrimSpace(rel)
	if target == "" {
		return nil
	}
	if eng == nil || eng.Runner() == nil {
		return fmt.Errorf("workspace runner is not configured")
	}
	var sb strings.Builder
	sb.WriteString("set -e; if [ -e ")
	sb.WriteString(shellQuote(target))
	sb.WriteString(" ]; then find ")
	sb.WriteString(shellQuote(target))
	sb.WriteString(" -type l -prune -o -exec chmod u+w {} +; fi")
	sb.WriteString("; rm -rf ")
	sb.WriteString(shellQuote(target))
	_, err := eng.Runner().RunProgram(
		ctx,
		ws,
		codeexecutor.RunProgramSpec{
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
	venv := path.Join(dest, skillDirVenv)
	var sb strings.Builder
	// Use find to skip symlinks and chmod others.
	sb.WriteString("set -e; find ")
	sb.WriteString(shellQuote(dest))
	sb.WriteString(" -path ")
	sb.WriteString(shellQuote(venv))
	sb.WriteString(" -prune -o -type l -prune -o -exec chmod a-w {} +")
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

const (
	envVarPrefix = "$"
	envVarLBrace = "${"
	envVarRBrace = "}"
)

func hasEnvPrefix(s string, name string) bool {
	if strings.HasPrefix(s, envVarPrefix+name) {
		tail := s[len(envVarPrefix+name):]
		return tail == "" || strings.HasPrefix(tail, "/") ||
			strings.HasPrefix(tail, "\\")
	}
	prefix := envVarLBrace + name + envVarRBrace
	if strings.HasPrefix(s, prefix) {
		tail := s[len(prefix):]
		return tail == "" || strings.HasPrefix(tail, "/") ||
			strings.HasPrefix(tail, "\\")
	}
	return false
}

func isWorkspaceEnvPath(s string) bool {
	return hasEnvPrefix(s, codeexecutor.WorkspaceEnvDirKey) ||
		hasEnvPrefix(s, codeexecutor.EnvSkillsDir) ||
		hasEnvPrefix(s, codeexecutor.EnvWorkDir) ||
		hasEnvPrefix(s, codeexecutor.EnvOutputDir) ||
		hasEnvPrefix(s, codeexecutor.EnvRunDir)
}

func isAllowedWorkspacePath(rel string) bool {
	switch {
	case rel == codeexecutor.DirSkills || strings.HasPrefix(rel, codeexecutor.DirSkills+"/"):
		return true
	case rel == codeexecutor.DirWork || strings.HasPrefix(rel, codeexecutor.DirWork+"/"):
		return true
	case rel == codeexecutor.DirOut || strings.HasPrefix(rel, codeexecutor.DirOut+"/"):
		return true
	case rel == codeexecutor.DirRuns || strings.HasPrefix(rel, codeexecutor.DirRuns+"/"):
		return true
	default:
		return false
	}
}

func sanitizeWorkspaceRelPath(rel string, fallback string) string {
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == "" {
		return "."
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fallback
	}
	if isAllowedWorkspacePath(cleaned) {
		return cleaned
	}
	return fallback
}

func resolveCWD(cwd string, name string) string {
	// Default: run at the skill root. Relative cwd resolves under the
	// skill root. "$WORK_DIR" style paths resolve to workspace-relative
	// roots. Absolute paths are treated as workspace-absolute and must
	// start with known workspace dirs like "/skills" or "/work".
	base := path.Join(codeexecutor.DirSkills, name)
	s := strings.TrimSpace(cwd)
	s = strings.ReplaceAll(s, "\\", "/")
	if s == "" {
		return base
	}

	if isWorkspaceEnvPath(s) {
		if out := codeexecutor.NormalizeGlobs([]string{s}); len(out) > 0 {
			return sanitizeWorkspaceRelPath(out[0], base)
		}
		return base
	}

	if strings.HasPrefix(s, "/") {
		rel := strings.TrimPrefix(path.Clean(s), "/")
		if rel == "" || rel == "." {
			return "."
		}
		if isAllowedWorkspacePath(rel) {
			return rel
		}
		return base
	}

	joined := path.Join(base, s)
	if joined == base || strings.HasPrefix(joined, base+"/") {
		return joined
	}
	return base
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
	if in.Timeout <= 0 {
		timeout = defaultSkillRunTimeout
	}
	env := in.Env
	if env == nil {
		env = map[string]string{}
	}
	if _, ok := env[codeexecutor.EnvSkillName]; !ok {
		env[codeexecutor.EnvSkillName] = in.Skill
	}

	venvRel, venvBinRel := venvRelPaths(cwd, in.Skill)

	if len(t.allowedCmds) > 0 || len(t.deniedCmds) > 0 {
		injectVenvEnv(env, venvRel, venvBinRel)
		argv, err := splitCommandLine(in.Command)
		if err != nil {
			return codeexecutor.RunResult{}, err
		}
		cmd := argv[0]
		if len(t.allowedCmds) > 0 && !cmdInList(t.allowedCmds, cmd) {
			return codeexecutor.RunResult{}, fmt.Errorf(
				"skill_run: command %q is not allowed by allowed_commands",
				cmd,
			)
		}
		if cmdInList(t.deniedCmds, cmd) {
			return codeexecutor.RunResult{}, fmt.Errorf(
				"skill_run: command %q is denied by denied_commands",
				cmd,
			)
		}
		return eng.Runner().RunProgram(
			ctx, ws, codeexecutor.RunProgramSpec{
				Cmd:     cmd,
				Args:    argv[1:],
				Env:     env,
				Cwd:     cwd,
				Timeout: timeout,
			},
		)
	}

	cmd := wrapWithVenvPrefix(in.Command, venvRel, venvBinRel)
	return eng.Runner().RunProgram(
		ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-c", cmd},
			Env:     env,
			Cwd:     cwd,
			Timeout: timeout,
		},
	)
}

func venvRelPaths(cwd string, skillName string) (string, string) {
	base := path.Clean(strings.TrimSpace(cwd))
	if base == "" {
		base = "."
	}
	skillRoot := path.Join(codeexecutor.DirSkills, skillName)
	venv := path.Join(skillRoot, skillDirVenv)
	venvBin := path.Join(venv, "bin")

	relVenv := slashRel(base, venv)
	relBin := slashRel(base, venvBin)
	return relVenv, relBin
}

func slashRel(base string, target string) string {
	base = strings.TrimPrefix(path.Clean(base), "/")
	target = strings.TrimPrefix(path.Clean(target), "/")
	if base == "." {
		base = ""
	}
	if target == "." {
		target = ""
	}
	if base == target {
		return "."
	}

	var bParts []string
	if base != "" {
		bParts = strings.Split(base, "/")
	}
	var tParts []string
	if target != "" {
		tParts = strings.Split(target, "/")
	}

	i := 0
	for i < len(bParts) && i < len(tParts) && bParts[i] == tParts[i] {
		i++
	}
	var out []string
	for j := i; j < len(bParts); j++ {
		if bParts[j] == "" || bParts[j] == "." {
			continue
		}
		out = append(out, "..")
	}
	out = append(out, tParts[i:]...)
	if len(out) == 0 {
		return "."
	}
	return strings.Join(out, "/")
}

func injectVenvEnv(env map[string]string, venv string, venvBin string) {
	if env == nil {
		return
	}
	if _, ok := env[envVirtualEnv]; !ok && strings.TrimSpace(venv) != "" {
		env[envVirtualEnv] = venv
	}
	basePATH := strings.TrimSpace(env[envPath])
	if basePATH == "" {
		basePATH = strings.TrimSpace(os.Getenv(envPath))
	}
	sep := string(os.PathListSeparator)
	if basePATH == "" {
		env[envPath] = venvBin
		return
	}
	env[envPath] = venvBin + sep + basePATH
}

func wrapWithVenvPrefix(cmd string, venv string, venvBin string) string {
	var sb strings.Builder
	sb.WriteString("export ")
	sb.WriteString(envPath)
	sb.WriteString("=")
	sb.WriteString(shellQuote(venvBin))
	sb.WriteString(":\"$")
	sb.WriteString(envPath)
	sb.WriteString("\"; ")
	sb.WriteString("if [ -z \"$")
	sb.WriteString(envVirtualEnv)
	sb.WriteString("\" ]; then export ")
	sb.WriteString(envVirtualEnv)
	sb.WriteString("=")
	sb.WriteString(shellQuote(venv))
	sb.WriteString("; fi; ")
	sb.WriteString(cmd)
	return sb.String()
}

const disallowedShellMeta = "\n\r;&|<>"

func cmdInList(list map[string]struct{}, cmd string) bool {
	if len(list) == 0 {
		return false
	}
	if _, ok := list[cmd]; ok {
		return true
	}
	base := filepathBase(cmd)
	_, ok := list[base]
	return ok
}

func splitCommandLine(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("skill_run: command is empty")
	}
	if idx := strings.IndexAny(s, disallowedShellMeta); idx >= 0 {
		meta := s[idx : idx+1]
		return nil, fmt.Errorf(
			"skill_run: shell metacharacter %q is not allowed when command restrictions are enabled (allowed_commands/denied_commands set). Use a single executable with args only (no redirects/pipes/chaining). To allow shell syntax, clear allowed_commands/denied_commands in the tool config",
			meta,
		)
	}
	var args []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		args = append(args, cur.String())
		cur.Reset()
	}
	for _, r := range s {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if !inSingle && r == '\\' {
			escaped = true
			continue
		}
		if !inDouble && r == '\'' {
			inSingle = !inSingle
			continue
		}
		if !inSingle && r == '"' {
			inDouble = !inDouble
			continue
		}
		if !inSingle && !inDouble && (r == ' ' || r == '\t') {
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	if escaped {
		return nil, fmt.Errorf("skill_run: trailing escape")
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("skill_run: unterminated quote")
	}
	flush()
	if len(args) == 0 {
		return nil, fmt.Errorf("skill_run: command is empty")
	}
	return args, nil
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
					Name:      fr.Name,
					Content:   fr.Content,
					MIMEType:  fr.MIMEType,
					SizeBytes: fr.SizeBytes,
					Truncated: fr.Truncated,
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
	stdout, stdoutTrunc := truncateOutput(rr.Stdout)
	stderr, stderrTrunc := truncateOutput(rr.Stderr)
	var warnings []string
	if stdoutTrunc {
		warnings = append(warnings, warnStdoutTruncated)
	}
	if stderrTrunc {
		warnings = append(warnings, warnStderrTruncated)
	}

	outFiles := toRunFiles(files)
	return runOutput{
		OutputFiles:   outFiles,
		PrimaryOutput: selectPrimaryOutput(outFiles),
		Stdout:        stdout,
		Stderr:        stderr,
		ExitCode:      rr.ExitCode,
		TimedOut:      rr.TimedOut,
		Duration:      rr.Duration.Milliseconds(),
		Warnings:      warnings,
	}
}

const (
	maxOutputChars = 16 * 1024
)

const (
	maxPrimaryOutputChars = 32 * 1024
)

const (
	warnStdoutTruncated = "stdout truncated"
	warnStderrTruncated = "stderr truncated"
)

func truncateOutput(s string) (string, bool) {
	if len(s) <= maxOutputChars {
		return s, false
	}
	return s[:maxOutputChars], true
}

func toRunFiles(files []codeexecutor.File) []runFile {
	out := make([]runFile, 0, len(files))
	for _, f := range files {
		rf := f
		if !shouldInlineFileContent(rf) {
			rf.Content = ""
		}
		out = append(out, runFile{
			File: rf,
			Ref:  fileref.WorkspaceRef(f.Name),
		})
	}
	return out
}

func shouldInlineFileContent(f codeexecutor.File) bool {
	if f.Content == "" {
		return true
	}
	if !codeexecutor.IsTextMIME(f.MIMEType) {
		return false
	}
	if strings.IndexByte(f.Content, 0) >= 0 {
		return false
	}
	return utf8.ValidString(f.Content)
}

const maxTrimUTF8SuffixBytes = utf8.UTFMax - 1

func trimTruncatedUTF8TextFiles(files []codeexecutor.File) {
	for i := range files {
		f := &files[i]
		if !f.Truncated || f.Content == "" {
			continue
		}
		if !codeexecutor.IsTextMIME(f.MIMEType) {
			continue
		}
		if strings.IndexByte(f.Content, 0) >= 0 {
			continue
		}
		if utf8.ValidString(f.Content) {
			continue
		}
		n := validUTF8PrefixLen(f.Content)
		if len(f.Content)-n > maxTrimUTF8SuffixBytes {
			continue
		}
		if n > 0 {
			f.Content = f.Content[:n]
		}
	}
}

func validUTF8PrefixLen(s string) int {
	n := 0
	for n < len(s) {
		r, size := utf8.DecodeRuneInString(s[n:])
		if r == utf8.RuneError && size == 1 {
			break
		}
		n += size
	}
	return n
}

func selectPrimaryOutput(files []runFile) *runFile {
	var best *runFile
	for _, f := range files {
		if strings.TrimSpace(f.Content) == "" {
			continue
		}
		if !codeexecutor.IsTextMIME(f.MIMEType) {
			continue
		}
		if len(f.Content) > maxPrimaryOutputChars {
			continue
		}
		if best != nil && best.Name < f.Name {
			continue
		}
		tmp := f
		best = &tmp
	}
	return best
}

func mergeAutoPrimaryOutput(files []codeexecutor.File, out *runOutput) {
	if out == nil || out.PrimaryOutput != nil || len(files) == 0 {
		return
	}
	runFiles := toRunFiles(files)
	out.PrimaryOutput = selectPrimaryOutput(runFiles)
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
		reason := artifactSaveSkipReason(ctx)
		if reason != "" {
			// Best-effort behavior: keep output_files content since
			// artifacts were not persisted anywhere.
			appendWarning(out, reason)
			return nil
		}
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

const warnSaveArtifactsSkippedTmpl = "save_as_artifacts requested but " +
	"%s; outputs are not persisted"

const warnOutputFilesWorkspaceOnly = "output_files are workspace-" +
	"relative; prefer output_files content; when you need a " +
	"stable reference, use output_files[*].ref (workspace://...)"

const (
	reasonNoInvocation = "invocation is missing from context"
	reasonNoService    = "artifact service is not configured"
	reasonNoSession    = "session is missing from invocation"
	reasonNoSessionIDs = "session app/user/session IDs are missing"
)

func artifactSaveSkipReason(ctx context.Context) string {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return reasonNoInvocation
	}
	if inv.ArtifactService == nil {
		return reasonNoService
	}
	if inv.Session == nil {
		return reasonNoSession
	}
	if inv.Session.AppName == "" || inv.Session.UserID == "" ||
		inv.Session.ID == "" {
		return reasonNoSessionIDs
	}
	return ""
}

func appendWarning(out *runOutput, reason string) {
	if out == nil || reason == "" {
		return
	}
	out.Warnings = append(
		out.Warnings,
		fmt.Sprintf(warnSaveArtifactsSkippedTmpl, reason),
	)
}

const warnOmitInlineNoFallback = "omit_inline_content requested but " +
	"invocation is missing; returning inline output_files"

func applyOmitInlineContent(
	ctx context.Context,
	out *runOutput,
	omit bool,
) {
	if out == nil || !omit {
		return
	}
	if !hasOmitInlineFallback(ctx) {
		out.Warnings = append(out.Warnings, warnOmitInlineNoFallback)
		return
	}
	for i := range out.OutputFiles {
		out.OutputFiles[i].Content = ""
	}
	if out.PrimaryOutput != nil {
		out.PrimaryOutput.Content = ""
	}
}

func hasOmitInlineFallback(ctx context.Context) bool {
	inv, ok := agent.InvocationFromContext(ctx)
	return ok && inv != nil
}

func inputSpecsSchema() *tool.Schema {
	return &tool.Schema{
		Type:        "array",
		Description: "Declarative inputs to stage into workspace",
		Items: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"from": {Type: "string", Description: "" +
					"Source ref (artifact://, host://, " +
					"workspace://, skill://)"},
				"to": {Type: "string", Description: "" +
					"Workspace-relative destination"},
				"mode": {Type: "string", Description: "" +
					"copy (default) or link"},
				"pin": {Type: "boolean", Description: "" +
					"Pin artifact version when supported"},
			},
		},
	}
}

func outputSpecSchema() *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: "Declarative outputs with limits and persistence",
		Properties: map[string]*tool.Schema{
			"globs": {
				Type:  "array",
				Items: &tool.Schema{Type: "string"},
				Description: "Workspace-relative patterns " +
					"(supports ** and $OUTPUT_DIR/**)",
			},
			"inline": {Type: "boolean", Description: "" +
				"Inline file contents into result"},
			"save": {Type: "boolean", Description: "" +
				"Persist outputs via Artifact service"},
			"name_template": {Type: "string", Description: "" +
				"Prefix for artifact names (e.g. pref/)"},
			"max_files": {Type: "integer", Description: "" +
				"Max number of matched files"},
			"max_file_bytes": {Type: "integer", Description: "" +
				"Max bytes per file (default 4 MiB)"},
			"max_total_bytes": {Type: "integer", Description: "" +
				"Max total bytes across files (default 64 MiB)"},
		},
	}
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
