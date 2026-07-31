//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestReplayConsistency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reportPath := os.Getenv("REPLAY_REPORT_PATH")
	if reportPath == "" {
		reportPath = filepath.Join(
			t.TempDir(),
			"session_memory_summary_diff_report.json",
		)
	}
	result, err := RunReplayConsistency(ctx, RunnerConfig{
		CaseDir:    filepath.Join("testdata", "replay_cases"),
		ReportPath: reportPath,
		TempDir:    t.TempDir(),
		NormalizeOptions: NormalizeOptions{
			NormalizeGeneratedMemoryIDs: true,
			NilEqualsEmpty:              true,
		},
		RunMutations: true,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, result.Report.Summary.CaseCount, 20)
	wantFactories, err := BackendFactoriesFromEnv()
	require.NoError(t, err)
	require.Equal(t, len(wantFactories), result.Report.Summary.BackendCount)
	if result.Report.Summary.UnexpectedDiffCount != 0 {
		for _, tc := range result.Report.Cases {
			for _, comparison := range tc.Comparisons {
				for _, diff := range comparison.Differences {
					if !diff.Allowed {
						t.Logf("case=%s backend=%s path=%s reference=%v actual=%v",
							tc.CaseID, comparison.ActualBackend, diff.Path,
							diff.Reference, diff.Actual)
					}
				}
			}
		}
	}
	require.Zero(t, result.Report.Summary.UnexpectedDiffCount,
		FormatReportSummary(result.Report))
	require.Equal(t, 1.0, result.Report.Summary.MutationDetectionRate,
		FormatReportSummary(result.Report))
	require.Less(t, result.Report.Summary.DurationMS, int64(30_000))

	wantSummaryMutations := map[string]string{
		"10_summary_missing":       MutationSummaryMissing,
		"11_summary_overwrite":     MutationSummaryOverwrite,
		"12_summary_wrong_session": MutationSummaryWrongSession,
		"15_event_postwrite_retry": MutationEventDuplicate,
		"16_state_summary_failure": MutationStateDirty,
		"18_track_observability":   MutationTrackPayload,
	}
	seenSummaryMutations := make(map[string]bool, len(wantSummaryMutations))
	for _, tc := range result.Report.Cases {
		want, ok := wantSummaryMutations[tc.CaseID]
		if !ok {
			continue
		}
		seenSummaryMutations[tc.CaseID] = true
		require.Len(t, tc.Mutations, 1)
		require.Equal(t, want, tc.Mutations[0].Name)
		require.True(t, tc.Mutations[0].Detected)
		require.NotEmpty(t, tc.Mutations[0].Differences)
		for _, diff := range tc.Mutations[0].Differences {
			require.NotEmpty(t, diff.Path)
			require.NotEmpty(t, diff.SessionID)
			if want == MutationSummaryMissing ||
				want == MutationSummaryOverwrite ||
				want == MutationSummaryWrongSession {
				require.NotEmpty(t, diff.SummaryID)
			}
		}
	}
	for caseID := range wantSummaryMutations {
		require.Truef(
			t,
			seenSummaryMutations[caseID],
			"required mutation case %q was not loaded or executed",
			caseID,
		)
	}
}

func TestRunReplayConsistencyUsesUniqueRedisNamespace(t *testing.T) {
	redisServer := miniredis.RunT(t)
	factory := &emptyNamespaceRedisFactory{redis: redisServer}
	caseDir := t.TempDir()
	fixture := []byte(
		"{\"action\":\"metadata\",\"version\":1,\"id\":\"redis-namespace\"}\n" +
			"{\"action\":\"create_session\",\"session_id\":\"session-redis\"}\n",
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(caseDir, "redis-namespace.jsonl"), fixture, 0o600,
	))

	cfg := RunnerConfig{
		CaseDir: caseDir, ReportPath: filepath.Join(t.TempDir(), "report.json"),
		TempDir: t.TempDir(), RedisURL: "redis://" + redisServer.Addr(),
		BackendFactories: []BackendFactory{factory},
	}
	for run := 1; run <= 2; run++ {
		result, err := RunReplayConsistency(context.Background(), cfg)
		require.NoErrorf(t, err, "shared Redis run %d failed", run)
		require.Equal(t, "passed", result.Report.Status)
	}
	require.Len(t, factory.prefixes, 2)
	require.NotEqual(t, factory.prefixes[0], factory.prefixes[1])
	for _, prefix := range factory.prefixes {
		require.Regexp(t, `^replay:[0-9a-f]{32}:redis-namespace$`, prefix)
	}
}

func TestReplayParentCorrelationRoundTripAndAssertion(t *testing.T) {
	input := &EventInput{
		ID: "event-child", InvocationID: "inv-child",
		ParentInvocationID: "inv-root",
		ParentMetadata: &ReplayParentMetadata{
			TriggerType: "tool_call",
			TriggerID:   "call-a",
			TriggerName: "research-agent",
		},
		Author: "research-agent", Role: "assistant",
		Content: "child response", Timestamp: "2026-01-01T00:00:00Z",
	}
	evt, err := buildEvent(input)
	require.NoError(t, err)
	require.Equal(t, "inv-root", evt.ParentInvocationID)
	require.Equal(t, "tool_call", evt.ParentMetadata.TriggerType)
	require.Equal(t, "call-a", evt.ParentMetadata.TriggerID)
	require.Equal(t, "research-agent", evt.ParentMetadata.TriggerName)

	sess := &session.Session{
		ID:     "session-parent-correlation",
		Events: []event.Event{*evt},
	}
	expected := &SessionExpectation{
		EventCorrelations: map[string]EventCorrelationExpectation{
			"event-child": {
				InvocationID:       "inv-child",
				ParentInvocationID: "inv-root",
				ParentMetadata: &ReplayParentMetadata{
					TriggerType: "tool_call",
					TriggerID:   "call-a",
					TriggerName: "research-agent",
				},
			},
		},
	}
	require.NoError(t, assertReplaySession(
		context.Background(), nil, sess, expected,
	))

	snapshot := snapshotSession(sess)
	require.Len(t, snapshot.Events, 1)
	require.Equal(t, "inv-root", snapshot.Events[0].ParentInvocationID)
	require.Equal(t, &ReplayParentMetadata{
		TriggerType: "tool_call",
		TriggerID:   "call-a",
		TriggerName: "research-agent",
	}, snapshot.Events[0].ParentMetadata)

	evt.ParentMetadata.TriggerID = "changed-after-snapshot"
	require.Equal(t, "call-a", snapshot.Events[0].ParentMetadata.TriggerID,
		"the canonical snapshot must own a deep copy of parent metadata")
}

func TestReplayParentCorrelationAssertionDetectsDroppedFields(t *testing.T) {
	expected := &SessionExpectation{
		EventCorrelations: map[string]EventCorrelationExpectation{
			"event-child": {
				InvocationID:       "inv-child",
				ParentInvocationID: "inv-root",
				ParentMetadata: &ReplayParentMetadata{
					TriggerType: "tool_call",
					TriggerID:   "call-a",
					TriggerName: "research-agent",
				},
			},
		},
	}
	newSession := func() *session.Session {
		return &session.Session{
			ID: "session-parent-correlation",
			Events: []event.Event{{
				ID:                 "event-child",
				InvocationID:       "inv-child",
				ParentInvocationID: "inv-root",
				ParentMetadata: &event.ParentInvocationMetadata{
					TriggerType: "tool_call",
					TriggerID:   "call-a",
					TriggerName: "research-agent",
				},
			}},
		}
	}
	tests := []struct {
		name      string
		mutate    func(*event.Event)
		wantError string
	}{
		{
			name: "dropped parent invocation id",
			mutate: func(evt *event.Event) {
				evt.ParentInvocationID = ""
			},
			wantError: "parent invocation id",
		},
		{
			name: "dropped parent metadata",
			mutate: func(evt *event.Event) {
				evt.ParentMetadata = nil
			},
			wantError: "parent metadata is missing",
		},
		{
			name: "dropped trigger id",
			mutate: func(evt *event.Event) {
				evt.ParentMetadata.TriggerID = ""
			},
			wantError: "parent metadata",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := newSession()
			tt.mutate(&sess.Events[0])
			err := assertReplaySession(
				context.Background(), nil, sess, expected,
			)
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestReplayParentCorrelationValidation(t *testing.T) {
	validEvent := func() *EventInput {
		return &EventInput{
			ID: "event-child", Role: "assistant",
			Timestamp:          "2026-01-01T00:00:00Z",
			ParentInvocationID: "inv-root",
			ParentMetadata: &ReplayParentMetadata{
				TriggerType: "tool_call",
				TriggerID:   "call-a",
				TriggerName: "research-agent",
			},
		}
	}

	require.NoError(t, (ReplayAction{
		Action: ActionAppendEvent, SessionID: "session", Event: validEvent(),
	}).Validate())

	withoutParent := validEvent()
	withoutParent.ParentInvocationID = ""
	require.ErrorContains(t, (ReplayAction{
		Action: ActionAppendEvent, SessionID: "session", Event: withoutParent,
	}).Validate(), "parent_metadata requires parent_invocation_id")

	withoutTriggerID := validEvent()
	withoutTriggerID.ParentMetadata.TriggerID = ""
	require.ErrorContains(t, (ReplayAction{
		Action: ActionAppendEvent, SessionID: "session", Event: withoutTriggerID,
	}).Validate(), "requires trigger_type, trigger_id, and trigger_name")
}

func TestRebaseReplayCaseTimesRebasesTrackExpectations(t *testing.T) {
	base := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	testCase := ReplayCase{Actions: []ReplayAction{
		{
			Action: ActionAppendEvent,
			Event: &EventInput{
				Timestamp: "2026-01-01T00:00:01Z",
			},
		},
		{
			Action: ActionAppendTrack,
			Track: &TrackInput{
				Name:      "tool.execution",
				Timestamp: "2026-01-01T00:00:03Z",
			},
		},
		{
			Action: ActionAssertSession,
			Expected: &SessionExpectation{
				Tracks: map[string][]TrackEventExpectation{
					"tool.execution": {
						{Timestamp: "2026-01-01T00:00:03Z"},
						{},
					},
				},
			},
		},
	}}

	rebased, err := rebaseReplayCaseTimes(testCase, base)
	require.NoError(t, err)
	require.Equal(
		t,
		base.Format(time.RFC3339Nano),
		rebased.Actions[0].Event.Timestamp,
	)
	wantTrackTime := base.Add(2 * time.Second).Format(time.RFC3339Nano)
	require.Equal(t, wantTrackTime, rebased.Actions[1].Track.Timestamp)
	require.Equal(
		t,
		wantTrackTime,
		rebased.Actions[2].Expected.Tracks["tool.execution"][0].Timestamp,
	)
	require.Empty(
		t,
		rebased.Actions[2].Expected.Tracks["tool.execution"][1].Timestamp,
		"an omitted exact timestamp must remain optional",
	)
}

// ID差集单元测试
func TestResolveAddedMemoryID(t *testing.T) {
	idSet := func(ids ...string) map[string]struct{} {
		result := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			result[id] = struct{}{}
		}
		return result
	}
	tests := []struct {
		name      string
		before    map[string]struct{}
		after     map[string]struct{}
		want      string
		wantError string
	}{
		{
			name:   "one new ID among existing records",
			before: idSet("episode-a"),
			after:  idSet("episode-b", "episode-a"),
			want:   "episode-b",
		},
		{
			name:      "no new ID",
			before:    idSet("episode-a"),
			after:     idSet("episode-a"),
			wantError: "no new ID",
		},
		{
			name:      "multiple new IDs",
			before:    idSet("episode-a"),
			after:     idSet("episode-c", "episode-a", "episode-b"),
			wantError: "multiple new IDs",
		},
		{
			name:      "existing ID removed",
			before:    idSet("episode-a"),
			after:     idSet("episode-b"),
			wantError: "removed existing IDs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveAddedMemoryID(tt.before, tt.after)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCompareSnapshotsAllowedDiff(t *testing.T) {
	reference := comparisonFixture("reference")
	actual := comparisonFixture("actual")
	path := "$.sessions[0].events[0].content"

	withoutRule := CompareSnapshots(
		reference, actual, "inmemory", "sqlite", nil,
	)
	require.False(t, withoutRule.Equal)
	require.Len(t, withoutRule.Differences, 1)
	require.False(t, withoutRule.Differences[0].Allowed)

	withRule := CompareSnapshots(
		reference,
		actual,
		"inmemory",
		"sqlite",
		[]AllowedDiffRule{{
			Path: path, Backend: "sqlite",
			Reason: "explicit test-only backend representation difference",
		}},
	)
	require.True(t, withRule.Equal)
	require.Len(t, withRule.Differences, 1)
	require.True(t, withRule.Differences[0].Allowed)
	require.Equal(t, "session-allowed", withRule.Differences[0].SessionID)
	require.NotNil(t, withRule.Differences[0].EventIndex)
	require.Equal(t, 0, *withRule.Differences[0].EventIndex)
	require.Equal(t, "reference", withRule.Differences[0].Reference)
	require.Equal(t, "actual", withRule.Differences[0].Actual)

	wrongBackendRule := CompareSnapshots(
		reference,
		actual,
		"inmemory",
		"sqlite",
		[]AllowedDiffRule{{
			Path: path, Backend: "redis", Reason: "must not match sqlite",
		}},
	)
	require.False(t, wrongBackendRule.Equal)
	require.False(t, wrongBackendRule.Differences[0].Allowed)
}

func TestLoadReplayCaseAllowedDiff(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "allowed-diff.jsonl")
	fixture := []byte(
		"{\"action\":\"metadata\",\"version\":1,\"id\":\"allowed-diff\"}\n" +
			"{\"action\":\"allow_diff\",\"allowed_diff\":{\"path\":\"$.memories[*].id\"," +
			"\"backend\":\"redis\",\"reason\":\"generated id\"}}\n" +
			"{\"action\":\"create_session\",\"session_id\":\"session-allowed\"}\n",
	)
	require.NoError(t, os.WriteFile(fixturePath, fixture, 0o600))
	testCase, err := LoadReplayCase(fixturePath)
	require.NoError(t, err)
	require.Len(t, testCase.AllowedDiff, 1)
	require.Equal(t, "$.memories[*].id", testCase.AllowedDiff[0].Path)
	require.Equal(t, "redis", testCase.AllowedDiff[0].Backend)
	require.Equal(t, "generated id", testCase.AllowedDiff[0].Reason)
}

func comparisonFixture(content string) CanonicalSnapshot {
	return CanonicalSnapshot{Snapshot: Snapshot{
		CaseID: "allowed-diff",
		Sessions: []SessionSnapshot{{
			ID: "session-allowed",
			Events: []EventSnapshot{{
				ID: "event-allowed", Index: 0, Content: content,
			}},
		}},
	}}
}

func TestBackendFactoriesFromEnv(t *testing.T) {
	for _, name := range []string{
		"REPLAY_BACKENDS", "REPLAY_SKIP_INMEMORY", "REPLAY_SKIP_SQL",
		"REPLAY_SKIP_SQLITE", "REPLAY_SKIP_REDIS",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("REPLAY_BACKENDS", "inmemory,sqlite,redis")
	t.Setenv("REPLAY_SKIP_REDIS", "true")
	factories, err := BackendFactoriesFromEnv()
	require.NoError(t, err)
	require.Equal(t, []string{"inmemory", "sqlite"}, factoryNames(factories))

	t.Setenv("REPLAY_SKIP_REDIS", "false")
	t.Setenv("REPLAY_SKIP_SQL", "true")
	factories, err = BackendFactoriesFromEnv()
	require.NoError(t, err)
	require.Equal(t, []string{"inmemory", "redis"}, factoryNames(factories))

	t.Setenv("REPLAY_SKIP_SQL", "false")
	t.Setenv("REPLAY_BACKENDS", "unknown")
	_, err = BackendFactoriesFromEnv()
	require.ErrorContains(t, err, "unknown REPLAY_BACKENDS entry")
}

func factoryNames(factories []BackendFactory) []string {
	names := make([]string, 0, len(factories))
	for _, factory := range factories {
		names = append(names, factory.Name())
	}
	return names
}

type emptyNamespaceRedisFactory struct {
	redis    *miniredis.Miniredis
	prefixes []string
}

func (f *emptyNamespaceRedisFactory) Name() string { return "redis" }
func (f *emptyNamespaceRedisFactory) Create(
	ctx context.Context,
	cfg BackendConfig,
) (Backend, error) {
	f.prefixes = append(f.prefixes, cfg.KeyPrefix)
	for _, key := range f.redis.Keys() {
		if strings.HasPrefix(key, cfg.KeyPrefix+":") {
			return nil, fmt.Errorf(
				"redis namespace %q is not empty: found key %q",
				cfg.KeyPrefix, key,
			)
		}
	}
	return (RedisBackendFactory{}).Create(ctx, cfg)
}

type failingBackendFactory struct{}

func (failingBackendFactory) Name() string { return "failing" }
func (failingBackendFactory) Create(context.Context, BackendConfig) (Backend, error) {
	return nil, errors.New("injected backend creation failure")
}

type appendFailingSessionService struct {
	session.Service
}

func (s *appendFailingSessionService) AppendEvent(
	context.Context,
	*session.Session,
	*event.Event,
	...session.Option,
) error {
	return errors.New("injected append event failure")
}

type appendFailingBackend struct {
	Backend
	sessionService session.Service
}

func (b *appendFailingBackend) Name() string { return "append-failing" }
func (b *appendFailingBackend) SessionService() session.Service {
	return b.sessionService
}

type appendFailingBackendFactory struct{}

func (appendFailingBackendFactory) Name() string { return "append-failing" }
func (appendFailingBackendFactory) Create(
	ctx context.Context,
	cfg BackendConfig,
) (Backend, error) {
	backend, err := (InMemoryBackendFactory{}).Create(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &appendFailingBackend{
		Backend: backend,
		sessionService: &appendFailingSessionService{
			Service: backend.SessionService(),
		},
	}, nil
}

func TestRunReplayConsistencyWritesFailureReport(t *testing.T) {
	caseDir := t.TempDir()
	fixture := []byte(
		"{\"action\":\"metadata\",\"version\":1,\"id\":\"failure-report\"}\n" +
			"{\"action\":\"create_session\",\"session_id\":\"session-failure\"}\n",
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(caseDir, "failure-report.jsonl"), fixture, 0o600,
	))
	reportPath := filepath.Join(t.TempDir(), "failure-report.json")
	result, err := RunReplayConsistency(context.Background(), RunnerConfig{
		CaseDir: caseDir, ReportPath: reportPath, TempDir: t.TempDir(),
		BackendFactories: []BackendFactory{failingBackendFactory{}},
	})
	require.ErrorContains(t, err, "injected backend creation failure")
	require.NotNil(t, result)
	require.Equal(t, "failed", result.Report.Status)
	require.Len(t, result.Report.Cases, 1)
	require.NotEmpty(t, result.Report.Cases[0].Error)

	raw, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	var persisted ReplayReport
	require.NoError(t, json.Unmarshal(raw, &persisted))
	require.Equal(t, "failed", persisted.Status)
	require.Contains(t, persisted.Error, "injected backend creation failure")
	require.Contains(t, persisted.Cases[0].Error, "injected backend creation failure")
}

func TestRunReplayConsistencyWritesActionFailureReport(t *testing.T) {
	caseDir := t.TempDir()
	fixture := []byte(
		"{\"action\":\"metadata\",\"version\":1,\"id\":\"action-failure-report\"}\n" +
			"{\"action\":\"create_session\",\"session_id\":\"session-failure\"}\n" +
			"{\"action\":\"append_event\",\"session_id\":\"session-failure\"," +
			"\"failure\":{\"fail_before\":true},\"event\":{\"id\":\"event-failure\"," +
			"\"role\":\"user\",\"content\":\"fail\",\"timestamp\":\"2026-01-01T00:00:00Z\"}}\n",
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(caseDir, "action-failure-report.jsonl"), fixture, 0o600,
	))
	reportPath := filepath.Join(t.TempDir(), "action-failure-report.json")
	result, err := RunReplayConsistency(context.Background(), RunnerConfig{
		CaseDir: caseDir, ReportPath: reportPath, TempDir: t.TempDir(),
		BackendFactories: []BackendFactory{InMemoryBackendFactory{}},
	})
	require.ErrorContains(t, err, "injected failure before write")
	require.NotNil(t, result)
	require.Equal(t, "failed", result.Report.Status)
	require.Len(t, result.Report.Cases, 1)
	require.Len(t, result.Report.Cases[0].Runs, 1)
	require.Contains(t, result.Report.Cases[0].Runs[0].Error, "injected failure before write")

	require.Len(t, result.Report.Cases[0].Runs[0].ActionResults, 2)

	run := result.Report.Cases[0].Runs[0]
	createSessionAction := run.ActionResults[0]
	require.Equal(t, 0, createSessionAction.Index)
	require.Equal(t, ActionCreateSession, createSessionAction.Action)
	require.True(t, createSessionAction.Success)
	require.Empty(t, createSessionAction.Error)

	// The injected pre-write failure must belong to append_event.
	injectedAction := run.ActionResults[1]
	require.Equal(t, 1, injectedAction.Index)
	require.Equal(t, ActionAppendEvent, injectedAction.Action)
	require.False(t, injectedAction.Success)
	require.Contains(
		t,
		injectedAction.Error,
		"injected failure before write",
	)

	raw, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	var persisted ReplayReport
	require.NoError(t, json.Unmarshal(raw, &persisted))
	require.Equal(t, "failed", persisted.Status)
	require.Contains(t, persisted.Error, "injected failure before write")
}

func TestRunReplayConsistencyContinuesWithNextBackend(t *testing.T) {
	caseDir := t.TempDir()
	fixture := []byte(
		"{\"action\":\"metadata\",\"version\":1," +
			"\"id\":\"backend-continuation\"}\n" +
			"{\"action\":\"create_session\"," +
			"\"session_id\":\"session-backend\"}\n" +
			"{\"action\":\"append_event\"," +
			"\"session_id\":\"session-backend\",\"event\":{" +
			"\"id\":\"event-01\",\"role\":\"user\"," +
			"\"content\":\"continue\"," +
			"\"timestamp\":\"2026-01-01T00:00:00Z\"}}\n" +
			"{\"action\":\"checkpoint\",\"checkpoint\":\"after-event\"}\n",
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(caseDir, "backend-continuation.jsonl"), fixture, 0o600,
	))

	result, err := RunReplayConsistency(context.Background(), RunnerConfig{
		CaseDir: caseDir, ReportPath: filepath.Join(t.TempDir(), "report.json"),
		TempDir: t.TempDir(),
		BackendFactories: []BackendFactory{
			appendFailingBackendFactory{},
			InMemoryBackendFactory{},
			SQLiteBackendFactory{},
		},
	})

	require.ErrorContains(t, err, "injected append event failure")
	require.Len(t, result.Report.Cases, 1)
	runs := result.Report.Cases[0].Runs
	require.Len(t, runs, 3)
	require.Equal(t, "append-failing", runs[0].Backend)
	require.Contains(t, runs[0].Error, "injected append event failure")
	require.Len(t, runs[0].ActionResults, 2,
		"the failed trajectory must stop before the checkpoint")
	require.Equal(t, "inmemory", runs[1].Backend)
	require.Empty(t, runs[1].Error)
	require.Len(t, runs[1].ActionResults, 3,
		"the next backend must execute the complete trajectory")
	require.Len(t, runs[1].Checkpoints, 1)
	require.Equal(t, "sqlite", runs[2].Backend)
	require.Empty(t, runs[2].Error)
	require.Len(t, runs[2].ActionResults, 3)
	require.Len(t, result.Report.Cases[0].Comparisons, 1,
		"the two successful backends must still be compared")
	require.Equal(t, "inmemory",
		result.Report.Cases[0].Comparisons[0].ReferenceBackend)
	require.Equal(t, "sqlite",
		result.Report.Cases[0].Comparisons[0].ActualBackend)
	require.True(t, result.Report.Cases[0].Comparisons[0].Equal)
}

func TestRunReplayConsistencyContinuesWithNextCase(t *testing.T) {
	caseDir := t.TempDir()
	failingFixture := []byte(
		"{\"action\":\"metadata\",\"version\":1,\"id\":\"01-failing\"}\n" +
			"{\"action\":\"create_session\",\"session_id\":\"session-failing\"}\n" +
			"{\"action\":\"append_event\",\"session_id\":\"session-failing\"," +
			"\"failure\":{\"fail_before\":true},\"event\":{" +
			"\"id\":\"event-failing\",\"role\":\"user\",\"content\":\"fail\"," +
			"\"timestamp\":\"2026-01-01T00:00:00Z\"}}\n" +
			"{\"action\":\"checkpoint\",\"checkpoint\":\"must-not-run\"}\n",
	)
	successFixture := []byte(
		"{\"action\":\"metadata\",\"version\":1,\"id\":\"02-success\"}\n" +
			"{\"action\":\"create_session\",\"session_id\":\"session-success\"}\n" +
			"{\"action\":\"checkpoint\",\"checkpoint\":\"completed\"}\n",
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(caseDir, "01-failing.jsonl"), failingFixture, 0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(caseDir, "02-success.jsonl"), successFixture, 0o600,
	))
	reportPath := filepath.Join(t.TempDir(), "complete-report.json")

	result, err := RunReplayConsistency(context.Background(), RunnerConfig{
		CaseDir: caseDir, ReportPath: reportPath, TempDir: t.TempDir(),
		BackendFactories: []BackendFactory{InMemoryBackendFactory{}},
	})

	require.ErrorContains(t, err, "injected failure before write")
	require.Len(t, result.Report.Cases, 2)
	require.Equal(t, "failed", result.Report.Cases[0].Status)
	require.Len(t, result.Report.Cases[0].Runs[0].ActionResults, 2,
		"the failed trajectory must stop before its checkpoint")
	require.Equal(t, "passed", result.Report.Cases[1].Status)
	require.Len(t, result.Report.Cases[1].Runs, 1)
	require.Len(t, result.Report.Cases[1].Runs[0].Checkpoints, 1,
		"the case after the failure must still run")

	raw, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	var persisted ReplayReport
	require.NoError(t, json.Unmarshal(raw, &persisted))
	require.Len(t, persisted.Cases, 2,
		"the persisted report must include all executable cases")
	require.Equal(t, "failed", persisted.Status)
}

func TestExecuteWithFailureFailBeforeRetry(t *testing.T) {
	calls := 0

	err := executeWithFailure(
		func() error {
			calls++
			return nil
		},
		&FailureInput{
			FailBefore: true,
			Retry:      true,
		},
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestExecuteWithFailureFailAfterConfirmedDoesNotRetry(t *testing.T) {
	operationCalls := 0
	confirmCalls := 0

	err := executeWithFailure(
		func() error {
			operationCalls++
			return nil
		},
		&FailureInput{
			FailAfter: true,
			Retry:     true,
		},
		func() (bool, error) {
			confirmCalls++
			return true, nil
		},
	)

	require.NoError(t, err)
	require.Equal(t, 1, operationCalls)
	require.Equal(t, 1, confirmCalls)
}

func TestReplayMemoryFailAfterConfirmsBeforeRetry(t *testing.T) {
	ctx := context.Background()
	backend, err := (InMemoryBackendFactory{}).Create(ctx, BackendConfig{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, backend.Close())
	})

	service, ok := backend.(*serviceBackend)
	require.True(t, ok)
	counting := &countingMemoryService{Service: service.memoryService}
	service.memoryService = counting
	state := &replayState{
		appName:   "replay-app",
		userID:    "replay-user",
		sessions:  make(map[string]*session.Session),
		memoryIDs: make(map[string]string),
	}

	add := ReplayAction{
		Action: ActionAddMemory,
		Memory: &MemoryInput{
			Ref:     "fact",
			Content: "original fact",
			Topics:  []string{"retry"},
			Kind:    memory.KindFact,
		},
		Failure: &FailureInput{FailAfter: true, Retry: true},
	}
	require.NoError(t, executeReplayAction(ctx, backend, state, add))
	require.Equal(t, 1, counting.addCalls)
	require.NotEmpty(t, state.memoryIDs["fact"])

	update := ReplayAction{
		Action: ActionUpdateMemory,
		Memory: &MemoryInput{
			Ref:     "fact",
			Content: "updated fact",
			Topics:  []string{"confirmed", "retry"},
			Kind:    memory.KindFact,
		},
		Failure: &FailureInput{FailAfter: true, Retry: true},
	}
	require.NoError(t, executeReplayAction(ctx, backend, state, update))
	require.Equal(t, 1, counting.updateCalls)

	entries, err := counting.ReadMemories(
		ctx,
		memory.UserKey{AppName: state.appName, UserID: state.userID},
		0,
	)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "updated fact", entries[0].Memory.Memory)
	require.ElementsMatch(
		t,
		[]string{"confirmed", "retry"},
		entries[0].Memory.Topics,
	)

	remove := ReplayAction{
		Action:  ActionDeleteMemory,
		Memory:  &MemoryInput{Ref: "fact"},
		Failure: &FailureInput{FailAfter: true, Retry: true},
	}
	require.NoError(t, executeReplayAction(ctx, backend, state, remove))
	require.Equal(t, 1, counting.deleteCalls)
	_, exists := state.memoryIDs["fact"]
	require.False(t, exists)

	entries, err = counting.ReadMemories(
		ctx,
		memory.UserKey{AppName: state.appName, UserID: state.userID},
		0,
	)
	require.NoError(t, err)
	require.Empty(t, entries)
}

type countingMemoryService struct {
	memory.Service
	addCalls    int
	updateCalls int
	deleteCalls int
}

func (s *countingMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryText string,
	topics []string,
	opts ...memory.AddOption,
) error {
	s.addCalls++
	return s.Service.AddMemory(ctx, userKey, memoryText, topics, opts...)
}

func (s *countingMemoryService) UpdateMemory(
	ctx context.Context,
	key memory.Key,
	memoryText string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	s.updateCalls++
	return s.Service.UpdateMemory(ctx, key, memoryText, topics, opts...)
}

func (s *countingMemoryService) DeleteMemory(
	ctx context.Context,
	key memory.Key,
) error {
	s.deleteCalls++
	return s.Service.DeleteMemory(ctx, key)
}

func TestReplayActionValidateAssertMemoryRequiresExpectation(t *testing.T) {
	action := ReplayAction{
		Action: ActionAssertMemory,
	}

	err := action.Validate()

	require.EqualError(
		t,
		err,
		"assert_memory requires expected_memory",
	)
}

func TestReplayActionValidateAssertMemoryRejectsEmptyItem(t *testing.T) {
	action := ReplayAction{
		Action: ActionAssertMemory,
		ExpectedMemory: &MemoryExpectation{
			Contains: []MemoryItemExpectation{{}},
		},
	}

	err := action.Validate()

	require.EqualError(
		t,
		err,
		"assert_memory contains item 0 must specify at least one field",
	)
}
