//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	rootskill "trpc.group/trpc-go/trpc-agent-go/skill"
	toolskill "trpc.group/trpc-go/trpc-agent-go/tool/skill"
)

const (
	sandboxOutputBytes       = 16 * 1024
	staticcheckSkippedMarker = "staticcheck skipped"
)

type skillRunSandboxRunner struct {
	runtimeName string
	skillName   string
	runTool     *toolskill.RunTool
	close       func() error
}

type skillRunCallOutput struct {
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
	ExitCode   int      `json:"exit_code"`
	TimedOut   bool     `json:"timed_out"`
	DurationMS int64    `json:"duration_ms"`
	Warnings   []string `json:"warnings,omitempty"`
}

func newConfiguredSandboxRunner(
	ctx context.Context,
	cfg config,
	meta codeReviewSkill,
) (sandboxRunner, error) {
	runtimeName := cfg.effectiveRuntime
	if strings.TrimSpace(runtimeName) == "" {
		runtimeName = runtimeFake
	}
	switch runtimeName {
	case runtimeFake:
		return fakeSandboxRunner{fixture: cfg.fixture}, nil
	case runtimeE2B:
		return newE2BSandboxRunner(ctx, cfg, meta)
	case runtimeLocal:
		return newLocalSandboxRunner(meta)
	default:
		return nil, fmt.Errorf("unsupported runtime %q", runtimeName)
	}
}

func newE2BSandboxRunner(
	ctx context.Context,
	cfg config,
	meta codeReviewSkill,
) (sandboxRunner, error) {
	opts := []e2bexec.Option{
		e2bexec.WithExecutionTimeout(
			time.Duration(defaultCommandTimeoutSeconds) * time.Second,
		),
	}
	if strings.TrimSpace(cfg.e2bTemplate) != "" {
		opts = append(opts, e2bexec.WithTemplate(cfg.e2bTemplate))
	}
	executor, err := e2bexec.NewWithContext(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create e2b sandbox: %w", err)
	}
	runner, err := newSkillRunSandboxRunner(runtimeE2B, meta, executor)
	if err != nil {
		_ = executor.Close()
		return nil, err
	}
	runner.close = executor.Close
	return runner, nil
}

func newLocalSandboxRunner(meta codeReviewSkill) (sandboxRunner, error) {
	if err := preflightLocalRuntime(); err != nil {
		return nil, err
	}
	executor := localexec.New(
		localexec.WithTimeout(
			time.Duration(defaultCommandTimeoutSeconds) * time.Second,
		),
	)
	return newSkillRunSandboxRunner(runtimeLocal, meta, executor)
}

func newSkillRunSandboxRunner(
	runtimeName string,
	meta codeReviewSkill,
	executor codeexecutor.CodeExecutor,
) (*skillRunSandboxRunner, error) {
	repo, err := rootskill.NewFSRepository(meta.Root)
	if err != nil {
		return nil, fmt.Errorf("open skill repository: %w", err)
	}
	runTool := toolskill.NewRunTool(
		repo,
		executor,
		toolskill.WithAllowedCommands("go", "bash"),
		toolskill.WithRunOutputLimits(toolskill.RunOutputLimits{
			StdoutStderrBytes:  sandboxOutputBytes,
			PrimaryOutputBytes: sandboxOutputBytes,
		}),
	)
	return &skillRunSandboxRunner{
		runtimeName: runtimeName,
		skillName:   meta.Name,
		runTool:     runTool,
	}, nil
}

func preflightLocalRuntime() error {
	var missing []string
	for _, name := range []string{"bash", "go"} {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("local runtime missing required tools: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (r *skillRunSandboxRunner) RunSandboxCommand(
	ctx context.Context,
	spec commandSpec,
) sandboxRun {
	start := time.Now()
	run := sandboxRun{
		Runtime:  r.runtimeName,
		Command:  string(spec.Kind),
		ExitCode: -1,
	}
	args, err := permissionArguments(r.skillName, spec)
	if err != nil {
		run.Error = err.Error()
		run.DurationMS = time.Since(start).Milliseconds()
		return run
	}
	raw, err := r.runTool.Call(ctx, args)
	run.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		run.Error = err.Error()
		return run
	}
	data, err := json.Marshal(raw)
	if err != nil {
		run.Error = fmt.Sprintf("marshal skill_run result: %v", err)
		return run
	}
	var out skillRunCallOutput
	if err := json.Unmarshal(data, &out); err != nil {
		run.Error = fmt.Sprintf("parse skill_run result: %v", err)
		return run
	}
	run.ExitCode = out.ExitCode
	run.Stdout = out.Stdout
	run.Stderr = out.Stderr
	run.TimedOut = out.TimedOut
	if out.DurationMS > 0 {
		run.DurationMS = out.DurationMS
	}
	run.Warnings = append([]string(nil), out.Warnings...)
	if spec.Kind == commandCheckStaticcheck && staticcheckWasSkipped(run) {
		run.Skipped = true
		run.Warnings = append(run.Warnings, "staticcheck was skipped by run_checks.sh")
	}
	return run
}

func (r *skillRunSandboxRunner) Close() error {
	if r.close == nil {
		return nil
	}
	return r.close()
}

func skippedSandboxRun(runtimeName string, spec commandSpec, reason string) sandboxRun {
	if strings.TrimSpace(runtimeName) == "" {
		runtimeName = runtimeFake
	}
	if strings.TrimSpace(reason) == "" {
		reason = "sandbox preflight failed"
	}
	return sandboxRun{
		Runtime:    runtimeName,
		Command:    string(spec.Kind),
		ExitCode:   -1,
		DurationMS: 0,
		Skipped:    true,
		Error:      reason,
		Warnings:   []string{reason},
	}
}

func sandboxRunNeedsWarning(run sandboxRun) bool {
	return sandboxRunFailed(run) || len(run.Warnings) > 0
}

func sandboxRunFailed(run sandboxRun) bool {
	return run.Skipped || run.TimedOut || run.ExitCode != 0 ||
		strings.TrimSpace(run.Error) != ""
}

func sandboxRunFailureReason(run sandboxRun) string {
	var parts []string
	if strings.TrimSpace(run.Error) != "" {
		parts = append(parts, run.Error)
	}
	if run.Skipped {
		parts = append(parts, "command skipped")
	}
	if run.TimedOut {
		parts = append(parts, "command timed out")
	}
	if run.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit code %d", run.ExitCode))
	}
	parts = append(parts, run.Warnings...)
	if strings.TrimSpace(run.Stderr) != "" {
		parts = append(parts, strings.TrimSpace(run.Stderr))
	}
	if len(parts) == 0 {
		return "sandbox command did not complete successfully"
	}
	return strings.Join(parts, "; ")
}

func staticcheckWasSkipped(run sandboxRun) bool {
	output := strings.ToLower(run.Stdout + "\n" + run.Stderr)
	return strings.Contains(output, staticcheckSkippedMarker)
}
