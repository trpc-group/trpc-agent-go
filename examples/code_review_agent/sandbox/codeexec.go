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
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	e2bexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
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

	switch name {
	case "fake":
		return &CreateResult{Runner: FakeRunner{}}, nil
	case "local":
		ce := localexec.New(
			localexec.WithTimeout(timeout),
			localexec.WithCleanTempFiles(true),
		)
		return &CreateResult{
			Runner:       &CodeExecRunner{name: "local", exec: ce},
			CodeExecutor: ce,
		}, nil
	case "container":
		ce, err := newContainerExecutor(opts.SkillsRoot)
		if err != nil {
			if !opts.AllowLocalFallback {
				return nil, fmt.Errorf("container executor: %w (pass --executor=local or --allow-local-fallback)", err)
			}
			local := localexec.New(
				localexec.WithTimeout(timeout),
				localexec.WithCleanTempFiles(true),
			)
			return &CreateResult{
				Runner:           &CodeExecRunner{name: "local", exec: local},
				CodeExecutor:     local,
				ExecutorFallback: "container_unavailable: " + err.Error(),
			}, nil
		}
		return &CreateResult{
			Runner:       &CodeExecRunner{name: "container", exec: ce},
			CodeExecutor: ce,
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
				Runner:           &CodeExecRunner{name: "local", exec: local},
				CodeExecutor:     local,
				ExecutorFallback: "e2b_unavailable: " + err.Error(),
			}, nil
		}
		return &CreateResult{
			Runner:       &CodeExecRunner{name: "e2b", exec: ce},
			CodeExecutor: ce,
		}, nil
	default:
		return nil, fmt.Errorf("unknown executor %q (want local|container|e2b|fake)", name)
	}
}

func newContainerExecutor(skillsRoot string) (codeexecutor.CodeExecutor, error) {
	var opts []containerexec.Option
	if skillsRoot != "" {
		if abs, err := absPath(skillsRoot); err == nil {
			opts = append(opts, containerexec.WithBindMount(abs, "/opt/trpc-agent/skills", "ro"))
		}
	}
	return containerexec.New(opts...)
}

func absPath(p string) (string, error) {
	return filepath.Abs(p)
}

// CodeExecRunner adapts codeexecutor.CodeExecutor to sandbox.Runner.
type CodeExecRunner struct {
	name string
	exec codeexecutor.CodeExecutor
}

// Name implements Runner.
func (r *CodeExecRunner) Name() string { return r.name }

// Run implements Runner via ExecuteCode (bash).
func (r *CodeExecRunner) Run(ctx context.Context, spec Spec, limits safety.Limits) Result {
	id := uuid.NewString()
	start := time.Now().UTC()
	timeout := limits.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Prefer writing a small bash snippet so cwd/env can be expressed in-script.
	script := buildBashScript(spec, limits)
	out, err := r.exec.ExecuteCode(runCtx, codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{
			Language: "bash",
			Code:     script,
		}},
		ExecutionID: id,
	})

	dur := time.Since(start).Milliseconds()
	stdout := out.Output
	truncated := false
	if limits.MaxStdoutBytes > 0 && len(stdout) > limits.MaxStdoutBytes {
		stdout = stdout[:limits.MaxStdoutBytes]
		truncated = true
	}

	sum := review.SandboxRunSummary{
		ID:           id,
		Executor:     r.name,
		Command:      spec.Command,
		DurationMS:   dur,
		StdoutBytes:  len(stdout),
		Truncated:    truncated,
		StdoutSample: trimSample(stdout, 512),
	}
	if runCtx.Err() == context.DeadlineExceeded {
		sum.Status = "timeout"
		sum.ExitCode = -1
		sum.Error = fmt.Sprintf("command timed out after %s", timeout)
		return Result{Summary: sum, Stdout: stdout}
	}
	if err != nil {
		sum.Status = "failed"
		sum.ExitCode = 1
		sum.Error = err.Error()
		sum.StderrSample = trimSample(err.Error(), 512)
		if truncated {
			sum.Status = "truncated"
		}
		return Result{Summary: sum, Stdout: stdout, Stderr: err.Error()}
	}
	sum.Status = "ok"
	sum.ExitCode = 0
	if truncated {
		sum.Status = "truncated"
	}
	return Result{Summary: sum, Stdout: stdout}
}

func buildBashScript(spec Spec, limits safety.Limits) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	if spec.Dir != "" {
		fmt.Fprintf(&b, "cd %q\n", spec.Dir)
	}
	for _, e := range spec.Env {
		key, val, ok := strings.Cut(e, "=")
		if !ok || !limits.IsEnvAllowed(key) {
			continue
		}
		fmt.Fprintf(&b, "export %s=%q\n", key, val)
	}
	b.WriteString(spec.Command)
	b.WriteByte('\n')
	return b.String()
}

// NewRunner is retained for compatibility; prefer Create.
// It does not silently fall back for container/e2b.
func NewRunner(name string) (Runner, error) {
	res, err := Create(CreateOptions{Name: name, AllowLocalFallback: false})
	if err != nil {
		return nil, err
	}
	return res.Runner, nil
}
