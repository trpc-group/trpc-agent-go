//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package codeexecutor defines workspace types and helpers.
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

// StageOptions controls directory staging behavior.
type StageOptions struct {
	// ReadOnly makes the staged tree non-writable after copy/mount.
	ReadOnly bool
	// AllowMount lets implementations use read-only mounts when possible.
	AllowMount bool
}

// WorkspaceManager handles workspace lifecycle.
type WorkspaceManager interface {
	CreateWorkspace(ctx context.Context, execID string,
		pol WorkspacePolicy) (Workspace, error)
	Cleanup(ctx context.Context, ws Workspace) error
}

// WorkspaceFS performs file operations within a workspace.
type WorkspaceFS interface {
	PutFiles(ctx context.Context, ws Workspace,
		files []PutFile) error
	StageDirectory(ctx context.Context, ws Workspace,
		src, to string, opt StageOptions) error
	Collect(ctx context.Context, ws Workspace,
		patterns []string) ([]File, error)
}

// ProgramRunner executes programs within a workspace.
type ProgramRunner interface {
	RunProgram(ctx context.Context, ws Workspace,
		spec RunProgramSpec) (RunResult, error)
}

// Capabilities describes engine capabilities for selection.
type Capabilities struct {
	Isolation      string
	NetworkAllowed bool
	ReadOnlyMount  bool
	Streaming      bool
	MaxDiskBytes   int64
}

// Engine is a backend that provides workspace and execution services.
type Engine interface {
	Manager() WorkspaceManager
	FS() WorkspaceFS
	Runner() ProgramRunner
	// Describe returns optional capabilities.
	Describe() Capabilities
}

// EngineProvider is an optional interface that a CodeExecutor may
// implement to expose its underlying engine for skill tools.
type EngineProvider interface {
	Engine() Engine
}

type stdEngine struct {
	m WorkspaceManager
	f WorkspaceFS
	r ProgramRunner
	c Capabilities
}

func (e *stdEngine) Manager() WorkspaceManager { return e.m }
func (e *stdEngine) FS() WorkspaceFS           { return e.f }
func (e *stdEngine) Runner() ProgramRunner     { return e.r }
func (e *stdEngine) Describe() Capabilities    { return e.c }

// NewEngine constructs a simple Engine from its components.
func NewEngine(m WorkspaceManager, f WorkspaceFS,
	r ProgramRunner) Engine {
	return &stdEngine{m: m, f: f, r: r}
}

// Default file modes and common subdirectories.
const (
	// DefaultScriptFileMode is the default POSIX mode for text scripts.
	DefaultScriptFileMode = 0o644
	// DefaultExecFileMode is the default POSIX mode for executables.
	DefaultExecFileMode = 0o755
	// InlineSourceDir is the subdirectory where inline code blocks
	// are written and executed as the current working directory.
	InlineSourceDir = "src"
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
