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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// allowSleepScanner 返回放行 sleep 命令的扫描器（默认 allowlist 无 sleep，
// 且 sleep 长睡眠规则会 deny，测试需显式放行）。
func allowSleepScanner() *safety.Scanner {
	p := safety.DefaultPolicy()
	p.AllowedCommands = append(p.AllowedCommands, "sleep")
	p.Rules = nil
	return safety.NewScanner(p)
}

// TestRunCommand_FailureDoesNotCrash 验证沙箱命令失败不会导致整个评审任务崩溃
// （验收标准 4）。
func TestRunCommand_FailureDoesNotCrash(t *testing.T) {
	se := NewSandboxExecutor(DefaultSandboxConfig(), false)
	res, err := se.RunCommand(context.Background(), "ls /nonexistent-dir-xyz", t.TempDir())
	if err != nil {
		t.Fatalf("命令失败不应导致错误返回（由调用方处理 exit code）: %v", err)
	}
	if res.Intercepted {
		t.Fatal("ls 不应被安全策略拦截")
	}
	if res.ExitCode == 0 {
		t.Error("ls 不存在的目录应返回非零 exit code")
	}
}

// TestRunCommand_TimedOut 验证超时被正确识别并报告（A2）。
func TestRunCommand_TimedOut(t *testing.T) {
	cfg := DefaultSandboxConfig()
	cfg.TimeoutSec = 1
	se := NewSandboxExecutor(cfg, false)
	se.gate = &SafetyGate{scanner: allowSleepScanner()}
	res, err := se.RunCommand(context.Background(), "sleep 30", t.TempDir())
	if err != nil {
		t.Fatalf("超时命令不应导致错误返回: %v", err)
	}
	if !res.TimedOut {
		t.Errorf("应识别为超时, 实际 TimedOut=%v exit=%d", res.TimedOut, res.ExitCode)
	}
	// 超时在 ~1s 触发；WaitDelay 兜底使整体耗时不超过几秒，
	// 若超过 15s 说明 Wait 仍被孤儿进程阻塞。
	if res.DurationMs < 900 || res.DurationMs > 15000 {
		t.Errorf("超时应在 ~1s 触发且不被孤儿进程阻塞, 实际耗时 %dms", res.DurationMs)
	}
}

// TestRunCommand_Intercepted 验证高风险命令被安全门禁拦截（验收标准 7）。
func TestRunCommand_Intercepted(t *testing.T) {
	se := NewSandboxExecutor(DefaultSandboxConfig(), true)
	res, err := se.RunCommand(context.Background(), "sudo rm -rf /", t.TempDir())
	if err == nil {
		t.Fatal("高风险命令应被拦截并返回错误")
	}
	if !res.Intercepted {
		t.Error("拦截命令应标记 Intercepted")
	}
	if res.Decision != "deny" {
		t.Errorf("sudo rm -rf 应被 deny, 实际 decision=%s", res.Decision)
	}
}

// TestRunCommand_OutputLimited verifies output is bounded at run time and
// truncated past the limit (A1).
func TestRunCommand_OutputLimited(t *testing.T) {
	dir := t.TempDir()
	// Deterministic large file (host-path independent); cat output must be
	// truncated while the process is running.
	bigFile := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(bigFile, []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	cfg := DefaultSandboxConfig()
	cfg.MaxOutputSize = 128
	se := NewSandboxExecutor(cfg, false)
	// Relative path keeps the temp directory's random name out of the
	// command, which would otherwise trip the sensitive_leak rule.
	res, err := se.RunCommand(context.Background(), "cat big.txt", dir)
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	if res.Intercepted {
		t.Fatal("cat must not be intercepted")
	}
	// Truncated output = MaxOutputSize bytes + the truncation marker, never more.
	const marker = "\n... (输出已截断)"
	if len(res.Stdout) > cfg.MaxOutputSize+len(marker) {
		t.Errorf("output exceeds configured limit: len=%d > %d",
			len(res.Stdout), cfg.MaxOutputSize+len(marker))
	}
	if !strings.Contains(res.Stdout, "输出已截断") {
		t.Error("truncated output must carry the truncation marker")
	}
	if len(res.Stdout) == 0 {
		t.Error("truncated buffer must not be empty")
	}
}

// TestRunGoVet_DryRun 验证 dry-run 模式不执行真实命令。
func TestRunGoVet_DryRun(t *testing.T) {
	se := NewSandboxExecutor(DefaultSandboxConfig(), true)
	res, err := se.RunGoVet(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("dry-run 不应失败: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("dry-run exit code 应为 0, 实际 %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "dry-run") {
		t.Errorf("dry-run 输出应包含标记, 实际 %q", res.Stdout)
	}
}
