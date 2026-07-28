//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
)

const (
	stagedDiffRel     = "work/inputs/diff.patch"
	stagedSkillsRel   = "skills"
	skillScriptPrefix = "skills/code-review/scripts/"
)

// CreateOptions configures CodeExecutor-backed runners.
type CreateOptions struct {
	Name               string
	SkillsRoot         string
	AllowLocalFallback bool
	Timeout            time.Duration
}

// CreateResult is the runner plus optional fallback note for governance.
type CreateResult struct {
	Runner           Runner
	ExecutorFallback string
	CodeExecutor     codeexecutor.CodeExecutor // for llmagent wiring; may be nil for fake
	// Closer releases executors owned by Create. Caller-supplied executors
	// are never closed here. Nil when nothing was owned.
	Closer func() error
}

// Create builds a Runner backed by framework CodeExecutors when possible.
func Create(opts CreateOptions) (*CreateResult, error) {
	name := strings.ToLower(strings.TrimSpace(opts.Name))
	if name == "" {
		name = "container"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	skillsRoot := opts.SkillsRoot

	switch name {
	case "fake":
		return &CreateResult{Runner: FakeRunner{}}, nil
	case "local":
		ce := localexec.New(
			localexec.WithTimeout(timeout),
			localexec.WithCleanTempFiles(true),
		)
		return &CreateResult{
			Runner:       &CodeExecRunner{name: "local", exec: ce, skillsRoot: skillsRoot},
			CodeExecutor: ce,
			Closer:       closerOf(ce),
		}, nil
	case "container":
		ce, err := newContainerExecutor(skillsRoot)
		if err != nil {
			if !opts.AllowLocalFallback {
				return nil, fmt.Errorf("container executor: %w (pass --executor=local or --allow-local-fallback)", err)
			}
			local := localexec.New(
				localexec.WithTimeout(timeout),
				localexec.WithCleanTempFiles(true),
			)
			return &CreateResult{
				Runner:           &CodeExecRunner{name: "local", exec: local, skillsRoot: skillsRoot},
				CodeExecutor:     local,
				Closer:           closerOf(local),
				ExecutorFallback: "container_unavailable: " + err.Error(),
			}, nil
		}
		return &CreateResult{
			Runner:       &CodeExecRunner{name: "container", exec: ce, skillsRoot: skillsRoot},
			CodeExecutor: ce,
			Closer:       closerOf(ce),
		}, nil
	case "e2b":
		ce, err := e2bexec.New()
		if err != nil {
			if !opts.AllowLocalFallback {
				return nil, fmt.Errorf("e2b executor: %w (set E2B_API_KEY or pass --allow-local-fallback)", err)
			}
			local := localexec.New(
				localexec.WithTimeout(timeout),
				localexec.WithCleanTempFiles(true),
			)
			return &CreateResult{
				Runner:           &CodeExecRunner{name: "local", exec: local, skillsRoot: skillsRoot},
				CodeExecutor:     local,
				Closer:           closerOf(local),
				ExecutorFallback: "e2b_unavailable: " + err.Error(),
			}, nil
		}
		return &CreateResult{
			Runner:       &CodeExecRunner{name: "e2b", exec: ce, skillsRoot: skillsRoot},
			CodeExecutor: ce,
			Closer:       closerOf(ce),
		}, nil
	default:
		return nil, fmt.Errorf("unknown executor %q (want local|container|e2b|fake)", name)
	}
}

// closerOf returns a Close wrapper for executors that implement Close.
// The returned function is safe for concurrent use and closes at most once,
// caching and returning the first Close error on later calls.
func closerOf(ce codeexecutor.CodeExecutor) func() error {
	type closer interface{ Close() error }
	c, ok := ce.(closer)
	if !ok {
		return nil
	}
	var (
		once sync.Once
		err  error
	)
	return func() error {
		once.Do(func() { err = c.Close() })
		return err
	}
}

// newContainerExecutor creates a container CodeExecutor with optional skills bind-mount.
func newContainerExecutor(skillsRoot string) (codeexecutor.CodeExecutor, error) {
	var opts []containerexec.Option
	if skillsRoot != "" {
		if abs, err := absPath(skillsRoot); err == nil {
			opts = append(opts, containerexec.WithBindMount(abs, "/opt/trpc-agent/skills", "ro"))
		}
	}
	return containerexec.New(opts...)
}

// absPath returns an absolute path for p.
func absPath(p string) (string, error) {
	return filepath.Abs(p)
}

// CodeExecRunner adapts codeexecutor.CodeExecutor to sandbox.Runner via the
// workspace Engine.RunProgram contract (exit code, CleanEnv, staged inputs).
type CodeExecRunner struct {
	name       string
	exec       codeexecutor.CodeExecutor
	skillsRoot string
}

// Name implements Runner.
func (r *CodeExecRunner) Name() string { return r.name }

// Run implements Runner via Engine.RunProgram.
// Stdout/stderr are truncated after capture to MaxStdoutBytes/MaxStderrBytes:
// RunProgram ResourceLimits do not currently expose byte caps, so this is a
// post-capture bound for report/storage safety rather than a streaming limit.
func (r *CodeExecRunner) Run(ctx context.Context, spec Spec, limits safety.Limits) Result {
	id := uuid.NewString()
	start := time.Now().UTC()
	timeout := limits.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sum := review.SandboxRunSummary{
		ID:       id,
		Executor: r.name,
		Command:  spec.Command,
	}

	provider, ok := r.exec.(codeexecutor.EngineProvider)
	if !ok {
		sum.Status = "failed"
		sum.ExitCode = 1
		sum.Error = "executor does not expose workspace Engine"
		sum.DurationMS = time.Since(start).Milliseconds()
		return Result{Summary: sum, Stderr: sum.Error}
	}
	eng := provider.Engine()
	ws, err := eng.Manager().CreateWorkspace(runCtx, id, codeexecutor.WorkspacePolicy{})
	if err != nil {
		sum.Status = "failed"
		sum.ExitCode = 1
		sum.Error = err.Error()
		sum.DurationMS = time.Since(start).Milliseconds()
		return Result{Summary: sum, Stderr: err.Error()}
	}
	defer func() { _ = eng.Manager().Cleanup(context.Background(), ws) }()

	if err := r.stageInputs(runCtx, eng, ws, spec); err != nil {
		sum.Status = "failed"
		sum.ExitCode = 1
		sum.Error = err.Error()
		sum.DurationMS = time.Since(start).Milliseconds()
		return Result{Summary: sum, Stderr: err.Error()}
	}

	env := buildEnvMap(spec.Env, limits)
	env["REVIEW_DIFF_PATH"] = stagedDiffRel
	env["REVIEW_OUT_DIR"] = codeexecutor.DirWork

	cmd, args := splitCommand(spec.Command)
	res, err := eng.Runner().RunProgram(runCtx, ws, codeexecutor.RunProgramSpec{
		Cmd:      cmd,
		Args:     args,
		Env:      env,
		CleanEnv: true,
		Cwd:      ".",
		Timeout:  timeout,
	})

	stdout := res.Stdout
	stderr := res.Stderr
	truncated := false
	if limits.MaxStdoutBytes > 0 && len(stdout) > limits.MaxStdoutBytes {
		stdout = stdout[:limits.MaxStdoutBytes]
		truncated = true
	}
	if limits.MaxStderrBytes > 0 && len(stderr) > limits.MaxStderrBytes {
		stderr = stderr[:limits.MaxStderrBytes]
		truncated = true
	}

	sum.DurationMS = time.Since(start).Milliseconds()
	sum.StdoutBytes = len(stdout)
	sum.StderrBytes = len(stderr)
	sum.Truncated = truncated
	sum.StdoutSample = trimSample(stdout, 512)
	sum.StderrSample = trimSample(stderr, 512)
	sum.ExitCode = res.ExitCode

	switch {
	case runCtx.Err() == context.DeadlineExceeded || res.TimedOut:
		sum.Status = "timeout"
		sum.ExitCode = -1
		sum.Error = fmt.Sprintf("command timed out after %s", timeout)
	case err != nil:
		sum.Status = "failed"
		if sum.ExitCode == 0 {
			sum.ExitCode = 1
		}
		sum.Error = err.Error()
	case res.ExitCode != 0:
		sum.Status = "failed"
		sum.Error = fmt.Sprintf("exit code %d", res.ExitCode)
	case truncated:
		sum.Status = "truncated"
	default:
		sum.Status = "ok"
	}
	return Result{Summary: sum, Stdout: stdout, Stderr: stderr}
}

func (r *CodeExecRunner) stageInputs(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	spec Spec,
) error {
	skillsRoot := firstNonEmpty(spec.SkillsRoot, r.skillsRoot)
	if skillsRoot != "" {
		if abs, err := filepath.Abs(skillsRoot); err == nil {
			if err := eng.FS().StageDirectory(ctx, ws, abs, stagedSkillsRel, codeexecutor.StageOptions{
				ReadOnly:   true,
				AllowMount: true,
			}); err != nil {
				// Fallback: copy individual allowlisted scripts.
				if copyErr := stageSkillScripts(ctx, eng, ws, abs); copyErr != nil {
					return fmt.Errorf("stage skills: %w (fallback: %v)", err, copyErr)
				}
			}
		}
	}
	diff := spec.DiffText
	if diff == "" && spec.DiffHostPath != "" {
		b, err := os.ReadFile(spec.DiffHostPath)
		if err != nil {
			return fmt.Errorf("read diff: %w", err)
		}
		diff = string(b)
	}
	if err := eng.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    stagedDiffRel,
		Content: []byte(diff),
		Mode:    0o644,
	}}); err != nil {
		return fmt.Errorf("stage diff: %w", err)
	}
	return nil
}

func stageSkillScripts(ctx context.Context, eng codeexecutor.Engine, ws codeexecutor.Workspace, skillsRoot string) error {
	dir := filepath.Join(skillsRoot, "code-review", "scripts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []codeexecutor.PutFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		files = append(files, codeexecutor.PutFile{
			Path:    skillScriptPrefix + e.Name(),
			Content: b,
			Mode:    0o755,
		})
	}
	if len(files) == 0 {
		return fmt.Errorf("no skill scripts under %s", dir)
	}
	return eng.FS().PutFiles(ctx, ws, files)
}

func buildEnvMap(env []string, limits safety.Limits) map[string]string {
	out := map[string]string{}
	// Seed CleanEnv runs with allowlisted host variables (PATH, etc.) so
	// binaries resolve without inheriting secrets from the ambient process.
	for _, e := range os.Environ() {
		key, val, ok := strings.Cut(e, "=")
		if !ok || !limits.IsEnvAllowed(key) {
			continue
		}
		out[key] = val
	}
	for _, e := range env {
		key, val, ok := strings.Cut(e, "=")
		if !ok || !limits.IsEnvAllowed(key) {
			continue
		}
		out[key] = val
	}
	return out
}

// splitCommand turns an allowlisted skill script path into bash + script args.
// Other allowlisted binaries keep their argv fields.
func splitCommand(command string) (cmd string, args []string) {
	command = strings.Trim(strings.TrimSpace(command), `"'`)
	slash := filepath.ToSlash(command)
	if strings.Contains(slash, skillScriptPrefix) ||
		strings.HasPrefix(slash, skillScriptPrefix) {
		// Run via bash so shebang scripts work across backends.
		rel := slash
		if i := strings.Index(slash, skillScriptPrefix); i >= 0 {
			rel = slash[i:]
		}
		return "bash", []string{rel}
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "false", nil
	}
	return fields[0], fields[1:]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// NewRunner is retained for local/fake compatibility tests.
// Container and E2B backends own long-lived resources and must be created via
// Create so callers can defer CreateResult.Closer; NewRunner refuses them to
// avoid leaking Docker containers or remote sandboxes.
func NewRunner(name string) (Runner, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "", "container", "e2b":
		return nil, fmt.Errorf(
			"sandbox.NewRunner(%q) would leak a long-lived executor; use sandbox.Create and defer CreateResult.Closer",
			name,
		)
	}
	res, err := Create(CreateOptions{Name: name, AllowLocalFallback: false})
	if err != nil {
		return nil, err
	}
	// local/fake closers are nil today; close anyway if a future backend adds one.
	if res.Closer != nil {
		_ = res.Closer()
		return nil, fmt.Errorf(
			"sandbox.NewRunner(%q) obtained a Closer-backed executor; use sandbox.Create instead",
			name,
		)
	}
	return res.Runner, nil
}
