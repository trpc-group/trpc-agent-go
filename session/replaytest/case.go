//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replaytest provides a multi-backend replay consistency test framework.
// It defines replay cases, executes them across multiple backends, and
// generates cross-backend difference reports.
package replaytest

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// OpType defines the type of a replay operation.
type OpType string

const (
	// OpCreateSession creates a new session.
	OpCreateSession OpType = "CreateSession"
	// OpAppendEvent appends an event to a session.
	OpAppendEvent OpType = "AppendEvent"
	// OpUpdateSessionState updates session state.
	OpUpdateSessionState OpType = "UpdateSessionState"
	// OpDeleteSessionState deletes a session state key.
	OpDeleteSessionState OpType = "DeleteSessionState"
	// OpAddMemory adds a memory entry.
	OpAddMemory OpType = "AddMemory"
	// OpUpdateMemory updates a memory entry.
	OpUpdateMemory OpType = "UpdateMemory"
	// OpDeleteMemory deletes a memory entry.
	OpDeleteMemory OpType = "DeleteMemory"
	// OpClearMemories clears all memories for a user.
	OpClearMemories OpType = "ClearMemories"
	// OpCreateSessionSummary creates a session summary.
	OpCreateSessionSummary OpType = "CreateSessionSummary"
	// OpGetSession reads the full session snapshot.
	OpGetSession OpType = "GetSession"
	// OpAppendTrackEvent appends a track event.
	OpAppendTrackEvent OpType = "AppendTrackEvent"
	// OpGetSessionSummaryText retrieves summary text.
	OpGetSessionSummaryText OpType = "GetSessionSummaryText"
	// OpReadMemories reads memories.
	OpReadMemories OpType = "ReadMemories"
	// OpSearchMemories searches memories.
	OpSearchMemories OpType = "SearchMemories"
)

// ReplayOp defines a single atomic operation within a replay case.
type ReplayOp struct {
	// Type is the operation type.
	Type OpType `json:"type"`
	// Key is the target session key.
	Key session.Key `json:"key"`
	// Data carries operation-specific data (event, state, memory, summary, etc.).
	Data any `json:"data,omitempty"`
}

// ReplayCase defines a named sequence of operations to replay across backends.
type ReplayCase struct {
	// Name is a human-readable name for the case.
	Name string `json:"name"`
	// Ops is the ordered sequence of replay operations.
	Ops []ReplayOp `json:"ops"`
	// Want is the expected result descriptor (for trap mode verification).
	Want WantResult `json:"want,omitempty"`
}

// WantResult describes the expected result for trap-based verification.
type WantResult struct {
	// ExpectedDiffKeys lists the field paths that should appear in the diff report.
	ExpectedDiffKeys []string `json:"expectedDiffKeys,omitempty"`
	// ExpectedDiffCount is the expected number of diffs.
	ExpectedDiffCount int `json:"expectedDiffCount"`
}

// BackendResult stores the execution result of a replay case on a single backend.
type BackendResult struct {
	// BackendName is the name of the backend.
	BackendName string `json:"backendName"`
	// Session is the final session snapshot after all operations.
	Session *session.Session `json:"session,omitempty"`
	// Memories is the final list of memory entries.
	Memories []*memory.Entry `json:"memories,omitempty"`
	// SummaryTexts maps filter key to summary text.
	SummaryTexts map[string]string `json:"summaryTexts,omitempty"`
	// Tracks maps track name to track events.
	Tracks map[session.Track]*session.TrackEvents `json:"tracks,omitempty"`
	// Duration is the total execution time.
	Duration time.Duration `json:"duration"`
	// Error is the execution error, if any.
	Error error `json:"-"`
}

// DiffEntry records a single difference between two backends.
type DiffEntry struct {
	// CaseName is the name of the replay case.
	CaseName string `json:"caseName"`
	// BackendA is the baseline backend name.
	BackendA string `json:"backendA"`
	// BackendB is the comparison backend name.
	BackendB string `json:"backendB"`
	// FieldPath is the dot-notation field path (e.g. "events[2].content").
	FieldPath string `json:"fieldPath"`
	// SessionID is the session ID.
	SessionID string `json:"sessionID,omitempty"`
	// EventIndex is the event index (if applicable).
	EventIndex int `json:"eventIndex,omitempty"`
	// SummaryKey is the summary filter key (if applicable).
	SummaryKey string `json:"summaryKey,omitempty"`
	// TrackName is the track name (if applicable).
	TrackName string `json:"trackName,omitempty"`
	// MemoryID is the memory ID (if applicable).
	MemoryID string `json:"memoryID,omitempty"`
	// Baseline is the value from the baseline backend.
	Baseline any `json:"baseline"`
	// Actual is the value from the comparison backend.
	Actual any `json:"actual"`
	// AllowedDiff indicates whether this difference is within allowed tolerance.
	AllowedDiff bool `json:"allowedDiff"`
	// DiffReason is a human-readable explanation of the difference.
	DiffReason string `json:"diffReason"`
}

// EventData carries the data needed for an AppendEvent operation.
type EventData struct {
	Event *event.Event `json:"event"`
}

// StateData carries the data needed for an UpdateSessionState operation.
type StateData struct {
	State session.StateMap `json:"state"`
}

// MemoryData carries the data needed for AddMemory / UpdateMemory operations.
type MemoryData struct {
	UserKey  memory.UserKey  `json:"userKey"`
	Memory   string          `json:"memory"`
	Topics   []string        `json:"topics,omitempty"`
	Metadata *memory.Metadata `json:"metadata,omitempty"`
}

// SummaryData carries the data needed for CreateSessionSummary operations.
type SummaryData struct {
	FilterKey string `json:"filterKey"`
	Force     bool   `json:"force"`
}

// TrackEventData carries the data needed for AppendTrackEvent operations.
type TrackEventData struct {
	Event *session.TrackEvent `json:"event"`
}