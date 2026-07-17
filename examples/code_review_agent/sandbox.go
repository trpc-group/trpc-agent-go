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
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SandboxRunner runs deterministic review commands after permission checks.
type SandboxRunner interface {
	Run(ctx context.Context, taskID string, commands []string, gate *CommandGate) ([]SandboxRun, error)
	Close() error
}

type engineRunner struct {
	runtime     string
	exec        codeexecutor.CodeExecutor
	timeout     time.Duration
	outputLimit int64
	repoPath    string
	skillsRoot  string
	dryRun      bool
}

type fakeRunner struct {
	runtime     string
	outputLimit int64
	fail        bool
	setupErr    bool
	timeout     bool
}

// NewSandboxRunner creates the configured sandbox runtime.
func NewSandboxRunner(opts ReviewOptions) (SandboxRunner, error) {
	outputLimit := opts.OutputLimit
	if outputLimit <= 0 {
		outputLimit = 10 << 20
	}
	timeout := opts.SandboxTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	switch strings.ToLower(opts.Runtime) {
	case "", "container":
		return newContainerRunner(opts, timeout, outputLimit)
	case "e2b":
		return newE2BRunner(opts, timeout, outputLimit)
	case "local":
		if !opts.AllowTrustedLocal {
			return nil, errors.New("local runtime requires --allow-trusted-local")
		}
		return &engineRunner{runtime: "local", exec: localexec.New(localexec.WithTimeout(timeout)), timeout: timeout, outputLimit: outputLimit, repoPath: opts.RepoPath, skillsRoot: opts.SkillsRoot, dryRun: opts.DryRun}, nil
	case "fake":
		return &fakeRunner{runtime: "fake", outputLimit: outputLimit, fail: opts.Fixture == "sandbox_failure", setupErr: opts.Fixture == "sandbox_setup_failure"}, nil
	default:
		return nil, fmt.Errorf("unknown runtime %q", opts.Runtime)
	}
}

func (r *engineRunner) Run(ctx context.Context, taskID string, commands []string, gate *CommandGate) ([]SandboxRun, error) {
	if r == nil {
		return nil, errors.New("sandbox runner is not configured")
	}
	if r.exec == nil && !r.dryRun {
		return nil, errors.New("sandbox runner is not configured")
	}
	ctx, span := otel.Tracer("trpc-agent-go/examples/code_review_agent").Start(ctx, "code_review_agent.sandbox")
	defer span.End()
	span.SetAttributes(attribute.String("runtime", r.runtime), attribute.Int("commands", len(commands)))
	if r.dryRun {
		return r.runDryRun(ctx, taskID, commands, gate), nil
	}
	provider, ok := r.exec.(codeexecutor.EngineProvider)
	if !ok {
		return nil, errors.New("executor does not expose workspace engine")
	}
	eng := provider.Engine()
	if eng == nil {
		return nil, errors.New("executor returned nil workspace engine")
	}
	ws, err := eng.Manager().CreateWorkspace(ctx, "code-review-"+taskID, codeexecutor.WorkspacePolicy{Isolated: true})
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := eng.Manager().Cleanup(cleanupCtx, ws); err != nil {
			span.RecordError(err)
		}
	}()
	if r.repoPath != "" {
		if err := eng.FS().StageDirectory(ctx, ws, r.repoPath, "repo", codeexecutor.StageOptions{ReadOnly: true, AllowMount: true}); err != nil {
			return nil, fmt.Errorf("stage repo: %w", err)
		}
	}
	if r.skillsRoot != "" {
		if err := eng.FS().StageDirectory(ctx, ws, r.skillsRoot, "skills", codeexecutor.StageOptions{ReadOnly: true, AllowMount: true}); err != nil {
			return nil, fmt.Errorf("stage skills: %w", err)
		}
	}
	runs := make([]SandboxRun, 0, len(commands))
	for _, command := range commands {
		run, err := r.runOne(ctx, eng, ws, taskID, command, gate)
		runs = append(runs, run)
		if err != nil {
			return runs, nil
		}
	}
	return runs, nil
}

func (r *engineRunner) runOne(ctx context.Context, eng codeexecutor.Engine, ws codeexecutor.Workspace, taskID string, command string, gate *CommandGate) (SandboxRun, error) {
	start := time.Now().UTC()
	run := SandboxRun{TaskID: taskID, Runtime: r.runtime, Command: command, Status: "skipped", StartedAt: start}
	decision, err := gate.Check(ctx, taskID, command)
	if err != nil {
		run.Status = "failed"
		run.ErrorType = "permission_error"
		run.CompletedAt = time.Now().UTC()
		return run, err
	}
	if decision.Action != tool.PermissionActionAllow {
		run.Status = string(decision.Action)
		run.ErrorType = "permission_" + string(decision.Action)
		run.CompletedAt = time.Now().UTC()
		return run, errors.New(decision.Reason)
	}
	if r.dryRun {
		run.Status = "dry_run"
		run.Output = "dry-run: command permitted but not executed"
		run.CompletedAt = time.Now().UTC()
		run.Duration = run.CompletedAt.Sub(start)
		return run, nil
	}
	spec := r.commandSpec(command)
	rr, err := eng.Runner().RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:      spec.cmd,
		Args:     spec.args,
		Cwd:      spec.cwd,
		CleanEnv: true,
		Env: map[string]string{
			"HOME": "/tmp",
			"PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
		},
		Timeout: r.timeout,
	})
	run.CompletedAt = time.Now().UTC()
	run.Duration = rr.Duration
	run.ExitCode = rr.ExitCode
	run.TimedOut = rr.TimedOut
	run.Output, run.Truncated = limitOutput(RedactSecrets(rr.Stdout+rr.Stderr), r.outputLimit)
	if err != nil {
		run.Status = "failed"
		if rr.TimedOut {
			run.ErrorType = "timeout"
		} else {
			run.ErrorType = "execution_error"
		}
		return run, err
	}
	run.Status = "completed"
	return run, nil
}

func (r *engineRunner) runDryRun(ctx context.Context, taskID string, commands []string, gate *CommandGate) []SandboxRun {
	runs := make([]SandboxRun, 0, len(commands))
	for _, command := range commands {
		start := time.Now().UTC()
		run := SandboxRun{TaskID: taskID, Runtime: r.runtime, Command: command, Status: "skipped", StartedAt: start}
		decision, err := gate.Check(ctx, taskID, command)
		run.CompletedAt = time.Now().UTC()
		run.Duration = run.CompletedAt.Sub(start)
		if err != nil {
			run.Status = "failed"
			run.ErrorType = "permission_error"
			runs = append(runs, run)
			continue
		}
		if decision.Action != tool.PermissionActionAllow {
			run.Status = string(decision.Action)
			run.ErrorType = "permission_" + string(decision.Action)
			runs = append(runs, run)
			continue
		}
		run.Status = "dry_run"
		run.Output = "dry-run: command permitted but not executed"
		runs = append(runs, run)
	}
	return runs
}

type sandboxCommandSpec struct {
	cmd  string
	args []string
	cwd  string
}

func (r *engineRunner) commandSpec(command string) sandboxCommandSpec {
	cwd := "."
	if r.repoPath != "" {
		cwd = "repo"
	}
	switch command {
	case "go test ./...":
		return sandboxCommandSpec{cmd: "go", args: []string{"test", "./..."}, cwd: cwd}
	case "go vet ./...":
		return sandboxCommandSpec{cmd: "go", args: []string{"vet", "./..."}, cwd: cwd}
	case skillScriptCommand:
		scriptPath := "skills/code-review/scripts/run_go_checks.sh"
		if r.repoPath != "" {
			scriptPath = "../" + scriptPath
		}
		return sandboxCommandSpec{cmd: "bash", args: []string{scriptPath}, cwd: cwd}
	default:
		return sandboxCommandSpec{cmd: "false", cwd: cwd}
	}
}

func (r *engineRunner) Close() error {
	if r == nil || r.exec == nil {
		return nil
	}
	if c, ok := r.exec.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

func (r *fakeRunner) Run(ctx context.Context, taskID string, commands []string, gate *CommandGate) ([]SandboxRun, error) {
	_, span := otel.Tracer("trpc-agent-go/examples/code_review_agent").Start(ctx, "code_review_agent.sandbox.fake")
	defer span.End()
	span.SetAttributes(attribute.Int("commands", len(commands)))
	if r.setupErr {
		return []SandboxRun{}, errors.New("fake sandbox setup failure")
	}
	runs := make([]SandboxRun, 0, len(commands))
	for _, command := range commands {
		start := time.Now().UTC()
		run := SandboxRun{TaskID: taskID, Runtime: r.runtime, Command: command, StartedAt: start}
		decision, err := gate.Check(ctx, taskID, command)
		if err != nil {
			run.Status = "failed"
			run.ErrorType = "permission_error"
			run.CompletedAt = time.Now().UTC()
			runs = append(runs, run)
			continue
		}
		if decision.Action != tool.PermissionActionAllow {
			run.Status = string(decision.Action)
			run.ErrorType = "permission_" + string(decision.Action)
			run.CompletedAt = time.Now().UTC()
			runs = append(runs, run)
			continue
		}
		run.ExitCode = 0
		run.Status = "completed"
		run.Output = "ok"
		if r.fail || strings.Contains(command, "run_go_checks") && os.Getenv("CODE_REVIEW_AGENT_FAKE_FAIL") == "1" {
			run.ExitCode = 1
			run.Status = "failed"
			run.ErrorType = "execution_error"
			run.Output = "fake sandbox failure"
		}
		if r.timeout {
			run.Status = "failed"
			run.TimedOut = true
			run.ErrorType = "timeout"
		}
		run.Output, run.Truncated = limitOutput(run.Output, r.outputLimit)
		run.CompletedAt = time.Now().UTC()
		run.Duration = run.CompletedAt.Sub(start)
		runs = append(runs, run)
	}
	return runs, nil
}

func (r *fakeRunner) Close() error { return nil }

func limitOutput(out string, max int64) (string, bool) {
	if max <= 0 || int64(len(out)) <= max {
		return out, false
	}
	const marker = "\n[output truncated]"
	if max < 32 {
		return out[:max], true
	}
	keep := max - int64(len(marker))
	if keep < 0 {
		keep = 0
	}
	return out[:keep] + marker, true
}
