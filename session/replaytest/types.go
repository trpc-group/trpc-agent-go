//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package replaytest provides a multi-backend replay consistency test
// framework for Session, Memory, and Summary operations. It drives the
// same deterministic inputs through different backend implementations
// and produces a structured diff report.
package replaytest

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Backend bundles a session service and a memory service for a
// single backend implementation (e.g. InMemory, SQLite, Redis).
type Backend struct {
	Name           string
	SessionService session.Service
	MemoryService  memory.Service
	Setup          func(ctx context.Context) error
	Teardown       func(ctx context.Context) error
}

// EventSpec describes a backend-independent event to be appended
// during a replay case.
type EventSpec struct {
	Author         string            `json:"author"`
	InvocationID   string            `json:"invocation_id"`
	Role           string            `json:"role"`
	Content        string            `json:"content,omitempty"`
	ToolCalls      []ToolCallSpec    `json:"tool_calls,omitempty"`
	ToolResponse   *ToolResponseSpec `json:"tool_response,omitempty"`
	StateDelta     map[string]string `json:"state_delta,omitempty"`
	FilterKey      string            `json:"filter_key,omitempty"`
	Branch         string            `json:"branch,omitempty"`
	Tag            string            `json:"tag,omitempty"`
}

// ToolCallSpec represents a tool call initiated by the assistant.
type ToolCallSpec struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolResponseSpec represents a tool response (function result).
type ToolResponseSpec struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// MemoryWriteSpec describes a memory entry to be stored.
type MemoryWriteSpec struct {
	Memory  string   `json:"memory"`
	Topics  []string `json:"topics,omitempty"`
	Kind    string   `json:"kind,omitempty"`
}

// MemoryQuerySpec describes a memory search to perform during replay.
type MemoryQuerySpec struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// SummaryStep describes a summary operation to perform after a
// specific event index.
type SummaryStep struct {
	AfterEventIndex int    `json:"after_event_index"`
	FilterKey       string `json:"filter_key,omitempty"`
	Force           bool   `json:"force,omitempty"`
}

// TrackEventSpec describes a track event to append during replay.
type TrackEventSpec struct {
	Track   string `json:"track"`
	Payload string `json:"payload"`
}

// ReplayCase defines a single replay test scenario. It contains the
// complete input trajectory and the expected outcome criteria.
type ReplayCase struct {
	Name         string            `json:"name"`
	AppName      string            `json:"app_name"`
	UserID       string            `json:"user_id"`
	SessionID    string            `json:"session_id"`
	InitialState map[string]string `json:"initial_state,omitempty"`
	Events       []EventSpec       `json:"events"`
	MemoryWrites []MemoryWriteSpec `json:"memory_writes,omitempty"`
	MemoryQueries []MemoryQuerySpec `json:"memory_queries,omitempty"`
	SummarySteps []SummaryStep     `json:"summary_steps,omitempty"`
	TrackEvents  []TrackEventSpec  `json:"track_events,omitempty"`
}

// Snapshot is a normalized, backend-independent view of a session's
// state after a replay case completes. All auto-generated fields
// are stripped or normalized so that cross-backend comparison is
// meaningful.
type Snapshot struct {
	SessionID string           `json:"session_id"`
	State     map[string]string `json:"state"`
	Events    []NormalizedEvent `json:"events"`
	Memories  []NormalizedMemory `json:"memories"`
	Summaries []NormalizedSummary `json:"summaries"`
	Tracks    []NormalizedTrack `json:"tracks"`
}

// NormalizedEvent is an event with auto-generated fields removed.
type NormalizedEvent struct {
	Author        string            `json:"author"`
	Role          string            `json:"role"`
	Content       string            `json:"content,omitempty"`
	ToolCallID    string            `json:"tool_call_id,omitempty"`
	ToolCallName  string            `json:"tool_call_name,omitempty"`
	ToolCallArgs  string            `json:"tool_call_args,omitempty"`
	ToolResponseID   string         `json:"tool_response_id,omitempty"`
	ToolResponseContent string      `json:"tool_response_content,omitempty"`
	StateDelta    map[string]string `json:"state_delta,omitempty"`
	FilterKey     string            `json:"filter_key,omitempty"`
	Branch        string            `json:"branch,omitempty"`
	Tag           string            `json:"tag,omitempty"`
}

// NormalizedMemory is a memory entry with auto-generated fields removed.
type NormalizedMemory struct {
	Content  string   `json:"content"`
	Topics   []string `json:"topics,omitempty"`
	Score    float64  `json:"score,omitempty"`
}

// NormalizedSummary is a summary with auto-generated timestamps removed.
type NormalizedSummary struct {
	FilterKey string `json:"filter_key"`
	Summary   string `json:"summary"`
}

// NormalizedTrack is a track event with auto-generated timestamps removed.
type NormalizedTrack struct {
	Track   string `json:"track"`
	Payload string `json:"payload"`
}

// DiffReport is the top-level output structure containing all
// cross-backend diffs for a replay run.
type DiffReport struct {
	CaseName   string     `json:"case_name"`
	SessionID  string     `json:"session_id"`
	BackendA   string     `json:"backend_a"`
	BackendB   string     `json:"backend_b"`
	Diffs      []FieldDiff `json:"diffs"`
}

// FieldDiff describes a single field-level difference between
// two backend snapshots.
type FieldDiff struct {
	SessionID  string      `json:"session_id,omitempty"`
	EventIndex int         `json:"event_index,omitempty"`
	MemoryID   string      `json:"memory_id,omitempty"`
	SummaryID  string      `json:"summary_id,omitempty"`
	TrackName  string      `json:"track_name,omitempty"`
	FieldPath  string      `json:"field_path"`
	ValueA     interface{} `json:"value_a"`
	ValueB     interface{} `json:"value_b"`
	Allowed    bool        `json:"allowed"`
	Reason     string      `json:"reason,omitempty"`
}
