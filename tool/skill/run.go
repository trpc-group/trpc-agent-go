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
	var in runInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if in.Skill == "" || in.Command == "" {
		return nil, fmt.Errorf("skill and command are required")
	}
	root, err := t.repo.Path(in.Skill)
	if err != nil {
		return nil, err
	}
	if t.exec == nil {
		return nil, fmt.Errorf("executor is not configured")
	}

	// Obtain engine from executor when available; fallback to local.
	var eng codeexecutor.Engine
	if ep, ok := t.exec.(codeexecutor.EngineProvider); ok && ep != nil {
		eng = ep.Engine()
	}
	if eng == nil {
		log.Warnf(
			"skill_run: falling back to local engine; " +
				"no EngineProvider on executor",
		)
		rt := localexec.NewRuntime("")
		eng = codeexecutor.NewEngine(rt, rt, rt)
	}

	ws, err := eng.Manager().CreateWorkspace(
		ctx, in.Skill, codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		return nil, err
	}
	defer eng.Manager().Cleanup(ctx, ws)

	const stagedSkillDir = "skill"
	if err := eng.FS().StageDirectory(ctx, ws, root, stagedSkillDir,
		codeexecutor.StageOptions{ReadOnly: false, AllowMount: true},
	); err != nil {
		return nil, err
	}

	// Default CWD to staged skill root when not provided. If a
	// relative CWD is provided, resolve it under the staged skill
	// directory to make commands skill-root-relative by default.
	cwd := in.Cwd
	if cwd == "" {
		cwd = stagedSkillDir
	} else if !strings.HasPrefix(cwd, "/") {
		cwd = path.Join(stagedSkillDir, cwd)
	}

	timeout := time.Duration(in.Timeout) * time.Second
	rr, err := eng.Runner().RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-lc", in.Command},
		Env:     in.Env,
		Cwd:     cwd,
		Timeout: timeout,
	})
	if err != nil {
		return nil, err
	}

	var files []codeexecutor.File
	if len(in.OutputFiles) > 0 {
		files, err = eng.FS().Collect(ctx, ws, in.OutputFiles)
		if err != nil {
			return nil, err
		}
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
		cb, err := agent.NewCallbackContext(ctx)
		if err != nil {
			return nil, fmt.Errorf(
				"artifact save requested but no invocation: %w", err,
			)
		}
		var refs []artifactRef
		for _, f := range files {
			name := f.Name
			if in.ArtifactPrefix != "" {
				name = in.ArtifactPrefix + name
			}
			ver, err := cb.SaveArtifact(name, &artifact.Artifact{
				Data: []byte(f.Content), MimeType: f.MIMEType, Name: name,
			})
			if err != nil {
				return nil, fmt.Errorf("save artifact %s: %w", name, err)
			}
			refs = append(refs, artifactRef{Name: name, Version: ver})
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
