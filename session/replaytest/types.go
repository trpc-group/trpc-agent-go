//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package replaytest provides reusable cross-backend replay consistency helpers.
package replaytest

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Capability names understood by the replay helpers. Section capabilities
// control snapshot capture; EventStateDeltaNull records partial state semantics.
const (
	CapabilityEvents              = "events"
	CapabilityState               = "state"
	CapabilityEventStateDeltaNull = "event_state_delta_null"
	CapabilityMemory              = "memory"
	CapabilitySummary             = "summary"
	CapabilityTracks              = "tracks"
)

// Capability describes whether a backend supports one observable behavior.
type Capability struct {
	Supported   bool   `json:"supported"`
	Reason      string `json:"reason,omitempty"`
	AllowedDiff bool   `json:"allowed_diff"`
}

// Backend is a Session/Memory pair participating in one replay case.
// Load overrides capture reads for backends that need custom consistency logic;
// services used by Case.Run remain the caller's responsibility.
type Backend struct {
	Name         string
	Session      session.Service
	Memory       memory.Service
	SessionKey   session.Key
	Capabilities map[string]Capability
	Load         func(context.Context, Backend) (*session.Session, []*memory.Entry, error)
}

// Case replays the same operations against every configured backend.
type Case struct {
	Name                   string
	Run                    func(context.Context, Backend) error
	RequiredCapabilities   []string
	OrderEventsByTimestamp bool
	UnorderedMemories      bool
}

// Snapshot is the normalized, replay-visible state of one backend.
type Snapshot struct {
	SessionID   string                     `json:"session_id"`
	AppName     string                     `json:"app_name"`
	UserID      string                     `json:"user_id"`
	Events      []map[string]any           `json:"events"`
	State       map[string]any             `json:"state"`
	Memories    []MemorySnapshot           `json:"memories"`
	Summaries   map[string]SummarySnapshot `json:"summaries"`
	Tracks      map[string][]TrackSnapshot `json:"tracks"`
	Unsupported map[string]string          `json:"unsupported,omitempty"`
}

// Clone returns a deep copy suitable for drift injection in tests.
func (s Snapshot) Clone() (Snapshot, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return Snapshot{}, fmt.Errorf("marshal snapshot clone: %w", err)
	}
	var cloned Snapshot
	if err := decodeJSON(raw, &cloned); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot clone: %w", err)
	}
	return cloned, nil
}

// MemorySnapshot is a stable representation of a memory entry.
type MemorySnapshot struct {
	ID           string   `json:"id"`
	AppName      string   `json:"app_name"`
	UserID       string   `json:"user_id"`
	Rank         int      `json:"rank"`
	Score        float64  `json:"score"`
	Content      string   `json:"content"`
	Topics       []string `json:"topics,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	EventTime    string   `json:"event_time,omitempty"`
	Participants []string `json:"participants,omitempty"`
	Location     string   `json:"location,omitempty"`
}

// SummarySnapshot retains semantic boundary information without backend IDs.
type SummarySnapshot struct {
	SessionID           string   `json:"session_id"`
	AppName             string   `json:"app_name"`
	UserID              string   `json:"user_id"`
	FilterKey           string   `json:"filter_key"`
	BoundaryPresent     bool     `json:"boundary_present"`
	BoundaryFilterKey   string   `json:"boundary_filter_key"`
	Text                string   `json:"text"`
	Topics              []string `json:"topics,omitempty"`
	Version             int      `json:"version"`
	UpdatedAtEventIndex *int     `json:"updated_at_event_index"`
	CutoffAtEventIndex  *int     `json:"cutoff_at_event_index"`
	LastEventIDPresent  bool     `json:"last_event_id_present"`
	LastEventIndex      *int     `json:"last_event_index"`
}

// TrackSnapshot is one ordered track event with volatile time removed.
type TrackSnapshot struct {
	Track   string `json:"track"`
	Payload any    `json:"payload"`
}

// MissingValue is emitted in reports when a path exists on only one side.
// It distinguishes an absent value from an explicit JSON null.
type MissingValue struct {
	Missing bool `json:"missing"`
}

// AllowedDiff documents one intentional backend difference.
type AllowedDiff struct {
	Section  string `json:"section"`
	Path     string `json:"path"`
	BackendA string `json:"backend_a"`
	BackendB string `json:"backend_b"`
	Reason   string `json:"reason"`
}

// Diff identifies one normalized mismatch.
type Diff struct {
	Case            string  `json:"case"`
	SessionID       string  `json:"session_id"`
	BackendA        string  `json:"backend_a"`
	BackendB        string  `json:"backend_b"`
	Section         string  `json:"section"`
	Path            string  `json:"path"`
	Baseline        any     `json:"baseline"`
	Compared        any     `json:"compared"`
	BaselinePresent bool    `json:"baseline_present"`
	ComparedPresent bool    `json:"compared_present"`
	Allowed         bool    `json:"allowed_diff"`
	Explanation     string  `json:"explanation"`
	EventIndex      *int    `json:"event_index,omitempty"`
	MemoryID        string  `json:"memory_id,omitempty"`
	TrackName       string  `json:"track_name,omitempty"`
	SummaryKey      *string `json:"summary_filter_key,omitempty"`
}

// Report is the machine-readable output for one or more replay cases.
type Report struct {
	Version int          `json:"version"`
	Cases   []CaseReport `json:"cases"`
}

// CaseReport contains comparison results for a single case.
type CaseReport struct {
	Name                 string                           `json:"name"`
	SessionID            string                           `json:"session_id"`
	Backends             []string                         `json:"backends"`
	RequiredCapabilities []string                         `json:"required_capabilities,omitempty"`
	SkippedBackends      map[string][]string              `json:"skipped_backends,omitempty"`
	Inconclusive         bool                             `json:"inconclusive,omitempty"`
	Capabilities         map[string]map[string]Capability `json:"capabilities"`
	Diffs                []Diff                           `json:"diffs"`
}

// Normalizer configures volatile payload fields that have no replay semantics.
type Normalizer struct {
	VolatilePayloadKeys map[string]struct{}
}

// DefaultNormalizer returns the conservative default normalization policy.
func DefaultNormalizer() Normalizer {
	return Normalizer{VolatilePayloadKeys: map[string]struct{}{
		"duration": {}, "duration_ms": {}, "elapsed": {}, "elapsed_ms": {},
		"latency": {}, "latency_ms": {},
	}}
}
