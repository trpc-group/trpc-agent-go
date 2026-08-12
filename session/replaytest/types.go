//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replaytest provides a multi-backend replay consistency testing
// framework for the session, memory, summary, and track subsystems of
// trpc-agent-go.  It drives identical operations through multiple backends
// and compares the results with normalization and difference reporting.
package replaytest

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ---------------------------------------------------------------------------
// Operation types
// ---------------------------------------------------------------------------

// OpType identifies the kind of replay operation.
type OpType string

// Operation type identifiers for replay steps.
const (
	// OpCreateSession creates a new session before replaying operations.
	OpCreateSession OpType = "create_session"
	// OpAppendEvent appends one event to the session.
	OpAppendEvent OpType = "append_event"
	// OpUpdateSessionState writes or overwrites session state keys.
	OpUpdateSessionState OpType = "update_session_state"
	// OpDeleteSessionState deletes one session state key.
	OpDeleteSessionState OpType = "delete_session_state"
	// OpAppendTrackEvent appends one track event to the session.
	OpAppendTrackEvent OpType = "append_track_event"
	// OpCreateSummary triggers session summary generation.
	OpCreateSummary OpType = "create_summary"
	// OpAddMemory adds one memory entry.
	OpAddMemory OpType = "add_memory"
	// OpUpdateMemory updates one memory entry.
	OpUpdateMemory OpType = "update_memory"
	// OpDeleteMemory deletes one memory entry.
	OpDeleteMemory OpType = "delete_memory"
	// OpClearMemories clears all memory entries for the user.
	OpClearMemories OpType = "clear_memories"
	// OpSimulateWriteError marks the next write as failed-then-retried.
	OpSimulateWriteError OpType = "simulate_write_error"
)

// ---------------------------------------------------------------------------
// Replay case
// ---------------------------------------------------------------------------

// ReplayOperation describes one step in a replay case.
type ReplayOperation struct {
	Type OpType `json:"type"`

	// -- session / event fields --
	Event            *event.Event     `json:"-"`
	StateMap         session.StateMap `json:"state_map,omitempty"`
	StateKey         string           `json:"state_key,omitempty"`
	TrackEvent       *session.TrackEvent
	SummaryFilterKey string `json:"summary_filter_key,omitempty"`
	SummaryForce     bool   `json:"summary_force,omitempty"`

	// -- memory fields --
	MemoryContent string   `json:"memory_content,omitempty"`
	MemoryTopics  []string `json:"memory_topics,omitempty"`
	MemoryID      string   `json:"memory_id,omitempty"`

	// SimulateWriteError makes the next write return an error.
	SimulateWriteError bool `json:"simulate_write_error,omitempty"`
}

// ReplayCase defines a named replay scenario.
type ReplayCase struct {
	Name         string
	Description  string
	Operations   []ReplayOperation
	SkipMemories bool
}

// ---------------------------------------------------------------------------
// Backend factory
// ---------------------------------------------------------------------------

// BackendFactory creates service instances for one named backend.
type BackendFactory struct {
	Name           string
	CreateSession  func() (session.Service, error)
	CreateTrack    func() (session.TrackService, error)
	CreateMemory   func() (memory.Service, error)
	UnsupportedOps map[OpType]string
}

// Supports reports whether the backend supports the given op.
func (bf *BackendFactory) Supports(op OpType) bool {
	_, blocked := bf.UnsupportedOps[op]
	return !blocked
}

// UnsupportedReason returns the reason, or "".
func (bf *BackendFactory) UnsupportedReason(op OpType) string {
	return bf.UnsupportedOps[op]
}

// ---------------------------------------------------------------------------
// Backend snapshot
// ---------------------------------------------------------------------------

// BackendSnapshot captures the observable state of one backend after replay.
type BackendSnapshot struct {
	BackendName string                                 `json:"backend_name"`
	SessionID   string                                 `json:"session_id"`
	Events      []event.Event                          `json:"events,omitempty"`
	State       session.StateMap                       `json:"state,omitempty"`
	Summaries   map[string]*session.Summary            `json:"summaries,omitempty"`
	Tracks      map[session.Track]*session.TrackEvents `json:"tracks,omitempty"`
	Memories    []*memory.Entry                        `json:"memories,omitempty"`
}

// ---------------------------------------------------------------------------
// Diff / report types
// ---------------------------------------------------------------------------

// DiffEntry records one field-level difference between two backends.
type DiffEntry struct {
	SessionID        string `json:"session_id"`
	EventIndex       int    `json:"event_index,omitempty"`
	SummaryFilterKey string `json:"summary_filter_key,omitempty"`
	MemoryID         string `json:"memory_id,omitempty"`
	TrackName        string `json:"track_name,omitempty"`
	FieldPath        string `json:"field_path"`
	BaseBackend      string `json:"base_backend"`
	BaseValue        any    `json:"base_value"`
	CompareBackend   string `json:"compare_backend"`
	CompareValue     any    `json:"compare_value"`
	AllowedDiff      bool   `json:"allowed_diff"`
	Explanation      string `json:"explanation,omitempty"`
}

// CaseResult aggregates the comparison outcome for one replay case.
type CaseResult struct {
	CaseName         string      `json:"case_name"`
	BackendPairs     [][2]string `json:"backend_pairs"`
	HasDiff          bool        `json:"has_diff"`
	DiffCount        int         `json:"diff_count"`
	AllowedDiffCount int         `json:"allowed_diff_count"`
	Differences      []DiffEntry `json:"differences,omitempty"`
}

// ReplayReport is the top-level output document.
type ReplayReport struct {
	Timestamp    time.Time    `json:"timestamp"`
	TotalCases   int          `json:"total_cases"`
	PassCases    int          `json:"pass_cases"`
	FailCases    int          `json:"fail_cases"`
	TotalDiffs   int          `json:"total_diffs"`
	AllowedDiffs int          `json:"allowed_diffs"`
	CaseResults  []CaseResult `json:"case_results"`
	BackendNames []string     `json:"backend_names"`
}

// ---------------------------------------------------------------------------
// Normalized types (used by comparator)
// ---------------------------------------------------------------------------

// NormalizedEvent is a comparison-friendly event representation.
type NormalizedEvent struct {
	Author           string             `json:"author"`
	Role             string             `json:"role"`
	Content          string             `json:"content"`
	Branch           string             `json:"branch,omitempty"`
	Tag              string             `json:"tag,omitempty"`
	FilterKey        string             `json:"filter_key,omitempty"`
	Version          int                `json:"version"`
	StateDeltaKeys   []string           `json:"state_delta_keys,omitempty"`
	ExtensionKeys    []string           `json:"extension_keys,omitempty"`
	ResponseObj      string             `json:"response_object,omitempty"`
	Choices          []NormalizedChoice `json:"choices,omitempty"`
	StateDeltaValues map[string]string  `json:"state_delta_values,omitempty"`
}

// NormalizedChoice is a comparison-friendly choice representation.
type NormalizedChoice struct {
	Index         int      `json:"index"`
	Role          string   `json:"role"`
	Content       string   `json:"content"`
	ToolCallNames []string `json:"tool_call_names,omitempty"`
	FinishReason  string   `json:"finish_reason,omitempty"`
}

// NormalizedSummary is a comparison-friendly summary.
type NormalizedSummary struct {
	Text                string   `json:"text"`
	Topics              []string `json:"topics,omitempty"`
	FilterKey           string   `json:"filter_key"`
	BoundaryVersion     int      `json:"boundary_version,omitempty"`
	BoundaryFilterKey   string   `json:"boundary_filter_key,omitempty"`
	BoundaryCutoffAt    string   `json:"boundary_cutoff_at,omitempty"`
	BoundaryLastEventID string   `json:"boundary_last_event_id,omitempty"`
}

// NormalizedTrackEvent is a comparison-friendly track event.
type NormalizedTrackEvent struct {
	Track   string `json:"track"`
	Payload string `json:"payload"`
}

// NormalizedMemory is a comparison-friendly memory entry.
type NormalizedMemory struct {
	Content   string   `json:"content"`
	Topics    []string `json:"topics,omitempty"`
	Scope     string   `json:"scope"`
	Kind      string   `json:"kind,omitempty"`
	EventTime string   `json:"event_time,omitempty"`
	Location  string   `json:"location,omitempty"`
}
