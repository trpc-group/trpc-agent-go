// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package sandbox 提供代码审查的沙箱执行能力。
//
// 基于 trpc-agent-go 的 codeexecutor/local 实现本地执行，
// 后续可扩展为 Docker 容器或 E2B 云沙箱。
package sandbox

import (
	"context"
	"fmt"
	"time"
)

// Sandbox 定义沙箱执行的接口。
//
// 三种实现：
//   - LocalSandbox：本地执行（开发 fallback）
//   - ContainerSandbox：Docker 容器执行（生产方案）
//   - E2BSandbox：E2B 云沙箱（可选扩展）
type Sandbox interface {
	// Execute 在沙箱中执行命令。
	Execute(ctx context.Context, opts ExecuteOptions) (*ExecuteResult, error)

	// Name 返回沙箱后端名称（local / container / e2b）。
	Name() string

	// Close 清理资源。
	Close() error
}

// ExecuteOptions 执行选项。
type ExecuteOptions struct {
	Command   string            // 要执行的命令，如 "go test ./..."
	WorkDir   string            // 工作目录（代码目录）
	Env       map[string]string // 环境变量（白名单内的）
	Timeout   time.Duration     // 超时时间（默认 30s）
	MaxOutput int               // 最大输出字节数（默认 1MB）
}

// ExecuteResult 执行结果。
type ExecuteResult struct {
	ExitCode  int    // 退出码（0 = 成功）
	Stdout    string // 标准输出
	Stderr    string // 标准错误
	Output    string // 合并输出（stdout + stderr）
	Truncated bool   // 输出是否被截断
	Duration  string // 执行耗时
	TimedOut  bool   // 是否超时
	Backend   string // 执行后端（local / container）
}

// DefaultOptions 返回默认的执行选项。
func DefaultOptions() ExecuteOptions {
	return ExecuteOptions{
		Timeout:   30 * time.Second,
		MaxOutput: 1024 * 1024, // 1MB
	}
}

// Validate 验证执行选项。
func (opts *ExecuteOptions) Validate() error {
	if opts.Command == "" {
		return fmt.Errorf("命令不能为空")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MaxOutput <= 0 {
		opts.MaxOutput = 1024 * 1024
	}
	return nil
}
