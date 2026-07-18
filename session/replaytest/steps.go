// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// AppendEventStep appends one event.
type AppendEventStep struct {
	StepKey    string
	SessionKey session.Key
	Event      *event.Event
	// LogicalKey is the deterministic logical event identity used by the
	// normalizer/comparator. When empty, StepKey is used for backward compatibility.
	// Keep this independent from StepKey when the same logical event is retried
	// under a different step name (e.g. recovery_duplicate_event).
	LogicalKey string
}

// Type implements Step.
func (s AppendEventStep) Type() string { return "append_event" }

// Key implements Step.
func (s AppendEventStep) Key() string { return s.StepKey }

// UpdateStateStep updates app/user/session state.
type UpdateStateStep struct {
	StepKey    string
	Scope      string // app | user | session
	SessionKey session.Key
	UserKey    session.UserKey
	AppName    string
	State      session.StateMap
	DeleteKey  string
}

// Type implements Step.
func (s UpdateStateStep) Type() string { return "update_state" }

// Key implements Step.
func (s UpdateStateStep) Key() string { return s.StepKey }

// AddMemoryStep adds a memory entry.
type AddMemoryStep struct {
	StepKey string
	UserKey memory.UserKey
	Memory  string
	Topics  []string
}

// Type implements Step.
func (s AddMemoryStep) Type() string { return "add_memory" }

// Key implements Step.
func (s AddMemoryStep) Key() string { return s.StepKey }

// CaptureMemoryStep reads memories into the snapshot.
type CaptureMemoryStep struct {
	StepKey string
	UserKey memory.UserKey
	Limit   int
}

// Type implements Step.
func (s CaptureMemoryStep) Type() string { return "capture_memory" }

// Key implements Step.
func (s CaptureMemoryStep) Key() string { return s.StepKey }

// CreateSummaryStep triggers summary generation.
type CreateSummaryStep struct {
	StepKey    string
	SessionKey session.Key
	FilterKey  string
	Force      bool
	Async      bool
}

// Type implements Step.
func (s CreateSummaryStep) Type() string { return "create_summary" }

// Key implements Step.
func (s CreateSummaryStep) Key() string { return s.StepKey }

// WaitSummaryStep polls until a summary exists.
type WaitSummaryStep struct {
	StepKey      string
	SessionKey   session.Key
	FilterKey    string
	Timeout      time.Duration
	PollInterval time.Duration
}

// Type implements Step.
func (s WaitSummaryStep) Type() string { return "wait_summary" }

// Key implements Step.
func (s WaitSummaryStep) Key() string { return s.StepKey }

// AppendTrackStep appends a track event.
type AppendTrackStep struct {
	StepKey    string
	SessionKey session.Key
	Event      *session.TrackEvent
}

// Type implements Step.
func (s AppendTrackStep) Type() string { return "append_track" }

// Key implements Step.
func (s AppendTrackStep) Key() string { return s.StepKey }

// GetSessionStep captures a session snapshot.
type GetSessionStep struct {
	StepKey    string
	SessionKey session.Key
}

// Type implements Step.
func (s GetSessionStep) Type() string { return "get_session" }

// Key implements Step.
func (s GetSessionStep) Key() string { return s.StepKey }

// ListAppStatesStep captures app states.
type ListAppStatesStep struct {
	StepKey string
	AppName string
}

// Type implements Step.
func (s ListAppStatesStep) Type() string { return "list_app_states" }

// Key implements Step.
func (s ListAppStatesStep) Key() string { return s.StepKey }

// ListUserStatesStep captures user states.
type ListUserStatesStep struct {
	StepKey string
	UserKey session.UserKey
}

// Type implements Step.
func (s ListUserStatesStep) Type() string { return "list_user_states" }

// Key implements Step.
func (s ListUserStatesStep) Key() string { return s.StepKey }

// ReloadSessionStep drops the executor's cached session pointer and reloads from
// the backend, simulating a recovery / process-restart boundary.
type ReloadSessionStep struct {
	StepKey    string
	SessionKey session.Key
}

// Type implements Step.
func (s ReloadSessionStep) Type() string { return "reload_session" }

// Key implements Step.
func (s ReloadSessionStep) Key() string { return s.StepKey }

// ParallelGroupStep runs nested steps concurrently.
// Each inner item is a branch: steps within a branch run sequentially in one
// worker; branches start together after a barrier and join before continuing.
type ParallelGroupStep struct {
	StepKey  string
	Branches [][]Step
}

// Type implements Step.
func (s ParallelGroupStep) Type() string { return "parallel_group" }

// Key implements Step.
func (s ParallelGroupStep) Key() string { return s.StepKey }
