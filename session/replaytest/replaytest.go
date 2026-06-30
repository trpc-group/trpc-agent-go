//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replaytest defines replay consistency test models and backend adapters.
package replaytest

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ReplayCase describes one deterministic replay scenario.
type ReplayCase struct {
	Name         string
	Description  string
	Steps        []ReplayStep
	RequiredCaps RequiredCapabilities
	AllowedDiffs []AllowedDiff
}

// RequiredCapabilities declares the backend features a case needs.
type RequiredCapabilities struct {
	NeedsTrack        bool
	NeedsWindow       bool
	NeedsSearch       bool
	NeedsMemory       bool
	NeedsAsyncSummary bool
}

// ReplayStep is one logical operation in a replay case.
type ReplayStep interface {
	Type() string
	LogicalKey() string
}

// StateScope identifies the state scope used by an UpdateStateStep.
type StateScope string

const (
	// ScopeApp selects application-scoped state.
	ScopeApp StateScope = "app"
	// ScopeUser selects user-scoped state.
	ScopeUser StateScope = "user"
	// ScopeSession selects session-scoped state.
	ScopeSession StateScope = "session"
)

// AppendEventStep appends one event to a session.
type AppendEventStep struct {
	Key   string
	Event *event.Event
}

// Type returns the replay step type.
func (s AppendEventStep) Type() string { return "append_event" }

// LogicalKey returns the stable replay key for the step.
func (s AppendEventStep) LogicalKey() string { return s.Key }

// UpdateStateStep updates state in app, user, or session scope.
type UpdateStateStep struct {
	Key        string
	Scope      StateScope
	SessionKey session.Key
	UserKey    session.UserKey
	AppName    string
	State      session.StateMap
	DeleteKey  string
}

// Type returns the replay step type.
func (s UpdateStateStep) Type() string { return "update_state" }

// LogicalKey returns the stable replay key for the step.
func (s UpdateStateStep) LogicalKey() string { return s.Key }

// AddMemoryStep adds a memory entry.
type AddMemoryStep struct {
	Key     string
	UserKey memory.UserKey
	Memory  string
	Topics  []string
}

// Type returns the replay step type.
func (s AddMemoryStep) Type() string { return "add_memory" }

// LogicalKey returns the stable replay key for the step.
func (s AddMemoryStep) LogicalKey() string { return s.Key }

// SearchMemoryStep searches memories and records the result.
type SearchMemoryStep struct {
	Key     string
	UserKey memory.UserKey
	Query   string
	Limit   int
}

// Type returns the replay step type.
func (s SearchMemoryStep) Type() string { return "search_memory" }

// LogicalKey returns the stable replay key for the step.
func (s SearchMemoryStep) LogicalKey() string { return s.Key }

// CreateSummaryStep requests a session summary.
type CreateSummaryStep struct {
	Key        string
	SessionKey session.Key
	FilterKey  string
	Force      bool
	Async      bool
}

// Type returns the replay step type.
func (s CreateSummaryStep) Type() string { return "create_summary" }

// LogicalKey returns the stable replay key for the step.
func (s CreateSummaryStep) LogicalKey() string { return s.Key }

// AppendTrackStep appends one track event.
type AppendTrackStep struct {
	Key        string
	SessionKey session.Key
	Event      *session.TrackEvent
}

// Type returns the replay step type.
func (s AppendTrackStep) Type() string { return "append_track" }

// LogicalKey returns the stable replay key for the step.
func (s AppendTrackStep) LogicalKey() string { return s.Key }

// GetSessionStep reads a session snapshot.
type GetSessionStep struct {
	Key        string
	SessionKey session.Key
}

// Type returns the replay step type.
func (s GetSessionStep) Type() string { return "get_session" }

// LogicalKey returns the stable replay key for the step.
func (s GetSessionStep) LogicalKey() string { return s.Key }

// ListAppStatesStep reads app-scoped states.
type ListAppStatesStep struct {
	Key     string
	AppName string
}

// Type returns the replay step type.
func (s ListAppStatesStep) Type() string { return "list_app_states" }

// LogicalKey returns the stable replay key for the step.
func (s ListAppStatesStep) LogicalKey() string { return s.Key }

// ListUserStatesStep reads user-scoped states.
type ListUserStatesStep struct {
	Key     string
	UserKey session.UserKey
}

// Type returns the replay step type.
func (s ListUserStatesStep) Type() string { return "list_user_states" }

// LogicalKey returns the stable replay key for the step.
func (s ListUserStatesStep) LogicalKey() string { return s.Key }

// SessionSnapshot stores raw or normalized backend replay output.
type SessionSnapshot struct {
	BackendName      string
	Session          *session.Session
	Memories         []*memory.Entry
	MemSearchResults []*memory.Entry
	AppStates        session.StateMap
	UserStates       session.StateMap
	TrackEvents      map[string]*session.TrackEvents
	SummaryMap       map[string]*session.Summary
	Errors           []string
}

// DiffResult describes one field-level comparison difference.
type DiffResult struct {
	CaseName    string `json:"case"`
	BackendA    string `json:"backend_a"`
	BackendB    string `json:"backend_b"`
	Path        string `json:"path"`
	ValueA      any    `json:"value_a"`
	ValueB      any    `json:"value_b"`
	Severity    string `json:"severity"`
	AllowedDiff string `json:"allowed_diff,omitempty"`
	Explanation string `json:"explanation,omitempty"`
}

// AllowedDiff declares a pre-approved difference rule.
type AllowedDiff struct {
	Path      string  `json:"path"`
	Reason    string  `json:"reason"`
	MatchRule string  `json:"match_rule"`
	Delta     float64 `json:"delta,omitempty"`
}

// ComparisonResult stores one backend comparison result.
type ComparisonResult struct {
	BackendA    string               `json:"backend_a"`
	BackendB    string               `json:"backend_b"`
	Reference   string               `json:"reference,omitempty"`
	Status      string               `json:"status"`
	SkipReason  string               `json:"skip_reason,omitempty"`
	Unsupported []UnsupportedFeature `json:"unsupported,omitempty"`
	Diffs       []DiffResult         `json:"diffs,omitempty"`
}

// Report summarizes a replay consistency run.
type Report struct {
	GeneratedAt  time.Time            `json:"generated_at"`
	Reference    string               `json:"reference"`
	Backends     []string             `json:"backends"`
	TotalCases   int                  `json:"total_cases"`
	PassedCases  int                  `json:"passed_cases"`
	FailedCases  int                  `json:"failed_cases"`
	SkippedCases int                  `json:"skipped_cases"`
	TotalDiffs   int                  `json:"total_diffs"`
	ErrorDiffs   int                  `json:"error_diffs"`
	AllowedDiffs int                  `json:"allowed_diffs"`
	Results      []CaseResult         `json:"results"`
	Unsupported  []UnsupportedFeature `json:"unsupported,omitempty"`
}

// CaseResult summarizes comparisons for one replay case.
type CaseResult struct {
	CaseName      string             `json:"case"`
	Comparisons   []ComparisonResult `json:"comparisons"`
	OverallStatus string             `json:"overall_status"`
}

// UnsupportedFeature identifies a missing backend feature.
type UnsupportedFeature struct {
	Backend string `json:"backend"`
	Feature string `json:"feature"`
	Impact  string `json:"impact"`
}
