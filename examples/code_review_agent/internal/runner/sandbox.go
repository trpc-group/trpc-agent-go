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
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

// SandboxConfig configures the sandbox execution environment.
type SandboxConfig struct {
	DefaultBackend  string
	DefaultTimeout  time.Duration
	MaxOutputBytes  int64
	EnvWhitelist    []string
	AllowedCommands []string
	DeniedCommands  []string
}

// DefaultSandboxConfig returns a safe default sandbox configuration.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		DefaultBackend:  "local",
		DefaultTimeout:  120 * time.Second,
		MaxOutputBytes:  10 << 20,
		EnvWhitelist:    []string{"PATH", "HOME", "GOPATH", "GOROOT"},
		AllowedCommands: []string{"go", "gofmt", "git", "cat", "echo", "ls", "diff"},
		DeniedCommands:  []string{"rm", "dd", "curl", "wget", "sudo", "apt", "npm", "pip"},
	}
}

// SandboxManager manages sandbox execution for code review checks.
type SandboxManager struct {
	engine codeexecutor.Engine
	config SandboxConfig
}

// NewSandboxManager creates a new sandbox manager.
func NewSandboxManager(engine codeexecutor.Engine, config SandboxConfig) *SandboxManager {
	return &SandboxManager{engine: engine, config: config}
}

// SandboxCheck describes a single check to run in the sandbox.
type SandboxCheck struct {
	TaskID      string
	Cmd         string
	Args        []string
	Cwd         string
	Files       map[string][]byte
	Timeout     time.Duration
	OutputLimit int64
}

// RunCheck runs a check in the sandbox and returns the result.
func (m *SandboxManager) RunCheck(ctx context.Context, check SandboxCheck) (*finding.SandboxRun, error) {
	if m.engine == nil {
		return nil, fmt.Errorf("sandbox engine not configured")
	}

	startTime := time.Now()

	timeout := m.config.DefaultTimeout
	if check.Timeout > 0 {
		timeout = check.Timeout
	}

	ws, err := m.engine.Manager().CreateWorkspace(ctx, check.TaskID, codeexecutor.WorkspacePolicy{})
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	// Copy files into sandbox if provided.
	if len(check.Files) > 0 {
		var putFiles []codeexecutor.PutFile
		for path, content := range check.Files {
			putFiles = append(putFiles, codeexecutor.PutFile{Path: path, Content: content})
		}
		if err := m.engine.FS().PutFiles(ctx, ws, putFiles); err != nil {
			_ = m.engine.Manager().Cleanup(ctx, ws)
			return nil, fmt.Errorf("put files: %w", err)
		}
	}

	// Run the program in sandbox.
	result, err := m.engine.Runner().RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     check.Cmd,
		Args:    check.Args,
		Cwd:     check.Cwd,
		Timeout: timeout,
	})

	durationMs := time.Since(startTime).Milliseconds()

	sandboxRun := &finding.SandboxRun{
		TaskID:     check.TaskID,
		Command:    check.Cmd + " " + strings.Join(check.Args, " "),
		Backend:    m.config.DefaultBackend,
		ExitCode:   -1,
		DurationMs: durationMs,
		CreatedAt:  startTime,
	}

	if err != nil {
		sandboxRun.Error = err.Error()
	}
	if result.TimedOut {
		sandboxRun.Timeout = true
	}
	if result.ExitCode >= 0 {
		sandboxRun.ExitCode = result.ExitCode
	}

	sandboxRun.StdoutSummary = m.truncateOutput(result.Stdout)
	sandboxRun.StderrSummary = m.truncateOutput(result.Stderr)

	// Best-effort cleanup.
	_ = m.engine.Manager().Cleanup(ctx, ws)

	return sandboxRun, nil
}

// IsCommandAllowed checks if a command is permitted by the sandbox policy.
func (m *SandboxManager) IsCommandAllowed(cmd string) bool {
	cmdName := extractCommandName(cmd)
	if cmdName == "" {
		return false
	}
	for _, denied := range m.config.DeniedCommands {
		if cmdName == denied {
			return false
		}
	}
	if len(m.config.AllowedCommands) == 0 {
		return true
	}
	for _, allowed := range m.config.AllowedCommands {
		if cmdName == allowed {
			return true
		}
	}
	return false
}

func (m *SandboxManager) truncateOutput(output string) string {
	if int64(len(output)) <= m.config.MaxOutputBytes {
		return output
	}
	return output[:m.config.MaxOutputBytes] + fmt.Sprintf("\n... (truncated %d bytes)", len(output)-int(m.config.MaxOutputBytes))
}

func extractCommandName(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if idx := strings.Index(cmd, " "); idx > 0 {
		return cmd[:idx]
	}
	return cmd
}
