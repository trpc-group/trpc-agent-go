//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a CodeExecutor that executes code blocks in the local environment.
// It supports Python and Bash scripts, executing them in the current local command line.
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
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// CodeExecutor that unsafely execute code in the current local command line.
type CodeExecutor struct {
	WorkDir        string        // Working directory for code execution
	Timeout        time.Duration // The timeout for the execution of any single code block
	CleanTempFiles bool          // Whether to clean temporary files after execution
	ws             *Runtime      // workspace runtime
}

// CodeExecutorOption defines a function type for configuring CodeExecutor
type CodeExecutorOption func(*CodeExecutor)

// WithWorkDir sets the working directory for code execution
func WithWorkDir(workDir string) CodeExecutorOption {
	return func(l *CodeExecutor) {
		l.WorkDir = workDir
	}
}

// WithTimeout sets the timeout for code execution
func WithTimeout(timeout time.Duration) CodeExecutorOption {
	return func(l *CodeExecutor) {
		l.Timeout = timeout
	}
}

// WithCleanTempFiles sets whether to clean temporary files after execution
func WithCleanTempFiles(clean bool) CodeExecutorOption {
	return func(l *CodeExecutor) {
		l.CleanTempFiles = clean
	}
}

// New creates a new CodeExecutor with the given options
func New(options ...CodeExecutorOption) *CodeExecutor {
	executor := &CodeExecutor{
		Timeout:        1 * time.Second,
		CleanTempFiles: true,
	}

	for _, option := range options {
		option(executor)
	}

	// Lazy init ws; create on first use.
	return executor
}

// ExecuteCode executes the code in the local environment and returns the result.
func (e *CodeExecutor) ExecuteCode(ctx context.Context, input codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	var output strings.Builder

	// Determine working directory
	var workDir string
	var shouldCleanup bool

	if e.WorkDir != "" {
		// Use specified working directory.
		workDir = e.WorkDir
		// Normalize relative paths to absolute.
		if !filepath.IsAbs(workDir) {
			if abs, err := filepath.Abs(workDir); err == nil {
				workDir = abs
			}
		}
		// Ensure the directory exists
		if err := os.MkdirAll(workDir, 0755); err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf("failed to create work directory: %w", err)
		}
		// Never cleanup user-specified work directories
		shouldCleanup = false
	} else {
		// Create a temporary directory for execution
		tempDir, err := os.MkdirTemp("", "codeexec_"+input.ExecutionID)
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf("failed to create temp directory: %w", err)
		}
		workDir = tempDir
		// Cleanup temp directory based on CleanTempFiles setting
		shouldCleanup = e.CleanTempFiles
	}

	if shouldCleanup {
		defer os.RemoveAll(workDir)
	}

	// Execute each code block
	for i, block := range input.CodeBlocks {
		blockOutput, err := e.executeCodeBlock(ctx, workDir, block, i)
		if err != nil {
			output.WriteString(fmt.Sprintf("Error executing code block %d: %v\n", i, err))
			continue
		}
		// Combine stdout and stderr into output
		if blockOutput != "" {
			output.WriteString(blockOutput)
		}
	}

	// CodeExecutor only outputs to Output, no output files
	return codeexecutor.CodeExecutionResult{
		Output:      output.String(),
		OutputFiles: []codeexecutor.File{}, // Empty slice, no output files
	}, nil
}

// executeCodeBlock executes a single code block based on its language
func (e *CodeExecutor) executeCodeBlock(ctx context.Context, workDir string, block codeexecutor.CodeBlock, blockIndex int) (output string, err error) {
	filePath, err := e.prepareCodeFile(workDir, block, blockIndex)
	if err != nil {
		return "", err
	}

	if e.CleanTempFiles {
		defer func() {
			if removeErr := os.Remove(filePath); removeErr != nil {
				log.Warnf("Failed to remove temp file %s: %v", filePath, removeErr)
			}
		}()
	}

	cmdArgs := e.buildCommandArgs(block.Language, filePath)
	if len(cmdArgs) == 0 {
		return "", fmt.Errorf("unsupported language: %s", block.Language)
	}

	return e.executeCommand(ctx, workDir, cmdArgs)
}

// prepareCodeFile prepares the file content, writes it to disk, and returns the file path
func (e *CodeExecutor) prepareCodeFile(workDir string, block codeexecutor.CodeBlock, blockIndex int) (filePath string, err error) {
	var filename, content string

	switch strings.ToLower(block.Language) {
	case "python", "py", "python3":
		filename = fmt.Sprintf("code_%d.py", blockIndex)
		content = block.Code
	case "go":
		filename = fmt.Sprintf("code_%d.go", blockIndex)
		content = fmt.Sprintf("package main\n\n%s", block.Code)
	case "bash", "sh":
		filename = fmt.Sprintf("code_%d.sh", blockIndex)
		content = block.Code
	default:
		return "", fmt.Errorf("unsupported language: %s", block.Language)
	}

	// Create full file path
	filePath = filepath.Join(workDir, filename)

	// Get appropriate file mode for the language
	fileMode := e.getFileMode(block.Language)

	// Write code file to disk
	if err := os.WriteFile(filePath, []byte(content), fileMode); err != nil {
		return "", fmt.Errorf("failed to write %s file: %w", block.Language, err)
	}

	return filePath, nil
}

// getFileMode returns the appropriate file mode for the language
func (e *CodeExecutor) getFileMode(language string) os.FileMode {
	switch strings.ToLower(language) {
	case "bash", "sh":
		return 0755 // Executable for shell scripts
	default:
		return 0644 // Regular file mode
	}
}

// buildCommandArgs returns the command arguments for executing the file
func (e *CodeExecutor) buildCommandArgs(language, filePath string) []string {
	switch strings.ToLower(language) {
	case "python", "py", "python3":
		return []string{"python3", filePath}
	case "bash", "sh":
		return []string{"bash", filePath}
	default:
		return nil
	}
}

// executeCommand executes the command with proper timeout and context handling
func (e *CodeExecutor) executeCommand(ctx context.Context, workDir string, cmdArgs []string) (string, error) {
	// Set timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()

	// Create command with timeout context
	cmd := exec.CommandContext(timeoutCtx, cmdArgs[0], cmdArgs[1:]...) //nolint:gosec
	cmd.Dir = workDir

	// Execute the command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command failed (cwd=%s, cmd=%s): %s: %w", workDir, strings.Join(cmdArgs, " "), string(output), err)
	}
	return string(output), nil
}

// CodeBlockDelimiter returns the code block delimiter used by the local executor.
func (e *CodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{
		Start: "```",
		End:   "```",
	}
}

// Workspace methods

func (e *CodeExecutor) ensureWS() *Runtime {
	if e.ws == nil {
		e.ws = NewRuntime("")
	}
	return e.ws
}

// CreateWorkspace implements the CodeExecutor interface.
func (e *CodeExecutor) CreateWorkspace(
	ctx context.Context, execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return e.ensureWS().CreateWorkspace(ctx, execID, pol)
}

// Cleanup implements the CodeExecutor interface.
func (e *CodeExecutor) Cleanup(
	ctx context.Context, ws codeexecutor.Workspace,
) error {
	return e.ensureWS().Cleanup(ctx, ws)
}

// PutFiles implements the CodeExecutor interface.
func (e *CodeExecutor) PutFiles(
	ctx context.Context, ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	return e.ensureWS().PutFiles(ctx, ws, files)
}

// PutDirectory implements the CodeExecutor interface.
func (e *CodeExecutor) PutDirectory(
	ctx context.Context, ws codeexecutor.Workspace,
	hostPath, to string,
) error {
	return e.ensureWS().PutDirectory(ctx, ws, hostPath, to)
}

// PutSkill implements the CodeExecutor interface.
func (e *CodeExecutor) PutSkill(
	ctx context.Context, ws codeexecutor.Workspace,
	skillRoot, to string,
) error {
	return e.ensureWS().PutSkill(ctx, ws, skillRoot, to)
}

// RunProgram implements the CodeExecutor interface.
func (e *CodeExecutor) RunProgram(
	ctx context.Context, ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	return e.ensureWS().RunProgram(ctx, ws, spec)
}

// Collect implements the CodeExecutor interface.
func (e *CodeExecutor) Collect(
	ctx context.Context, ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	return e.ensureWS().Collect(ctx, ws, patterns)
}

// ExecuteInline implements the CodeExecutor interface.
func (e *CodeExecutor) ExecuteInline(
	ctx context.Context, execID string,
	blocks []codeexecutor.CodeBlock,
	timeout time.Duration,
) (codeexecutor.RunResult, error) {
	return e.ensureWS().ExecuteInline(ctx, execID, blocks, timeout)
}
