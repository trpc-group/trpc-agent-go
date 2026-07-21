//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package replaytest provides a reusable replay consistency harness for
// session, memory, summary, and track backends.
package replaytest

import (
	"context"
	"encoding/json"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Capability names backend features that may be unsupported by a concrete
// implementation.
type Capability string

const (
	// CapabilityEventPage marks strict event page reads.
	CapabilityEventPage Capability = "event_page"
	// CapabilityTTL marks time-to-live expiry.
	CapabilityTTL Capability = "ttl"
	// CapabilityTrack marks persisted track events.
	CapabilityTrack Capability = "track"
	// CapabilityMemorySearch marks memory search support.
	CapabilityMemorySearch Capability = "memory_search"
	// CapabilityStateDelete marks session-state key delete support.
	CapabilityStateDelete Capability = "state_delete"
	// CapabilityStateClear marks full session-state clear support.
	CapabilityStateClear Capability = "state_clear"
)

// Backend persists replay operations and returns a normalized snapshot.
type Backend interface {
	Name() string
	Supports(Capability) bool
	Unsupported(Capability) string
	Apply(context.Context, ReplayCase) (*Snapshot, error)
	Close() error
}

// ServiceBundle supplies concrete services for one backend replay run.
//
// Backends such as SQLite, Redis, Postgres, MySQL, and ClickHouse live in
// independent Go modules. Tests in those modules can reuse this package by
// returning their concrete services from a ServiceFactory instead of copying
// the replay driver.
type ServiceBundle struct {
	SessionService     session.Service
	MemoryService      memory.Service
	TrackService       session.TrackService
	TTLProbe           func(context.Context) error
	DeleteSessionState func(context.Context, session.Key, string) error
	ClearSessionState  func(context.Context, session.Key) error
	Close              func() error
}

// ServiceFactory creates fresh backend services for one replay case.
type ServiceFactory func(context.Context, ReplayCase) (*ServiceBundle, error)

// ServiceBackendOption configures a service-backed replay backend.
type ServiceBackendOption func(*serviceBackend)

// ReplayCase is one deterministic input trajectory.
type ReplayCase struct {
	Name           string            `json:"name"`
	Description    string            `json:"description,omitempty"`
	Key            session.Key       `json:"key"`
	ReadEventLimit int               `json:"read_event_limit,omitempty"`
	Operations     []Operation       `json:"operations"`
	MemoryQueries  []MemoryQuerySpec `json:"memory_queries,omitempty"`
}

// OperationKind identifies a replay operation.
type OperationKind string

const (
	// OpAppendEvent appends one session event.
	OpAppendEvent OperationKind = "append_event"
	// OpSetState sets or overwrites one session state key.
	OpSetState OperationKind = "set_state"
	// OpDeleteState deletes one session state key.
	OpDeleteState OperationKind = "delete_state"
	// OpClearState clears all session state keys.
	OpClearState OperationKind = "clear_state"
	// OpAddMemory adds one memory entry.
	OpAddMemory OperationKind = "add_memory"
	// OpUpdateMemory updates one logical memory entry.
	OpUpdateMemory OperationKind = "update_memory"
	// OpDeleteMemory deletes one logical memory entry.
	OpDeleteMemory OperationKind = "delete_memory"
	// OpClearMemory clears all memories for the replay user.
	OpClearMemory OperationKind = "clear_memory"
	// OpWriteSummary writes or updates one summary.
	OpWriteSummary OperationKind = "write_summary"
	// OpAppendTrack appends one track event.
	OpAppendTrack OperationKind = "append_track"
	// OpRetryEvent appends the same event through retry semantics.
	OpRetryEvent OperationKind = "retry_event"
	// OpConcurrent runs child operations from separate goroutines. The harness
	// gives earlier operations a longer deterministic delay so commits are
	// intentionally interleaved instead of serialized in the listed order.
	OpConcurrent OperationKind = "concurrent"
	// OpUnsupportedProbe records unsupported backend capability metadata.
	OpUnsupportedProbe OperationKind = "unsupported_probe"
	// OpTTLProbe requests a real session TTL expiry check for backends that
	// claim CapabilityTTL support.
	OpTTLProbe OperationKind = "ttl_probe"
)

// Operation is a single replay input step. Only the field matching Kind is used.
type Operation struct {
	Kind        OperationKind `json:"kind"`
	Event       *EventSpec    `json:"event,omitempty"`
	State       *StateSpec    `json:"state,omitempty"`
	Memory      *MemorySpec   `json:"memory,omitempty"`
	Summary     *SummarySpec  `json:"summary,omitempty"`
	Track       *TrackSpec    `json:"track,omitempty"`
	Concurrent  []Operation   `json:"concurrent,omitempty"`
	Unsupported Capability    `json:"unsupported,omitempty"`
	Note        string        `json:"note,omitempty"`
}

// EventSpec describes one session event.
type EventSpec struct {
	LogicalID          string                          `json:"logical_id"`
	InvocationID       string                          `json:"invocation_id"`
	ParentInvocationID string                          `json:"parent_invocation_id,omitempty"`
	ParentMetadata     *event.ParentInvocationMetadata `json:"parent_metadata,omitempty"`
	Author             string                          `json:"author"`
	Role               model.Role                      `json:"role"`
	Content            string                          `json:"content,omitempty"`
	ToolCalls          []ToolCallSpec                  `json:"tool_calls,omitempty"`
	ToolID             string                          `json:"tool_id,omitempty"`
	ToolName           string                          `json:"tool_name,omitempty"`
	Branch             string                          `json:"branch,omitempty"`
	Tag                string                          `json:"tag,omitempty"`
	FilterKey          string                          `json:"filter_key,omitempty"`
	RequiresCompletion bool                            `json:"requires_completion,omitempty"`
	LongRunningToolIDs map[string]struct{}             `json:"long_running_tool_ids,omitempty"`
	StateDelta         map[string]json.RawMessage      `json:"state_delta,omitempty"`
	Extensions         map[string]any                  `json:"extensions,omitempty"`
	Object             string                          `json:"object,omitempty"`
	Done               bool                            `json:"done,omitempty"`
	Partial            bool                            `json:"partial,omitempty"`
	UseSequence        bool                            `json:"-"`
	Sequence           int                             `json:"-"`
}

// ToolCallSpec describes a function call inside an assistant event.
type ToolCallSpec struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// StateSpec describes state mutation.
type StateSpec struct {
	Key   string          `json:"key,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// MemorySpec describes memory mutation.
type MemorySpec struct {
	ID       string           `json:"id,omitempty"`
	Content  string           `json:"content,omitempty"`
	Topics   []string         `json:"topics,omitempty"`
	Metadata *memory.Metadata `json:"metadata,omitempty"`
	Query    string           `json:"query,omitempty"`
}

// MemoryQuerySpec describes one memory retrieval probe.
type MemoryQuerySpec struct {
	Name  string `json:"name"`
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// SummarySpec requests deterministic summary generation for a filter key.
type SummarySpec struct {
	FilterKey string `json:"filter_key"`
	Force     bool   `json:"force"`
}

// TrackSpec describes one observability track event.
type TrackSpec struct {
	Name      session.Track  `json:"name"`
	Payload   map[string]any `json:"payload"`
	Timestamp time.Time      `json:"timestamp,omitempty"`
}

// Snapshot is the normalized state read back from one backend after a case.
type Snapshot struct {
	Case        string                     `json:"case"`
	Backend     string                     `json:"backend"`
	SessionID   string                     `json:"session_id"`
	AppName     string                     `json:"app_name"`
	UserID      string                     `json:"user_id"`
	Events      []NormalizedEvent          `json:"events"`
	State       map[string]NormalizedValue `json:"state"`
	Memories    []NormalizedMemory         `json:"memories"`
	MemoryQuery []NormalizedMemoryQuery    `json:"memory_queries,omitempty"`
	Summaries   []NormalizedSummary        `json:"summaries"`
	Tracks      []NormalizedTrack          `json:"tracks"`
	Unsupported []UnsupportedFeature       `json:"unsupported,omitempty"`
	EventOrder  []string                   `json:"event_order,omitempty"`
	RawEventIDs map[string]int             `json:"-"`
}

// UnsupportedFeature records an unsupported backend capability.
type UnsupportedFeature struct {
	Capability  Capability `json:"capability"`
	AllowedDiff bool       `json:"allowed_diff"`
	Explanation string     `json:"explanation"`
}

// NormalizedValue preserves absent/null/value distinctions.
type NormalizedValue struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

// NormalizedEvent is a stable event representation.
type NormalizedEvent struct {
	ID                 string                          `json:"id,omitempty"`
	Index              int                             `json:"index"`
	InvocationID       string                          `json:"invocation_id,omitempty"`
	ParentInvocationID string                          `json:"parent_invocation_id,omitempty"`
	ParentMetadata     *event.ParentInvocationMetadata `json:"parent_metadata,omitempty"`
	Author             string                          `json:"author"`
	Object             string                          `json:"object,omitempty"`
	Done               bool                            `json:"done,omitempty"`
	RequiresCompletion bool                            `json:"requires_completion,omitempty"`
	Role               string                          `json:"role"`
	Content            string                          `json:"content,omitempty"`
	ToolCalls          []NormalizedToolCall            `json:"tool_calls,omitempty"`
	ToolID             string                          `json:"tool_id,omitempty"`
	ToolName           string                          `json:"tool_name,omitempty"`
	Branch             string                          `json:"branch,omitempty"`
	Tag                string                          `json:"tag,omitempty"`
	FilterKey          string                          `json:"filter_key,omitempty"`
	LongRunningToolIDs map[string]struct{}             `json:"long_running_tool_ids,omitempty"`
	StateDelta         map[string]NormalizedValue      `json:"state_delta,omitempty"`
	Extensions         map[string]string               `json:"extensions,omitempty"`
}

// NormalizedToolCall is a stable tool call representation.
type NormalizedToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// NormalizedMemory is a stable memory representation.
type NormalizedMemory struct {
	ID        string            `json:"id"`
	BackendID string            `json:"-"`
	StableID  string            `json:"stable_id"`
	Content   string            `json:"content"`
	Topics    []string          `json:"topics,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Scope     string            `json:"scope"`
	ScoreBand string            `json:"score_band,omitempty"`
}

// NormalizedMemoryQuery is a stable memory retrieval result.
type NormalizedMemoryQuery struct {
	Name    string             `json:"name"`
	Query   string             `json:"query"`
	Results []NormalizedMemory `json:"results"`
}

// NormalizedSummary is a stable summary representation.
type NormalizedSummary struct {
	FilterKey      string   `json:"filter_key"`
	Text           string   `json:"text"`
	Version        int      `json:"version"`
	SessionID      string   `json:"session_id"`
	Topics         []string `json:"topics,omitempty"`
	UpdatedAt      string   `json:"updated_at"`
	CutoffEventRef string   `json:"cutoff_event_ref,omitempty"`
}

// NormalizedTrack is a stable track representation.
type NormalizedTrack struct {
	Name   string                 `json:"name"`
	Events []NormalizedTrackEvent `json:"events"`
}

// NormalizedTrackEvent is a stable track event representation.
type NormalizedTrackEvent struct {
	Index     int    `json:"index"`
	Type      string `json:"type,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Payload   string `json:"payload"`
}

// Report is emitted as JSON for human and CI consumption.
type Report struct {
	GeneratedAt time.Time    `json:"generated_at"`
	BaseBackend string       `json:"base_backend"`
	Cases       []CaseReport `json:"cases"`
}

// CaseReport contains all backend results for one case.
type CaseReport struct {
	Case        string               `json:"case"`
	SessionID   string               `json:"session_id"`
	Compared    []string             `json:"compared_backends"`
	Differences []Difference         `json:"differences"`
	Unsupported []BackendUnsupported `json:"unsupported,omitempty"`
}

// BackendUnsupported records backend-specific unsupported capabilities.
type BackendUnsupported struct {
	Backend     string               `json:"backend"`
	Unsupported []UnsupportedFeature `json:"unsupported"`
}

// Difference records one normalized mismatch.
type Difference struct {
	Case         string `json:"case"`
	Backend      string `json:"backend"`
	SessionID    string `json:"session_id"`
	Locator      string `json:"locator,omitempty"`
	FieldPath    string `json:"field_path"`
	BaseValue    any    `json:"base_value,omitempty"`
	CompareValue any    `json:"compare_value,omitempty"`
	AllowedDiff  bool   `json:"allowed_diff"`
	Explanation  string `json:"explanation,omitempty"`
}
