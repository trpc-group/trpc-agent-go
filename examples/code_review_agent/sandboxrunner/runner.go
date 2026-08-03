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
	"os"
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
	// goFlags carries the offline -mod flag chosen by offlineGoDeps.
	goFlags string
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
	deps := OfflineGoDeps(cfg.RepoPath)
	cfg.goFlags = deps.GoFlags
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
		// Network-isolated sandboxes cannot download modules, so an
		// unvendored dependency set is an explicit skipped condition
		// rather than a misleading resolution failure.
		if IsolatedSandbox(cfg.SandboxKind) && !deps.OK {
			out.Runs = append(out.Runs, review.SandboxRun{
				Command: command,
				Status:  "skipped",
				Error:   deps.Reason,
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

// runLocal executes the command directly on the host as a development fallback.
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

// runContainer executes the command inside a Docker-based golang container.
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

// runE2B executes the command inside a remote E2B sandbox.
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

// runManaged executes the command inside the managed OS sandbox runtime.
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

// runEngine stages the repo into a workspace and runs the command on the engine.
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
	if err != nil {
		run.Status = "failed"
		run.Error = redaction.RedactText(err.Error())
	}
	// Timeout wins last so a deadline is never masked by the generic
	// engine error that usually accompanies it.
	if res.TimedOut {
		run.Status = "timeout"
	}
	return run
}

// IsolatedSandbox reports whether the sandbox kind blocks network access
// and therefore cannot download Go module dependencies on demand.
func IsolatedSandbox(kind string) bool {
	switch kind {
	case "managed", "sandbox", "container", "e2b":
		return true
	default:
		return false
	}
}

// OfflineDeps describes how sandboxed Go checks resolve dependencies.
type OfflineDeps struct {
	// OK reports that checks can resolve dependencies offline.
	OK bool
	// Reason explains why checks are skipped when !OK.
	Reason string
	// GoFlags is the GOFLAGS value applied to sandboxed go commands.
	GoFlags string
}

// OfflineGoDeps decides the offline dependency policy for the repo:
// vendored modules run with -mod=vendor, dependency-free modules run
// directly, and everything else becomes an explicit skipped condition
// because the isolated sandboxes cannot reach a module proxy.
func OfflineGoDeps(repoPath string) OfflineDeps {
	data, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		// No module file: run the go tool so it reports the real error.
		return OfflineDeps{OK: true}
	}
	if !hasRequire(data) {
		return OfflineDeps{OK: true}
	}
	if _, err := os.Stat(filepath.Join(repoPath, "vendor", "modules.txt")); err == nil {
		return OfflineDeps{OK: true, GoFlags: "-mod=vendor"}
	}
	return OfflineDeps{
		OK: false,
		Reason: "module declares external dependencies without a vendor " +
			"directory; the sandbox runs offline, so vendor the module " +
			"or use --sandbox local-dev",
	}
}

// hasRequire reports whether go.mod declares any required module.
func hasRequire(gomod []byte) bool {
	for _, line := range strings.Split(string(gomod), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "require") {
			return true
		}
	}
	return false
}

// sandboxEnv supplies per-runtime Go environment for sandboxed commands.
func sandboxEnv(cfg Config) map[string]string {
	env := map[string]string{}
	switch cfg.SandboxKind {
	case "managed", "sandbox":
		if runtime.GOROOT() != "" {
			env["GOROOT"] = runtime.GOROOT()
		}
	case "container":
		env["GOCACHE"] = "/tmp/go-build"
		env["GOPATH"] = "/tmp/go"
		env["HOME"] = "/tmp"
		env["PATH"] = "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	default:
		return nil
	}
	// Fail fast on any accidental module download instead of hanging
	// against a proxy the isolated sandbox can never reach.
	env["GOPROXY"] = "off"
	if cfg.goFlags != "" {
		env["GOFLAGS"] = cfg.goFlags
	}
	return env
}

// runFromOutput builds an audited run from raw process output and exit status.
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

// failedRun records a command that could not be executed.
func failedRun(command string, start time.Time, err error) review.SandboxRun {
	return review.SandboxRun{
		Command:    command,
		Status:     "failed",
		DurationMS: time.Since(start).Milliseconds(),
		Error:      redaction.RedactText(err.Error()),
	}
}

// excerpt truncates output to the audit excerpt limit.
func excerpt(s string) string {
	if len(s) <= outputExcerptLimit {
		return s
	}
	return s[:outputExcerptLimit] + "\n[TRUNCATED]"
}
