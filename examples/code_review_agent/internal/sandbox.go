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
	Command        string `json:"command"`
	ExitCode       int    `json:"exit_code"`
	Stdout         string `json:"stdout"`
	Stderr         string `json:"stderr"`
	DurationMs     int64  `json:"duration_ms"`
	TimedOut       bool   `json:"timed_out"`
	Intercepted    bool   `json:"intercepted"`
	Decision       string `json:"decision,omitempty"`
	RuleID         string `json:"rule_id,omitempty"`
	RiskLevel      string `json:"risk_level,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
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
				Command:        "go vet ./...",
				Intercepted:    true,
				Decision:       string(scanReport.Decision),
				RuleID:         scanReport.RuleID,
				RiskLevel:      string(scanReport.RiskLevel),
				Recommendation: scanReport.Recommendation,
			}, fmt.Errorf("go vet 被安全策略拦截 (decision=%s): %s",
				scanReport.Decision, scanReport.Recommendation)
	}

	if se.dryRun {
		return SandboxResult{
			Command:  "go vet ./...",
			ExitCode: 0,
			Stdout:   "[dry-run] go vet 模拟执行成功",
			Decision: "allow",
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
	// 超时杀进程后，若子进程继续持有输出管道，Wait 会无限阻塞；
	// WaitDelay 强制关闭管道让调用方在超时后尽快返回。
	cmd.WaitDelay = time.Second

	var stdout, stderr limitedBuffer
	stdout.max = se.cfg.MaxOutputSize
	stderr.max = se.cfg.MaxOutputSize
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	exitCode := 0
	timedOut := false
	if err != nil {
		// CommandContext 超时杀进程时 Go 返回 *exec.ExitError，必须先判断
		// context 是否超时，否则 TimedOut 永远为 false。
		if timedCtx.Err() != nil {
			timedOut = true
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return SandboxResult{
		Command:    "go vet ./...",
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: elapsed,
		TimedOut:   timedOut,
		Decision:   "allow",
	}, nil
}

// RunCommand 在沙箱中执行任意命令。
func (se *SandboxExecutor) RunCommand(ctx context.Context, command string, dir string) (SandboxResult, error) {
	scanReport := se.gate.Check(command)
	if !isAllowed(scanReport.Decision) {
		return SandboxResult{
				Command:        command,
				Intercepted:    true,
				Decision:       string(scanReport.Decision),
				RuleID:         scanReport.RuleID,
				RiskLevel:      string(scanReport.RiskLevel),
				Recommendation: scanReport.Recommendation,
			}, fmt.Errorf("命令被安全策略拦截 (decision=%s): %s",
				scanReport.Decision, scanReport.Recommendation)
	}

	if se.dryRun {
		return SandboxResult{
			Command:  command,
			ExitCode: 0,
			Stdout:   fmt.Sprintf("[dry-run] 命令 %q 模拟执行成功", command),
			Decision: "allow",
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
	// 超时杀进程后，若子进程继续持有输出管道，Wait 会无限阻塞；
	// WaitDelay 强制关闭管道让调用方在超时后尽快返回。
	cmd.WaitDelay = time.Second

	var stdout, stderr limitedBuffer
	stdout.max = se.cfg.MaxOutputSize
	stderr.max = se.cfg.MaxOutputSize
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	exitCode := 0
	timedOut := false
	if err != nil {
		// 先判断 context 超时，再判断 ExitError（CommandContext 超时返回
		// ExitError 而非 context 错误）。
		if timedCtx.Err() != nil {
			timedOut = true
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return SandboxResult{
		Command:    command,
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: elapsed,
		TimedOut:   timedOut,
		Decision:   "allow",
	}, nil
}

// limitedBuffer 是有界输出缓冲区：超过 max 字节后丢弃多余内容并标记
// 截断，保证进程输出再大也不会撑爆内存（运行时限制，而非事后截断）。
type limitedBuffer struct {
	max       int
	buf       []byte
	truncated bool
}

// Write 实现 io.Writer，始终返回 len(p) 以免 exec 报错。
func (b *limitedBuffer) Write(p []byte) (int, error) {
	space := b.max - len(b.buf)
	if space > 0 {
		if len(p) <= space {
			b.buf = append(b.buf, p...)
		} else {
			b.buf = append(b.buf, p[:space]...)
			b.truncated = true
		}
	} else {
		b.truncated = true
	}
	return len(p), nil
}

// String 返回缓冲区内容，截断时追加标记。
func (b *limitedBuffer) String() string {
	if b.truncated {
		return string(b.buf) + "\n... (输出已截断)"
	}
	return string(b.buf)
}
