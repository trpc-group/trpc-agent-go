// Package container provides a CodeExecutor that executes code blocks in a Docker container.
// It supports Python and Bash scripts, executing them in a controlled Docker environment.
package container

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"
	archive "github.com/moby/go-archive"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	// defaultImageTag is the default Docker image tag for code execution
	defaultImageTag            = "trpc-agent-go-code-executor:latest"
	defaultContainerWorkingDir = "/workspace"
)

// CodeExecutor executes code using a Docker container
type CodeExecutor struct {
	host            string               // Optional base URL of the user hosted Docker client, default client.DefaultDockerHost
	dockerFilePath  string               // Path to directory containing Dockerfile
	client          *client.Client       // Docker client
	container       *container.Summary   // Running container instance
	hostConfig      container.HostConfig // Host configuration for the container
	containerConfig container.Config     // Configuration for the container
	containerName   string               // Name of the Docker container which is created. If empty, will autogenerate a name.
}

// New creates a new CodeExecutor instance
func New(opts ...Option) (*CodeExecutor, error) {
	c := &CodeExecutor{
		hostConfig: container.HostConfig{
			AutoRemove:  true,   // Automatically remove container after it stops
			Privileged:  false,  // Run in unprivileged mode
			NetworkMode: "none", // No network access
		},
		containerConfig: container.Config{
			Image:      defaultImageTag,
			WorkingDir: defaultContainerWorkingDir,
			Cmd:        []string{"tail", "-f", "/dev/null"}, // Keep container running
			Tty:        true,
			OpenStdin:  true,
		},
	}

	// Apply options
	for _, opt := range opts {
		opt(c)
	}

	// Validate configuration
	if c.containerConfig.Image == "" && c.dockerFilePath == "" {
		return nil, fmt.Errorf("either image or dockerFilePath must be set for CodeExecutor")
	}
	if c.dockerFilePath != "" {
		abs, err := filepath.Abs(c.dockerFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for %s: %v", abs, err)
		}
		c.dockerFilePath = abs
	}
	if c.containerName == "" {
		c.containerName = generateContainerName()
	}

	// Initialize Docker client
	var err error
	if c.host != "" {
		c.client, err = client.NewClientWithOpts(client.WithHost(c.host), client.WithAPIVersionNegotiation())
	} else {
		c.client, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Initialize container
	if err := c.initContainer(); err != nil {
		return nil, fmt.Errorf("failed to initialize container: %w", err)
	}

	// Setup cleanup finalizer
	runtime.SetFinalizer(c, (*CodeExecutor).cleanup)

	return c, nil
}

// Option defines configuration options for CodeExecutor
type Option func(*CodeExecutor)

// WithHost sets the base URL for Docker client
func WithHost(host string) Option {
	return func(c *CodeExecutor) {
		c.host = host
	}
}

// WithDockerFilePath sets the path to Dockerfile directory
func WithDockerFilePath(path string) Option {
	return func(c *CodeExecutor) {
		c.dockerFilePath = path
	}
}

// WithHostConfig sets the configuration for the Docker container.
func WithHostConfig(hostConfig container.HostConfig) Option {
	return func(c *CodeExecutor) {
		c.hostConfig = hostConfig
	}
}

// WithContainerName sets the name for the Docker container.
func WithContainerName(name string) Option {
	return func(c *CodeExecutor) {
		c.containerName = name
	}
}

// WithContainerConfig sets the configuration for the Docker container.
func WithContainerConfig(containerConfig container.Config) Option {
	return func(c *CodeExecutor) {
		c.containerConfig = containerConfig
	}
}

// ExecuteCode implements the CodeExecutor interface
func (c *CodeExecutor) ExecuteCode(ctx context.Context, input codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	if c.container == nil {
		return codeexecutor.CodeExecutionResult{}, fmt.Errorf("container not initialized")
	}

	var allOutput strings.Builder
	var allErrors strings.Builder

	// Execute each code block
	for _, block := range input.CodeBlocks {
		var execCmd []string

		// Determine command based on language
		switch block.Language {
		case "bash", "sh":
			execCmd = []string{"/bin/bash", "-c", block.Code}
		case "python", "":
			// Default to python if no language specified
			execCmd = []string{"python3", "-c", block.Code}
		default:
			// For unsupported languages, return an error message as output
			if block.Language != "" {
				errorMsg := fmt.Sprintf("unsupported language: %s\n", block.Language)
				allErrors.WriteString(errorMsg)
				continue
			}
			// If no language specified, default to python
			execCmd = []string{"python3", "-c", block.Code}
		}

		// Create exec configuration
		execConfig := container.ExecOptions{
			Cmd:          execCmd,
			AttachStdout: true,
			AttachStderr: true,
		}

		// Create exec instance
		execResp, err := c.client.ContainerExecCreate(ctx, c.container.ID, execConfig)
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf("failed to create exec: %w", err)
		}

		// Start exec
		hijacked, err := c.client.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf("failed to attach to exec: %w", err)
		}
		defer hijacked.Close()

		// Read output
		var stdout, stderr strings.Builder
		_, err = stdcopy.StdCopy(&stdout, &stderr, hijacked.Reader)
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf("failed to read exec output: %w", err)
		}

		// Accumulate outputs
		if stdout.Len() > 0 {
			allOutput.WriteString(stdout.String())
		}
		if stderr.Len() > 0 {
			allErrors.WriteString(stderr.String())
		}
	}

	// Combine stdout and stderr
	output := allOutput.String()
	if allErrors.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += allErrors.String()
	}

	return codeexecutor.CodeExecutionResult{
		Output:      output,
		OutputFiles: []codeexecutor.File{}, // Container executor doesn't support file output yet
	}, nil
}

// CodeBlockDelimiter implements the CodeExecutor interface
func (c *CodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{
		Start: "```",
		End:   "```",
	}
}

// createBuildContext creates a tar archive of the build context
func createBuildContext(dockerPath string) (io.ReadCloser, error) {
	return archive.TarWithOptions(dockerPath, &archive.TarOptions{})
}

// ensureImageExists checks if the image exists locally, and pulls it if not
func (c *CodeExecutor) ensureImageExists(ctx context.Context) error {
	// Check if image exists locally
	images, err := c.client.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list images: %w", err)
	}

	// Check if our image exists in the list
	imageExists := false
	for _, img := range images {
		for _, tag := range img.RepoTags {
			if tag == c.containerConfig.Image {
				imageExists = true
				break
			}
		}
		if imageExists {
			break
		}
	}

	if imageExists {
		log.Printf("Image %s already exists locally", c.containerConfig.Image)
		return nil
	}

	// Image doesn't exist, try to pull it
	log.Printf("Image %s not found locally, pulling...", c.containerConfig.Image)
	reader, err := c.client.ImagePull(ctx, c.containerConfig.Image, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", c.containerConfig.Image, err)
	}
	defer reader.Close()

	// Read the pull output to ensure the pull completes
	_, err = io.Copy(io.Discard, reader)
	if err != nil {
		return fmt.Errorf("failed to read image pull output: %w", err)
	}

	log.Printf("Successfully pulled image %s", c.containerConfig.Image)
	return nil
}

// buildDockerImage builds the Docker image from Dockerfile
func (c *CodeExecutor) buildDockerImage(ctx context.Context) error {
	log.Println("Building Docker image...")

	// Create build context
	buildContext, err := createBuildContext(c.dockerFilePath)
	if err != nil {
		return fmt.Errorf("failed to create build context: %w", err)
	}
	defer buildContext.Close()

	// Build image
	buildResponse, err := c.client.ImageBuild(ctx, buildContext, build.ImageBuildOptions{
		Tags:   []string{c.containerConfig.Image},
		Remove: true,
	})
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}
	defer buildResponse.Body.Close()

	// Read build output (optional, for logging)
	_, err = io.Copy(io.Discard, buildResponse.Body)
	if err != nil {
		log.Printf("Error reading build output: %v", err)
	}

	log.Printf("Docker image: %s built successfully", c.containerConfig.Image)
	return nil
}

// verifyPythonInstallation verifies that python3 is installed in the container
func (c *CodeExecutor) verifyPythonInstallation(ctx context.Context) error {
	execConfig := container.ExecOptions{
		Cmd:          []string{"which", "python3"},
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := c.client.ContainerExecCreate(ctx, c.container.ID, execConfig)
	if err != nil {
		return fmt.Errorf("failed to create exec for python verification: %w", err)
	}

	hijacked, err := c.client.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("failed to attach to python verification exec: %w", err)
	}
	defer hijacked.Close()

	// Check exit code
	inspectResp, err := c.client.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return fmt.Errorf("failed to inspect exec: %w", err)
	}

	if inspectResp.ExitCode != 0 {
		return fmt.Errorf("python3 is not installed in the container")
	}

	return nil
}

// initContainer initializes the Docker container
func (c *CodeExecutor) initContainer() error {
	ctx := context.Background()

	if c.client == nil {
		return fmt.Errorf("docker client is not initialized")
	}

	// Build image if dockerFilePath is provided
	if c.dockerFilePath != "" {
		if err := c.buildDockerImage(ctx); err != nil {
			return err
		}
	}

	log.Println("Starting container for CodeExecutor...")

	// Ensure image exists locally, pull if not
	if err := c.ensureImageExists(ctx); err != nil {
		return err
	}

	// Create container
	resp, err := c.client.ContainerCreate(ctx, &c.containerConfig, &c.hostConfig, nil, nil, c.containerName)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	// Start container
	if err := c.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	waitForContainerReadyErr := c.waitForContainerReady(ctx, 60*time.Second, resp.ID)
	if waitForContainerReadyErr != nil {
		log.Printf("Container %s did not become ready in time: %v", resp.ID, waitForContainerReadyErr)
		return fmt.Errorf("container %s did not become ready in time: %w", resp.ID, waitForContainerReadyErr)
	}

	// Get container info
	containerJSON, err := c.client.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	// Check if container is running
	if containerJSON.State.Status != "running" {
		log.Printf("Container state: %+v", containerJSON.State)
		return fmt.Errorf("container is not running, status: %s, exit code: %d",
			containerJSON.State.Status, containerJSON.State.ExitCode)
	}

	c.container = &container.Summary{
		ID:    containerJSON.ID,
		Names: []string{containerJSON.Name},
		Image: containerJSON.Image,
		State: containerJSON.State.Status,
	}

	log.Printf("Container %s started successfully and is running", c.container.ID)

	// Verify python3 installation
	if err := c.verifyPythonInstallation(ctx); err != nil {
		return err
	}

	return nil
}

func (c *CodeExecutor) waitForContainerReady(ctx context.Context, timeout time.Duration, containerID string) error {
	// For containers that should keep running (like ours with tail -f /dev/null),
	// we should check if the container is running, not wait for it to exit
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	timeoutCh := time.After(timeout)

	for {
		select {
		case <-timeoutCh:
			return fmt.Errorf("timeout %v reached while waiting for container %s to be ready", timeout, containerID)
		case <-ticker.C:
			// Check container status
			containerJSON, err := c.client.ContainerInspect(ctx, containerID)
			if err != nil {
				return fmt.Errorf("failed to inspect container during readiness check: %w", err)
			}

			// If container is running, it's ready
			if containerJSON.State.Running {
				log.Printf("Container %s is running and ready", containerID)
				return nil
			}

			// If container has exited, it's an error for our use case
			if containerJSON.State.Status == "exited" {
				return fmt.Errorf("container exited unexpectedly with code %d", containerJSON.State.ExitCode)
			}

			// Continue waiting for other states (like "created", "starting")
		}
	}
}

// cleanup stops and removes the container
func (c *CodeExecutor) cleanup() {
	if c.container == nil || c.client == nil {
		return
	}

	ctx := context.Background()
	log.Printf("[Cleanup] Stopping container %s...", c.container.ID)

	// Stop container
	if err := c.client.ContainerStop(ctx, c.container.ID, container.StopOptions{}); err != nil {
		log.Printf("Failed to stop container: %v", err)
	}

	// Remove container
	if err := c.client.ContainerRemove(ctx, c.container.ID, container.RemoveOptions{}); err != nil {
		log.Printf("Failed to remove container: %v", err)
	}

	log.Printf("Container %s stopped and removed", c.container.ID)
}

// Close manually cleans up resources
func (c *CodeExecutor) Close() error {
	c.cleanup()
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

const defaultContainerNamePrefix = "trpc.go.agent-code-exec-"

func generateContainerName() string {
	return fmt.Sprintf("%s%s", defaultContainerNamePrefix, uuid.New().String())
}
