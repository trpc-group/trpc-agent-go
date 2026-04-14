//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package programsession provides shared helpers for interactive program
// sessions, including default polling parameters and non-destructive state
// inspection utilities reused by exec-oriented tools.
package programsession

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	// DefaultExecYieldMS is the default initial wait window for interactive
	// exec startup output.
	DefaultExecYieldMS = 1_000
	// DefaultIOYieldMS is the default wait window for follow-up stdin writes.
	DefaultIOYieldMS = 400
	// DefaultPollLines is the default maximum number of lines requested per poll.
	DefaultPollLines = 40
	// DefaultPollWait is the sleep interval between successive polls.
	DefaultPollWait = 50 * time.Millisecond
	// DefaultPollSettle is the quiet-period window used after output starts
	// arriving before returning a chunk.
	DefaultPollSettle = 75 * time.Millisecond
	// DefaultSessionTTL is how long exited sessions are retained before cleanup.
	DefaultSessionTTL = 30 * time.Minute
	// DefaultSessionKill is the default grace period used before forcing kill.
	DefaultSessionKill = 2 * time.Second
)

// WaitForProgramOutput polls a running ProgramSession until it exits or
// output has settled according to the configured yield window.
func WaitForProgramOutput(
	proc codeexecutor.ProgramSession,
	yield time.Duration,
	limit *int,
) codeexecutor.ProgramPoll {
	deadline := time.Now()
	if yield > 0 {
		deadline = deadline.Add(yield)
	}
	var (
		out            strings.Builder
		offset         int
		nextOffset     int
		haveChunk      bool
		settleDeadline time.Time
	)
	for {
		poll := proc.Poll(limit)
		if poll.Output != "" {
			if !haveChunk {
				offset = poll.Offset
				haveChunk = true
			}
			out.WriteString(poll.Output)
			nextOffset = poll.NextOffset
			settleDeadline = time.Now().Add(DefaultPollSettle)
			if yield <= 0 {
				deadline = settleDeadline
			}
		} else if !haveChunk {
			offset = poll.Offset
			nextOffset = poll.NextOffset
		} else {
			nextOffset = poll.NextOffset
		}
		if poll.Status == codeexecutor.ProgramStatusExited {
			poll.Output = out.String()
			poll.Offset = offset
			poll.NextOffset = nextOffset
			return poll
		}
		now := time.Now()
		if !settleDeadline.IsZero() && now.After(settleDeadline) {
			poll.Output = out.String()
			poll.Offset = offset
			poll.NextOffset = nextOffset
			return poll
		}
		if yield > 0 && now.After(deadline) {
			poll.Output = out.String()
			poll.Offset = offset
			poll.NextOffset = nextOffset
			return poll
		}
		time.Sleep(DefaultPollWait)
	}
}

// YieldDuration normalizes a millisecond input into a duration, using the
// fallback when the provided value is zero and clamping negatives to zero.
func YieldDuration(ms int, fallback int) time.Duration {
	if ms < 0 {
		ms = 0
	}
	if ms == 0 {
		ms = fallback
	}
	return time.Duration(ms) * time.Millisecond
}

// PollLineLimit returns a pointer to a positive poll-line limit, falling back
// to DefaultPollLines when the provided value is not positive.
func PollLineLimit(lines int) *int {
	if lines <= 0 {
		lines = DefaultPollLines
	}
	return &lines
}

// State returns non-destructive program state when the session exposes
// ProgramStateProvider, together with a boolean indicating support.
func State(
	proc codeexecutor.ProgramSession,
) (codeexecutor.ProgramState, bool) {
	provider, ok := proc.(codeexecutor.ProgramStateProvider)
	if !ok {
		return codeexecutor.ProgramState{}, false
	}
	return provider.State(), true
}
