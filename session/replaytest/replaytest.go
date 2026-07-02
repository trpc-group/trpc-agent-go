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
	// Name is the stable case identifier used in reports and subtests.
	Name string
	// Description summarizes the scenario covered by the case.
	Description string
	// Steps are executed in order against each backend.
	Steps []ReplayStep
	// RequiredCaps declares backend capabilities needed to run the case.
	RequiredCaps RequiredCapabilities
	// AllowedDiffs records path-scoped differences accepted for this case.
	AllowedDiffs []AllowedDiff
}

// RequiredCapabilities declares the backend features a case needs.
type RequiredCapabilities struct {
	// NeedsTrack requires session.TrackService support.
	NeedsTrack bool
	// NeedsWindow requires event window read support.
	NeedsWindow bool
	// NeedsSearch requires session event search support.
	NeedsSearch bool
	// NeedsMemory requires a non-nil memory service and retrieval profile.
	NeedsMemory bool
	// NeedsAsyncSummary requires async summary enqueue support.
	NeedsAsyncSummary bool
}

// ReplayStep is one logical operation in a replay case.
type ReplayStep interface {
	// Type returns a stable machine-readable step kind.
	Type() string
	// LogicalKey returns the stable key used for event identity and errors.
	LogicalKey() string
}

// NamedBackend binds a backend name, capability profile, and concrete services.
type NamedBackend struct {
	// Name is the backend label shown in comparison reports.
	Name string
	// Profile declares the observable backend capabilities.
	Profile BackendProfile
	// SessionService is the session backend under test.
	SessionService session.Service
	// MemoryService is the optional memory backend under test.
	MemoryService memory.Service
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
	// Key is the stable logical event key injected into the event.
	Key string
	// Event is cloned and appended to the inferred replay session.
	Event *event.Event
}

// Type returns the replay step type.
func (s AppendEventStep) Type() string { return "append_event" }

// LogicalKey returns the stable replay key for the step.
func (s AppendEventStep) LogicalKey() string { return s.Key }

// UpdateStateStep updates state in app, user, or session scope.
// DeleteKey removes one app-scoped or user-scoped state key.
// Session-scoped DeleteKey is rejected because session.Service does not expose
// a per-key session state delete method.
type UpdateStateStep struct {
	// Key is the stable logical operation key used in errors.
	Key string
	// Scope selects app, user, or session state.
	Scope StateScope
	// SessionKey selects the session for session-scoped updates.
	SessionKey session.Key
	// UserKey selects the user for user-scoped updates.
	UserKey session.UserKey
	// AppName selects the app for app-scoped updates.
	AppName string
	// State contains keys and values to write.
	State session.StateMap
	// DeleteKey deletes one app-scoped or user-scoped state key when set.
	DeleteKey string
}

// Type returns the replay step type.
func (s UpdateStateStep) Type() string { return "update_state" }

// LogicalKey returns the stable replay key for the step.
func (s UpdateStateStep) LogicalKey() string { return s.Key }

// AddMemoryStep adds a memory entry.
type AddMemoryStep struct {
	// Key is the stable logical operation key used in errors.
	Key string
	// UserKey identifies the memory owner.
	UserKey memory.UserKey
	// Memory is the memory text to persist.
	Memory string
	// Topics are the memory topics persisted with the entry.
	Topics []string
}

// Type returns the replay step type.
func (s AddMemoryStep) Type() string { return "add_memory" }

// LogicalKey returns the stable replay key for the step.
func (s AddMemoryStep) LogicalKey() string { return s.Key }

// SearchMemoryStep searches memories and records the result.
type SearchMemoryStep struct {
	// Key is the stable logical operation key used in errors.
	Key string
	// UserKey identifies the memory owner.
	UserKey memory.UserKey
	// Query is the memory search query.
	Query string
	// Limit caps recorded search results when greater than zero.
	Limit int
}

// Type returns the replay step type.
func (s SearchMemoryStep) Type() string { return "search_memory" }

// LogicalKey returns the stable replay key for the step.
func (s SearchMemoryStep) LogicalKey() string { return s.Key }

// CreateSummaryStep requests a session summary.
type CreateSummaryStep struct {
	// Key is the stable logical operation key used in errors.
	Key string
	// SessionKey selects the session to summarize.
	SessionKey session.Key
	// FilterKey selects the summary scope.
	FilterKey string
	// Force requests summary generation even when the backend would skip it.
	Force bool
	// Async enqueues summary generation instead of running it synchronously.
	Async bool
}

// Type returns the replay step type.
func (s CreateSummaryStep) Type() string { return "create_summary" }

// LogicalKey returns the stable replay key for the step.
func (s CreateSummaryStep) LogicalKey() string { return s.Key }

// WaitSummaryStep waits until a session summary is available.
type WaitSummaryStep struct {
	// Key is the stable logical operation key used in errors.
	Key string
	// SessionKey selects the session whose summary should become visible.
	SessionKey session.Key
	// FilterKey selects the summary scope to poll.
	FilterKey string
	// Timeout is the maximum wait before the step fails.
	Timeout time.Duration
	// PollInterval is the delay between summary visibility checks.
	PollInterval time.Duration
}

// Type returns the replay step type.
func (s WaitSummaryStep) Type() string { return "wait_summary" }

// LogicalKey returns the stable replay key for the step.
func (s WaitSummaryStep) LogicalKey() string { return s.Key }

// AppendTrackStep appends one track event.
type AppendTrackStep struct {
	// Key is the stable logical operation key used in errors.
	Key string
	// SessionKey selects the session receiving the track event.
	SessionKey session.Key
	// Event is appended through session.TrackService when supported.
	Event *session.TrackEvent
}

// Type returns the replay step type.
func (s AppendTrackStep) Type() string { return "append_track" }

// LogicalKey returns the stable replay key for the step.
func (s AppendTrackStep) LogicalKey() string { return s.Key }

// GetSessionStep reads a session snapshot.
type GetSessionStep struct {
	// Key is the stable logical operation key used in errors.
	Key string
	// SessionKey selects the session to capture.
	SessionKey session.Key
}

// Type returns the replay step type.
func (s GetSessionStep) Type() string { return "get_session" }

// LogicalKey returns the stable replay key for the step.
func (s GetSessionStep) LogicalKey() string { return s.Key }

// ListAppStatesStep reads app-scoped states.
type ListAppStatesStep struct {
	// Key is the stable logical operation key used in errors.
	Key string
	// AppName selects the app-scoped state namespace.
	AppName string
}

// Type returns the replay step type.
func (s ListAppStatesStep) Type() string { return "list_app_states" }

// LogicalKey returns the stable replay key for the step.
func (s ListAppStatesStep) LogicalKey() string { return s.Key }

// ListUserStatesStep reads user-scoped states.
type ListUserStatesStep struct {
	// Key is the stable logical operation key used in errors.
	Key string
	// UserKey selects the user-scoped state namespace.
	UserKey session.UserKey
}

// Type returns the replay step type.
func (s ListUserStatesStep) Type() string { return "list_user_states" }

// LogicalKey returns the stable replay key for the step.
func (s ListUserStatesStep) LogicalKey() string { return s.Key }

// SessionSnapshot stores raw or normalized backend replay output.
type SessionSnapshot struct {
	// BackendName identifies the backend that produced this snapshot.
	BackendName string
	// Session is the captured session after replay execution.
	Session *session.Session
	// Memories contains memory entries read after add operations.
	Memories []*memory.Entry
	// MemSearchResults contains memory search results captured by search steps.
	MemSearchResults []*memory.Entry
	// AppStates contains app-scoped state captured by list steps.
	AppStates session.StateMap
	// UserStates contains user-scoped state captured by list steps.
	UserStates session.StateMap
	// TrackEvents contains track events keyed by track name.
	TrackEvents map[string]*session.TrackEvents
	// SummaryMap contains summaries keyed by filter key.
	SummaryMap map[string]*session.Summary
	// Errors records backend-specific non-fatal collection errors.
	Errors []string
}

// DiffResult describes one field-level comparison difference.
type DiffResult struct {
	// CaseName is the replay case that produced the diff.
	CaseName string `json:"case"`
	// BackendA is the reference or left-hand backend name.
	BackendA string `json:"backend_a"`
	// BackendB is the compared or right-hand backend name.
	BackendB string `json:"backend_b"`
	// Path identifies the semantic field that differs.
	Path string `json:"path"`
	// ValueA is the normalized value from BackendA.
	ValueA any `json:"value_a"`
	// ValueB is the normalized value from BackendB.
	ValueB any `json:"value_b"`
	// Severity is error or allowed.
	Severity string `json:"severity"`
	// AllowedDiff records the matching allowed-diff path when severity is allowed.
	AllowedDiff string `json:"allowed_diff,omitempty"`
	// Explanation records the allowed-diff reason or diagnostic detail.
	Explanation string `json:"explanation,omitempty"`
}

// AllowedDiff declares a pre-approved difference rule.
type AllowedDiff struct {
	// Path is the glob-like diff path matched by this rule.
	Path string `json:"path"`
	// Reason explains why the difference is accepted.
	Reason string `json:"reason"`
	// MatchRule selects ignore, same-type, not-empty, or numeric delta behavior.
	MatchRule string `json:"match_rule"`
	// Delta is the maximum numeric difference for within-delta matching.
	Delta float64 `json:"delta,omitempty"`
}

// ComparisonResult stores one backend comparison result.
type ComparisonResult struct {
	// BackendA is the reference or left-hand backend name.
	BackendA string `json:"backend_a"`
	// BackendB is the compared or right-hand backend name.
	BackendB string `json:"backend_b"`
	// Reference records the reference backend used for comparison.
	Reference string `json:"reference,omitempty"`
	// Status is passed, failed, skipped, or mixed.
	Status string `json:"status"`
	// SkipReason explains skipped comparisons.
	SkipReason string `json:"skip_reason,omitempty"`
	// Unsupported lists missing backend features that caused a skip.
	Unsupported []UnsupportedFeature `json:"unsupported,omitempty"`
	// Diffs contains semantic differences after allowed-diff filtering.
	Diffs []DiffResult `json:"diffs,omitempty"`
}

// Report summarizes a replay consistency run.
type Report struct {
	// GeneratedAt is the UTC time when the report was built.
	GeneratedAt time.Time `json:"generated_at"`
	// Reference is the preferred reference backend name.
	Reference string `json:"reference"`
	// Backends lists backend names included in the run.
	Backends []string `json:"backends"`
	// TotalCases is the number of replay cases reported.
	TotalCases int `json:"total_cases"`
	// PassedCases is the number of cases with passed overall status.
	PassedCases int `json:"passed_cases"`
	// FailedCases is the number of cases with failed overall status.
	FailedCases int `json:"failed_cases"`
	// SkippedCases is the number of cases skipped for unsupported features.
	SkippedCases int `json:"skipped_cases"`
	// TotalDiffs is the number of emitted diff records.
	TotalDiffs int `json:"total_diffs"`
	// ErrorDiffs is the number of non-allowed diff records.
	ErrorDiffs int `json:"error_diffs"`
	// AllowedDiffs is the number of accepted diff records.
	AllowedDiffs int `json:"allowed_diffs"`
	// Results contains per-case comparison results.
	Results []CaseResult `json:"results"`
	// Unsupported aggregates unsupported features across skipped comparisons.
	Unsupported []UnsupportedFeature `json:"unsupported,omitempty"`
}

// CaseResult summarizes comparisons for one replay case.
type CaseResult struct {
	// CaseName is the replay case name.
	CaseName string `json:"case"`
	// Comparisons contains backend comparison results for the case.
	Comparisons []ComparisonResult `json:"comparisons"`
	// OverallStatus summarizes all comparisons for the case.
	OverallStatus string `json:"overall_status"`
}

// UnsupportedFeature identifies a missing backend feature.
type UnsupportedFeature struct {
	// Backend is the backend missing the feature.
	Backend string `json:"backend"`
	// Feature is the missing capability name.
	Feature string `json:"feature"`
	// Impact describes how the missing feature affected the run.
	Impact string `json:"impact"`
}
