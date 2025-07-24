package container

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

// CodeExecutor that executes code in a containerized environment.
type CodeExecutor struct {
	Timeout         time.Duration // The timeout for the execution of any single code block
	CleanContainers bool          // Whether to clean containers after execution
	WorkDir         string        // Host working directory to mount in container
	DockerImage     string        // Docker image to use for execution
}

// ExecutorOption defines a function type for configuring CodeExecutor
type ExecutorOption func(*CodeExecutor)

func WithDockerImage(image string) ExecutorOption {
	return func(c *CodeExecutor) {
		c.DockerImage = image
	}
}

// WithContainerTimeout sets the timeout for code execution
func WithContainerTimeout(timeout time.Duration) ExecutorOption {
	return func(c *CodeExecutor) {
		c.Timeout = timeout
	}
}

// WithCleanContainers sets whether to clean containers after execution
func WithCleanContainers(clean bool) ExecutorOption {
	return func(c *CodeExecutor) {
		c.CleanContainers = clean
	}
}

// WithContainerWorkDir sets the working directory for code execution
func WithContainerWorkDir(workDir string) ExecutorOption {
	return func(c *CodeExecutor) {
		c.WorkDir = workDir
	}
}

// New creates a new CodeExecutor with the given options
func New(options ...ExecutorOption) *CodeExecutor {
	executor := &CodeExecutor{
		Timeout:         60 * time.Second,
		CleanContainers: true,
		DockerImage:     "python:3-slim",
	}

	for _, option := range options {
		option(executor)
	}

	return executor
}

// ExecuteCode executes the code in a containerized environment and returns the result.
func (c *CodeExecutor) ExecuteCode(ctx context.Context, input codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	var output strings.Builder

	// Check if Docker is available
	if !c.isDockerAvailable() {
		return codeexecutor.CodeExecutionResult{}, fmt.Errorf("docker is not available or not running")
	}

	// Determine working directory
	var workDir string
	var shouldCleanup bool

	if c.WorkDir != "" {
		// Use specified working directory
		workDir = c.WorkDir
		// Ensure the directory exists
		if err := os.MkdirAll(workDir, 0755); err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf("failed to create work directory: %w", err)
		}
		shouldCleanup = false
	} else {
		// Create a temporary directory for execution
		// Use user's home directory for Docker volume mount compatibility (Colima/Docker Desktop)
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf("failed to get user home directory: %w", err)
		}
		tempDir, err := os.MkdirTemp(filepath.Join(homeDir, ".tmp"), "containerexec_"+input.ExecutionID)
		if err != nil {
			// Fallback to system temp if home temp creation fails
			tempDir, err = os.MkdirTemp("", "containerexec_"+input.ExecutionID)
			if err != nil {
				return codeexecutor.CodeExecutionResult{}, fmt.Errorf("failed to create temp directory: %w", err)
			}
		}
		workDir = tempDir
		shouldCleanup = true
	}

	if shouldCleanup {
		defer os.RemoveAll(workDir)
	}

	// Execute each code block
	for i, block := range input.CodeBlocks {
		blockOutput, err := c.executeCodeBlock(ctx, workDir, block, i)
		if err != nil {
			output.WriteString(fmt.Sprintf("Error executing code block %d: %v\n", i, err))
			continue
		}
		if blockOutput != "" {
			output.WriteString(blockOutput)
		}
	}

	return codeexecutor.CodeExecutionResult{
		Output:      output.String(),
		OutputFiles: []codeexecutor.File{}, // TODO: Implement output file extraction from containers
	}, nil
}

// isDockerAvailable checks if Docker is available and running
func (c *CodeExecutor) isDockerAvailable() bool {
	cmd := exec.Command("docker", "version")
	return cmd.Run() == nil
}

// executeCodeBlock executes a single code block in a container
func (c *CodeExecutor) executeCodeBlock(ctx context.Context, workDir string, block codeexecutor.CodeBlock, blockIndex int) (output string, err error) {
	// Prepare code file
	filePath, err := c.prepareCodeFile(workDir, block, blockIndex)
	if err != nil {
		return "", err
	}

	// Verify file was created (for debugging)
	if _, err := os.Stat(filePath); err != nil {
		return "", fmt.Errorf("code file was not created properly: %w", err)
	}

	// Get Docker image and command for the language
	dockerCmd, err := c.buildCommand(block.Language, filepath.Base(filePath))
	if err != nil {
		return "", err
	}

	// Execute in container
	return c.executeInContainer(ctx, workDir, dockerCmd)
}

// prepareCodeFile prepares the code file for container execution
func (c *CodeExecutor) prepareCodeFile(workDir string, block codeexecutor.CodeBlock, blockIndex int) (filePath string, err error) {
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
	case "node", "nodejs", "javascript", "js":
		filename = fmt.Sprintf("code_%d.js", blockIndex)
		content = block.Code
	default:
		return "", fmt.Errorf("unsupported language: %s", block.Language)
	}

	// Create full file path
	filePath = filepath.Join(workDir, filename)

	// Write code file to disk
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write %s file: %w", block.Language, err)
	}

	return filePath, nil
}

// buildCommand builds command for the language
func (c *CodeExecutor) buildCommand(language, filename string) ([]string, error) {
	switch strings.ToLower(language) {
	case "python", "py", "python3":
		return []string{"python", filename}, nil
	case "go":
		return []string{"go", "run", filename}, nil
	case "bash", "sh":
		return []string{"sh", filename}, nil
	default:
		return nil, fmt.Errorf("unsupported language: %s", language)
	}
}

// executeInContainer executes the command in a Docker container
func (c *CodeExecutor) executeInContainer(ctx context.Context, workDir string, cmdArgs []string) (string, error) {
	// Set timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	// Build Docker command
	dockerArgs := []string{
		"run",
		"--rm",                                      // Remove container after execution
		"-v", fmt.Sprintf("%s:/workspace", workDir), // Mount work directory
		"-w", "/workspace", // Set working directory in container
		"--network", "none", // Disable network access for security
		"--memory", "256m", // Limit memory usage
		"--cpus", "1.0", // Limit CPU usage
	}

	// Don't add container name when using --rm, as it causes conflicts
	dockerArgs = append(dockerArgs, c.DockerImage)
	dockerArgs = append(dockerArgs, cmdArgs...)

	// Create and execute Docker command
	cmd := exec.CommandContext(timeoutCtx, "docker", dockerArgs...)

	// Capture both stdout and stderr
	output, err := cmd.CombinedOutput()

	// No need for cleanup goroutine since we're using --rm

	if err != nil {
		// Include both error and output for better debugging
		if len(output) > 0 {
			return string(output), fmt.Errorf("container execution failed with output: %s, error: %w", string(output), err)
		}
		return string(output), fmt.Errorf("container execution failed: %w", err)
	}

	return string(output), nil
}

// CodeBlockDelimiter returns the code block delimiter used by the container executor.
func (c *CodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{
		Start: "```",
		End:   "```",
	}
}
