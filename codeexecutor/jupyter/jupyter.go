//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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
	"trpc.group/trpc-go/trpc-agent-go/log"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

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

// CodeExecutor executes code using a Jupyter kernel
type CodeExecutor struct {
	sync.Mutex

	ip           string
	port         int
	token        string
	kernelName   string
	logFile      string
	logLevel     string
	logMaxBytes  int
	startTimeout time.Duration
	subprocess   *exec.Cmd
	cli          *Client
	ctx          context.Context
	cancel       context.CancelFunc
}

// New creates a new CodeExecutor instance
func New(opts ...Option) (*CodeExecutor, error) {
	ctx, cancel := context.WithCancel(context.Background())

	c := &CodeExecutor{
		ip:           "127.0.0.1",
		port:         8888,
		token:        generateToken(),
		kernelName:   "python3",
		logFile:      "",
		logLevel:     "INFO",
		logMaxBytes:  1048576,
		startTimeout: 10 * time.Second,
		ctx:          ctx,
		cancel:       cancel,
	}

	for _, opt := range opts {
		opt(c)
	}

	if err := c.checkJupyterGateway(); err != nil {
		return nil, err
	}

	loggingConfig := map[string]interface{}{
		"loggers": map[string]interface{}{
			"KernelGatewayApp": map[string]interface{}{
				"level":    c.logLevel,
				"handlers": []string{"console"},
			},
		},
	}
	logHandlers := map[string]interface{}{}

	if len(c.logFile) > 0 {
		logHandlers["file"] = map[string]interface{}{
			"class":    "logging.handlers.RotatingFileHandler",
			"level":    c.logLevel,
			"maxBytes": c.logMaxBytes,
			"filename": c.logFile,
		}
		loggingConfig["handlers"] = logHandlers
		loggingConfig["loggers"] = map[string]interface{}{
			"KernelGatewayApp": map[string]interface{}{
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
	scan := bufio.NewReader(buff)
	for {
		select {
		case <-timeout:
			c.cleanup()
			return nil, fmt.Errorf("jupyter gateway startup timeout")
		default:
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
					Host:       c.ip,
					Port:       c.port,
					Token:      c.token,
					KernelName: c.kernelName,
				})
				if err != nil {
					return nil, err
				}
				return c, nil
			}
		}
	}
}

// CodeBlockDelimiter implements the CodeExecutor interface
func (c *CodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{
		Start: "```",
		End:   "```",
	}
}

// ExecuteCode implements the CodeExecutor interface
func (c *CodeExecutor) ExecuteCode(ctx context.Context, input codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	if c.cli == nil {
		return codeexecutor.CodeExecutionResult{}, fmt.Errorf("jupyter client not initialized")
	}

	return c.cli.ExecuteCode(ctx, input)
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
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
