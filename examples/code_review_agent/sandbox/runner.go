//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sandbox runs review checks with timeout and output limits.
package sandbox

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
)

// Spec describes one sandbox command.
type Spec struct {
	Command string
	Dir     string
	Env     []string
}

// Result is the outcome of one run.
type Result struct {
	Summary review.SandboxRunSummary
	Stdout  string
	Stderr  string
}

// Runner executes commands inside an isolated (or local) environment.
type Runner interface {
	Name() string
	Run(ctx context.Context, spec Spec, limits safety.Limits) Result
}

// LocalRunner runs commands on the host via the same CodeExecutor path as
// Create(Name: "local"). Prefer Create for production wiring; this type remains
// convenient for unit tests.
type LocalRunner struct{}

// Name implements Runner.
func (LocalRunner) Name() string { return "local" }

// Run implements Runner by delegating to CodeExecRunner + localexec.
func (LocalRunner) Run(ctx context.Context, spec Spec, limits safety.Limits) Result {
	timeout := limits.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ce := localexec.New(
		localexec.WithTimeout(timeout),
		localexec.WithCleanTempFiles(true),
	)
	return (&CodeExecRunner{name: "local", exec: ce}).Run(ctx, spec, limits)
}

// FakeRunner records commands without executing them (dry-run).
type FakeRunner struct {
	FailCommands []string
}

// Name implements Runner.
func (FakeRunner) Name() string { return "fake" }

// Run implements Runner.
func (f FakeRunner) Run(ctx context.Context, spec Spec, limits safety.Limits) Result {
	_ = ctx
	_ = limits
	id := uuid.NewString()
	start := time.Now().UTC()
	for _, fail := range f.FailCommands {
		if strings.Contains(spec.Command, fail) {
			return Result{
				Summary: review.SandboxRunSummary{
					ID:         id,
					Executor:   "fake",
					Command:    spec.Command,
					Status:     "failed",
					ExitCode:   1,
					DurationMS: time.Since(start).Milliseconds(),
					Error:      "injected sandbox failure",
				},
				Stderr: "injected sandbox failure",
			}
		}
	}
	return Result{
		Summary: review.SandboxRunSummary{
			ID:         id,
			Executor:   "fake",
			Command:    spec.Command,
			Status:     "ok",
			ExitCode:   0,
			DurationMS: time.Since(start).Milliseconds(),
		},
		Stdout: "[]\n",
	}
}

// FailingRunner always fails; used by the sandbox_fail fixture path.
type FailingRunner struct {
	Inner Runner
}

// Name implements Runner.
func (f FailingRunner) Name() string {
	if f.Inner != nil {
		return f.Inner.Name()
	}
	return "failing"
}

// Run implements Runner.
func (f FailingRunner) Run(ctx context.Context, spec Spec, limits safety.Limits) Result {
	if f.Inner != nil && !strings.Contains(spec.Command, "FORCE_SANDBOX_FAIL") {
		return f.Inner.Run(ctx, spec, limits)
	}
	id := uuid.NewString()
	return Result{
		Summary: review.SandboxRunSummary{
			ID:       id,
			Executor: f.Name(),
			Command:  spec.Command,
			Status:   "failed",
			ExitCode: 1,
			Error:    "forced sandbox failure",
		},
		Stderr: "forced sandbox failure",
	}
}

// trimSample truncates s to at most n runes and appends an ellipsis when needed.
func trimSample(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
