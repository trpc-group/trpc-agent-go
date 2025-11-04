//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package codeexecutor adds a higher level execution interface that
// supports workspaces, staging files, running programs, and collecting
// output files. It coexists with the original CodeExecutor for backward
// compatibility.
package codeexecutor

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Well-known environment and telemetry keys to avoid magic strings.
const (
	// WorkspaceEnvDirKey is set in program env to point to the workspace
	// directory for outputs and scratch files.
	WorkspaceEnvDirKey = "WORKSPACE_DIR"

	// Span names for workspace lifecycle.
	SpanWorkspaceCreate     = "workspace.create"
	SpanWorkspaceCleanup    = "workspace.cleanup"
	SpanWorkspaceStageFiles = "workspace.stage.files"
	SpanWorkspaceStageDir   = "workspace.stage.dir"
	SpanWorkspaceStageSkill = "workspace.stage.skill"
	SpanWorkspaceRun        = "workspace.run"
	SpanWorkspaceCollect    = "workspace.collect"
	SpanWorkspaceInline     = "workspace.inline"

	// Common attribute keys used in tracing spans.
	AttrExecID    = "exec_id"
	AttrPath      = "path"
	AttrCount     = "count"
	AttrPatterns  = "patterns"
	AttrCmd       = "cmd"
	AttrCwd       = "cwd"
	AttrExitCode  = "exit_code"
	AttrTimedOut  = "timed_out"
	AttrHostPath  = "host_path"
	AttrTo        = "to"
	AttrRoot      = "root"
	AttrMountUsed = "mount_used"
)

// Workspace represents an isolated execution workspace.
// Path is host path for local runtime or a logical mount path for
// containers.
type Workspace struct {
	ID   string
	Path string
}

// WorkspacePolicy configures workspace behavior.
type WorkspacePolicy struct {
	// Isolated toggles runtime isolation (e.g., container usage).
	Isolated bool
	// Persist keeps workspace after Cleanup when true.
	Persist bool
	// MaxDiskBytes is a soft limit for staged and produced files.
	MaxDiskBytes int64
}

// PutFile describes a file to place into a workspace.
type PutFile struct {
	Path    string // relative to workspace root
	Content []byte
	Mode    uint32 // POSIX mode bits (e.g., 0644, 0755)
}

// ResourceLimits restrict program execution resources.
type ResourceLimits struct {
	// CPUPercent is an approximate percentage of one CPU.
	CPUPercent int
	// MemoryMB is a soft limit in megabytes.
	MemoryMB int
	// MaxPIDs limits number of processes/threads.
	MaxPIDs int
}

// RunProgramSpec describes a program invocation in a workspace.
type RunProgramSpec struct {
	Cmd     string
	Args    []string
	Env     map[string]string
	Cwd     string // relative to workspace root
	Stdin   string
	Timeout time.Duration
	Limits  ResourceLimits
}

// RunResult captures a single program run result.
type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	TimedOut bool
}

// WorkspaceExecutor is a higher-level execution interface. Implementations
// may
// be backed by local processes or containers. Skills should depend on
// this interface and avoid concrete runtime details.
type WorkspaceExecutor interface {
	// CreateWorkspace creates a new workspace for an execution ID.
	CreateWorkspace(
		ctx context.Context,
		execID string,
		pol WorkspacePolicy,
	) (Workspace, error)

	// Cleanup removes a workspace unless pol.Persist is true.
	Cleanup(ctx context.Context, ws Workspace) error

	// PutFiles writes file blobs under the workspace root.
	PutFiles(ctx context.Context, ws Workspace, files []PutFile) error

	// PutDirectory copies a host directory into the workspace at "to".
	PutDirectory(
		ctx context.Context,
		ws Workspace,
		hostPath string,
		to string,
	) error

	// PutSkill copies a skill root into the workspace at "to".
	PutSkill(
		ctx context.Context,
		ws Workspace,
		skillRoot string,
		to string,
	) error

	// RunProgram runs the given command spec inside the workspace.
	RunProgram(
		ctx context.Context,
		ws Workspace,
		spec RunProgramSpec,
	) (RunResult, error)

	// Collect returns output files matched by glob patterns relative to
	// the workspace root.
	Collect(
		ctx context.Context,
		ws Workspace,
		patterns []string,
	) ([]File, error)

	// ExecuteInline executes inline code blocks by writing temp files
	// and invoking suitable interpreters via RunProgram.
	ExecuteInline(
		ctx context.Context,
		execID string,
		blocks []CodeBlock,
		timeout time.Duration,
	) (RunResult, error)
}

// Default file modes for generated code files.
const (
	// DefaultScriptFileMode is the default POSIX mode for text scripts.
	DefaultScriptFileMode = 0o644
	// DefaultExecFileMode is the default POSIX mode for executables.
	DefaultExecFileMode = 0o755
)

// BuildBlockSpec maps a code block into a file name, mode, command,
// and arguments suitable for execution via RunProgram. It supports a
// minimal set of languages to keep behavior predictable.
func BuildBlockSpec(
	idx int,
	b CodeBlock,
) (file string, mode uint32, cmd string, args []string, err error) {
	lang := strings.ToLower(strings.TrimSpace(b.Language))
	switch lang {
	case "python", "py", "python3":
		return fmt.Sprintf("code_%d.py", idx), DefaultScriptFileMode,
			"python3", nil, nil
	case "bash", "sh":
		return fmt.Sprintf("code_%d.sh", idx), DefaultExecFileMode,
			"bash", nil, nil
	default:
		return "", 0, "", nil,
			fmt.Errorf("unsupported language: %s", b.Language)
	}
}
