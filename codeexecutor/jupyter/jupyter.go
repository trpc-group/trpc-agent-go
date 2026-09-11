//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package jupyter provides a Jupyter code executor.
package jupyter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Option defines configuration options for CodeExecutor
type Option func(*CodeExecutor)

// WithIP sets the IP address of the Jupyter server
func WithIP(ip string) Option {
	return func(c *CodeExecutor) {
		c.ip = ip
	}
}

// WithPort sets the port number of the Jupyter server
func WithPort(port int) Option {
	return func(c *CodeExecutor) {
		c.port = port
	}
}

// WithToken sets the authentication token for the Jupyter server
func WithToken(token string) Option {
	return func(c *CodeExecutor) {
		c.token = token
	}
}

// WithKernelName sets the kernel name for the Jupyter server
func WithKernelName(kernelName string) Option {
	return func(c *CodeExecutor) {
		c.kernelName = kernelName
	}
}

// WithLogFile sets the log file path for the Jupyter server
func WithLogFile(logFile string) Option {
	return func(c *CodeExecutor) {
		c.logFile = logFile
	}
}

// WithLogLevel sets the log level for the Jupyter server
func WithLogLevel(logLevel string) Option {
	return func(c *CodeExecutor) {
		c.logLevel = logLevel
	}
}

// WithStartTimeout sets the timeout for the Jupyter server startup
func WithStartTimeout(timeout time.Duration) Option {
	return func(c *CodeExecutor) {
		c.startTimeout = timeout
	}
}

// WithWaitReadyTimeout sets the timeout for waiting for the Jupyter kernel channel to be ready
func WithWaitReadyTimeout(timeout time.Duration) Option {
	return func(c *CodeExecutor) {
		c.waitReadyTimeout = timeout
	}
}

// CodeExecutor executes code using a Jupyter kernel
type CodeExecutor struct {
	sync.Mutex

	ip               string
	port             int
	token            string
	kernelName       string
	logFile          string
	logLevel         string
	logMaxBytes      int
	startTimeout     time.Duration
	waitReadyTimeout time.Duration
	subprocess       *exec.Cmd
	cli              *Client
	ctx              context.Context
	cancel           context.CancelFunc
	ws               *localexec.Runtime
}

// New creates a new CodeExecutor instance
func New(opts ...Option) (*CodeExecutor, error) {
	ctx, cancel := context.WithCancel(context.Background())

	c := &CodeExecutor{
		ip:               "127.0.0.1",
		port:             8888,
		token:            generateToken(),
		kernelName:       "python3",
		logFile:          "",
		logLevel:         "INFO",
		logMaxBytes:      1048576,
		startTimeout:     10 * time.Second,
		waitReadyTimeout: 10 * time.Second,
		ctx:              ctx,
		cancel:           cancel,
	}

	for _, opt := range opts {
		opt(c)
	}

	if err := c.checkJupyterGateway(); err != nil {
		return nil, err
	}

	loggingConfig := map[string]any{
		"loggers": map[string]any{
			"KernelGatewayApp": map[string]any{
				"level":    c.logLevel,
				"handlers": []string{"console"},
			},
		},
	}
	logHandlers := map[string]any{}

	if len(c.logFile) > 0 {
		logHandlers["file"] = map[string]any{
			"class":    "logging.handlers.RotatingFileHandler",
			"level":    c.logLevel,
			"maxBytes": c.logMaxBytes,
			"filename": c.logFile,
		}
		loggingConfig["handlers"] = logHandlers
		loggingConfig["loggers"] = map[string]any{
			"KernelGatewayApp": map[string]any{
				"level":    c.logLevel,
				"handlers": []string{"file", "console"},
			},
		}
	}

	loggingConfigJSON, err := json.Marshal(loggingConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal logging config: %v", err)
	}

	args := []string{
		"-m", "jupyter", "kernelgateway",
		"--KernelGatewayApp.ip", c.ip,
		"--KernelGatewayApp.auth_token", c.token,
		"--JupyterApp.answer_yes", "true",
		"--JupyterApp.logging_config", string(loggingConfigJSON),
		"--JupyterWebsocketPersonality.list_kernels", "true",
	}

	if c.port != 0 {
		args = append(args, "--KernelGatewayApp.port", strconv.Itoa(c.port))
		args = append(args, "--KernelGatewayApp.port_retries", "0")
	}

	c.subprocess = exec.CommandContext(c.ctx, "python", args...)

	buff := bytes.NewBuffer(make([]byte, 1024))
	c.subprocess.Stderr = buff

	if err := c.subprocess.Start(); err != nil {
		return nil, fmt.Errorf("failed to start jupyter gateway: %v", err)
	}

	timeout := time.After(c.startTimeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	scan := bufio.NewReader(buff)
	for {
		select {
		case <-timeout:
			c.cleanup()
			return nil, fmt.Errorf("jupyter gateway startup timeout")
		case <-ticker.C:
			if c.subprocess.ProcessState != nil && c.subprocess.ProcessState.Exited() {
				exitCode := c.subprocess.ProcessState.ExitCode()
				c.cleanup()
				return nil, fmt.Errorf("jupyter gateway exited with code %d", exitCode)
			}

			line, _, _ := scan.ReadLine()
			if strings.Contains(string(line), "ERROR:") {
				errorInfo := strings.Split(string(line), "ERROR:")[1]
				c.cleanup()
				return nil, fmt.Errorf("jupyter gateway error: %s", errorInfo)
			}
			if strings.Contains(string(line), "is available at") {
				c.cli, err = NewClient(ConnectionInfo{
					Host:             c.ip,
					Port:             c.port,
					Token:            c.token,
					KernelName:       c.kernelName,
					WaitReadyTimeout: c.waitReadyTimeout,
				})
				if err != nil {
					return nil, err
				}
				c.ws = localexec.NewRuntime("")
				return c, nil
			}
		}
	}
}

// CodeBlockDelimiter returns the fenced code delimiter.
func (c *CodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{
		Start: "```",
		End:   "```",
	}
}

// ExecuteCode executes code blocks via the Jupyter client.
func (c *CodeExecutor) ExecuteCode(ctx context.Context, input codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	if c.cli == nil {
		return codeexecutor.CodeExecutionResult{}, fmt.Errorf("jupyter client not initialized")
	}

	return c.cli.ExecuteCode(ctx, input)
}

// Workspace methods delegate to local runtime by default.

func (c *CodeExecutor) ensureWS() *localexec.Runtime {
	if c.ws == nil {
		c.ws = localexec.NewRuntime("")
	}
	return c.ws
}

// CreateWorkspace creates a workspace using the local runtime.
func (c *CodeExecutor) CreateWorkspace(
	ctx context.Context, execID string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return c.ensureWS().CreateWorkspace(ctx, execID, pol)
}

// Cleanup deletes a workspace using the local runtime.
func (c *CodeExecutor) Cleanup(
	ctx context.Context, ws codeexecutor.Workspace,
) error {
	return c.ensureWS().Cleanup(ctx, ws)
}

// PutFiles writes files using the local runtime.
func (c *CodeExecutor) PutFiles(
	ctx context.Context, ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	return c.ensureWS().PutFiles(ctx, ws, files)
}

// PutDirectory stages a directory using the local runtime.
func (c *CodeExecutor) PutDirectory(
	ctx context.Context, ws codeexecutor.Workspace,
	hostPath, to string,
) error {
	return c.ensureWS().PutDirectory(ctx, ws, hostPath, to)
}

// RunProgram executes a command using the local runtime.
func (c *CodeExecutor) RunProgram(
	ctx context.Context, ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	return c.ensureWS().RunProgram(ctx, ws, spec)
}

// Collect copies files using the local runtime.
func (c *CodeExecutor) Collect(
	ctx context.Context, ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	return c.ensureWS().Collect(ctx, ws, patterns)
}

// ExecuteInline writes code blocks and runs them via local runtime.
func (c *CodeExecutor) ExecuteInline(
	ctx context.Context, execID string,
	blocks []codeexecutor.CodeBlock,
	timeout time.Duration,
) (codeexecutor.RunResult, error) {
	return c.ensureWS().ExecuteInline(ctx, execID, blocks, timeout)
}

// Engine exposes the local runtime as an Engine for skills.
func (c *CodeExecutor) Engine() codeexecutor.Engine {
	rt := c.ensureWS()
	return codeexecutor.NewEngine(rt, rt, rt)
}

// silencePip silences pip install commands
func silencePip(code string, lang string) string {
	var regexPattern string

	switch lang {
	case "python":
		regexPattern = `^! ?pip install`
	case "bash", "shell", "sh", "pwsh", "powershell", "ps1":
		regexPattern = `^pip install`
	default:
		return code
	}

	regex, err := regexp.Compile(regexPattern)
	if err != nil {
		return code
	}

	lines := strings.Split(code, "\n")

	for i, line := range lines {
		if regex.MatchString(line) && !strings.Contains(line, "-qqq") {
			matched := regex.FindString(line)
			if matched != "" {
				lines[i] = strings.Replace(line, matched, matched+" -qqq", 1)
			}
		}
	}

	return strings.Join(lines, "\n")
}

// checkJupyterGateway checks if the Jupyter gateway server is installed
func (c *CodeExecutor) checkJupyterGateway() error {
	cmd := exec.Command("python", "-m", "jupyter", "kernelgateway", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Jupyter gateway server is not installed. Please install it with `pip install jupyter_kernel_gateway`")
	}
	return nil
}

func (c *CodeExecutor) cleanup() {
	c.cancel()
	if c.cli != nil && c.cli.ws != nil {
		c.cli.Close()
	}
	if c.subprocess != nil {
		if err := c.subprocess.Process.Signal(syscall.SIGINT); err != nil {
			c.subprocess.Process.Kill()
		}
		c.subprocess.Wait()
	}
	log.Debugf("Jupyter gateway server stopped")
}

// Close manually cleans up resources
func (c *CodeExecutor) Close() error {
	c.cleanup()
	return nil
}

func generateToken() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 32)
	for i := range b {
		b[i] = charset[rand.New(rand.NewSource(time.Now().UnixNano())).Intn(len(charset))]
	}
	return string(b)
}
