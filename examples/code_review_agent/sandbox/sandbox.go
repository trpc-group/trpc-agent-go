// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package sandbox 提供沙箱执行功能，支持 local、container、e2b 三种模式
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

// ExecutorType 执行器类型
type ExecutorType string

const (
	ExecutorLocal     ExecutorType = "local"
	ExecutorContainer ExecutorType = "container"
	ExecutorE2B       ExecutorType = "e2b"
)

// SandboxConfig 沙箱配置
type SandboxConfig struct {
	ExecutorType  ExecutorType
	Timeout       time.Duration
	MaxOutputSize int
	EnvWhitelist  []string
	ForbiddenDirs []string
	WorkingDir    string
}

// DefaultSandboxConfig 默认沙箱配置
func DefaultSandboxConfig() *SandboxConfig {
	return &SandboxConfig{
		ExecutorType:  ExecutorLocal,
		Timeout:       5 * time.Minute,
		MaxOutputSize: 1024 * 1024, // 1MB
		EnvWhitelist: []string{
			"HOME", "PATH", "LANG", "LC_ALL", "LC_CTYPE",
			"LOGNAME", "TERM", "TMPDIR", "TMP", "TEMP",
			"USER", "SHELL", "PWD", "GOPATH", "GOROOT",
			"GOPROXY", "CGO_ENABLED",
		},
		ForbiddenDirs: []string{
			"/etc", "/var", "/usr", "/bin", "/sbin", "/root",
		},
	}
}

// Executor 沙箱执行器
type Executor struct {
	repoPath string
	config   *SandboxConfig
}

// NewExecutor 创建沙箱执行器（默认 local，仅作开发 fallback）
func NewExecutor(repoPath string) *Executor {
	return &Executor{
		repoPath: repoPath,
		config:   DefaultSandboxConfig(),
	}
}

// NewExecutorWithType 创建指定类型的沙箱执行器
func NewExecutorWithType(repoPath string, executorType ExecutorType) *Executor {
	config := DefaultSandboxConfig()
	config.ExecutorType = executorType
	return &Executor{
		repoPath: repoPath,
		config:   config,
	}
}

// RunAllChecks 运行所有检查
func (e *Executor) RunAllChecks(ctx context.Context, taskID string) ([]store.SandboxRun, error) {
	if err := e.validateWorkDir(e.repoPath); err != nil {
		return nil, err
	}

	switch e.config.ExecutorType {
	case ExecutorLocal:
		return e.runLocalChecks(ctx, taskID)
	case ExecutorContainer:
		return e.runContainerChecks(ctx, taskID)
	case ExecutorE2B:
		return nil, fmt.Errorf("executor %q is not supported", ExecutorE2B)
	default:
		return nil, fmt.Errorf("unknown executor type %q", e.config.ExecutorType)
	}
}

// runLocalChecks 本地执行（开发 fallback）
func (e *Executor) runLocalChecks(ctx context.Context, taskID string) ([]store.SandboxRun, error) {
	runs := make([]store.SandboxRun, 0, 2)
	if !isCommandAvailable("go") {
		return runs, fmt.Errorf("go command not available")
	}
	if !isCommandAvailable("staticcheck") {
		return runs, fmt.Errorf("staticcheck command not available")
	}

	runs = append(runs, e.runCommand(ctx, taskID, "go_vet", "go", []string{"vet", "./..."}))
	runs = append(runs, e.runCommand(ctx, taskID, "staticcheck", "staticcheck", []string{"./..."}))
	return runs, nil
}

// runContainerChecks 容器执行
func (e *Executor) runContainerChecks(ctx context.Context, taskID string) ([]store.SandboxRun, error) {
	runs := make([]store.SandboxRun, 0)

	// 检查 Docker 是否可用
	if !isCommandAvailable("docker") {
		return runs, fmt.Errorf("docker not available")
	}

	// 使用 Docker 容器执行
	containerName := fmt.Sprintf("golens-sandbox-%s", taskID)

	// 运行 go vet
	result := e.runDockerCommand(ctx, taskID, containerName, "go_vet", []string{"go", "vet", "./..."})
	runs = append(runs, result)

	// The base image only guarantees Go tooling; staticcheck is run locally when installed.
	return runs, nil
}

// runCommand 执行命令（带超时和输出限制）
func (e *Executor) runCommand(ctx context.Context, taskID, scriptName, command string, args []string) store.SandboxRun {
	startTime := time.Now()

	// 创建带超时的 context
	ctx, cancel := context.WithTimeout(ctx, e.config.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = e.repoPath

	// 设置环境变量白名单
	cmd.Env = e.buildEnv()

	stdout := newLimitedBuffer(e.config.MaxOutputSize)
	stderr := newLimitedBuffer(e.config.MaxOutputSize)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	durationMs := int(time.Since(startTime).Milliseconds())

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			exitCode = -2 // 超时
		} else {
			exitCode = -1
		}
	}

	return store.SandboxRun{
		TaskID:     taskID,
		ScriptName: scriptName,
		Command:    fmt.Sprintf("%s %s", command, joinArgs(args)),
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: durationMs,
		Truncated:  stdout.Truncated() || stderr.Truncated(),
	}
}

// runDockerCommand 在 Docker 容器中执行命令
func (e *Executor) runDockerCommand(ctx context.Context, taskID, containerName, scriptName string, cmd []string) store.SandboxRun {
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(ctx, e.config.Timeout)
	defer cancel()

	// 构建 docker run 命令
	dockerArgs := []string{
		"run", "--rm",
		"--name", containerName,
		"-v", e.repoPath + ":/workspace",
		"-w", "/workspace",
		"--network", "none", // 禁用网络
		"golang:1.21-alpine",
	}
	dockerArgs = append(dockerArgs, cmd...)

	execCmd := exec.CommandContext(ctx, "docker", dockerArgs...)

	stdout := newLimitedBuffer(e.config.MaxOutputSize)
	stderr := newLimitedBuffer(e.config.MaxOutputSize)
	execCmd.Stdout = stdout
	execCmd.Stderr = stderr

	err := execCmd.Run()
	durationMs := int(time.Since(startTime).Milliseconds())

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			exitCode = -2
		} else {
			exitCode = -1
		}
	}

	return store.SandboxRun{
		TaskID:     taskID,
		ScriptName: scriptName,
		Command:    "docker " + strings.Join(dockerArgs, " "),
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: durationMs,
		Truncated:  stdout.Truncated() || stderr.Truncated(),
	}
}

// validateWorkDir validates that the working directory is not in a forbidden location
func (e *Executor) validateWorkDir(workDir string) error {
	absPath, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}
	absPath, err = filepath.EvalSymlinks(absPath)
	if err != nil {
		return fmt.Errorf("cannot resolve work directory: %w", err)
	}

	for _, forbidden := range e.config.ForbiddenDirs {
		forbiddenAbs, err := filepath.Abs(forbidden)
		if err != nil {
			return fmt.Errorf("cannot resolve forbidden directory: %w", err)
		}
		forbiddenAbs, err = filepath.EvalSymlinks(forbiddenAbs)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(forbiddenAbs, absPath)
		if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))) {
			return fmt.Errorf("path %s is in forbidden directory %s", absPath, forbidden)
		}
	}

	return nil
}

// buildEnv builds environment variables (whitelist only)
func (e *Executor) buildEnv() []string {
	env := make([]string, 0)

	for _, name := range e.config.EnvWhitelist {
		if value, exists := os.LookupEnv(name); exists {
			env = append(env, name+"="+value)
		}
	}

	// Use isolated HOME
	env = append(env, "HOME=/tmp/golens-sandbox")

	return env
}

func isCommandAvailable(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
}

func joinArgs(args []string) string {
	return strings.Join(args, " ")
}

func truncateOutput(output string, maxSize int) string {
	if len(output) > maxSize {
		return output[:maxSize] + "\n... (truncated)"
	}
	return output
}
