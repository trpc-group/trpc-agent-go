//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package replaytest provides reusable replay consistency helpers for session,
// memory, summary, and track backends.
package replaytest

import (
	"context"
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Capability names reported by replay backends.
const (
	CapabilityEventPaging = "event_paging"
	CapabilityTTL         = "ttl"
	CapabilityTrack       = "track"
	CapabilityMemory      = "memory"
	CapabilitySummary     = "summary"
)

// CapabilityStatus records whether a backend supports one replay dimension.
type CapabilityStatus struct {
	Supported   bool   `json:"supported"`
	AllowedDiff bool   `json:"allowed_diff,omitempty"`
	Explanation string `json:"explanation,omitempty"`
}

// Backend binds one session service and optional memory service to a replay run.
type Backend struct {
	Name         string
	Session      session.Service
	Memory       memory.Service
	Capabilities map[string]CapabilityStatus
}

// ReplayCase is a deterministic backend-neutral replay script.
type ReplayCase struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// SnapshotEventNum asks Normalize to read only the most recent N events.
	// This is useful for summary plus event-window replay cases.
	SnapshotEventNum int         `json:"snapshot_event_num,omitempty"`
	Operations       []Operation `json:"operations"`
}

// OperationKind identifies one replay operation.
type OperationKind string

const (
	OperationCreateSession      OperationKind = "create_session"
	OperationAppendEvent        OperationKind = "append_event"
	OperationUpdateSessionState OperationKind = "update_session_state"
	OperationAddMemory          OperationKind = "add_memory"
	OperationUpdateMemory       OperationKind = "update_memory"
	OperationDeleteMemory       OperationKind = "delete_memory"
	OperationCreateSummary      OperationKind = "create_summary"
	OperationAppendTrack        OperationKind = "append_track"
	OperationConcurrent         OperationKind = "concurrent"
	OperationRetry              OperationKind = "retry"
)

// Operation is one replay step. Nested operations are executed in stable order
// unless Kind is OperationConcurrent, where the harness starts them together.
type Operation struct {
	Kind OperationKind `json:"kind"`

	State session.StateMap    `json:"state,omitempty"`
	Event *event.Event        `json:"event,omitempty"`
	Track *session.TrackEvent `json:"track,omitempty"`

	Memory  *MemoryOperation  `json:"memory,omitempty"`
	Summary *SummaryOperation `json:"summary,omitempty"`

	Operations []Operation `json:"operations,omitempty"`
	RetryCount int         `json:"retry_count,omitempty"`
}

// MemoryOperation describes one memory mutation.
type MemoryOperation struct {
	ID       string           `json:"id,omitempty"`
	Content  string           `json:"content,omitempty"`
	Topics   []string         `json:"topics,omitempty"`
	Metadata *memory.Metadata `json:"metadata,omitempty"`
}

// SummaryOperation describes one summary mutation.
type SummaryOperation struct {
	FilterKey string `json:"filter_key"`
	Force     bool   `json:"force"`
}

// RunConfig scopes one replay execution.
type RunConfig struct {
	AppName   string
	UserID    string
	SessionID string
}

// Snapshot is the normalized backend output for one case.
type Snapshot struct {
	CaseName     string                       `json:"case"`
	Backend      string                       `json:"backend"`
	SessionID    string                       `json:"session_id"`
	Events       []NormalizedEvent            `json:"events,omitempty"`
	State        map[string]NormalizedValue   `json:"state,omitempty"`
	Memories     []NormalizedMemory           `json:"memories,omitempty"`
	Summaries    map[string]NormalizedSummary `json:"summaries,omitempty"`
	Tracks       map[string][]NormalizedTrack `json:"tracks,omitempty"`
	Capabilities map[string]CapabilityStatus  `json:"capabilities,omitempty"`
	Unsupported  []UnsupportedCapability      `json:"unsupported,omitempty"`
}

// UnsupportedCapability is included in snapshots and reports for skipped
// backend-specific features.
type UnsupportedCapability struct {
	Capability  string `json:"capability"`
	AllowedDiff bool   `json:"allowed_diff"`
	Explanation string `json:"explanation,omitempty"`
}

// NormalizedValue stores canonical JSON-friendly data.
type NormalizedValue struct {
	Value any `json:"value"`
}

// NormalizedEvent is the comparable event projection.
type NormalizedEvent struct {
	Index        int                        `json:"index"`
	Author       string                     `json:"author,omitempty"`
	Role         string                     `json:"role,omitempty"`
	Content      string                     `json:"content,omitempty"`
	ToolCalls    []NormalizedToolCall       `json:"tool_calls,omitempty"`
	ToolResponse *NormalizedToolResponse    `json:"tool_response,omitempty"`
	Branch       string                     `json:"branch,omitempty"`
	Tag          string                     `json:"tag,omitempty"`
	FilterKey    string                     `json:"filter_key,omitempty"`
	StateDelta   map[string]NormalizedValue `json:"state_delta,omitempty"`
	Extensions   map[string]NormalizedValue `json:"extensions,omitempty"`
}

// NormalizedToolCall is the comparable tool-call projection.
type NormalizedToolCall struct {
	ID        string          `json:"id,omitempty"`
	Type      string          `json:"type,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments NormalizedValue `json:"arguments,omitempty"`
}

// NormalizedToolResponse is the comparable tool-response projection.
type NormalizedToolResponse struct {
	ToolID   string `json:"tool_id,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
	Content  string `json:"content,omitempty"`
}

// NormalizedMemory is the comparable memory projection.
type NormalizedMemory struct {
	ID       string                     `json:"id"`
	Content  string                     `json:"content,omitempty"`
	Topics   []string                   `json:"topics,omitempty"`
	Metadata map[string]NormalizedValue `json:"metadata,omitempty"`
	Scope    string                     `json:"scope,omitempty"`
	Score    *float64                   `json:"score,omitempty"`
}

// NormalizedSummary is the comparable summary projection.
type NormalizedSummary struct {
	FilterKey string                     `json:"filter_key"`
	Text      string                     `json:"summary"`
	Version   int                        `json:"version,omitempty"`
	SessionID string                     `json:"session_id"`
	UpdatedAt string                     `json:"updated_at,omitempty"`
	Boundary  *NormalizedSummaryBoundary `json:"boundary,omitempty"`
}

// NormalizedSummaryBoundary is the comparable summary cutoff projection.
type NormalizedSummaryBoundary struct {
	Version     int    `json:"version,omitempty"`
	FilterKey   string `json:"filter_key,omitempty"`
	CutoffAt    string `json:"cutoff_at,omitempty"`
	LastEventID string `json:"last_event_id,omitempty"`
}

// NormalizedTrack is the comparable track-event projection.
type NormalizedTrack struct {
	Index      int             `json:"index"`
	TrackName  string          `json:"track_name"`
	EventType  string          `json:"event_type,omitempty"`
	Invocation string          `json:"invocation,omitempty"`
	Error      string          `json:"error,omitempty"`
	DurationMs *float64        `json:"duration_ms,omitempty"`
	Payload    NormalizedValue `json:"payload,omitempty"`
}

// Diff reports one mismatch between a baseline and candidate backend.
type Diff struct {
	CaseName         string `json:"case"`
	BaselineBackend  string `json:"baseline_backend"`
	CandidateBackend string `json:"candidate_backend"`
	SessionID        string `json:"session_id,omitempty"`
	EventIndex       *int   `json:"event_index,omitempty"`
	SummaryFilterKey string `json:"summary_filter_key,omitempty"`
	MemoryID         string `json:"memory_id,omitempty"`
	TrackName        string `json:"track_name,omitempty"`
	Path             string `json:"path"`
	Baseline         any    `json:"baseline,omitempty"`
	Candidate        any    `json:"candidate,omitempty"`
	AllowedDiff      bool   `json:"allowed_diff"`
	Explanation      string `json:"explanation,omitempty"`
	Unsupported      bool   `json:"unsupported,omitempty"`
}

// Report is the JSON artifact emitted by the harness.
type Report struct {
	GeneratedBy string   `json:"generated_by"`
	Cases       []string `json:"cases"`
	Diffs       []Diff   `json:"diffs"`
}

// CanonicalJSON returns an indented JSON representation used by tests and
// example report generation.
func CanonicalJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// Run executes a replay case against a backend and returns a normalized
// snapshot.
func Run(ctx context.Context, backend Backend, cfg RunConfig, tc ReplayCase) (Snapshot, error) {
	return executeCase(ctx, backend, cfg, tc)
}
