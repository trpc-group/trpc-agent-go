//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replaytest provides internal building blocks for comparing replayed
// Session and Memory data across storage backends.
package replaytest

import "time"

// Capability identifies an optional backend feature used by a replay case.
type Capability string

const (
	CapabilitySession       Capability = "session"
	CapabilityMemory        Capability = "memory"
	CapabilitySummary       Capability = "summary"
	CapabilityTrack         Capability = "track"
	CapabilitySessionPaging Capability = "session_paging"
	CapabilityEventPaging   Capability = "event_paging"
	CapabilityTTL           Capability = "ttl"
	CapabilityMemorySearch  Capability = "memory_search"
)

// CapabilitySet records the features supported by one backend fixture.
type CapabilitySet map[Capability]bool

// Supports reports whether every requested capability is available.
func (set CapabilitySet) Supports(required ...Capability) bool {
	for _, capability := range required {
		if !set[capability] {
			return false
		}
	}
	return true
}

// Missing returns requested capabilities that are not available.
func (set CapabilitySet) Missing(required ...Capability) []Capability {
	missing := make([]Capability, 0, len(required))
	for _, capability := range required {
		if !set[capability] {
			missing = append(missing, capability)
		}
	}
	return missing
}

// Snapshot is the complete observable result of replaying one case.
type Snapshot struct {
	Sessions       []SessionSnapshot      `json:"sessions,omitempty"`
	Memories       []MemorySnapshot       `json:"memories,omitempty"`
	MemorySearches []MemorySearchSnapshot `json:"memory_searches,omitempty"`
	Unsupported    []UnsupportedFeature   `json:"unsupported,omitempty"`
}

// SessionSnapshot contains persisted session data relevant to replay.
type SessionSnapshot struct {
	ID        string                        `json:"id"`
	AppName   string                        `json:"app_name"`
	UserID    string                        `json:"user_id"`
	State     map[string]StateValueSnapshot `json:"state,omitempty"`
	Events    []EventSnapshot               `json:"events,omitempty"`
	Summaries []SummarySnapshot             `json:"summaries,omitempty"`
	Tracks    []TrackSnapshot               `json:"tracks,omitempty"`
	CreatedAt time.Time                     `json:"created_at,omitempty"`
	UpdatedAt time.Time                     `json:"updated_at,omitempty"`
}

// EventSnapshot contains the stable and semantically relevant event fields.
type EventSnapshot struct {
	ID           string                        `json:"id,omitempty"`
	InvocationID string                        `json:"invocation_id,omitempty"`
	Author       string                        `json:"author,omitempty"`
	Role         string                        `json:"role,omitempty"`
	Content      string                        `json:"content,omitempty"`
	Object       string                        `json:"object,omitempty"`
	Done         bool                          `json:"done"`
	ToolCalls    []ToolCallSnapshot            `json:"tool_calls,omitempty"`
	ToolResponse *ToolResponse                 `json:"tool_response,omitempty"`
	Branch       string                        `json:"branch,omitempty"`
	Tag          string                        `json:"tag,omitempty"`
	FilterKey    string                        `json:"filter_key,omitempty"`
	StateDelta   map[string]StateValueSnapshot `json:"state_delta,omitempty"`
	Extensions   map[string]any                `json:"extensions,omitempty"`
	Timestamp    time.Time                     `json:"timestamp,omitempty"`
}

// StateValueKind preserves how a Session state value was stored.
type StateValueKind string

const (
	StateValueNull   StateValueKind = "null"
	StateValueJSON   StateValueKind = "json"
	StateValueText   StateValueKind = "text"
	StateValueBinary StateValueKind = "binary"
)

// StateValueSnapshot distinguishes null, JSON, text, and binary state values.
// A missing key is represented by absence from the containing map.
type StateValueSnapshot struct {
	Kind  StateValueKind `json:"kind"`
	Value any            `json:"value,omitempty"`
}

// JSONStateValue returns a JSON-tagged state value.
func JSONStateValue(value any) StateValueSnapshot {
	return StateValueSnapshot{Kind: StateValueJSON, Value: value}
}

// NullStateValue returns an explicit JSON null state value.
func NullStateValue() StateValueSnapshot {
	return StateValueSnapshot{Kind: StateValueNull}
}

// TextStateValue returns a UTF-8 text state value.
func TextStateValue(value string) StateValueSnapshot {
	return StateValueSnapshot{Kind: StateValueText, Value: value}
}

// BinaryStateValue returns a binary state value with an isolated byte slice.
func BinaryStateValue(value []byte) StateValueSnapshot {
	return StateValueSnapshot{
		Kind: StateValueBinary, Value: append([]byte(nil), value...),
	}
}

// ToolCallSnapshot captures a tool call and provider-specific extension data.
type ToolCallSnapshot struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments any            `json:"arguments,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// ToolResponse captures the matching tool result.
type ToolResponse struct {
	ToolCallID string         `json:"tool_call_id"`
	Name       string         `json:"name,omitempty"`
	Content    string         `json:"content,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

// SummarySnapshot captures filter-aware summary ownership and cutoff data.
type SummarySnapshot struct {
	SessionID string         `json:"session_id"`
	FilterKey string         `json:"filter_key"`
	Text      string         `json:"text"`
	Version   int            `json:"version,omitempty"`
	Boundary  map[string]any `json:"boundary,omitempty"`
	UpdatedAt time.Time      `json:"updated_at,omitempty"`
}

// TrackSnapshot contains one named sequence of track events.
type TrackSnapshot struct {
	Name   string               `json:"name"`
	Events []TrackEventSnapshot `json:"events,omitempty"`
}

// TrackEventSnapshot captures track ordering and diagnostic semantics.
type TrackEventSnapshot struct {
	EventType    string         `json:"event_type,omitempty"`
	InvocationID string         `json:"invocation_id,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
	Error        string         `json:"error,omitempty"`
	Duration     time.Duration  `json:"duration,omitempty"`
	Timestamp    time.Time      `json:"timestamp,omitempty"`
}

// MemorySnapshot captures one persisted memory entry.
type MemorySnapshot struct {
	ID        string         `json:"id"`
	AppName   string         `json:"app_name"`
	UserID    string         `json:"user_id"`
	Scope     MemoryScope    `json:"scope"`
	Content   string         `json:"content"`
	Topics    []string       `json:"topics,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Score     float64        `json:"score,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
	UpdatedAt time.Time      `json:"updated_at,omitempty"`
}

// MemoryScope identifies the repository's user-scoped memory namespace.
type MemoryScope struct {
	AppName string `json:"app_name"`
	UserID  string `json:"user_id"`
}

// MemorySearchSnapshot captures one ordered memory query result.
type MemorySearchSnapshot struct {
	AppName string           `json:"app_name"`
	UserID  string           `json:"user_id"`
	Query   string           `json:"query"`
	Results []MemorySnapshot `json:"results"`
}

// UnsupportedFeature records an explicit backend capability gap.
type UnsupportedFeature struct {
	Capability Capability `json:"capability"`
	Reason     string     `json:"reason"`
}

// Locator identifies the semantic object containing a difference.
type Locator struct {
	SessionID        string `json:"session_id,omitempty"`
	EventIndex       *int   `json:"event_index,omitempty"`
	StateKey         string `json:"state_key,omitempty"`
	MemoryID         string `json:"memory_id,omitempty"`
	MemoryAppName    string `json:"memory_app_name,omitempty"`
	MemoryUserID     string `json:"memory_user_id,omitempty"`
	SummaryFilterKey string `json:"summary_filter_key,omitempty"`
	TrackName        string `json:"track_name,omitempty"`
}

// Difference describes one normalized mismatch.
type Difference struct {
	Case        string  `json:"case"`
	Backend     string  `json:"backend"`
	Path        string  `json:"path"`
	Locator     Locator `json:"locator,omitempty"`
	Baseline    any     `json:"baseline,omitempty"`
	Actual      any     `json:"actual,omitempty"`
	AllowedDiff bool    `json:"allowed_diff"`
	Explanation string  `json:"explanation,omitempty"`
}

// AllowedDiffRule permits one exact path or a deliberately bounded prefix.
type AllowedDiffRule struct {
	Case        string `json:"case,omitempty"`
	Backend     string `json:"backend,omitempty"`
	Path        string `json:"path"`
	PathPrefix  bool   `json:"path_prefix,omitempty"`
	Explanation string `json:"explanation"`
}

// UnsupportedAllowance permits one exact backend/case capability gap.
type UnsupportedAllowance struct {
	Backend    string     `json:"backend"`
	Case       string     `json:"case"`
	Capability Capability `json:"capability"`
	Reason     string     `json:"reason"`
}

// CompareOptions controls semantic comparison behavior.
type CompareOptions struct {
	ScoreTolerance    float64
	DurationTolerance time.Duration
	AllowedDiffRules  []AllowedDiffRule
}

// DefaultCompareOptions returns conservative comparison defaults.
func DefaultCompareOptions() CompareOptions {
	return CompareOptions{
		ScoreTolerance:    defaultScoreTolerance,
		DurationTolerance: time.Millisecond,
	}
}

// Report contains stable results for a replay matrix.
type Report struct {
	Baseline    string                  `json:"baseline"`
	GeneratedAt time.Time               `json:"generated_at,omitempty"`
	Cases       []CaseResult            `json:"cases,omitempty"`
	Probes      []CapabilityProbeResult `json:"probes,omitempty"`
	Differences []Difference            `json:"differences"`
}

// CapabilityProbeResult records an independent capability check.
type CapabilityProbeResult struct {
	Probe       string       `json:"probe"`
	Backend     string       `json:"backend"`
	Capability  Capability   `json:"capability"`
	Status      ResultStatus `json:"status"`
	AllowedDiff bool         `json:"allowed_diff"`
	Explanation string       `json:"explanation,omitempty"`
}

// ResultStatus describes the outcome of one case or backend comparison.
type ResultStatus string

const (
	ResultPass         ResultStatus = "pass"
	ResultFail         ResultStatus = "fail"
	ResultUnsupported  ResultStatus = "unsupported"
	ResultInconclusive ResultStatus = "inconclusive"
)

// CaseBackendResult records one candidate backend's result for a case.
type CaseBackendResult struct {
	Backend     string       `json:"backend"`
	Status      ResultStatus `json:"status"`
	Unsupported []Capability `json:"unsupported,omitempty"`
}

// CaseResult aggregates all candidate backend results for one replay case.
type CaseResult struct {
	Case     string              `json:"case"`
	Status   ResultStatus        `json:"status"`
	Backends []CaseBackendResult `json:"backends,omitempty"`
}

// HasUnexpectedDifferences reports whether any mismatch is not allowed.
func (report Report) HasUnexpectedDifferences() bool {
	for _, difference := range report.Differences {
		if !difference.AllowedDiff {
			return true
		}
	}
	for _, result := range report.Cases {
		if result.Status == ResultFail {
			return true
		}
	}
	for _, probe := range report.Probes {
		if probe.Status == ResultFail ||
			(probe.Status == ResultUnsupported && !probe.AllowedDiff) {
			return true
		}
	}
	return false
}

// HasInconclusiveResults reports whether any case had no comparable candidate.
func (report Report) HasInconclusiveResults() bool {
	for _, result := range report.Cases {
		if result.Status == ResultInconclusive {
			return true
		}
	}
	for _, probe := range report.Probes {
		if probe.Status == ResultInconclusive {
			return true
		}
	}
	return false
}
