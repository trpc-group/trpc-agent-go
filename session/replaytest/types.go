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
	CapabilitySession      Capability = "session"
	CapabilityAppState     Capability = "app_state"
	CapabilityUserState    Capability = "user_state"
	CapabilitySessionState Capability = "session_state"
	CapabilityMemory       Capability = "memory"
	CapabilitySummary      Capability = "summary"
	CapabilityTrack        Capability = "track"
	CapabilityConcurrent   Capability = "concurrent_write"
)

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
		CapabilitySummary:      true,
		CapabilityTrack:        true,
		CapabilityConcurrent:   true,
	}
}

// Services owns one isolated pair of session and memory services.
type Services struct {
	Session session.Service
	Memory  memory.Service
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
	Name         string
	Capabilities Capabilities
	Open         func(context.Context, string) (*Services, error)
}

// Case is one deterministic replay scenario.
type Case struct {
	Name         string
	Description  string
	InitialState session.StateMap
	Requires     []Capability
	Steps        []Step
	AllowedDiffs []AllowedDiff
	EventOrder   EventOrderMode
	Fault        FaultKind
}

// EventOrderMode controls whether global ordering or branch-local causal
// ordering is part of the replay contract.
type EventOrderMode string

const (
	EventOrderGlobal EventOrderMode = "global"
	EventOrderCausal EventOrderMode = "causal"
)

// StepKind identifies a replay operation.
type StepKind string

const (
	StepAppendEvent   StepKind = "append_event"
	StepRetryEvent    StepKind = "retry_event"
	StepUpdateState   StepKind = "update_state"
	StepAddMemory     StepKind = "add_memory"
	StepCreateSummary StepKind = "create_summary"
	StepAppendTrack   StepKind = "append_track"
	StepReloadSession StepKind = "reload_session"
	StepConcurrent    StepKind = "concurrent"
)

// Step is a tagged replay operation. Exactly one payload matching Kind must
// be populated, except ReloadSession which has no payload.
type Step struct {
	Name       string
	Kind       StepKind
	Event      *EventInput
	State      *StateInput
	Memory     *MemoryInput
	Summary    *SummaryInput
	Track      *TrackInput
	Concurrent [][]Step
}

// EventInput appends an event under a stable logical identity. Physical event
// IDs may differ by backend and are normalized back to LogicalID.
type EventInput struct {
	LogicalID string
	Event     *event.Event
	Offset    time.Duration
}

// StateScope identifies the owner of a state key.
type StateScope string

const (
	StateScopeApp     StateScope = "app"
	StateScopeUser    StateScope = "user"
	StateScopeSession StateScope = "session"
)

// StateInput updates or deletes state keys in one scope. Session state does
// not expose a delete operation, so DeleteKeys is valid only for app/user.
type StateInput struct {
	Scope      StateScope
	Values     session.StateMap
	DeleteKeys []string
}

// MemoryInput adds an idempotent memory entry.
type MemoryInput struct {
	Memory   string
	Topics   []string
	Metadata *memory.Metadata
}

// SummaryInput creates or refreshes one filter-aware summary.
type SummaryInput struct {
	FilterKey string
	Force     bool
}

// TrackInput appends one observation event.
type TrackInput struct {
	Event  *session.TrackEvent
	Offset time.Duration
}

// CanonicalMap is a JSON-compatible normalized object.
type CanonicalMap map[string]any

// Snapshot is the backend-independent replay-visible state after a case.
type Snapshot struct {
	Backend    string                       `json:"backend"`
	Case       string                       `json:"case"`
	Session    CanonicalMap                 `json:"session"`
	Events     []CanonicalMap               `json:"events"`
	EventOrder map[string][]string          `json:"event_order"`
	State      map[string]map[string]string `json:"state"`
	Memories   []CanonicalMap               `json:"memories"`
	Summaries  map[string]CanonicalMap      `json:"summaries"`
	Tracks     map[string][]CanonicalMap    `json:"tracks"`
}

// AllowedRule controls how one known backend difference is evaluated.
type AllowedRule string

const (
	AllowedIgnore      AllowedRule = "ignore"
	AllowedWithinDelta AllowedRule = "within_delta"
	AllowedSameType    AllowedRule = "same_type"
)

// AllowedDiff documents one explicit, path-scoped backend difference.
// BackendA and BackendB form an unordered pair. Path is a slash-separated
// glob, for example /tracks/*/*/payload/duration_ms.
type AllowedDiff struct {
	BackendA string      `json:"backend_a"`
	BackendB string      `json:"backend_b"`
	Path     string      `json:"path"`
	Rule     AllowedRule `json:"rule"`
	Delta    float64     `json:"delta,omitempty"`
	Reason   string      `json:"reason"`
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
	Case             string `json:"case"`
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
	Explanation      string `json:"explanation,omitempty"`
}

// ConsensusVerdict describes what an oracle-free comparison can conclude.
type ConsensusVerdict string

const (
	ConsensusUnanimous    ConsensusVerdict = "unanimous"
	ConsensusOutlier      ConsensusVerdict = "outlier"
	ConsensusAmbiguous    ConsensusVerdict = "ambiguous"
	ConsensusInsufficient ConsensusVerdict = "insufficient"
)

// PairComparison summarizes one deterministic backend-pair comparison.
type PairComparison struct {
	BackendA      string `json:"backend_a"`
	BackendB      string `json:"backend_b"`
	BlockingDiffs int    `json:"blocking_diffs"`
	AllowedDiffs  int    `json:"allowed_diffs"`
}

// ConsensusResult records pairwise agreement without assuming one backend is
// correct. Outliers is populated only for a conclusive single-outlier result.
type ConsensusResult struct {
	Verdict            ConsensusVerdict `json:"verdict"`
	ComparableBackends []string         `json:"comparable_backends"`
	Pairs              []PairComparison `json:"pairs"`
	Outliers           []string         `json:"outliers,omitempty"`
}

// CaseResult is one case in a report.
type CaseResult struct {
	Name      string           `json:"case"`
	Status    string           `json:"status"`
	Duration  int64            `json:"duration_ms"`
	Diffs     []Diff           `json:"diffs,omitempty"`
	Consensus *ConsensusResult `json:"consensus,omitempty"`
}

const (
	StatusPassed      = "passed"
	StatusFailed      = "failed"
	StatusUnsupported = "unsupported"
)

// Report is the machine-readable replay result.
type Report struct {
	GeneratedAt      time.Time      `json:"generated_at"`
	ComparisonMode   ComparisonMode `json:"comparison_mode"`
	Reference        string         `json:"reference,omitempty"`
	Backends         []string       `json:"backends"`
	TotalCases       int            `json:"total_cases"`
	PassedCases      int            `json:"passed_cases"`
	FailedCases      int            `json:"failed_cases"`
	UnsupportedCases int            `json:"unsupported_cases"`
	BlockingDiffs    int            `json:"blocking_diffs"`
	AllowedDiffs     int            `json:"allowed_diffs"`
	Cases            []CaseResult   `json:"cases"`
}

// FaultKind identifies a deterministic snapshot mutation used to prove that
// each public case detects a regression.
type FaultKind string

const (
	FaultEventContent     FaultKind = "event_content"
	FaultEventOrder       FaultKind = "event_order"
	FaultToolArguments    FaultKind = "tool_arguments"
	FaultStateValue       FaultKind = "state_value"
	FaultMemoryContent    FaultKind = "memory_content"
	FaultSummaryText      FaultKind = "summary_text"
	FaultSummaryMissing   FaultKind = "summary_missing"
	FaultSummaryFilterKey FaultKind = "summary_filter_key"
	FaultTrackPayload     FaultKind = "track_payload"
	FaultDuplicateEvent   FaultKind = "duplicate_event"
	FaultSummaryOwner     FaultKind = "summary_owner"
)
