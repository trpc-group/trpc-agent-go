//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	commandTimeout = 45 * time.Second
	maxRunOutput   = 64 << 10
	maxWorkspace   = 128 << 20
)

// SandboxResult contains command audit records even when individual checks
// fail. A failed check does not abort the review.
type SandboxResult struct {
	Runs      []SandboxRun
	Decisions []PermissionDecision
}

// ReviewSandbox is the isolated execution boundary used by the reviewer.
type ReviewSandbox interface {
	RunChecks(
		ctx context.Context,
		taskID string,
		diff []byte,
		repoPath string,
		skillPath string,
		staticcheck bool,
	) (SandboxResult, error)
	Close() error
}

// WorkspaceSandbox runs governed checks through a codeexecutor.Engine.
type WorkspaceSandbox struct {
	engine codeexecutor.Engine
	policy *CommandPermissionPolicy
	close  func() error
	env    map[string]string
}

// NewWorkspaceSandbox constructs an isolated workspace check runner.
func NewWorkspaceSandbox(
	engine codeexecutor.Engine,
	closeFn func() error,
	env map[string]string,
) (*WorkspaceSandbox, error) {
	if engine == nil {
		return nil, errors.New("workspace engine is required")
	}
	if !engine.Describe().SupportsCleanEnv {
		return nil, errors.New("workspace engine does not enforce clean environments")
	}
	return &WorkspaceSandbox{
		engine: engine,
		policy: NewCommandPermissionPolicy(),
		close:  closeFn,
		env:    cloneEnv(env),
	}, nil
}

// Close releases the underlying sandbox runtime.
func (s *WorkspaceSandbox) Close() error {
	if s == nil || s.close == nil {
		return nil
	}
	return s.close()
}

// RunChecks stages bounded inputs and executes each allowed command.
func (s *WorkspaceSandbox) RunChecks(
	ctx context.Context,
	taskID string,
	diff []byte,
	repoPath string,
	skillPath string,
	staticcheck bool,
) (SandboxResult, error) {
	if s == nil || s.engine == nil {
		return SandboxResult{}, errors.New("workspace sandbox is not initialized")
	}
	workspace, err := s.engine.Manager().CreateWorkspace(
		ctx, taskID,
		codeexecutor.WorkspacePolicy{
			Isolated: true, Persist: false, MaxDiskBytes: maxWorkspace,
		},
	)
	if err != nil {
		return SandboxResult{}, fmt.Errorf("create sandbox workspace: %w", err)
	}
	defer func() { _ = s.engine.Manager().Cleanup(context.Background(), workspace) }()

	if err := s.engine.FS().PutFiles(
		ctx, workspace,
		[]codeexecutor.PutFile{{
			Path: "work/input.diff", Content: diff, Mode: 0o644,
		}},
	); err != nil {
		return SandboxResult{}, fmt.Errorf("stage diff: %w", err)
	}
	if err := s.engine.FS().StageDirectory(
		ctx, workspace, skillPath, "skills/code-review",
		codeexecutor.StageOptions{ReadOnly: false},
	); err != nil {
		return SandboxResult{}, fmt.Errorf("stage code-review skill: %w", err)
	}

	commands := []checkCommand{{
		Name: "bash",
		Args: []string{reviewScriptPath, "work/input.diff"},
		Cwd:  ".",
		Env:  s.env,
	}}
	var cleanupSnapshot func()
	if repoPath != "" {
		snapshot, cleanup, err := createRepoSnapshot(repoPath)
		if err != nil {
			return SandboxResult{}, fmt.Errorf("prepare repository snapshot: %w", err)
		}
		cleanupSnapshot = cleanup
		defer cleanupSnapshot()
		if err := s.engine.FS().StageDirectory(
			ctx, workspace, snapshot, "work/repo",
			codeexecutor.StageOptions{ReadOnly: false},
		); err != nil {
			return SandboxResult{}, fmt.Errorf("stage repository: %w", err)
		}
		commands = append(commands,
			checkCommand{
				Name: "go", Args: []string{"test", "./..."},
				Cwd: "work/repo", Env: s.env,
			},
			checkCommand{
				Name: "go", Args: []string{"vet", "./..."},
				Cwd: "work/repo", Env: s.env,
			},
		)
		if staticcheck {
			commands = append(commands, checkCommand{
				Name: "staticcheck", Args: []string{"./..."},
				Cwd: "work/repo", Env: s.env,
			})
		}
	}

	var result SandboxResult
	for _, command := range commands {
		decision, risk, err := s.checkPermission(ctx, command)
		if err != nil {
			return result, err
		}
		result.Decisions = append(result.Decisions, PermissionDecision{
			Tool:      "workspace_exec",
			Command:   command.String(),
			Action:    string(decision.Action),
			Reason:    decision.Reason,
			Risk:      risk,
			CreatedAt: time.Now().UTC(),
		})
		if decision.Action != tool.PermissionActionAllow {
			result.Runs = append(result.Runs, SandboxRun{
				Command:      command.String(),
				Status:       "blocked",
				ErrorType:    "permission_" + string(decision.Action),
				ErrorMessage: decision.Reason,
			})
			continue
		}
		result.Runs = append(
			result.Runs,
			s.runCommand(ctx, workspace, command),
		)
	}
	return result, nil
}

func (s *WorkspaceSandbox) checkPermission(
	ctx context.Context,
	command checkCommand,
) (tool.PermissionDecision, string, error) {
	arguments, err := json.Marshal(command)
	if err != nil {
		return tool.PermissionDecision{}, "", err
	}
	decision, err := s.policy.CheckToolPermission(
		ctx,
		&tool.PermissionRequest{
			ToolName:  "workspace_exec",
			Arguments: arguments,
		},
	)
	if err != nil {
		return tool.PermissionDecision{}, "", err
	}
	normalized, err := tool.NormalizePermissionDecision(decision)
	if err != nil {
		return tool.PermissionDecision{}, "", err
	}
	_, risk := s.policy.Evaluate(command)
	return normalized, risk, nil
}

func (s *WorkspaceSandbox) runCommand(
	ctx context.Context,
	workspace codeexecutor.Workspace,
	command checkCommand,
) SandboxRun {
	started := time.Now()
	result, err := s.engine.Runner().RunProgram(
		ctx, workspace,
		codeexecutor.RunProgramSpec{
			Cmd: command.Name, Args: command.Args, Env: cloneEnv(command.Env),
			CleanEnv: true, Cwd: command.Cwd, Timeout: commandTimeout,
			Limits: codeexecutor.ResourceLimits{
				CPUPercent: 200, MemoryMB: 1024, MaxPIDs: 128,
			},
		},
	)
	run := SandboxRun{
		Command:    command.String(),
		ExitCode:   result.ExitCode,
		DurationMS: time.Since(started).Milliseconds(),
		TimedOut:   result.TimedOut,
		Output:     boundedOutput(result.Stdout, result.Stderr),
		Status:     "passed",
	}
	if result.TimedOut {
		run.Status = "failed"
		run.ErrorType = "timeout"
		run.ErrorMessage = fmt.Sprintf("command exceeded %s timeout", commandTimeout)
	} else if err != nil || result.ExitCode != 0 {
		run.Status = "failed"
		run.ErrorType = "command_failed"
		if err != nil {
			run.ErrorMessage = boundedText(Redact(err.Error()), maxRunOutput)
		} else {
			run.ErrorMessage = fmt.Sprintf(
				"command exited with status %d", result.ExitCode,
			)
		}
	}
	return run
}

// FakeSandbox exercises orchestration without Docker or model credentials.
type FakeSandbox struct {
	FailCommand string
}

// RunChecks simulates the same governed command set deterministically.
func (s *FakeSandbox) RunChecks(
	_ context.Context,
	_ string,
	_ []byte,
	repoPath string,
	_ string,
	staticcheck bool,
) (SandboxResult, error) {
	commands := []checkCommand{{
		Name: "bash", Args: []string{reviewScriptPath, "work/input.diff"},
	}}
	if repoPath != "" {
		commands = append(commands,
			checkCommand{Name: "go", Args: []string{"test", "./..."}},
			checkCommand{Name: "go", Args: []string{"vet", "./..."}},
		)
		if staticcheck {
			commands = append(
				commands,
				checkCommand{Name: "staticcheck", Args: []string{"./..."}},
			)
		}
	}
	policy := NewCommandPermissionPolicy()
	result := SandboxResult{}
	for _, command := range commands {
		decision, risk := policy.Evaluate(command)
		result.Decisions = append(result.Decisions, PermissionDecision{
			Tool: "workspace_exec", Command: command.String(),
			Action: string(decision.Action), Reason: decision.Reason,
			Risk: risk, CreatedAt: time.Now().UTC(),
		})
		run := SandboxRun{
			Command: command.String(), Status: "passed",
			DurationMS: 1,
		}
		if s != nil && command.Name == s.FailCommand {
			run.Status = "failed"
			run.ExitCode = 1
			run.ErrorType = "command_failed"
			run.ErrorMessage = "simulated sandbox failure"
		}
		result.Runs = append(result.Runs, run)
	}
	return result, nil
}

// Close implements ReviewSandbox.
func (s *FakeSandbox) Close() error {
	return nil
}

func boundedOutput(stdout, stderr string) string {
	var output strings.Builder
	if stdout != "" {
		output.WriteString(stdout)
	}
	if stderr != "" {
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(stderr)
	}
	return boundedText(Redact(output.String()), maxRunOutput)
}

func boundedText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "\n[output truncated]"
}

func cloneEnv(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
