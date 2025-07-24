package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// CodeExecutor that executes code in a containerized environment.
type CodeExecutor struct {
	Timeout       time.Duration // The timeout for the execution of any single code block
	WorkDir       string        // Host working directory to mount in container
	BindDir       string        // The directory that will be bound to the code executor container.
	DockerImage   string        // Docker image to use for execution
	AutoRemove    bool          // If true, will automatically remove the Docker container when it is stopped.
	ContainerName string        // Name of the Docker container which is created. If empty, will autogenerate a name.
	StopContainer bool          // If true, will automatically stop the container when stop is called.
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

// WithWorkDir sets the working directory for code execution
func WithWorkDir(workDir string) ExecutorOption {
	return func(c *CodeExecutor) {
		c.WorkDir = workDir
	}
}

// WithBindDir sets the bind directory for container volume mounting
// Useful for cases where you want to spawn the container from within a container.
func WithBindDir(bindDir string) ExecutorOption {
	return func(c *CodeExecutor) {
		c.BindDir = bindDir
	}
}

// WithAutoRemove sets whether to automatically remove containers after execution
func WithAutoRemove(autoRemove bool) ExecutorOption {
	return func(c *CodeExecutor) {
		c.AutoRemove = autoRemove
	}
}

// WithContainerName sets the name of the Docker container
func WithContainerName(name string) ExecutorOption {
	return func(c *CodeExecutor) {
		c.ContainerName = name
	}
}

// WithStopContainer sets whether to automatically stop containers when stop is called
func WithStopContainer(stopContainer bool) ExecutorOption {
	return func(c *CodeExecutor) {
		c.StopContainer = stopContainer
	}
}

const (
	defaultDockerImage         = "python:3-slim"
	defaultTimeout             = 60 * time.Second
	defaultContainerNamePrefix = "trpc.go.agent-code-exec-"
	defaultContainerWorkingDir = "/workspace"
)

// New creates a new CodeExecutor with the given options
func New(options ...ExecutorOption) *CodeExecutor {
	executor := &CodeExecutor{
		Timeout:       defaultTimeout,
		DockerImage:   defaultDockerImage,
		AutoRemove:    true,
		StopContainer: true,
	}

	for _, option := range options {
		option(executor)
	}

	executor.start(context.Background())

	return executor
}

func (c *CodeExecutor) start(ctx context.Context) error {
	// Check if Docker is available
	if !c.isDockerAvailable() {
		return fmt.Errorf("docker is not available or not running")
	}

	// Initialize working directory if not provided
	if c.WorkDir == "" {
		// Create a temporary directory for execution
		// Use user's home directory for Docker volume mount compatibility (Colima/Docker Desktop)
		homeDir, _ := os.UserHomeDir()
		if homeDir != "" {
			tempDir, err := os.MkdirTemp(filepath.Join(homeDir, ".tmp"), fmt.Sprintf("containerexec_%s_", uuid.New().String()[:8]))
			if err == nil {
				c.WorkDir = tempDir
			}
		}
		// Fallback to system temp if home temp creation fails
		if c.WorkDir == "" {
			tempDir, err := os.MkdirTemp("", fmt.Sprintf("containerexec_%s_", uuid.New().String()[:8]))
			if err == nil {
				c.WorkDir = tempDir
			}
		}
	} else {
		// Ensure the specified directory exists
		os.MkdirAll(c.WorkDir, 0755)
	}

	// Set up bind directory if not provided
	if c.BindDir == "" {
		c.BindDir = c.WorkDir
	}

	// Generate container name if not provided
	if c.ContainerName == "" {
		c.ContainerName = generateContainerName()
	}

	return nil
}

// Stop cleans up resources and stops the code executor.
// If a temporary working directory was created, it will be removed.
// If StopContainer is true, will stop the Docker container (but not remove it).
func (c *CodeExecutor) Stop() error {
	var lastErr error

	// Stop container if requested and not using AutoRemove
	if c.StopContainer && !c.AutoRemove && c.ContainerName != "" {
		// Try to stop the container
		stopCmd := exec.Command("docker", "stop", c.ContainerName)
		if err := stopCmd.Run(); err != nil {
			// Don't fail if container is already stopped or doesn't exist
			// Just record the error and continue
			lastErr = fmt.Errorf("failed to stop container %s: %w", c.ContainerName, err)
		}
	}

	// Clean up temporary working directory if it was auto-created
	if c.WorkDir != "" && strings.Contains(c.WorkDir, "containerexec_") {
		if err := os.RemoveAll(c.WorkDir); err != nil {
			if lastErr != nil {
				lastErr = fmt.Errorf("%v; failed to cleanup work directory: %w", lastErr, err)
			} else {
				lastErr = fmt.Errorf("failed to cleanup work directory: %w", err)
			}
		}
		c.WorkDir = ""
	}

	return lastErr
}

// Close is an alias for Stop to implement io.Closer interface if needed
func (c *CodeExecutor) Close() error {
	return c.Stop()
}

// generateContainerName generates a unique container name
func generateContainerName() string {
	return fmt.Sprintf("%s%s", defaultContainerNamePrefix, uuid.New().String())
}

// ExecuteCode executes the code in a containerized environment and returns the result.
func (c *CodeExecutor) ExecuteCode(ctx context.Context, input codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	var output strings.Builder

	// Check if Docker is available
	if !c.isDockerAvailable() {
		return codeexecutor.CodeExecutionResult{}, fmt.Errorf("docker is not available or not running")
	}

	// Execute each code block
	for i, block := range input.CodeBlocks {
		blockOutput, err := c.executeCodeBlock(ctx, c.WorkDir, block, i)
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

	// Determine bind directory - if not specified, use workDir
	bindDir := c.BindDir
	if bindDir == "" {
		bindDir = workDir
	}

	// Build Docker command
	dockerArgs := []string{
		"run",
	}

	// Add --rm flag based on AutoRemove setting
	if c.AutoRemove {
		dockerArgs = append(dockerArgs, "--rm") // Remove container after execution
	}

	dockerArgs = append(dockerArgs,
		"-v", fmt.Sprintf("%s:%s", bindDir, defaultContainerWorkingDir), // Mount bind directory to workspace
		"-w", defaultContainerWorkingDir, // Set working directory in container
		"--network", "none", // Disable network access for security
		"--memory", "256m", // Limit memory usage
		"--cpus", "1.0", // Limit CPU usage
		"--name", c.ContainerName, // Use specified container name or generate one
	)

	// Don't add container name when using --rm, as it causes conflicts
	dockerArgs = append(dockerArgs, c.DockerImage)
	dockerArgs = append(dockerArgs, cmdArgs...)

	// Create and execute Docker command
	cmd := exec.CommandContext(timeoutCtx, "docker", dockerArgs...)

	// Capture both stdout and stderr
	output, err := cmd.CombinedOutput()

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
