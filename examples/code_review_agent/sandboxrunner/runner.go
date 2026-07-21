//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sandboxrunner runs optional static checks behind permission gates.
package sandboxrunner

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/permission"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

const outputExcerptLimit = 4096

// Config controls sandbox check execution.
type Config struct {
	TaskID      string
	RepoPath    string
	SandboxKind string
	DryRun      bool
	// EnableStaticcheck adds "staticcheck ./..." to the check commands.
	// It stays optional because the binary may be absent in the sandbox.
	EnableStaticcheck bool
	Timeout           time.Duration
}

// Result is the audit trail from sandbox execution.
type Result struct {
	Runs      []review.SandboxRun
	Decisions []review.PermissionDecision
}

// RunChecks executes deterministic Go checks when a repository path is present.
func RunChecks(ctx context.Context, cfg Config) Result {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if strings.TrimSpace(cfg.RepoPath) == "" {
		return Result{}
	}
	commands := []string{"go test ./...", "go vet ./..."}
	if cfg.EnableStaticcheck {
		commands = append(commands, "staticcheck ./...")
	}
	var out Result
	for _, command := range commands {
		decision := permission.Decide(command)
		out.Decisions = append(out.Decisions, decision)
		if decision.Decision != permission.DecisionAllow {
			out.Runs = append(out.Runs, review.SandboxRun{
				Command: command,
				Status:  "blocked",
				Error:   decision.Reason,
			})
			continue
		}
		if cfg.DryRun || cfg.SandboxKind == "mock" {
			out.Runs = append(out.Runs, review.SandboxRun{
				Command: command,
				Status:  "skipped",
				Error:   "dry-run/mock mode did not execute external commands",
			})
			continue
		}
		switch cfg.SandboxKind {
		case "local-dev":
			out.Runs = append(out.Runs, runLocal(ctx, cfg.RepoPath, command, cfg.Timeout))
		case "managed", "sandbox":
			out.Runs = append(out.Runs, runManaged(ctx, cfg, command))
		case "container":
			out.Runs = append(out.Runs, runContainer(ctx, cfg, command))
		case "e2b":
			out.Runs = append(out.Runs, runE2B(ctx, cfg, command))
		default:
			out.Runs = append(out.Runs, review.SandboxRun{
				Command: command,
				Status:  "skipped",
				Error:   fmt.Sprintf("unsupported sandbox kind %q in this example", cfg.SandboxKind),
			})
		}
	}
	return out
}

func runLocal(ctx context.Context, repoPath string, command string, timeout time.Duration) review.SandboxRun {
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	parts := strings.Fields(command)
	cmd := exec.CommandContext(runCtx, parts[0], parts[1:]...)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return runFromOutput(command, start, output, exitCode, runCtx.Err(), err)
}

func runContainer(ctx context.Context, cfg Config, command string) review.SandboxRun {
	start := time.Now()
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
		return failedRun(command, start, err)
	}
	defer exec.Close()
	return runEngine(ctx, exec.Engine(), cfg, command, start)
}

func runE2B(ctx context.Context, cfg Config, command string) review.SandboxRun {
	start := time.Now()
	exec, err := e2bexec.NewWithContext(ctx,
		e2bexec.WithSandboxTimeout(cfg.Timeout+30*time.Second),
		e2bexec.WithExecutionTimeout(cfg.Timeout),
	)
	if err != nil {
		return failedRun(command, start, err)
	}
	defer exec.Close()
	return runEngine(ctx, exec.Engine(), cfg, command, start)
}

func runManaged(ctx context.Context, cfg Config, command string) review.SandboxRun {
	start := time.Now()
	repoPath, err := filepath.Abs(cfg.RepoPath)
	if err != nil {
		return failedRun(command, start, err)
	}
	profile := sandbox.WorkspaceWriteProfile().WithReadPaths(repoPath)
	if runtime.GOROOT() != "" {
		profile = profile.WithReadPaths(runtime.GOROOT())
	}
	rt := sandbox.NewRuntime(
		sandbox.WithPermissionProfile(profile),
		sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
			Inherit:              sandbox.ShellEnvironmentPolicyInheritCore,
			ApplyDefaultExcludes: true,
		}),
		sandbox.WithOutputMaxBytes(outputExcerptLimit),
		sandbox.WithDefaultTimeout(cfg.Timeout),
	)
	return runEngine(ctx, rt, cfg, command, start)
}

func runEngine(ctx context.Context, eng codeexecutor.Engine, cfg Config, command string, start time.Time) review.SandboxRun {
	ws, err := eng.Manager().CreateWorkspace(ctx, cfg.TaskID, codeexecutor.WorkspacePolicy{})
	if err != nil {
		return failedRun(command, start, err)
	}
	defer eng.Manager().Cleanup(ctx, ws)
	if err := eng.FS().StageDirectory(ctx, ws, cfg.RepoPath, codeexecutor.DirWork, codeexecutor.StageOptions{}); err != nil {
		return failedRun(command, start, err)
	}
	parts := strings.Fields(command)
	res, err := eng.Runner().RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:      parts[0],
		Args:     parts[1:],
		Cwd:      codeexecutor.DirWork,
		Env:      sandboxEnv(cfg),
		Timeout:  cfg.Timeout,
		CleanEnv: true,
	})
	return engineRun(command, start, res, err)
}

// engineRun converts an engine RunProgram result into an audited run.
func engineRun(command string, start time.Time, res codeexecutor.RunResult, err error) review.SandboxRun {
	run := review.SandboxRun{
		Command:       command,
		Status:        "completed",
		ExitCode:      res.ExitCode,
		DurationMS:    time.Since(start).Milliseconds(),
		StdoutExcerpt: excerpt(redaction.RedactText(res.Stdout)),
		StderrExcerpt: excerpt(redaction.RedactText(res.Stderr)),
	}
	// Non-zero exits are failed checks and must reach exception metrics.
	if res.ExitCode != 0 {
		run.Status = "failed"
		run.Error = fmt.Sprintf("command exited with code %d", res.ExitCode)
	}
	if res.TimedOut {
		run.Status = "timeout"
	}
	if err != nil {
		run.Status = "failed"
		run.Error = redaction.RedactText(err.Error())
	}
	return run
}

func sandboxEnv(cfg Config) map[string]string {
	switch cfg.SandboxKind {
	case "managed", "sandbox":
		if runtime.GOROOT() == "" {
			return nil
		}
		return map[string]string{
			"GOROOT": runtime.GOROOT(),
		}
	case "container":
		return map[string]string{
			"GOCACHE": "/tmp/go-build",
			"GOPATH":  "/tmp/go",
			"HOME":    "/tmp",
			"PATH":    "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		}
	default:
		return nil
	}
}

func runFromOutput(command string, start time.Time, output []byte, exitCode int, ctxErr error, err error) review.SandboxRun {
	run := review.SandboxRun{
		Command:       command,
		Status:        "completed",
		ExitCode:      exitCode,
		DurationMS:    time.Since(start).Milliseconds(),
		StdoutExcerpt: excerpt(redaction.RedactText(string(output))),
	}
	if ctxErr != nil {
		run.Status = "timeout"
		run.Error = ctxErr.Error()
		return run
	}
	if err != nil {
		run.Status = "failed"
		run.Error = redaction.RedactText(err.Error())
	}
	return run
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
