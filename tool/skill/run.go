//
//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
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
}

// NewRunTool creates a new RunTool.
func NewRunTool(repo skill.Repository,
	exec codeexecutor.CodeExecutor) *RunTool {
	return &RunTool{repo: repo, exec: exec}
}

// runInput is the JSON schema for skill_run.
type runInput struct {
	Skill             string            `json:"skill"`
	Command           string            `json:"command"`
	Cwd               string            `json:"cwd,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	OutputFiles       []string          `json:"output_files,omitempty"`
	Timeout           int               `json:"timeout,omitempty"`
	SaveAsArtifacts   bool              `json:"save_as_artifacts,omitempty"`
	OmitInlineContent bool              `json:"omit_inline_content,omitempty"`
	ArtifactPrefix    string            `json:"artifact_prefix,omitempty"`
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
func (t *RunTool) Call(ctx context.Context, args []byte) (any, error) {
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
	defer eng.Manager().Cleanup(ctx, ws)

	if err := t.stageSkill(ctx, eng, ws, root); err != nil {
		return nil, err
	}

	cwd := resolveCWD(in.Cwd)
	rr, err := t.runProgram(ctx, eng, ws, cwd, in)
	if err != nil {
		return nil, err
	}

	files, err := t.collectFiles(ctx, eng, ws, in.OutputFiles)
	if err != nil {
		return nil, err
	}

	out := runOutput{
		Stdout:      rr.Stdout,
		Stderr:      rr.Stderr,
		ExitCode:    rr.ExitCode,
		TimedOut:    rr.TimedOut,
		Duration:    rr.Duration.Milliseconds(),
		OutputFiles: files,
	}

	if in.SaveAsArtifacts && len(files) > 0 {
		refs, err := t.saveArtifacts(ctx, files, in.ArtifactPrefix)
		if err != nil {
			return nil, err
		}
		out.ArtifactFiles = refs
		if in.OmitInlineContent {
			for i := range out.OutputFiles {
				out.OutputFiles[i].Content = ""
			}
		}
	}
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
	return eng.Manager().CreateWorkspace(
		ctx, name, codeexecutor.WorkspacePolicy{},
	)
}

func (t *RunTool) stageSkill(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	root string,
) error {
	const stagedSkillDir = "skill"
	return eng.FS().StageDirectory(
		ctx, ws, root, stagedSkillDir,
		codeexecutor.StageOptions{ReadOnly: false, AllowMount: true},
	)
}

func resolveCWD(cwd string) string {
	const stagedSkillDir = "skill"
	if strings.TrimSpace(cwd) == "" {
		// Default to workspace root so output patterns like
		// "out/*.txt" are relative to root, as tests expect.
		return ""
	}
	if !strings.HasPrefix(cwd, "/") {
		return path.Join(stagedSkillDir, cwd)
	}
	return cwd
}

func (t *RunTool) runProgram(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	cwd string,
	in runInput,
) (codeexecutor.RunResult, error) {
	timeout := time.Duration(in.Timeout) * time.Second
	return eng.Runner().RunProgram(
		ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", in.Command},
			Env:     in.Env,
			Cwd:     cwd,
			Timeout: timeout,
		},
	)
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
