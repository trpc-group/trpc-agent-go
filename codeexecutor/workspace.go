//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	// StageInputs maps external inputs into workspace according to
	// the provided specs. Implementations should prefer link
	// strategies when Mode=="link" and environment allows it.
	StageInputs(ctx context.Context, ws Workspace,
		specs []InputSpec) error
	// CollectOutputs applies the declarative output spec to collect
	// files and optionally persist artifacts.
	CollectOutputs(ctx context.Context, ws Workspace,
		spec OutputSpec) (OutputManifest, error)
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
func NewEngine(
	m WorkspaceManager,
	f WorkspaceFS,
	r ProgramRunner,
) Engine {
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

	// normalizeGlobsVarPrefix and related constants avoid repeating
	// common prefixes when normalizing glob patterns.
	normalizeGlobsVarPrefix    = "$"
	normalizeGlobsVarLBrace    = "${"
	normalizeGlobsVarRBrace    = "}"
	normalizeGlobsSlash        = "/"
	normalizeGlobsBackslash    = "\\"
	normalizeGlobsWorkspace    = "WORKSPACE_DIR"
	normalizeGlobsSkills       = "SKILLS_DIR"
	normalizeGlobsWork         = "WORK_DIR"
	normalizeGlobsOut          = "OUTPUT_DIR"
	normalizeGlobsWorkspaceDir = "."
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

// NormalizeGlobs rewrites glob patterns that use well-known
// environment-style prefixes such as $OUTPUT_DIR into workspace-
// relative paths like out/. It understands the variables injected by
// workspace runtimes: WORKSPACE_DIR, SKILLS_DIR, WORK_DIR, OUTPUT_DIR.
//
// Examples:
//
//	$OUTPUT_DIR/a.txt   -> out/a.txt
//	${WORK_DIR}/x/**    -> work/x/**
//	$WORKSPACE_DIR/out  -> out
//
// Unknown variables and patterns without a prefix are returned as-is.
func NormalizeGlobs(patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		s = normalizeGlobPrefix(
			s, normalizeGlobsWorkspace, normalizeGlobsWorkspaceDir,
		)
		s = normalizeGlobPrefix(s, normalizeGlobsSkills, DirSkills)
		s = normalizeGlobPrefix(s, normalizeGlobsWork, DirWork)
		s = normalizeGlobPrefix(s, normalizeGlobsOut, DirOut)
		out = append(out, s)
	}
	return out
}

func normalizeGlobPrefix(s string, name string, dir string) string {
	if strings.HasPrefix(s, normalizeGlobsVarLBrace+name+
		normalizeGlobsVarRBrace) {
		return normalizeGlobTail(
			s[len(normalizeGlobsVarLBrace+name+
				normalizeGlobsVarRBrace):],
			dir,
		)
	}
	if strings.HasPrefix(s, normalizeGlobsVarPrefix+name) {
		return normalizeGlobTail(
			s[len(normalizeGlobsVarPrefix+name):],
			dir,
		)
	}
	return s
}

func normalizeGlobTail(tail string, dir string) string {
	if tail == "" {
		if dir == normalizeGlobsWorkspaceDir {
			return normalizeGlobsWorkspaceDir
		}
		return dir
	}
	r := trimGlobSeparator(tail)
	if dir == normalizeGlobsWorkspaceDir {
		if r == "" {
			return normalizeGlobsWorkspaceDir
		}
		return r
	}
	if r == "" {
		return dir
	}
	return dir + normalizeGlobsSlash + r
}

func trimGlobSeparator(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, normalizeGlobsSlash) ||
		strings.HasPrefix(s, normalizeGlobsBackslash) {
		return s[1:]
	}
	return s
}
