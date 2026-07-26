package sandbox

import (
	"context"
	"testing"
	"time"
)

func TestNewExecutor(t *testing.T) {
	executor := NewExecutor("/tmp/test")

	if executor.repoPath != "/tmp/test" {
		t.Errorf("repoPath = %s, want /tmp/test", executor.repoPath)
	}

	if executor.config.ExecutorType != ExecutorLocal {
		t.Errorf("ExecutorType = %s, want local", executor.config.ExecutorType)
	}

	if executor.config.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %v, want 5m", executor.config.Timeout)
	}

	if executor.config.MaxOutputSize != 1024*1024 {
		t.Errorf("MaxOutputSize = %d, want 1048576", executor.config.MaxOutputSize)
	}
}

func TestNewExecutorWithType(t *testing.T) {
	executor := NewExecutorWithType("/tmp/test", ExecutorContainer)

	if executor.config.ExecutorType != ExecutorContainer {
		t.Errorf("ExecutorType = %s, want container", executor.config.ExecutorType)
	}
}

func TestDefaultSandboxConfig(t *testing.T) {
	config := DefaultSandboxConfig()

	if config.ExecutorType != ExecutorLocal {
		t.Errorf("ExecutorType = %s, want local", config.ExecutorType)
	}

	if config.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %v, want 5m", config.Timeout)
	}

	if config.MaxOutputSize != 1024*1024 {
		t.Errorf("MaxOutputSize = %d, want 1048576", config.MaxOutputSize)
	}

	if len(config.EnvWhitelist) == 0 {
		t.Error("EnvWhitelist should not be empty")
	}

	if len(config.ForbiddenDirs) == 0 {
		t.Error("ForbiddenDirs should not be empty")
	}
}

func TestExecutor_RunAllChecks_SandboxFailureDoesNotCrash(t *testing.T) {
	// 测试沙箱执行失败不会导致崩溃
	executor := NewExecutor("/nonexistent/path")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 即使路径不存在，也不应该 panic
	runs, err := executor.RunAllChecks(ctx, "task_test")

	// 可能返回错误或空结果，但不应该 panic
	if err != nil {
		t.Logf("RunAllChecks() error = %v (expected for nonexistent path)", err)
	}

	if runs != nil {
		t.Logf("RunAllChecks() returned %d runs", len(runs))
	}
}

func TestExecutor_RunLocalChecks_Timeout(t *testing.T) {
	// 测试超时控制
	config := DefaultSandboxConfig()
	config.Timeout = 1 * time.Millisecond // 极短超时

	executor := &Executor{
		repoPath: ".",
		config:   config,
	}

	ctx := context.Background()
	runs, err := executor.runLocalChecks(ctx, "task_timeout")

	// 超时不应该导致 panic
	if err != nil {
		t.Logf("runLocalChecks() error = %v", err)
	}

	if runs != nil {
		t.Logf("runLocalChecks() returned %d runs", len(runs))
	}
}

func TestTruncateOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxSize  int
		expected bool
	}{
		{
			name:     "short output",
			input:    "hello",
			maxSize:  100,
			expected: false,
		},
		{
			name:     "exact size",
			input:    "hello",
			maxSize:  5,
			expected: false,
		},
		{
			name:     "truncated",
			input:    "hello world",
			maxSize:  5,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateOutput(tt.input, tt.maxSize)
			isTruncated := len(result) > tt.maxSize || (len(result) == tt.maxSize+20 && result[len(result)-20:] == "\n... (truncated)")

			if tt.expected && !isTruncated {
				t.Errorf("expected truncation, got %q", result)
			}

			if !tt.expected && result != tt.input {
				t.Errorf("expected %q, got %q", tt.input, result)
			}
		})
	}
}

func TestBuildEnv(t *testing.T) {
	executor := NewExecutor(".")

	env := executor.buildEnv()

	if len(env) == 0 {
		t.Error("buildEnv() returned empty env")
	}

	// 检查是否包含 HOME
	found := false
	for _, e := range env {
		if e == "HOME=/tmp/golens-sandbox" {
			found = true
			break
		}
	}

	if !found {
		t.Error("buildEnv() should set HOME=/tmp/golens-sandbox")
	}
}

func TestIsCommandAvailable(t *testing.T) {
	// go 命令应该可用
	if !isCommandAvailable("go") {
		t.Error("go command should be available")
	}

	// 不存在的命令应该不可用
	if isCommandAvailable("nonexistent_command_xyz") {
		t.Error("nonexistent command should not be available")
	}
}
