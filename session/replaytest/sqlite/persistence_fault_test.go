//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

type persistenceFaultKind string

const (
	persistenceFaultEventContent        persistenceFaultKind = "event_content"
	persistenceFaultSessionState        persistenceFaultKind = "session_state"
	persistenceFaultMemoryContent       persistenceFaultKind = "memory_content"
	persistenceFaultTrackPayload        persistenceFaultKind = "track_payload"
	persistenceFaultSummaryMissing      persistenceFaultKind = "summary_missing"
	persistenceFaultSummaryStale        persistenceFaultKind = "summary_stale"
	persistenceFaultSummaryFilterKey    persistenceFaultKind = "summary_filter_key"
	persistenceFaultSummaryWrongSession persistenceFaultKind = "summary_wrong_session"
)

type persistenceFaultPlan struct {
	mu        sync.Mutex
	kind      persistenceFaultKind
	mutations int
}

func (p *persistenceFaultPlan) mutateOnce(kind persistenceFaultKind, mutate func() error) error {
	if p.kind != kind {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mutations != 0 {
		return nil
	}
	if err := mutate(); err != nil {
		return err
	}
	p.mutations++
	return nil
}

func (p *persistenceFaultPlan) mutationCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mutations
}

type persistenceFaultSessionService struct {
	session.Service
	db   *sql.DB
	plan *persistenceFaultPlan
}

func (s *persistenceFaultSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if err := s.Service.AppendEvent(ctx, sess, evt, opts...); err != nil {
		return err
	}
	return s.plan.mutateOnce(persistenceFaultEventContent, func() error {
		return mutatePersistedEventContent(ctx, s.db, session.Key{
			AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID,
		})
	})
}

func (s *persistenceFaultSessionService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	if err := s.Service.UpdateSessionState(ctx, key, state); err != nil {
		return err
	}
	return s.plan.mutateOnce(persistenceFaultSessionState, func() error {
		return mutatePersistedSessionState(ctx, s.db, key)
	})
}

func (s *persistenceFaultSessionService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	trackService, ok := s.Service.(session.TrackService)
	if !ok {
		return fmt.Errorf("wrapped SQLite service does not implement session.TrackService")
	}
	if err := trackService.AppendTrackEvent(ctx, sess, trackEvent, opts...); err != nil {
		return err
	}
	return s.plan.mutateOnce(persistenceFaultTrackPayload, func() error {
		return mutatePersistedTrackPayload(ctx, s.db, session.Key{
			AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID,
		}, trackEvent.Track)
	})
}

func (s *persistenceFaultSessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	var staleRaw []byte
	if s.plan.kind == persistenceFaultSummaryStale {
		var err error
		staleRaw, _, err = readPersistedSummary(ctx, s.db, key, filterKey)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("capture prior persisted summary: %w", err)
		}
		if errors.Is(err, sql.ErrNoRows) {
			staleRaw = nil
		}
	}
	if err := s.Service.CreateSessionSummary(ctx, sess, filterKey, force); err != nil {
		return err
	}
	switch s.plan.kind {
	case persistenceFaultSummaryStale:
		if len(staleRaw) == 0 {
			return nil
		}
		return s.plan.mutateOnce(s.plan.kind, func() error {
			return restorePersistedSummary(ctx, s.db, key, filterKey, staleRaw)
		})
	case persistenceFaultSummaryMissing:
		return s.plan.mutateOnce(s.plan.kind, func() error {
			return deletePersistedSummary(ctx, s.db, key, filterKey)
		})
	case persistenceFaultSummaryFilterKey:
		return s.plan.mutateOnce(s.plan.kind, func() error {
			return movePersistedSummaryFilterKey(ctx, s.db, key, filterKey, "agent")
		})
	case persistenceFaultSummaryWrongSession:
		return s.plan.mutateOnce(s.plan.kind, func() error {
			return movePersistedSummarySession(ctx, s.db, key, filterKey, key.SessionID+"-foreign")
		})
	default:
		return nil
	}
}

type persistenceFaultMemoryService struct {
	memory.Service
	db   *sql.DB
	plan *persistenceFaultPlan
}

func (s *persistenceFaultMemoryService) AddMemory(
	ctx context.Context,
	key memory.UserKey,
	value string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if err := s.Service.AddMemory(ctx, key, value, topics, opts...); err != nil {
		return err
	}
	return s.plan.mutateOnce(persistenceFaultMemoryContent, func() error {
		return mutatePersistedMemoryContent(ctx, s.db, key)
	})
}

func persistenceFaultBackend(root string, plan *persistenceFaultPlan) replaytest.Backend {
	dependencies := defaultBackendDependencies()
	newSession := dependencies.newSession
	dependencies.newSession = func(db *sql.DB) (session.Service, error) {
		service, err := newSession(db)
		if err != nil || isNilService(service) {
			return service, err
		}
		return &persistenceFaultSessionService{Service: service, db: db, plan: plan}, nil
	}
	newMemory := dependencies.newMemory
	dependencies.newMemory = func(db *sql.DB) (memory.Service, error) {
		service, err := newMemory(db)
		if err != nil || isNilService(service) {
			return service, err
		}
		return &persistenceFaultMemoryService{Service: service, db: db, plan: plan}, nil
	}
	backend := newBackend(root, dependencies)
	backend.Name = "sqlite-fault-" + string(plan.kind)
	return backend
}

type expectedPersistenceDiff struct {
	path             string
	eventIndex       *int
	summaryFilterKey *string
	trackName        string
	memoryID         string
}

func TestSQLitePersistenceFaultsReachReport(t *testing.T) {
	zero := 0
	fullSummary := ""
	tests := []struct {
		name     string
		kind     persistenceFaultKind
		caseName string
		custom   *replaytest.Case
		diffs    []expectedPersistenceDiff
	}{
		{
			name: "event content", kind: persistenceFaultEventContent, caseName: "single_turn_text",
			diffs: []expectedPersistenceDiff{{path: "/events/0/choices/0/message/content", eventIndex: &zero}},
		},
		{
			name: "session state", kind: persistenceFaultSessionState,
			custom: &replaytest.Case{
				Name: "sqlite_session_state_fault",
				Requires: []replaytest.Capability{
					replaytest.CapabilitySession, replaytest.CapabilitySessionState,
				},
				Steps: []replaytest.Step{{
					Name: "write-state", Kind: replaytest.StepUpdateState,
					State: &replaytest.StateInput{
						Scope:  replaytest.StateScopeSession,
						Values: session.StateMap{"value": []byte(`"expected"`)},
					},
				}},
			},
			diffs: []expectedPersistenceDiff{{path: "/state/session/value/json"}},
		},
		{
			name: "memory content", kind: persistenceFaultMemoryContent,
			custom: &replaytest.Case{
				Name: "sqlite_memory_content_fault",
				Requires: []replaytest.Capability{
					replaytest.CapabilitySession, replaytest.CapabilityMemory,
				},
				Steps: []replaytest.Step{{
					Name: "write-memory", Kind: replaytest.StepAddMemory,
					Memory: &replaytest.MemoryInput{Memory: "expected memory"},
				}},
			},
			diffs: []expectedPersistenceDiff{{path: "/memories/0/memory/memory", memoryID: "memory-0"}},
		},
		{
			name: "track payload", kind: persistenceFaultTrackPayload, caseName: "track_events",
			diffs: []expectedPersistenceDiff{{path: "/tracks/tool~1weather/0/payload/status", trackName: "tool/weather"}},
		},
		{
			name: "summary missing", kind: persistenceFaultSummaryMissing, caseName: "summary_generation",
			diffs: []expectedPersistenceDiff{{path: "/summaries/", summaryFilterKey: &fullSummary}},
		},
		{
			name: "summary stale update ignored", kind: persistenceFaultSummaryStale, caseName: "summary_update",
			diffs: []expectedPersistenceDiff{
				{path: "/summaries//boundary/cutoff_at", summaryFilterKey: &fullSummary},
				{path: "/summaries//boundary/last_event_id", summaryFilterKey: &fullSummary},
				{path: "/summaries//retained_event_ids/length", summaryFilterKey: &fullSummary},
				{path: "/summaries//retained_event_ids/0", summaryFilterKey: &fullSummary},
				{path: "/summaries//text", summaryFilterKey: &fullSummary},
				{path: "/summaries//updated_at", summaryFilterKey: &fullSummary},
			},
		},
		{
			name: "summary filter key", kind: persistenceFaultSummaryFilterKey, caseName: "summary_filter_key",
			diffs: []expectedPersistenceDiff{
				{path: "/summaries/agent", summaryFilterKey: stringPointer("agent")},
				{path: "/summaries/agent~1custom", summaryFilterKey: stringPointer("agent/custom")},
			},
		},
		{
			name: "summary wrong session", kind: persistenceFaultSummaryWrongSession, caseName: "summary_generation",
			diffs: []expectedPersistenceDiff{{path: "/summaries/", summaryFilterKey: &fullSummary}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var replayCase replaytest.Case
			if test.custom != nil {
				replayCase = *test.custom
			} else {
				replayCase = persistencePublicCase(t, test.caseName)
			}
			root := t.TempDir()
			plan := &persistenceFaultPlan{kind: test.kind}
			report, err := (replaytest.Runner{Reference: "inmemory"}).Run(
				context.Background(),
				[]replaytest.Case{replayCase},
				[]replaytest.Backend{
					replaytest.InMemoryBackend(),
					persistenceFaultBackend(root, plan),
				},
			)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got := plan.mutationCount(); got != 1 {
				t.Fatalf("persistence mutations = %d, want 1", got)
			}
			if report.FailedCases != 1 || report.BlockingDiffs == 0 {
				t.Fatalf("failure totals = failed %d, blocking %d, want one failed case with blocking diffs", report.FailedCases, report.BlockingDiffs)
			}
			assertPersistenceDiffs(t, report.Cases[0].Diffs, test.diffs)
			assertReportRoundTrip(t, report)
			assertSQLiteRootClean(t, root)
		})
	}
}

func persistencePublicCase(t *testing.T, name string) replaytest.Case {
	t.Helper()
	for _, replayCase := range replaytest.PublicCases() {
		if replayCase.Name == name {
			return replayCase
		}
	}
	t.Fatalf("public replay case %q not found", name)
	return replaytest.Case{}
}

func assertPersistenceDiffs(t *testing.T, diffs []replaytest.Diff, expected []expectedPersistenceDiff) {
	t.Helper()
	type diffKey struct {
		path             string
		eventIndex       int
		hasEventIndex    bool
		summaryFilterKey string
		hasSummaryKey    bool
		trackName        string
		memoryID         string
	}
	keyOf := func(diff replaytest.Diff) diffKey {
		key := diffKey{path: diff.Path, trackName: diff.TrackName, memoryID: diff.MemoryID}
		if diff.EventIndex != nil {
			key.eventIndex, key.hasEventIndex = *diff.EventIndex, true
		}
		if diff.SummaryFilterKey != nil {
			key.summaryFilterKey, key.hasSummaryKey = *diff.SummaryFilterKey, true
		}
		return key
	}
	expectedKeys := make(map[diffKey]struct{}, len(expected))
	for _, want := range expected {
		key := diffKey{path: want.path, trackName: want.trackName, memoryID: want.memoryID}
		if want.eventIndex != nil {
			key.eventIndex, key.hasEventIndex = *want.eventIndex, true
		}
		if want.summaryFilterKey != nil {
			key.summaryFilterKey, key.hasSummaryKey = *want.summaryFilterKey, true
		}
		if _, exists := expectedKeys[key]; exists {
			t.Fatalf("test definition repeats expected diff %#v", key)
		}
		expectedKeys[key] = struct{}{}
	}
	actualKeys := make(map[diffKey]struct{}, len(diffs))
	for _, diff := range diffs {
		if diff.Allowed {
			t.Fatalf("persistence fault produced an allowed diff: %+v", diff)
		}
		if diff.Path == "/execution" {
			t.Fatalf("persistence fault became an execution exclusion: %+v", diff)
		}
		key := keyOf(diff)
		if _, exists := actualKeys[key]; exists {
			t.Fatalf("persistence fault produced duplicate blocking diff %#v", key)
		}
		actualKeys[key] = struct{}{}
	}
	if len(actualKeys) != len(expectedKeys) {
		t.Fatalf("blocking diff count = %d, want %d; got %#v", len(actualKeys), len(expectedKeys), actualKeys)
	}
	for key := range actualKeys {
		if _, exists := expectedKeys[key]; !exists {
			t.Fatalf("unexpected blocking persistence diff %#v", key)
		}
	}
	for _, want := range expected {
		var found *replaytest.Diff
		for index := range diffs {
			if diffs[index].Path == want.path && !diffs[index].Allowed {
				found = &diffs[index]
				break
			}
		}
		if found == nil {
			t.Fatalf("blocking diff %q not found in %+v", want.path, diffs)
		}
		if !equalIntPointer(found.EventIndex, want.eventIndex) {
			t.Fatalf("diff %q event locator = %v, want %v", want.path, found.EventIndex, want.eventIndex)
		}
		if !equalStringPointer(found.SummaryFilterKey, want.summaryFilterKey) {
			t.Fatalf("diff %q summary locator = %v, want %v", want.path, found.SummaryFilterKey, want.summaryFilterKey)
		}
		if found.TrackName != want.trackName || found.MemoryID != want.memoryID {
			t.Fatalf("diff %q locators = track %q memory %q, want track %q memory %q", want.path, found.TrackName, found.MemoryID, want.trackName, want.memoryID)
		}
	}
}

func restorePersistedSummary(
	ctx context.Context,
	db *sql.DB,
	key session.Key,
	filterKey string,
	raw []byte,
) error {
	if len(raw) == 0 {
		return fmt.Errorf("prior persisted summary is empty")
	}
	return execExactlyOne(ctx, db, "restore stale persisted summary", `UPDATE session_summaries
SET summary = ?
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?`,
		raw, key.AppName, key.UserID, key.SessionID, filterKey)
}

func assertReportRoundTrip(t *testing.T, report replaytest.Report) {
	t.Helper()
	if err := report.Validate(); err != nil {
		t.Fatalf("Report.Validate() error = %v", err)
	}
	var output bytes.Buffer
	if err := replaytest.WriteReport(&output, report); err != nil {
		t.Fatalf("WriteReport() error = %v", err)
	}
	var decoded replaytest.Report
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal(WriteReport()) error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("round-trip Report.Validate() error = %v", err)
	}
}

func assertSQLiteRootClean(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", root, err)
	}
	if len(entries) != 0 {
		t.Fatalf("SQLite backend left %d case directories", len(entries))
	}
}

func equalIntPointer(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalStringPointer(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func stringPointer(value string) *string { return &value }

func mutatePersistedEventContent(ctx context.Context, db *sql.DB, key session.Key) error {
	var rowID int64
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT id, event FROM session_events
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL
ORDER BY id LIMIT 1`, key.AppName, key.UserID, key.SessionID).Scan(&rowID, &raw)
	if err != nil {
		return fmt.Errorf("select persisted event: %w", err)
	}
	var evt event.Event
	if err := json.Unmarshal(raw, &evt); err != nil {
		return fmt.Errorf("decode persisted event: %w", err)
	}
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return fmt.Errorf("persisted event has no response message")
	}
	evt.Response.Choices[0].Message.Content = "sqlite-injected-event-content"
	encoded, err := json.Marshal(&evt)
	if err != nil {
		return fmt.Errorf("encode persisted event: %w", err)
	}
	return execExactlyOne(ctx, db, "update persisted event",
		`UPDATE session_events SET event = ? WHERE id = ?`, encoded, rowID)
}

func mutatePersistedSessionState(ctx context.Context, db *sql.DB, key session.Key) error {
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT state FROM session_states
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
		key.AppName, key.UserID, key.SessionID).Scan(&raw)
	if err != nil {
		return fmt.Errorf("select persisted session state: %w", err)
	}
	var state sessionsqlite.SessionState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("decode persisted session state: %w", err)
	}
	state.State["value"] = []byte(`"sqlite-injected-state"`)
	encoded, err := json.Marshal(&state)
	if err != nil {
		return fmt.Errorf("encode persisted session state: %w", err)
	}
	return execExactlyOne(ctx, db, "update persisted session state", `UPDATE session_states SET state = ?
WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL`,
		encoded, key.AppName, key.UserID, key.SessionID)
}

func mutatePersistedMemoryContent(ctx context.Context, db *sql.DB, key memory.UserKey) error {
	var memoryID string
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT memory_id, memory_data FROM memories
WHERE app_name = ? AND user_id = ? AND deleted_at IS NULL
ORDER BY memory_id LIMIT 1`, key.AppName, key.UserID).Scan(&memoryID, &raw)
	if err != nil {
		return fmt.Errorf("select persisted memory: %w", err)
	}
	var entry memory.Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return fmt.Errorf("decode persisted memory: %w", err)
	}
	if entry.Memory == nil {
		return fmt.Errorf("persisted memory has nil content")
	}
	entry.Memory.Memory = "sqlite-injected-memory-content"
	encoded, err := json.Marshal(&entry)
	if err != nil {
		return fmt.Errorf("encode persisted memory: %w", err)
	}
	return execExactlyOne(ctx, db, "update persisted memory",
		`UPDATE memories SET memory_data = ? WHERE memory_id = ?`, encoded, memoryID)
}

func mutatePersistedTrackPayload(
	ctx context.Context,
	db *sql.DB,
	key session.Key,
	track session.Track,
) error {
	var rowID int64
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT id, event FROM session_track_events
WHERE app_name = ? AND user_id = ? AND session_id = ? AND track = ? AND deleted_at IS NULL
ORDER BY id LIMIT 1`, key.AppName, key.UserID, key.SessionID, track).Scan(&rowID, &raw)
	if err != nil {
		return fmt.Errorf("select persisted track event: %w", err)
	}
	var trackEvent session.TrackEvent
	if err := json.Unmarshal(raw, &trackEvent); err != nil {
		return fmt.Errorf("decode persisted track event: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(trackEvent.Payload, &payload); err != nil {
		return fmt.Errorf("decode persisted track payload: %w", err)
	}
	payload["status"] = "sqlite-injected-track-status"
	trackEvent.Payload, err = json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode persisted track payload: %w", err)
	}
	encoded, err := json.Marshal(&trackEvent)
	if err != nil {
		return fmt.Errorf("encode persisted track event: %w", err)
	}
	return execExactlyOne(ctx, db, "update persisted track event",
		`UPDATE session_track_events SET event = ? WHERE id = ?`, encoded, rowID)
}

func deletePersistedSummary(ctx context.Context, db *sql.DB, key session.Key, filterKey string) error {
	return execExactlyOne(ctx, db, "delete persisted summary", `DELETE FROM session_summaries
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?`,
		key.AppName, key.UserID, key.SessionID, filterKey)
}

func movePersistedSummaryFilterKey(
	ctx context.Context,
	db *sql.DB,
	key session.Key,
	from string,
	to string,
) error {
	raw, summaryValue, err := readPersistedSummary(ctx, db, key, from)
	if err != nil {
		return err
	}
	if summaryValue.Boundary == nil {
		return fmt.Errorf("persisted summary has no boundary")
	}
	summaryValue.Boundary.FilterKey = to
	raw, err = json.Marshal(summaryValue)
	if err != nil {
		return fmt.Errorf("encode persisted summary: %w", err)
	}
	return execExactlyOne(ctx, db, "move persisted summary filter key", `UPDATE session_summaries
SET filter_key = ?, summary = ?
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?`,
		to, raw, key.AppName, key.UserID, key.SessionID, from)
}

func movePersistedSummarySession(
	ctx context.Context,
	db *sql.DB,
	key session.Key,
	filterKey string,
	toSessionID string,
) error {
	return execExactlyOne(ctx, db, "move persisted summary session", `UPDATE session_summaries
SET session_id = ?
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?`,
		toSessionID, key.AppName, key.UserID, key.SessionID, filterKey)
}

func readPersistedSummary(
	ctx context.Context,
	db *sql.DB,
	key session.Key,
	filterKey string,
) ([]byte, *session.Summary, error) {
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT summary FROM session_summaries
WHERE app_name = ? AND user_id = ? AND session_id = ? AND filter_key = ?`,
		key.AppName, key.UserID, key.SessionID, filterKey).Scan(&raw)
	if err != nil {
		return nil, nil, fmt.Errorf("select persisted summary: %w", err)
	}
	var summaryValue session.Summary
	if err := json.Unmarshal(raw, &summaryValue); err != nil {
		return nil, nil, fmt.Errorf("decode persisted summary: %w", err)
	}
	return raw, &summaryValue, nil
}

func execExactlyOne(
	ctx context.Context,
	db *sql.DB,
	operation string,
	query string,
	args ...any,
) error {
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if affected != 1 {
		return fmt.Errorf("%s affected %d rows, want 1", operation, affected)
	}
	return nil
}
