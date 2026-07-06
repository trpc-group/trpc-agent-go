//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandboxrun

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	StatusPassed      = "passed"
	StatusFailed      = "failed"
	StatusUnavailable = "unavailable"
	StatusSkipped     = "skipped"

	ErrorRuntimeUnavailable = "runtime_unavailable"
	ErrorCommandFailed      = "command_failed"
	ErrorPermissionBlocked  = "permission_blocked"
)

// Result is the raw outcome from a runtime.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Runtime executes a command in a workspace runtime.
type Runtime interface {
	Name() string
	Run(ctx context.Context, command string) (Result, error)
}

// FakeRuntime is a deterministic test/runtime seam.
type FakeRuntime struct {
	RuntimeName string
	Results     map[string]Result
	Errors      map[string]error
}

func (r FakeRuntime) Name() string {
	if r.RuntimeName == "" {
		return "fake"
	}
	return r.RuntimeName
}

func (r FakeRuntime) Run(ctx context.Context, command string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if r.Errors != nil {
		if err, ok := r.Errors[command]; ok {
			return Result{}, err
		}
	}
	if r.Results != nil {
		if res, ok := r.Results[command]; ok {
			return res, nil
		}
	}
	return Result{ExitCode: 0, Stdout: "ok\n"}, nil
}

// Run executes the command and converts the outcome into a durable record.
func Run(ctx context.Context, runtime Runtime, taskID string, id string, command string, maxOutput int) review.SandboxRun {
	start := time.Now()
	if runtime == nil {
		return review.SandboxRun{
			ID:             id,
			TaskID:         taskID,
			Runtime:        "unknown",
			Command:        redact.Text(command).Text,
			Status:         StatusUnavailable,
			DurationMillis: elapsedMillis(start),
			ErrorType:      ErrorRuntimeUnavailable,
		}
	}
	res, err := runtime.Run(ctx, command)
	stdout := truncate(redact.Text(res.Stdout).Text, maxOutput)
	stderr := truncate(redact.Text(res.Stderr).Text, maxOutput)
	record := review.SandboxRun{
		ID:              id,
		TaskID:          taskID,
		Runtime:         runtime.Name(),
		Command:         redact.Text(command).Text,
		Status:          StatusPassed,
		ExitCode:        res.ExitCode,
		DurationMillis:  elapsedMillis(start),
		StdoutRedacted:  stdout.Text,
		StderrRedacted:  stderr.Text,
		OutputTruncated: stdout.Truncated || stderr.Truncated,
	}
	if err != nil {
		record.Status = StatusFailed
		record.ErrorType = ErrorCommandFailed
		record.StderrRedacted = truncate(redact.Text(err.Error()).Text, maxOutput).Text
		if errors.Is(err, context.DeadlineExceeded) {
			record.ErrorType = "timeout"
		}
		return record
	}
	if res.ExitCode != 0 {
		record.Status = StatusFailed
		record.ErrorType = ErrorCommandFailed
	}
	return record
}

type truncatedText struct {
	Text      string
	Truncated bool
}

func truncate(in string, limit int) truncatedText {
	if limit <= 0 || len(in) <= limit {
		return truncatedText{Text: in}
	}
	return truncatedText{Text: in[:limit] + "\n[TRUNCATED]", Truncated: true}
}

func elapsedMillis(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
