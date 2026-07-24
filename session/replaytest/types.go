//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// CapabilityName identifies one observable backend behavior.
type CapabilityName string

// Replay capability names cover every behavior compared by the harness.
const (
	CapabilityEvents              CapabilityName = "events"
	CapabilityState               CapabilityName = "state"
	CapabilityAppState            CapabilityName = "app_state"
	CapabilityUserState           CapabilityName = "user_state"
	CapabilityMemory              CapabilityName = "memory"
	CapabilityMemorySearch        CapabilityName = "memory_search"
	CapabilitySummary             CapabilityName = "summary"
	CapabilityTracks              CapabilityName = "tracks"
	CapabilityEventPaging         CapabilityName = "event_paging"
	CapabilityTTL                 CapabilityName = "ttl"
	CapabilityStateDelete         CapabilityName = "state_delete"
	CapabilityStateClear          CapabilityName = "state_clear"
	CapabilityEventStateDeltaNull CapabilityName = "event_state_delta_null"
)

// Capability declares whether a backend supports a behavior and whether an
// unsupported result is an accepted, documented difference.
type Capability struct {
	Supported   bool   `json:"supported"`
	AllowedDiff bool   `json:"allowed_diff"`
	Reason      string `json:"reason,omitempty"`
}

// CapabilitySet is the explicit capability contract for a backend.
type CapabilitySet map[CapabilityName]Capability

// Clone returns a copy of the capability set.
func (c CapabilitySet) Clone() CapabilitySet {
	out := make(CapabilitySet, len(c))
	for name, capability := range c {
		out[name] = capability
	}
	return out
}

// Supports reports whether a capability is explicitly declared as supported.
func (c CapabilitySet) Supports(name CapabilityName) bool {
	capability, ok := c[name]
	return ok && capability.Supported
}

// Backend is one Session/Memory implementation participating in a replay.
type Backend struct {
	Name         string
	Session      session.Service
	Memory       memory.Service
	Track        session.TrackService
	SessionKey   session.Key
	Capabilities CapabilitySet
	Load         func(context.Context, Backend) (CaptureInput, error)
}

// Validate checks the required backend fields.
func (b Backend) Validate() error {
	if b.Name == "" {
		return fmt.Errorf("backend name is required")
	}
	if b.Session == nil {
		return fmt.Errorf("backend %q session service is required", b.Name)
	}
	if err := b.SessionKey.CheckSessionKey(); err != nil {
		return fmt.Errorf("backend %q session key: %w", b.Name, err)
	}
	if b.Capabilities == nil {
		return fmt.Errorf("backend %q capabilities are required", b.Name)
	}
	for name, capability := range b.Capabilities {
		if !capability.Supported &&
			strings.TrimSpace(capability.Reason) == "" {
			return fmt.Errorf(
				"backend %q unsupported capability %q requires a reason",
				b.Name,
				name,
			)
		}
	}
	return nil
}

// CaptureInput contains public, replay-visible backend state before normalization.
type CaptureInput struct {
	Session       *session.Session
	AppState      session.StateMap
	UserState     session.StateMap
	Memories      []*memory.Entry
	MemoryQueries map[string][]*memory.Entry
	Unsupported   map[CapabilityName]string
}

// MemoryOrder specifies how a memory result set is compared.
type MemoryOrder string

// Memory comparison orders support exact and unordered result sets.
const (
	MemoryOrderExact     MemoryOrder = "exact"
	MemoryOrderUnordered MemoryOrder = "unordered"
)

// NormalizeOptions controls semantic normalization.
type NormalizeOptions struct {
	MemoryOrder         MemoryOrder
	MemoryQueryOrders   map[string]MemoryOrder
	ScorePrecision      int
	VolatilePayloadKeys map[string]struct{}
}

// EventOrderContract defines the legal causal order for a concurrent case.
type EventOrderContract struct {
	ExactLogicalIDs []string    `json:"exact_logical_ids,omitempty"`
	HappensBefore   [][2]string `json:"happens_before,omitempty"`
}

// ReplayCase is one reusable operation program and its comparison contract.
type ReplayCase struct {
	Name       string
	Required   []CapabilityName
	Operations []Operation
	Normalize  NormalizeOptions
	Order      EventOrderContract
	Allowed    []AllowedDiff
}

// BackendFactory creates an isolated backend for one case.
type BackendFactory struct {
	Name         string
	Capabilities CapabilitySet
	Create       func(context.Context, string) (Backend, func() error, error)
}

// RunResult returns the report and captured traces for anomaly tests.
type RunResult struct {
	Report CaseReport
	Traces map[string]Trace
}

// Snapshot is a normalized representation of replay-visible backend state.
type Snapshot struct {
	SessionID     string                          `json:"session_id"`
	AppName       string                          `json:"app_name"`
	UserID        string                          `json:"user_id"`
	Events        []map[string]any                `json:"events"`
	State         map[string]any                  `json:"state"`
	AppState      map[string]any                  `json:"app_state"`
	UserState     map[string]any                  `json:"user_state"`
	Memories      []MemorySnapshot                `json:"memories"`
	MemoryQueries map[string][]MemorySnapshot     `json:"memory_queries,omitempty"`
	Summaries     map[string]SummarySnapshot      `json:"summaries"`
	Tracks        map[string][]TrackEventSnapshot `json:"tracks"`
	Unsupported   map[CapabilityName]string       `json:"unsupported,omitempty"`
}

// Clone returns a deep copy suitable for anomaly injection.
func (s Snapshot) Clone() (Snapshot, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return Snapshot{}, fmt.Errorf("marshal snapshot clone: %w", err)
	}
	var out Snapshot
	if err := decodeJSON(raw, &out); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot clone: %w", err)
	}
	return out, nil
}

// TaggedBytes preserves nil, JSON, UTF-8, and arbitrary binary state values.
type TaggedBytes struct {
	Kind  string `json:"kind"`
	Value any    `json:"value,omitempty"`
}

// MemorySnapshot is a stable memory representation.
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

// SummarySnapshot preserves summary ownership, filter, and semantic boundary.
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
	LastEventLogicalID  string   `json:"last_event_logical_id,omitempty"`
	LastEventIndex      *int     `json:"last_event_index"`
}

// TrackEventSnapshot is one ordered track event with volatile time removed.
type TrackEventSnapshot struct {
	Track   string `json:"track"`
	Payload any    `json:"payload"`
}

// CheckpointSnapshot captures a transition after a logical operation.
type CheckpointSnapshot struct {
	Name     string   `json:"name"`
	AfterOp  string   `json:"after_op"`
	Snapshot Snapshot `json:"snapshot"`
}

// Trace contains transition snapshots plus the final backend snapshot.
type Trace struct {
	Backend     string               `json:"backend"`
	Checkpoints []CheckpointSnapshot `json:"checkpoints,omitempty"`
	Final       Snapshot             `json:"final"`
}

// MissingValue distinguishes an absent field from an explicit JSON null.
type MissingValue struct {
	Missing bool `json:"__missing"`
}

// AllowedDiff documents one narrowly accepted backend difference.
type AllowedDiff struct {
	Section  string `json:"section"`
	Path     string `json:"path"`
	BackendA string `json:"backend_a"`
	BackendB string `json:"backend_b"`
	Reason   string `json:"reason"`
}

// Diff is one located semantic mismatch.
type Diff struct {
	Case             string  `json:"case"`
	SessionID        string  `json:"session_id"`
	BackendA         string  `json:"backend_a"`
	BackendB         string  `json:"backend_b"`
	Section          string  `json:"section"`
	Path             string  `json:"path"`
	Baseline         any     `json:"baseline"`
	Compared         any     `json:"compared"`
	BaselinePresent  bool    `json:"baseline_present"`
	ComparedPresent  bool    `json:"compared_present"`
	AllowedDiff      bool    `json:"allowed_diff"`
	Explanation      string  `json:"explanation"`
	Checkpoint       string  `json:"checkpoint,omitempty"`
	EventIndex       *int    `json:"event_index,omitempty"`
	MemoryID         string  `json:"memory_id,omitempty"`
	SummaryID        string  `json:"summary_id,omitempty"`
	SummaryFilterKey *string `json:"summary_filter_key,omitempty"`
	TrackName        string  `json:"track_name,omitempty"`
}

// Unsupported records one skipped backend capability.
type Unsupported struct {
	Backend     string         `json:"backend"`
	Capability  CapabilityName `json:"capability"`
	AllowedDiff bool           `json:"allowed_diff"`
	Reason      string         `json:"reason"`
}

// CaseStatus is the result state for one replay case.
type CaseStatus string

// Case statuses describe the aggregate outcome of one replay case.
const (
	StatusPassed       CaseStatus = "passed"
	StatusFailed       CaseStatus = "failed"
	StatusSkipped      CaseStatus = "skipped"
	StatusMixed        CaseStatus = "mixed"
	StatusInconclusive CaseStatus = "inconclusive"
)

// CaseReport is the complete result for one replay case.
type CaseReport struct {
	Name         string                   `json:"name"`
	Status       CaseStatus               `json:"status"`
	Backends     []string                 `json:"backends"`
	Capabilities map[string]CapabilitySet `json:"capabilities"`
	Unsupported  []Unsupported            `json:"unsupported,omitempty"`
	Diffs        []Diff                   `json:"diffs"`
	Duration     time.Duration            `json:"duration"`
}

// Report is the aggregate replay report artifact.
type Report struct {
	Version       int          `json:"version"`
	GeneratedAt   time.Time    `json:"generated_at"`
	Baseline      string       `json:"baseline"`
	Backends      []string     `json:"backends"`
	TotalCases    int          `json:"total_cases"`
	PassedCases   int          `json:"passed_cases"`
	FailedCases   int          `json:"failed_cases"`
	SkippedCases  int          `json:"skipped_cases"`
	MixedCases    int          `json:"mixed_cases"`
	Inconclusive  int          `json:"inconclusive_cases"`
	AllowedDiffs  int          `json:"allowed_diffs"`
	BlockingDiffs int          `json:"blocking_diffs"`
	Cases         []CaseReport `json:"cases"`
}
