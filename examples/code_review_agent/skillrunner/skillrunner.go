//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package skillrunner loads the code-review skill via tool/skill and
// executes its scripts inside a sandboxed skill workspace.
package skillrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/permission"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	toolskill "trpc.group/trpc-go/trpc-agent-go/tool/skill"
)

const (
	// DefaultSkillName is the skill executed by this runner.
	DefaultSkillName = "code-review"

	outputExcerptLimit = 4096
	repoStageTarget    = "work/repo"
)

// Config controls skill script execution.
type Config struct {
	TaskID      string
	SkillsRoot  string
	SkillName   string
	RepoPath    string
	SandboxKind string
	DryRun      bool
	Timeout     time.Duration
	DiffText    string
}

// Result is the audit trail from skill script execution.
type Result struct {
	SkillLoaded bool
	LoadMessage string
	Runs        []review.SandboxRun
	Decisions   []review.PermissionDecision
	Err         error
}

// scriptSpec describes one allow-listed skill script invocation.
type scriptSpec struct {
	command    string
	stdin      string
	env        map[string]string
	inputs     []codeexecutor.InputSpec
	skipReason string
}

// runArgs is the JSON payload passed to the framework skill_run tool.
type runArgs struct {
	Skill   string                   `json:"skill"`
	Command string                   `json:"command"`
	Cwd     string                   `json:"cwd,omitempty"`
	Env     map[string]string        `json:"env,omitempty"`
	Stdin   string                   `json:"stdin,omitempty"`
	Timeout int                      `json:"timeout,omitempty"`
	Inputs  []codeexecutor.InputSpec `json:"inputs,omitempty"`
}

// runResult mirrors the fields of the skill_run structured output.
type runResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
	Duration int64  `json:"duration_ms"`
}

// RunScripts loads the skill through tool/skill and runs its scripts.
// Failures degrade to failed/skipped runs instead of aborting the review.
func RunScripts(ctx context.Context, cfg Config) Result {
	if cfg.SkillName == "" {
		cfg.SkillName = DefaultSkillName
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	// The OS sandbox validates host paths and requires them to be
	// absolute, so resolve the skills root before staging.
	absRoot, err := filepath.Abs(cfg.SkillsRoot)
	if err != nil {
		return Result{Err: fmt.Errorf("skills root: %w", err)}
	}
	cfg.SkillsRoot = absRoot
	repo, err := skill.NewFSRepository(cfg.SkillsRoot)
	if err != nil {
		return Result{Err: fmt.Errorf("skill repository: %w", err)}
	}
	out := Result{}
	loadMsg, err := loadSkill(ctx, repo, cfg.SkillName)
	if err != nil {
		out.Err = fmt.Errorf("skill load: %w", err)
		return out
	}
	out.SkillLoaded = true
	out.LoadMessage = loadMsg

	runTool, execCleanup, execErr := buildRunTool(ctx, repo, cfg)
	defer execCleanup()
	for _, spec := range buildScripts(cfg) {
		decision := permission.Decide(spec.command)
		out.Decisions = append(out.Decisions, decision)
		switch {
		case decision.Decision != permission.DecisionAllow:
			out.Runs = append(out.Runs, review.SandboxRun{
				Command: spec.command,
				Status:  "blocked",
				Error:   decision.Reason,
			})
		case spec.skipReason != "":
			out.Runs = append(out.Runs, skippedRun(spec.command, spec.skipReason))
		case cfg.DryRun || cfg.SandboxKind == "mock":
			out.Runs = append(out.Runs, skippedRun(spec.command,
				"dry-run/mock mode did not execute skill scripts"))
		case execErr != nil:
			out.Runs = append(out.Runs, skippedRun(spec.command, execErr.Error()))
		default:
			out.Runs = append(out.Runs, executeScript(ctx, runTool, cfg, spec))
		}
	}
	return out
}

// loadSkill uses the framework skill_load tool to validate and load the skill.
func loadSkill(ctx context.Context, repo skill.Repository, name string) (string, error) {
	loadTool := toolskill.NewLoadTool(repo)
	args, err := json.Marshal(map[string]string{"skill": name})
	if err != nil {
		return "", err
	}
	res, err := loadTool.Call(ctx, args)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", res), nil
}

// buildRunTool creates the framework skill_run tool over the selected sandbox.
// The returned cleanup releases executor resources (container/e2b sandboxes).
func buildRunTool(ctx context.Context, repo skill.Repository, cfg Config) (*toolskill.RunTool, func(), error) {
	exec, cleanup, err := buildExecutor(ctx, cfg)
	if err != nil {
		return nil, cleanup, err
	}
	if exec == nil {
		return nil, cleanup, nil
	}
	return toolskill.NewRunTool(repo, exec,
		toolskill.WithAllowedCommands("bash")), cleanup, nil
}

// buildExecutor maps the sandbox kind onto a codeexecutor implementation.
func buildExecutor(ctx context.Context, cfg Config) (codeexecutor.CodeExecutor, func(), error) {
	noop := func() {}
	if cfg.DryRun || cfg.SandboxKind == "mock" {
		return nil, noop, nil
	}
	switch cfg.SandboxKind {
	case "local-dev":
		return localexec.New(), noop, nil
	case "managed", "sandbox":
		exec, err := sandboxExecutor(cfg)
		return exec, noop, err
	case "container":
		exec, err := containerexec.New(
			containerexec.WithContainerConfig(dockercontainer.Config{
				Image:      "golang:1.24",
				WorkingDir: "/",
				Cmd:        []string{"tail", "-f", "/dev/null"},
				Tty:        true,
				OpenStdin:  true,
			}),
		)
		if err != nil {
			return nil, noop, err
		}
		return exec, func() { _ = exec.Close() }, nil
	case "e2b":
		exec, err := e2bexec.NewWithContext(ctx,
			e2bexec.WithSandboxTimeout(cfg.Timeout+30*time.Second),
			e2bexec.WithExecutionTimeout(cfg.Timeout),
		)
		if err != nil {
			return nil, noop, err
		}
		return exec, func() { _ = exec.Close() }, nil
	default:
		return nil, noop, fmt.Errorf(
			"skill scripts support managed/container/e2b/local-dev sandboxes; got %q",
			cfg.SandboxKind)
	}
}

// sandboxExecutor builds the OS-sandboxed executor mirroring sandboxrunner.
// Note: do not grant WithWritePaths(os.TempDir()) here; the default
// workspace root lives under the host temp dir, and adding the temp dir
// as an extra write grant makes the Seatbelt policy reject writes at the
// workspace root itself (breaking the skill metadata commit).
func sandboxExecutor(cfg Config) (codeexecutor.CodeExecutor, error) {
	profile := sandbox.WorkspaceWriteProfile().
		WithReadPaths(cfg.SkillsRoot)
	if cfg.RepoPath != "" {
		absRepo, err := filepath.Abs(cfg.RepoPath)
		if err != nil {
			return nil, err
		}
		profile = profile.WithReadPaths(absRepo)
	}
	if runtime.GOROOT() != "" {
		profile = profile.WithReadPaths(runtime.GOROOT())
	}
	return sandbox.New(
		sandbox.WithPermissionProfile(profile),
		sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
			Inherit:              sandbox.ShellEnvironmentPolicyInheritCore,
			ApplyDefaultExcludes: true,
		}),
		sandbox.WithOutputMaxBytes(outputExcerptLimit),
		sandbox.WithDefaultTimeout(cfg.Timeout),
	), nil
}

// buildScripts lists the allow-listed script invocations for one review.
func buildScripts(cfg Config) []scriptSpec {
	scriptPath := func(name string) string {
		return fmt.Sprintf("bash skills/%s/scripts/%s", cfg.SkillName, name)
	}
	specs := []scriptSpec{
		{command: scriptPath("diff_summary.sh") + " -", stdin: cfg.DiffText},
		{command: scriptPath("secret_scan.sh") + " -", stdin: cfg.DiffText},
	}
	static := scriptSpec{
		command: scriptPath("go_static_checks.sh") + " " + repoStageTarget,
	}
	if strings.TrimSpace(cfg.RepoPath) == "" {
		static.skipReason = "no --repo-path provided; static checks need a repository"
	} else if absRepo, err := filepath.Abs(cfg.RepoPath); err != nil {
		static.skipReason = err.Error()
	} else {
		static.inputs = []codeexecutor.InputSpec{{
			From: "host://" + absRepo,
			To:   repoStageTarget,
			Mode: repoStageMode(cfg.SandboxKind),
		}}
		static.env = goEnv(cfg.SandboxKind)
	}
	return append(specs, static)
}

// repoStageMode picks a staging mode with consistent placement per runtime.
func repoStageMode(sandboxKind string) string {
	if sandboxKind == "local-dev" {
		return "link"
	}
	return ""
}

// goEnv supplies Go env for the static checks because policy mode
// scrubs HOME. In the OS sandbox the host temp dir is not writable, so
// caches default to the sandbox TMPDIR inside the script; only GOROOT
// is forwarded there. Containers use the golang image toolchain.
func goEnv(sandboxKind string) map[string]string {
	if sandboxKind == "container" {
		return map[string]string{
			"GOCACHE": "/tmp/go-build",
			"GOPATH":  "/tmp/go",
			"HOME":    "/tmp",
			"PATH":    "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		}
	}
	env := map[string]string{}
	if sandboxKind == "local-dev" {
		env["GOCACHE"] = filepath.Join(os.TempDir(), "code-review-gocache")
		env["GOPATH"] = filepath.Join(os.TempDir(), "code-review-gopath")
	}
	if runtime.GOROOT() != "" {
		env["GOROOT"] = runtime.GOROOT()
	}
	return env
}

// executeScript invokes skill_run and converts its result into a SandboxRun.
func executeScript(ctx context.Context, runTool *toolskill.RunTool, cfg Config, spec scriptSpec) review.SandboxRun {
	start := time.Now()
	args, err := json.Marshal(runArgs{
		Skill:   cfg.SkillName,
		Command: spec.command,
		// Run at the workspace root so script paths match the
		// permission allowlist ("bash skills/<name>/scripts/...").
		Cwd:     "/",
		Env:     spec.env,
		Stdin:   spec.stdin,
		Timeout: int(cfg.Timeout.Seconds()),
		Inputs:  spec.inputs,
	})
	if err != nil {
		return failedRun(spec.command, start, err)
	}
	res, err := runTool.Call(ctx, args)
	if err != nil {
		return failedRun(spec.command, start, err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return failedRun(spec.command, start, err)
	}
	var rr runResult
	if err := json.Unmarshal(raw, &rr); err != nil {
		return failedRun(spec.command, start, err)
	}
	return scriptRun(spec.command, start, rr)
}

// scriptRun converts a skill_run result into an audited SandboxRun.
func scriptRun(command string, start time.Time, rr runResult) review.SandboxRun {
	run := review.SandboxRun{
		Command:       command,
		Status:        "completed",
		ExitCode:      rr.ExitCode,
		DurationMS:    rr.Duration,
		StdoutExcerpt: excerpt(redaction.RedactText(rr.Stdout)),
		StderrExcerpt: excerpt(redaction.RedactText(rr.Stderr)),
	}
	if run.DurationMS == 0 {
		run.DurationMS = time.Since(start).Milliseconds()
	}
	// Non-zero exits are failed scripts and must reach exception metrics.
	if rr.ExitCode != 0 {
		run.Status = "failed"
		run.Error = fmt.Sprintf("script exited with code %d", rr.ExitCode)
	}
	if rr.TimedOut {
		run.Status = "timeout"
	}
	return run
}

func skippedRun(command, reason string) review.SandboxRun {
	return review.SandboxRun{
		Command: command,
		Status:  "skipped",
		Error:   reason,
	}
}

func failedRun(command string, start time.Time, err error) review.SandboxRun {
	return review.SandboxRun{
		Command:    command,
		Status:     "failed",
		DurationMS: time.Since(start).Milliseconds(),
		Error:      redaction.RedactText(err.Error()),
	}
}

func excerpt(s string) string {
	if len(s) <= outputExcerptLimit {
		return s
	}
	return s[:outputExcerptLimit] + "\n[TRUNCATED]"
}
