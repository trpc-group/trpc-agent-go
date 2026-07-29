// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
)

// ContainerSandbox 基于 Docker 容器的沙箱实现。
//
// 安全特性：
//   - --network=none：禁止网络访问
//   - --memory=512m：限制内存
//   - --cpus=1：限制 CPU
//   - --read-only：只读文件系统
//   - --tmpfs /tmp：临时可写目录
//   - 非 root 用户执行
//   - 超时控制
//   - 输出大小限制
type ContainerSandbox struct {
	image   string // Docker 镜像名
	timeout time.Duration
}

// NewContainerSandbox 创建容器沙箱。
//
// 参数：
//   - image: Docker 镜像名，如 "golang:1.21-alpine"
func NewContainerSandbox(image string) (*ContainerSandbox, error) {
	if image == "" {
		image = "golang:1.21-alpine"
	}

	// 检查 Docker 是否可用
	if err := checkDockerAvailable(); err != nil {
		return nil, fmt.Errorf("Docker 不可用: %w", err)
	}

	return &ContainerSandbox{
		image:   image,
		timeout: 30 * time.Second,
	}, nil
}

// Name 返回沙箱后端名称。
func (s *ContainerSandbox) Name() string {
	return "container"
}

// Execute 在 Docker 容器中执行命令。
func (s *ContainerSandbox) Execute(ctx context.Context, opts ExecuteOptions) (*ExecuteResult, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = s.timeout
	}

	// 构建 docker run 命令
	args := []string{
		"run",
		"--rm",                     // 用完自动删除容器
		"--network=none",           // 禁止网络
		"--memory=512m",            // 限制内存
		"--cpus=1",                 // 限制 CPU
		"--read-only",              // 只读文件系统
		"--tmpfs", "/tmp:size=64m", // 临时可写目录
		"--user", "65532:65532", // 非 root 用户
	}

	// 挂载工作目录（只读）
	if opts.WorkDir != "" {
		args = append(args, "-v", opts.WorkDir+":/workspace:ro")
		args = append(args, "-w", "/workspace")
	}

	// 环境变量白名单
	env := safety.NewSafetyFilter(nil).FilterEnvVars(opts.Env)
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}

	// 镜像和命令
	args = append(args, s.image, "sh", "-c", opts.Command)

	// 执行
	start := time.Now()
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "docker", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	result := &ExecuteResult{
		ExitCode: 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration.Round(time.Millisecond).String(),
		Backend:  "container",
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else if timeoutCtx.Err() == context.DeadlineExceeded {
			result.TimedOut = true
			result.ExitCode = -1
		} else {
			result.ExitCode = -1
		}
	}

	// 合并输出
	result.Output = result.Stdout
	if result.Stderr != "" {
		result.Output += "\n" + result.Stderr
	}

	// 超时标记
	if timeoutCtx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.Output += "\n[sandbox] 命令超时，已被终止"
	}

	// 输出大小限制
	if opts.MaxOutput > 0 && len(result.Output) > opts.MaxOutput {
		result.Output = result.Output[:opts.MaxOutput]
		result.Truncated = true
		result.Output += "\n[sandbox] 输出已被截断"
	}

	// 脱敏处理
	result.Output = safety.MaskSensitiveInfo(result.Output)
	result.Stdout = safety.MaskSensitiveInfo(result.Stdout)
	result.Stderr = safety.MaskSensitiveInfo(result.Stderr)

	return result, nil
}

// Close 清理资源。
func (s *ContainerSandbox) Close() error {
	return nil
}

// checkDockerAvailable 检查 Docker 是否可用。
func checkDockerAvailable() error {
	cmd := exec.Command("docker", "version", "--format", "{{.Server.Version}}")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker 命令执行失败: %w", err)
	}
	if len(strings.TrimSpace(string(output))) == 0 {
		return fmt.Errorf("docker 返回空版本")
	}
	return nil
}
