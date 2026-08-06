//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package container provides a CodeExecutor that executes code blocks in a Docker container.
// It supports Python and Bash scripts, executing them in a controlled Docker environment.
package container

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"
	archive "github.com/moby/go-archive"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	// defaultImageTag is the default Docker image tag for code execution
	defaultImageTag = "python:3.9-slim"
	// Use root as default working dir to avoid Docker trying to
	// mkdir a custom path (e.g., /workspace) on read-only roots.
	defaultContainerWorkingDir = "/"
	// defaultInitTimeoutSec bounds image and container initialization when the
	// caller did not provide a deadline (for example, a registry worker).
	defaultInitTimeoutSec = 60
)

// CodeExecutor executes code using a Docker container.
//
// Its lifecycle methods are safe for concurrent use. Close is idempotent and
// waits for an in-progress lifecycle transition to finish. Calls that begin
// after Close returns fail without starting or reusing a container; calls
// already using a Docker request may finish with that request's result or an
// error caused by the closed client.
type CodeExecutor struct {
	containerMu     sync.Mutex
	closed          bool
	host            string               // Optional base URL of the user hosted Docker client, default client.DefaultDockerHost
	dockerFilePath  string               // Path to directory containing Dockerfile
	client          *client.Client       // Docker client
	container       *container.Summary   // Running container instance
	hostConfig      container.HostConfig // Host configuration for the container
	containerConfig container.Config     // Configuration for the container
	containerName   string               // Name of the Docker container which is created. If empty, will autogenerate a name.
	ws              *workspaceRuntime    // workspace runtime
	generation      uint64               // Monotonic physical container generation.
	workspaceGen    map[string]uint64    // Generation that created each live logical workspace path.
	// autoInputs controls mapping of inputs-host into workspace.
	autoInputs bool
}

// New creates a new CodeExecutor instance
func New(opts ...Option) (*CodeExecutor, error) {
	return NewWithContext(context.Background(), opts...)
}

// NewWithContext creates a CodeExecutor and uses ctx only for image
// preparation and container startup. A context without a deadline is bounded
// to 60 seconds for initialization. Cancelling ctx after this function returns
// does not close the executor. A nil ctx returns an error.
func NewWithContext(ctx context.Context, opts ...Option) (*CodeExecutor, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context must not be nil")
	}
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
		autoInputs: true,
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
	if err := c.initContainer(ctx); err != nil {
		c.cleanup()
		_ = c.client.Close()
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

// WithHostConfig replaces the entire HostConfig for the Docker container.
// Note: This is a replacement operation. If WithBindMount is also used,
// make sure it is called after this option to avoid bind mounts being overwritten.
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

// WithBindMount appends a bind mount in the form source:dest:mode.
// Example mode: "ro" or "rw". If WithHostConfig is also used, make sure
// WithBindMount is called after it, otherwise the bind mount will be overwritten.
// This option is generic and does not imply any domain-specific semantics.
func WithBindMount(src, dest, mode string) Option {
	return func(c *CodeExecutor) {
		spec := src + ":" + dest
		if mode != "" {
			spec += ":" + mode
		}
		c.hostConfig.Binds = append(c.hostConfig.Binds, spec)
	}
}

// WithAutoInputs enables or disables automatic mapping of the
// inputs host directory into the workspace-level work/inputs
// directory. When enabled and an inputs bind is present, each
// created workspace will expose that directory under inputs/.
func WithAutoInputs(enable bool) Option {
	return func(c *CodeExecutor) {
		c.autoInputs = enable
	}
}

// ExecuteCode implements the CodeExecutor interface
func (c *CodeExecutor) ExecuteCode(ctx context.Context, input codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	// Validate the lifecycle even when input has no executable blocks. Besides
	// preserving the executor contract for empty input, this avoids reading the
	// container field outside the lifecycle lock.
	if err := c.requireContainer(); err != nil {
		return codeexecutor.CodeExecutionResult{}, err
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
		client, containerID, err := c.containerClient()
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, err
		}

		// Create exec instance
		execResp, err := client.ContainerExecCreate(ctx, containerID, execConfig)
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf("failed to create exec: %w", err)
		}

		// Start exec
		hijacked, err := client.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
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

// Workspace methods

func (c *CodeExecutor) ensureWS(ctx context.Context) (*workspaceRuntime, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	return c.ensureWSLocked(ctx)
}

func (c *CodeExecutor) ensureWSLocked(ctx context.Context) (*workspaceRuntime, error) {
	if c.closed {
		return nil, fmt.Errorf("container executor is closed")
	}
	// Preserve the lightweight wrapper behavior for a zero-value executor used
	// by callers that only need workspace adapters. A configured executor gets
	// a fresh container after timeout invalidation.
	if c.container == nil && c.client != nil {
		if err := c.ensureContainerLocked(ctx); err != nil {
			return nil, err
		}
	}
	if c.ws != nil {
		return c.ws, nil
	}
	rt, err := newWorkspaceRuntime(c)
	if err != nil {
		return nil, err
	}
	c.ws = rt
	return rt, nil
}

// InstanceID identifies the current physical container generation for
// WorkspaceRegistry. It returns an error until the executor has a Docker
// client and a physical container to identify. When an earlier timed-out
// attach invalidated a configured container, it recreates that physical
// instance with the caller's context before returning. A context without a
// deadline is bounded to 60 seconds for initialization. This keeps the
// registry's pre- and post-create probes stable while preserving cancellation
// for image and container startup.
func (c *CodeExecutor) InstanceID(ctx context.Context) (codeexecutor.WorkspaceInstanceID, error) {
	if ctx == nil {
		return "", fmt.Errorf("context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	if c.closed {
		return "", fmt.Errorf("container executor is closed")
	}
	if c.container == nil {
		if c.client == nil {
			return "", fmt.Errorf("container executor is not initialized")
		}
		if err := c.ensureContainerLocked(ctx); err != nil {
			return "", err
		}
	}
	return codeexecutor.WorkspaceInstanceID(fmt.Sprintf("container-%d", c.generation)), nil
}

func (c *CodeExecutor) rememberWorkspace(ws codeexecutor.Workspace, generation uint64) {
	if ws.Path == "" {
		return
	}
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	if c.workspaceGen == nil {
		c.workspaceGen = make(map[string]uint64)
	}
	c.workspaceGen[ws.Path] = generation
}

func (c *CodeExecutor) validateWorkspace(ws codeexecutor.Workspace) error {
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	return c.validateWorkspaceLocked(ws)
}

func (c *CodeExecutor) validateWorkspaceLocked(ws codeexecutor.Workspace) error {
	if ws.Path == "" {
		return nil
	}
	if generation, known := c.workspaceGen[ws.Path]; known && generation != c.generation {
		return fmt.Errorf("%w: container generation %d replaced workspace generation %d", codeexecutor.ErrWorkspaceStale, c.generation, generation)
	}
	return nil
}

// runtimeForWorkspace atomically validates a workspace handle and chooses the
// runtime that will use it. abortContainer cannot replace the physical
// container between these operations while the lifecycle lock is held; if it
// replaces it immediately afterward, the runtime's generation check reports
// ErrWorkspaceStale instead of executing against the replacement.
func (c *CodeExecutor) runtimeForWorkspace(ctx context.Context, ws codeexecutor.Workspace) (*workspaceRuntime, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	if err := c.validateWorkspaceLocked(ws); err != nil {
		return nil, err
	}
	return c.ensureWSLocked(ctx)
}

func (c *CodeExecutor) forgetWorkspace(ws codeexecutor.Workspace, generation uint64) {
	if ws.Path == "" {
		return
	}
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	if c.workspaceGen[ws.Path] == generation {
		delete(c.workspaceGen, ws.Path)
	}
}

// ensureContainer recreates the isolated execution container after a timed-out
// attach has invalidated it. It must not reuse a container that may still be
// running an untrusted process.
func (c *CodeExecutor) ensureContainer(ctx context.Context) error {
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	return c.ensureContainerLocked(ctx)
}

func (c *CodeExecutor) ensureContainerLocked(ctx context.Context) error {
	if c.closed {
		return fmt.Errorf("container executor is closed")
	}
	if c.container != nil {
		return nil
	}
	return c.initContainer(ctx)
}

func (c *CodeExecutor) containerClient() (*client.Client, string, error) {
	return c.containerClientForGeneration(0)
}

func (c *CodeExecutor) containerClientForGeneration(generation uint64) (*client.Client, string, error) {
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	if c.closed {
		return nil, "", fmt.Errorf("container executor is closed")
	}
	// A generation-bound runtime must fail stale before considering whether a
	// replacement container exists. Otherwise an old workspace operation could
	// rebuild a physical container only to fail on its later client lookup.
	if generation != 0 && generation != c.generation {
		return nil, "", fmt.Errorf("%w: container generation %d replaced runtime generation %d", codeexecutor.ErrWorkspaceStale, c.generation, generation)
	}
	if c.client == nil || c.container == nil {
		return nil, "", fmt.Errorf("container not initialized")
	}
	return c.client, c.container.ID, nil
}

// requireContainer validates the lifecycle for operations that can complete
// without a Docker client (for example, an unsupported-language diagnostic).
func (c *CodeExecutor) requireContainer() error {
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	if c.closed {
		return fmt.Errorf("container executor is closed")
	}
	if c.container == nil {
		return fmt.Errorf("container not initialized")
	}
	return nil
}

// abortContainer terminates a container after its exec attach has timed out.
// Docker cannot cancel an individual exec, so retaining the container could
// leave the untrusted process running after RunProgram has returned.
func (c *CodeExecutor) abortContainer() {
	if c == nil {
		return
	}
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	if c.closed || c.client == nil || c.container == nil {
		return
	}
	containerID := c.container.ID
	ctx, cancel := context.WithTimeout(context.Background(), defaultRmTimeoutSec*time.Second)
	defer cancel()
	if err := c.client.ContainerKill(ctx, containerID, "SIGKILL"); err != nil {
		log.DebugfContext(ctx, "Failed to kill timed-out container %s: %v", containerID, err)
	}
	// AutoRemove is a default, not an invariant: callers can replace
	// HostConfig. Always request removal so a timed-out runtime never leaks a
	// container when AutoRemove was disabled.
	if err := c.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		log.DebugfContext(ctx, "Failed to remove timed-out container %s: %v", containerID, err)
	}
	// Do not reuse the container even if Docker returns an error: closing the
	// attach makes its state uncertain, and the next operation will create a
	// fresh isolated container.
	c.container = nil
	c.ws = nil
	c.generation++
}

// Engine exposes the container runtime as an Engine for skills.
//
// The returned Engine advertises Capabilities{SupportsCleanEnv: true}
// because RunProgram honors spec.CleanEnv: in clean mode the command
// is spawned via `env -i ...` under a non-login `bash --noprofile
// --norc`, so it starts from a minimal environment (workspace base
// vars plus a default PATH) instead of inheriting the container
// process env or sourcing start-up files. Tool layers that gate
// policy mode on SupportsCleanEnv (tool/workspaceexec) therefore no
// longer fail closed on the container backend (issue #1845).
func (c *CodeExecutor) Engine() codeexecutor.Engine {
	return codeexecutor.NewEngineWithCapabilities(
		c, c, c,
		codeexecutor.Capabilities{SupportsCleanEnv: true},
	)
}

// CreateWorkspace creates a workspace using the container runtime.
func (c *CodeExecutor) CreateWorkspace(
	ctx context.Context, execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	rt, err := c.ensureWS(ctx)
	if err != nil {
		return codeexecutor.Workspace{}, err
	}
	ws, err := rt.CreateWorkspace(ctx, execID, pol)
	if err == nil {
		c.rememberWorkspace(ws, rt.generation)
	}
	return ws, err
}

// Cleanup removes a workspace via the container runtime.
func (c *CodeExecutor) Cleanup(
	ctx context.Context, ws codeexecutor.Workspace,
) error {
	rt, err := c.runtimeForWorkspace(ctx, ws)
	if err != nil {
		return err
	}
	if err := rt.Cleanup(ctx, ws); err != nil {
		return err
	}
	c.forgetWorkspace(ws, rt.generation)
	return nil
}

// PutFiles writes files into a workspace in the container.
func (c *CodeExecutor) PutFiles(
	ctx context.Context, ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	if len(files) == 0 {
		return nil
	}
	rt, err := c.runtimeForWorkspace(ctx, ws)
	if err != nil {
		return err
	}
	return rt.PutFiles(ctx, ws, files)
}

// PutDirectory stages a host directory into the workspace.
func (c *CodeExecutor) PutDirectory(
	ctx context.Context, ws codeexecutor.Workspace,
	hostPath, to string,
) error {
	rt, err := c.runtimeForWorkspace(ctx, ws)
	if err != nil {
		return err
	}
	return rt.PutDirectory(ctx, ws, hostPath, to)
}

// StageDirectory stages a host directory with the requested workspace policy.
func (c *CodeExecutor) StageDirectory(
	ctx context.Context, ws codeexecutor.Workspace,
	src, to string, opt codeexecutor.StageOptions,
) error {
	rt, err := c.runtimeForWorkspace(ctx, ws)
	if err != nil {
		return err
	}
	return rt.StageDirectory(ctx, ws, src, to, opt)
}

// StageInputs maps declared external inputs into a workspace.
func (c *CodeExecutor) StageInputs(
	ctx context.Context, ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	rt, err := c.runtimeForWorkspace(ctx, ws)
	if err != nil {
		return err
	}
	return rt.StageInputs(ctx, ws, specs)
}

// CollectOutputs collects workspace output according to the declared spec.
func (c *CodeExecutor) CollectOutputs(
	ctx context.Context, ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	rt, err := c.runtimeForWorkspace(ctx, ws)
	if err != nil {
		return codeexecutor.OutputManifest{}, err
	}
	return rt.CollectOutputs(ctx, ws, spec)
}

// RunProgram runs a command inside the workspace.
func (c *CodeExecutor) RunProgram(
	ctx context.Context, ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	setupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rt, err := c.runtimeForWorkspace(setupCtx, ws)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	return rt.RunProgram(ctx, ws, spec)
}

// Collect copies files out of the workspace.
func (c *CodeExecutor) Collect(
	ctx context.Context, ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	rt, err := c.runtimeForWorkspace(ctx, ws)
	if err != nil {
		return nil, err
	}
	return rt.Collect(ctx, ws, patterns)
}

// ExecuteInline writes code blocks and executes them in the container.
func (c *CodeExecutor) ExecuteInline(
	ctx context.Context, execID string,
	blocks []codeexecutor.CodeBlock,
	timeout time.Duration,
) (codeexecutor.RunResult, error) {
	rt, err := c.ensureWS(ctx)
	if err != nil {
		return codeexecutor.RunResult{}, err
	}
	return rt.ExecuteInline(ctx, execID, blocks, timeout)
}

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
		return nil
	}

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

	return nil
}

// buildDockerImage builds the Docker image from Dockerfile
func (c *CodeExecutor) buildDockerImage(ctx context.Context) error {
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
		log.WarnfContext(ctx, "Error reading build output: %v", err)
	}
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

func initializationContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("context must not be nil")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		bounded, cancel := context.WithTimeout(ctx, defaultInitTimeoutSec*time.Second)
		return bounded, cancel, nil
	}
	return ctx, func() {}, nil
}

// failedInitializationCleanupContext uses only the initialization operation's
// remaining budget. It deliberately ignores caller cancellation so a created
// container can be removed, but never extends the initialization deadline.
func failedInitializationCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(context.Background(), deadline)
	}
	return context.WithTimeout(context.Background(), defaultRmTimeoutSec*time.Second)
}

// initContainer initializes the Docker container.
func (c *CodeExecutor) initContainer(ctx context.Context) error {
	ctx, cancel, err := initializationContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	if c.client == nil {
		return fmt.Errorf("docker client is not initialized")
	}

	// Build image if dockerFilePath is provided
	if c.dockerFilePath != "" {
		if err := c.buildDockerImage(ctx); err != nil {
			return err
		}
	}

	// Ensure image exists locally, pull if not
	if err := c.ensureImageExists(ctx); err != nil {
		return err
	}

	// Create container
	resp, err := c.client.ContainerCreate(ctx, &c.containerConfig, &c.hostConfig, nil, nil, c.containerName)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}
	// Record the ID before any subsequent operation so the constructor's
	// cleanup path can remove a container created during a failed startup.
	c.container = &container.Summary{ID: resp.ID}
	// Container recreation after a timed-out attach uses this method too. Do
	// not leave a partially started ID behind on any later error: a future
	// ensureContainer call must retry from a fresh isolated container.
	succeeded := false
	defer func() {
		if succeeded {
			return
		}
		cleanupCtx, cancel := failedInitializationCleanupContext(ctx)
		defer cancel()
		if removeErr := c.client.ContainerRemove(cleanupCtx, resp.ID, container.RemoveOptions{Force: true}); removeErr != nil {
			log.DebugfContext(cleanupCtx, "Failed to remove failed container %s: %v", resp.ID, removeErr)
		}
		c.container = nil
		c.ws = nil
	}()

	// Start container
	if err := c.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	waitForContainerReadyErr := c.waitForContainerReady(ctx, 60*time.Second, resp.ID)
	if waitForContainerReadyErr != nil {
		return fmt.Errorf("container %s did not become ready in time: %w", resp.ID, waitForContainerReadyErr)
	}

	// Get container info
	containerJSON, err := c.client.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	// Check if container is running
	if containerJSON.State.Status != "running" {
		return fmt.Errorf("container is not running, status: %s, exit code: %d",
			containerJSON.State.Status, containerJSON.State.ExitCode)
	}

	c.container = &container.Summary{
		ID:    containerJSON.ID,
		Names: []string{containerJSON.Name},
		Image: containerJSON.Image,
		State: containerJSON.State.Status,
	}
	c.generation++

	log.DebugfContext(ctx, "Container %s started successfully and is running", c.container.ID)

	// Verify python3 installation
	if err := c.verifyPythonInstallation(ctx); err != nil {
		return err
	}

	succeeded = true
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
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	c.cleanupLocked()
}

func (c *CodeExecutor) cleanupLocked() {
	if c.container == nil || c.client == nil {
		c.ws = nil
		return
	}
	containerID := c.container.ID
	client := c.client
	// Prevent newly obtained runtimes from using a container that is being
	// stopped. The lifecycle lock remains held until removal completes.
	c.container = nil
	c.ws = nil
	c.generation++

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Stop container
	if err := client.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
		log.DebugfContext(ctx, "Failed to stop container: %v", err)
	}

	// Remove container
	if err := client.ContainerRemove(ctx, containerID, container.RemoveOptions{}); err != nil {
		log.DebugfContext(ctx, "Failed to remove container: %v", err)
	}

	log.DebugfContext(ctx, "Container %s stopped and removed", containerID)
}

// Close stops and removes the current container and releases the Docker
// client. It is safe to call concurrently and more than once. After it
// returns, new executor operations fail; operations already in flight can
// complete or return an error from the closed Docker client.
func (c *CodeExecutor) Close() error {
	c.containerMu.Lock()
	defer c.containerMu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.cleanupLocked()
	if c.client != nil {
		err := c.client.Close()
		c.client = nil
		return err
	}
	return nil
}

const defaultContainerNamePrefix = "trpc.go.agent-code-exec-"

func generateContainerName() string {
	return fmt.Sprintf("%s%s", defaultContainerNamePrefix, uuid.New().String())
}
