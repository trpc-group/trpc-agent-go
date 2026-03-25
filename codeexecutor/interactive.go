//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

import (
	"context"
	"time"
)

const (
	// ProgramStatusRunning indicates the interactive program is still
	// running and may accept more input.
	ProgramStatusRunning = "running"
	// ProgramStatusExited indicates the interactive program has exited.
	ProgramStatusExited = "exited"
)

// InteractiveProgramSpec describes a session-oriented program
// invocation in a workspace.
type InteractiveProgramSpec struct {
	RunProgramSpec
	TTY bool
}

// ProgramPoll captures the latest incremental output for a running or
// exited interactive program session.
type ProgramPoll struct {
	Status     string
	Output     string
	Offset     int
	NextOffset int
	ExitCode   *int
}

// ProgramLog returns output from a specific offset without mutating the
// incremental cursor.
type ProgramLog struct {
	Output     string
	Offset     int
	NextOffset int
}

// ProgramSession exposes a running interactive program session.
//
// Implementations are expected to be safe for concurrent use by tool-layer
// callers. In particular, Poll, Log, Write, Kill, Close, and any optional
// state/result helpers may be invoked concurrently for the same session.
type ProgramSession interface {
	ID() string
	Poll(limit *int) ProgramPoll
	Log(offset *int, limit *int) ProgramLog
	Write(data string, newline bool) error
	Kill(grace time.Duration) error
	Close() error
}

// ProgramState captures non-streaming session status without advancing any
// incremental output cursor.
type ProgramState struct {
	Status   string
	ExitCode *int
}

// ProgramStateProvider optionally exposes non-destructive session state for
// lifecycle management and cleanup.
type ProgramStateProvider interface {
	State() ProgramState
}

// ProgramResultProvider optionally exposes a final RunResult for
// interactive sessions after they exit.
type ProgramResultProvider interface {
	RunResult() RunResult
}

// InteractiveProgramRunner is an optional executor capability for
// multi-turn interactive program execution.
type InteractiveProgramRunner interface {
	StartProgram(
		ctx context.Context,
		ws Workspace,
		spec InteractiveProgramSpec,
	) (ProgramSession, error)
}
