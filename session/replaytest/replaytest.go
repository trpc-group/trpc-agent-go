//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replaytest provides reusable helpers for replaying equivalent
// session and memory operations across backends and comparing their results.
package replaytest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const stateScopePeerCleanupTimeout = 5 * time.Second

// Backend groups the services needed to execute a replay case.
type Backend struct {
	// Name identifies the backend within one comparison. It must be non-empty
	// and must not contain surrounding whitespace.
	Name           string
	SessionService session.Service
	TrackService   session.TrackService
	MemoryService  memory.Service
	// ReadAllMemories must return every memory for the requested user. Complete
	// must be true only when the backend-specific adapter has proved that the
	// read is exhaustive; Run rejects unconfirmed partial reads.
	ReadAllMemories ReadAllMemoriesFunc
	// CreateSummary optionally performs one complete replay summary operation.
	// Implementations must be safe for concurrent calls when multiple Run
	// invocations share the same backend. When nil, Run calls
	// SessionService.CreateSessionSummary directly.
	CreateSummary CreateSummaryFunc
}

// CreateSummaryFunc prepares and creates one summary for a replay step.
// The callback owns the complete operation, including any fixture-specific
// summary setup and the call that persists the summary.
type CreateSummaryFunc func(
	ctx context.Context,
	sess *session.Session,
	step SummaryStep,
) error

// ReadAllMemoriesFunc performs a backend-specific exhaustive memory read.
type ReadAllMemoriesFunc func(
	ctx context.Context,
	userKey memory.UserKey,
) (entries []*memory.Entry, complete bool, err error)

// MemoryOperation identifies a memory mutation used by a replay case.
type MemoryOperation string

const (
	// MemoryAdd adds a memory.
	MemoryAdd MemoryOperation = "add"
	// MemoryUpdate updates a previously aliased memory.
	MemoryUpdate MemoryOperation = "update"
	// MemoryDelete deletes a previously aliased memory.
	MemoryDelete MemoryOperation = "delete"
)

// MemoryOp describes one memory mutation.
type MemoryOp struct {
	Name      string
	Operation MemoryOperation
	// Ref is a logical alias that advances when an update rotates the memory ID.
	Ref      string
	Content  string
	Topics   []string
	Metadata *memory.Metadata
	// ResultAlias optionally binds an additional alias to the operation result.
	// A live alias cannot be rebound to a different memory, but a deleted alias
	// may be reused.
	ResultAlias string
}

// MemoryQuery describes one memory search assertion.
type MemoryQuery struct {
	Query string
	// ExpectedContents is the exact unordered multiset of result contents.
	ExpectedContents []string
}

// SummaryStep describes one summary creation and read-back assertion.
type SummaryStep struct {
	Name      string
	FilterKey string
	Force     bool
	Text      string
	WantText  string
	// EventPrefix is the number of leading Case.Events that must be appended
	// before this summary runs. Nil means all events.
	EventPrefix *int
}

// TrackSpec describes one track event append.
type TrackSpec struct {
	Name string
	// Payload accepts any JSON-marshalable value, including arrays and scalars.
	Payload   any
	Timestamp time.Time
}

type preparedTrack struct {
	name      string
	payload   json.RawMessage
	timestamp time.Time
}

type preparedCase struct {
	preparedTracks            []preparedTrack
	summaryTargets            []int
	preparedAppExpectedState  session.StateMap
	preparedUserExpectedState session.StateMap
	validateDirectStateScopes bool
}

// Case is a backend-independent replay scenario.
type Case struct {
	Name               string
	InitialState       session.StateMap
	AppState           session.StateMap
	UserState          session.StateMap
	SessionState       session.StateMap
	Events             []*event.Event
	ConcurrentMemories []MemoryOp
	Summaries          []SummaryStep
	Tracks             []TrackSpec
	Memories           []MemoryOp
	Queries            []MemoryQuery
	AllowedDiffs       []AllowedDiffRule
}

// Result contains the normalized result of running one case on one backend.
type Result struct {
	Backend  string
	Key      session.Key
	Snapshot Snapshot
}

// Snapshot is the normalized replay state compared across backends.
type Snapshot struct {
	Session SessionSnapshot         `json:"session"`
	Events  []EventSnapshot         `json:"events"`
	State   map[string]any          `json:"state"`
	Memory  []MemorySnapshot        `json:"memory"`
	Summary map[string]SummaryEntry `json:"summary"`
	Tracks  []TrackSnapshot         `json:"tracks"`
}

// SessionSnapshot contains stable session identity fields.
type SessionSnapshot struct {
	ID     string `json:"id"`
	App    string `json:"app"`
	UserID string `json:"user_id"`
}

// EventSnapshot is a normalized event representation.
type EventSnapshot map[string]any

// MemorySnapshot contains stable memory fields and raw IDs for diagnostics.
type MemorySnapshot struct {
	Key          string   `json:"-"`
	RawID        string   `json:"-"`
	App          string   `json:"app"`
	UserID       string   `json:"user_id"`
	Content      string   `json:"content,omitempty"`
	Topics       []string `json:"topics,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	EventTime    string   `json:"event_time,omitempty"`
	Participants []string `json:"participants,omitempty"`
	Location     string   `json:"location,omitempty"`
}

// SummaryEntry is the normalized representation of one filter-key summary.
type SummaryEntry struct {
	Summary          string           `json:"summary"`
	Topics           []string         `json:"topics,omitempty"`
	UpdatedAtNonZero bool             `json:"updated_at_non_zero"`
	Boundary         *SummaryBoundary `json:"boundary,omitempty"`
}

// SummaryBoundary contains stable summary cutoff metadata.
type SummaryBoundary struct {
	Version        int    `json:"version"`
	FilterKey      string `json:"filter_key"`
	CutoffAt       string `json:"cutoff_at,omitempty"`
	LastEventIndex *int   `json:"last_event_index,omitempty"`
}

// TrackSnapshot contains one normalized track and its ordered events.
type TrackSnapshot struct {
	// Name is the map key returned by the session backend.
	Name string `json:"name"`
	// Track is the identity stored on the outer TrackEvents container.
	Track  string               `json:"track"`
	Events []TrackEventSnapshot `json:"events"`
}

// TrackEventSnapshot contains stable track event fields.
type TrackEventSnapshot struct {
	Track     string               `json:"track,omitempty"`
	Payload   TrackPayloadSnapshot `json:"payload"`
	Timestamp string               `json:"timestamp,omitempty"`
}

// TrackPayloadSnapshot preserves the representation class of track payload bytes.
type TrackPayloadSnapshot struct {
	Kind  string `json:"kind"`
	Value any    `json:"value,omitempty"`
}

// StateBytesSnapshot preserves the exact representation of state bytes.
// Value contains the original text for json and utf8 kinds, base64-encoded
// bytes for the base64 kind, and is omitted for the nil kind.
type StateBytesSnapshot struct {
	Kind  string `json:"kind"`
	Value any    `json:"value,omitempty"`
}

// Diff describes one normalized difference between two replay results.
type Diff struct {
	Case      string `json:"case"`
	SessionID string `json:"session_id"`
	BackendA  string `json:"backend_a"`
	BackendB  string `json:"backend_b"`
	Section   string `json:"section"`
	Path      string `json:"path"`
	Left      any    `json:"left"`
	Right     any    `json:"right"`
	// LeftMissing and RightMissing distinguish an absent map key or list
	// index from a present JSON null. An omitted false value means that side
	// is present, even when Left or Right is nil. Compare and CompareSnapshots
	// set at most one flag, only for missing map keys or list indexes; a nil
	// snapshot section leaves both flags false.
	LeftMissing  bool           `json:"left_missing,omitempty"`
	RightMissing bool           `json:"right_missing,omitempty"`
	Allowed      bool           `json:"allowed"`
	Reason       string         `json:"reason"`
	Context      map[string]any `json:"context"`
}

// AllowedDiffRule explicitly permits one backend-specific normalized diff.
type AllowedDiffRule struct {
	Section  string `json:"section"`
	Path     string `json:"path"`
	BackendA string `json:"backend_a"`
	BackendB string `json:"backend_b"`
	Reason   string `json:"reason"`
}

// Run executes a replay case against one backend and returns a normalized snapshot.
// runNamespace must be non-empty, must not contain surrounding whitespace, and
// must be shared by all backends participating in one comparison. Callers must
// use a new namespace for every rerun. Before creating a session, Run preflights
// statically detectable fixture errors in the fixed order track, event, summary,
// direct state-map scope, sequential memory, then concurrent memory. Direct
// AppState and UserState keys may be bare or carry their matching app:/user:
// prefix; known cross-scope prefixes remain rejected by replay-matrix policy.
// SessionState rejects only app:/user: prefixes and preserves temp: as part of
// the session-local key. An invalid direct state map returns before any backend
// call. The cross-scope rule is replay-harness policy rather than a general
// session.Service contract. Direct-state expected values are normalized for
// independent scope checks, while writes retain the caller's accepted key forms
// so the service API's input behavior is exercised. InitialState and
// Event.StateDelta retain their session-local semantics. Runtime failures may
// still leave partially persisted case data; Run leaves that cleanup lifecycle
// to the caller.
func Run(ctx context.Context, runNamespace string, backend Backend, tc Case) (Result, error) {
	if err := validateBackend(backend); err != nil {
		return Result{}, err
	}
	if err := validateRunNamespace(runNamespace); err != nil {
		return Result{}, err
	}
	prepared, err := prepareCase(backend, tc)
	if err != nil {
		return Result{}, err
	}
	key := replayKey(runNamespace, tc.Name)
	if err := createSessionAndState(ctx, backend, key, tc); err != nil {
		return Result{}, err
	}
	if err := runEventSummaryTimeline(ctx, backend, key, tc, prepared.summaryTargets); err != nil {
		return Result{}, err
	}
	if err := appendTracks(ctx, backend, key, tc.Name, prepared.preparedTracks); err != nil {
		return Result{}, err
	}
	userKey := memory.UserKey{AppName: key.AppName, UserID: key.UserID}
	if err := applyMemoryOperations(ctx, backend, userKey, tc); err != nil {
		return Result{}, err
	}
	if err := assertMemoryQueries(ctx, backend, userKey, tc); err != nil {
		return Result{}, err
	}
	if err := validateStateScopes(
		ctx,
		backend,
		key,
		tc.Name,
		prepared.preparedAppExpectedState,
		prepared.preparedUserExpectedState,
		prepared.validateDirectStateScopes,
	); err != nil {
		return Result{}, err
	}
	return buildResult(ctx, backend, key, userKey, tc.Name)
}

func validateBackend(backend Backend) error {
	name := strings.TrimSpace(backend.Name)
	if name == "" {
		return fmt.Errorf("replay backend name is empty")
	}
	if name != backend.Name {
		return fmt.Errorf("replay backend name %q has surrounding whitespace", backend.Name)
	}
	if backend.SessionService == nil {
		return fmt.Errorf("replay backend %q has nil session service", backend.Name)
	}
	if backend.MemoryService == nil {
		return fmt.Errorf("replay backend %q has nil memory service", backend.Name)
	}
	if backend.ReadAllMemories == nil {
		return fmt.Errorf("replay backend %q has nil ReadAllMemories", backend.Name)
	}
	return nil
}

func validateRunNamespace(runNamespace string) error {
	trimmed := strings.TrimSpace(runNamespace)
	if trimmed == "" {
		return fmt.Errorf("replay run namespace is empty")
	}
	if trimmed != runNamespace {
		return fmt.Errorf("replay run namespace %q has surrounding whitespace", runNamespace)
	}
	return nil
}

func prepareCase(backend Backend, tc Case) (preparedCase, error) {
	tracks, err := prepareTracks(backend, tc)
	if err != nil {
		return preparedCase{}, err
	}
	if err := validateFixtureEvents(tc); err != nil {
		return preparedCase{}, err
	}
	targets, err := prepareSummaryTargets(tc)
	if err != nil {
		return preparedCase{}, err
	}
	if err := validateFixtureStateMaps(tc); err != nil {
		return preparedCase{}, err
	}
	preparedAppState, preparedUserState, err := prepareExpectedDirectStateMaps(tc)
	if err != nil {
		return preparedCase{}, err
	}
	if err := validateSequentialMemoryOperations(tc); err != nil {
		return preparedCase{}, err
	}
	if err := validateConcurrentMemoryOperations(tc); err != nil {
		return preparedCase{}, err
	}
	return preparedCase{
		preparedTracks:            tracks,
		summaryTargets:            targets,
		preparedAppExpectedState:  preparedAppState,
		preparedUserExpectedState: preparedUserState,
		validateDirectStateScopes: len(tc.AppState) > 0 || len(tc.UserState) > 0 || len(tc.SessionState) > 0,
	}, nil
}

func validateFixtureStateMaps(tc Case) error {
	// Keep this order stable: AppState, UserState, then SessionState.
	if err := validateScopedStateMap(tc.Name, appStateScopePolicy(), tc.AppState); err != nil {
		return err
	}
	if err := validateScopedStateMap(tc.Name, userStateScopePolicy(), tc.UserState); err != nil {
		return err
	}
	return validateScopedStateMap(tc.Name, sessionStateScopePolicy(), tc.SessionState)
}

type stateScopePolicy struct {
	fieldName          string
	ownPrefix          string
	disallowedPrefixes []string
	stripOwnPrefix     bool
}

func appStateScopePolicy() stateScopePolicy {
	return stateScopePolicy{
		fieldName:          "app state",
		ownPrefix:          session.StateAppPrefix,
		disallowedPrefixes: []string{session.StateUserPrefix, session.StateTempPrefix},
		stripOwnPrefix:     true,
	}
}

func userStateScopePolicy() stateScopePolicy {
	return stateScopePolicy{
		fieldName:          "user state",
		ownPrefix:          session.StateUserPrefix,
		disallowedPrefixes: []string{session.StateAppPrefix, session.StateTempPrefix},
		stripOwnPrefix:     true,
	}
}

func sessionStateScopePolicy() stateScopePolicy {
	return stateScopePolicy{
		fieldName:          "session state",
		ownPrefix:          session.StateTempPrefix,
		disallowedPrefixes: []string{session.StateAppPrefix, session.StateUserPrefix},
		// temp: is a session-local namespace marker, not a routing prefix.
		stripOwnPrefix: false,
	}
}

// validateScopedStateMap validates only the outermost known scope prefix.
// Bare keys and unknown prefixes are ordinary keys; a known prefix belonging
// to another direct-state scope is rejected to keep the InMemory/SQLite
// replay matrix deterministic.
func validateScopedStateMap(
	caseName string,
	policy stateScopePolicy,
	state session.StateMap,
) error {
	keys := sortedStateKeys(state)
	for _, key := range keys {
		if policy.ownPrefix != "" && strings.HasPrefix(key, policy.ownPrefix) {
			continue
		}
		for _, prefix := range policy.disallowedPrefixes {
			if strings.HasPrefix(key, prefix) {
				return fmt.Errorf(
					"%s for case %q has key %q with disallowed prefix %q",
					policy.fieldName, caseName, key, prefix,
				)
			}
		}
	}
	return nil
}

func sortedStateKeys(state session.StateMap) []string {
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// prepareExpectedDirectStateMaps creates the canonical maps used by the
// independent app/user scope assertions. The backend writes continue to use
// the caller's accepted maps (deep-copied by createSessionAndState).
func prepareExpectedDirectStateMaps(tc Case) (session.StateMap, session.StateMap, error) {
	appState, err := canonicalizeScopedStateWithPolicy(
		tc.AppState, appStateScopePolicy(), stateMapFixtureInput,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("app state for case %q has %w", tc.Name, err)
	}
	userState, err := canonicalizeScopedStateWithPolicy(
		tc.UserState, userStateScopePolicy(), stateMapFixtureInput,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("user state for case %q has %w", tc.Name, err)
	}
	return appState, userState, nil
}

type stateMapRepresentation uint8

const (
	stateMapFixtureInput stateMapRepresentation = iota
	stateMapStoredOutput
)

// canonicalizeScopedState keeps the original helper signature for package
// tests and callers that describe a raw fixture map with one scope prefix.
func canonicalizeScopedState(state session.StateMap, prefix string) (session.StateMap, error) {
	return canonicalizeScopedStateWithPolicy(state, stateScopePolicy{
		ownPrefix:      prefix,
		stripOwnPrefix: true,
	}, stateMapFixtureInput)
}

// canonicalizeScopedStateWithPolicy applies one deterministic normalization
// implementation to both fixture inputs and backend reads. Fixture inputs have
// one outer matching prefix removed; List*States results are already in the
// backend's stored-key representation and therefore must not be stripped a
// second time (important for app:app:flag-style keys). Values are copied and
// source keys that collapse to one canonical key are rejected.
func canonicalizeScopedStateWithPolicy(
	state session.StateMap,
	policy stateScopePolicy,
	representation stateMapRepresentation,
) (session.StateMap, error) {
	keys := sortedStateKeys(state)
	sources := make(map[string][]string, len(state))
	for _, sourceKey := range keys {
		canonicalKey := sourceKey
		if representation == stateMapFixtureInput && policy.stripOwnPrefix {
			canonicalKey = strings.TrimPrefix(sourceKey, policy.ownPrefix)
		}
		sources[canonicalKey] = append(sources[canonicalKey], sourceKey)
	}
	canonicalKeys := make([]string, 0, len(sources))
	for canonicalKey := range sources {
		canonicalKeys = append(canonicalKeys, canonicalKey)
	}
	sort.Strings(canonicalKeys)
	for _, canonicalKey := range canonicalKeys {
		if len(sources[canonicalKey]) > 1 {
			return nil, fmt.Errorf(
				"duplicate canonical key %q from keys %q",
				canonicalKey, sources[canonicalKey],
			)
		}
	}

	out := make(session.StateMap, len(state))
	for _, sourceKey := range keys {
		canonicalKey := sourceKey
		if representation == stateMapFixtureInput && policy.stripOwnPrefix {
			canonicalKey = strings.TrimPrefix(sourceKey, policy.ownPrefix)
		}
		value := state[sourceKey]
		if value == nil {
			out[canonicalKey] = nil
			continue
		}
		out[canonicalKey] = append([]byte(nil), value...)
	}
	return out, nil
}

func validateFixtureEvents(tc Case) error {
	for i, evt := range tc.Events {
		if evt == nil {
			return fmt.Errorf("event %d for case %q is nil", i, tc.Name)
		}
		if _, err := normalizeEvent(i, *evt); err != nil {
			return fmt.Errorf("validate events for case %q: %w", tc.Name, err)
		}
	}
	return nil
}

func prepareSummaryTargets(tc Case) ([]int, error) {
	targets := make([]int, 0, len(tc.Summaries))
	appended := 0
	for i, spec := range tc.Summaries {
		target := len(tc.Events)
		if spec.EventPrefix != nil {
			target = *spec.EventPrefix
		}
		if target < 0 || target > len(tc.Events) {
			return nil, fmt.Errorf(
				"summary step %d for case %q has event prefix %d outside [0,%d]",
				i, tc.Name, target, len(tc.Events),
			)
		}
		if target < appended {
			return nil, fmt.Errorf(
				"summary step %d for case %q has event prefix %d before already appended prefix %d",
				i, tc.Name, target, appended,
			)
		}
		targets = append(targets, target)
		appended = target
	}
	return targets, nil
}

// memoryAliasGroup is the lifecycle unit for replay aliases. All aliases in a
// group refer to one logical memory identity (during preflight) or one backend
// memory ID (during execution). A deleted group remains attached to its former
// aliases so later references can report the more useful deleted-memory error.
type memoryAliasGroup[K comparable] struct {
	key     K
	aliases map[string]struct{}
	live    bool
}

// memoryAliasRegistry tracks alias groups and their live identity/ID
// transitions. Preflight records every canonical identity, including adds
// without an alias; runtime records backend IDs when an alias or update makes
// one available. The registry is intentionally local to a single Run and is
// not shared between concurrent runs.
type memoryAliasRegistry[K comparable] struct {
	aliases    map[string]*memoryAliasGroup[K]
	liveGroups map[K]*memoryAliasGroup[K]
}

func newMemoryAliasRegistry[K comparable]() *memoryAliasRegistry[K] {
	return &memoryAliasRegistry[K]{
		aliases:    make(map[string]*memoryAliasGroup[K]),
		liveGroups: make(map[K]*memoryAliasGroup[K]),
	}
}

func (r *memoryAliasRegistry[K]) resolve(alias string) (*memoryAliasGroup[K], error) {
	group, ok := r.aliases[alias]
	if !ok {
		return nil, fmt.Errorf("missing memory alias %q", alias)
	}
	if !group.live {
		return nil, fmt.Errorf("memory alias %q refers to deleted memory", alias)
	}
	return group, nil
}

func (r *memoryAliasRegistry[K]) validateAliasBinding(
	alias string,
	group *memoryAliasGroup[K],
) error {
	if alias == "" {
		return nil
	}
	previous, ok := r.aliases[alias]
	if !ok || !previous.live || previous == group {
		return nil
	}
	return fmt.Errorf("memory alias %q is already bound to another live memory", alias)
}

func (r *memoryAliasRegistry[K]) commitAliasBinding(
	alias string,
	group *memoryAliasGroup[K],
) {
	if alias == "" || group == nil {
		return
	}
	if previous, ok := r.aliases[alias]; ok {
		if previous == group {
			return
		}
		delete(previous.aliases, alias)
	}
	group.aliases[alias] = struct{}{}
	r.aliases[alias] = group
}

func (r *memoryAliasRegistry[K]) bind(
	alias string,
	key K,
) (*memoryAliasGroup[K], error) {
	group := r.liveGroups[key]
	if err := r.validateAliasBinding(alias, group); err != nil {
		return nil, err
	}
	if group == nil {
		group = &memoryAliasGroup[K]{key: key, aliases: make(map[string]struct{}), live: true}
		r.liveGroups[key] = group
	}
	r.commitAliasBinding(alias, group)
	return group, nil
}

func (r *memoryAliasRegistry[K]) bindToGroup(alias string, group *memoryAliasGroup[K]) error {
	if alias == "" || group == nil {
		return nil
	}
	if err := r.validateAliasBinding(alias, group); err != nil {
		return err
	}
	r.commitAliasBinding(alias, group)
	return nil
}

// transition moves a live group to a new identity/ID. A destination owned by
// another live group is an identity collision; groups are never merged.
func (r *memoryAliasRegistry[K]) transition(
	group *memoryAliasGroup[K],
	newKey K,
) (*memoryAliasGroup[K], error) {
	if group == nil || !group.live {
		return group, nil
	}
	if target := r.liveGroups[newKey]; target != nil && target != group {
		return nil, fmt.Errorf(
			"memory identity collision: current identity %+v already targets live identity %+v",
			group.key, newKey,
		)
	}
	if current, ok := r.liveGroups[group.key]; ok && current == group && group.key != newKey {
		delete(r.liveGroups, group.key)
	}
	group.key = newKey
	r.liveGroups[newKey] = group
	return group, nil
}

func (r *memoryAliasRegistry[K]) invalidate(group *memoryAliasGroup[K]) {
	if group == nil || !group.live {
		return
	}
	if current, ok := r.liveGroups[group.key]; ok && current == group {
		delete(r.liveGroups, group.key)
	}
	group.live = false
}

func validateSequentialMemoryOperations(tc Case) error {
	aliases := newMemoryAliasRegistry[canonicalMemoryIdentity]()
	// AppName/UserID are constant across all operations, so zero values are
	// sufficient for equality during preflight. canonicalMemoryOpIdentity still
	// supplies the complete content/metadata identity rules.
	userKey := memory.UserKey{}
	for i, op := range tc.Memories {
		if err := validateSequentialMemoryOperation(aliases, userKey, op); err != nil {
			return fmt.Errorf("memory operation %d for case %q: %w", i, tc.Name, err)
		}
	}
	return nil
}

func validateSequentialMemoryOperation(
	aliases *memoryAliasRegistry[canonicalMemoryIdentity],
	userKey memory.UserKey,
	op MemoryOp,
) error {
	switch op.Operation {
	case MemoryAdd:
		// Register every live identity, including adds without a result alias.
		// An alias is only an optional handle; it must not determine whether an
		// active identity participates in collision preflight.
		_, err := aliases.bind(op.ResultAlias, canonicalMemoryOpIdentity(userKey, op))
		return err
	case MemoryUpdate:
		group, err := aliases.resolve(op.Ref)
		if err != nil {
			return err
		}
		if err := aliases.validateAliasBinding(op.ResultAlias, group); err != nil {
			return err
		}
		// UpdateMemory treats metadata as a patch: omitted/zero metadata fields
		// are inherited from the current memory. Keep preflight identity tracking
		// aligned with that backend behavior so a later idempotent Add can join
		// the same alias group after an update that omitted metadata.
		group, err = aliases.transition(
			group,
			canonicalMemoryUpdateIdentity(userKey, op, group.key),
		)
		if err != nil {
			return err
		}
		return aliases.bindToGroup(op.ResultAlias, group)
	case MemoryDelete:
		group, err := aliases.resolve(op.Ref)
		if err != nil {
			return err
		}
		aliases.invalidate(group)
	default:
		return fmt.Errorf("unknown memory operation %q (%s)", op.Operation, op.Name)
	}
	return nil
}

func validateConcurrentMemoryOperations(tc Case) error {
	var errs []error
	for i, op := range tc.ConcurrentMemories {
		if op.Operation != MemoryAdd {
			errs = append(errs, fmt.Errorf(
				"concurrent memory operation %d: unsupported concurrent memory operation %q",
				i, op.Operation,
			))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("concurrent memory operations for case %q: %w", tc.Name, errors.Join(errs...))
}

func replayKey(runNamespace, caseName string) session.Key {
	scope := fmt.Sprintf("%d-%s-%s", len(runNamespace), runNamespace, caseName)
	return session.Key{
		AppName:   "replay-matrix-" + scope,
		UserID:    "user-" + scope,
		SessionID: "session-" + scope,
	}
}

func createSessionAndState(ctx context.Context, backend Backend, key session.Key, tc Case) error {
	sess, err := backend.SessionService.CreateSession(ctx, key, cloneStateMap(tc.InitialState))
	if err != nil {
		return fmt.Errorf("create session for case %q: %w", tc.Name, err)
	}
	if sess == nil {
		return fmt.Errorf("create session for case %q returned nil", tc.Name)
	}
	if len(tc.AppState) > 0 {
		if err := backend.SessionService.UpdateAppState(ctx, key.AppName, cloneStateMap(tc.AppState)); err != nil {
			return fmt.Errorf("update app state for case %q: %w", tc.Name, err)
		}
	}
	if len(tc.UserState) > 0 {
		if err := backend.SessionService.UpdateUserState(ctx, session.UserKey{
			AppName: key.AppName,
			UserID:  key.UserID,
		}, cloneStateMap(tc.UserState)); err != nil {
			return fmt.Errorf("update user state for case %q: %w", tc.Name, err)
		}
	}
	if len(tc.SessionState) > 0 {
		if err := backend.SessionService.UpdateSessionState(ctx, key, cloneStateMap(tc.SessionState)); err != nil {
			return fmt.Errorf("update session state for case %q: %w", tc.Name, err)
		}
	}
	return nil
}

func runEventSummaryTimeline(
	ctx context.Context,
	backend Backend,
	key session.Key,
	tc Case,
	summaryTargets []int,
) error {
	appended := 0
	for i, spec := range tc.Summaries {
		target := summaryTargets[i]
		if err := appendEventRange(ctx, backend, key, tc, appended, target); err != nil {
			return err
		}
		appended = target
		if err := createSummary(ctx, backend, key, spec); err != nil {
			return fmt.Errorf("summary step %d for case %q: %w", i, tc.Name, err)
		}
	}
	return appendEventRange(ctx, backend, key, tc, appended, len(tc.Events))
}

func appendEventRange(ctx context.Context, backend Backend, key session.Key, tc Case, start, end int) error {
	for i := start; i < end; i++ {
		evt := tc.Events[i]
		got, err := backend.SessionService.GetSession(ctx, key)
		if err != nil {
			return fmt.Errorf("get session before event %d for case %q: %w", i, tc.Name, err)
		}
		if got == nil {
			return fmt.Errorf("get session before event %d for case %q returned nil", i, tc.Name)
		}
		if evt == nil {
			return fmt.Errorf("event %d for case %q is nil", i, tc.Name)
		}
		if err := backend.SessionService.AppendEvent(ctx, got, evt.Clone()); err != nil {
			return fmt.Errorf("append event %d for case %q: %w", i, tc.Name, err)
		}
	}
	return nil
}

func prepareTracks(backend Backend, tc Case) ([]preparedTrack, error) {
	if len(tc.Tracks) == 0 {
		return nil, nil
	}
	if backend.TrackService == nil {
		return nil, fmt.Errorf("track 0 for case %q requires track service", tc.Name)
	}
	prepared := make([]preparedTrack, 0, len(tc.Tracks))
	for i, spec := range tc.Tracks {
		if strings.TrimSpace(spec.Name) == "" {
			return nil, fmt.Errorf("track %d for case %q has empty name", i, tc.Name)
		}
		payload, err := json.Marshal(spec.Payload)
		if err != nil {
			return nil, fmt.Errorf("marshal track %d for case %q: %w", i, tc.Name, err)
		}
		prepared = append(prepared, preparedTrack{
			name: spec.Name, payload: payload, timestamp: spec.Timestamp,
		})
	}
	return prepared, nil
}

func appendTracks(
	ctx context.Context,
	backend Backend,
	key session.Key,
	caseName string,
	tracks []preparedTrack,
) error {
	for i, track := range tracks {
		got, err := backend.SessionService.GetSession(ctx, key)
		if err != nil {
			return fmt.Errorf("get session before track %d for case %q: %w", i, caseName, err)
		}
		if got == nil {
			return fmt.Errorf("get session before track %d for case %q returned nil", i, caseName)
		}
		if err := backend.TrackService.AppendTrackEvent(ctx, got, &session.TrackEvent{
			Track:     session.Track(track.name),
			Payload:   track.payload,
			Timestamp: track.timestamp,
		}); err != nil {
			return fmt.Errorf("append track %d for case %q: %w", i, caseName, err)
		}
	}
	return nil
}

func applyMemoryOperations(ctx context.Context, backend Backend, userKey memory.UserKey, tc Case) error {
	aliases := newMemoryAliasRegistry[string]()
	for i, op := range tc.Memories {
		if err := applyMemoryOp(
			ctx, backend.MemoryService, backend.ReadAllMemories, userKey, aliases, op,
		); err != nil {
			return fmt.Errorf("memory operation %d for case %q: %w", i, tc.Name, err)
		}
	}
	if err := applyMemoriesConcurrently(ctx, backend.MemoryService, userKey, tc.ConcurrentMemories); err != nil {
		return fmt.Errorf("concurrent memory operations for case %q: %w", tc.Name, err)
	}
	return nil
}

func assertMemoryQueries(ctx context.Context, backend Backend, userKey memory.UserKey, tc Case) error {
	for i, query := range tc.Queries {
		results, err := backend.MemoryService.SearchMemories(ctx, userKey, query.Query)
		if err != nil {
			return fmt.Errorf("memory query %d for case %q: %w", i, tc.Name, err)
		}
		want := append([]string{}, query.ExpectedContents...)
		sort.Strings(want)
		got := make([]string, 0, len(results))
		for resultIndex, result := range results {
			if result == nil {
				return fmt.Errorf("memory query %d for case %q returned nil result at index %d, want contents %q", i, tc.Name, resultIndex, want)
			}
			if result.Memory == nil {
				return fmt.Errorf("memory query %d for case %q returned result with nil memory at index %d, want contents %q", i, tc.Name, resultIndex, want)
			}
			got = append(got, result.Memory.Memory)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("memory query %d for case %q returned contents %q, want %q", i, tc.Name, got, want)
		}
	}
	return nil
}

func validateStateScopes(
	ctx context.Context,
	backend Backend,
	key session.Key,
	caseName string,
	appState session.StateMap,
	userState session.StateMap,
	validateDirectStateScopes bool,
) (err error) {
	if !validateDirectStateScopes {
		return nil
	}

	gotAppState, err := backend.SessionService.ListAppStates(ctx, key.AppName)
	if err != nil {
		return fmt.Errorf("list app state for case %q on backend %q: %w", caseName, backend.Name, err)
	}
	gotAppState, err = canonicalizeScopedStateWithPolicy(
		gotAppState, appStateScopePolicy(), stateMapStoredOutput,
	)
	if err != nil {
		return fmt.Errorf("normalize app state for case %q on backend %q: %w", caseName, backend.Name, err)
	}
	appState, err = canonicalizeScopedStateWithPolicy(
		appState, appStateScopePolicy(), stateMapStoredOutput,
	)
	if err != nil {
		return fmt.Errorf("normalize expected app state for case %q: %w", caseName, err)
	}
	if err := requireStateScope(caseName, backend.Name, "app", gotAppState, appState); err != nil {
		return err
	}

	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
	gotUserState, err := backend.SessionService.ListUserStates(ctx, userKey)
	if err != nil {
		return fmt.Errorf("list user state for case %q on backend %q: %w", caseName, backend.Name, err)
	}
	gotUserState, err = canonicalizeScopedStateWithPolicy(
		gotUserState, userStateScopePolicy(), stateMapStoredOutput,
	)
	if err != nil {
		return fmt.Errorf("normalize user state for case %q on backend %q: %w", caseName, backend.Name, err)
	}
	userState, err = canonicalizeScopedStateWithPolicy(
		userState, userStateScopePolicy(), stateMapStoredOutput,
	)
	if err != nil {
		return fmt.Errorf("normalize expected user state for case %q: %w", caseName, err)
	}
	if err := requireStateScope(caseName, backend.Name, "user", gotUserState, userState); err != nil {
		return err
	}

	peerKey := key
	peerKey.SessionID += "-scope-peer"
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), stateScopePeerCleanupTimeout,
		)
		defer cancel()
		if deleteErr := backend.SessionService.DeleteSession(cleanupCtx, peerKey); deleteErr != nil {
			err = errors.Join(err, fmt.Errorf(
				"delete state-scope peer %q for case %q on backend %q: %w",
				peerKey.SessionID, caseName, backend.Name, deleteErr,
			))
		}
	}()
	peer, err := backend.SessionService.CreateSession(ctx, peerKey, nil)
	if err != nil {
		return fmt.Errorf("create state-scope peer for case %q on backend %q: %w", caseName, backend.Name, err)
	}
	if peer == nil {
		return fmt.Errorf("create state-scope peer for case %q on backend %q returned nil", caseName, backend.Name)
	}

	peer, err = backend.SessionService.GetSession(ctx, peerKey)
	if err != nil {
		return fmt.Errorf("get state-scope peer for case %q on backend %q: %w", caseName, backend.Name, err)
	}
	if peer == nil {
		return fmt.Errorf("get state-scope peer for case %q on backend %q returned nil", caseName, backend.Name)
	}
	peerState := mergeScopedState(appState, userState)
	return requireStateScope(caseName, backend.Name, "peer", peer.SnapshotState(), peerState)
}

func requireStateScope(caseName, backendName, scope string, got, want session.StateMap) error {
	normalizedGot := normalizeState(got)
	normalizedWant := normalizeState(want)
	if reflect.DeepEqual(normalizedGot, normalizedWant) {
		return nil
	}
	return fmt.Errorf(
		"%s state for case %q on backend %q = %#v, want %#v",
		scope, caseName, backendName, normalizedGot, normalizedWant,
	)
}

func mergeScopedState(appState, userState session.StateMap) session.StateMap {
	out := make(session.StateMap, len(appState)+len(userState))
	for key, value := range appState {
		if value == nil {
			out[session.StateAppPrefix+key] = nil
			continue
		}
		out[session.StateAppPrefix+key] = append([]byte(nil), value...)
	}
	for key, value := range userState {
		if value == nil {
			out[session.StateUserPrefix+key] = nil
			continue
		}
		out[session.StateUserPrefix+key] = append([]byte(nil), value...)
	}
	return out
}

func buildResult(ctx context.Context, backend Backend, key session.Key, userKey memory.UserKey, caseName string) (Result, error) {
	got, err := backend.SessionService.GetSession(ctx, key)
	if err != nil {
		return Result{}, fmt.Errorf("get final session for case %q: %w", caseName, err)
	}
	if got == nil {
		return Result{}, fmt.Errorf("get final session for case %q returned nil", caseName)
	}
	memories, err := readMemoriesForReplay(ctx, backend.ReadAllMemories, userKey)
	if err != nil {
		return Result{}, fmt.Errorf("read final memories for case %q: %w", caseName, err)
	}
	snapshot, err := BuildSnapshot(got, memories)
	if err != nil {
		return Result{}, fmt.Errorf(
			"build final snapshot for case %q on backend %q: %w",
			caseName, backend.Name, err,
		)
	}
	return Result{Backend: backend.Name, Key: key, Snapshot: snapshot}, nil
}

func createSummary(ctx context.Context, backend Backend, key session.Key, spec SummaryStep) error {
	got, err := backend.SessionService.GetSession(ctx, key)
	if err != nil {
		return err
	}
	if got == nil {
		return fmt.Errorf("get session returned nil")
	}
	if backend.CreateSummary != nil {
		err = backend.CreateSummary(ctx, got, spec)
	} else {
		err = backend.SessionService.CreateSessionSummary(ctx, got, spec.FilterKey, spec.Force)
	}
	if err != nil {
		return err
	}
	got, err = backend.SessionService.GetSession(ctx, key)
	if err != nil {
		return err
	}
	if got == nil {
		return fmt.Errorf("get session after summary returned nil")
	}
	wantText := spec.WantText
	if wantText == "" {
		wantText = spec.Text
	}
	var opts []session.SummaryOption
	if spec.FilterKey != session.SummaryFilterKeyAllContents {
		opts = append(opts, session.WithSummaryFilterKey(spec.FilterKey))
	}
	text, ok := backend.SessionService.GetSessionSummaryText(ctx, got, opts...)
	if !ok {
		return fmt.Errorf("summary %q with filter key %q not found", spec.Name, spec.FilterKey)
	}
	if text != wantText {
		return fmt.Errorf("summary %q with filter key %q returned %q, want %q", spec.Name, spec.FilterKey, text, wantText)
	}
	return nil
}

func applyMemoryOp(
	ctx context.Context,
	service memory.Service,
	readAllMemories ReadAllMemoriesFunc,
	userKey memory.UserKey,
	aliases *memoryAliasRegistry[string],
	op MemoryOp,
) error {
	switch op.Operation {
	case MemoryAdd:
		var opts []memory.AddOption
		if op.Metadata != nil {
			opts = append(opts, memory.WithMetadata(op.Metadata))
		}
		if err := service.AddMemory(ctx, userKey, op.Content, append([]string(nil), op.Topics...), opts...); err != nil {
			return err
		}
		if op.ResultAlias != "" {
			id, err := findMemoryID(ctx, readAllMemories, userKey, op)
			if err != nil {
				return err
			}
			if _, err := aliases.bind(op.ResultAlias, id); err != nil {
				return err
			}
		}
	case MemoryUpdate:
		group, err := aliases.resolve(op.Ref)
		if err != nil {
			return err
		}
		if err := aliases.validateAliasBinding(op.ResultAlias, group); err != nil {
			return err
		}
		memoryID := group.key
		var opts []memory.UpdateOption
		if op.Metadata != nil {
			opts = append(opts, memory.WithUpdateMetadata(op.Metadata))
		}
		result := &memory.UpdateResult{}
		opts = append(opts, memory.WithUpdateResult(result))
		if err := service.UpdateMemory(ctx, memory.Key{
			AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: memoryID,
		}, op.Content, append([]string(nil), op.Topics...), opts...); err != nil {
			return err
		}
		if result.MemoryID == "" {
			return fmt.Errorf("memory update returned empty ID")
		}
		group, err = aliases.transition(group, result.MemoryID)
		if err != nil {
			return err
		}
		return aliases.bindToGroup(op.ResultAlias, group)
	case MemoryDelete:
		group, err := aliases.resolve(op.Ref)
		if err != nil {
			return err
		}
		memoryID := group.key
		if err := service.DeleteMemory(ctx, memory.Key{
			AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: memoryID,
		}); err != nil {
			return err
		}
		aliases.invalidate(group)
	default:
		return fmt.Errorf("unknown memory operation %q (%s)", op.Operation, op.Name)
	}
	return nil
}

type canonicalMemoryIdentity struct {
	AppName      string
	UserID       string
	Content      string
	Kind         memory.Kind
	EventTime    string
	Participants string
	Location     string
}

// Keep this identity logic aligned with GenerateMemoryID,
// metadataIdentityKind, metadataIdentityParticipants, and
// metadataIdentityLocation in memory/internal/memory/memory.go. replaytest
// cannot import that internal package, so runtime identity changes must update
// this helper and TestCanonicalMemoryIdentityMatchesRuntimeIDs together.
func newCanonicalMemoryIdentity(appName, userID string, mem *memory.Memory) canonicalMemoryIdentity {
	identity := canonicalMemoryIdentity{AppName: appName, UserID: userID}
	if mem == nil {
		return identity
	}
	participants := canonicalIdentityParticipants(mem.Participants)
	location := strings.TrimSpace(mem.Location)
	identity.Content = mem.Memory
	identity.Kind = canonicalIdentityKind(mem.Kind, mem.EventTime != nil, len(mem.Participants) > 0, location != "")
	if mem.EventTime != nil {
		identity.EventTime = mem.EventTime.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	identity.Participants = strings.Join(participants, ",")
	identity.Location = location
	return identity
}

func canonicalMemoryOpIdentity(userKey memory.UserKey, op MemoryOp) canonicalMemoryIdentity {
	mem := &memory.Memory{Memory: op.Content}
	if op.Metadata != nil {
		mem.Kind = op.Metadata.Kind
		mem.EventTime = op.Metadata.EventTime
		mem.Participants = canonicalIdentityParticipants(op.Metadata.Participants)
		mem.Location = strings.TrimSpace(op.Metadata.Location)
	}
	return newCanonicalMemoryIdentity(userKey.AppName, userKey.UserID, mem)
}

// canonicalMemoryUpdateIdentity applies the same metadata-patch semantics as
// memory.UpdateMemory. A nil metadata pointer (and zero-valued fields within a
// non-nil pointer) leaves the corresponding identity component unchanged.
// Topics are intentionally ignored because they do not participate in memory
// identity generation.
func canonicalMemoryUpdateIdentity(
	userKey memory.UserKey,
	op MemoryOp,
	previous canonicalMemoryIdentity,
) canonicalMemoryIdentity {
	identity := previous
	identity.AppName = userKey.AppName
	identity.UserID = userKey.UserID
	identity.Content = op.Content

	if op.Metadata != nil {
		metadata := op.Metadata
		if metadata.Kind != "" {
			identity.Kind = metadata.Kind
		}
		if metadata.EventTime != nil {
			identity.EventTime = metadata.EventTime.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		if participants := canonicalIdentityParticipants(metadata.Participants); len(participants) > 0 {
			identity.Participants = strings.Join(participants, ",")
		}
		if location := strings.TrimSpace(metadata.Location); location != "" {
			identity.Location = location
		}
	}

	// GenerateMemoryID treats KindFact as an implicit kind when no episodic
	// metadata is present, and as an explicit fact kind when any such metadata
	// exists. Re-normalize that distinction after applying the patch.
	hasEventMetadata := identity.EventTime != "" || identity.Participants != "" || identity.Location != ""
	if identity.Kind == memory.KindFact {
		if !hasEventMetadata {
			identity.Kind = ""
		}
	} else if identity.Kind == "" && hasEventMetadata {
		identity.Kind = memory.KindFact
	}
	return identity
}

func canonicalIdentityKind(kind memory.Kind, hasEventTime, hasParticipants, hasLocation bool) memory.Kind {
	if kind != "" && kind != memory.KindFact {
		return kind
	}
	if hasEventTime || hasParticipants || hasLocation {
		return memory.KindFact
	}
	return ""
}

func canonicalIdentityParticipants(values []string) []string {
	participants := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			participants = append(participants, value)
		}
	}
	sort.Slice(participants, func(i, j int) bool {
		left := strings.ToLower(participants[i])
		right := strings.ToLower(participants[j])
		if left != right {
			return left < right
		}
		return participants[i] < participants[j]
	})
	out := participants[:0]
	var previous string
	for _, participant := range participants {
		folded := strings.ToLower(participant)
		if len(out) > 0 && folded == previous {
			continue
		}
		out = append(out, participant)
		previous = folded
	}
	return out
}

func findMemoryID(
	ctx context.Context,
	readAllMemories ReadAllMemoriesFunc,
	userKey memory.UserKey,
	op MemoryOp,
) (string, error) {
	entries, err := readMemoriesForReplay(ctx, readAllMemories, userKey)
	if err != nil {
		return "", err
	}
	want := canonicalMemoryOpIdentity(userKey, op)
	var matches []string
	for _, entry := range entries {
		got := newCanonicalMemoryIdentity(entry.AppName, entry.UserID, entry.Memory)
		if got == want {
			matches = append(matches, entry.ID)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("memory identity %+v not found", want)
	case 1:
		if matches[0] == "" {
			return "", fmt.Errorf("memory identity %+v resolved to empty ID", want)
		}
		return matches[0], nil
	default:
		return "", fmt.Errorf("memory identity %+v is ambiguous across IDs %q", want, matches)
	}
}

func readMemoriesForReplay(
	ctx context.Context,
	readAllMemories ReadAllMemoriesFunc,
	userKey memory.UserKey,
) ([]*memory.Entry, error) {
	entries, complete, err := readAllMemories(ctx, userKey)
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, fmt.Errorf("memory read returned %d entries without completeness confirmation", len(entries))
	}
	if err := validateMemoryEntries(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func validateMemoryEntries(entries []*memory.Entry) error {
	for i, entry := range entries {
		if entry == nil {
			return fmt.Errorf("memory entry %d is nil", i)
		}
		if entry.Memory == nil {
			return fmt.Errorf("memory entry %d has nil Memory", i)
		}
	}
	return nil
}

func applyMemoriesConcurrently(ctx context.Context, service memory.Service, userKey memory.UserKey, ops []MemoryOp) error {
	if len(ops) == 0 {
		return nil
	}
	type operationError struct {
		index int
		err   error
	}
	var wg sync.WaitGroup
	errCh := make(chan operationError, len(ops))
	start := make(chan struct{})
	for i, op := range ops {
		i := i
		op := op
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if op.Operation != MemoryAdd {
				errCh <- operationError{index: i, err: fmt.Errorf("unsupported concurrent memory operation %q", op.Operation)}
				return
			}
			var opts []memory.AddOption
			if op.Metadata != nil {
				opts = append(opts, memory.WithMetadata(op.Metadata))
			}
			if err := service.AddMemory(ctx, userKey, op.Content, append([]string(nil), op.Topics...), opts...); err != nil {
				errCh <- operationError{index: i, err: err}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	var failures []operationError
	for failure := range errCh {
		failures = append(failures, failure)
	}
	if len(failures) == 0 {
		return nil
	}
	sort.Slice(failures, func(i, j int) bool { return failures[i].index < failures[j].index })
	errs := make([]error, 0, len(failures))
	for _, failure := range failures {
		errs = append(errs, fmt.Errorf("concurrent memory operation %d: %w", failure.index, failure.err))
	}
	return errors.Join(errs...)
}

// BuildSnapshot normalizes a session and its memories for stable comparison.
// A nil session produces an empty session snapshot, but supplied memories are
// still validated and normalized. BuildSnapshot returns an error for malformed
// events, memory entries, summary entries, or track containers.
func BuildSnapshot(sess *session.Session, memories []*memory.Entry) (Snapshot, error) {
	if sess == nil {
		normalizedMemories, err := normalizeMemories(memories)
		if err != nil {
			return Snapshot{}, err
		}
		return Snapshot{
			State: map[string]any{}, Memory: normalizedMemories,
			Summary: map[string]SummaryEntry{}, Tracks: []TrackSnapshot{},
		}, nil
	}
	events := sess.GetEvents()
	normalizedEvents, err := normalizeEvents(events)
	if err != nil {
		return Snapshot{}, err
	}
	normalizedMemories, err := normalizeMemories(memories)
	if err != nil {
		return Snapshot{}, err
	}
	normalizedSummaries, err := normalizeSummaries(cloneSummaries(sess), events)
	if err != nil {
		return Snapshot{}, err
	}
	normalizedTracks, err := normalizeTracks(cloneTracks(sess))
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Session: SessionSnapshot{ID: sess.ID, App: sess.AppName, UserID: sess.UserID},
		Events:  normalizedEvents,
		State:   normalizeState(sess.SnapshotState()),
		Memory:  normalizedMemories,
		Summary: normalizedSummaries,
		Tracks:  normalizedTracks,
	}, nil
}

func cloneSummaries(sess *session.Session) map[string]*session.Summary {
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	out := make(map[string]*session.Summary, len(sess.Summaries))
	for key, summary := range sess.Summaries {
		out[key] = summary.Clone()
	}
	return out
}

func cloneTracks(sess *session.Session) map[session.Track]*session.TrackEvents {
	sess.TracksMu.RLock()
	defer sess.TracksMu.RUnlock()
	out := make(map[session.Track]*session.TrackEvents, len(sess.Tracks))
	for track, events := range sess.Tracks {
		if events == nil {
			out[track] = nil
			continue
		}
		copied := &session.TrackEvents{Track: events.Track}
		copied.Events = append([]session.TrackEvent(nil), events.Events...)
		out[track] = copied
	}
	return out
}

func normalizeEvents(events []event.Event) ([]EventSnapshot, error) {
	out := make([]EventSnapshot, 0, len(events))
	for i, evt := range events {
		normalized, err := normalizeEvent(i, evt)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeEvent(index int, evt event.Event) (EventSnapshot, error) {
	encoded, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("normalize event %d: marshal: %w", index, err)
	}
	var normalized map[string]any
	if err := decodeJSON(encoded, &normalized); err != nil {
		return nil, fmt.Errorf("normalize event %d: decode: %w", index, err)
	}
	delete(normalized, "id")
	normalized["timestamp"] = normalizeTime(evt.Timestamp)
	if evt.Response != nil {
		response, ok := normalized["response"].(map[string]any)
		if !ok {
			response = make(map[string]any)
			normalized["response"] = response
		}
		if evt.Response.ID == "" {
			delete(response, "id")
		} else {
			response["id"] = evt.Response.ID
		}
		response["timestamp"] = normalizeTime(evt.Response.Timestamp)
	}
	if evt.StateDelta != nil {
		normalized["stateDelta"] = normalizeState(session.StateMap(evt.StateDelta))
	}
	return EventSnapshot(normalized), nil
}

func normalizeState(state session.StateMap) map[string]any {
	out := make(map[string]any, len(state))
	for key, value := range state {
		out[key] = normalizeBytes(value)
	}
	return out
}

func normalizeBytes(value []byte) any {
	if value == nil {
		return StateBytesSnapshot{Kind: "nil"}
	}
	if !utf8.Valid(value) {
		return StateBytesSnapshot{Kind: "base64", Value: base64.StdEncoding.EncodeToString(value)}
	}
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) > 0 {
		var decoded any
		if err := decodeJSON(trimmed, &decoded); err == nil {
			return StateBytesSnapshot{Kind: "json", Value: string(value)}
		}
	}
	return StateBytesSnapshot{Kind: "utf8", Value: string(value)}
}

func normalizeTrackPayload(value json.RawMessage) TrackPayloadSnapshot {
	if value == nil {
		return TrackPayloadSnapshot{Kind: "nil"}
	}
	if len(value) == 0 {
		return TrackPayloadSnapshot{Kind: "empty"}
	}
	var decoded any
	if err := decodeJSON(value, &decoded); err == nil {
		return TrackPayloadSnapshot{Kind: "json", Value: canonicalJSON(decoded)}
	}
	if utf8.Valid(value) {
		return TrackPayloadSnapshot{Kind: "utf8", Value: string(value)}
	}
	return TrackPayloadSnapshot{Kind: "base64", Value: base64.StdEncoding.EncodeToString(value)}
}

func decodeJSON(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return fmt.Errorf("decode trailing JSON value: %w", err)
	}
	return nil
}

func canonicalJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = canonicalJSON(value)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, value := range typed {
			out[i] = canonicalJSON(value)
		}
		return out
	case json.Number:
		return json.Number(typed.String())
	default:
		return value
	}
}

func normalizeMemories(entries []*memory.Entry) ([]MemorySnapshot, error) {
	if err := validateMemoryEntries(entries); err != nil {
		return nil, err
	}
	out := make([]MemorySnapshot, 0, len(entries))
	for i, entry := range entries {
		snapshot := MemorySnapshot{RawID: entry.ID, App: entry.AppName, UserID: entry.UserID}
		snapshot.Content = entry.Memory.Memory
		snapshot.Topics = sortedStrings(entry.Memory.Topics)
		snapshot.Kind = string(entry.Memory.Kind)
		snapshot.EventTime = normalizeTimePtr(entry.Memory.EventTime)
		snapshot.Participants = sortedStrings(entry.Memory.Participants)
		snapshot.Location = entry.Memory.Location
		key, err := memoryKey(snapshot)
		if err != nil {
			return nil, fmt.Errorf("normalize memory entry %d key: %w", i, err)
		}
		snapshot.Key = key
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func memoryKey(snapshot MemorySnapshot) (string, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal replay memory key: %w", err)
	}
	return string(encoded), nil
}

func normalizeSummaries(
	summaries map[string]*session.Summary,
	events []event.Event,
) (map[string]SummaryEntry, error) {
	filterKeys := make([]string, 0, len(summaries))
	for filterKey := range summaries {
		filterKeys = append(filterKeys, filterKey)
	}
	sort.Strings(filterKeys)

	out := make(map[string]SummaryEntry, len(summaries))
	for _, filterKey := range filterKeys {
		summary := summaries[filterKey]
		if summary == nil {
			return nil, fmt.Errorf("summary entry %q is nil", filterKey)
		}
		entry := SummaryEntry{Summary: summary.Summary, Topics: sortedStrings(summary.Topics), UpdatedAtNonZero: !summary.UpdatedAt.IsZero()}
		if boundary := summary.CutoffBoundary(); boundary != nil {
			entry.Boundary = &SummaryBoundary{
				Version: boundary.Version, FilterKey: boundary.FilterKey,
				CutoffAt: normalizeTime(boundary.CutoffAt), LastEventIndex: summaryLastEventIndex(events, boundary.LastEventID),
			}
		}
		out[filterKey] = entry
	}
	return out, nil
}

func summaryLastEventIndex(events []event.Event, lastEventID string) *int {
	if lastEventID == "" {
		return nil
	}
	for i, evt := range events {
		if evt.ID == lastEventID {
			index := i
			return &index
		}
	}
	unmatched := -1
	return &unmatched
}

func normalizeTracks(tracks map[session.Track]*session.TrackEvents) ([]TrackSnapshot, error) {
	names := make([]string, 0, len(tracks))
	for track := range tracks {
		names = append(names, string(track))
	}
	sort.Strings(names)
	out := make([]TrackSnapshot, 0, len(names))
	for _, name := range names {
		events := tracks[session.Track(name)]
		if events == nil {
			return nil, fmt.Errorf("track entry %q is nil", name)
		}
		snapshot := TrackSnapshot{Name: name, Track: string(events.Track)}
		for _, evt := range events.Events {
			snapshot.Events = append(snapshot.Events, TrackEventSnapshot{
				Track: string(evt.Track), Payload: normalizeTrackPayload(evt.Payload), Timestamp: normalizeTime(evt.Timestamp),
			})
		}
		out = append(out, snapshot)
	}
	return out, nil
}

func sortedStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func normalizeTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return normalizeTime(*value)
}

func normalizeTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

// Compare returns all pairwise normalized differences for a replay case.
// It returns an error when a caller-constructed snapshot contains a value that
// cannot be converted to the canonical JSON comparison representation. Compare
// stops at the first failing pair and returns a nil diff slice on error.
func Compare(tc Case, results []Result) ([]Diff, error) {
	var diffs []Diff
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			pairDiffs, err := CompareSnapshots(
				tc.Name, results[i].Key.SessionID, results[i].Backend, results[j].Backend,
				results[i].Snapshot, results[j].Snapshot, tc.AllowedDiffs,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"compare case %q between backends %q and %q: %w",
					tc.Name, results[i].Backend, results[j].Backend, err,
				)
			}
			diffs = append(diffs, pairDiffs...)
		}
	}
	return diffs, nil
}

type valueDiff struct {
	Path         string
	Left         any
	Right        any
	LeftMissing  bool
	RightMissing bool
}

// CompareSnapshots compares two normalized replay snapshots. It converts the
// session, events, state, memory, summary, and tracks sections in that order,
// checking the left snapshot before the right snapshot in each section. It
// returns a nil diff slice on the first canonical JSON conversion error.
func CompareSnapshots(caseName, sessionID, backendA, backendB string, left, right Snapshot, allowedRules []AllowedDiffRule) ([]Diff, error) {
	sections := []struct {
		name, path  string
		left, right any
	}{
		{name: "session", path: "$.session", left: left.Session, right: right.Session},
		{name: "events", path: "$.events", left: left.Events, right: right.Events},
		{name: "state", path: "$.state", left: left.State, right: right.State},
		{name: "memory", path: "$.memory", left: left.Memory, right: right.Memory},
		{name: "summary", path: "$.summary", left: left.Summary, right: right.Summary},
		{name: "tracks", path: "$.tracks", left: left.Tracks, right: right.Tracks},
	}
	var entries []Diff
	for _, section := range sections {
		leftValue, err := jsonValue(section.left)
		if err != nil {
			return nil, fmt.Errorf(
				"normalize left %s section for backend %q: %w",
				section.name, backendA, err,
			)
		}
		rightValue, err := jsonValue(section.right)
		if err != nil {
			return nil, fmt.Errorf(
				"normalize right %s section for backend %q: %w",
				section.name, backendB, err,
			)
		}
		for _, d := range recursiveDiff(section.path, leftValue, rightValue) {
			entries = append(entries, Diff{
				Case: caseName, SessionID: sessionID, BackendA: backendA, BackendB: backendB,
				Section: section.name, Path: d.Path, Left: d.Left, Right: d.Right,
				LeftMissing: d.LeftMissing, RightMissing: d.RightMissing,
				Context: diffContext(section.name, d.Path, left, right),
			})
		}
	}
	applyAllowedDiffRules(entries, allowedRules)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Section != entries[j].Section {
			return entries[i].Section < entries[j].Section
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

func jsonValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal replay diff value: %w", err)
	}
	var out any
	if err := decodeJSON(encoded, &out); err != nil {
		return nil, fmt.Errorf("decode replay diff value: %w", err)
	}
	return canonicalJSON(out), nil
}

func recursiveDiff(path string, left, right any) []valueDiff {
	if reflect.DeepEqual(left, right) {
		return nil
	}
	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if leftIsMap && rightIsMap {
		return recursiveMapDiff(path, leftMap, rightMap)
	}
	leftList, leftIsList := left.([]any)
	rightList, rightIsList := right.([]any)
	if leftIsList && rightIsList {
		return recursiveListDiff(path, leftList, rightList)
	}
	return []valueDiff{{Path: path, Left: left, Right: right}}
}

func recursiveMapDiff(path string, left, right map[string]any) []valueDiff {
	keys := make([]string, 0, len(left)+len(right))
	seen := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		keys = append(keys, key)
		seen[key] = struct{}{}
	}
	for key := range right {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var diffs []valueDiff
	for _, key := range keys {
		childPath := appendPath(path, key)
		leftValue, leftOK := left[key]
		rightValue, rightOK := right[key]
		switch {
		case !leftOK:
			diffs = append(diffs, valueDiff{
				Path: childPath, Left: nil, Right: rightValue, LeftMissing: true,
			})
		case !rightOK:
			diffs = append(diffs, valueDiff{
				Path: childPath, Left: leftValue, Right: nil, RightMissing: true,
			})
		default:
			diffs = append(diffs, recursiveDiff(childPath, leftValue, rightValue)...)
		}
	}
	return diffs
}

func recursiveListDiff(path string, left, right []any) []valueDiff {
	maxLen := len(left)
	if len(right) > maxLen {
		maxLen = len(right)
	}
	var diffs []valueDiff
	for i := 0; i < maxLen; i++ {
		childPath := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case i >= len(left):
			diffs = append(diffs, valueDiff{
				Path: childPath, Left: nil, Right: right[i], LeftMissing: true,
			})
		case i >= len(right):
			diffs = append(diffs, valueDiff{
				Path: childPath, Left: left[i], Right: nil, RightMissing: true,
			})
		default:
			diffs = append(diffs, recursiveDiff(childPath, left[i], right[i])...)
		}
	}
	return diffs
}

func appendPath(path, key string) string {
	if isPathIdent(key) {
		return path + "." + key
	}
	quoted, err := json.Marshal(key)
	if err != nil {
		panic(fmt.Sprintf("quote replay path key: %v", err))
	}
	return path + "[" + string(quoted) + "]"
}

func isPathIdent(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
				continue
			}
			return false
		}
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func diffContext(section, path string, left, right Snapshot) map[string]any {
	context := map[string]any{}
	switch section {
	case "events":
		if index, ok := pathIndex(path, "$.events"); ok {
			context["event_index"] = index
		}
	case "memory":
		if index, ok := pathIndex(path, "$.memory"); ok {
			if index < len(left.Memory) {
				context["memory_key"] = left.Memory[index].Key
				context["left_memory_key"] = left.Memory[index].Key
				context["left_memory_id"] = left.Memory[index].RawID
			}
			if index < len(right.Memory) {
				if _, ok := context["memory_key"]; !ok {
					context["memory_key"] = right.Memory[index].Key
				}
				context["right_memory_key"] = right.Memory[index].Key
				context["right_memory_id"] = right.Memory[index].RawID
			}
		}
	case "summary":
		if filterKey, ok := summaryFilterKey(path); ok {
			context["summary_filter_key"] = filterKey
		}
	case "tracks":
		if index, ok := pathIndex(path, "$.tracks"); ok {
			if index < len(left.Tracks) {
				context["track_name"] = left.Tracks[index].Name
			} else if index < len(right.Tracks) {
				context["track_name"] = right.Tracks[index].Name
			}
		}
		if index, ok := nestedPathIndex(path, ".events"); ok {
			context["track_event_index"] = index
		}
	}
	if len(context) == 0 {
		return nil
	}
	return context
}

func pathIndex(path, prefix string) (int, bool) {
	if !strings.HasPrefix(path, prefix+"[") {
		return 0, false
	}
	start := len(prefix) + 1
	end := strings.Index(path[start:], "]")
	if end < 0 {
		return 0, false
	}
	index, err := strconv.Atoi(path[start : start+end])
	return index, err == nil
}

func nestedPathIndex(path, marker string) (int, bool) {
	position := strings.Index(path, marker+"[")
	if position < 0 {
		return 0, false
	}
	start := position + len(marker) + 1
	end := strings.Index(path[start:], "]")
	if end < 0 {
		return 0, false
	}
	index, err := strconv.Atoi(path[start : start+end])
	return index, err == nil
}

func summaryFilterKey(path string) (string, bool) {
	const bracketPrefix = "$.summary["
	if strings.HasPrefix(path, bracketPrefix) {
		segment, rest, ok := allowedPathBracketSegment(
			strings.TrimPrefix(path, "$.summary"),
		)
		if !ok || rest != "" && rest[0] != '.' && rest[0] != '[' {
			return "", false
		}
		var value string
		if err := json.Unmarshal([]byte(segment), &value); err != nil {
			return "", false
		}
		return value, true
	}
	const dotPrefix = "$.summary."
	if !strings.HasPrefix(path, dotPrefix) {
		return "", false
	}
	key := strings.TrimPrefix(path, dotPrefix)
	if dot := strings.Index(key, "."); dot >= 0 {
		key = key[:dot]
	}
	if bracket := strings.Index(key, "["); bracket >= 0 {
		key = key[:bracket]
	}
	return key, true
}

func applyAllowedDiffRules(entries []Diff, rules []AllowedDiffRule) {
	for i := range entries {
		for _, rule := range rules {
			if rule.matches(entries[i]) {
				entries[i].Allowed = true
				entries[i].Reason = strings.TrimSpace(rule.Reason)
				break
			}
		}
	}
}

func (rule AllowedDiffRule) matches(entry Diff) bool {
	section := strings.TrimSpace(rule.Section)
	path := strings.TrimSpace(rule.Path)
	backendA := strings.TrimSpace(rule.BackendA)
	backendB := strings.TrimSpace(rule.BackendB)
	reason := strings.TrimSpace(rule.Reason)
	if section == "" || section == "*" || path == "" || !allowedPathHasConcreteSegment(section, path) ||
		backendA == "" || backendA == "*" || backendB == "" || backendB == "*" || reason == "" {
		return false
	}
	return section == entry.Section && MatchAllowedDiffPath(path, entry.Path) && backendRuleMatches(backendA, backendB, entry.BackendA, entry.BackendB)
}

func allowedPathHasConcreteSegment(section, path string) bool {
	section = strings.TrimSpace(section)
	path = strings.TrimSpace(path)
	root := "$." + section
	if section == "" || !strings.HasPrefix(path, root) {
		return false
	}
	remainder := strings.TrimPrefix(path, root)
	if remainder == "" {
		return false
	}

	hasConcrete := false
	for remainder != "" {
		switch remainder[0] {
		case '.':
			segment, rest, ok := allowedPathDotSegment(remainder)
			if !ok {
				return false
			}
			candidate := strings.ReplaceAll(segment, "*", "x")
			if !isPathIdent(candidate) {
				return false
			}
			hasConcrete = hasConcrete || strings.Trim(segment, "*") != ""
			remainder = rest
		case '[':
			segment, rest, ok := allowedPathBracketSegment(remainder)
			if !ok {
				return false
			}
			concrete, valid := allowedBracketSegmentIsConcrete(segment)
			if !valid {
				return false
			}
			hasConcrete = hasConcrete || concrete
			remainder = rest
		default:
			return false
		}
	}
	return hasConcrete
}

func allowedPathDotSegment(path string) (string, string, bool) {
	if len(path) < 2 || path[0] != '.' {
		return "", path, false
	}
	end := strings.IndexAny(path[1:], ".[")
	if end < 0 {
		return path[1:], "", true
	}
	end++
	if end == 1 {
		return "", path, false
	}
	return path[1:end], path[end:], true
}

func allowedPathBracketSegment(path string) (string, string, bool) {
	if len(path) < 3 || path[0] != '[' {
		return "", path, false
	}
	inString := false
	escaped := false
	for i := 1; i < len(path); i++ {
		switch {
		case escaped:
			escaped = false
		case inString && path[i] == '\\':
			escaped = true
		case path[i] == '"':
			inString = !inString
		case !inString && path[i] == ']':
			return path[1:i], path[i+1:], true
		}
	}
	return "", path, false
}

func allowedBracketSegmentIsConcrete(segment string) (bool, bool) {
	if segment == "" {
		return false, false
	}
	if strings.Trim(segment, "*") == "" {
		return false, true
	}
	if _, err := strconv.ParseUint(segment, 10, 64); err == nil {
		return true, true
	}
	var key string
	if segment[0] == '"' && json.Unmarshal([]byte(segment), &key) == nil {
		return strings.Trim(key, "*") != "", true
	}
	return false, false
}

func backendRuleMatches(ruleA, ruleB, entryA, entryB string) bool {
	return ruleA == entryA && ruleB == entryB || ruleA == entryB && ruleB == entryA
}

// MatchAllowedDiffPath reports whether a normalized diff path matches an
// AllowedDiffRule path pattern.
func MatchAllowedDiffPath(pattern, value string) bool {
	if pattern == value || pattern == "*" {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 || parts[0] != "" && !strings.HasPrefix(value, parts[0]) {
		return false
	}
	position := len(parts[0])
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		index := strings.Index(value[position:], part)
		if index < 0 {
			return false
		}
		position += index + len(part)
	}
	last := parts[len(parts)-1]
	return last == "" || strings.HasSuffix(value, last)
}

// HasUnallowedDiffs reports whether any diff is not explicitly allowed.
func HasUnallowedDiffs(entries []Diff) bool {
	for _, entry := range entries {
		if !entry.Allowed {
			return true
		}
	}
	return false
}

// WriteReport encodes replay diffs as indented JSON followed by a newline.
func WriteReport(w io.Writer, entries []Diff) error {
	if w == nil {
		return fmt.Errorf("replay report writer is nil")
	}
	if entries == nil {
		entries = []Diff{}
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(entries); err != nil {
		return fmt.Errorf("encode replay diff report: %w", err)
	}
	return nil
}

func cloneStateMap(state session.StateMap) session.StateMap {
	if state == nil {
		return nil
	}
	out := make(session.StateMap, len(state))
	for key, value := range state {
		if value == nil {
			out[key] = nil
		} else {
			out[key] = append([]byte(nil), value...)
		}
	}
	return out
}
