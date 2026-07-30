// Package sandbox implements the SandboxRunner GraphAgent node.
// Routes sandbox command execution through upstream codeexecutor.Engine
// (Issue #2004: tool chain via workspace_exec / codeexec).
// - "container": codeexecutor/container (Docker, production default)
// - "local": codeexecutor/local (os/exec, dev fallback only)
package sandbox

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/state"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

const MaxOutputBytes = 10 * 1024 * 1024

// ── SandboxRunner Node ──

// Run is the SandboxRunner GraphAgent node.
// Uses upstream codeexecutor.Engine for all execution — localexec for dev
// fallback, container for production. The Issue #2004 requirement is that
// commands must flow through the framework's execution layer, not raw os/exec.
// Never returns error — failures are captured in SandboxResult.ErrorType.
func Run(ctx context.Context, gs graph.State) (any, error) {
	start := time.Now()
	defer func() {
		gs[state.StateKeyNodeSandboxRunnerMs] = time.Since(start).Milliseconds()
	}()

	allowed, _ := gs[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	cfg, _ := gs[state.StateKeyExecutorConfig].(types.ExecutorConfig)

	repoPath, _ := gs[state.StateKeyInputRepoPath].(string)
	if repoPath == "" {
		gs[state.StateKeySandboxResults] = []types.SandboxResult{{
			Command:    "skipped (dry-run)",
			ExitCode:   0,
			Stdout:     "Sandbox execution skipped: no --repo-path provided. Run with --repo-path <git-repo> to execute go vet / staticcheck / go test via codeexecutor.Engine.",
			DurationMs: 0,
			ErrorType:  "",
		}}
		return gs, nil
	}

	if len(allowed) == 0 {
		gs[state.StateKeySandboxResults] = []types.SandboxResult{}
		return gs, nil
	}

	maxBytes := int64(cfg.MaxOutputMB) * 1024 * 1024
	if maxBytes == 0 {
		maxBytes = MaxOutputBytes
	}

	// Resolve the codeexecutor.Engine via framework tool layer.
	// - "container": upstream codeexecutor/container (Docker, production)
	// - "local": upstream codeexecutor/local (os/exec, dev fallback only)
	var engine codeexecutor.Engine
	switch cfg.Type {
	case "container", "cube":
		ce, err := container.New()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sandbox: container unavailable (%v), falling back to local\n", err)
			engine = localexec.New().Engine()
		} else {
			engine = ce.Engine()
		}
	default:
		engine = localexec.New().Engine()
	}

	gs[state.StateKeySandboxResults] = runCommands(ctx, engine, allowed, maxBytes)
	return gs, nil
}

// runCommands executes sandbox commands through the framework's codeexecutor.Engine.
func runCommands(ctx context.Context, engine codeexecutor.Engine, commands []types.SandboxCommand, maxBytes int64) []types.SandboxResult {
	mgr := engine.Manager()
	runner := engine.Runner()
	var results []types.SandboxResult

	for _, cmd := range commands {
		timeout := time.Duration(cmd.Timeout) * time.Millisecond
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		execCtx, cancel := context.WithTimeout(ctx, timeout)

		start := time.Now()
		ws, err := mgr.CreateWorkspace(execCtx, "sandbox-"+cmd.Name, codeexecutor.WorkspacePolicy{})
		if err != nil {
			results = append(results, types.SandboxResult{
				Command:    cmd.Cmd + " " + strings.Join(cmd.Args, " "),
				ExitCode:   -1,
				Stderr:     fmt.Sprintf("create workspace: %v", err),
				ErrorType:  "sandbox_crash",
				DurationMs: time.Since(start).Milliseconds(),
			})
			cancel()
			continue
		}

		result, runErr := runner.RunProgram(execCtx, ws, codeexecutor.RunProgramSpec{
			Cmd:     cmd.Cmd,
			Args:    cmd.Args,
			Timeout: timeout,
		})
		mgr.Cleanup(execCtx, ws)
		cancel()

		sr := types.SandboxResult{
			Command:    cmd.Cmd + " " + strings.Join(cmd.Args, " "),
			DurationMs: time.Since(start).Milliseconds(),
		}
		if runErr != nil {
			sr.ExitCode = -1
			sr.Stderr = runErr.Error()
			sr.ErrorType = "sandbox_crash"
		} else {
			sr.ExitCode = result.ExitCode
			sr.Stdout = result.Stdout
			sr.Stderr = result.Stderr
			if result.TimedOut {
				sr.TimedOut = true
				sr.ErrorType = "timeout"
			} else if result.ExitCode != 0 {
				sr.ErrorType = "build_error"
			}
		}
		if int64(len(sr.Stdout)) > maxBytes {
			sr.Stdout = sr.Stdout[:maxBytes] + "\n... (truncated)"
		}
		if int64(len(sr.Stderr)) > maxBytes {
			sr.Stderr = sr.Stderr[:maxBytes] + "\n... (truncated)"
		}
		results = append(results, sr)
	}
	return results
}
