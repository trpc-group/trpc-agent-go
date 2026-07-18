//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package execution 负责沙箱 runtime 和 Go 检查执行辅助逻辑。
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	workspaceexec "trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"
)

const (
	// RuntimeContainer 是生产默认沙箱路径。
	RuntimeContainer = "container"
	// RuntimeLocalFallback 是显式本地开发 fallback。
	RuntimeLocalFallback = "local-fallback"
	// RuntimeE2B 是显式 unsupported 的远端沙箱占位入口。
	RuntimeE2B = "e2b"
	// RuntimeFakeExecution 是测试专用 seam，不是生产 fallback。
	RuntimeFakeExecution = "fake-execution"

	ContainerRepoMountPath = "/workspace/repo"
	DefaultContainerImage  = "golang:1.25-bookworm"
	GoSandboxCacheDir      = "/tmp/cr-agent-gocache"
	GoSandboxBinary        = "/usr/local/go/bin/go"
	GoSandboxPath          = "/go/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	SandboxEnvWhitelist    = "PATH,HOME,TMPDIR,GOCACHE"
)

// Config 保存 runtime 相关执行器配置。
type Config struct {
	Runtime               string
	Timeout               time.Duration
	ContainerRepoHostPath string
}

// NewExecutor 为配置的 runtime 创建 trpc-agent-go CodeExecutor。
func NewExecutor(cfg Config) (codeexecutor.CodeExecutor, error) {
	switch cfg.Runtime {
	case RuntimeLocalFallback:
		workDir, err := os.MkdirTemp("", "cr-agent-localexec-*")
		if err != nil {
			return nil, fmt.Errorf("create local fallback workdir: %w", err)
		}
		return localexec.New(
			localexec.WithTimeout(cfg.Timeout),
			localexec.WithWorkDir(workDir),
		), nil
	case RuntimeContainer:
		opts := []containerexec.Option{
			containerexec.WithHostConfig(ContainerHostConfig()),
			containerexec.WithContainerConfig(dockercontainer.Config{
				Image:      DefaultContainerImage,
				WorkingDir: "/",
				Cmd:        []string{"tail", "-f", "/dev/null"},
				Tty:        true,
				OpenStdin:  true,
				Env: []string{
					"PATH=" + GoSandboxPath,
					"HOME=/tmp",
					"TMPDIR=/tmp",
					"GOCACHE=" + GoSandboxCacheDir,
					"GOPATH=/go",
					"GOTOOLCHAIN=local",
				},
			}),
		}
		if strings.TrimSpace(cfg.ContainerRepoHostPath) != "" {
			opts = append(opts, containerexec.WithBindMount(cfg.ContainerRepoHostPath, ContainerRepoMountPath, "ro"))
		}
		exec, err := containerexec.New(opts...)
		if err != nil {
			return nil, fmt.Errorf("create container executor: %w", err)
		}
		return exec, nil
	case RuntimeE2B:
		return UnsupportedExecutor{Runtime: RuntimeE2B}, nil
	case RuntimeFakeExecution:
		return FakeExecutor{}, nil
	default:
		return nil, fmt.Errorf("unsupported runtime %q", cfg.Runtime)
	}
}

// CleanupExecutor releases runtime-specific executor resources.
func CleanupExecutor(exec codeexecutor.CodeExecutor) error {
	if exec == nil {
		return nil
	}
	var cleanupErr error
	if closer, ok := exec.(interface{ Close() error }); ok {
		cleanupErr = errors.Join(cleanupErr, closer.Close())
	}
	localExec, ok := exec.(*localexec.CodeExecutor)
	if !ok {
		return cleanupErr
	}
	workDir := strings.TrimSpace(localExec.WorkDir)
	if workDir == "" {
		return cleanupErr
	}
	if err := os.RemoveAll(workDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove local fallback workdir %q: %w", workDir, err))
	}
	return cleanupErr
}

// ContainerHostConfig returns the enforced production isolation profile.
func ContainerHostConfig() dockercontainer.HostConfig {
	pidsLimit := int64(256)
	return dockercontainer.HostConfig{
		AutoRemove:     true,
		Privileged:     false,
		NetworkMode:    "none",
		ReadonlyRootfs: false,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
		Resources: dockercontainer.Resources{
			Memory:    1024 * 1024 * 1024,
			NanoCPUs:  2_000_000_000,
			PidsLimit: &pidsLimit,
		},
	}
}

// UnsupportedExecutor 记录显式 unsupported runtime，避免静默回退到本地执行。
type UnsupportedExecutor struct {
	Runtime string
}

func (e UnsupportedExecutor) ExecuteCode(context.Context, codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, fmt.Errorf("runtime %q is not supported by this adapter yet", e.Runtime)
}

func (e UnsupportedExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

// FakeExecutor 是测试专用 runtime seam，不会调用 shell 或 Docker。
type FakeExecutor struct{}

func (FakeExecutor) ExecuteCode(context.Context, codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{
		Output: RuntimeFakeExecution + ": test-only executor did not run code",
	}, nil
}

func (FakeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

// SandboxExecCommand 返回 runtime 内实际执行的命令。
func SandboxExecCommand(runtime string, command string) string {
	if runtime == RuntimeContainer && strings.HasPrefix(command, "go ") {
		return GoSandboxBinary + strings.TrimPrefix(command, "go")
	}
	return command
}

// BoundedSandboxCommand caps combined stdout/stderr before executor collection.
func BoundedSandboxCommand(command string, limit int) string {
	if limit <= 0 {
		return command
	}
	pipeline := fmt.Sprintf("{ %s; } 2>&1 | { head -c %d; cat >/dev/null; }", command, limit)
	return "bash -o pipefail -c " + ShellQuote(pipeline)
}

// SandboxEnv 返回传给 workspace execution 的实际环境变量。
func SandboxEnv(runtime string) map[string]string {
	pathValue := GoSandboxPath
	if runtime == RuntimeLocalFallback && os.Getenv("PATH") != "" {
		pathValue = os.Getenv("PATH")
	}
	homeValue := "/tmp"
	tmpdirValue := "/tmp"
	if runtime == RuntimeLocalFallback {
		homeValue = sandboxEnvValue("HOME", "/tmp")
		tmpdirValue = sandboxEnvValue("TMPDIR", "/tmp")
	}
	return map[string]string{
		"GOCACHE": GoSandboxCacheDir,
		"HOME":    homeValue,
		"PATH":    pathValue,
		"TMPDIR":  tmpdirValue,
	}
}

// AllowedSandboxEnvKey 判断环境变量名是否允许进入沙箱命令规格。
func AllowedSandboxEnvKey(key string) bool {
	switch strings.TrimSpace(key) {
	case "PATH", "HOME", "TMPDIR", "GOCACHE":
		return true
	default:
		return false
	}
}

func sandboxEnvValue(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" || strings.Contains(value, "sk-") || strings.Contains(strings.ToLower(value), "secret") {
		return fallback
	}
	return value
}

// WorkspaceArgs 构造 Go 检查命令的 workspace execution 参数。
func WorkspaceArgs(command string, timeout time.Duration, env map[string]string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"command": command,
		"cwd":     "work/repo",
		"timeout": int(timeout.Seconds()),
		"env":     env,
	})
}

// RunWorkspaceCommand 在由 host repo 填充的 executor workspace 中执行命令。
func RunWorkspaceCommand(ctx context.Context, exec codeexecutor.CodeExecutor, repoPath string, command string, timeout time.Duration, env map[string]string) (any, error) {
	if exec == nil {
		return nil, fmt.Errorf("workspace exec is not configured")
	}
	tool := workspaceexec.NewExecTool(exec,
		workspaceexec.WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Files: []codeexecutor.WorkspaceFile{{
				Target: "work/repo",
				Input: &codeexecutor.InputSpec{
					From: "host://" + repoPath,
					To:   "work/repo/.repo",
					Mode: "copy",
				},
			}},
		}),
	)
	args, err := WorkspaceArgs(command, timeout, env)
	if err != nil {
		return nil, err
	}
	return tool.Call(ctx, args)
}

// SandboxRepoPathForRuntime 返回 runtime 内可见的 repo 路径。
func SandboxRepoPathForRuntime(runtime string, hostRepoPath string) string {
	if runtime == RuntimeContainer {
		return ContainerRepoMountPath
	}
	return hostRepoPath
}

// SandboxCode 构造 legacy codeexec fallback 命令。
func SandboxCode(runtime string, hostRepoPath string, command string) string {
	return "cd " + ShellQuote(SandboxRepoPathForRuntime(runtime, hostRepoPath)) +
		" && export GOCACHE=" + ShellQuote(GoSandboxCacheDir) + " && " + command
}

// ShellQuote 返回 POSIX 单引号转义后的值。
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
