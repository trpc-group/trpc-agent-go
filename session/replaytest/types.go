// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ReplayCase is one deterministic public replay scenario.
type ReplayCase struct {
	Name         string
	Description  string
	Steps        []Step
	RequiredCaps Caps
	AllowedDiffs []AllowedDiff
}

// Caps declares backend capabilities required by a case.
type Caps struct {
	NeedsTrack        bool
	NeedsMemory       bool
	NeedsAsyncSummary bool
}

// Step is one typed operation in a ReplayCase.
type Step interface {
	Type() string
	Key() string
}

// NamedBackend binds a concrete backend and its capability profile.
type NamedBackend struct {
	Name           string
	Profile        BackendProfile
	SessionService session.Service
	MemoryService  memory.Service
}

// Snapshot is the comparable view of a backend after replaying a case.
type Snapshot struct {
	Backend   string
	SessionID string
	Session   *session.Session
	AppState  session.StateMap
	UserState session.StateMap
	Memories  []*memory.Entry
	Errors    []string
}

// Diff records one semantic difference between two snapshots.
// Field names match issue #2001 acceptance locators.
type Diff struct {
	CaseName         string `json:"case"`
	BackendA         string `json:"backend_a"`
	BackendB         string `json:"backend_b"`
	SessionID        string `json:"session_id,omitempty"`
	EventIndex       *int   `json:"event_index,omitempty"`
	SummaryFilterKey string `json:"summary_filter_key,omitempty"`
	TrackName        string `json:"track_name,omitempty"`
	MemoryID         string `json:"memory_id,omitempty"`
	Path             string `json:"path"`
	Baseline         any    `json:"baseline"`
	Actual           any    `json:"actual"`
	Allowed          bool   `json:"allowed_diff"`
	Explanation      string `json:"explanation"`
}

// CaseResult aggregates comparison outcomes for one public case.
type CaseResult struct {
	CaseName string `json:"case"`
	Status   string `json:"status"`
	Diffs    []Diff `json:"diffs,omitempty"`
	Skipped  string `json:"skipped_reason,omitempty"`
}

// Report is the JSON-serializable multi-backend consistency report.
type Report struct {
	GeneratedAt  time.Time    `json:"generated_at"`
	Mode         string       `json:"mode"`
	Reference    string       `json:"reference"`
	Backends     []string     `json:"backends"`
	TotalCases   int          `json:"total_cases"`
	PassedCases  int          `json:"passed_cases"`
	FailedCases  int          `json:"failed_cases"`
	SkippedCases int          `json:"skipped_cases"`
	Results      []CaseResult `json:"results"`
	Diffs        []Diff       `json:"diffs"`
}

// AllowedDiff describes a path-scoped accepted difference.
type AllowedDiff struct {
	PathPattern string  `json:"path_pattern"`
	Rule        string  `json:"rule"` // ignore | within_delta | not_empty | same_type
	Delta       float64 `json:"delta,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

// BackendProfile describes observable backend capabilities.
type BackendProfile struct {
	Name                 string
	SupportsTrack        bool
	SupportsAppState     bool
	SupportsUserState    bool
	SupportsSessionState bool
	SupportsAsyncSummary bool
	SupportsMemory       bool
}

// Status constants used in CaseResult / Report.
const (
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// AllowedDiff rule names.
const (
	RuleIgnore      = "ignore"
	RuleWithinDelta = "within_delta"
	RuleNotEmpty    = "not_empty"
	RuleSameType    = "same_type"
)

// EventLogicalKeyExtension stores the stable replay event key.
const EventLogicalKeyExtension = "replay_event_key"
