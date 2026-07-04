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
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
	Intercepted bool  `json:"intercepted"`
}

// SandboxExecutor 沙箱执行器，封装安全门禁和超时控制。
type SandboxExecutor struct {
	gate    *SafetyGate
	cfg     SandboxConfig
	dryRun  bool
}

// NewSandboxExecutor 创建沙箱执行器。
func NewSandboxExecutor(cfg SandboxConfig, dryRun bool) *SandboxExecutor {
	return &SandboxExecutor{
		gate:   NewSafetyGate(),
		cfg:    cfg,
		dryRun: dryRun,
	}
}

// RunGoVet 在指定目录运行 go vet。
// dir 应该是包含 Go 代码的目录路径。
func (se *SandboxExecutor) RunGoVet(ctx context.Context, dir string) (SandboxResult, error) {
	// 安全门禁检查
	scanReport := se.gate.Check("go vet ./...")
	if scanReport.Decision == safety.DecisionDeny {
		return SandboxResult{
			Command:    "go vet ./...",
			Intercepted: true,
		}, fmt.Errorf("go vet 被安全策略拒绝: %s", scanReport.Recommendation)
	}

	if se.dryRun {
		// Dry-run 模式：返回模拟结果
		return SandboxResult{
			Command:    "go vet ./...",
			ExitCode:   0,
			Stdout:     "[dry-run] go vet 模拟执行成功",
			DurationMs: 0,
		}, nil
	}

	start := time.Now()

	cmd := exec.CommandContext(ctx, "go", "vet", "./...")
	cmd.Dir = dir
	cmd.Env = os.Environ()

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
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
	}, nil
}

// RunCommand 在沙箱中执行任意命令。
// 所有命令执行前都会经过安全门禁检查。
func (se *SandboxExecutor) RunCommand(ctx context.Context, command string, dir string) (SandboxResult, error) {
	// 安全门禁检查
	scanReport := se.gate.Check(command)
	if scanReport.Decision == safety.DecisionDeny {
		return SandboxResult{
			Command:    command,
			Intercepted: true,
		}, fmt.Errorf("命令被安全策略拒绝: %s", scanReport.Recommendation)
	}

	if se.dryRun {
		return SandboxResult{
			Command:    command,
			ExitCode:   0,
			Stdout:     fmt.Sprintf("[dry-run] 命令 %q 模拟执行成功", command),
			DurationMs: 0,
		}, nil
	}

	start := time.Now()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
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
		} else if ctx.Err() != nil {
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
