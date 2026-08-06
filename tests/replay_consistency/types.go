//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// BackendKind classifies a backend as session-oriented, memory-oriented, or hybrid.
type BackendKind string

const (
	BackendKindSession BackendKind = "session"
	BackendKindMemory  BackendKind = "memory"
)

// OperationKind identifies the type of an operation within a replay case.
type OperationKind string

const (
	OperationKindAppendEvent      OperationKind = "append_event"
	OperationKindUpdateState      OperationKind = "update_state"
	OperationKindDeleteState      OperationKind = "delete_state"
	OperationKindClearState       OperationKind = "clear_state"
	OperationKindAddMemory        OperationKind = "add_memory"
	OperationKindUpdateMemory     OperationKind = "update_memory"
	OperationKindDeleteMemory     OperationKind = "delete_memory"
	OperationKindCreateSummary    OperationKind = "create_summary"
	OperationKindAppendTrackEvent OperationKind = "append_track_event"
	OperationKindReadBack         OperationKind = "read_back"
)

// StateScope indicates which storage scope a state operation targets.
type StateScope string

const (
	StateScopeSession StateScope = "session"
	StateScopeApp     StateScope = "app"
	StateScopeUser    StateScope = "user"
)

// ReplayCase defines a replay consistency test case with an ordered sequence of operations.
type ReplayCase struct {
	Name                string
	Description         string
	Operations          []Operation
	AllowedDiffPatterns []string
	Tags                []string
}

// Operation represents a single mutating action within a replay case, such as appending an event or updating state.
type Operation struct {
	Kind  OperationKind
	Scope StateScope

	Event       *event.Event
	StatePatch  session.StateMap
	StateDelete []string
	ClearState  bool

	MemoryAdd    *MemoryWrite
	MemoryUpdate *MemoryWrite
	MemoryDelete *memory.Key

	Summary *session.Summary
	Track   *session.TrackEvent

	FilterKey string
	Note      string
}

// MemoryWrite carries the parameters for an AddMemory or UpdateMemory operation.
type MemoryWrite struct {
	UserKey  memory.UserKey
	MemoryID string
	Content  string
	Topics   []string
	Metadata *memory.Metadata
}

// HarnessOptions configures runtime behavior of the replay harness.
type HarnessOptions struct {
	BaselineBackend string
	LightMode       bool
	MaxCases        int
	SkipEnv         bool
}

// Backend abstracts a session/memory backend under test.
type Backend interface {
	Name() string
	Kind() BackendKind
	Supports(feature string) bool
	Close() error
}

// ReplayHarness orchestrates replay consistency tests across one or more backends.
type ReplayHarness struct {
	Backends []Backend
	Cases    []ReplayCase
	Options  HarnessOptions
}

// Diff describes a single field-level difference between the baseline and a backend under test.
type Diff struct {
	CaseName    string    `json:"case_name"`
	Backend     string    `json:"backend"`
	Path        string    `json:"path"`
	Baseline    string    `json:"baseline"`
	Actual      string    `json:"actual"`
	AllowedDiff bool      `json:"allowed_diff"`
	Explanation string    `json:"explanation"`
	SessionID   string    `json:"session_id,omitempty"`
	SummaryID   string    `json:"summary_id,omitempty"`
	SummaryKey  string    `json:"summary_key,omitempty"`
	TrackName   string    `json:"track_name,omitempty"`
	MemoryID    string    `json:"memory_id,omitempty"`
	OccurredAt  time.Time `json:"occurred_at,omitempty"`
}

// CaseResult holds the outcome of a single replay case executed against a backend.
type CaseResult struct {
	CaseName string
	Backend  string
	Snapshot NormalizedSnapshot
	Diffs    []Diff
	Error    string
}

// Report is the top-level result of a replay consistency run, suitable for JSON serialization.
type Report struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Cases       []string      `json:"cases"`
	Diffs       []Diff        `json:"diffs"`
	Results     []CaseResult  `json:"results,omitempty"`
	Summary     ReportSummary `json:"summary"`
}

// ReportSummary provides aggregate counts for a replay consistency run.
type ReportSummary struct {
	CasesRun         int `json:"cases_run"`
	BackendsRun      int `json:"backends_run"`
	DiffCount        int `json:"diff_count"`
	AllowedDiffCount int `json:"allowed_diff_count"`
}
