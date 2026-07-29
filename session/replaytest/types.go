//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replaytest provides backend-neutral replay consistency testing for
// session, memory, summary, and track services.
package replaytest

import (
	"context"
	"encoding/json"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ReportSchemaVersion identifies the JSON diff report schema.
const ReportSchemaVersion = "trpc-agent-go/replay-diff/v1"

// ExtensionLogicalID stores a stable event identity used only by the replay
// normalizer. It is removed from normalized extension output.
const ExtensionLogicalID = "trpc_agent.replay.logical_id"

// ExtensionSequence stores the logical order of concurrently appended events.
// It is removed from normalized extension output.
const ExtensionSequence = "trpc_agent.replay.sequence"

// Feature identifies one backend capability.
type Feature string

const (
	// FeatureEvents identifies event persistence support.
	FeatureEvents Feature = "events"
	// FeatureState identifies state persistence support.
	FeatureState Feature = "state"
	// FeatureMemory identifies long-term memory support.
	FeatureMemory Feature = "memory"
	// FeatureSummary identifies filter-aware summary support.
	FeatureSummary Feature = "summary"
	// FeatureTrack identifies track event support.
	FeatureTrack Feature = "track"
	// FeatureEventPaging identifies event pagination support.
	FeatureEventPaging Feature = "event_paging"
	// FeatureTTL identifies TTL support.
	FeatureTTL Feature = "ttl"
)

// Capabilities declares optional semantics implemented by a backend.
type Capabilities struct {
	Events      bool `json:"events"`
	State       bool `json:"state"`
	Memory      bool `json:"memory"`
	Summary     bool `json:"summary"`
	Track       bool `json:"track"`
	EventPaging bool `json:"event_paging"`
	TTL         bool `json:"ttl"`
}

// CoreCapabilities returns capabilities required by the standard replay
// suite. Event paging and TTL remain opt-in capability checks.
func CoreCapabilities() Capabilities {
	return Capabilities{
		Events:  true,
		State:   true,
		Memory:  true,
		Summary: true,
		Track:   true,
	}
}

// Backend binds replay operations to concrete session and memory services.
// Close must release all resources owned by the backend.
type Backend struct {
	Name         string
	Session      session.Service
	Memory       memory.Service
	Capabilities Capabilities
	Close        func() error
}

// BackendFactory creates an isolated backend for one replay case. A factory
// with no Open function is reported as unsupported rather than silently
// skipped. Open receives the runner context, but concrete service constructors
// may use their own connection timeout when they do not accept a context.
type BackendFactory struct {
	Name           string
	Capabilities   Capabilities
	Open           func(context.Context, ReplayCase) (*Backend, error)
	DisabledReason string
}

// ReplayCase is a backend-neutral sequence of replay operations.
type ReplayCase struct {
	Name                   string            `json:"name"`
	Description            string            `json:"description,omitempty"`
	Key                    session.Key       `json:"key"`
	InitialState           session.StateMap  `json:"initial_state,omitempty"`
	EventLimit             int               `json:"event_limit,omitempty"`
	CanonicalizeEventOrder bool              `json:"canonicalize_event_order,omitempty"`
	Operations             []Operation       `json:"operations"`
	AllowedDiffs           []AllowedDiffRule `json:"allowed_diffs,omitempty"`
	Expected               Expectations      `json:"expected,omitempty"`
}

// Expectations validates semantics shared by every backend, in addition to
// cross-backend equality.
type Expectations struct {
	EventCount      *int           `json:"event_count,omitempty"`
	MemoryCount     *int           `json:"memory_count,omitempty"`
	SummaryFilters  []string       `json:"summary_filters,omitempty"`
	TrackEventCount map[string]int `json:"track_event_count,omitempty"`
}

// OperationKind identifies a replay script instruction.
type OperationKind string

const (
	// OperationAppendEvent appends a conversation event.
	OperationAppendEvent OperationKind = "append_event"
	// OperationSetState writes or overwrites state.
	OperationSetState OperationKind = "set_state"
	// OperationDeleteState deletes app or user state, or clears session state.
	OperationDeleteState OperationKind = "delete_state"
	// OperationAddMemory adds an idempotent memory.
	OperationAddMemory OperationKind = "add_memory"
	// OperationUpdateMemory updates a memory referenced by Ref.
	OperationUpdateMemory OperationKind = "update_memory"
	// OperationDeleteMemory deletes a memory referenced by Ref.
	OperationDeleteMemory OperationKind = "delete_memory"
	// OperationSearchMemory records memory retrieval results.
	OperationSearchMemory OperationKind = "search_memory"
	// OperationGenerateSummary creates or refreshes one filter-key summary.
	OperationGenerateSummary OperationKind = "generate_summary"
	// OperationAppendTrack appends one track event.
	OperationAppendTrack OperationKind = "append_track"
	// OperationParallel executes child operations concurrently.
	OperationParallel OperationKind = "parallel"
)

// Operation is one replay instruction. Exactly one payload matching Kind is
// expected, except OperationParallel which uses Parallel.
type Operation struct {
	Kind     OperationKind `json:"kind"`
	Event    *EventInput   `json:"event,omitempty"`
	State    *StateInput   `json:"state,omitempty"`
	Memory   *MemoryInput  `json:"memory,omitempty"`
	Summary  *SummaryInput `json:"summary,omitempty"`
	Track    *TrackInput   `json:"track,omitempty"`
	Parallel []Operation   `json:"parallel,omitempty"`
	Retry    RetryPolicy   `json:"retry,omitempty"`
}

// RetryPolicy controls deterministic retry simulation. FailBeforeAttempts
// injects failures before persistence, which models a safely retryable outage.
type RetryPolicy struct {
	Attempts           int `json:"attempts,omitempty"`
	FailBeforeAttempts int `json:"fail_before_attempts,omitempty"`
}

// EventInput describes a persisted conversation event.
type EventInput struct {
	LogicalID          string                     `json:"logical_id,omitempty"`
	InvocationID       string                     `json:"invocation_id,omitempty"`
	ParentInvocationID string                     `json:"parent_invocation_id,omitempty"`
	Author             string                     `json:"author"`
	Role               model.Role                 `json:"role"`
	Content            string                     `json:"content,omitempty"`
	ToolID             string                     `json:"tool_id,omitempty"`
	ToolName           string                     `json:"tool_name,omitempty"`
	ToolCalls          []ToolCallInput            `json:"tool_calls,omitempty"`
	Branch             string                     `json:"branch,omitempty"`
	Tag                string                     `json:"tag,omitempty"`
	FilterKey          string                     `json:"filter_key,omitempty"`
	StateDelta         map[string]json.RawMessage `json:"state_delta,omitempty"`
	Extensions         map[string]json.RawMessage `json:"extensions,omitempty"`
	Timestamp          time.Time                  `json:"timestamp,omitempty"`
	Sequence           int                        `json:"sequence,omitempty"`
}

// ToolCallInput describes one assistant tool call.
type ToolCallInput struct {
	ID          string                     `json:"id"`
	Type        string                     `json:"type,omitempty"`
	Name        string                     `json:"name"`
	Arguments   json.RawMessage            `json:"arguments,omitempty"`
	ExtraFields map[string]json.RawMessage `json:"extra_fields,omitempty"`
}

// StateScope identifies the persistence scope of a state operation.
type StateScope string

const (
	// StateScopeSession identifies session-local state.
	StateScopeSession StateScope = "session"
	// StateScopeUser identifies user state.
	StateScopeUser StateScope = "user"
	// StateScopeApp identifies application state.
	StateScopeApp StateScope = "app"
)

// StateInput describes one state mutation.
type StateInput struct {
	Scope StateScope      `json:"scope"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value,omitempty"`
}

// MemoryInput describes a long-term memory operation. Ref is a case-local
// stable name that follows memory ID rotation across updates.
type MemoryInput struct {
	Ref      string           `json:"ref,omitempty"`
	Content  string           `json:"content,omitempty"`
	Topics   []string         `json:"topics,omitempty"`
	Metadata *memory.Metadata `json:"metadata,omitempty"`
	Query    string           `json:"query,omitempty"`
	Limit    int              `json:"limit,omitempty"`
}

// SummaryInput describes one filter-aware summary generation.
type SummaryInput struct {
	FilterKey string `json:"filter_key"`
	Force     bool   `json:"force,omitempty"`
}

// TrackInput describes one observability track event.
type TrackInput struct {
	Name      string          `json:"name"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp,omitempty"`
}

// Snapshot is the normalized replay output for one backend and case.
type Snapshot struct {
	Backend            string                          `json:"backend"`
	AppName            string                          `json:"app_name"`
	UserID             string                          `json:"user_id"`
	SessionID          string                          `json:"session_id"`
	Events             []EventSnapshot                 `json:"events"`
	ObservedEventOrder []string                        `json:"observed_event_order,omitempty"`
	State              map[string]any                  `json:"state"`
	StateTransitions   []StateTransition               `json:"state_transitions,omitempty"`
	Memories           []MemorySnapshot                `json:"memories"`
	MemorySearches     []MemorySearchSnapshot          `json:"memory_searches,omitempty"`
	Summaries          map[string]SummarySnapshot      `json:"summaries"`
	SummaryHistory     []SummarySnapshot               `json:"summary_history,omitempty"`
	Contexts           map[string]ContextSnapshot      `json:"contexts,omitempty"`
	Tracks             map[string][]TrackEventSnapshot `json:"tracks"`
	Recoveries         []RecoverySnapshot              `json:"recoveries,omitempty"`
	Unsupported        []UnsupportedFeature            `json:"unsupported,omitempty"`
}

// EventSnapshot is the normalized semantic surface of an event.
type EventSnapshot struct {
	Index              int                `json:"index"`
	ID                 string             `json:"id"`
	Sequence           int                `json:"sequence,omitempty"`
	Author             string             `json:"author"`
	Role               string             `json:"role"`
	Content            string             `json:"content,omitempty"`
	ToolID             string             `json:"tool_id,omitempty"`
	ToolName           string             `json:"tool_name,omitempty"`
	ToolCalls          []ToolCallSnapshot `json:"tool_calls,omitempty"`
	InvocationID       string             `json:"invocation_id,omitempty"`
	ParentInvocationID string             `json:"parent_invocation_id,omitempty"`
	Branch             string             `json:"branch,omitempty"`
	Tag                string             `json:"tag,omitempty"`
	FilterKey          string             `json:"filter_key,omitempty"`
	StateDelta         map[string]any     `json:"state_delta,omitempty"`
	Extensions         map[string]any     `json:"extensions,omitempty"`
	Timestamp          string             `json:"timestamp,omitempty"`
}

// ToolCallSnapshot is the normalized semantic surface of a tool call.
type ToolCallSnapshot struct {
	ID          string         `json:"id"`
	Type        string         `json:"type,omitempty"`
	Name        string         `json:"name"`
	Arguments   any            `json:"arguments,omitempty"`
	ExtraFields map[string]any `json:"extra_fields,omitempty"`
}

// StateTransition records observed state after a mutation.
type StateTransition struct {
	Operation int        `json:"operation"`
	Scope     StateScope `json:"scope"`
	Key       string     `json:"key"`
	Exists    bool       `json:"exists"`
	Value     any        `json:"value,omitempty"`
}

// MemorySnapshot is a normalized long-term memory entry.
type MemorySnapshot struct {
	ID           string         `json:"id"`
	Content      string         `json:"content"`
	Topics       []string       `json:"topics,omitempty"`
	Kind         string         `json:"kind,omitempty"`
	EventTime    string         `json:"event_time,omitempty"`
	Participants []string       `json:"participants,omitempty"`
	Location     string         `json:"location,omitempty"`
	AppName      string         `json:"app_name"`
	UserID       string         `json:"user_id"`
	Score        float64        `json:"score,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// MemorySearchSnapshot records one normalized retrieval operation.
type MemorySearchSnapshot struct {
	Query   string           `json:"query"`
	Results []MemorySnapshot `json:"results"`
}

// SummarySnapshot is one normalized summary revision.
type SummarySnapshot struct {
	ID                  string   `json:"id"`
	SessionID           string   `json:"session_id"`
	FilterKey           string   `json:"filter_key"`
	Text                string   `json:"text"`
	Topics              []string `json:"topics,omitempty"`
	Version             int      `json:"version"`
	Revision            int      `json:"revision"`
	ReplacesRevision    int      `json:"replaces_revision,omitempty"`
	UpdatedAt           string   `json:"updated_at,omitempty"`
	BoundaryLastEventID string   `json:"boundary_last_event_id,omitempty"`
}

// ContextSnapshot records how a summary and retained events reconstruct a
// compressed conversation.
type ContextSnapshot struct {
	SummaryID         string   `json:"summary_id"`
	SummaryText       string   `json:"summary_text"`
	RetainedEvents    []string `json:"retained_events,omitempty"`
	EventsAfterCutoff []string `json:"events_after_cutoff,omitempty"`
}

// TrackEventSnapshot is one normalized track observation.
type TrackEventSnapshot struct {
	Index        int     `json:"index"`
	EventType    string  `json:"event_type,omitempty"`
	InvocationID string  `json:"invocation_id,omitempty"`
	Error        string  `json:"error,omitempty"`
	DurationMS   float64 `json:"duration_ms,omitempty"`
	Payload      any     `json:"payload,omitempty"`
	Timestamp    string  `json:"timestamp,omitempty"`
}

// RecoverySnapshot records deterministic retry behavior.
type RecoverySnapshot struct {
	Operation string `json:"operation"`
	Attempts  int    `json:"attempts"`
	Failures  int    `json:"failures"`
}

// UnsupportedFeature explains a backend capability gap.
type UnsupportedFeature struct {
	Feature     Feature `json:"feature"`
	Reason      string  `json:"reason"`
	AllowedDiff bool    `json:"allowed_diff"`
}

// AllowedDiffRule explains a known backend difference. A zero tolerance allows
// every difference under PathPrefix; a positive tolerance only allows numeric
// differences within that absolute distance.
type AllowedDiffRule struct {
	PathPrefix        string  `json:"path_prefix"`
	Backend           string  `json:"backend,omitempty"`
	AbsoluteTolerance float64 `json:"absolute_tolerance,omitempty"`
	Explanation       string  `json:"explanation"`
}

// DifferenceSource identifies whether a mismatch came from backend comparison
// or from a case-level semantic expectation.
type DifferenceSource string

const (
	// DifferenceSourceBackend identifies a cross-backend mismatch.
	DifferenceSourceBackend DifferenceSource = "backend"
	// DifferenceSourceExpectation identifies a case expectation violation.
	DifferenceSourceExpectation DifferenceSource = "expectation"
)

// Difference is one localized normalized value mismatch.
type Difference struct {
	Case             string           `json:"case"`
	Source           DifferenceSource `json:"source"`
	BaselineBackend  string           `json:"baseline_backend,omitempty"`
	Backend          string           `json:"backend"`
	SessionID        string           `json:"session_id"`
	EventIndex       *int             `json:"event_index,omitempty"`
	SummaryID        string           `json:"summary_id,omitempty"`
	SummaryFilterKey string           `json:"summary_filter_key,omitempty"`
	TrackName        string           `json:"track_name,omitempty"`
	MemoryID         string           `json:"memory_id,omitempty"`
	FieldPath        string           `json:"field_path"`
	BaselineValue    any              `json:"baseline_value"`
	ComparedValue    any              `json:"compared_value"`
	AllowedDiff      bool             `json:"allowed_diff"`
	Explanation      string           `json:"explanation,omitempty"`
}

// ComparisonStatus summarizes one backend comparison.
type ComparisonStatus string

const (
	// ComparisonPassed means no disallowed differences were found.
	ComparisonPassed ComparisonStatus = "passed"
	// ComparisonFailed means at least one disallowed difference was found.
	ComparisonFailed ComparisonStatus = "failed"
	// ComparisonUnsupported means the backend was not enabled or available.
	ComparisonUnsupported ComparisonStatus = "unsupported"
)

// CaseComparison contains the report for one case/backend pair.
type CaseComparison struct {
	Case        string               `json:"case"`
	SessionID   string               `json:"session_id"`
	Backend     string               `json:"backend"`
	Status      ComparisonStatus     `json:"status"`
	DurationMS  int64                `json:"duration_ms,omitempty"`
	Differences []Difference         `json:"differences,omitempty"`
	Unsupported []UnsupportedFeature `json:"unsupported,omitempty"`
}

// ReportSummary aggregates replay outcomes.
type ReportSummary struct {
	CaseComparisons int `json:"case_comparisons"`
	Passed          int `json:"passed"`
	Failed          int `json:"failed"`
	Unsupported     int `json:"unsupported"`
	AllowedDiffs    int `json:"allowed_diffs"`
	DisallowedDiffs int `json:"disallowed_diffs"`
}

// DiffReport is the machine-readable replay result.
type DiffReport struct {
	SchemaVersion   string           `json:"schema_version"`
	GeneratedAt     time.Time        `json:"generated_at"`
	BaselineBackend string           `json:"baseline_backend"`
	Cases           []CaseComparison `json:"cases"`
	Summary         ReportSummary    `json:"summary"`
}

// RunResult returns both the report and normalized snapshots so callers can
// perform additional quality gates or deliberate fault injection.
type RunResult struct {
	Report    *DiffReport
	Snapshots map[string]map[string]*Snapshot
}
