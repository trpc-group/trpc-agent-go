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
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// IDAliasMap maps original UUIDs to stable deterministic aliases.
// Each category gets its own counter so cross-references are preserved:
// if event-003 references tool-call-001, that alias is consistent everywhere.
type IDAliasMap struct {
	mu sync.Mutex

	eventIDs      map[string]string
	toolCallIDs   map[string]string
	invocationIDs map[string]string
	memoryIDs     map[string]string

	eventCounter      int
	toolCallCounter   int
	invocationCounter int
	memoryCounter     int
}

// NewIDAliasMap creates an empty alias map.
func NewIDAliasMap() *IDAliasMap {
	return &IDAliasMap{
		eventIDs:      make(map[string]string),
		toolCallIDs:   make(map[string]string),
		invocationIDs: make(map[string]string),
		memoryIDs:     make(map[string]string),
	}
}

// Alias returns the stable alias for an original ID, assigning one if new.
// category must be "event", "tool-call", "invocation", or "memory".
func (m *IDAliasMap) Alias(original, category string) string {
	if original == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var aliases map[string]string
	var counter *int

	switch category {
	case "event":
		aliases = m.eventIDs
		counter = &m.eventCounter
	case "tool-call":
		aliases = m.toolCallIDs
		counter = &m.toolCallCounter
	case "invocation":
		aliases = m.invocationIDs
		counter = &m.invocationCounter
	case "memory":
		aliases = m.memoryIDs
		counter = &m.memoryCounter
	default:
		return original
	}

	if alias, ok := aliases[original]; ok {
		return alias
	}
	alias := fmt.Sprintf("%s-%03d", category, *counter)
	*counter++
	aliases[original] = alias
	return alias
}

// Lookup returns the alias for an already-seen ID, or "" if not seen.
func (m *IDAliasMap) Lookup(original, category string) string {
	if original == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	switch category {
	case "event":
		return m.eventIDs[original]
	case "tool-call":
		return m.toolCallIDs[original]
	case "invocation":
		return m.invocationIDs[original]
	case "memory":
		return m.memoryIDs[original]
	default:
		return ""
	}
}

// MissingValue is a sentinel indicating "this path does not exist."
// It is distinct from JSON null (which means "path exists, value is null").
// This is critical for StateDelta where deleting a key != setting it to null.
type MissingValue struct{}

// MarshalJSON produces a distinctive sentinel.
func (MissingValue) MarshalJSON() ([]byte, error) {
	return []byte(`{"__missing":true}`), nil
}

// UnmarshalJSON rejects any input; MissingValue is synthetic-only.
func (m *MissingValue) UnmarshalJSON(data []byte) error {
	return fmt.Errorf("MissingValue is synthetic and should not be unmarshaled")
}

// Snapshot is the normalized, pure-JSON representation of backend state.
// All non-deterministic fields (UUIDs, timestamps) have been replaced with
// stable aliases before this struct is created.
type Snapshot struct {
	Events    []map[string]any           `json:"events,omitempty"`
	State     map[string]any             `json:"state,omitempty"`
	AppState  map[string]any             `json:"app_state,omitempty"`
	UserState map[string]any             `json:"user_state,omitempty"`
	Memories  []MemorySnapshot           `json:"memories,omitempty"`
	Summaries map[string]SummarySnapshot `json:"summaries,omitempty"`
	Tracks    map[string][]TrackSnapshot `json:"tracks,omitempty"`
	// Unsupported lists capabilities not supported by the capturing backend.
	Unsupported []string `json:"unsupported,omitempty"`
}

// Clone returns a deep copy via JSON round-trip for drift injection.
func (s Snapshot) Clone() (Snapshot, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return Snapshot{}, fmt.Errorf("marshal snapshot clone: %w", err)
	}
	var cloned Snapshot
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return Snapshot{}, fmt.Errorf("unmarshal snapshot clone: %w", err)
	}
	restoreMissingInSnapshot(&cloned)
	return cloned, nil
}

// restoreMissingInSnapshot walks generic map/slice fields and restores
// MissingValue sentinels that were lost during JSON round-trip.
// MissingValue marshals as {"__missing":true}, but json.Unmarshal into
// map[string]any produces a regular map, not a MissingValue instance.
func restoreMissingInSnapshot(s *Snapshot) {
	for _, m := range s.Events {
		restoreMissingInMap(m)
	}
	restoreMissingInMap(s.State)
	restoreMissingInMap(s.AppState)
	restoreMissingInMap(s.UserState)
	for range s.Summaries {
		// SummarySnapshot has no generic map fields that need restoration.
	}
	for _, events := range s.Tracks {
		for i := range events {
			events[i].Payload = restoreMissingInAny(events[i].Payload)
		}
	}
}

// restoreMissingInMap walks a map[string]any in place and replaces any
// map[string]any{"__missing": true} with MissingValue{}.
func restoreMissingInMap(m map[string]any) {
	if m == nil {
		return
	}
	for k, v := range m {
		m[k] = restoreMissingInAny(v)
	}
}

// restoreMissingInAny walks a generic value and replaces any
// map[string]any{"__missing": true} with MissingValue{}.
func restoreMissingInAny(v any) any {
	switch tv := v.(type) {
	case map[string]any:
		if len(tv) == 1 && tv["__missing"] == true {
			return MissingValue{}
		}
		restoreMissingInMap(tv)
		return tv
	case []any:
		for i, elem := range tv {
			tv[i] = restoreMissingInAny(elem)
		}
		return tv
	default:
		return v
	}
}

// MemorySnapshot is the normalized representation of a memory.Entry.
type MemorySnapshot struct {
	ID           string   `json:"id"`
	Content      string   `json:"content"`
	Topics       []string `json:"topics,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	Score        *float64 `json:"score,omitempty"`
	Rank         int      `json:"rank"`
	EventTime    string   `json:"event_time,omitempty"`
	Participants []string `json:"participants,omitempty"`
	Location     string   `json:"location,omitempty"`
	AppName      string   `json:"app_name,omitempty"`
	UserID       string   `json:"user_id,omitempty"`
}

// SummarySnapshot is the normalized representation of a session.Summary.
// Timestamps are resolved to event indexes for cross-backend comparison.
type SummarySnapshot struct {
	SessionID           string   `json:"session_id"`
	FilterKey           string   `json:"filter_key"`
	Text                string   `json:"text"`
	Topics              []string `json:"topics,omitempty"`
	Version             int      `json:"version"`
	BoundaryPresent     bool     `json:"boundary_present"`
	BoundaryFilterKey   string   `json:"boundary_filter_key,omitempty"`
	UpdatedAtEventIndex *int     `json:"updated_at_event_index,omitempty"`
	CutoffAtEventIndex  *int     `json:"cutoff_at_event_index,omitempty"`
	LastEventIDPresent  bool     `json:"last_event_id_present"`
	LastEventIndex      *int     `json:"last_event_index,omitempty"`
}

// TrackSnapshot is the normalized representation of a single track event.
type TrackSnapshot struct {
	Track   string `json:"track"`
	Payload any    `json:"payload"`
}

// Capability constants name observable behaviors of a backend.
// Section capabilities control snapshot capture; sub-capabilities
// record partial state semantics.
const (
	CapEvents              = "events"
	CapState               = "state"
	CapMemory              = "memory"
	CapSummary             = "summary"
	CapTrack               = "track"
	CapEventStateDeltaNull = "event_state_delta_null"
	CapNeedsAsyncSummary   = "needs_async_summary"
)

// CapabilityDesc describes whether a backend supports one observable behavior.
type CapabilityDesc struct {
	Supported   bool   `json:"supported"`
	Reason      string `json:"reason,omitempty"`
	AllowedDiff bool   `json:"allowed_diff"`
}

// Capabilities maps capability names to their descriptors.
// An omitted capability defaults to supported for backward compatibility.
type Capabilities map[string]CapabilityDesc

// Has reports whether the given capability is supported.
// An omitted capability defaults to supported.
func (c Capabilities) Has(cap string) bool {
	desc, ok := c[cap]
	return !ok || desc.Supported
}

// AllCapabilities returns Capabilities with everything enabled.
func AllCapabilities() Capabilities {
	return Capabilities{
		CapEvents:              {Supported: true},
		CapState:               {Supported: true},
		CapMemory:              {Supported: true},
		CapSummary:             {Supported: true},
		CapTrack:               {Supported: true},
		CapEventStateDeltaNull: {Supported: true},
	}
}

// UnsupportedList returns the names of unsupported capabilities in sorted order.
func (c Capabilities) UnsupportedList() []string {
	var list []string
	for name, desc := range c {
		if !desc.Supported {
			list = append(list, name)
		}
	}
	sort.Strings(list)
	return list
}

// AllowedDiff declares a known, acceptable difference between two backends.
// Every exception must specify both backends, the exact section+path,
// and a reason. No wildcards are permitted.
type AllowedDiff struct {
	BackendA string `json:"backend_a"`
	BackendB string `json:"backend_b"`
	Section  string `json:"section"`
	Path     string `json:"path"`
	Reason   string `json:"reason"`
}

// Validate checks that the AllowedDiff is well-formed.
func (ad AllowedDiff) Validate() error {
	if ad.BackendA == "" || ad.BackendB == "" {
		return fmt.Errorf("allowed diff requires both backend_a and backend_b")
	}
	switch ad.Section {
	case "events", "state", "memories", "summaries", "tracks", "app_state", "user_state":
	default:
		return fmt.Errorf("allowed diff section %q is not a known section", ad.Section)
	}
	if ad.Path == "" {
		return fmt.Errorf("allowed diff requires a non-empty path")
	}
	if ad.Reason == "" {
		return fmt.Errorf("allowed diff requires a reason")
	}
	return nil
}

// DiffSeverity classifies how serious a mismatch is.
type DiffSeverity string

const (
	// SeverityCritical indicates data loss or corruption (entire section missing).
	SeverityCritical DiffSeverity = "critical"
	// SeverityMajor indicates a value or type mismatch (data present but wrong).
	SeverityMajor DiffSeverity = "major"
	// SeverityMinor indicates an allowed or cosmetic difference.
	SeverityMinor DiffSeverity = "minor"
)

// Diff records a single mismatch between two normalized snapshots.
type Diff struct {
	Case            string       `json:"case"`
	SessionID       string       `json:"session_id"`
	BackendA        string       `json:"backend_a"`
	BackendB        string       `json:"backend_b"`
	Section         string       `json:"section"`
	Path            string       `json:"path"`
	ValueA          any          `json:"value_a"`
	ValueB          any          `json:"value_b"`
	BaselinePresent bool         `json:"baseline_present"`
	ComparedPresent bool         `json:"compared_present"`
	Allowed         bool         `json:"allowed,omitempty"`
	Severity        DiffSeverity `json:"severity,omitempty"`
	Explanation     string       `json:"explanation"`
	EventIndex      *int         `json:"event_index,omitempty"`
	MemoryID        string       `json:"memory_id,omitempty"`
	TrackName       string       `json:"track_name,omitempty"`
	SummaryKey      *string      `json:"summary_filter_key,omitempty"`
}

// CaseStatus enumerates possible outcomes for a case.
type CaseStatus string

const (
	StatusPass         CaseStatus = "pass"
	StatusFail         CaseStatus = "fail"
	StatusSkip         CaseStatus = "skip"
	StatusInconclusive CaseStatus = "inconclusive"
	StatusMixed        CaseStatus = "mixed"
)

// BackendMetric records per-backend execution timing and size metrics.
type BackendMetric struct {
	Name            string        `json:"name"`
	RunDuration     time.Duration `json:"run_duration"`
	CaptureDuration time.Duration `json:"capture_duration"`
	SnapshotSize    int64         `json:"snapshot_size"`
	EventCount      int           `json:"event_count"`
	RetryCount      int           `json:"retry_count,omitempty"`
	RetryTotalDelay time.Duration `json:"retry_total_delay,omitempty"`
}

// CaseResult holds the outcome of running a single case.
type CaseResult struct {
	Name                string                               `json:"name"`
	Status              CaseStatus                           `json:"status"`
	Diffs               []Diff                               `json:"diffs,omitempty"`
	GoldenDiffs         []Diff                               `json:"golden_diffs,omitempty"`
	Duration            string                               `json:"duration"`
	UnsupportedCaps     []string                             `json:"unsupported_caps,omitempty"`
	SkipReason          string                               `json:"skip_reason,omitempty"`
	SectionsCompared    int                                  `json:"sections_compared"`
	SectionsSkipped     int                                  `json:"sections_skipped"`
	SkippedBackends     map[string][]string                  `json:"skipped_backends,omitempty"`
	Capabilities        map[string]map[string]CapabilityDesc `json:"capabilities,omitempty"`
	SnapshotFingerprint string                               `json:"snapshot_fingerprint,omitempty"`
	PanicRecovered      any                                  `json:"panic_recovered,omitempty"`
	PanicStack          string                               `json:"panic_stack,omitempty"`
	BackendMetrics      []BackendMetric                      `json:"backend_metrics,omitempty"`
}

// Backend is a fully initialized backend pair with identity and capabilities.
type Backend struct {
	Name  string
	Sess  session.Service
	Track session.TrackService
	Mem   memory.Service
	Caps  Capabilities
	// SessKey returns the session key for this backend. Override per test.
	SessKey func() session.Key
	// Load overrides capture reads for backends that need custom consistency logic.
	Load func(context.Context, Backend) (*session.Session, []*memory.Entry, error)
	// Probe checks backend health before running cases. Optional.
	Probe func(ctx context.Context) error
	// WarmUp runs a quick validation cycle (create+get+delete). Optional.
	WarmUp func(ctx context.Context, backend Backend) error
	// Retry overrides the harness-level retry policy for this backend. Optional.
	Retry *RetryPolicy
	// IsRetryable overrides the default transient error detection. Optional.
	IsRetryable func(err error) bool
	// RateLimit controls per-backend operation rate limiting. Optional.
	// The function is called before each backend operation; it should block
	// until the operation is allowed or return an error if the context is cancelled.
	RateLimit func(ctx context.Context) error
}

// Cleanup removes all test data from the backend after test completion.
func (b *Backend) Cleanup(ctx context.Context, key session.Key, userKey memory.UserKey) error {
	var errs []error
	if b.Sess != nil {
		if err := b.Sess.DeleteSession(ctx, key); err != nil {
			errs = append(errs, fmt.Errorf("DeleteSession: %w", err))
		}
		if key.AppName != "" {
			appState, err := b.Sess.ListAppStates(ctx, key.AppName)
			if err != nil {
				errs = append(errs, fmt.Errorf("ListAppStates: %w", err))
			} else {
				for stateKey := range appState {
					if err := b.Sess.DeleteAppState(ctx, key.AppName, stateKey); err != nil {
						errs = append(errs, fmt.Errorf("DeleteAppState(%s): %w", stateKey, err))
					}
				}
			}
		}
		if key.AppName != "" && key.UserID != "" {
			scopedUserKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
			userState, err := b.Sess.ListUserStates(ctx, scopedUserKey)
			if err != nil {
				errs = append(errs, fmt.Errorf("ListUserStates: %w", err))
			} else {
				for stateKey := range userState {
					if err := b.Sess.DeleteUserState(ctx, scopedUserKey, stateKey); err != nil {
						errs = append(errs, fmt.Errorf("DeleteUserState(%s): %w", stateKey, err))
					}
				}
			}
		}
	}
	if b.Mem != nil {
		if err := b.Mem.ClearMemories(ctx, userKey); err != nil {
			errs = append(errs, fmt.Errorf("ClearMemories: %w", err))
		}
	}
	return errors.Join(errs...)
}

// VerifyCleanup checks that no test data remains after cleanup.
// Returns an error if a leak is detected (session or memories still exist).
func (b *Backend) VerifyCleanup(ctx context.Context, key session.Key, userKey memory.UserKey) error {
	if b.Sess != nil {
		sess, err := b.Sess.GetSession(ctx, key)
		if err == nil && sess != nil {
			return fmt.Errorf("leak detected: session %v still exists after cleanup", key)
		}
		if key.AppName != "" {
			appState, err := b.Sess.ListAppStates(ctx, key.AppName)
			if err == nil && len(appState) > 0 {
				return fmt.Errorf("leak detected: %d app states still exist after cleanup", len(appState))
			}
		}
		if key.AppName != "" && key.UserID != "" {
			userState, err := b.Sess.ListUserStates(ctx, session.UserKey{
				AppName: key.AppName,
				UserID:  key.UserID,
			})
			if err == nil && len(userState) > 0 {
				return fmt.Errorf("leak detected: %d user states still exist after cleanup", len(userState))
			}
		}
	}
	if b.Mem != nil {
		memories, err := b.Mem.ReadMemories(ctx, userKey, 0)
		if err == nil && len(memories) > 0 {
			return fmt.Errorf("leak detected: %d memories still exist after cleanup", len(memories))
		}
	}
	return nil
}

// BackendFactory creates Backend instances for testing.
type BackendFactory interface {
	Kind() string
	Capabilities() Capabilities
	Create(ctx context.Context, t *testing.T) *Backend
}

// CaptureOptions controls how one backend snapshot is normalized.
type CaptureOptions struct {
	NormalizerConfig       NormalizerConfig
	OrderEventsByTimestamp bool
	UnorderedMemories      bool
	AppState               session.StateMap
	UserState              session.StateMap
}

// NormalizerConfig controls normalization behavior.
type NormalizerConfig struct {
	VolatilePayloadKeys []string
	MemoryUnordered     bool
	ScorePrecision      int
}

// DefaultNormalizerConfig returns sensible defaults.
func DefaultNormalizerConfig() NormalizerConfig {
	return NormalizerConfig{
		VolatilePayloadKeys: []string{
			"duration", "duration_ms", "elapsed", "elapsed_ms",
			"latency", "latency_ms",
		},
		MemoryUnordered: false,
		ScorePrecision:  6,
	}
}

// Case defines a replay test case. It supports both Op-based declarative
// cases and function-based imperative cases.
type Case struct {
	Name                   string
	RequiredCaps           []string
	AllowedDiffs           []AllowedDiff
	OrderEventsByTimestamp bool
	UnorderedMemories      bool
	Ops                    []Op
	ParallelGroups         [][]Op
	CountOnly              bool
	// Run overrides Op-based execution. If non-nil, it takes precedence.
	Run func(ctx context.Context, backend Backend) error
}

// Report is the machine-readable output for one or more replay cases.
type Report struct {
	ReportID    string        `json:"report_id"`
	Version     string        `json:"version"`
	RunID       string        `json:"run_id,omitempty"`
	GeneratedAt *time.Time    `json:"generated_at,omitempty"`
	Backends    []string      `json:"backends"`
	Cases       []CaseResult  `json:"cases"`
	Summary     ReportSummary `json:"summary"`
}

// ReportSummary is the aggregate summary.
type ReportSummary struct {
	TotalCases        int           `json:"total_cases"`
	PassedCases       int           `json:"passed_cases"`
	FailedCases       int           `json:"failed_cases"`
	SkippedCases      int           `json:"skipped_cases"`
	InconclusiveCases int           `json:"inconclusive_cases"`
	TotalDiffs        int           `json:"total_diffs"`
	AllowedDiffs      int           `json:"allowed_diffs"`
	CriticalDiffs     int           `json:"critical_diffs"`
	MajorDiffs        int           `json:"major_diffs"`
	MinorDiffs        int           `json:"minor_diffs"`
	TotalRetries      int           `json:"total_retries,omitempty"`
	SuiteDuration     time.Duration `json:"suite_duration,omitempty"`
}

// RetryPolicy controls retry behavior for transient backend errors.
type RetryPolicy struct {
	MaxAttempts   int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
	Jitter        bool
}

// DefaultRetryPolicy returns a sensible retry policy for backend operations.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:   3,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	}
}

// --- Structured Errors ---

// ReplayError is a structured error from the replay framework.
// It carries a Kind for programmatic routing, optional Backend/Case
// context, and wraps the underlying Cause.
type ReplayError struct {
	Kind    ReplayErrorKind `json:"kind"`
	Backend string          `json:"backend,omitempty"`
	Case    string          `json:"case,omitempty"`
	Cause   error           `json:"-"`
}

type ReplayErrorKind string

const (
	ErrBackendProbe     ReplayErrorKind = "backend_probe"
	ErrBackendWarmUp    ReplayErrorKind = "backend_warmup"
	ErrBackendCapture   ReplayErrorKind = "backend_capture"
	ErrBackendRun       ReplayErrorKind = "backend_run"
	ErrCaseValidation   ReplayErrorKind = "case_validation"
	ErrComparison       ReplayErrorKind = "comparison"
	ErrReportWrite      ReplayErrorKind = "report_write"
	ErrSnapshotTooLarge ReplayErrorKind = "snapshot_too_large"
	ErrCircuitBreaker   ReplayErrorKind = "circuit_breaker"
)

func (e *ReplayError) Error() string {
	msg := string(e.Kind)
	if e.Backend != "" {
		msg += " backend=" + e.Backend
	}
	if e.Case != "" {
		msg += " case=" + e.Case
	}
	if e.Cause != nil {
		msg += ": " + e.Cause.Error()
	}
	return msg
}

func (e *ReplayError) Unwrap() error { return e.Cause }

// --- Circuit Breaker ---

// circuitBreaker tracks consecutive failures per backend within one suite run.
// Once maxFailures consecutive failures occur for a backend, it trips and
// subsequent runs skip that backend entirely until the suite ends.
type circuitBreaker struct {
	mu          sync.Mutex
	failures    map[string]int
	maxFailures int
	tripped     map[string]bool
}

func newCircuitBreaker(maxFailures int) *circuitBreaker {
	return &circuitBreaker{
		failures:    make(map[string]int),
		maxFailures: maxFailures,
		tripped:     make(map[string]bool),
	}
}

func (cb *circuitBreaker) recordFailure(backendName string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures[backendName]++
	if cb.failures[backendName] >= cb.maxFailures {
		cb.tripped[backendName] = true
	}
}

func (cb *circuitBreaker) recordSuccess(backendName string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures[backendName] = 0
}

func (cb *circuitBreaker) isTripped(backendName string) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.tripped[backendName]
}
