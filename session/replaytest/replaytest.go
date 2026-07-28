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
	SetSummaryText func(string)
}

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

// StateBytesSnapshot preserves the representation class of state bytes.
type StateBytesSnapshot struct {
	Kind  string `json:"kind"`
	Value any    `json:"value,omitempty"`
}

// Diff describes one normalized difference between two replay results.
type Diff struct {
	Case      string         `json:"case"`
	SessionID string         `json:"session_id"`
	BackendA  string         `json:"backend_a"`
	BackendB  string         `json:"backend_b"`
	Section   string         `json:"section"`
	Path      string         `json:"path"`
	Left      any            `json:"left"`
	Right     any            `json:"right"`
	Allowed   bool           `json:"allowed"`
	Reason    string         `json:"reason"`
	Context   map[string]any `json:"context"`
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
func Run(ctx context.Context, backend Backend, tc Case) (Result, error) {
	if err := validateBackend(backend); err != nil {
		return Result{}, err
	}
	key := replayKey(tc.Name)
	if err := createSessionAndState(ctx, backend, key, tc); err != nil {
		return Result{}, err
	}
	if err := runEventSummaryTimeline(ctx, backend, key, tc); err != nil {
		return Result{}, err
	}
	if err := appendTracks(ctx, backend, key, tc); err != nil {
		return Result{}, err
	}
	userKey := memory.UserKey{AppName: key.AppName, UserID: key.UserID}
	if err := applyMemoryOperations(ctx, backend, userKey, tc); err != nil {
		return Result{}, err
	}
	if err := assertMemoryQueries(ctx, backend, userKey, tc); err != nil {
		return Result{}, err
	}
	if err := validateStateScopes(ctx, backend, key, tc); err != nil {
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
	return nil
}

func replayKey(caseName string) session.Key {
	return session.Key{
		AppName:   "replay-matrix-" + caseName,
		UserID:    "user-" + caseName,
		SessionID: "session-" + caseName,
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

func runEventSummaryTimeline(ctx context.Context, backend Backend, key session.Key, tc Case) error {
	appended := 0
	for i, spec := range tc.Summaries {
		target, err := summaryEventPrefix(tc, i, appended)
		if err != nil {
			return err
		}
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

func summaryEventPrefix(tc Case, stepIndex, appended int) (int, error) {
	spec := tc.Summaries[stepIndex]
	target := len(tc.Events)
	if spec.EventPrefix != nil {
		target = *spec.EventPrefix
	}
	if target < 0 || target > len(tc.Events) {
		return 0, fmt.Errorf(
			"summary step %d for case %q has event prefix %d outside [0,%d]",
			stepIndex, tc.Name, target, len(tc.Events),
		)
	}
	if target < appended {
		return 0, fmt.Errorf(
			"summary step %d for case %q has event prefix %d before already appended prefix %d",
			stepIndex, tc.Name, target, appended,
		)
	}
	return target, nil
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

func appendTracks(ctx context.Context, backend Backend, key session.Key, tc Case) error {
	for i, spec := range tc.Tracks {
		if backend.TrackService == nil {
			return fmt.Errorf("track %d for case %q requires track service", i, tc.Name)
		}
		if strings.TrimSpace(spec.Name) == "" {
			return fmt.Errorf("track %d for case %q has empty name", i, tc.Name)
		}
		got, err := backend.SessionService.GetSession(ctx, key)
		if err != nil {
			return fmt.Errorf("get session before track %d for case %q: %w", i, tc.Name, err)
		}
		payload, err := json.Marshal(spec.Payload)
		if err != nil {
			return fmt.Errorf("marshal track %d for case %q: %w", i, tc.Name, err)
		}
		if err := backend.TrackService.AppendTrackEvent(ctx, got, &session.TrackEvent{
			Track:     session.Track(spec.Name),
			Payload:   payload,
			Timestamp: spec.Timestamp,
		}); err != nil {
			return fmt.Errorf("append track %d for case %q: %w", i, tc.Name, err)
		}
	}
	return nil
}

func applyMemoryOperations(ctx context.Context, backend Backend, userKey memory.UserKey, tc Case) error {
	aliases := make(map[string]string)
	for i, op := range tc.Memories {
		if err := applyMemoryOp(ctx, backend.MemoryService, userKey, aliases, op); err != nil {
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

func validateStateScopes(ctx context.Context, backend Backend, key session.Key, tc Case) (err error) {
	if len(tc.AppState) == 0 && len(tc.UserState) == 0 && len(tc.SessionState) == 0 {
		return nil
	}

	appState := stateWithoutPrefix(tc.AppState, session.StateAppPrefix)
	gotAppState, err := backend.SessionService.ListAppStates(ctx, key.AppName)
	if err != nil {
		return fmt.Errorf("list app state for case %q on backend %q: %w", tc.Name, backend.Name, err)
	}
	if err := requireStateScope(tc.Name, backend.Name, "app", gotAppState, appState); err != nil {
		return err
	}

	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
	userState := stateWithoutPrefix(tc.UserState, session.StateUserPrefix)
	gotUserState, err := backend.SessionService.ListUserStates(ctx, userKey)
	if err != nil {
		return fmt.Errorf("list user state for case %q on backend %q: %w", tc.Name, backend.Name, err)
	}
	if err := requireStateScope(tc.Name, backend.Name, "user", gotUserState, userState); err != nil {
		return err
	}

	peerKey := key
	peerKey.SessionID += "-scope-peer"
	peer, err := backend.SessionService.CreateSession(ctx, peerKey, nil)
	if err != nil {
		return fmt.Errorf("create state-scope peer for case %q on backend %q: %w", tc.Name, backend.Name, err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), stateScopePeerCleanupTimeout,
		)
		defer cancel()
		if deleteErr := backend.SessionService.DeleteSession(cleanupCtx, peerKey); deleteErr != nil {
			err = errors.Join(err, fmt.Errorf(
				"delete state-scope peer %q for case %q on backend %q: %w",
				peerKey.SessionID, tc.Name, backend.Name, deleteErr,
			))
		}
	}()
	if peer == nil {
		return fmt.Errorf("create state-scope peer for case %q on backend %q returned nil", tc.Name, backend.Name)
	}

	peer, err = backend.SessionService.GetSession(ctx, peerKey)
	if err != nil {
		return fmt.Errorf("get state-scope peer for case %q on backend %q: %w", tc.Name, backend.Name, err)
	}
	if peer == nil {
		return fmt.Errorf("get state-scope peer for case %q on backend %q returned nil", tc.Name, backend.Name)
	}
	peerState := mergeScopedState(appState, userState)
	return requireStateScope(tc.Name, backend.Name, "peer", peer.SnapshotState(), peerState)
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

func stateWithoutPrefix(state session.StateMap, prefix string) session.StateMap {
	out := make(session.StateMap, len(state))
	for key, value := range state {
		key = strings.TrimPrefix(key, prefix)
		if value == nil {
			out[key] = nil
			continue
		}
		out[key] = append([]byte(nil), value...)
	}
	return out
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
	memories, err := backend.MemoryService.ReadMemories(ctx, userKey, 0)
	if err != nil {
		return Result{}, fmt.Errorf("read final memories for case %q: %w", caseName, err)
	}
	return Result{Backend: backend.Name, Key: key, Snapshot: BuildSnapshot(got, memories)}, nil
}

func createSummary(ctx context.Context, backend Backend, key session.Key, spec SummaryStep) error {
	got, err := backend.SessionService.GetSession(ctx, key)
	if err != nil {
		return err
	}
	if got == nil {
		return fmt.Errorf("get session returned nil")
	}
	if backend.SetSummaryText != nil {
		backend.SetSummaryText(spec.Text)
	}
	if err := backend.SessionService.CreateSessionSummary(ctx, got, spec.FilterKey, spec.Force); err != nil {
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

func applyMemoryOp(ctx context.Context, service memory.Service, userKey memory.UserKey, aliases map[string]string, op MemoryOp) error {
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
			id, err := findMemoryID(ctx, service, userKey, op)
			if err != nil {
				return err
			}
			aliases[op.ResultAlias] = id
		}
	case MemoryUpdate:
		memoryID := aliases[op.Ref]
		if memoryID == "" {
			return fmt.Errorf("missing memory alias %q", op.Ref)
		}
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
		aliases[op.Ref] = result.MemoryID
		if op.ResultAlias != "" {
			aliases[op.ResultAlias] = result.MemoryID
		}
	case MemoryDelete:
		memoryID := aliases[op.Ref]
		if memoryID == "" {
			return fmt.Errorf("missing memory alias %q", op.Ref)
		}
		if err := service.DeleteMemory(ctx, memory.Key{
			AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: memoryID,
		}); err != nil {
			return err
		}
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

func findMemoryID(ctx context.Context, service memory.Service, userKey memory.UserKey, op MemoryOp) (string, error) {
	entries, err := service.ReadMemories(ctx, userKey, 0)
	if err != nil {
		return "", err
	}
	want := canonicalMemoryOpIdentity(userKey, op)
	var matches []string
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
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

func applyMemoriesConcurrently(ctx context.Context, service memory.Service, userKey memory.UserKey, ops []MemoryOp) error {
	if len(ops) == 0 {
		return nil
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(ops))
	start := make(chan struct{})
	for _, op := range ops {
		op := op
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if op.Operation != MemoryAdd {
				errCh <- fmt.Errorf("unsupported concurrent memory operation %q", op.Operation)
				return
			}
			var opts []memory.AddOption
			if op.Metadata != nil {
				opts = append(opts, memory.WithMetadata(op.Metadata))
			}
			if err := service.AddMemory(ctx, userKey, op.Content, append([]string(nil), op.Topics...), opts...); err != nil {
				errCh <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// BuildSnapshot normalizes a session and its memories for stable comparison.
func BuildSnapshot(sess *session.Session, memories []*memory.Entry) Snapshot {
	if sess == nil {
		return Snapshot{State: map[string]any{}, Memory: []MemorySnapshot{}, Summary: map[string]SummaryEntry{}, Tracks: []TrackSnapshot{}}
	}
	events := sess.GetEvents()
	return Snapshot{
		Session: SessionSnapshot{ID: sess.ID, App: sess.AppName, UserID: sess.UserID},
		Events:  normalizeEvents(events),
		State:   normalizeState(sess.SnapshotState()),
		Memory:  normalizeMemories(memories),
		Summary: normalizeSummaries(cloneSummaries(sess), events),
		Tracks:  normalizeTracks(cloneTracks(sess)),
	}
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
		copied := &session.TrackEvents{Track: track}
		if events != nil {
			copied.Track = events.Track
			copied.Events = append([]session.TrackEvent(nil), events.Events...)
		}
		out[track] = copied
	}
	return out
}

func normalizeEvents(events []event.Event) []EventSnapshot {
	out := make([]EventSnapshot, 0, len(events))
	for _, evt := range events {
		encoded, err := json.Marshal(evt)
		if err != nil {
			panic(fmt.Sprintf("marshal replay event: %v", err))
		}
		var normalized map[string]any
		if err := decodeJSON(encoded, &normalized); err != nil {
			panic(fmt.Sprintf("unmarshal replay event: %v", err))
		}
		delete(normalized, "id")
		normalized["timestamp"] = normalizeTime(evt.Timestamp)
		delete(normalized, "created")
		if response, ok := normalized["response"].(map[string]any); ok {
			delete(response, "id")
			delete(response, "timestamp")
			if len(response) == 0 {
				delete(normalized, "response")
			}
		}
		if evt.StateDelta != nil {
			normalized["stateDelta"] = normalizeState(session.StateMap(evt.StateDelta))
		}
		out = append(out, EventSnapshot(normalized))
	}
	return out
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
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) > 0 {
		var decoded any
		if err := decodeJSON(trimmed, &decoded); err == nil {
			return StateBytesSnapshot{Kind: "json", Value: canonicalJSON(decoded)}
		}
	}
	if utf8.Valid(value) {
		return StateBytesSnapshot{Kind: "utf8", Value: string(value)}
	}
	return StateBytesSnapshot{Kind: "base64", Value: base64.StdEncoding.EncodeToString(value)}
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

func normalizeMemories(entries []*memory.Entry) []MemorySnapshot {
	out := make([]MemorySnapshot, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		snapshot := MemorySnapshot{RawID: entry.ID, App: entry.AppName, UserID: entry.UserID}
		if entry.Memory != nil {
			snapshot.Content = entry.Memory.Memory
			snapshot.Topics = sortedStrings(entry.Memory.Topics)
			snapshot.Kind = string(entry.Memory.Kind)
			snapshot.EventTime = normalizeTimePtr(entry.Memory.EventTime)
			snapshot.Participants = sortedStrings(entry.Memory.Participants)
			snapshot.Location = entry.Memory.Location
		}
		snapshot.Key = memoryKey(snapshot)
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func memoryKey(snapshot MemorySnapshot) string {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		panic(fmt.Sprintf("marshal replay memory key: %v", err))
	}
	return string(encoded)
}

func normalizeSummaries(summaries map[string]*session.Summary, events []event.Event) map[string]SummaryEntry {
	out := make(map[string]SummaryEntry, len(summaries))
	for filterKey, summary := range summaries {
		if summary == nil {
			continue
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
	return out
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

func normalizeTracks(tracks map[session.Track]*session.TrackEvents) []TrackSnapshot {
	names := make([]string, 0, len(tracks))
	for track := range tracks {
		names = append(names, string(track))
	}
	sort.Strings(names)
	out := make([]TrackSnapshot, 0, len(names))
	for _, name := range names {
		events := tracks[session.Track(name)]
		snapshot := TrackSnapshot{Name: name}
		if events != nil {
			snapshot.Track = string(events.Track)
			for _, evt := range events.Events {
				snapshot.Events = append(snapshot.Events, TrackEventSnapshot{
					Track: string(evt.Track), Payload: normalizeTrackPayload(evt.Payload), Timestamp: normalizeTime(evt.Timestamp),
				})
			}
		}
		out = append(out, snapshot)
	}
	return out
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
func Compare(tc Case, results []Result) []Diff {
	var diffs []Diff
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			diffs = append(diffs, CompareSnapshots(
				tc.Name, results[i].Key.SessionID, results[i].Backend, results[j].Backend,
				results[i].Snapshot, results[j].Snapshot, tc.AllowedDiffs,
			)...)
		}
	}
	return diffs
}

type valueDiff struct {
	Path  string
	Left  any
	Right any
}

// CompareSnapshots compares two normalized replay snapshots.
func CompareSnapshots(caseName, sessionID, backendA, backendB string, left, right Snapshot, allowedRules []AllowedDiffRule) []Diff {
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
		for _, d := range recursiveDiff(section.path, jsonValue(section.left), jsonValue(section.right)) {
			entries = append(entries, Diff{
				Case: caseName, SessionID: sessionID, BackendA: backendA, BackendB: backendB,
				Section: section.name, Path: d.Path, Left: d.Left, Right: d.Right,
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
	return entries
}

func jsonValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal replay diff value: %v", err))
	}
	var out any
	if err := decodeJSON(encoded, &out); err != nil {
		panic(fmt.Sprintf("unmarshal replay diff value: %v", err))
	}
	return canonicalJSON(out)
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
			diffs = append(diffs, valueDiff{Path: childPath, Left: missingValue(), Right: rightValue})
		case !rightOK:
			diffs = append(diffs, valueDiff{Path: childPath, Left: leftValue, Right: missingValue()})
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
			diffs = append(diffs, valueDiff{Path: childPath, Left: missingValue(), Right: right[i]})
		case i >= len(right):
			diffs = append(diffs, valueDiff{Path: childPath, Left: left[i], Right: missingValue()})
		default:
			diffs = append(diffs, recursiveDiff(childPath, left[i], right[i])...)
		}
	}
	return diffs
}

func missingValue() map[string]string { return map[string]string{"replay": "missing"} }

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
		start := len(bracketPrefix)
		end := strings.Index(path[start:], "]")
		if end < 0 {
			return "", false
		}
		value, err := strconv.Unquote(path[start : start+end])
		return value, err == nil
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
	if section == "" || section == "*" || path == "" || !allowedPathHasConcreteSegment(path) ||
		backendA == "" || backendA == "*" || backendB == "" || backendB == "*" || reason == "" {
		return false
	}
	return section == entry.Section && wildcardMatch(path, entry.Path) && backendRuleMatches(backendA, backendB, entry.BackendA, entry.BackendB)
}

func allowedPathHasConcreteSegment(path string) bool {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$")
	path = strings.ReplaceAll(path, "*", "")
	return strings.Trim(path, " \t\r\n.[]\"'") != ""
}

func backendRuleMatches(ruleA, ruleB, entryA, entryB string) bool {
	return ruleA == entryA && ruleB == entryB || ruleA == entryB && ruleB == entryA
}

func wildcardMatch(pattern, value string) bool {
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
