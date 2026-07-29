// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
)

// LocalSandbox 基于框架的 local.Runtime 实现的本地沙箱。
//
// 使用 trpc-agent-go 的 codeexecutor/local 包执行命令，
// 提供超时控制、输出大小限制和环境变量白名单。
//
// 注意：本地执行没有文件系统隔离，仅作为开发 fallback。
// 生产环境应使用 ContainerSandbox。
type LocalSandbox struct {
	runtime *local.Runtime
}

// NewLocalSandbox 创建一个本地沙箱。
//
// 参数：
//   - workRoot: 工作根目录（临时工作区的基础路径）
func NewLocalSandbox(workRoot string) (*LocalSandbox, error) {
	if workRoot == "" {
		workRoot = os.TempDir()
	}

	runtime := local.NewRuntimeWithOptions(workRoot,
		local.WithRuntimeWorkspaceMode(local.WorkspaceModeTrustedLocal),
	)

	return &LocalSandbox{runtime: runtime}, nil
}

// Name 返回沙箱后端名称。
func (s *LocalSandbox) Name() string {
	return "local"
}

// Execute 在本地执行命令。
//
// 使用框架的 Runtime.RunProgram，支持：
//   - 超时控制（context.WithTimeout）
//   - 工作目录设置
//   - 环境变量传递
//   - 输出大小限制（读取后截断）
func (s *LocalSandbox) Execute(ctx context.Context, opts ExecuteOptions) (*ExecuteResult, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	// 创建超时 context
	timeoutCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// 准备工作区
	ws := codeexecutor.Workspace{
		Path: opts.WorkDir,
	}
	if ws.Path == "" {
		tmpDir, err := os.MkdirTemp("", "sandbox-*")
		if err != nil {
			return nil, fmt.Errorf("创建工作区失败: %w", err)
		}
		ws.Path = tmpDir
	}

	// 构建执行参数
	// 使用 sh -c 包装命令，支持管道、重定向等 shell 特性
	spec := codeexecutor.RunProgramSpec{
		Cmd:     "sh",
		Args:    []string{"-c", opts.Command},
		Cwd:     ws.Path,
		Env:     opts.Env,
		Timeout: opts.Timeout,
	}

	// 使用框架的 Runtime 执行
	start := time.Now()
	runResult, _ := s.runtime.RunProgram(timeoutCtx, ws, spec)
	duration := time.Since(start)

	result := &ExecuteResult{
		Duration: duration.Round(time.Millisecond).String(),
		Backend:  "local",
	}

	// 填充结果（RunResult 是值类型，不是指针）
	result.ExitCode = runResult.ExitCode
	result.Stdout = runResult.Stdout
	result.Stderr = runResult.Stderr
	result.Output = runResult.Stdout
	if runResult.Stderr != "" {
		result.Output += "\n" + runResult.Stderr
	}
	result.TimedOut = runResult.TimedOut

	// 超时判断（优先于其他错误处理）
	if timeoutCtx.Err() == context.DeadlineExceeded || runResult.TimedOut {
		result.TimedOut = true
		result.ExitCode = -1
		result.Output += "\n[sandbox] 命令超时，已被终止"
	}

	// 先脱敏，再截断（避免截断后的提示信息泄漏）
	result.Output = safety.MaskSensitiveInfo(result.Output)
	result.Stdout = safety.MaskSensitiveInfo(result.Stdout)
	result.Stderr = safety.MaskSensitiveInfo(result.Stderr)

	// 输出大小限制（截断在脱敏之后）
	if opts.MaxOutput > 0 && len(result.Output) > opts.MaxOutput {
		result.Output = result.Output[:opts.MaxOutput]
		result.Truncated = true
		result.Output += "\n[sandbox] 输出已被截断"
	}

	return result, nil
}

// Close 清理资源。
func (s *LocalSandbox) Close() error {
	return nil
}
