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
	"strings"
	"time"
)

type SandboxType string

const (
	SandboxTypeE2B       SandboxType = "e2b"
	SandboxTypeContainer SandboxType = "container"
	SandboxTypeLocal     SandboxType = "local"
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
	return &LocalSandbox{workDir: workDir}, nil
}

func (s *LocalSandbox) RunCommand(ctx context.Context, command string, config SandboxConfig) (SandboxResult, error) {
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	start := time.Now()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
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

func NewSandbox(workDir string) (Sandbox, error) {
	return NewSandboxWithConfig(workDir, SandboxConfig{})
}

func NewSandboxWithConfig(workDir string, config SandboxConfig) (Sandbox, error) {
	log.Printf("Attempting to create sandbox...")

	if os.Getenv("E2B_API_KEY") != "" {
		log.Printf("E2B API key found, attempting E2B sandbox...")
		return createE2BSandbox(workDir)
	}

	if os.Getenv("CONTAINER_RUNTIME") != "" {
		log.Printf("Container runtime available, attempting container sandbox...")
		return createContainerSandbox(workDir)
	}

	if config.UnsafeLocal || os.Getenv("UNSAFE_LOCAL_SANDBOX") == "true" {
		log.Printf("Unsafe local sandbox enabled, using local sandbox")
		return NewLocalSandbox(workDir)
	}

	return nil, fmt.Errorf("no external sandbox available and unsafe-local mode is not enabled")
}

func createE2BSandbox(workDir string) (Sandbox, error) {
	return nil, fmt.Errorf("E2B sandbox not implemented")
}

func createContainerSandbox(workDir string) (Sandbox, error) {
	return nil, fmt.Errorf("container sandbox not implemented")
}

var DefaultConfig = SandboxConfig{
	Timeout:          60 * time.Second,
	OutputSizeLimit:  1024 * 1024,
	EnvWhitelist:     []string{"PATH", "HOME"},
	UseLocalFallback: true,
	Type:             SandboxTypeLocal,
}
