//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// SandboxConfig 控制沙箱执行行为。
type SandboxConfig struct {
	TimeoutSec    int // 超时时间（秒）
	MaxOutputSize int // 最大输出大小（字节）
}

// DefaultSandboxConfig 返回默认的沙箱配置。
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		TimeoutSec:    30,
		MaxOutputSize: 1024 * 1024, // 1MB
	}
}

// SandboxResult 表示沙箱执行的结果。
type SandboxResult struct {
	Command     string `json:"command"`
	ExitCode    int    `json:"exit_code"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	DurationMs  int64  `json:"duration_ms"`
	TimedOut    bool   `json:"timed_out"`
	Intercepted bool   `json:"intercepted"`
}

// SandboxExecutor 沙箱执行器，封装安全门禁和超时控制。
type SandboxExecutor struct {
	gate   *SafetyGate
	cfg    SandboxConfig
	dryRun bool
}

// NewSandboxExecutor 创建沙箱执行器。
func NewSandboxExecutor(cfg SandboxConfig, dryRun bool) *SandboxExecutor {
	return &SandboxExecutor{
		gate:   NewSafetyGate(),
		cfg:    cfg,
		dryRun: dryRun,
	}
}

// isAllowed 检查安全决策是否允许执行。
// 只允许 DecisionAllow；deny/ask/needs_human_review 全部拦截。
func isAllowed(d safety.Decision) bool {
	return d == safety.DecisionAllow
}

// RunGoVet 在指定目录运行 go vet。
func (se *SandboxExecutor) RunGoVet(ctx context.Context, dir string) (SandboxResult, error) {
	scanReport := se.gate.Check("go vet ./...")
	if !isAllowed(scanReport.Decision) {
		return SandboxResult{
			Command:     "go vet ./...",
			Intercepted: true,
		}, fmt.Errorf("go vet 被安全策略拦截 (decision=%s): %s",
			scanReport.Decision, scanReport.Recommendation)
	}

	if se.dryRun {
		return SandboxResult{
			Command:    "go vet ./...",
			ExitCode:   0,
			Stdout:     "[dry-run] go vet 模拟执行成功",
			DurationMs: 0,
		}, nil
	}

	// 创建超时 context
	timedCtx := ctx
	if se.cfg.TimeoutSec > 0 {
		var cancel context.CancelFunc
		timedCtx, cancel = context.WithTimeout(ctx, time.Duration(se.cfg.TimeoutSec)*time.Second)
		defer cancel()
	}

	start := time.Now()
	cmd := exec.CommandContext(timedCtx, "go", "vet", "./...")
	cmd.Dir = dir
	cmd.Env = os.Environ()

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	exitCode := 0
	timedOut := false
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if timedCtx.Err() != nil {
			timedOut = true
			exitCode = -1
		} else {
			exitCode = -1
		}
	}

	return SandboxResult{
		Command:    "go vet ./...",
		ExitCode:   exitCode,
		Stdout:     se.truncate(stdout.String()),
		Stderr:     se.truncate(stderr.String()),
		DurationMs: elapsed,
		TimedOut:   timedOut,
	}, nil
}

// RunCommand 在沙箱中执行任意命令。
func (se *SandboxExecutor) RunCommand(ctx context.Context, command string, dir string) (SandboxResult, error) {
	scanReport := se.gate.Check(command)
	if !isAllowed(scanReport.Decision) {
		return SandboxResult{
			Command:     command,
			Intercepted: true,
		}, fmt.Errorf("命令被安全策略拦截 (decision=%s): %s",
			scanReport.Decision, scanReport.Recommendation)
	}

	if se.dryRun {
		return SandboxResult{
			Command:    command,
			ExitCode:   0,
			Stdout:     fmt.Sprintf("[dry-run] 命令 %q 模拟执行成功", command),
			DurationMs: 0,
		}, nil
	}

	timedCtx := ctx
	if se.cfg.TimeoutSec > 0 {
		var cancel context.CancelFunc
		timedCtx, cancel = context.WithTimeout(ctx, time.Duration(se.cfg.TimeoutSec)*time.Second)
		defer cancel()
	}

	start := time.Now()
	cmd := exec.CommandContext(timedCtx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = os.Environ()

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	exitCode := 0
	timedOut := false
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if timedCtx.Err() != nil {
			timedOut = true
			exitCode = -1
		} else {
			exitCode = -1
		}
	}

	return SandboxResult{
		Command:    command,
		ExitCode:   exitCode,
		Stdout:     se.truncate(stdout.String()),
		Stderr:     se.truncate(stderr.String()),
		DurationMs: elapsed,
		TimedOut:   timedOut,
	}, nil
}

// truncate 截断输出不超过 MaxOutputSize。
func (se *SandboxExecutor) truncate(output string) string {
	if len(output) <= se.cfg.MaxOutputSize {
		return output
	}
	return output[:se.cfg.MaxOutputSize] + "\n... (输出已截断)"
}
