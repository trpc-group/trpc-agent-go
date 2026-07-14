//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type SandboxType string

const (
	SandboxTypeE2B       SandboxType = "e2b"
	SandboxTypeContainer SandboxType = "container"
	SandboxTypeLocal     SandboxType = "local"
	SandboxTypeNoop      SandboxType = "noop"
)

type SandboxConfig struct {
	Timeout          time.Duration
	OutputSizeLimit  int
	EnvWhitelist     []string
	UseLocalFallback bool
	Type             SandboxType
	UnsafeLocal      bool
}

type SandboxResult struct {
	Output      string
	Error       string
	ExitCode    int
	TimedOut    bool
	Duration    time.Duration
	SandboxType SandboxType
}

type Sandbox interface {
	RunCommand(ctx context.Context, command string, config SandboxConfig) (SandboxResult, error)
	ExecuteScript(ctx context.Context, scriptPath string, args []string, config SandboxConfig) (SandboxResult, error)
	Close() error
	GetType() SandboxType
}

type LocalSandbox struct {
	workDir string
}

func NewLocalSandbox(workDir string) (*LocalSandbox, error) {
	if strings.Contains(workDir, "..") {
		return nil, fmt.Errorf("path traversal detected in work directory: %s", workDir)
	}

	absPath, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("get absolute path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat work directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("work directory is not a directory: %s", workDir)
	}

	return &LocalSandbox{workDir: absPath}, nil
}

func (s *LocalSandbox) RunCommand(ctx context.Context, command string, config SandboxConfig) (SandboxResult, error) {
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	start := time.Now()

	args := parseShellCommand(command)
	if len(args) == 0 {
		return SandboxResult{
			Error:       "Empty command",
			ExitCode:    -1,
			Duration:    time.Since(start),
			TimedOut:    false,
			SandboxType: SandboxTypeLocal,
		}, nil
	}

	var cmd *exec.Cmd
	if len(args) == 1 {
		cmd = exec.CommandContext(ctx, args[0])
	} else {
		cmd = exec.CommandContext(ctx, args[0], args[1:]...)
	}
	cmd.Dir = s.workDir

	if len(config.EnvWhitelist) > 0 {
		cmd.Env = filterEnv(os.Environ(), config.EnvWhitelist)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return SandboxResult{
				Error:       err.Error(),
				ExitCode:    -1,
				Duration:    duration,
				TimedOut:    ctx.Err() == context.DeadlineExceeded,
				SandboxType: SandboxTypeLocal,
			}, nil
		}
	}

	output := stdout.String()
	if config.OutputSizeLimit > 0 && len(output) > config.OutputSizeLimit {
		output = output[:config.OutputSizeLimit] + "... [truncated]"
	}

	errOutput := stderr.String()
	if config.OutputSizeLimit > 0 && len(errOutput) > config.OutputSizeLimit {
		errOutput = errOutput[:config.OutputSizeLimit] + "... [truncated]"
	}

	return SandboxResult{
		Output:      output,
		Error:       errOutput,
		ExitCode:    exitCode,
		TimedOut:    ctx.Err() == context.DeadlineExceeded,
		Duration:    duration,
		SandboxType: SandboxTypeLocal,
	}, nil
}

func (s *LocalSandbox) ExecuteScript(ctx context.Context, scriptPath string, args []string, config SandboxConfig) (SandboxResult, error) {
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	start := time.Now()

	cmdArgs := append([]string{scriptPath}, args...)
	cmd := exec.CommandContext(ctx, "bash", cmdArgs...)
	cmd.Dir = s.workDir

	if len(config.EnvWhitelist) > 0 {
		cmd.Env = filterEnv(os.Environ(), config.EnvWhitelist)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return SandboxResult{
				Error:       err.Error(),
				ExitCode:    -1,
				Duration:    duration,
				TimedOut:    ctx.Err() == context.DeadlineExceeded,
				SandboxType: SandboxTypeLocal,
			}, nil
		}
	}

	output := stdout.String()
	if config.OutputSizeLimit > 0 && len(output) > config.OutputSizeLimit {
		output = output[:config.OutputSizeLimit] + "... [truncated]"
	}

	errOutput := stderr.String()
	if config.OutputSizeLimit > 0 && len(errOutput) > config.OutputSizeLimit {
		errOutput = errOutput[:config.OutputSizeLimit] + "... [truncated]"
	}

	return SandboxResult{
		Output:      output,
		Error:       errOutput,
		ExitCode:    exitCode,
		TimedOut:    ctx.Err() == context.DeadlineExceeded,
		Duration:    duration,
		SandboxType: SandboxTypeLocal,
	}, nil
}

func (s *LocalSandbox) Close() error {
	return nil
}

func (s *LocalSandbox) GetType() SandboxType {
	return SandboxTypeLocal
}

type NoopSandbox struct{}

func NewNoopSandbox() *NoopSandbox {
	return &NoopSandbox{}
}

func (s *NoopSandbox) RunCommand(ctx context.Context, command string, config SandboxConfig) (SandboxResult, error) {
	return SandboxResult{
		Output:      "",
		Error:       "",
		ExitCode:    0,
		TimedOut:    false,
		Duration:    0,
		SandboxType: SandboxTypeNoop,
	}, nil
}

func (s *NoopSandbox) ExecuteScript(ctx context.Context, scriptPath string, args []string, config SandboxConfig) (SandboxResult, error) {
	return SandboxResult{
		Output:      "",
		Error:       "",
		ExitCode:    0,
		TimedOut:    false,
		Duration:    0,
		SandboxType: SandboxTypeNoop,
	}, nil
}

func (s *NoopSandbox) Close() error {
	return nil
}

func (s *NoopSandbox) GetType() SandboxType {
	return SandboxTypeNoop
}

func filterEnv(env []string, whitelist []string) []string {
	var result []string
	for _, e := range env {
		for _, allowed := range whitelist {
			if strings.HasPrefix(e, allowed+"=") {
				result = append(result, e)
				break
			}
		}
	}
	return result
}

func parseShellCommand(cmd string) []string {
	var args []string
	var current []byte
	inSingleQuote := false
	inDoubleQuote := false
	escape := false

	for _, c := range cmd {
		if escape {
			current = append(current, byte(c))
			escape = false
			continue
		}

		if c == '\\' && !inSingleQuote {
			escape = true
			continue
		}

		if c == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			continue
		}

		if c == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			continue
		}

		if (c == ' ' || c == '\t' || c == '\n') && !inSingleQuote && !inDoubleQuote {
			if len(current) > 0 {
				args = append(args, string(current))
				current = current[:0]
			}
			continue
		}

		current = append(current, byte(c))
	}

	if len(current) > 0 {
		args = append(args, string(current))
	}

	return args
}

func NewSandbox(workDir string) (Sandbox, error) {
	return NewSandboxWithConfig(workDir, SandboxConfig{})
}

func NewSandboxWithConfig(workDir string, config SandboxConfig) (Sandbox, error) {
	log.Printf("Attempting to create sandbox...")

	if config.UnsafeLocal || os.Getenv("UNSAFE_LOCAL_SANDBOX") == "true" {
		log.Printf("Unsafe local sandbox enabled, using local sandbox")
		return NewLocalSandbox(workDir)
	}

	log.Printf("Using no-op sandbox (dry-run mode) - commands will not be executed")
	return NewNoopSandbox(), nil
}

var DefaultConfig = SandboxConfig{
	Timeout:          60 * time.Second,
	OutputSizeLimit:  1024 * 1024,
	EnvWhitelist:     []string{"PATH", "HOME"},
	UseLocalFallback: true,
	Type:             SandboxTypeLocal,
}
