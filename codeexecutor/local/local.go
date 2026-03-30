//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a CodeExecutor that executes code blocks in the
// local environment. It supports Python and Bash scripts by writing them
// to files and invoking the appropriate interpreter.
package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// CodeExecutor that executes code on the local host (unsafe).
type CodeExecutor struct {
	WorkDir            string
	Timeout            time.Duration
	CleanTempFiles     bool
	codeBlockDelimiter codeexecutor.CodeBlockDelimiter
	ws                 *Runtime
	inputsHostBase     string
	autoInputs         bool
	workspaceMode      WorkspaceMode
}

// CodeExecutorOption configures a local CodeExecutor.
type CodeExecutorOption func(*CodeExecutor)

// WithWorkDir sets the working directory used for execution.
func WithWorkDir(workDir string) CodeExecutorOption {
	return func(l *CodeExecutor) { l.WorkDir = workDir }
}

// WithTimeout sets the per-command timeout.
func WithTimeout(timeout time.Duration) CodeExecutorOption {
	return func(l *CodeExecutor) { l.Timeout = timeout }
}

// WithCleanTempFiles toggles cleanup of temporary helper files.
func WithCleanTempFiles(clean bool) CodeExecutorOption {
	return func(l *CodeExecutor) { l.CleanTempFiles = clean }
}

// WithWorkspaceInputsHostBase sets the host inputs directory that
// will be exposed under work/inputs when auto inputs are enabled.
func WithWorkspaceInputsHostBase(host string) CodeExecutorOption {
	return func(l *CodeExecutor) { l.inputsHostBase = host }
}

// WithWorkspaceAutoInputs enables or disables automatic mapping of
// the host inputs directory (when configured) into work/inputs for
// each workspace.
func WithWorkspaceAutoInputs(enable bool) CodeExecutorOption {
	return func(l *CodeExecutor) { l.autoInputs = enable }
}

// WithWorkspaceMode configures how local workspaces are created.
//
// The default is WorkspaceModeIsolated, which creates a unique workspace per
// run. WorkspaceModeTrustedLocal reuses WorkDir as the workspace root.
func WithWorkspaceMode(mode WorkspaceMode) CodeExecutorOption {
	return func(l *CodeExecutor) { l.workspaceMode = mode }
}

// WithCodeBlockDelimiter sets the code block delimiter.
func WithCodeBlockDelimiter(
	delimiter codeexecutor.CodeBlockDelimiter,
) CodeExecutorOption {
	return func(l *CodeExecutor) { l.codeBlockDelimiter = delimiter }
}

var defaultCodeBlockDelimiter = codeexecutor.CodeBlockDelimiter{
	Start: "```",
	End:   "```",
}

const (
	codeFilePatternBase = "code_*"
	pythonFileExt       = ".py"
	shellFileExt        = ".sh"
	prepareFileErrFmt   = "failed to prepare %s file: %w"
)

// New creates a local CodeExecutor.
func New(options ...CodeExecutorOption) *CodeExecutor {
	executor := &CodeExecutor{
		Timeout:            1 * time.Second,
		CleanTempFiles:     true,
		codeBlockDelimiter: defaultCodeBlockDelimiter,
		autoInputs:         true,
		workspaceMode:      WorkspaceModeIsolated,
	}
	for _, option := range options {
		option(executor)
	}
	return executor
}

// ExecuteCode executes code blocks and returns combined output.
func (e *CodeExecutor) ExecuteCode(
	ctx context.Context, input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	var output strings.Builder

	// Determine working directory for the command CWD and a separate
	// script directory for writing intermediate script files.
	// When WorkDir is set, we create a unique temp subdirectory inside it
	// for script files to avoid collisions from concurrent ExecuteCode calls
	// (e.g. multiple calls all writing to code_0.sh).
	var cmdDir string    // CWD for the executed command
	var scriptDir string // directory where script files are written
	var shouldCleanup bool

	if e.WorkDir != "" {
		cmdDir = e.WorkDir
		if !filepath.IsAbs(cmdDir) {
			if abs, err := filepath.Abs(cmdDir); err == nil {
				cmdDir = abs
			}
		}
		if err := os.MkdirAll(cmdDir, 0o755); err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf(
				"failed to create work directory: %w", err,
			)
		}
		if e.CleanTempFiles {
			// Create a unique temp subdirectory for script files to prevent
			// concurrent calls from overwriting each other's code_0.sh.
			tmpDir, err := os.MkdirTemp(cmdDir, ".exec_")
			if err != nil {
				// Fall back to writing scripts directly into WorkDir.
				// Per-block errors will surface via result.Output.
				scriptDir = cmdDir
			} else {
				scriptDir = tmpDir
				// Clean up the temp script directory after execution.
				defer os.RemoveAll(scriptDir)
			}
		} else {
			// When CleanTempFiles is false, write scripts directly into
			// WorkDir so they can be inspected after execution.
			scriptDir = cmdDir
		}
	} else {
		tempDir, err := os.MkdirTemp("", "codeexec_"+input.ExecutionID)
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf(
				"failed to create temp directory: %w", err,
			)
		}
		cmdDir = tempDir
		scriptDir = tempDir
		shouldCleanup = e.CleanTempFiles
	}

	if shouldCleanup {
		defer os.RemoveAll(cmdDir)
	}

	for i, block := range input.CodeBlocks {
		blockOutput, err := e.executeCodeBlock(ctx, cmdDir, scriptDir, block)
		if err != nil {
			output.WriteString(fmt.Sprintf(
				"Error executing code block %d: %v\n", i, err,
			))
			continue
		}
		if blockOutput != "" {
			output.WriteString(blockOutput)
		}
	}

	return codeexecutor.CodeExecutionResult{
		Output:      output.String(),
		OutputFiles: []codeexecutor.File{},
	}, nil
}

func (e *CodeExecutor) executeCodeBlock(
	ctx context.Context, cmdDir, scriptDir string,
	block codeexecutor.CodeBlock,
) (string, error) {
	filePath, err := e.prepareCodeFile(scriptDir, block)
	if err != nil {
		return "", err
	}
	cmdArgs := e.buildCommandArgs(block.Language, filePath)
	return e.executeCommand(ctx, cmdDir, cmdArgs)
}

// prepareCodeFile writes code to a temporary helper file.
func (e *CodeExecutor) prepareCodeFile(
	workDir string, block codeexecutor.CodeBlock,
) (filePath string, err error) {
	ext, err := helperFileExtension(block.Language)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(block.Code)
	if strings.EqualFold(block.Language, "python") ||
		strings.EqualFold(block.Language, "py") ||
		strings.EqualFold(block.Language, "python3") {
		if !strings.Contains(content, "print(") &&
			!strings.Contains(content, "sys.stdout.write(") {
			content = content + "\n"
		}
	}
	helperFile, err := os.CreateTemp(
		workDir, codeFilePatternBase+ext,
	)
	if err != nil {
		return "", fmt.Errorf(
			"failed to create %s file: %w", block.Language, err,
		)
	}
	filePath = helperFile.Name()
	_ = helperFile.Close()
	fileMode := e.getFileMode(block.Language)
	if err = writeHelperFile(filePath, content, fileMode); err != nil {
		return "", fmt.Errorf(prepareFileErrFmt, block.Language, err)
	}
	return filePath, nil
}

func writeHelperFile(
	filePath, content string,
	fileMode os.FileMode,
) error {
	if err := os.WriteFile(filePath, []byte(content), fileMode); err != nil {
		return err
	}
	return os.Chmod(filePath, fileMode)
}

func helperFileExtension(language string) (string, error) {
	switch strings.ToLower(language) {
	case "python", "py", "python3":
		return pythonFileExt, nil
	case "bash", "sh":
		return shellFileExt, nil
	default:
		return "", fmt.Errorf("unsupported language: %s", language)
	}
}

func (e *CodeExecutor) getFileMode(language string) os.FileMode {
	switch strings.ToLower(language) {
	case "bash", "sh":
		return 0o755
	default:
		return 0o644
	}
}

func (e *CodeExecutor) buildCommandArgs(
	language, filePath string,
) []string {
	switch strings.ToLower(language) {
	case "python", "py", "python3":
		return []string{"python3", filePath}
	case "bash", "sh":
		return []string{"bash", filePath}
	default:
		return nil
	}
}

func (e *CodeExecutor) executeCommand(
	ctx context.Context, workDir string, cmdArgs []string,
) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	// #nosec G204 — interpreter and path are controlled by us
	cmd := exec.CommandContext(
		timeoutCtx, cmdArgs[0], cmdArgs[1:]...,
	)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"command failed (cwd=%s, cmd=%s): %s: %w",
			workDir, strings.Join(cmdArgs, " "), string(output), err,
		)
	}
	return string(output), nil
}

func removeHelperFile(path string) {
	_ = os.Remove(path)
}

// CodeBlockDelimiter returns the code block delimiter used by the local
// executor.
func (e *CodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return e.codeBlockDelimiter
}

// Workspace methods

// CreateWorkspace creates a new workspace directory.
func (e *CodeExecutor) ensureWS() *Runtime {
	if e.ws == nil {
		var opts []RuntimeOption
		if e.inputsHostBase != "" {
			opts = append(
				opts, WithInputsHostBase(e.inputsHostBase),
			)
		}
		opts = append(opts, WithRuntimeWorkspaceMode(e.workspaceMode))
		opts = append(opts, WithAutoInputs(e.autoInputs))
		workRoot := strings.TrimSpace(e.WorkDir)
		if workRoot != "" && !filepath.IsAbs(workRoot) {
			if abs, err := filepath.Abs(workRoot); err == nil {
				workRoot = abs
			}
		}
		e.ws = NewRuntimeWithOptions(workRoot, opts...)
	}
	return e.ws
}

// CreateWorkspace delegates to the local workspace runtime.
func (e *CodeExecutor) CreateWorkspace(
	ctx context.Context, execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return e.ensureWS().CreateWorkspace(ctx, execID, pol)
}

// Cleanup delegates to the local workspace runtime.
func (e *CodeExecutor) Cleanup(
	ctx context.Context, ws codeexecutor.Workspace,
) error {
	return e.ensureWS().Cleanup(ctx, ws)
}

// PutFiles delegates to the local workspace runtime.
func (e *CodeExecutor) PutFiles(
	ctx context.Context, ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	return e.ensureWS().PutFiles(ctx, ws, files)
}

// PutDirectory delegates to the local workspace runtime.
func (e *CodeExecutor) PutDirectory(
	ctx context.Context, ws codeexecutor.Workspace,
	hostPath, to string,
) error {
	return e.ensureWS().PutDirectory(ctx, ws, hostPath, to)
}

// RunProgram delegates to the local workspace runtime.
func (e *CodeExecutor) RunProgram(
	ctx context.Context, ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	return e.ensureWS().RunProgram(ctx, ws, spec)
}

// Collect delegates to the local workspace runtime.
func (e *CodeExecutor) Collect(
	ctx context.Context, ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	return e.ensureWS().Collect(ctx, ws, patterns)
}

// ExecuteInline delegates to the local workspace runtime.
func (e *CodeExecutor) ExecuteInline(
	ctx context.Context, execID string,
	blocks []codeexecutor.CodeBlock,
	timeout time.Duration,
) (codeexecutor.RunResult, error) {
	return e.ensureWS().ExecuteInline(ctx, execID, blocks, timeout)
}

// Engine exposes the local runtime as an Engine for skills.
func (e *CodeExecutor) Engine() codeexecutor.Engine {
	rt := e.ensureWS()
	return codeexecutor.NewEngine(rt, rt, rt)
}
