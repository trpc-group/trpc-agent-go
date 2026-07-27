//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memsqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sesssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	sessionsummary "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type backendBundle struct {
	name              string
	sessionService    session.Service
	trackService      session.TrackService
	memoryService     memory.Service
	sqliteMemoryDB    *sql.DB
	sqliteMemoryTable string
	summarizer        *deterministicSummarizer
}

type replaySnapshot = replaytest.Snapshot
type replaySessionSnapshot = replaytest.SessionSnapshot
type replayEventSnapshot = replaytest.EventSnapshot
type replayMemorySnapshot = replaytest.MemorySnapshot
type summaryEntry = replaytest.SummaryEntry
type summaryBoundary = replaytest.SummaryBoundary
type trackSnapshot = replaytest.TrackSnapshot
type trackEventSnapshot = replaytest.TrackEventSnapshot
type trackPayloadSnapshot = replaytest.TrackPayloadSnapshot
type replayStateBytesSnapshot = replaytest.StateBytesSnapshot
type diffEntry = replaytest.Diff
type allowedDiffRule = replaytest.AllowedDiffRule

type replayCase struct {
	name               string
	initialState       session.StateMap
	appState           session.StateMap
	userState          session.StateMap
	sessionState       session.StateMap
	events             []eventSpec
	concurrentMemories []memoryOpSpec
	summaries          []summaryStep
	tracks             []trackSpec
	memories           []memoryOpSpec
	queries            []memoryQuerySpec
	allowedDiffs       []allowedDiffRule
}

type eventSpec struct {
	invocationID       string
	parentInvocationID string
	parentMetadata     *event.ParentInvocationMetadata
	author             string
	message            model.Message
	object             string
	branch             string
	filterKey          string
	tag                string
	stateDelta         session.StateMap
	extensions         map[string]any
	actions            *event.EventActions
}

type memoryOpSpec struct {
	name        string
	op          string
	ref         string
	content     string
	topics      []string
	metadata    *memory.Metadata
	resultAlias string
}

type memoryQuerySpec struct {
	query            string
	expectedContents []string
}

type summaryStep struct {
	name        string
	filterKey   string
	force       bool
	text        string
	wantText    string
	eventPrefix *int
}

type trackSpec struct {
	name      string
	payload   any
	timestamp time.Time
}

type replayCaseResult struct {
	backend  string
	key      session.Key
	snapshot replaySnapshot
}

type replayBackendInjection func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key)

type replayFailOperation string

const (
	replayFailAppendEvent        replayFailOperation = "append_event"
	replayFailUpdateSessionState replayFailOperation = "update_session_state"
	replayFailAddMemory          replayFailOperation = "add_memory"
	replayFailCreateSummary      replayFailOperation = "create_summary"
)

type replayFailBoundary string

const (
	replayFailBeforeWrite replayFailBoundary = "before_write"
	replayFailAfterWrite  replayFailBoundary = "after_write"
)

type replayFailSpec struct {
	operation  replayFailOperation
	boundary   replayFailBoundary
	occurrence int
}

type replayFailStats struct {
	triggered             int
	injectedErrors        int
	retries               int
	targetUnderlyingCalls int
}

type replayFailOnce struct {
	mu              sync.Mutex
	spec            replayFailSpec
	seenOccurrences int
	retryPending    bool
	stats           replayFailStats
}

type replayRetrySessionService struct {
	session.Service
	fault *replayFailOnce
}

type replayRetryMemoryService struct {
	memory.Service
	fault *replayFailOnce
}

type replaySessionReadMutationService struct {
	session.Service
	mutate func(*session.Session)
}

type replayStaleSummaryBoundaryService struct {
	session.Service
	mu            sync.Mutex
	summaryCalls  int
	filterKey     string
	firstBoundary *session.SummaryBoundary
}

type replayRetryComparison struct {
	backend  string
	baseline replayCaseResult
	retry    replayCaseResult
	diffs    []diffEntry
	stats    replayFailStats
}

var replayBaseTime = time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)

const replaySQLiteMemoryTableName = "memories"

var _ sessionsummary.SessionSummarizer = (*deterministicSummarizer)(nil)

type deterministicSummarizer struct {
	text string
}

func (s *deterministicSummarizer) ShouldSummarize(*session.Session) bool {
	return true
}

func (s *deterministicSummarizer) Summarize(
	context.Context,
	*session.Session,
) (string, error) {
	if s.text == "" {
		return "smoke summary", nil
	}
	return s.text, nil
}

func (s *deterministicSummarizer) SetPrompt(string) {}

func (s *deterministicSummarizer) SetModel(model.Model) {}

func (s *deterministicSummarizer) Metadata() map[string]any {
	return map[string]any{"deterministic": true}
}

func openSQLiteDB(t *testing.T, name string) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), name+".db")
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	return db
}

func makeReplayBackends(t *testing.T) []backendBundle {
	t.Helper()

	inMemorySummarizer := &deterministicSummarizer{}
	inMemorySessionService := sessinmemory.NewSessionService(
		sessinmemory.WithSummarizer(inMemorySummarizer),
	)
	inMemoryMemoryService := meminmemory.NewMemoryService(
		meminmemory.WithMinSearchScore(0),
		meminmemory.WithMaxResults(0),
	)

	sqliteSummarizer := &deterministicSummarizer{}
	sqliteSessionService, err := sesssqlite.NewService(
		openSQLiteDB(t, "replay-session"),
		sesssqlite.WithSummarizer(sqliteSummarizer),
	)
	require.NoError(t, err)
	sqliteMemoryDB := openSQLiteDB(t, "replay-memory")
	sqliteMemoryService, err := memsqlite.NewService(
		sqliteMemoryDB,
		memsqlite.WithTableName(replaySQLiteMemoryTableName),
		memsqlite.WithMinSearchScore(0),
		memsqlite.WithMaxResults(0),
	)
	require.NoError(t, err)

	backends := []backendBundle{
		{
			name:           "in_memory",
			sessionService: inMemorySessionService,
			trackService:   inMemorySessionService,
			memoryService:  inMemoryMemoryService,
			summarizer:     inMemorySummarizer,
		},
		{
			name:              "sqlite",
			sessionService:    sqliteSessionService,
			trackService:      sqliteSessionService,
			memoryService:     sqliteMemoryService,
			sqliteMemoryDB:    sqliteMemoryDB,
			sqliteMemoryTable: replaySQLiteMemoryTableName,
			summarizer:        sqliteSummarizer,
		},
	}
	t.Cleanup(func() {
		closeReplayBackends(t, backends)
	})
	return backends
}

func closeReplayBackends(t *testing.T, backends []backendBundle) {
	t.Helper()

	for _, backend := range backends {
		require.NoError(t, backend.memoryService.Close())
	}
	for _, backend := range backends {
		require.NoError(t, backend.sessionService.Close())
	}
}

func makeReplaySnapshot(sess *session.Session, memories []*memory.Entry) replaySnapshot {
	return replaytest.BuildSnapshot(sess, memories)
}

func normalizeReplayState(state session.StateMap) map[string]any {
	return replaytest.BuildSnapshot(&session.Session{State: cloneReplayStateMap(state)}, nil).State
}

func normalizeReplayTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func diffReplaySnapshots(
	caseName string,
	sessionID string,
	backendA string,
	backendB string,
	left replaySnapshot,
	right replaySnapshot,
	allowedRules []allowedDiffRule,
) []diffEntry {
	return replaytest.CompareSnapshots(
		caseName,
		sessionID,
		backendA,
		backendB,
		left,
		right,
		allowedRules,
	)
}

func replayWildcardMatch(pattern string, value string) bool {
	if pattern == value || pattern == "*" {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return false
	}
	if parts[0] != "" && !strings.HasPrefix(value, parts[0]) {
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

func replayMissingValue() map[string]string {
	return map[string]string{"replay": "missing"}
}
func replayDiffReportPath() string {
	if override := strings.TrimSpace(os.Getenv("TRPC_AGENT_REPLAY_REPORT_PATH")); override != "" {
		return override
	}
	return filepath.Join("..", "session_memory_summary_track_diff_report.json")
}

func writeReplayDiffReport(path string, entries []diffEntry) error {
	if strings.TrimSpace(path) == "" {
		path = replayDiffReportPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create replay diff report dir: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create replay diff report: %w", err)
	}
	if err := replaytest.WriteReport(file, entries); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close replay diff report: %w", err)
	}
	return nil
}

func runReplayCaseOnBackend(
	t *testing.T,
	ctx context.Context,
	backend backendBundle,
	tc replayCase,
) replayCaseResult {
	t.Helper()
	result, err := replaytest.Run(ctx, toReplayTestBackend(backend), toReplayTestCase(tc))
	require.NoError(t, err)
	return replayCaseResult{
		backend:  result.Backend,
		key:      result.Key,
		snapshot: result.Snapshot,
	}
}

func toReplayTestBackend(backend backendBundle) replaytest.Backend {
	return replaytest.Backend{
		Name:           backend.name,
		SessionService: backend.sessionService,
		TrackService:   backend.trackService,
		MemoryService:  backend.memoryService,
		SetSummaryText: func(text string) {
			backend.summarizer.text = text
		},
	}
}

func toReplayTestCase(tc replayCase) replaytest.Case {
	events := make([]*event.Event, 0, len(tc.events))
	for i, spec := range tc.events {
		events = append(events, buildReplayEvent(tc.name, i, spec))
	}
	convertMemoryOps := func(specs []memoryOpSpec) []replaytest.MemoryOp {
		out := make([]replaytest.MemoryOp, 0, len(specs))
		for _, spec := range specs {
			out = append(out, replaytest.MemoryOp{
				Name: spec.name, Operation: replaytest.MemoryOperation(spec.op), Ref: spec.ref,
				Content: spec.content, Topics: append([]string(nil), spec.topics...),
				Metadata: spec.metadata, ResultAlias: spec.resultAlias,
			})
		}
		return out
	}
	summaries := make([]replaytest.SummaryStep, 0, len(tc.summaries))
	for _, spec := range tc.summaries {
		summaries = append(summaries, replaytest.SummaryStep{
			Name: spec.name, FilterKey: spec.filterKey, Force: spec.force,
			Text: spec.text, WantText: spec.wantText, EventPrefix: spec.eventPrefix,
		})
	}
	tracks := make([]replaytest.TrackSpec, 0, len(tc.tracks))
	for _, spec := range tc.tracks {
		tracks = append(tracks, replaytest.TrackSpec{
			Name: spec.name, Payload: spec.payload, Timestamp: spec.timestamp,
		})
	}
	queries := make([]replaytest.MemoryQuery, 0, len(tc.queries))
	for _, spec := range tc.queries {
		queries = append(queries, replaytest.MemoryQuery{
			Query: spec.query, ExpectedContents: append([]string(nil), spec.expectedContents...),
		})
	}
	return replaytest.Case{
		Name: tc.name, InitialState: tc.initialState, AppState: tc.appState,
		UserState: tc.userState, SessionState: tc.sessionState, Events: events,
		ConcurrentMemories: convertMemoryOps(tc.concurrentMemories),
		Summaries:          summaries, Tracks: tracks, Memories: convertMemoryOps(tc.memories),
		Queries: queries, AllowedDiffs: tc.allowedDiffs,
	}
}

func refreshReplayCaseResultSnapshot(
	t *testing.T,
	ctx context.Context,
	backend backendBundle,
	key session.Key,
) replayCaseResult {
	t.Helper()

	got, err := backend.sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	memories, err := backend.memoryService.ReadMemories(ctx, memory.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	}, 0)
	require.NoError(t, err)
	return replayCaseResult{
		backend:  backend.name,
		key:      key,
		snapshot: makeReplaySnapshot(got, memories),
	}
}

// injectSQLiteReplayMemoryRow bypasses AddMemory so anomaly tests can model a
// backend bug that persists duplicate retry effects despite idempotent APIs.
func injectSQLiteReplayMemoryRow(
	t *testing.T,
	ctx context.Context,
	backend backendBundle,
	key session.Key,
	memoryID string,
	content string,
	topics []string,
) {
	t.Helper()
	require.NotNil(t, backend.sqliteMemoryDB)
	require.NotEmpty(t, backend.sqliteMemoryTable)

	var tableName string
	err := backend.sqliteMemoryDB.QueryRowContext(
		ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
		backend.sqliteMemoryTable,
	).Scan(&tableName)
	require.NoError(t, err)
	require.Equal(t, backend.sqliteMemoryTable, tableName)

	now := replayBaseTime.Add(100 * time.Second)
	entry := &memory.Entry{
		ID:      memoryID,
		AppName: key.AppName,
		UserID:  key.UserID,
		Memory: &memory.Memory{
			Memory:      content,
			Topics:      append([]string(nil), topics...),
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	memoryData, err := json.Marshal(entry)
	require.NoError(t, err)

	const insertSQL = `
INSERT INTO %s (
  memory_id, app_name, user_id, memory_data, created_at, updated_at,
  deleted_at
) VALUES (?, ?, ?, ?, ?, ?, NULL)`
	_, err = backend.sqliteMemoryDB.ExecContext(
		ctx,
		fmt.Sprintf(insertSQL, backend.sqliteMemoryTable),
		memoryID,
		key.AppName,
		key.UserID,
		memoryData,
		now.UTC().UnixNano(),
		now.UTC().UnixNano(),
	)
	require.NoError(t, err)
}

func runReplayCaseWithBackendInjection(
	t *testing.T,
	ctx context.Context,
	tc replayCase,
	targetBackend string,
	inject replayBackendInjection,
) []diffEntry {
	t.Helper()

	backends := makeReplayBackends(t)
	results := make([]replayCaseResult, 0, len(backends))
	for _, backend := range backends {
		result := runReplayCaseOnBackend(t, ctx, backend, tc)
		if backend.name == targetBackend {
			inject(t, ctx, backend, result.key)
			result = refreshReplayCaseResultSnapshot(t, ctx, backend, result.key)
		}
		results = append(results, result)
	}
	return compareReplayCaseResults(tc, results)
}

var errReplayInjectedFailure = errors.New("replay injected failure")

func (f *replayFailOnce) execute(operation replayFailOperation, call func() error) error {
	f.mu.Lock()
	if operation != f.spec.operation {
		f.mu.Unlock()
		return call()
	}
	if f.retryPending {
		f.retryPending = false
		f.stats.targetUnderlyingCalls++
		f.mu.Unlock()
		return call()
	}
	occurrence := f.seenOccurrences
	f.seenOccurrences++
	target := occurrence == f.spec.occurrence && f.stats.triggered == 0
	if !target {
		f.mu.Unlock()
		return call()
	}

	switch f.spec.boundary {
	case replayFailBeforeWrite:
		f.stats.triggered++
		f.stats.injectedErrors++
		f.retryPending = true
		f.mu.Unlock()
		return errReplayInjectedFailure
	case replayFailAfterWrite:
		f.stats.targetUnderlyingCalls++
		f.mu.Unlock()
		if err := call(); err != nil {
			return err
		}
		f.mu.Lock()
		f.stats.triggered++
		f.stats.injectedErrors++
		f.retryPending = true
		f.mu.Unlock()
		return errReplayInjectedFailure
	default:
		f.mu.Unlock()
		return fmt.Errorf("unknown replay failure boundary %q", f.spec.boundary)
	}
}

func (f *replayFailOnce) recordRetry() {
	f.mu.Lock()
	f.stats.retries++
	f.mu.Unlock()
}

func executeReplayRetry(fault *replayFailOnce, call func() error) error {
	err := call()
	if !errors.Is(err, errReplayInjectedFailure) {
		return err
	}
	fault.recordRetry()
	return call()
}

func (f *replayFailOnce) snapshotStats() replayFailStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stats
}

func (s *replayRetrySessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	return executeReplayRetry(s.fault, func() error {
		return s.fault.execute(replayFailAppendEvent, func() error {
			return s.Service.AppendEvent(ctx, sess, evt, opts...)
		})
	})
}

func (s *replayRetrySessionService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	return executeReplayRetry(s.fault, func() error {
		return s.fault.execute(replayFailUpdateSessionState, func() error {
			return s.Service.UpdateSessionState(ctx, key, state)
		})
	})
}

func (s *replayRetrySessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	return executeReplayRetry(s.fault, func() error {
		return s.fault.execute(replayFailCreateSummary, func() error {
			return s.Service.CreateSessionSummary(ctx, sess, filterKey, force)
		})
	})
}

func (s *replayRetryMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryText string,
	topics []string,
	opts ...memory.AddOption,
) error {
	return executeReplayRetry(s.fault, func() error {
		return s.fault.execute(replayFailAddMemory, func() error {
			return s.Service.AddMemory(ctx, userKey, memoryText, topics, opts...)
		})
	})
}

func (s *replaySessionReadMutationService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	got, err := s.Service.GetSession(ctx, key, opts...)
	if err != nil || got == nil {
		return got, err
	}
	got = got.Clone()
	s.mutate(got)
	return got, nil
}

func (s *replayStaleSummaryBoundaryService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if err := s.Service.CreateSessionSummary(ctx, sess, filterKey, force); err != nil {
		return err
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	got, err := s.Service.GetSession(ctx, key)
	if err != nil {
		return err
	}
	if got == nil {
		return fmt.Errorf("get session after summary returned nil")
	}
	got.SummariesMu.RLock()
	summary := got.Summaries[filterKey]
	got.SummariesMu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.summaryCalls++
	if s.summaryCalls == 1 && summary != nil {
		s.filterKey = filterKey
		s.firstBoundary = summary.Boundary.Clone()
	}
	return nil
}

func (s *replayStaleSummaryBoundaryService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	got, err := s.Service.GetSession(ctx, key, opts...)
	if err != nil || got == nil {
		return got, err
	}
	s.mu.Lock()
	shouldMutate := s.summaryCalls >= 2 && s.firstBoundary != nil
	filterKey := s.filterKey
	boundary := s.firstBoundary.Clone()
	s.mu.Unlock()
	if !shouldMutate {
		return got, nil
	}
	got = got.Clone()
	got.SummariesMu.Lock()
	if summary := got.Summaries[filterKey]; summary != nil {
		summary = summary.Clone()
		summary.Boundary = boundary
		got.Summaries[filterKey] = summary
	}
	got.SummariesMu.Unlock()
	return got, nil
}

func wrapReplayBackendForRetry(backend backendBundle, fault *replayFailOnce) backendBundle {
	wrappedSession := &replayRetrySessionService{Service: backend.sessionService, fault: fault}
	wrappedMemory := &replayRetryMemoryService{Service: backend.memoryService, fault: fault}
	backend.sessionService = wrappedSession
	backend.memoryService = wrappedMemory
	return backend
}

func runReplayCaseWithRetry(
	t *testing.T,
	ctx context.Context,
	tc replayCase,
	spec replayFailSpec,
) []replayRetryComparison {
	t.Helper()

	baselineBackends := makeReplayBackends(t)
	retryBackends := makeReplayBackends(t)
	retryByName := make(map[string]backendBundle, len(retryBackends))
	for _, backend := range retryBackends {
		retryByName[backend.name] = backend
	}
	comparisons := make([]replayRetryComparison, 0, len(baselineBackends))
	for _, baselineBackend := range baselineBackends {
		retryBackend, ok := retryByName[baselineBackend.name]
		require.Truef(t, ok, "missing retry backend %s", baselineBackend.name)
		fault := &replayFailOnce{spec: spec}
		baseline := runReplayCaseOnBackend(t, ctx, baselineBackend, tc)
		retry := runReplayCaseOnBackend(t, ctx, wrapReplayBackendForRetry(retryBackend, fault), tc)
		diffs := diffReplaySnapshots(
			tc.name,
			baseline.key.SessionID,
			baseline.backend+"_baseline",
			retry.backend+"_retry",
			baseline.snapshot,
			retry.snapshot,
			nil,
		)
		comparisons = append(comparisons, replayRetryComparison{
			backend: baselineBackend.name, baseline: baseline, retry: retry,
			diffs: diffs, stats: fault.snapshotStats(),
		})
	}
	return comparisons
}

func requireReplayDiff(
	t *testing.T,
	diffs []diffEntry,
	section string,
	pathGlob string,
	context map[string]any,
) diffEntry {
	t.Helper()

	for _, diff := range diffs {
		if diff.Section != section {
			continue
		}
		if !replayWildcardMatch(pathGlob, diff.Path) {
			continue
		}
		matchesContext := true
		for key, want := range context {
			got, ok := diff.Context[key]
			if !ok || !reflect.DeepEqual(got, want) {
				matchesContext = false
				break
			}
		}
		if matchesContext {
			return diff
		}
	}
	require.Failf(
		t,
		"missing replay diff",
		"section=%s pathGlob=%s context=%v diffs=%+v",
		section,
		pathGlob,
		context,
		diffs,
	)
	return diffEntry{}
}

func requireSummaryLastEventIndexDiff(t *testing.T, diffs []diffEntry, wantLeft any, wantRight any) {
	t.Helper()

	diff := requireReplayDiff(
		t,
		diffs,
		"summary",
		`$.summary["branch/a"].boundary.last_event_index`,
		map[string]any{"summary_filter_key": "branch/a"},
	)
	require.Equal(t, `$.summary["branch/a"].boundary.last_event_index`, diff.Path)
	require.Equal(t, wantLeft, diff.Left)
	require.Equal(t, wantRight, diff.Right)
	require.False(t, diff.Allowed)
}

func requireReplayReportFields(t *testing.T, reportPath string) []map[string]any {
	t.Helper()

	encoded, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	var rawReport []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &rawReport))
	require.NotEmpty(t, rawReport)
	for _, entry := range rawReport {
		for _, key := range []string{
			"case",
			"session_id",
			"backend_a",
			"backend_b",
			"section",
			"path",
			"left",
			"right",
			"allowed",
			"reason",
			"context",
		} {
			require.Contains(t, entry, key)
		}
	}
	return rawReport
}

func cloneReplayStateMap(state session.StateMap) session.StateMap {
	if state == nil {
		return nil
	}
	out := make(session.StateMap, len(state))
	for key, value := range state {
		if value == nil {
			out[key] = nil
			continue
		}
		out[key] = append([]byte(nil), value...)
	}
	return out
}

func buildReplayEvent(caseName string, index int, spec eventSpec) *event.Event {
	responseObject := spec.object
	if responseObject == "" {
		responseObject = model.ObjectTypeChatCompletion
	}
	author := spec.author
	if author == "" {
		author = "agent"
	}
	invocationID := spec.invocationID
	if invocationID == "" {
		invocationID = fmt.Sprintf("%s-invocation-%d", caseName, index)
	}

	evt := &event.Event{
		Response: &model.Response{
			ID:        fmt.Sprintf("%s-response-%d", caseName, index),
			Object:    responseObject,
			Created:   int64(index + 1),
			Timestamp: replaySpecTime(index),
			Done:      true,
			Choices: []model.Choice{{
				Index:   0,
				Message: spec.message,
			}},
		},
		RequestID:          fmt.Sprintf("%s-request-%d", caseName, index),
		InvocationID:       invocationID,
		ParentInvocationID: spec.parentInvocationID,
		ParentMetadata:     spec.parentMetadata,
		Author:             author,
		ID:                 fmt.Sprintf("%s-event-%d", caseName, index),
		Timestamp:          replaySpecTime(index),
		Branch:             spec.branch,
		Tag:                spec.tag,
		StateDelta:         cloneReplayStateMap(spec.stateDelta),
		Actions:            cloneReplayEventActions(spec.actions),
		Version:            event.CurrentVersion,
	}
	if spec.filterKey != "" {
		evt.FilterKey = spec.filterKey
	} else {
		evt.FilterKey = spec.branch
	}
	for key, value := range spec.extensions {
		if err := event.SetExtension(evt, key, value); err != nil {
			panic(fmt.Sprintf("set replay extension %s: %v", key, err))
		}
	}
	return evt
}

func replaySpecTime(index int) time.Time {
	return replayBaseTime.Add(time.Duration(index) * time.Second)
}

func cloneReplayEventActions(actions *event.EventActions) *event.EventActions {
	if actions == nil {
		return nil
	}
	return &event.EventActions{
		SkipSummarization: actions.SkipSummarization,
	}
}

func compareReplayCaseResults(tc replayCase, results []replayCaseResult) []diffEntry {
	converted := make([]replaytest.Result, 0, len(results))
	for _, result := range results {
		converted = append(converted, replaytest.Result{
			Backend: result.backend, Key: result.key, Snapshot: result.snapshot,
		})
	}
	return replaytest.Compare(toReplayTestCase(tc), converted)
}

func hasReplayUnallowedDiffs(entries []diffEntry) bool {
	return replaytest.HasUnallowedDiffs(entries)
}

func replayUserEvent(content string, opts ...func(*eventSpec)) eventSpec {
	spec := eventSpec{
		author:  "user",
		message: model.NewUserMessage(content),
	}
	for _, opt := range opts {
		opt(&spec)
	}
	return spec
}

func replayAssistantEvent(content string, opts ...func(*eventSpec)) eventSpec {
	spec := eventSpec{
		author:  "agent",
		message: model.NewAssistantMessage(content),
	}
	for _, opt := range opts {
		opt(&spec)
	}
	return spec
}

func replayToolResultEvent(toolID string, toolName string, content string, opts ...func(*eventSpec)) eventSpec {
	spec := eventSpec{
		author:  "tool",
		object:  model.ObjectTypeToolResponse,
		message: model.NewToolMessage(toolID, toolName, content),
	}
	for _, opt := range opts {
		opt(&spec)
	}
	return spec
}

func withReplayBranch(branch string) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.branch = branch
		spec.filterKey = branch
	}
}

func withReplayInvocation(id string) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.invocationID = id
	}
}

func withReplayParent(parentID string, metadata *event.ParentInvocationMetadata) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.parentInvocationID = parentID
		spec.parentMetadata = metadata
	}
}

func withReplayTag(tag string) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.tag = tag
	}
}

func withReplayStateDelta(state session.StateMap) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.stateDelta = state
	}
}

func withReplayExtensions(extensions map[string]any) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.extensions = extensions
	}
}

func withReplayActions(actions *event.EventActions) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.actions = actions
	}
}

func replaySummary(filterKey, text string, opts ...func(*summaryStep)) summaryStep {
	spec := summaryStep{
		filterKey: filterKey,
		force:     true,
		text:      text,
	}
	for _, opt := range opts {
		opt(&spec)
	}
	if spec.name == "" {
		if spec.filterKey == session.SummaryFilterKeyAllContents {
			spec.name = "full_summary"
		} else {
			spec.name = "summary_" + spec.filterKey
		}
	}
	return spec
}

func withReplaySummaryName(name string) func(*summaryStep) {
	return func(spec *summaryStep) {
		spec.name = name
	}
}

func withReplaySummaryForce(force bool) func(*summaryStep) {
	return func(spec *summaryStep) {
		spec.force = force
	}
}

func withReplaySummaryWantText(text string) func(*summaryStep) {
	return func(spec *summaryStep) {
		spec.wantText = text
	}
}

func withReplaySummaryEventPrefix(prefix int) func(*summaryStep) {
	return func(spec *summaryStep) {
		spec.eventPrefix = &prefix
	}
}

func replayTrack(name string, index int, payload any) trackSpec {
	return trackSpec{
		name:      name,
		payload:   payload,
		timestamp: replaySpecTime(100 + index),
	}
}

func replayTextPtr(value string) *string {
	return &value
}

func TestReplayConsistencyBackends_ExpectedLightweightMatrix(t *testing.T) {
	backends := makeReplayBackends(t)
	names := make([]string, 0, len(backends))
	for _, backend := range backends {
		names = append(names, backend.name)
	}
	require.Equal(t, []string{"in_memory", "sqlite"}, names)
}

func TestReplayConsistencySmoke_BackendsConstructUseAndClose(t *testing.T) {
	ctx := context.Background()
	backends := makeReplayBackends(t)

	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			key := session.Key{
				AppName:   "replay-smoke",
				UserID:    "user-1",
				SessionID: backend.name + "-session",
			}
			sess, err := backend.sessionService.CreateSession(
				ctx,
				key,
				session.StateMap{"stage": []byte("smoke")},
			)
			require.NoError(t, err)
			require.NotNil(t, sess)

			got, err := backend.sessionService.GetSession(ctx, key)
			require.NoError(t, err)
			require.NotNil(t, got)
			stage, ok := got.GetState("stage")
			require.True(t, ok)
			require.Equal(t, []byte("smoke"), stage)

			userKey := memory.UserKey{
				AppName: key.AppName,
				UserID:  key.UserID,
			}
			require.NoError(t, backend.memoryService.AddMemory(
				ctx,
				userKey,
				"smoke memory for replay consistency",
				nil,
			))
			memories, err := backend.memoryService.SearchMemories(ctx, userKey, "smoke")
			require.NoError(t, err)
			require.NotEmpty(t, memories)

			payload, err := json.Marshal(map[string]string{"status": "ok"})
			require.NoError(t, err)
			require.NoError(t, backend.trackService.AppendTrackEvent(
				ctx,
				sess,
				&session.TrackEvent{
					Track:     session.Track("smoke-track"),
					Payload:   payload,
					Timestamp: time.Now(),
				},
			))

			got, err = backend.sessionService.GetSession(ctx, key)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Contains(t, got.Tracks, session.Track("smoke-track"))
		})
	}
}

func TestReplayConsistencyMatrix_BasicCases(t *testing.T) {
	ctx := context.Background()
	backends := makeReplayBackends(t)
	cases := basicReplayCases()

	var allDiffs []diffEntry
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := make([]replayCaseResult, 0, len(backends))
			for _, backend := range backends {
				results = append(results, runReplayCaseOnBackend(t, ctx, backend, tc))
			}
			requireReplayCaseIsolation(t, tc, results)
			requireReplayCaseSemantics(t, tc, results)
			diffs := compareReplayCaseResults(tc, results)
			allDiffs = append(allDiffs, diffs...)
			require.Falsef(
				t,
				hasReplayUnallowedDiffs(diffs),
				"unexpected replay diffs for case %s: %+v",
				tc.name,
				diffs,
			)
		})
	}

	require.Falsef(t, hasReplayUnallowedDiffs(allDiffs), "unexpected replay diffs: %+v", allDiffs)
	require.NoError(t, writeReplayDiffReport("", allDiffs))
}

func TestReplayConsistencyMatrix_AllowsExplicitAllowedDiff(t *testing.T) {
	ctx := context.Background()
	reportPath := filepath.Join(t.TempDir(), "replay-allowed-matrix-report.json")
	t.Setenv("TRPC_AGENT_REPLAY_REPORT_PATH", reportPath)
	const reason = "known matrix fixture state drift"

	tc := replayCaseByName(t, "single_turn")
	tc.allowedDiffs = []allowedDiffRule{{
		Section:  "state",
		Path:     "$.state.allowed",
		BackendA: "in_memory",
		BackendB: "sqlite",
		Reason:   reason,
	}}
	diffs := runReplayCaseWithBackendInjection(
		t,
		ctx,
		tc,
		"sqlite",
		func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key) {
			require.NoError(t, backend.sessionService.UpdateSessionState(
				ctx,
				key,
				session.StateMap{"allowed": []byte(`{"n":1}`)},
			))
		},
	)
	require.NotEmpty(t, diffs)
	require.Falsef(t, hasReplayUnallowedDiffs(diffs), "unexpected replay diffs: %+v", diffs)
	diff := requireReplayDiff(t, diffs, "state", "$.state.allowed", nil)
	require.True(t, diff.Allowed)
	require.Equal(t, reason, diff.Reason)

	require.NoError(t, writeReplayDiffReport("", diffs))
	report := requireReplayReportFields(t, reportPath)
	require.Len(t, report, len(diffs))
	require.Equal(t, true, report[0]["allowed"])
	require.Equal(t, reason, report[0]["reason"])
}

func requireReplayCaseIsolation(
	t *testing.T,
	tc replayCase,
	results []replayCaseResult,
) {
	t.Helper()

	if tc.name == "state_scopes" {
		return
	}
	for _, result := range results {
		require.NotContains(
			t,
			result.snapshot.State,
			session.StateAppPrefix+"feature_flags",
			"case %s reused app state from state_scopes in backend %s",
			tc.name,
			result.backend,
		)
	}
}

func requireReplayCaseSemantics(
	t *testing.T,
	tc replayCase,
	results []replayCaseResult,
) {
	t.Helper()

	switch tc.name {
	case "memory_same_content_identity":
		require.NotEmpty(t, tc.memories)
		want := tc.memories[0]
		require.NotNil(t, want.metadata)
		require.NotNil(t, want.metadata.EventTime)
		for _, result := range results {
			require.Lenf(t, result.snapshot.Memory, 1, "backend=%s", result.backend)
			got := result.snapshot.Memory[0]
			require.Equal(t, want.content, got.Content, "backend=%s", result.backend)
			require.Equal(t, want.topics, got.Topics, "backend=%s", result.backend)
			require.Equal(t, string(want.metadata.Kind), got.Kind, "backend=%s", result.backend)
			require.Equal(t, normalizeReplayTime(*want.metadata.EventTime), got.EventTime, "backend=%s", result.backend)
			require.Equal(t, want.metadata.Participants, got.Participants, "backend=%s", result.backend)
			require.Equal(t, want.metadata.Location, got.Location, "backend=%s", result.backend)
		}
	case "summary_overwrite_boundary":
		for _, result := range results {
			got, ok := result.snapshot.Summary[session.SummaryFilterKeyAllContents]
			require.Truef(t, ok, "backend=%s", result.backend)
			require.Equal(t, "updated full summary", got.Summary, "backend=%s", result.backend)
			require.NotNil(t, got.Boundary, "backend=%s", result.backend)
			require.NotNil(t, got.Boundary.LastEventIndex, "backend=%s", result.backend)
			require.Equal(t, 3, *got.Boundary.LastEventIndex, "backend=%s", result.backend)
			require.Equal(t, normalizeReplayTime(replaySpecTime(3)), got.Boundary.CutoffAt, "backend=%s", result.backend)
		}
	case "track_events":
		wantPayloads := []trackPayloadSnapshot{
			{Kind: "json", Value: []any{"array", json.Number("2")}},
			{Kind: "json", Value: "scalar"},
			{Kind: "json", Value: json.Number("9007199254740993")},
			{Kind: "json", Value: true},
			{Kind: "json"},
		}
		for _, result := range results {
			var payloadTrack *trackSnapshot
			for i := range result.snapshot.Tracks {
				track := &result.snapshot.Tracks[i]
				require.Equal(t, track.Name, track.Track, "backend=%s", result.backend)
				for _, evt := range track.Events {
					require.Equal(t, track.Name, evt.Track, "backend=%s", result.backend)
					require.NotEmpty(t, evt.Payload.Kind, "backend=%s", result.backend)
				}
				if track.Name == "payload.domain" {
					payloadTrack = track
				}
			}
			require.NotNil(t, payloadTrack, "backend=%s", result.backend)
			gotPayloads := make([]trackPayloadSnapshot, 0, len(payloadTrack.Events))
			for _, evt := range payloadTrack.Events {
				gotPayloads = append(gotPayloads, evt.Payload)
			}
			require.Equal(t, wantPayloads, gotPayloads, "backend=%s", result.backend)
		}
	}
}

func basicReplayCases() []replayCase {
	episodeTime := time.Date(2026, 7, 1, 3, 0, 0, 0, time.UTC)
	secondEpisodeTime := episodeTime.Add(time.Hour)
	toolCallIndex := 0
	return []replayCase{
		{
			name: "single_turn",
			initialState: session.StateMap{
				"seed": []byte(`{"case":"single_turn"}`),
			},
			events: []eventSpec{
				replayUserEvent(
					"hello replay",
					withReplayInvocation("single-root"),
					withReplayBranch("root"),
					withReplayStateDelta(session.StateMap{
						"turn": []byte(`{"index":1}`),
					}),
				),
				replayAssistantEvent(
					"hello from agent",
					withReplayInvocation("single-root"),
					withReplayBranch("root"),
				),
			},
		},
		{
			name: "multi_turn",
			events: []eventSpec{
				replayUserEvent("turn one", withReplayInvocation("multi-root"), withReplayBranch("root")),
				replayAssistantEvent("answer one", withReplayInvocation("multi-root"), withReplayBranch("root")),
				replayUserEvent("turn two", withReplayInvocation("multi-root"), withReplayBranch("root")),
				replayAssistantEvent("answer two", withReplayInvocation("multi-root"), withReplayBranch("root")),
			},
		},
		{
			name: "tool_call_response_extensions",
			events: []eventSpec{
				replayUserEvent("weather in Shenzhen?", withReplayInvocation("tool-root"), withReplayBranch("root")),
				{
					invocationID: "tool-root",
					author:       "agent",
					message: model.Message{
						Role:    model.RoleAssistant,
						Content: "checking weather",
						ToolCalls: []model.ToolCall{{
							Type:  "function",
							ID:    "call-weather",
							Index: &toolCallIndex,
							Function: model.FunctionDefinitionParam{
								Name:      "lookup_weather",
								Arguments: []byte(`{"city":"Shenzhen","unit":"celsius"}`),
							},
						}},
					},
					branch:    "root/tools/weather",
					filterKey: "root/tools/weather",
					tag:       "tool_call",
					extensions: map[string]any{
						event.ToolCallArgsExtensionKey: map[string]any{
							"call-weather": map[string]any{
								"city": "Shenzhen",
								"unit": "celsius",
							},
						},
					},
				},
				replayToolResultEvent(
					"call-weather",
					"lookup_weather",
					`{"city":"Shenzhen","temperature":29}`,
					withReplayInvocation("tool-root"),
					withReplayBranch("root/tools/weather"),
					withReplayTag("tool_result"),
					withReplayActions(&event.EventActions{SkipSummarization: true}),
				),
				replayAssistantEvent(
					"Shenzhen is 29C.",
					withReplayInvocation("tool-root"),
					withReplayBranch("root"),
				),
			},
		},
		{
			name: "state_scopes",
			initialState: session.StateMap{
				"session:init": []byte(`{"ready":true}`),
			},
			appState: session.StateMap{
				session.StateAppPrefix + "feature_flags": []byte(`{"replay":true}`),
			},
			userState: session.StateMap{
				session.StateUserPrefix + "locale": []byte(`"zh-CN"`),
			},
			sessionState: session.StateMap{
				session.StateTempPrefix + "scratch": []byte("working"),
				"session:mode":                      []byte(`{"name":"matrix"}`),
			},
			events: []eventSpec{
				replayUserEvent(
					"please use scoped state",
					withReplayInvocation("state-root"),
					withReplayBranch("root/state"),
					withReplayStateDelta(session.StateMap{
						"session:last_user_intent": []byte(`{"intent":"state"}`),
					}),
				),
				replayAssistantEvent(
					"scoped state applied",
					withReplayInvocation("state-root"),
					withReplayBranch("root/state"),
				),
			},
		},
		{
			name: "memory_add_update_search",
			memories: []memoryOpSpec{
				{
					name:        "add preference",
					op:          "add",
					content:     "User likes jasmine tea.",
					topics:      []string{"drink", "preference"},
					resultAlias: "preference",
				},
				{
					name:        "update preference",
					op:          "update",
					ref:         "preference",
					content:     "User likes jasmine tea in the afternoon.",
					topics:      []string{"drink", "preference", "schedule"},
					resultAlias: "preference",
				},
				{
					name:        "add episode",
					op:          "add",
					content:     "User visited Shenzhen library with Ada.",
					topics:      []string{"travel", "library"},
					metadata:    replayMemoryMetadata(memory.KindEpisode, &episodeTime, []string{"User", "Ada"}, "Shenzhen library"),
					resultAlias: "episode",
				},
			},
			queries: []memoryQuerySpec{
				{
					query: "jasmine tea afternoon",
					expectedContents: []string{
						"User likes jasmine tea in the afternoon.",
					},
				},
				{
					query: "Shenzhen library Ada",
					expectedContents: []string{
						"User visited Shenzhen library with Ada.",
					},
				},
			},
		},
		{
			name: "memory_same_content_identity",
			memories: []memoryOpSpec{
				{
					name: "add first review episode", op: "add", resultAlias: "first_review",
					content: "User attended the quarterly project review.",
					topics:  []string{"episode", "review"},
					metadata: replayMemoryMetadata(
						memory.KindEpisode, &episodeTime, []string{"Ada", "User"}, "Shenzhen",
					),
				},
				{
					name: "add second review episode", op: "add", resultAlias: "second_review",
					content: "User attended the quarterly project review.",
					topics:  []string{"episode", "review"},
					metadata: replayMemoryMetadata(
						memory.KindEpisode, &secondEpisodeTime, []string{"Bob", "User"}, "Guangzhou",
					),
				},
				{
					name: "update second review episode", op: "update", ref: "second_review",
					content: "User attended the follow-up quarterly project review.",
					topics:  []string{"episode", "follow-up", "review"},
				},
				{name: "delete second review episode", op: "delete", ref: "second_review"},
			},
		},
		{
			name: "concurrent_writes",
			events: []eventSpec{
				replayUserEvent(
					"run concurrent memory writes",
					withReplayInvocation("concurrent-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"concurrent writes completed",
					withReplayInvocation("concurrent-root"),
					withReplayBranch("root"),
				),
			},
			concurrentMemories: []memoryOpSpec{
				{
					name:    "concurrent preference",
					op:      "add",
					content: "Concurrent write records preferred response style.",
					topics:  []string{"concurrency", "preference"},
				},
				{
					name:    "concurrent fact",
					op:      "add",
					content: "Concurrent write records project fact.",
					topics:  []string{"concurrency", "fact"},
				},
				{
					name:    "repeated note branch A",
					op:      "add",
					content: "Concurrent write records repeated project note from branch A.",
					topics:  []string{"concurrency", "project-note", "branch-a"},
				},
				{
					name:    "repeated note branch B",
					op:      "add",
					content: "Concurrent write records repeated project note from branch B.",
					topics:  []string{"concurrency", "project-note", "branch-b"},
				},
			},
			queries: []memoryQuerySpec{
				{
					query: "repeated note from branch",
					expectedContents: []string{
						"Concurrent write records repeated project note from branch A.",
						"Concurrent write records repeated project note from branch B.",
					},
				},
			},
		},
		{
			name: "interleaved_child_invocation_branch_order",
			events: []eventSpec{
				replayUserEvent("compare two subtasks", withReplayInvocation("parent"), withReplayBranch("root")),
				replayAssistantEvent(
					"starting two branches",
					withReplayInvocation("parent"),
					withReplayBranch("root"),
					withReplayExtensions(map[string]any{
						"parallel_tool_calls": []string{"call-child-a", "call-child-b"},
					}),
				),
				replayAssistantEvent(
					"child A partial",
					withReplayInvocation("child-a"),
					withReplayParent("parent", &event.ParentInvocationMetadata{
						TriggerType: event.TriggerTypeToolCall,
						TriggerID:   "call-child-a",
						TriggerName: "delegate_child",
					}),
					withReplayBranch("root/child-a"),
				),
				replayAssistantEvent(
					"child B partial",
					withReplayInvocation("child-b"),
					withReplayParent("parent", &event.ParentInvocationMetadata{
						TriggerType: event.TriggerTypeToolCall,
						TriggerID:   "call-child-b",
						TriggerName: "delegate_child",
					}),
					withReplayBranch("root/child-b"),
				),
				replayAssistantEvent(
					"child A done",
					withReplayInvocation("child-a"),
					withReplayParent("parent", &event.ParentInvocationMetadata{
						TriggerType: event.TriggerTypeToolCall,
						TriggerID:   "call-child-a",
						TriggerName: "delegate_child",
					}),
					withReplayBranch("root/child-a"),
				),
				replayAssistantEvent(
					"merged result",
					withReplayInvocation("parent"),
					withReplayBranch("root"),
				),
			},
		},
		{
			name: "full_summary",
			events: []eventSpec{
				replayUserEvent(
					"summarize this session",
					withReplayInvocation("summary-full-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"summary source answer",
					withReplayInvocation("summary-full-root"),
					withReplayBranch("root"),
				),
			},
			summaries: []summaryStep{
				replaySummary(
					session.SummaryFilterKeyAllContents,
					"full summary for replay",
					withReplaySummaryName("full session summary"),
				),
			},
		},
		{
			name: "filter_key_summary",
			events: []eventSpec{
				replayUserEvent(
					"check weather and calendar",
					withReplayInvocation("filter-summary-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"weather branch started",
					withReplayInvocation("filter-summary-root"),
					withReplayBranch("root/tools/weather"),
					withReplayTag("weather_branch"),
				),
				replayToolResultEvent(
					"call-weather-filter",
					"lookup_weather",
					`{"city":"Shenzhen","temperature":30}`,
					withReplayInvocation("filter-summary-root"),
					withReplayBranch("root/tools/weather"),
					withReplayTag("weather_result"),
				),
				replayAssistantEvent(
					"calendar branch started",
					withReplayInvocation("filter-summary-root"),
					withReplayBranch("root/tools/calendar"),
					withReplayTag("calendar_branch"),
				),
			},
			summaries: []summaryStep{
				replaySummary(
					"root/tools/weather",
					"weather branch summary",
					withReplaySummaryName("weather filter summary"),
				),
			},
		},
		{
			name: "summary_overwrite_boundary",
			events: []eventSpec{
				replayUserEvent(
					"first summary source",
					withReplayInvocation("summary-overwrite-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"first source answer",
					withReplayInvocation("summary-overwrite-root"),
					withReplayBranch("root"),
				),
				replayUserEvent(
					"second summary source",
					withReplayInvocation("summary-overwrite-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"second source answer",
					withReplayInvocation("summary-overwrite-root"),
					withReplayBranch("root"),
				),
			},
			summaries: []summaryStep{
				replaySummary(
					session.SummaryFilterKeyAllContents,
					"first full summary",
					withReplaySummaryName("first full summary"),
					withReplaySummaryEventPrefix(2),
				),
				replaySummary(
					session.SummaryFilterKeyAllContents,
					"updated full summary",
					withReplaySummaryName("updated full summary"),
					withReplaySummaryEventPrefix(4),
				),
			},
		},
		{
			name: "track_events",
			events: []eventSpec{
				replayUserEvent(
					"run the weather tool",
					withReplayInvocation("track-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"weather tool completed",
					withReplayInvocation("track-root"),
					withReplayBranch("root"),
				),
			},
			tracks: []trackSpec{
				replayTrack("tool.latency", 0, map[string]any{
					"tool":       "weather",
					"latency_ms": float64(42),
					"status":     "started",
				}),
				replayTrack("tool.latency", 1, map[string]any{
					"tool":       "weather",
					"latency_ms": float64(87),
					"status":     "finished",
				}),
				replayTrack("task.status", 2, map[string]any{
					"task":  "child-a",
					"state": "done",
					"error": nil,
				}),
				replayTrack("payload.domain", 3, []any{"array", json.Number("2")}),
				replayTrack("payload.domain", 4, "scalar"),
				replayTrack("payload.domain", 5, json.Number("9007199254740993")),
				replayTrack("payload.domain", 6, true),
				replayTrack("payload.domain", 7, nil),
			},
		},
	}
}

func replayMemoryMetadata(
	kind memory.Kind,
	eventTime *time.Time,
	participants []string,
	location string,
) *memory.Metadata {
	return &memory.Metadata{
		Kind:         kind,
		EventTime:    eventTime,
		Participants: append([]string(nil), participants...),
		Location:     location,
	}
}

func TestReplayConsistencySnapshotNormalize_IgnoresGeneratedFields(t *testing.T) {
	left := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
	right := newReplaySnapshotFixture("right", `{"b":2,"a":1}`, `{"b":2,"a":1}`, "raw-right")

	diffs := diffReplaySnapshots(
		"normalize-generated-fields",
		left.Session.ID,
		"in_memory",
		"sqlite",
		left,
		right,
		nil,
	)
	require.Empty(t, diffs)
}

func TestReplayConsistencySnapshotNormalize_UsesSummaryLastEventAnchor(t *testing.T) {
	t.Run("same semantic anchor ignores raw ids", func(t *testing.T) {
		left := newReplaySnapshotFixtureWithSummaryAnchor(
			"left",
			`{"a":1,"b":2}`,
			`{"a":1,"b":2}`,
			"raw-left",
			"event-left",
			"event-left",
		)
		right := newReplaySnapshotFixtureWithSummaryAnchor(
			"left",
			`{"a":1,"b":2}`,
			`{"a":1,"b":2}`,
			"raw-left",
			"event-right",
			"event-right",
		)

		diffs := diffReplaySnapshots(
			"normalize-summary-last-event-anchor",
			left.Session.ID,
			"in_memory",
			"sqlite",
			left,
			right,
			nil,
		)
		require.Empty(t, diffs)
	})

	t.Run("missing anchor differs from present anchor", func(t *testing.T) {
		left := newReplaySnapshotFixtureWithSummaryAnchor(
			"left",
			`{"a":1,"b":2}`,
			`{"a":1,"b":2}`,
			"raw-left",
			"event-left",
			"event-left",
		)
		right := newReplaySnapshotFixtureWithSummaryAnchor(
			"left",
			`{"a":1,"b":2}`,
			`{"a":1,"b":2}`,
			"raw-left",
			"event-right",
			"",
		)

		diffs := diffReplaySnapshots(
			"summary-anchor-missing",
			left.Session.ID,
			"in_memory",
			"sqlite",
			left,
			right,
			nil,
		)
		requireSummaryLastEventIndexDiff(t, diffs, json.Number("0"), replayMissingValue())
	})

	t.Run("unmatched anchor uses sentinel", func(t *testing.T) {
		left := newReplaySnapshotFixtureWithSummaryAnchor(
			"left",
			`{"a":1,"b":2}`,
			`{"a":1,"b":2}`,
			"raw-left",
			"event-left",
			"event-left",
		)
		right := newReplaySnapshotFixtureWithSummaryAnchor(
			"left",
			`{"a":1,"b":2}`,
			`{"a":1,"b":2}`,
			"raw-left",
			"event-right",
			"event-not-in-snapshot",
		)

		diffs := diffReplaySnapshots(
			"summary-anchor-unmatched",
			left.Session.ID,
			"in_memory",
			"sqlite",
			left,
			right,
			nil,
		)
		requireSummaryLastEventIndexDiff(t, diffs, json.Number("0"), json.Number("-1"))
	})
}

func TestReplayConsistencySnapshotNormalize_PreservesLargeJSONNumbers(t *testing.T) {
	const (
		leftBig  = "9007199254740992"
		rightBig = "9007199254740993"
	)
	left := newReplaySnapshotFixture(
		"left",
		`{"big":`+leftBig+`}`,
		`{"big":`+leftBig+`}`,
		"raw-left",
	)
	right := newReplaySnapshotFixture(
		"left",
		`{"big":`+rightBig+`}`,
		`{"big":`+rightBig+`}`,
		"raw-left",
	)

	diffs := diffReplaySnapshots(
		"large-json-number",
		left.Session.ID,
		"in_memory",
		"sqlite",
		left,
		right,
		nil,
	)
	require.NotEmpty(t, diffs)

	eventExtensionDiff := requireReplayDiff(
		t,
		diffs,
		"events",
		"$.events[0].extensions.fixture.big",
		map[string]any{"event_index": 0},
	)
	require.Equal(t, json.Number(leftBig), eventExtensionDiff.Left)
	require.Equal(t, json.Number(rightBig), eventExtensionDiff.Right)

	eventStateDiff := requireReplayDiff(
		t,
		diffs,
		"events",
		"$.events[0].stateDelta.json.value.big",
		map[string]any{"event_index": 0},
	)
	require.Equal(t, json.Number(leftBig), eventStateDiff.Left)
	require.Equal(t, json.Number(rightBig), eventStateDiff.Right)

	stateDiff := requireReplayDiff(t, diffs, "state", "$.state.json.value.big", nil)
	require.Equal(t, json.Number(leftBig), stateDiff.Left)
	require.Equal(t, json.Number(rightBig), stateDiff.Right)

	trackDiff := requireReplayDiff(
		t,
		diffs,
		"tracks",
		"$.tracks[0].events[0].payload.value.big",
		map[string]any{
			"track_name":        "tool",
			"track_event_index": 0,
		},
	)
	require.Equal(t, json.Number(leftBig), trackDiff.Left)
	require.Equal(t, json.Number(rightBig), trackDiff.Right)
}

func TestReplayConsistencySnapshotNormalize_PreservesStateByteDistinctions(t *testing.T) {
	tests := []struct {
		name      string
		left      []byte
		right     []byte
		wantLeft  string
		wantRight string
	}{
		{
			name:      "raw utf8 string versus json string",
			left:      []byte("hello"),
			right:     []byte(`"hello"`),
			wantLeft:  "utf8",
			wantRight: "json",
		},
		{
			name:      "nil versus json null",
			left:      nil,
			right:     []byte("null"),
			wantLeft:  "nil",
			wantRight: "json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := replaySnapshot{
				Session: replaySessionSnapshot{ID: "session-1", App: "replay-app", UserID: "user-1"},
				State:   normalizeReplayState(session.StateMap{"value": tt.left}),
				Memory:  []replayMemorySnapshot{},
				Summary: map[string]summaryEntry{},
				Tracks:  []trackSnapshot{},
			}
			right := replaySnapshot{
				Session: replaySessionSnapshot{ID: "session-1", App: "replay-app", UserID: "user-1"},
				State:   normalizeReplayState(session.StateMap{"value": tt.right}),
				Memory:  []replayMemorySnapshot{},
				Summary: map[string]summaryEntry{},
				Tracks:  []trackSnapshot{},
			}

			diffs := diffReplaySnapshots(
				tt.name,
				left.Session.ID,
				"in_memory",
				"sqlite",
				left,
				right,
				nil,
			)
			diff := requireReplayDiff(t, diffs, "state", "$.state.value.kind", nil)
			require.Equal(t, tt.wantLeft, diff.Left)
			require.Equal(t, tt.wantRight, diff.Right)
		})
	}
}

func TestReplayConsistencySnapshotDiff_MutationsHavePrecisePaths(t *testing.T) {
	tests := []struct {
		name            string
		section         string
		path            string
		mutate          func(*replaySnapshot)
		expectedContext map[string]any
	}{
		{
			name:    "event author",
			section: "events",
			path:    "$.events[0].author",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Events[0]["author"] = "assistant"
			},
			expectedContext: map[string]any{"event_index": 0},
		},
		{
			name:    "state json field",
			section: "state",
			path:    "$.state.json.value.a",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.State["json"] = replayStateBytesSnapshot{
					Kind: "json",
					Value: map[string]any{
						"a": json.Number("2"),
						"b": json.Number("2"),
					},
				}
			},
		},
		{
			name:    "memory content",
			section: "memory",
			path:    "$.memory[0].content",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Memory[0].Content = "likes coffee"
			},
			expectedContext: map[string]any{
				"left_memory_id":  "raw-left",
				"right_memory_id": "raw-right",
			},
		},
		{
			name:    "summary text",
			section: "summary",
			path:    `$.summary["branch/a"].summary`,
			mutate: func(snapshot *replaySnapshot) {
				entry := snapshot.Summary["branch/a"]
				entry.Summary = "changed summary"
				snapshot.Summary["branch/a"] = entry
			},
			expectedContext: map[string]any{"summary_filter_key": "branch/a"},
		},
		{
			name:    "track payload",
			section: "tracks",
			path:    "$.tracks[0].events[0].payload.value.a",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Tracks[0].Events[0].Payload = trackPayloadSnapshot{
					Kind: "json",
					Value: map[string]any{
						"a": json.Number("2"),
						"b": json.Number("2"),
					},
				}
			},
			expectedContext: map[string]any{
				"track_name":        "tool",
				"track_event_index": 0,
			},
		},
		{
			name:    "track embedded field",
			section: "tracks",
			path:    "$.tracks[0].events[0].track",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Tracks[0].Events[0].Track = "wrong-track"
			},
			expectedContext: map[string]any{
				"track_name":        "tool",
				"track_event_index": 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
			right := newReplaySnapshotFixture("right", `{"b":2,"a":1}`, `{"b":2,"a":1}`, "raw-right")
			tt.mutate(&right)

			diffs := diffReplaySnapshots(
				"mutation",
				left.Session.ID,
				"in_memory",
				"sqlite",
				left,
				right,
				nil,
			)
			require.Len(t, diffs, 1)
			require.Equal(t, tt.section, diffs[0].Section)
			require.Equal(t, tt.path, diffs[0].Path)
			require.False(t, diffs[0].Allowed)
			for key, value := range tt.expectedContext {
				require.Equal(t, value, diffs[0].Context[key])
			}
		})
	}
}

func TestReplayConsistencyAnomaly_SnapshotMutations(t *testing.T) {
	tests := []struct {
		name     string
		section  string
		pathGlob string
		mutate   func(*replaySnapshot)
		context  map[string]any
	}{
		{
			name:     "partial_event_loss",
			section:  "events",
			pathGlob: "$.events[0]*",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Events = []replayEventSnapshot{}
			},
			context: map[string]any{"event_index": 0},
		},
		{
			name:     "event_timestamp_drift",
			section:  "events",
			pathGlob: "$.events[0].timestamp",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Events[0]["timestamp"] = normalizeReplayTime(
					time.Date(2026, time.July, 1, 1, 3, 4, 4, time.UTC),
				)
			},
			context: map[string]any{"event_index": 0},
		},
		{
			name:     "summary_loss",
			section:  "summary",
			pathGlob: `$.summary["branch/a"]*`,
			mutate: func(snapshot *replaySnapshot) {
				delete(snapshot.Summary, "branch/a")
			},
			context: map[string]any{"summary_filter_key": "branch/a"},
		},
		{
			name:     "wrong_session_attribution",
			section:  "session",
			pathGlob: "$.session.id",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Session.ID = "wrong-session"
			},
		},
		{
			name:     "wrong_summary_filter_key_missing",
			section:  "summary",
			pathGlob: `$.summary["branch/a"]*`,
			mutate: func(snapshot *replaySnapshot) {
				entry := snapshot.Summary["branch/a"]
				delete(snapshot.Summary, "branch/a")
				snapshot.Summary["branch/wrong"] = entry
			},
			context: map[string]any{"summary_filter_key": "branch/a"},
		},
		{
			name:     "wrong_summary_filter_key_extra",
			section:  "summary",
			pathGlob: `$.summary["branch/wrong"]*`,
			mutate: func(snapshot *replaySnapshot) {
				entry := snapshot.Summary["branch/a"]
				delete(snapshot.Summary, "branch/a")
				snapshot.Summary["branch/wrong"] = entry
			},
			context: map[string]any{"summary_filter_key": "branch/wrong"},
		},
		{
			name:     "track_payload_drift",
			section:  "tracks",
			pathGlob: "$.tracks[0].events[0].payload.value.a",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Tracks[0].Events[0].Payload = trackPayloadSnapshot{
					Kind: "json",
					Value: map[string]any{
						"a": json.Number("99"),
						"b": json.Number("2"),
					},
				}
			},
			context: map[string]any{
				"track_name":        "tool",
				"track_event_index": 0,
			},
		},
		{
			name:     "track_order_drift",
			section:  "tracks",
			pathGlob: "$.tracks[0].events[0]*",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Tracks[0].Events = append(snapshot.Tracks[0].Events, trackEventSnapshot{
					Track: "tool",
					Payload: trackPayloadSnapshot{
						Kind: "json",
						Value: map[string]any{
							"a": json.Number("3"),
							"b": json.Number("4"),
						},
					},
					Timestamp: normalizeReplayTime(replayBaseTime.Add(10 * time.Second)),
				})
				snapshot.Tracks[0].Events[0], snapshot.Tracks[0].Events[1] =
					snapshot.Tracks[0].Events[1], snapshot.Tracks[0].Events[0]
			},
			context: map[string]any{
				"track_name":        "tool",
				"track_event_index": 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
			right := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
			tt.mutate(&right)

			diffs := diffReplaySnapshots(
				tt.name,
				left.Session.ID,
				"in_memory",
				"sqlite",
				left,
				right,
				nil,
			)
			require.NotEmpty(t, diffs)
			for _, diff := range diffs {
				require.False(t, diff.Allowed)
			}
			requireReplayDiff(t, diffs, tt.section, tt.pathGlob, tt.context)
		})
	}
}

func TestReplayConsistencyAnomaly_ServiceContractMutationsDetected(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		caseName string
		wrap     func(backendBundle) backendBundle
		section  string
		pathGlob string
		context  map[string]any
	}{
		{
			name:     "stale summary boundary",
			caseName: "summary_overwrite_boundary",
			wrap: func(backend backendBundle) backendBundle {
				backend.sessionService = &replayStaleSummaryBoundaryService{Service: backend.sessionService}
				return backend
			},
			section:  "summary",
			pathGlob: `$.summary[""].boundary.last_event_index`,
		},
		{
			name:     "wrong track container",
			caseName: "track_events",
			wrap: func(backend backendBundle) backendBundle {
				backend.sessionService = &replaySessionReadMutationService{
					Service: backend.sessionService,
					mutate: func(sess *session.Session) {
						sess.TracksMu.Lock()
						defer sess.TracksMu.Unlock()
						if events := sess.Tracks["tool.latency"]; events != nil {
							events.Track = "wrong-container"
						}
					},
				}
				return backend
			},
			section:  "tracks",
			pathGlob: "$.tracks[2].track",
			context:  map[string]any{"track_name": "tool.latency"},
		},
		{
			name:     "json null restored as nil payload",
			caseName: "track_events",
			wrap: func(backend backendBundle) backendBundle {
				backend.sessionService = &replaySessionReadMutationService{
					Service: backend.sessionService,
					mutate: func(sess *session.Session) {
						sess.TracksMu.Lock()
						defer sess.TracksMu.Unlock()
						if events := sess.Tracks["payload.domain"]; events != nil && len(events.Events) == 5 {
							events.Events[4].Payload = nil
						}
					},
				}
				return backend
			},
			section:  "tracks",
			pathGlob: "$.tracks[0].events[4].payload.kind",
			context: map[string]any{
				"track_name":        "payload.domain",
				"track_event_index": 4,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baselineBackends := makeReplayBackends(t)
			mutatedBackends := makeReplayBackends(t)
			tc := replayCaseByName(t, tt.caseName)
			for i, baselineBackend := range baselineBackends {
				mutatedBackend := tt.wrap(mutatedBackends[i])
				mutatedBackend.name += "_mutated"
				baseline := runReplayCaseOnBackend(t, ctx, baselineBackend, tc)
				mutated := runReplayCaseOnBackend(t, ctx, mutatedBackend, tc)
				diffs := replaytest.CompareSnapshots(
					tc.name,
					baseline.key.SessionID,
					baseline.backend,
					mutated.backend,
					baseline.snapshot,
					mutated.snapshot,
					nil,
				)
				require.NotEmpty(t, diffs, "backend=%s", baseline.backend)
				for _, diff := range diffs {
					require.False(t, diff.Allowed, "backend=%s diff=%+v", baseline.backend, diff)
				}
				requireReplayDiff(t, diffs, tt.section, tt.pathGlob, tt.context)
			}
		})
	}
}

func TestReplayConsistencyReport_AllowedDiffAndEnvPath(t *testing.T) {
	left := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
	right := newReplaySnapshotFixture("right", `{"b":2,"a":1}`, `{"b":2,"a":1}`, "raw-right")
	right.Memory[0].Content = "likes coffee"
	reportPath := filepath.Join(t.TempDir(), "replay-report.json")
	t.Setenv("TRPC_AGENT_REPLAY_REPORT_PATH", reportPath)

	diffs := diffReplaySnapshots(
		"allowed-memory",
		left.Session.ID,
		"in_memory",
		"sqlite",
		left,
		right,
		[]allowedDiffRule{{
			Section:  "memory",
			Path:     "$.memory[0].content",
			BackendA: "sqlite",
			BackendB: "in_memory",
			Reason:   "known memory text drift",
		}},
	)
	require.Len(t, diffs, 1)
	require.True(t, diffs[0].Allowed)
	require.Equal(t, "known memory text drift", diffs[0].Reason)
	require.Equal(t, reportPath, replayDiffReportPath())
	require.NoError(t, writeReplayDiffReport("", diffs))
	requireReplayReportFields(t, reportPath)
}

func requireReplayRetryStats(
	t *testing.T,
	stats replayFailStats,
	boundary replayFailBoundary,
) {
	t.Helper()
	require.Equal(t, 1, stats.triggered)
	require.Equal(t, 1, stats.injectedErrors)
	require.Equal(t, 1, stats.retries)
	wantUnderlyingCalls := 1
	if boundary == replayFailAfterWrite {
		wantUnderlyingCalls = 2
	}
	require.Equal(t, wantUnderlyingCalls, stats.targetUnderlyingCalls)
}

func requireReplayIdempotentSection(
	t *testing.T,
	operation replayFailOperation,
	baseline replaySnapshot,
	retry replaySnapshot,
) {
	t.Helper()
	switch operation {
	case replayFailAddMemory:
		require.Len(t, retry.Memory, len(baseline.Memory))
		for i := range baseline.Memory {
			want := baseline.Memory[i]
			got := retry.Memory[i]
			want.RawID = ""
			got.RawID = ""
			require.Equalf(t, want, got, "memory entry %d differs after retry", i)
		}
	case replayFailUpdateSessionState:
		require.Equal(t, baseline.State, retry.State)
	case replayFailCreateSummary:
		require.Len(t, retry.Summary, len(baseline.Summary))
		require.Equal(t, baseline.Summary, retry.Summary)
	default:
		require.Failf(t, "unsupported idempotency assertion", "operation=%s", operation)
	}
}

func TestReplayConsistencyRetry_FailBeforeWriteClean(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		caseName  string
		operation replayFailOperation
	}{
		{name: "event", caseName: "single_turn", operation: replayFailAppendEvent},
		{name: "state", caseName: "state_scopes", operation: replayFailUpdateSessionState},
		{name: "memory", caseName: "memory_add_update_search", operation: replayFailAddMemory},
		{name: "summary", caseName: "full_summary", operation: replayFailCreateSummary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comparisons := runReplayCaseWithRetry(t, ctx, replayCaseByName(t, tt.caseName), replayFailSpec{
				operation: tt.operation, boundary: replayFailBeforeWrite, occurrence: 0,
			})
			require.Len(t, comparisons, 2)
			for _, comparison := range comparisons {
				requireReplayRetryStats(t, comparison.stats, replayFailBeforeWrite)
				require.Emptyf(
					t,
					comparison.diffs,
					"backend %s retained data after before-write retry: %+v",
					comparison.backend,
					comparison.diffs,
				)
			}
		})
	}
}

func TestReplayConsistencyRetry_FailAfterWriteIdempotent(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		caseName  string
		operation replayFailOperation
	}{
		{name: "memory", caseName: "memory_add_update_search", operation: replayFailAddMemory},
		{name: "state", caseName: "state_scopes", operation: replayFailUpdateSessionState},
		{name: "summary", caseName: "full_summary", operation: replayFailCreateSummary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comparisons := runReplayCaseWithRetry(t, ctx, replayCaseByName(t, tt.caseName), replayFailSpec{
				operation: tt.operation, boundary: replayFailAfterWrite, occurrence: 0,
			})
			require.Len(t, comparisons, 2)
			for _, comparison := range comparisons {
				requireReplayRetryStats(t, comparison.stats, replayFailAfterWrite)
				requireReplayIdempotentSection(
					t,
					tt.operation,
					comparison.baseline.snapshot,
					comparison.retry.snapshot,
				)
				require.Emptyf(
					t,
					comparison.diffs,
					"backend %s is not idempotent for %s: %+v",
					comparison.backend,
					tt.operation,
					comparison.diffs,
				)
			}
		})
	}
}

func TestReplayConsistencyRetry_DuplicateEventFailAfterWriteDetected(t *testing.T) {
	ctx := context.Background()
	reportPath := filepath.Join(t.TempDir(), "replay-event-retry-report.json")
	comparisons := runReplayCaseWithRetry(t, ctx, replayCaseByName(t, "single_turn"), replayFailSpec{
		operation: replayFailAppendEvent, boundary: replayFailAfterWrite, occurrence: 0,
	})
	require.Len(t, comparisons, 2)
	var allDiffs []diffEntry
	for _, comparison := range comparisons {
		requireReplayRetryStats(t, comparison.stats, replayFailAfterWrite)
		require.Len(t, comparison.baseline.snapshot.Events, 2)
		require.Len(t, comparison.retry.snapshot.Events, 3)
		require.Equal(t, comparison.baseline.snapshot.Events[0], comparison.retry.snapshot.Events[0])
		require.Equal(t, comparison.retry.snapshot.Events[0], comparison.retry.snapshot.Events[1])
		require.Equal(t, comparison.baseline.snapshot.Events[1], comparison.retry.snapshot.Events[2])
		require.NotEmpty(t, comparison.diffs)
		for _, diff := range comparison.diffs {
			require.False(t, diff.Allowed)
		}
		requireReplayDiff(
			t,
			comparison.diffs,
			"events",
			"$.events[1]*",
			map[string]any{"event_index": 1},
		)
		requireReplayDiff(
			t,
			comparison.diffs,
			"events",
			"$.events[2]*",
			map[string]any{"event_index": 2},
		)
		allDiffs = append(allDiffs, comparison.diffs...)
	}
	require.NoError(t, writeReplayDiffReport(reportPath, allDiffs))
	requireReplayReportFields(t, reportPath)
}

func TestReplayConsistencyAnomaly_SQLitePublicAPIInjection(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		tc       replayCase
		inject   replayBackendInjection
		section  string
		pathGlob string
		context  map[string]any
	}{
		{
			name: "state_pollution",
			tc:   replayCaseByName(t, "single_turn"),
			inject: func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key) {
				require.NoError(t, backend.sessionService.UpdateSessionState(
					ctx,
					key,
					session.StateMap{"polluted": []byte(`{"backend":"sqlite"}`)},
				))
			},
			section:  "state",
			pathGlob: "$.state.polluted*",
		},
		{
			name: "memory_pollution",
			tc:   replayCaseByName(t, "memory_add_update_search"),
			inject: func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key) {
				require.NoError(t, backend.memoryService.AddMemory(
					ctx,
					memory.UserKey{AppName: key.AppName, UserID: key.UserID},
					"Injected SQLite-only memory.",
					[]string{"pollution"},
				))
			},
			section:  "memory",
			pathGlob: "$.memory[*]*",
		},
		{
			name: "summary_overwrite",
			tc:   replayCaseByName(t, "full_summary"),
			inject: func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key) {
				got, err := backend.sessionService.GetSession(ctx, key)
				require.NoError(t, err)
				require.NotNil(t, got)
				backend.summarizer.text = "sqlite overwritten summary"
				require.NoError(t, backend.sessionService.CreateSessionSummary(
					ctx,
					got,
					session.SummaryFilterKeyAllContents,
					true,
				))
			},
			section:  "summary",
			pathGlob: `$.summary[""].summary`,
			context:  map[string]any{"summary_filter_key": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reportPath := filepath.Join(t.TempDir(), "replay-injection-report.json")
			t.Setenv("TRPC_AGENT_REPLAY_REPORT_PATH", reportPath)

			diffs := runReplayCaseWithBackendInjection(t, ctx, tt.tc, "sqlite", tt.inject)
			require.NotEmpty(t, diffs)
			for _, diff := range diffs {
				require.False(t, diff.Allowed)
			}
			found := requireReplayDiff(t, diffs, tt.section, tt.pathGlob, tt.context)
			if tt.section == "memory" {
				require.NotNil(t, found.Context)
				require.Contains(t, found.Context, "memory_key")
			}

			require.NoError(t, writeReplayDiffReport("", diffs))
			requireReplayReportFields(t, reportPath)
		})
	}
}

func TestReplayConsistencyAnomaly_SQLiteStorageInjection(t *testing.T) {
	ctx := context.Background()
	reportPath := filepath.Join(t.TempDir(), "replay-storage-injection-report.json")
	t.Setenv("TRPC_AGENT_REPLAY_REPORT_PATH", reportPath)

	diffs := runReplayCaseWithBackendInjection(
		t,
		ctx,
		replayCaseByName(t, "concurrent_writes"),
		"sqlite",
		func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key) {
			injectSQLiteReplayMemoryRow(
				t,
				ctx,
				backend,
				key,
				"retry-duplicate-"+key.SessionID,
				"Concurrent write records repeated project note from branch A.",
				[]string{"concurrency", "project-note", "branch-a"},
			)
		},
	)
	require.NotEmpty(t, diffs)
	for _, diff := range diffs {
		require.False(t, diff.Allowed)
	}
	found := requireReplayDiff(t, diffs, "memory", "$.memory[*]*", nil)
	require.NotNil(t, found.Context)
	require.Contains(t, found.Context, "memory_key")

	require.NoError(t, writeReplayDiffReport("", diffs))
	requireReplayReportFields(t, reportPath)
}

func TestReplayConsistencyAllowedDiffRules_RequireExplicitMatch(t *testing.T) {
	left := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
	right := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
	right.Memory[0].Content = "likes coffee"

	tests := []struct {
		name    string
		rules   []allowedDiffRule
		allowed bool
		reason  string
	}{
		{name: "no rule"},
		{
			name: "missing reason",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[0].content",
				BackendA: "in_memory",
				BackendB: "sqlite",
			}},
		},
		{
			name: "section wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "*",
				Path:     "$.memory[0].content",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "path wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "*",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "double path wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "**",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "triple path wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "***",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "root path rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "root wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$*",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "root repeated wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$**",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "root child wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.*",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "root index wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$[*]",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "backend wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[0].content",
				BackendA: "*",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "section mismatch",
			rules: []allowedDiffRule{{
				Section:  "summary",
				Path:     "$.memory[0].content",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "wrong section",
			}},
		},
		{
			name: "path mismatch",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[0].topics",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "wrong path",
			}},
		},
		{
			name: "backend mismatch",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[0].content",
				BackendA: "sqlite",
				BackendB: "postgres",
				Reason:   "wrong backend",
			}},
		},
		{
			name: "valid path glob",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[*].content",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "known memory text drift",
			}},
			allowed: true,
			reason:  "known memory text drift",
		},
		{
			name: "valid reversed backend pair",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[0].content",
				BackendA: "sqlite",
				BackendB: "in_memory",
				Reason:   "known reverse pair",
			}},
			allowed: true,
			reason:  "known reverse pair",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diffs := diffReplaySnapshots(
				tt.name,
				left.Session.ID,
				"in_memory",
				"sqlite",
				left,
				right,
				tt.rules,
			)
			require.Len(t, diffs, 1)
			require.Equal(t, tt.allowed, diffs[0].Allowed)
			require.Equal(t, tt.reason, diffs[0].Reason)
		})
	}
}

func replayCaseByName(t *testing.T, name string) replayCase {
	t.Helper()

	for _, tc := range basicReplayCases() {
		if tc.name == name {
			return tc
		}
	}
	require.Failf(t, "missing replay case", "name=%s", name)
	return replayCase{}
}

func newReplaySnapshotFixture(
	generated string,
	stateJSON string,
	trackPayload string,
	rawMemoryID string,
) replaySnapshot {
	eventID := "event-" + generated
	return newReplaySnapshotFixtureWithSummaryAnchor(
		generated,
		stateJSON,
		trackPayload,
		rawMemoryID,
		eventID,
		eventID,
	)
}

func newReplaySnapshotFixtureWithSummaryAnchor(
	generated string,
	stateJSON string,
	trackPayload string,
	rawMemoryID string,
	eventID string,
	summaryLastEventID string,
) replaySnapshot {
	fixed := time.Date(2026, 7, 1, 1, 2, 3, 4, time.UTC)
	eventTime := fixed.Add(-2 * time.Hour)
	toolCallIndex := 0
	evt := event.Event{
		Response: &model.Response{
			ID:        "response-" + generated,
			Object:    model.ObjectTypeChatCompletion,
			Created:   int64(len(generated)),
			Timestamp: fixed.Add(time.Duration(len(generated)) * time.Second),
			Done:      true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "tool call response",
					ToolCalls: []model.ToolCall{{
						Type:  "function",
						ID:    "call-weather",
						Index: &toolCallIndex,
						Function: model.FunctionDefinitionParam{
							Name:      "lookup_weather",
							Arguments: []byte(`{"city":"shenzhen","unit":"c"}`),
						},
					}},
				},
			}},
		},
		RequestID:    "request-1",
		InvocationID: "invocation-1",
		Author:       "agent",
		ID:           eventID,
		Timestamp:    fixed.Add(time.Minute),
		Branch:       "branch/a",
		FilterKey:    "branch/a",
		Tag:          "tool",
		StateDelta: map[string][]byte{
			"json": []byte(stateJSON),
		},
		Extensions: map[string]json.RawMessage{
			"fixture": json.RawMessage(stateJSON),
		},
		Actions: &event.EventActions{SkipSummarization: true},
		Version: event.CurrentVersion,
	}

	sess := session.NewSession(
		"replay-app",
		"user-1",
		"session-1",
		session.WithSessionEvents([]event.Event{evt}),
		session.WithSessionState(session.StateMap{
			"json":  []byte(stateJSON),
			"plain": []byte("hello"),
		}),
		session.WithSessionSummaries(map[string]*session.Summary{
			"branch/a": {
				Summary:   "base summary",
				Topics:    []string{"z", "a"},
				UpdatedAt: fixed,
				Boundary: session.NewSummaryBoundaryWithEventID(
					"branch/a",
					fixed,
					summaryLastEventID,
				),
			},
		}),
		session.WithSessionCreatedAt(fixed.Add(-time.Hour)),
		session.WithSessionUpdatedAt(fixed),
	)
	sess.Tracks = map[session.Track]*session.TrackEvents{
		session.Track("tool"): {
			Track: session.Track("tool"),
			Events: []session.TrackEvent{{
				Track:     session.Track("tool"),
				Payload:   json.RawMessage(trackPayload),
				Timestamp: fixed,
			}},
		},
	}

	memories := []*memory.Entry{{
		ID:      rawMemoryID,
		AppName: sess.AppName,
		UserID:  sess.UserID,
		Memory: &memory.Memory{
			Memory:       "likes tea",
			Topics:       []string{"preference", "drink"},
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Bob", "Ada"},
			Location:     "office",
		},
		CreatedAt: fixed,
		UpdatedAt: fixed,
	}}
	return makeReplaySnapshot(sess, memories)
}
