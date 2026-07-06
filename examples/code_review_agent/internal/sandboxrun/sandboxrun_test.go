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
	"strings"
	"testing"
)

func TestRunRecordsFailureWithoutPanic(t *testing.T) {
	runtime := FakeRuntime{Errors: map[string]error{"go test ./...": errors.New("sandbox unavailable: password=supersecretvalue")}}
	run := Run(context.Background(), runtime, "task-1", "run-1", "go test ./...", 1024)
	if run.Status != StatusFailed {
		t.Fatalf("Status = %q, want %q", run.Status, StatusFailed)
	}
	if run.ErrorType != ErrorCommandFailed {
		t.Fatalf("ErrorType = %q, want %q", run.ErrorType, ErrorCommandFailed)
	}
	if strings.Contains(run.StderrRedacted, "supersecretvalue") {
		t.Fatalf("stderr leaked secret: %s", run.StderrRedacted)
	}
}

func TestRunTruncatesOutput(t *testing.T) {
	runtime := FakeRuntime{Results: map[string]Result{"go test ./...": {Stdout: strings.Repeat("x", 20)}}}
	run := Run(context.Background(), runtime, "task-1", "run-1", "go test ./...", 5)
	if !run.OutputTruncated {
		t.Fatalf("OutputTruncated = false, want true")
	}
	if !strings.Contains(run.StdoutRedacted, "[TRUNCATED]") {
		t.Fatalf("StdoutRedacted = %q, want truncation marker", run.StdoutRedacted)
	}
}

func TestShellFallbackUsesShellForEmptyCommand(t *testing.T) {
	if got := shellCommand(""); got != "sh" {
		t.Fatalf("shellCommand(empty) = %q, want sh", got)
	}
	args := shellArgs("")
	if len(args) != 2 || args[0] != "-c" || args[1] != "true" {
		t.Fatalf("shellArgs(empty) = %#v, want [-c true]", args)
	}
}
