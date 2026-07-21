package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type CommandSpec struct {
	Tool    string
	Command []string
}

type SandboxRunner struct {
	Runtime             string
	RepoPath            string
	Timeout             time.Duration
	MaxOutputBytes      int64
	ForceSandboxFailure bool
	redactor            Redactor
}

func (s SandboxRunner) Run(ctx context.Context, taskID string, spec CommandSpec) SandboxRun {
	started := time.Now().UTC()
	run := SandboxRun{
		ID:        NewID("run"),
		TaskID:    taskID,
		Runtime:   s.Runtime,
		Tool:      spec.Tool,
		Command:   append([]string(nil), spec.Command...),
		Status:    "running",
		ExitCode:  -1,
		StartedAt: started,
	}
	if s.Timeout <= 0 {
		s.Timeout = 30 * time.Second
	}
	if s.MaxOutputBytes <= 0 {
		s.MaxOutputBytes = 64 * 1024
	}
	if s.ForceSandboxFailure {
		return s.finish(run, "failed", 42, "forced sandbox failure for deterministic test fixture", "forced_failure", started, false)
	}

	switch strings.ToLower(s.Runtime) {
	case "fake", "dry-run", "rule-only", "":
		return s.fakeRun(run, started)
	case "local":
		return s.localRun(ctx, run, started)
	case "container":
		return s.containerRun(ctx, run, started)
	case "e2b", "cube":
		return s.remoteSandboxRun(run, started)
	default:
		return s.finish(run, "failed", -1, "unknown sandbox runtime: "+s.Runtime, "configuration", started, false)
	}
}

func (s SandboxRunner) fakeRun(run SandboxRun, started time.Time) SandboxRun {
	if s.RepoPath == "" {
		return s.finish(run, "skipped", 0, "dry-run fake sandbox: no repo path; diff-only review executed deterministic rules", "", started, false)
	}
	return s.finish(run, "success", 0, "dry-run fake sandbox: would execute "+strings.Join(run.Command, " ")+" in isolated workspace", "", started, false)
}

func (s SandboxRunner) localRun(ctx context.Context, run SandboxRun, started time.Time) SandboxRun {
	if s.RepoPath == "" {
		return s.finish(run, "skipped", 0, "local runtime skipped: repo path is empty", "", started, false)
	}
	if len(run.Command) == 0 {
		return s.finish(run, "failed", -1, "empty command", "configuration", started, false)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, run.Command[0], run.Command[1:]...)
	cmd.Dir = s.RepoPath
	cmd.Env = allowedEnv()
	var out limitedBuffer
	out.limit = s.MaxOutputBytes
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	status := "success"
	exit := 0
	errorType := ""
	if cmdCtx.Err() == context.DeadlineExceeded {
		status = "timeout"
		exit = -1
		errorType = "timeout"
	} else if err != nil {
		status = "failed"
		exit = exitCode(err)
		errorType = classifyExecError(err)
	}
	return s.finish(run, status, exit, out.String(), errorType, started, out.truncated)
}

func (s SandboxRunner) containerRun(ctx context.Context, run SandboxRun, started time.Time) SandboxRun {
	if s.RepoPath == "" {
		return s.finish(run, "skipped", 0, "container runtime skipped: repo path is empty", "", started, false)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return s.finish(run, "failed", -1, "container runtime unavailable: docker executable not found", "runtime_unavailable", started, false)
	}
	absRepo, err := filepath.Abs(s.RepoPath)
	if err != nil {
		return s.finish(run, "failed", -1, err.Error(), "configuration", started, false)
	}
	dockerCommand := []string{"docker", "run", "--rm", "--network", "none", "-v", absRepo + ":/workspace:ro", "-w", "/workspace", "golang:1.22"}
	dockerCommand = append(dockerCommand, run.Command...)
	containerRun := run
	containerRun.Command = dockerCommand
	return SandboxRunner{Runtime: "local", RepoPath: s.RepoPath, Timeout: s.Timeout, MaxOutputBytes: s.MaxOutputBytes, redactor: s.redactor}.executeExternal(ctx, containerRun, started)
}

func (s SandboxRunner) executeExternal(ctx context.Context, run SandboxRun, started time.Time) SandboxRun {
	cmdCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, run.Command[0], run.Command[1:]...)
	cmd.Env = allowedEnv()
	var out limitedBuffer
	out.limit = s.MaxOutputBytes
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	status := "success"
	exit := 0
	errorType := ""
	if cmdCtx.Err() == context.DeadlineExceeded {
		status = "timeout"
		exit = -1
		errorType = "timeout"
	} else if err != nil {
		status = "failed"
		exit = exitCode(err)
		errorType = classifyExecError(err)
	}
	return s.finish(run, status, exit, out.String(), errorType, started, out.truncated)
}

func (s SandboxRunner) remoteSandboxRun(run SandboxRun, started time.Time) SandboxRun {
	if os.Getenv("E2B_API_KEY") == "" && os.Getenv("CUBE_SANDBOX_ENDPOINT") == "" {
		return s.finish(run, "failed", -1, "remote sandbox runtime is configured in the interface but no E2B_API_KEY or CUBE_SANDBOX_ENDPOINT is present", "runtime_unavailable", started, false)
	}
	return s.finish(run, "failed", -1, "remote sandbox execution is intentionally stubbed in this offline prototype", "not_implemented", started, false)
}

func (s SandboxRunner) finish(run SandboxRun, status string, exit int, output string, errorType string, started time.Time, truncated bool) SandboxRun {
	completed := time.Now().UTC()
	run.Status = status
	run.ExitCode = exit
	run.DurationMS = completed.Sub(started).Milliseconds()
	run.Output = s.redactor.Redact(output)
	run.ErrorType = errorType
	run.CompletedAt = completed
	run.OutputTruncated = truncated
	return run
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	written   int64
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.written
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	toWrite := int64(len(p))
	if toWrite > remaining {
		toWrite = remaining
		b.truncated = true
	}
	_, _ = b.buf.Write(p[:toWrite])
	b.written += toWrite
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + "\n[output truncated]"
	}
	return b.buf.String()
}

func allowedEnv() []string {
	allow := map[string]bool{"PATH": true, "HOME": true, "USERPROFILE": true, "TMP": true, "TEMP": true, "SYSTEMROOT": true, "WINDIR": true}
	out := []string{}
	for _, kv := range os.Environ() {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		if allow[strings.ToUpper(key)] {
			out = append(out, kv)
		}
	}
	return out
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func classifyExecError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "runtime_unavailable"
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "deadline") {
		return "timeout"
	}
	return "command_failed"
}

var _ io.Writer = (*limitedBuffer)(nil)

func SandboxCommands(cfg Config) []CommandSpec {
	commands := []CommandSpec{
		{Tool: "codeexec", Command: []string{"go", "test", "./..."}},
		{Tool: "codeexec", Command: []string{"go", "vet", "./..."}},
	}
	if cfg.EnableStaticcheck {
		commands = append(commands, CommandSpec{Tool: "codeexec", Command: []string{"staticcheck", "./..."}})
	} else {
		commands = append(commands, CommandSpec{Tool: "codeexec", Command: []string{"staticcheck", "./..."}})
	}
	return commands
}

func SandboxWarningFromRun(run SandboxRun) *Finding {
	if run.Status == "success" || run.Status == "skipped" {
		return nil
	}
	return &Finding{
		Severity:       SeverityLow,
		Category:       "sandbox_execution",
		File:           "sandbox",
		Line:           1,
		Title:          "Sandbox command did not complete successfully",
		Evidence:       fmt.Sprintf("%s %v: %s", run.Status, run.Command, run.Output),
		Recommendation: "Inspect sandbox configuration and rerun the command. Review continues because sandbox failure is isolated from rule analysis.",
		Confidence:     0.62,
		Source:         "tool:codeexec",
		RuleID:         "SANDBOX-001",
		NeedsHuman:     true,
	}
}
