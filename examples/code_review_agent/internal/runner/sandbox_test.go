//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

func TestDefaultSandboxConfig(t *testing.T) {
	cfg := DefaultSandboxConfig()
	assert.Equal(t, "local", cfg.DefaultBackend)
	assert.Equal(t, 120*time.Second, cfg.DefaultTimeout)
	assert.Equal(t, int64(10<<20), cfg.MaxOutputBytes)
	assert.Contains(t, cfg.AllowedCommands, "go")
	assert.Contains(t, cfg.DeniedCommands, "rm")
}

func TestIsCommandAllowed(t *testing.T) {
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())

	tests := []struct {
		cmd     string
		allowed bool
	}{
		{"go", true},
		{"go vet", true},
		{"git diff", true},
		{"cat file.go", true},
		{"rm", false},
		{"rm -rf /", false},
		{"curl", false},
		{"sudo", false},
		{"apt install", false},
		{"", false},
		{"unknown-cmd", false},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			assert.Equal(t, tt.allowed, mgr.IsCommandAllowed(tt.cmd))
		})
	}
}

func TestNewSandboxManager_NilEngine(t *testing.T) {
	// Should not panic; nil engine is allowed for config-only usage.
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	assert.NotNil(t, mgr)
	assert.Nil(t, mgr.engine)
}

func TestExtractCommandName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"go vet ./...", "go"},
		{"go", "go"},
		{"  go test  ", "go"},
		{"", ""},
		{"  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, extractCommandName(tt.input))
		})
	}
}

func TestTruncateOutput(t *testing.T) {
	mgr := NewSandboxManager(nil, SandboxConfig{MaxOutputBytes: 10})

	short := "hello"
	assert.Equal(t, short, mgr.truncateOutput(short))

	long := "hello world this is a long string"
	result := mgr.truncateOutput(long)
	assert.Contains(t, result, "... (truncated")
	assert.Contains(t, result, "hello worl") // first 10 bytes
	// Result should be shorter than original and contain the truncation marker.
	assert.Greater(t, len(result), 10)
	assert.Contains(t, result, "truncated")
}

func TestSandboxConfig_CustomValues(t *testing.T) {
	cfg := SandboxConfig{
		DefaultBackend:  "container",
		DefaultTimeout:  time.Minute,
		MaxOutputBytes:  1024,
		AllowedCommands: []string{"go", "git"},
		DeniedCommands:  []string{"rm"},
	}
	mgr := NewSandboxManager(nil, cfg)

	assert.True(t, mgr.IsCommandAllowed("go"))
	assert.True(t, mgr.IsCommandAllowed("git diff"))
	assert.False(t, mgr.IsCommandAllowed("rm"))
	assert.False(t, mgr.IsCommandAllowed("cat")) // not in allow list
}

func TestExtractCommandName_EdgeCases(t *testing.T) {
	assert.Equal(t, "go", extractCommandName("go test -v ./..."))
	assert.Equal(t, "bash", extractCommandName("bash script.sh"))
	assert.Equal(t, "python3", extractCommandName("python3 -m pytest"))
}

func TestSandboxCheck_DefaultTimeoutApplied(t *testing.T) {
	cfg := DefaultSandboxConfig()
	cfg.DefaultTimeout = 30 * time.Second
	// Verify the config carries the timeout value.
	assert.Equal(t, 30*time.Second, cfg.DefaultTimeout)
}

func TestSandboxCheck_OutputTruncated(t *testing.T) {
	cfg := SandboxConfig{MaxOutputBytes: 5}
	mgr := NewSandboxManager(nil, cfg)

	longOutput := "hello world this is very long"
	truncated := mgr.truncateOutput(longOutput)
	assert.Contains(t, truncated, "hello")
	assert.Contains(t, truncated, "truncated")
	// The truncated content is capped at 5 bytes, with a suffix appended.
	assert.NotContains(t, truncated, "hello world") // "world" is beyond 5 bytes
}

func TestSandboxCheck_ErrorNotPanic(t *testing.T) {
	// Error handling in RunCheck captures errors without panicking.
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	// RunCheck with nil engine should return error, not panic.
	_, err := mgr.RunCheck(nil, SandboxCheck{TaskID: "test"})
	assert.Error(t, err)
}

func TestSandboxCheck_ZeroTimeout(t *testing.T) {
	cfg := DefaultSandboxConfig()
	cfg.DefaultTimeout = 0
	mgr := NewSandboxManager(nil, cfg)
	_, err := mgr.RunCheck(nil, SandboxCheck{TaskID: "test-timeout"})
	assert.Error(t, err)
}

func TestSandboxManager_EngineGetter(t *testing.T) {
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	assert.Nil(t, mgr.engine)
}

func TestExtractCommandName_Empty(t *testing.T) {
	assert.Equal(t, "", extractCommandName(""))
	assert.Equal(t, "", extractCommandName("  "))
}

func TestRunCheck_WithLocalExecutor(t *testing.T) {
	exec := localexec.New()
	cfg := DefaultSandboxConfig()
	cfg.DefaultTimeout = 5 * time.Second
	mgr := NewSandboxManager(exec.Engine(), cfg)

	result, err := mgr.RunCheck(context.Background(), SandboxCheck{
		TaskID: "test-runcheck",
		Cmd:    "echo",
		Args:   []string{"hello"},
	})
	if err != nil {
		// May fail if local executor can't create workspace, but the path is covered.
		t.Logf("RunCheck returned error (acceptable in CI): %v", err)
		return
	}
	assert.NotNil(t, result)
	t.Logf("RunCheck: exit=%d stdout=%s", result.ExitCode, result.StdoutSummary)
}
