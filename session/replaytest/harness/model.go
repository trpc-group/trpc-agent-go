//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"encoding/json"
	"time"
)

// CaseKey identifies the session a replay case operates on.
type CaseKey struct {
	AppName   string `json:"appName"`
	UserID    string `json:"userID"`
	SessionID string `json:"sessionID"`
}

// ToolCallSpec declares a tool call inside an event.
type ToolCallSpec struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// EventSpec is the declarative form of an event to append.
type EventSpec struct {
	Author     string                     `json:"author"`
	Role       string                     `json:"role"`
	Content    string                     `json:"content,omitempty"`
	ToolCalls  []ToolCallSpec             `json:"toolCalls,omitempty"`
	ToolID     string                     `json:"toolID,omitempty"`
	Branch     string                     `json:"branch,omitempty"`
	Tag        string                     `json:"tag,omitempty"`
	FilterKey  string                     `json:"filterKey,omitempty"`
	StateDelta map[string]string          `json:"stateDelta,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
	Partial    bool                       `json:"partial,omitempty"`
}

// Operation is a single declarative step in a replay case.
type Operation struct {
	Type       string          `json:"type"`
	Event      *EventSpec      `json:"event,omitempty"`
	Key        string          `json:"key,omitempty"`
	Value      string          `json:"value,omitempty"`
	Bytes      []byte          `json:"bytes,omitempty"`
	IsNil      bool            `json:"isNil,omitempty"`
	Topics     []string        `json:"topics,omitempty"`
	MemoryID   string          `json:"memoryID,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	FilterKey  string          `json:"filterKey,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	Force      bool            `json:"force,omitempty"`
	Track      string          `json:"track,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Repeat     int             `json:"repeat,omitempty"`
	FailAfter  int             `json:"failAfter,omitempty"`
	Concurrent bool            `json:"concurrent,omitempty"`
}

// Locator pins a diff to a precise coordinate for reporting.
type Locator struct {
	SessionID        string  `json:"sessionID,omitempty"`
	EventIndex       *int    `json:"eventIndex,omitempty"`
	SummaryFilterKey *string `json:"summaryFilterKey,omitempty"`
	MemoryID         string  `json:"memoryID,omitempty"`
	TrackName        string  `json:"trackName,omitempty"`
}

// ExpectedDefect describes the defect a faulty case must surface.
type ExpectedDefect struct {
	Category  string  `json:"category"`
	FieldPath string  `json:"fieldPath,omitempty"`
	Locator   Locator `json:"locator"`
}

// ReplayCase is one standardized replay scenario.
type ReplayCase struct {
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	Key            CaseKey         `json:"key"`
	Mode           string          `json:"mode,omitempty"`
	Operations     []Operation     `json:"operations"`
	FaultInjection string          `json:"faultInjection,omitempty"`
	ExpectedDefect *ExpectedDefect `json:"expectedDefect,omitempty"`
}

// EventView is the normalized, comparable projection of a stored event.
type EventView struct {
	Author     string            `json:"author"`
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	ToolCalls  []ToolCallSpec    `json:"toolCalls,omitempty"`
	ToolID     string            `json:"toolID,omitempty"`
	Branch     string            `json:"branch,omitempty"`
	Tag        string            `json:"tag,omitempty"`
	FilterKey  string            `json:"filterKey,omitempty"`
	StateDelta map[string]string `json:"stateDelta,omitempty"`
	Extensions map[string]any    `json:"extensions,omitempty"`
}

// MemoryView is the normalized, comparable projection of a stored memory.
type MemoryView struct {
	ID       string         `json:"id"`
	Content  string         `json:"content"`
	Topics   []string       `json:"topics,omitempty"`
	Kind     string         `json:"kind,omitempty"`
	Score    float64        `json:"score,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SummaryView is the normalized, comparable projection of a stored summary.
type SummaryView struct {
	FilterKey string    `json:"filterKey"`
	Text      string    `json:"text"`
	Topics    []string  `json:"topics,omitempty"`
	Version   int       `json:"version,omitempty"`
	SessionID string    `json:"sessionID,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
	CutoffAt  time.Time `json:"cutoffAt,omitempty"`
}

// TrackView is the normalized, comparable projection of a track event.
type TrackView struct {
	Name      string    `json:"name"`
	Payload   any       `json:"payload,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// Snapshot is the unified read-back projection of one backend for one case.
type Snapshot struct {
	SessionID string            `json:"sessionID"`
	Events    []EventView       `json:"events"`
	State     map[string]string `json:"state"`
	Memories  []MemoryView      `json:"memories"`
	Summaries []SummaryView     `json:"summaries"`
	Tracks    []TrackView       `json:"tracks"`
}
