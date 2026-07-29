//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sandbox executes fixed, offline review checks in an isolated
// codeexecutor workspace.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	defaultTimeout        = 120 * time.Second
	defaultCleanupTimeout = 10 * time.Second
	defaultMaxOutputBytes = 64 << 10
	defaultMaxDiskBytes   = 512 << 20
	maxStagedBytes        = 32 << 20
)

var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// Check identifies one fixed review command.
type Check string

const (
	// CheckGoTest runs the repository test suite.
	CheckGoTest Check = "go-test"
	// CheckGoVet runs Go's static analyzer.
	CheckGoVet Check = "go-vet"
	// CheckStaticcheck runs the optional staticcheck analyzer.
	CheckStaticcheck Check = "staticcheck"
)

// Config bounds workspace creation and check execution.
type Config struct {
	Checks            []Check
	EnableStaticcheck bool
	Timeout           time.Duration
	CleanupTimeout    time.Duration
	MaxOutputBytes    int
	MaxDiskBytes      int64
	Limits            codeexecutor.ResourceLimits
}

// Request contains one task's approved source files and parsed diff.
type Request struct {
	TaskID string
	Diff   input.Diff
	Files  []codeexecutor.PutFile
}

// Result contains durable run records and untrusted diagnostic candidates.
type Result struct {
	Runs       []review.SandboxRun
	Candidates []findings.Candidate
}

// Coordinator owns one isolated workspace for a review request.
type Coordinator struct {
	engine codeexecutor.Engine
	config Config
	checks []Check
}

// New validates engine security capabilities and constructs a Coordinator.
func New(engine codeexecutor.Engine, config Config) (*Coordinator, error) {
	if engine == nil || engine.Manager() == nil || engine.FS() == nil || engine.Runner() == nil {
		return nil, errors.New("new sandbox coordinator: complete engine is required")
	}
	capabilities := engine.Describe()
	if !capabilities.SupportsCleanEnv {
		return nil, errors.New("new sandbox coordinator: clean environment support is required")
	}
	if capabilities.NetworkAllowed {
		return nil, errors.New("new sandbox coordinator: network-enabled engine is not allowed")
	}
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if config.Timeout < time.Millisecond || config.Timeout > defaultTimeout {
		return nil, errors.New("new sandbox coordinator: timeout must be between 1ms and 120s")
	}
	if config.CleanupTimeout == 0 {
		config.CleanupTimeout = defaultCleanupTimeout
	}
	if config.CleanupTimeout < time.Millisecond || config.CleanupTimeout > time.Minute {
		return nil, errors.New("new sandbox coordinator: cleanup timeout is invalid")
	}
	if config.MaxOutputBytes == 0 {
		config.MaxOutputBytes = defaultMaxOutputBytes
	}
	if config.MaxOutputBytes < 1 || config.MaxOutputBytes > 1<<20 {
		return nil, errors.New("new sandbox coordinator: output limit is invalid")
	}
	if config.MaxDiskBytes == 0 {
		config.MaxDiskBytes = defaultMaxDiskBytes
	}
	if config.MaxDiskBytes < maxStagedBytes || config.MaxDiskBytes > 4<<30 {
		return nil, errors.New("new sandbox coordinator: disk limit is invalid")
	}
	if config.Limits == (codeexecutor.ResourceLimits{}) {
		config.Limits = codeexecutor.ResourceLimits{CPUPercent: 100, MemoryMB: 1024, MaxPIDs: 256}
	}
	checks, err := configuredChecks(config)
	if err != nil {
		return nil, err
	}
	return &Coordinator{engine: engine, config: config, checks: checks}, nil
}

func configuredChecks(config Config) ([]Check, error) {
	checks := append([]Check(nil), config.Checks...)
	if len(checks) == 0 {
		checks = []Check{CheckGoTest, CheckGoVet}
		if config.EnableStaticcheck {
			checks = append(checks, CheckStaticcheck)
		}
	}
	seen := make(map[Check]struct{}, len(checks))
	for _, check := range checks {
		if check != CheckGoTest && check != CheckGoVet && check != CheckStaticcheck {
			return nil, errors.New("new sandbox coordinator: unknown check")
		}
		if _, exists := seen[check]; exists {
			return nil, errors.New("new sandbox coordinator: duplicate check")
		}
		seen[check] = struct{}{}
	}
	return checks, nil
}

// Run stages approved files read-only, executes fixed checks, and always
// attempts bounded workspace cleanup.
func (c *Coordinator) Run(ctx context.Context, request Request) (result Result, err error) {
	if ctx == nil {
		return Result{}, errors.New("run sandbox checks: context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if !taskIDPattern.MatchString(request.TaskID) || redact.String(request.TaskID) != request.TaskID {
		return Result{}, errors.New("run sandbox checks: invalid task id")
	}
	files, err := prepareFiles(request.Files)
	if err != nil {
		return Result{}, err
	}
	workspace, err := c.engine.Manager().CreateWorkspace(ctx, request.TaskID,
		codeexecutor.WorkspacePolicy{Isolated: true, MaxDiskBytes: c.config.MaxDiskBytes})
	if err != nil {
		return Result{}, fmt.Errorf("create review workspace: %w", redact.Error(err))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), c.config.CleanupTimeout)
		defer cancel()
		cleanupErr := c.engine.Manager().Cleanup(cleanupCtx, workspace)
		if cleanupErr == nil {
			return
		}
		cleanupErr = fmt.Errorf("cleanup review workspace: %w", redact.Error(cleanupErr))
		if err == nil {
			err = cleanupErr
		} else {
			err = errors.Join(err, cleanupErr)
		}
	}()
	if len(files) != 0 {
		if err := c.engine.FS().PutFiles(ctx, workspace, files); err != nil {
			return Result{}, fmt.Errorf("stage review inputs: %w", redact.Error(err))
		}
	}

	result.Runs = make([]review.SandboxRun, 0, len(c.checks))
	for _, check := range c.checks {
		run, candidates, runErr := c.runCheck(ctx, workspace, request, check)
		result.Runs = append(result.Runs, run)
		result.Candidates = append(result.Candidates, candidates...)
		if runErr != nil && check != CheckStaticcheck {
			return result, runErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
	}
	return result, nil
}

func prepareFiles(source []codeexecutor.PutFile) ([]codeexecutor.PutFile, error) {
	result := make([]codeexecutor.PutFile, len(source))
	total := 0
	seen := make(map[string]struct{}, len(source))
	for index, file := range source {
		if file.Path == "" || path.IsAbs(file.Path) || path.Clean(file.Path) != file.Path ||
			file.Path == "." || strings.ContainsAny(file.Path, "\\\x00") ||
			redact.String(file.Path) != file.Path {
			return nil, errors.New("stage review inputs: invalid file identity")
		}
		if _, exists := seen[file.Path]; exists {
			return nil, errors.New("stage review inputs: duplicate file")
		}
		seen[file.Path] = struct{}{}
		total += len(file.Content)
		if total > maxStagedBytes {
			return nil, errors.New("stage review inputs: byte limit exceeded")
		}
		result[index] = codeexecutor.PutFile{
			Path: file.Path, Content: append([]byte(nil), file.Content...), Mode: 0o444,
		}
	}
	return result, nil
}

func (c *Coordinator) runCheck(
	ctx context.Context,
	workspace codeexecutor.Workspace,
	request Request,
	check Check,
) (review.SandboxRun, []findings.Candidate, error) {
	command, args := checkCommand(check)
	display := strings.Join(append([]string{command}, args...), " ")
	started := time.Now()
	programResult, executionErr := c.engine.Runner().RunProgram(ctx, workspace,
		codeexecutor.RunProgramSpec{
			Cmd: command, Args: args, Env: offlineEnvironment(), CleanEnv: true,
			Cwd: ".", Timeout: c.config.Timeout, Limits: c.config.Limits,
		})
	duration := programResult.Duration
	if duration <= 0 {
		duration = time.Since(started)
	}
	stdout, stdoutTruncated := sanitizeAndTruncate(programResult.Stdout, c.config.MaxOutputBytes)
	stderr, stderrTruncated := sanitizeAndTruncate(programResult.Stderr, c.config.MaxOutputBytes)
	run := review.SandboxRun{
		SchemaVersion: review.SchemaVersion,
		TaskID:        request.TaskID,
		Command:       display,
		Duration:      duration,
		Stdout:        stdout,
		Stderr:        stderr,
		Truncated:     stdoutTruncated || stderrTruncated,
	}
	var runErr error
	switch {
	case programResult.TimedOut || errors.Is(executionErr, context.DeadlineExceeded):
		run.Status = review.SandboxStatusTimedOut
		run.TimedOut = true
	case errors.Is(executionErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		run.Status = review.SandboxStatusCanceled
		runErr = context.Canceled
	case executionErr != nil:
		run.Status = review.SandboxStatusUnavailable
		if check != CheckStaticcheck {
			runErr = fmt.Errorf("run %s: %w", check, redact.Error(executionErr))
		}
	case programResult.ExitCode == 0:
		run.Status = review.SandboxStatusCompleted
		run.ExitCode = intPointer(0)
	default:
		run.Status = review.SandboxStatusFailed
		run.ExitCode = intPointer(programResult.ExitCode)
	}
	if validationErr := run.Validate(); validationErr != nil {
		return review.SandboxRun{}, nil, fmt.Errorf("record %s run: %w", check, validationErr)
	}
	diagnostics := stdout + "\n" + stderr
	return run, ParseDiagnostics(request.TaskID, check, request.Diff, diagnostics), runErr
}

func checkCommand(check Check) (string, []string) {
	switch check {
	case CheckGoTest:
		return "go", []string{"test", "./..."}
	case CheckGoVet:
		return "go", []string{"vet", "./..."}
	case CheckStaticcheck:
		return "staticcheck", []string{"./..."}
	default:
		panic("unreachable review check")
	}
}

func offlineEnvironment() map[string]string {
	return map[string]string{
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"HOME":        "/tmp/review-home",
		"TMPDIR":      "/tmp",
		"PATH":        "/usr/local/go/bin:/usr/bin:/bin",
	}
}

func sanitizeAndTruncate(value string, limit int) (string, bool) {
	value = redact.String(value)
	if len(value) <= limit {
		return value, false
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "\n[TRUNCATED]", true
}

func intPointer(value int) *int {
	return &value
}
