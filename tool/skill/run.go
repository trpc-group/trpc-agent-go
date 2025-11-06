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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
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
	Skill       string            `json:"skill"`
	Command     string            `json:"command"`
	Cwd         string            `json:"cwd,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	OutputFiles []string          `json:"output_files,omitempty"`
	Timeout     int               `json:"timeout,omitempty"`
	// SaveAsArtifacts saves output files into artifact service when
	// available, returning artifact references instead of (or in
	// addition to) inline content.
	SaveAsArtifacts bool `json:"save_as_artifacts,omitempty"`
	// OmitInlineContent drops file contents from OutputFiles when
	// SaveAsArtifacts is true, reducing payload size.
	OmitInlineContent bool `json:"omit_inline_content,omitempty"`
	// ArtifactPrefix is an optional prefix for artifact filenames.
	// For user-namespaced artifacts, set to "user:".
	ArtifactPrefix string `json:"artifact_prefix,omitempty"`
}

// runOutput is the structured result returned by skill_run.
type runOutput struct {
	Stdout      string              `json:"stdout"`
	Stderr      string              `json:"stderr"`
	ExitCode    int                 `json:"exit_code"`
	TimedOut    bool                `json:"timed_out"`
	Duration    int64               `json:"duration_ms"`
	OutputFiles []codeexecutor.File `json:"output_files"`
	// ArtifactFiles lists saved artifacts when SaveAsArtifacts is
	// requested. Each entry contains the filename and version.
	ArtifactFiles []artifactRef `json:"artifact_files,omitempty"`
}

// artifactRef captures a saved artifact reference.
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
				"skill": {
					Type:        "string",
					Description: "Skill name to run",
				},
				"command": {
					Type:        "string",
					Description: "Shell command to execute",
				},
				"cwd": {
					Type:        "string",
					Description: "Working dir under skill root",
				},
				"env": {
					Type:                 "object",
					Description:          "Environment variables",
					AdditionalProperties: &tool.Schema{Type: "string"},
				},
				"output_files": {
					Type:        "array",
					Items:       &tool.Schema{Type: "string"},
					Description: "Glob patterns to collect",
				},
				"timeout": {
					Type:        "integer",
					Description: "Timeout in seconds",
				},
				"save_as_artifacts": {
					Type:        "boolean",
					Description: "Save files via artifact service",
				},
				"omit_inline_content": {
					Type:        "boolean",
					Description: "Do not inline file contents",
				},
				"artifact_prefix": {
					Type:        "string",
					Description: "Optional filename prefix (e.g., user:)",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "object",
			Description: "Run result with output files",
		},
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

	// Create workspace and stage the skill directory.
	ws, err := t.exec.CreateWorkspace(
		ctx, in.Skill, codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		return nil, err
	}
	defer t.exec.Cleanup(ctx, ws)

	if err := t.exec.PutSkill(ctx, ws, root, "."); err != nil {
		return nil, err
	}

	// Run through bash -lc "<command>" for free-form command string.
	timeout := time.Duration(in.Timeout) * time.Second
	rr, err := t.exec.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-lc", in.Command},
		Env:     in.Env,
		Cwd:     in.Cwd,
		Timeout: timeout,
	})
	if err != nil {
		return nil, err
	}

	// Collect output files if patterns provided.
	var files []codeexecutor.File
	if len(in.OutputFiles) > 0 {
		files, err = t.exec.Collect(ctx, ws, in.OutputFiles)
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

	// Optionally persist files as artifacts.
	if in.SaveAsArtifacts && len(files) > 0 {
		// Build callback context to access artifact service.
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
				Data:     []byte(f.Content),
				MimeType: f.MIMEType,
				Name:     name,
			})
			if err != nil {
				return nil, fmt.Errorf("save artifact %s: %w", name, err)
			}
			refs = append(refs, artifactRef{Name: name, Version: ver})
		}
		out.ArtifactFiles = refs
		if in.OmitInlineContent {
			// Keep metadata but drop inline contents.
			for i := range out.OutputFiles {
				out.OutputFiles[i].Content = ""
			}
		}
	}
	return out, nil
}

var _ tool.Tool = (*RunTool)(nil)
var _ tool.CallableTool = (*RunTool)(nil)
