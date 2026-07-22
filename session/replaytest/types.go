//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Capability is one observable operation supported by a backend.
type Capability string

const (
	// CapabilitySession indicates support for session lifecycle and events.
	CapabilitySession Capability = "session"
	// CapabilityAppState indicates support for application-scoped state.
	CapabilityAppState Capability = "app_state"
	// CapabilityUserState indicates support for user-scoped state.
	CapabilityUserState Capability = "user_state"
	// CapabilitySessionState indicates support for session-scoped state.
	CapabilitySessionState Capability = "session_state"
	// CapabilityMemory indicates support for memory persistence.
	CapabilityMemory Capability = "memory"
	// CapabilityMemorySearch indicates support for ranked memory retrieval.
	CapabilityMemorySearch Capability = "memory_search"
	// CapabilitySummary indicates support for session summaries.
	CapabilitySummary Capability = "summary"
	// CapabilityTrack indicates support for track-event persistence.
	CapabilityTrack Capability = "track"
	// CapabilityConcurrent indicates support for concurrent event appends.
	CapabilityConcurrent Capability = "concurrent_write"
)

// replayTrackStateKey mirrors the session-owned track index. Replay inputs
// reject this key; normalization excludes the backend-maintained index after
// normalizing the actual track events separately.
const replayTrackStateKey = "tracks"

func isKnownCapability(capability Capability) bool {
	switch capability {
	case CapabilitySession,
		CapabilityAppState,
		CapabilityUserState,
		CapabilitySessionState,
		CapabilityMemory,
		CapabilityMemorySearch,
		CapabilitySummary,
		CapabilityTrack,
		CapabilityConcurrent:
		return true
	default:
		return false
	}
}

// Capabilities declares backend support. Missing entries are unsupported.
type Capabilities map[Capability]bool

// FullCapabilities returns the capabilities required by the lightweight
// InMemory and SQLite replay matrix.
func FullCapabilities() Capabilities {
	return Capabilities{
		CapabilitySession:      true,
		CapabilityAppState:     true,
		CapabilityUserState:    true,
		CapabilitySessionState: true,
		CapabilityMemory:       true,
		CapabilityMemorySearch: true,
		CapabilitySummary:      true,
		CapabilityTrack:        true,
		CapabilityConcurrent:   true,
	}
}

// Services owns one isolated pair of session and memory services.
type Services struct {
	// Session is the isolated session service under test.
	Session session.Service
	// Memory is the isolated memory service under test. It may be nil for cases
	// that require neither CapabilityMemory nor CapabilityMemorySearch.
	Memory memory.Service
	// Cleanup removes backend resources after both services are closed.
	// It may be nil when the services own all of their resources.
	Cleanup func() error
}

// Close releases all resources owned by the service pair.
func (s *Services) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	if s.Memory != nil {
		errs = append(errs, s.Memory.Close())
	}
	if s.Session != nil {
		errs = append(errs, s.Session.Close())
	}
	if s.Cleanup != nil {
		errs = append(errs, s.Cleanup())
	}
	return errors.Join(errs...)
}

// Backend creates isolated services for one case.
type Backend struct {
	// Name is the stable, non-empty identifier used in reports and diff rules.
	Name string
	// Capabilities declares supported operations; omitted values are unsupported.
	Capabilities Capabilities
	// Open creates services isolated to the supplied case name. The runner calls
	// Services.Close for every non-nil result, including results returned with an error.
	Open func(context.Context, string) (*Services, error)
}

// Case is one deterministic replay scenario.
type Case struct {
	// Name is a stable, non-empty report and session identifier.
	Name string
	// Description states the externally observable contract exercised by the case.
	Description string
	// InitialState is copied before the session is created. Its session-scoped
	// keys follow the same rules as StateInput.Values.
	InitialState session.StateMap
	// Requires selects both the operations and snapshot domains exercised by the
	// case. It must include CapabilitySession and every capability implied by Steps.
	Requires []Capability
	// Steps execute sequentially except for branches inside StepConcurrent.
	Steps []Step
	// AllowedDiffs documents non-blocking backend-pair differences.
	AllowedDiffs []AllowedDiff
	// EventOrder selects global ordering by default or branch-local causal ordering.
	EventOrder EventOrderMode
	// Fault identifies the acceptance mutation expected to make this case fail.
	Fault FaultKind
}

// EventOrderMode controls whether global ordering or branch-local causal
// ordering is part of the replay contract.
type EventOrderMode string

const (
	// EventOrderGlobal requires one stable global event order.
	EventOrderGlobal EventOrderMode = "global"
	// EventOrderCausal compares order within each causal branch.
	EventOrderCausal EventOrderMode = "causal"
)

// StepKind identifies a replay operation.
type StepKind string

const (
	// StepAppendEvent appends one event.
	StepAppendEvent StepKind = "append_event"
	// StepUpdateState applies one scoped state mutation.
	StepUpdateState StepKind = "update_state"
	// StepAddMemory persists one memory entry.
	StepAddMemory StepKind = "add_memory"
	// StepSearchMemory records one ranked memory query result.
	StepSearchMemory StepKind = "search_memory"
	// StepCreateSummary creates or refreshes one summary.
	StepCreateSummary StepKind = "create_summary"
	// StepAppendTrack appends one track event.
	StepAppendTrack StepKind = "append_track"
	// StepReloadSession reloads the active session from the backend.
	StepReloadSession StepKind = "reload_session"
	// StepConcurrent runs multiple ordered event-only branches concurrently.
	StepConcurrent StepKind = "concurrent"
)

// Step is a tagged replay operation. Exactly one payload matching Kind must
// be populated, except ReloadSession which has no payload.
type Step struct {
	// Name identifies the step in errors and must be non-empty.
	Name string
	// Kind selects the payload and operation.
	Kind StepKind
	// Event is populated only for event append steps.
	Event *EventInput
	// State is populated only for state update steps.
	State *StateInput
	// Memory is populated only for memory add steps.
	Memory *MemoryInput
	// MemorySearch is populated only for memory search steps.
	MemorySearch *MemorySearchInput
	// Summary is populated only for summary creation steps.
	Summary *SummaryInput
	// Track is populated only for track append steps.
	Track *TrackInput
	// Concurrent contains two or more ordered event-only branches populated only
	// for concurrent steps. Branches use their stable execution paths for causal
	// comparison, independently of event filter keys.
	Concurrent [][]Step
}

// EventInput appends an event under a stable logical identity. Physical event
// IDs may differ by backend and are normalized back to LogicalID.
type EventInput struct {
	// LogicalID is the stable event identity used after backend IDs are normalized.
	LogicalID string
	// Event is cloned before the runner changes timestamps or extensions. Every
	// extension key must be non-empty and must not use the runner-owned logical
	// identity key; every non-nil extension value must contain valid JSON.
	// StateDelta is applied to session state even when the event itself is not
	// persisted. Keys beginning with app: or user: additionally declare scoped-state
	// intent; unprefixed and temp: keys are session-only. The runner preserves every
	// key in the event snapshot and selects the corresponding state domains, so
	// differences in how backends persist scoped deltas remain observable. The
	// backend-owned tracks key is rejected.
	Event *event.Event
	// Offset is added to the created session time for deterministic ordering.
	Offset time.Duration
}

// StateScope identifies the owner of a state key.
type StateScope string

const (
	// StateScopeApp selects application-scoped state.
	StateScopeApp StateScope = "app"
	// StateScopeUser selects user-scoped state.
	StateScopeUser StateScope = "user"
	// StateScopeSession selects session-scoped state.
	StateScopeSession StateScope = "session"
)

// StateInput updates or deletes state keys in one scope. Session state does
// not expose a delete operation, so DeleteKeys is valid only for app/user.
type StateInput struct {
	// Scope selects the application, user, or session state owner.
	Scope StateScope
	// Values is copied before it is passed to the backend. Keys must be non-empty.
	// App and user keys must not include a state scope prefix; session keys may
	// use temp: but must not use app:, user:, or the backend-owned tracks key.
	Values session.StateMap
	// DeleteKeys applies only to application and user state and follows the same
	// unprefixed, non-empty key rule as Values.
	DeleteKeys []string
}

// MemoryInput adds an idempotent memory entry.
type MemoryInput struct {
	// Memory is the non-empty content persisted by the memory service.
	Memory string
	// Topics is copied before it is passed to the backend.
	Topics []string
	// Metadata is copied before it is passed to the backend and may be nil.
	Metadata *memory.Metadata
}

// MemorySearchInput performs one named, ranked memory query. The enclosing
// Step.Name identifies the result in Snapshot.MemorySearches.
type MemorySearchInput struct {
	// Query is the non-empty query passed to memory.Service.SearchMemories.
	Query string
	// Options is copied before use. Its Query field is ignored; Query above is
	// authoritative so the two inputs cannot disagree.
	Options memory.SearchOptions
}

// SummaryInput creates or refreshes one filter-aware summary.
type SummaryInput struct {
	// FilterKey selects a branch; an empty value selects the full session.
	FilterKey string
	// Force is passed to the session summary service unchanged.
	Force bool
}

// TrackInput appends one observation event.
type TrackInput struct {
	// Event is copied before its payload and timestamp are normalized. Payload
	// may be nil for JSON null; every non-nil payload must contain valid JSON.
	Event *session.TrackEvent
	// Offset is added to the created session time for deterministic ordering.
	Offset time.Duration
}

// CanonicalMap is a JSON-compatible normalized object.
type CanonicalMap map[string]any

// Snapshot is the backend-independent replay-visible state after a case.
type Snapshot struct {
	// Backend identifies the implementation that produced the snapshot.
	Backend string `json:"backend"`
	// Case identifies the replay scenario that produced the snapshot.
	Case string `json:"case"`
	// Session contains normalized session identity and timestamp presence.
	Session CanonicalMap `json:"session"`
	// Events contains normalized event values.
	Events []CanonicalMap `json:"events"`
	// EventOrder records logical IDs by the selected ordering scope.
	EventOrder map[string][]string `json:"event_order"`
	// State contains normalized application, user, and session state. Nil values
	// and non-JSON byte sequences remain structurally distinct.
	State map[string]CanonicalMap `json:"state"`
	// Memories contains normalized memory entries ordered by semantic content.
	Memories []CanonicalMap `json:"memories"`
	// MemorySearches contains ranked memory query results keyed by step name.
	// Result order and similarity score remain observable.
	MemorySearches map[string][]CanonicalMap `json:"memory_searches"`
	// Summaries contains normalized summaries keyed by filter key.
	Summaries map[string]CanonicalMap `json:"summaries"`
	// Tracks contains normalized track events keyed by track name.
	Tracks map[string][]CanonicalMap `json:"tracks"`
}

// AllowedRule controls how one known backend difference is evaluated.
type AllowedRule string

const (
	// AllowedIgnore accepts every value at the matched path.
	AllowedIgnore AllowedRule = "ignore"
	// AllowedWithinDelta accepts numeric values within Delta.
	AllowedWithinDelta AllowedRule = "within_delta"
	// AllowedSameType accepts present values with the same normalized JSON type.
	AllowedSameType AllowedRule = "same_type"
)

// AllowedDiff documents one explicit, path-scoped backend difference.
// BackendA and BackendB form an unordered pair. Path is a slash-separated
// glob, for example /tracks/*/*/payload/duration_ms.
type AllowedDiff struct {
	// BackendA is one side of an unordered backend pair and may be "*".
	BackendA string `json:"backend_a"`
	// BackendB is the other side of an unordered backend pair and may be "*".
	BackendB string `json:"backend_b"`
	// Path is an absolute slash-separated glob over the normalized snapshot.
	Path string `json:"path"`
	// Rule selects how matched values are accepted.
	Rule AllowedRule `json:"rule"`
	// Delta is the non-negative tolerance used only by AllowedWithinDelta.
	Delta float64 `json:"delta,omitempty"`
	// Reason is the required report explanation for the allowance.
	Reason string `json:"reason"`
}

// ComparisonMode selects the oracle used to interpret backend differences.
type ComparisonMode string

const (
	// ComparisonReference compares every backend with one named reference.
	ComparisonReference ComparisonMode = "reference"
	// ComparisonConsensus compares every backend pair and only identifies an
	// outlier when all remaining backends agree with each other.
	ComparisonConsensus ComparisonMode = "consensus"
)

// Diff records one semantic mismatch and its nearest domain locator.
type Diff struct {
	// Case identifies the scenario that produced the difference.
	Case string `json:"case"`
	// BackendA identifies the baseline or canonical first pair member.
	BackendA string `json:"backend_a"`
	// BackendB identifies the compared or canonical second pair member.
	BackendB string `json:"backend_b"`
	// SessionID identifies the affected logical session when available.
	SessionID string `json:"session_id,omitempty"`
	// EventIndex identifies the nearest normalized event when applicable.
	EventIndex *int `json:"event_index,omitempty"`
	// SummaryFilterKey identifies the nearest summary when applicable.
	// A non-nil pointer to an empty string identifies the full-session summary.
	SummaryFilterKey *string `json:"summary_filter_key,omitempty"`
	// TrackName identifies the nearest track when applicable.
	TrackName string `json:"track_name,omitempty"`
	// MemoryID identifies the nearest normalized memory when applicable.
	MemoryID string `json:"memory_id,omitempty"`
	// Path is the absolute normalized snapshot path that differs.
	Path string `json:"path"`
	// Baseline is the normalized value from BackendA.
	Baseline any `json:"baseline"`
	// Actual is the normalized value from BackendB.
	Actual any `json:"actual"`
	// Allowed reports whether an AllowedDiff accepted this difference.
	Allowed bool `json:"allowed_diff"`
	// Explanation describes either the allowance or the blocking mismatch.
	Explanation string `json:"explanation,omitempty"`
}

// ConsensusVerdict describes what an oracle-free comparison can conclude.
type ConsensusVerdict string

const (
	// ConsensusUnanimous indicates that all comparable backends agree.
	ConsensusUnanimous ConsensusVerdict = "unanimous"
	// ConsensusOutlier identifies one backend that disagrees with all others.
	ConsensusOutlier ConsensusVerdict = "outlier"
	// ConsensusAmbiguous indicates disagreement without a unique outlier.
	ConsensusAmbiguous ConsensusVerdict = "ambiguous"
	// ConsensusInsufficient indicates that fewer than two backends were comparable.
	ConsensusInsufficient ConsensusVerdict = "insufficient"
)

// PairComparison summarizes one deterministic backend-pair comparison.
type PairComparison struct {
	// BackendA is the lexicographically first backend name.
	BackendA string `json:"backend_a"`
	// BackendB is the lexicographically second backend name.
	BackendB string `json:"backend_b"`
	// BlockingDiffs counts non-allowed differences for this pair.
	BlockingDiffs int `json:"blocking_diffs"`
	// AllowedDiffs counts explicitly allowed differences for this pair.
	AllowedDiffs int `json:"allowed_diffs"`
}

// ConsensusResult records pairwise agreement without assuming one backend is
// correct. Outliers is populated only for a conclusive single-outlier result.
type ConsensusResult struct {
	// Verdict is derived from ComparableBackends and Pairs.
	Verdict ConsensusVerdict `json:"verdict"`
	// ComparableBackends is sorted and excludes unsupported or failed backends.
	ComparableBackends []string `json:"comparable_backends"`
	// Pairs contains every unique canonical pair of comparable backends.
	Pairs []PairComparison `json:"pairs"`
	// Outliers contains one backend only when Verdict is ConsensusOutlier.
	Outliers []string `json:"outliers,omitempty"`
}

// CaseResult is one case in a report.
type CaseResult struct {
	// Name identifies the replay case.
	Name string `json:"case"`
	// Status is derived from blocking and capability evidence.
	Status string `json:"status"`
	// Duration is the wall-clock execution time in milliseconds.
	Duration int64 `json:"duration_ms"`
	// Diffs contains blocking, allowed, and backend exclusion evidence.
	Diffs []Diff `json:"diffs,omitempty"`
	// Consensus is populated only in ComparisonConsensus mode.
	Consensus *ConsensusResult `json:"consensus,omitempty"`
}

const (
	// StatusPassed indicates that a case has neither blocking differences nor
	// unsupported capability evidence.
	StatusPassed = "passed"
	// StatusFailed indicates that a case has at least one blocking difference.
	StatusFailed = "failed"
	// StatusUnsupported indicates that at least one backend lacks a required capability.
	StatusUnsupported = "unsupported"
)

// Report is the machine-readable replay result.
type Report struct {
	// GeneratedAt is the UTC time returned by Runner.Now or time.Now.
	GeneratedAt time.Time `json:"generated_at"`
	// ComparisonMode records the oracle used for the run.
	ComparisonMode ComparisonMode `json:"comparison_mode"`
	// Reference names the reference backend and is empty in consensus mode.
	Reference string `json:"reference,omitempty"`
	// Backends lists every configured backend in runner order.
	Backends []string `json:"backends"`
	// TotalCases equals len(Cases).
	TotalCases int `json:"total_cases"`
	// PassedCases counts cases without blocking or capability evidence.
	PassedCases int `json:"passed_cases"`
	// FailedCases counts cases with at least one blocking difference.
	FailedCases int `json:"failed_cases"`
	// UnsupportedCases counts cases with non-blocking capability evidence.
	UnsupportedCases int `json:"unsupported_cases"`
	// BlockingDiffs is the sum of non-allowed differences across Cases.
	BlockingDiffs int `json:"blocking_diffs"`
	// AllowedDiffs is the sum of allowed differences across Cases.
	AllowedDiffs int `json:"allowed_diffs"`
	// Cases contains results in input case order.
	Cases []CaseResult `json:"cases"`
}

// FaultKind identifies a deterministic snapshot mutation used to prove that
// each public case detects a regression.
type FaultKind string

const (
	// FaultEventContent changes one event message.
	FaultEventContent FaultKind = "event_content"
	// FaultEventOrder swaps two events.
	FaultEventOrder FaultKind = "event_order"
	// FaultToolArguments changes one persisted tool argument payload.
	FaultToolArguments FaultKind = "tool_arguments"
	// FaultStateValue changes one state value.
	FaultStateValue FaultKind = "state_value"
	// FaultMemoryContent changes one memory entry.
	FaultMemoryContent FaultKind = "memory_content"
	// FaultDuplicateMemory duplicates one memory entry.
	FaultDuplicateMemory FaultKind = "duplicate_memory"
	// FaultMemorySearchOrder swaps two ranked memory search results.
	FaultMemorySearchOrder FaultKind = "memory_search_order"
	// FaultSummaryText changes one summary text.
	FaultSummaryText FaultKind = "summary_text"
	// FaultSummaryMissing removes one summary.
	FaultSummaryMissing FaultKind = "summary_missing"
	// FaultSummaryFilterKey moves one summary to the wrong filter key.
	FaultSummaryFilterKey FaultKind = "summary_filter_key"
	// FaultSummaryStale replaces an updated summary with stale content and boundary.
	FaultSummaryStale FaultKind = "summary_stale"
	// FaultTrackPayload changes one track payload.
	FaultTrackPayload FaultKind = "track_payload"
	// FaultDuplicateEvent duplicates one event.
	FaultDuplicateEvent FaultKind = "duplicate_event"
)
