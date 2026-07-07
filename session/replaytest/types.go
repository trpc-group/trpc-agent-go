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

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Capability names backend features that may be unsupported by a concrete
// implementation.
type Capability string

const (
	CapabilityEventPage    Capability = "event_page"
	CapabilityTTL          Capability = "ttl"
	CapabilityTrack        Capability = "track"
	CapabilityMemorySearch Capability = "memory_search"
)

// Backend persists replay operations and returns a normalized snapshot.
type Backend interface {
	Name() string
	Supports(Capability) bool
	Unsupported(Capability) string
	Apply(context.Context, ReplayCase) (*Snapshot, error)
	Close() error
}

// ReplayCase is one deterministic input trajectory.
type ReplayCase struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Key         session.Key `json:"key"`
	Operations  []Operation `json:"operations"`
}

// OperationKind identifies a replay operation.
type OperationKind string

const (
	OpAppendEvent      OperationKind = "append_event"
	OpSetState         OperationKind = "set_state"
	OpDeleteState      OperationKind = "delete_state"
	OpClearState       OperationKind = "clear_state"
	OpAddMemory        OperationKind = "add_memory"
	OpUpdateMemory     OperationKind = "update_memory"
	OpDeleteMemory     OperationKind = "delete_memory"
	OpClearMemory      OperationKind = "clear_memory"
	OpWriteSummary     OperationKind = "write_summary"
	OpAppendTrack      OperationKind = "append_track"
	OpRetryEvent       OperationKind = "retry_event"
	OpUnsupportedProbe OperationKind = "unsupported_probe"
)

// Operation is a single replay input step. Only the field matching Kind is used.
type Operation struct {
	Kind        OperationKind `json:"kind"`
	Event       *EventSpec    `json:"event,omitempty"`
	State       *StateSpec    `json:"state,omitempty"`
	Memory      *MemorySpec   `json:"memory,omitempty"`
	Summary     *SummarySpec  `json:"summary,omitempty"`
	Track       *TrackSpec    `json:"track,omitempty"`
	Unsupported Capability    `json:"unsupported,omitempty"`
	Note        string        `json:"note,omitempty"`
}

// EventSpec describes one session event.
type EventSpec struct {
	LogicalID    string                     `json:"logical_id"`
	InvocationID string                     `json:"invocation_id"`
	Author       string                     `json:"author"`
	Role         model.Role                 `json:"role"`
	Content      string                     `json:"content,omitempty"`
	ToolCalls    []ToolCallSpec             `json:"tool_calls,omitempty"`
	ToolID       string                     `json:"tool_id,omitempty"`
	ToolName     string                     `json:"tool_name,omitempty"`
	Branch       string                     `json:"branch,omitempty"`
	Tag          string                     `json:"tag,omitempty"`
	FilterKey    string                     `json:"filter_key,omitempty"`
	StateDelta   map[string]json.RawMessage `json:"state_delta,omitempty"`
	Extensions   map[string]any             `json:"extensions,omitempty"`
	Object       string                     `json:"object,omitempty"`
	Done         bool                       `json:"done,omitempty"`
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
	Summaries   []NormalizedSummary        `json:"summaries"`
	Tracks      []NormalizedTrack          `json:"tracks"`
	Unsupported []UnsupportedFeature       `json:"unsupported,omitempty"`
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
	Index      int                        `json:"index"`
	Author     string                     `json:"author"`
	Role       string                     `json:"role"`
	Content    string                     `json:"content,omitempty"`
	ToolCalls  []NormalizedToolCall       `json:"tool_calls,omitempty"`
	ToolID     string                     `json:"tool_id,omitempty"`
	ToolName   string                     `json:"tool_name,omitempty"`
	Branch     string                     `json:"branch,omitempty"`
	Tag        string                     `json:"tag,omitempty"`
	FilterKey  string                     `json:"filter_key,omitempty"`
	StateDelta map[string]NormalizedValue `json:"state_delta,omitempty"`
	Extensions map[string]string          `json:"extensions,omitempty"`
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
	StableID  string            `json:"stable_id"`
	Content   string            `json:"content"`
	Topics    []string          `json:"topics,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Scope     string            `json:"scope"`
	ScoreBand string            `json:"score_band,omitempty"`
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
	Index   int    `json:"index"`
	Type    string `json:"type,omitempty"`
	Payload string `json:"payload"`
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
