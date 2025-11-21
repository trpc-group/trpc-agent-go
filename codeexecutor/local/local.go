//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
}

// CodeExecutorOption configures CodeExecutor.
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

// WithCleanTempFiles toggles cleanup of temporary files.
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

// WithCodeBlockDelimiter sets the code block delimiter.
func WithCodeBlockDelimiter(delimiter codeexecutor.CodeBlockDelimiter) CodeExecutorOption {
	return func(l *CodeExecutor) { l.codeBlockDelimiter = delimiter }
}

var defaultCodeBlockDelimiter = codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}

// New creates a local CodeExecutor.
func New(options ...CodeExecutorOption) *CodeExecutor {
	executor := &CodeExecutor{
		Timeout:            1 * time.Second,
		CleanTempFiles:     true,
		codeBlockDelimiter: defaultCodeBlockDelimiter,
		autoInputs:         true,
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

	// Determine working directory
	var workDir string
	var shouldCleanup bool

	if e.WorkDir != "" {
		workDir = e.WorkDir
		if !filepath.IsAbs(workDir) {
			if abs, err := filepath.Abs(workDir); err == nil {
				workDir = abs
			}
		}
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf(
				"failed to create work directory: %w", err,
			)
		}
		shouldCleanup = false
	} else {
		tempDir, err := os.MkdirTemp("", "codeexec_"+input.ExecutionID)
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf(
				"failed to create temp directory: %w", err,
			)
		}
		workDir = tempDir
		shouldCleanup = e.CleanTempFiles
	}

	if shouldCleanup {
		defer os.RemoveAll(workDir)
	}

	for i, block := range input.CodeBlocks {
		blockOutput, err := e.executeCodeBlock(ctx, workDir, block, i)
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
	ctx context.Context, workDir string,
	block codeexecutor.CodeBlock, blockIndex int,
) (string, error) {
	filePath, err := e.prepareCodeFile(workDir, block, blockIndex)
	if err != nil {
		return "", err
	}
	cmdArgs := e.buildCommandArgs(block.Language, filePath)
	if len(cmdArgs) == 0 {
		return "", fmt.Errorf("unsupported language: %s", block.Language)
	}
	return e.executeCommand(ctx, workDir, cmdArgs)
}

// prepareCodeFile writes code to a temporary file.
func (e *CodeExecutor) prepareCodeFile(
	workDir string, block codeexecutor.CodeBlock, blockIndex int,
) (string, error) {
	ext := ""
	switch strings.ToLower(block.Language) {
	case "python", "py", "python3":
		ext = ".py"
	case "bash", "sh":
		ext = ".sh"
	default:
		return "", fmt.Errorf("unsupported language: %s", block.Language)
	}
	fileName := fmt.Sprintf("code_%d%s", blockIndex, ext)
	filePath := filepath.Join(workDir, fileName)
	content := strings.TrimSpace(block.Code)
	if strings.EqualFold(block.Language, "python") ||
		strings.EqualFold(block.Language, "py") ||
		strings.EqualFold(block.Language, "python3") {
		if !strings.Contains(content, "print(") &&
			!strings.Contains(content, "sys.stdout.write(") {
			content = content + "\n"
		}
	}
	fileMode := e.getFileMode(block.Language)
	if err := os.WriteFile(filePath, []byte(content), fileMode); err != nil {
		return "", fmt.Errorf("failed to write %s file: %w",
			block.Language, err)
	}
	return filePath, nil
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

// CodeBlockDelimiter returns the code block delimiter used by the local executor.
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
		opts = append(opts, WithAutoInputs(e.autoInputs))
		e.ws = NewRuntimeWithOptions("", opts...)
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
