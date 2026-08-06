//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const testRunNamespace = "test-run"

func completeMemoryReader(service memory.Service) ReadAllMemoriesFunc {
	return func(ctx context.Context, userKey memory.UserKey) ([]*memory.Entry, bool, error) {
		entries, err := service.ReadMemories(ctx, userKey, 0)
		return entries, err == nil, err
	}
}

func buildSnapshotForTest(
	t testing.TB,
	sess *session.Session,
	memories []*memory.Entry,
) Snapshot {
	t.Helper()
	snapshot, err := BuildSnapshot(sess, memories)
	require.NoError(t, err)
	return snapshot
}

func compareSnapshotsForTest(
	t testing.TB,
	caseName string,
	sessionID string,
	backendA string,
	backendB string,
	left Snapshot,
	right Snapshot,
	allowedRules []AllowedDiffRule,
) []Diff {
	t.Helper()
	diffs, err := CompareSnapshots(
		caseName, sessionID, backendA, backendB, left, right, allowedRules,
	)
	require.NoError(t, err)
	return diffs
}

func TestRunRejectsInvalidBackendNames(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "", want: "replay backend name is empty"},
		{name: " \t", want: "replay backend name is empty"},
		{name: " sqlite ", want: `replay backend name " sqlite " has surrounding whitespace`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Run(context.Background(), "", Backend{Name: test.name}, Case{Name: "invalid"})
			require.EqualError(t, err, test.want)
		})
	}
}

func TestRunRejectsInvalidNamespaceBeforePersistence(t *testing.T) {
	ctx := context.Background()
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()
	backend := Backend{
		Name: "in_memory", SessionService: sessionService,
		MemoryService: memoryService, ReadAllMemories: completeMemoryReader(memoryService),
	}
	tests := []struct {
		name      string
		namespace string
		want      string
	}{
		{name: "empty", want: "replay run namespace is empty"},
		{name: "whitespace", namespace: " \t", want: "replay run namespace is empty"},
		{
			name: "surrounding whitespace", namespace: " run ",
			want: `replay run namespace " run " has surrounding whitespace`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tc := Case{
				Name:   "invalid-namespace-" + strings.ReplaceAll(test.name, " ", "-"),
				Tracks: []TrackSpec{{Name: "requires-track-preflight"}},
			}
			_, err := Run(ctx, test.namespace, backend, tc)
			require.EqualError(t, err, test.want)

			key := replayKey(test.namespace, tc.Name)
			got, getErr := sessionService.GetSession(ctx, key)
			require.NoError(t, getErr)
			require.Nil(t, got)
			memories, readErr := memoryService.ReadMemories(ctx, memory.UserKey{
				AppName: key.AppName, UserID: key.UserID,
			}, 0)
			require.NoError(t, readErr)
			require.Empty(t, memories)
		})
	}
}

func TestReplayKeyUsesRunNamespaceForEveryScope(t *testing.T) {
	first := replayKey("run-a", "case")
	same := replayKey("run-a", "case")
	second := replayKey("run-b", "case")
	boundaryA := replayKey("a", "b-c")
	boundaryB := replayKey("a-b", "c")
	unicode := replayKey("运行", "case")

	require.Equal(t, first, same)
	require.Equal(t, session.Key{
		AppName:   "replay-matrix-5-run-a-case",
		UserID:    "user-5-run-a-case",
		SessionID: "session-5-run-a-case",
	}, first)
	require.NotEqual(t, first.AppName, second.AppName)
	require.NotEqual(t, first.UserID, second.UserID)
	require.NotEqual(t, first.SessionID, second.SessionID)
	require.NotEqual(t, boundaryA, boundaryB)
	require.Equal(t, "session-6-运行-case", unicode.SessionID)
}

func TestRunRejectsMissingServices(t *testing.T) {
	ctx := context.Background()
	_, err := Run(ctx, testRunNamespace, Backend{Name: "missing-session"}, Case{Name: "invalid"})
	require.EqualError(t, err, `replay backend "missing-session" has nil session service`)

	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	_, err = Run(ctx, testRunNamespace, Backend{
		Name:           "missing-memory",
		SessionService: sessionService,
	}, Case{Name: "invalid"})
	require.EqualError(t, err, `replay backend "missing-memory" has nil memory service`)

	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()
	_, err = Run(ctx, testRunNamespace, Backend{
		Name: "missing-read-all", SessionService: sessionService,
		MemoryService: memoryService,
	}, Case{Name: "invalid"})
	require.EqualError(t, err, `replay backend "missing-read-all" has nil ReadAllMemories`)
	got, err := sessionService.GetSession(ctx, replayKey(testRunNamespace, "invalid"))
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestRunUsesReadAllMemoriesForAliasAndFinalSnapshot(t *testing.T) {
	tests := []struct {
		name      string
		tc        Case
		wantReads int
		wantFinal string
	}{
		{name: "final snapshot", tc: Case{Name: "complete-read-final"}, wantReads: 1},
		{
			name: "alias and final snapshot",
			tc: Case{
				Name: "complete-read-alias",
				Memories: []MemoryOp{
					{Operation: MemoryAdd, Content: "initial", ResultAlias: "memory"},
					{Operation: MemoryUpdate, Ref: "memory", Content: "updated"},
				},
			},
			wantReads: 2,
			wantFinal: "updated",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessionService := sessinmemory.NewSessionService()
			defer sessionService.Close()
			memoryService := meminmemory.NewMemoryService()
			defer memoryService.Close()
			readCount := 0
			readAllMemories := func(ctx context.Context, userKey memory.UserKey) ([]*memory.Entry, bool, error) {
				readCount++
				entries, err := memoryService.ReadMemories(ctx, userKey, 0)
				return entries, err == nil, err
			}

			result, err := Run(context.Background(), testRunNamespace, Backend{
				Name: "complete_read", SessionService: sessionService,
				MemoryService: memoryService, ReadAllMemories: readAllMemories,
			}, test.tc)
			require.NoError(t, err)
			require.Equal(t, test.wantReads, readCount)
			if test.wantFinal == "" {
				require.Empty(t, result.Snapshot.Memory)
				return
			}
			require.Len(t, result.Snapshot.Memory, 1)
			require.Equal(t, test.wantFinal, result.Snapshot.Memory[0].Content)
		})
	}
}

func TestRunRejectsMemoryReadWithoutCompletenessConfirmation(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()
	readCount := 0

	_, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "limited", SessionService: sessionService,
		MemoryService: memoryService,
		ReadAllMemories: func(ctx context.Context, userKey memory.UserKey) ([]*memory.Entry, bool, error) {
			readCount++
			entries, err := memoryService.ReadMemories(ctx, userKey, 0)
			return entries, false, err
		},
	}, Case{
		Name:     "truncated-memory",
		Memories: []MemoryOp{{Operation: MemoryAdd, Content: "short read"}},
	})
	require.EqualError(t, err,
		`read final memories for case "truncated-memory": memory read returned 1 entries without completeness confirmation`)
	require.Equal(t, 1, readCount)
}

func TestRunRejectsCorruptMemoryReads(t *testing.T) {
	tests := []struct {
		name    string
		entries []*memory.Entry
		want    string
	}{
		{name: "nil entry", entries: []*memory.Entry{nil}, want: "memory entry 0 is nil"},
		{
			name: "nil memory",
			entries: []*memory.Entry{{
				ID: "broken", AppName: "app", UserID: "user",
			}},
			want: "memory entry 0 has nil Memory",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessionService := sessinmemory.NewSessionService()
			defer sessionService.Close()
			memoryService := meminmemory.NewMemoryService()
			defer memoryService.Close()
			readAllMemories := func(context.Context, memory.UserKey) ([]*memory.Entry, bool, error) {
				return test.entries, true, nil
			}

			_, err := Run(context.Background(), testRunNamespace, Backend{
				Name: "corrupt", SessionService: sessionService,
				MemoryService: memoryService, ReadAllMemories: readAllMemories,
			}, Case{Name: "corrupt-final"})
			require.EqualError(t, err, `read final memories for case "corrupt-final": `+test.want)

			_, err = Run(context.Background(), testRunNamespace, Backend{
				Name: "corrupt-alias", SessionService: sessionService,
				MemoryService: memoryService, ReadAllMemories: readAllMemories,
			}, Case{
				Name: "corrupt-alias",
				Memories: []MemoryOp{{
					Operation: MemoryAdd, Content: "target", ResultAlias: "target",
				}},
			})
			require.EqualError(t, err, `memory operation 0 for case "corrupt-alias": `+test.want)
		})
	}
}

func TestRunReportsInvalidCaseOperations(t *testing.T) {
	zero, one, tooLarge := 0, 1, 3
	tests := []struct {
		name                string
		tc                  Case
		withoutTrackService bool
		want                string
	}{
		{
			name: "nil event",
			tc:   Case{Name: "nil-event", Events: []*event.Event{nil}},
			want: `event 0 for case "nil-event" is nil`,
		},
		{
			name:                "missing track service",
			withoutTrackService: true,
			tc: Case{Name: "missing-track", Tracks: []TrackSpec{{
				Name: "trace",
			}}},
			want: `track 0 for case "missing-track" requires track service`,
		},
		{
			name: "empty track name",
			tc: Case{Name: "empty-track", Tracks: []TrackSpec{{
				Name: " ",
			}}},
			want: `track 0 for case "empty-track" has empty name`,
		},
		{
			name: "unsupported track payload",
			tc: Case{Name: "bad-track-payload", Tracks: []TrackSpec{{
				Name: "trace", Payload: make(chan int),
			}}},
			want: `marshal track 0 for case "bad-track-payload": json: unsupported type: chan int`,
		},
		{
			name: "unknown memory operation",
			tc: Case{Name: "unknown-memory", Memories: []MemoryOp{{
				Name: "bad", Operation: MemoryOperation("replace"),
			}}},
			want: `memory operation 0 for case "unknown-memory": unknown memory operation "replace" (bad)`,
		},
		{
			name: "missing memory alias",
			tc: Case{Name: "missing-alias", Memories: []MemoryOp{{
				Operation: MemoryUpdate, Ref: "unknown", Content: "updated",
			}}},
			want: `memory operation 0 for case "missing-alias": missing memory alias "unknown"`,
		},
		{
			name: "unsupported concurrent operation",
			tc: Case{Name: "concurrent-update", ConcurrentMemories: []MemoryOp{{
				Operation: MemoryUpdate,
			}}},
			want: `concurrent memory operations for case "concurrent-update": concurrent memory operation 0: unsupported concurrent memory operation "update"`,
		},
		{
			name: "decreasing summary prefix",
			tc: Case{
				Name:   "decreasing-summary-prefix",
				Events: []*event.Event{{}, {}},
				Summaries: []SummaryStep{
					{EventPrefix: &one},
					{EventPrefix: &zero},
				},
			},
			want: `summary step 1 for case "decreasing-summary-prefix" has event prefix 0 before already appended prefix 1`,
		},
		{
			name: "summary prefix out of range",
			tc: Case{
				Name:   "summary-prefix-out-of-range",
				Events: []*event.Event{{}, {}},
				Summaries: []SummaryStep{
					{EventPrefix: &one},
					{EventPrefix: &tooLarge},
				},
			},
			want: `summary step 1 for case "summary-prefix-out-of-range" has event prefix 3 outside [0,2]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessionService := sessinmemory.NewSessionService()
			defer sessionService.Close()
			memoryService := meminmemory.NewMemoryService()
			defer memoryService.Close()
			var trackService session.TrackService = sessionService
			if test.withoutTrackService {
				trackService = nil
			}
			_, err := Run(context.Background(), testRunNamespace, Backend{
				Name:            "in_memory",
				SessionService:  sessionService,
				TrackService:    trackService,
				MemoryService:   memoryService,
				ReadAllMemories: completeMemoryReader(memoryService),
			}, test.tc)
			require.EqualError(t, err, test.want)
			got, getErr := sessionService.GetSession(context.Background(), replayKey(testRunNamespace, test.tc.Name))
			require.NoError(t, getErr)
			require.Nil(t, got)
		})
	}
}

func TestRunReportsMemoryQueryMismatch(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	tc := Case{Name: "query", Queries: []MemoryQuery{{
		Query: "missing", ExpectedContents: []string{"missing memory"},
	}}}
	_, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "in_memory", SessionService: sessionService,
		MemoryService: memoryService, ReadAllMemories: completeMemoryReader(memoryService),
	}, tc)
	require.EqualError(t, err, `memory query 0 for case "query" returned contents [], want ["missing memory"]`)

	got, getErr := sessionService.GetSession(context.Background(), replayKey(testRunNamespace, tc.Name))
	require.NoError(t, getErr)
	require.NotNil(t, got)
}

func TestRunRejectsStaticFixtureBeforePersistence(t *testing.T) {
	zero, one, outOfRange := 0, 1, 3
	validEvent := func(id string) *event.Event {
		return replayTimelineEvent(id, time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC))
	}
	tests := []struct {
		name         string
		tc           Case
		want         string
		wantContains []string
	}{
		{
			name: "later summary prefix decreases",
			tc: Case{
				Name:   "preflight-summary-decreases",
				Events: []*event.Event{validEvent("event-0"), validEvent("event-1")},
				Summaries: []SummaryStep{
					{Name: "first", Force: true, Text: "first summary", EventPrefix: &one},
					{Name: "second", Force: true, Text: "second summary", EventPrefix: &zero},
				},
			},
			want: `summary step 1 for case "preflight-summary-decreases" has event prefix 0 before already appended prefix 1`,
		},
		{
			name: "later summary prefix is out of range",
			tc: Case{
				Name:   "preflight-summary-out-of-range",
				Events: []*event.Event{validEvent("event-0"), validEvent("event-1")},
				Summaries: []SummaryStep{
					{Name: "first", Force: true, Text: "first summary", EventPrefix: &one},
					{Name: "second", Force: true, Text: "second summary", EventPrefix: &outOfRange},
				},
			},
			want: `summary step 1 for case "preflight-summary-out-of-range" has event prefix 3 outside [0,2]`,
		},
		{
			name: "middle event is nil",
			tc: Case{
				Name: "preflight-nil-event",
				Events: []*event.Event{
					validEvent("event-0"), nil, validEvent("event-2"),
				},
			},
			want: `event 1 for case "preflight-nil-event" is nil`,
		},
		{
			name: "event contains channel",
			tc: Case{
				Name:   "preflight-channel-event",
				Events: []*event.Event{validEvent("event-0"), replayMalformedEvent()},
			},
			wantContains: []string{
				`validate events for case "preflight-channel-event"`,
				"normalize event 1: marshal",
				"unsupported type: chan int",
			},
		},
		{
			name: "event contains function",
			tc: Case{
				Name: "preflight-function-event",
				Events: []*event.Event{
					validEvent("event-0"), replayMalformedEventWithExtra(func() {}),
				},
			},
			wantContains: []string{
				`validate events for case "preflight-function-event"`,
				"normalize event 1: marshal",
				"unsupported type: func()",
			},
		},
		{
			name: "event contains malformed raw message",
			tc: Case{
				Name: "preflight-raw-message-event",
				Events: []*event.Event{
					validEvent("event-0"),
					{Extensions: map[string]json.RawMessage{"broken": json.RawMessage("{")}},
				},
			},
			wantContains: []string{
				`validate events for case "preflight-raw-message-event"`,
				"normalize event 1: marshal",
			},
		},
		{
			name: "later memory operation is unknown",
			tc: Case{
				Name: "preflight-late-unknown-memory",
				Memories: []MemoryOp{
					{Operation: MemoryAdd, Content: "first", ResultAlias: "first"},
					{Name: "bad", Operation: MemoryOperation("replace")},
				},
			},
			want: `memory operation 1 for case "preflight-late-unknown-memory": unknown memory operation "replace" (bad)`,
		},
		{
			name: "later memory operation has missing alias",
			tc: Case{
				Name: "preflight-late-missing-alias",
				Memories: []MemoryOp{
					{Operation: MemoryAdd, Content: "first", ResultAlias: "first"},
					{Operation: MemoryUpdate, Ref: "missing", Content: "updated"},
				},
			},
			want: `memory operation 1 for case "preflight-late-missing-alias": missing memory alias "missing"`,
		},
		{
			name: "concurrent operations contain update and delete",
			tc: Case{
				Name: "preflight-concurrent-invalid",
				ConcurrentMemories: []MemoryOp{
					{Operation: MemoryAdd, Content: "valid"},
					{Operation: MemoryUpdate},
					{Operation: MemoryDelete},
				},
			},
			want: "concurrent memory operations for case \"preflight-concurrent-invalid\": " +
				"concurrent memory operation 1: unsupported concurrent memory operation \"update\"\n" +
				"concurrent memory operation 2: unsupported concurrent memory operation \"delete\"",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend, sessionService, calls := newPreflightRecordingBackend(t)
			tc := test.tc
			addPreflightProbeState(&tc)

			_, err := Run(context.Background(), testRunNamespace, backend, tc)
			if test.want != "" {
				require.EqualError(t, err, test.want)
			} else {
				require.Error(t, err)
				for _, want := range test.wantContains {
					require.ErrorContains(t, err, want)
				}
			}
			require.Empty(t, calls.snapshot())

			got, getErr := sessionService.GetSession(
				context.Background(), replayKey(testRunNamespace, tc.Name),
			)
			require.NoError(t, getErr)
			require.Nil(t, got)
		})
	}
}

func TestRunRejectsInvalidStateMapPrefixesBeforePersistence(t *testing.T) {
	tests := []struct {
		name string
		tc   Case
		want string
	}{
		{
			name: "app state requires app prefix",
			tc: Case{
				Name: "app-state-missing-prefix",
				AppState: session.StateMap{
					"flag": []byte(`"flag"`),
				},
			},
			want: `app state for case "app-state-missing-prefix" has key "flag" without required prefix "app:"`,
		},
		{
			name: "app state rejects user prefix",
			tc: Case{
				Name: "app-state-user-prefix",
				AppState: session.StateMap{
					session.StateUserPrefix + "bad": []byte(`"bad"`),
				},
			},
			want: `app state for case "app-state-user-prefix" has key "user:bad" with disallowed prefix "user:"`,
		},
		{
			name: "app state rejects temp prefix",
			tc: Case{
				Name: "app-state-temp-prefix",
				AppState: session.StateMap{
					session.StateTempPrefix + "bad": []byte(`"bad"`),
				},
			},
			want: `app state for case "app-state-temp-prefix" has key "temp:bad" with disallowed prefix "temp:"`,
		},
		{
			name: "app state rejects unknown prefix",
			tc: Case{
				Name: "app-state-unknown-prefix",
				AppState: session.StateMap{
					"custom:bad": []byte(`"bad"`),
				},
			},
			want: `app state for case "app-state-unknown-prefix" has key "custom:bad" without required prefix "app:"`,
		},
		{
			name: "user state requires user prefix",
			tc: Case{
				Name: "user-state-missing-prefix",
				UserState: session.StateMap{
					"locale": []byte(`"locale"`),
				},
			},
			want: `user state for case "user-state-missing-prefix" has key "locale" without required prefix "user:"`,
		},
		{
			name: "user state rejects unknown prefix",
			tc: Case{
				Name: "user-state-unknown-prefix",
				UserState: session.StateMap{
					"custom:locale": []byte(`"locale"`),
				},
			},
			want: `user state for case "user-state-unknown-prefix" has key "custom:locale" without required prefix "user:"`,
		},
		{
			name: "user state rejects temp prefix",
			tc: Case{
				Name: "user-state-temp-prefix-new",
				UserState: session.StateMap{
					session.StateTempPrefix + "bad": []byte(`"bad"`),
				},
			},
			want: `user state for case "user-state-temp-prefix-new" has key "temp:bad" with disallowed prefix "temp:"`,
		},
		{
			name: "user state rejects app prefix",
			tc: Case{
				Name: "user-state-app-prefix",
				UserState: session.StateMap{
					session.StateAppPrefix + "bad":   []byte(`"bad"`),
					session.StateUserPrefix + "good": []byte(`"good"`),
				},
			},
			want: `user state for case "user-state-app-prefix" has key "app:bad" with disallowed prefix "app:"`,
		},
		{
			name: "user state rejects temp prefix",
			tc: Case{
				Name: "user-state-temp-prefix",
				UserState: session.StateMap{
					session.StateTempPrefix + "bad": []byte(`"bad"`),
				},
			},
			want: `user state for case "user-state-temp-prefix" has key "temp:bad" with disallowed prefix "temp:"`,
		},
		{
			name: "session state rejects app prefix",
			tc: Case{
				Name: "session-state-app-prefix",
				UserState: session.StateMap{
					session.StateUserPrefix + "good": []byte(`"good"`),
				},
				SessionState: session.StateMap{
					session.StateAppPrefix + "bad":   []byte(`"bad"`),
					session.StateTempPrefix + "good": []byte(`"good"`),
				},
			},
			want: `session state for case "session-state-app-prefix" has key "app:bad" with disallowed prefix "app:"`,
		},
		{
			name: "session state rejects user prefix",
			tc: Case{
				Name: "session-state-user-prefix",
				SessionState: session.StateMap{
					session.StateUserPrefix + "bad": []byte(`"bad"`),
					"session:good":                  []byte(`"good"`),
				},
			},
			want: `session state for case "session-state-user-prefix" has key "user:bad" with disallowed prefix "user:"`,
		},
		{
			name: "user state and sorted key take priority",
			tc: Case{
				Name: "state-prefix-priority",
				UserState: session.StateMap{
					session.StateTempPrefix + "later": []byte(`"later"`),
					session.StateAppPrefix + "first":  []byte(`"first"`),
				},
				SessionState: session.StateMap{
					session.StateAppPrefix + "also-invalid": []byte(`"bad"`),
				},
			},
			want: `user state for case "state-prefix-priority" has key "app:first" with disallowed prefix "app:"`,
		},
		{
			name: "session state sorted key takes priority",
			tc: Case{
				Name: "session-state-prefix-priority",
				SessionState: session.StateMap{
					session.StateUserPrefix + "later": []byte(`"later"`),
					session.StateAppPrefix + "first":  []byte(`"first"`),
				},
			},
			want: `session state for case "session-state-prefix-priority" has key "app:first" with disallowed prefix "app:"`,
		},
		{
			name: "app and user validation order",
			tc: Case{
				Name: "state-scope-order",
				AppState: session.StateMap{
					session.StateUserPrefix + "bad": []byte(`"bad"`),
				},
				UserState: session.StateMap{
					session.StateAppPrefix + "bad": []byte(`"bad"`),
				},
			},
			want: `app state for case "state-scope-order" has key "user:bad" with disallowed prefix "user:"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend, sessionService, calls := newPreflightRecordingBackend(t)
			tc := test.tc
			addPreflightProbeState(&tc)
			for i := 0; i < 50; i++ {
				_, preflightErr := prepareCase(backend, tc)
				require.EqualError(t, preflightErr, test.want, "iteration=%d", i)
			}

			_, err := Run(context.Background(), testRunNamespace, backend, tc)
			require.EqualError(t, err, test.want)
			require.Empty(t, calls.snapshot())

			got, getErr := sessionService.GetSession(
				context.Background(), replayKey(testRunNamespace, tc.Name),
			)
			require.NoError(t, getErr)
			require.Nil(t, got)
		})
	}
}

func TestRunAcceptsSessionStateKeysOutsideAppAndUserPrefixes(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()
	tc := Case{
		Name: "session-state-allowed-keys",
		SessionState: session.StateMap{
			"plain":                             []byte(`"plain"`),
			session.StateTempPrefix + "scratch": []byte(`"temp"`),
			"session:mode":                      []byte(`"custom"`),
		},
	}
	result, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "in_memory", SessionService: sessionService, MemoryService: memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
	}, tc)
	require.NoError(t, err)
	require.Equal(t, StateBytesSnapshot{Kind: "json", Value: "plain"}, result.Snapshot.State["plain"])
	require.Equal(t, StateBytesSnapshot{Kind: "json", Value: "temp"}, result.Snapshot.State[session.StateTempPrefix+"scratch"])
	require.Equal(t, StateBytesSnapshot{Kind: "json", Value: "custom"}, result.Snapshot.State["session:mode"])
}

func TestRunAllowsAdditionalScopePrefixAfterRequiredPrefix(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()
	tc := Case{
		Name: "additional-scope-prefix",
		AppState: session.StateMap{
			session.StateAppPrefix + session.StateAppPrefix + "flag": []byte(`true`),
		},
		UserState: session.StateMap{
			session.StateUserPrefix + session.StateUserPrefix + "locale": []byte(`"zh-CN"`),
		},
	}
	result, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "in_memory", SessionService: sessionService, MemoryService: memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
	}, tc)
	require.NoError(t, err)
	require.Contains(t, result.Snapshot.State, session.StateAppPrefix+session.StateAppPrefix+"flag")
	require.Contains(t, result.Snapshot.State, session.StateUserPrefix+session.StateUserPrefix+"locale")
	appState, err := sessionService.ListAppStates(context.Background(), result.Key.AppName)
	require.NoError(t, err)
	require.Contains(t, appState, session.StateAppPrefix+"flag")
	userState, err := sessionService.ListUserStates(context.Background(), session.UserKey{
		AppName: result.Key.AppName, UserID: result.Key.UserID,
	})
	require.NoError(t, err)
	require.Contains(t, userState, session.StateUserPrefix+"locale")
}

func TestCanonicalizeScopedStateDetectsSortedCollisionsAndCopiesValues(t *testing.T) {
	state := session.StateMap{
		"flag":                          []byte("raw"),
		session.StateAppPrefix + "flag": []byte("prefixed"),
		"other":                         []byte("other"),
	}
	_, err := canonicalizeScopedState(state, session.StateAppPrefix)
	require.EqualError(t, err,
		`duplicate canonical key "flag" from keys ["app:flag" "flag"]`)
	_, _, err = prepareExpectedDirectStateMaps(Case{
		Name:     "app-collision",
		AppState: state,
	})
	require.EqualError(t, err,
		`app state for case "app-collision" has duplicate canonical key "flag" from keys ["app:flag" "flag"]`)

	value := []byte("value")
	prepared, err := canonicalizeScopedState(session.StateMap{
		session.StateAppPrefix + "flag": value,
	}, session.StateAppPrefix)
	require.NoError(t, err)
	value[0] = 'X'
	require.Equal(t, []byte("value"), prepared["flag"])
}

func TestRunSkipsDirectStateScopeValidationWhenNoDirectState(t *testing.T) {
	base := sessinmemory.NewSessionService()
	defer base.Close()
	sessionService := &scopeSessionService{
		Service:       base,
		listAppErr:    errors.New("ListAppStates must not be called"),
		listUserErr:   errors.New("ListUserStates must not be called"),
		createPeerErr: errors.New("peer must not be created"),
	}
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()
	_, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "no-direct-state", SessionService: sessionService,
		MemoryService: memoryService, ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{
		Name: "no-direct-state",
		Events: []*event.Event{{StateDelta: session.StateMap{
			session.StateAppPrefix + "event":  []byte(`"app"`),
			session.StateUserPrefix + "event": []byte(`"user"`),
		}}},
	})
	require.NoError(t, err)
}

func TestRunRejectsStateCollisionInputsAtRequiredPrefixStage(t *testing.T) {
	tests := []struct {
		name string
		tc   Case
		want string
	}{
		{
			name: "app collision-shaped input",
			tc: Case{
				Name: "app-collision-shaped",
				AppState: session.StateMap{
					"flag":                          []byte(`"raw"`),
					session.StateAppPrefix + "flag": []byte(`"prefixed"`),
				},
			},
			want: `app state for case "app-collision-shaped" has key "flag" without required prefix "app:"`,
		},
		{
			name: "user collision-shaped input",
			tc: Case{
				Name: "user-collision-shaped",
				UserState: session.StateMap{
					"locale":                           []byte(`"raw"`),
					session.StateUserPrefix + "locale": []byte(`"prefixed"`),
				},
			},
			want: `user state for case "user-collision-shaped" has key "locale" without required prefix "user:"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend, sessionService, calls := newPreflightRecordingBackend(t)
			_, err := Run(context.Background(), testRunNamespace, backend, test.tc)
			require.EqualError(t, err, test.want)
			require.Empty(t, calls.snapshot())
			got, getErr := sessionService.GetSession(context.Background(), replayKey(testRunNamespace, test.tc.Name))
			require.NoError(t, getErr)
			require.Nil(t, got)
		})
	}
}

func TestPrepareCaseValidationPriority(t *testing.T) {
	two := 2
	validEvent := replayTimelineEvent(
		"valid", time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC),
	)
	invalidMemory := []MemoryOp{{Name: "bad", Operation: MemoryOperation("replace")}}
	invalidConcurrentMemory := []MemoryOp{{Operation: MemoryUpdate}}
	invalidState := session.StateMap{
		session.StateAppPrefix + "bad": []byte(`"bad"`),
	}
	tests := []struct {
		name string
		tc   Case
		want string
	}{
		{
			name: "track before event",
			tc: Case{
				Name: "priority-track", Tracks: []TrackSpec{{Name: " "}},
				Events: []*event.Event{nil}, Summaries: []SummaryStep{{EventPrefix: &two}},
				SessionState: invalidState,
				Memories:     invalidMemory, ConcurrentMemories: invalidConcurrentMemory,
			},
			want: `track 0 for case "priority-track" has empty name`,
		},
		{
			name: "event before summary",
			tc: Case{
				Name: "priority-event", Events: []*event.Event{nil},
				Summaries:    []SummaryStep{{EventPrefix: &two}},
				SessionState: invalidState,
				Memories:     invalidMemory, ConcurrentMemories: invalidConcurrentMemory,
			},
			want: `event 0 for case "priority-event" is nil`,
		},
		{
			name: "summary before sequential memory",
			tc: Case{
				Name: "priority-summary", Events: []*event.Event{validEvent},
				Summaries:    []SummaryStep{{EventPrefix: &two}},
				SessionState: invalidState,
				Memories:     invalidMemory, ConcurrentMemories: invalidConcurrentMemory,
			},
			want: `summary step 0 for case "priority-summary" has event prefix 2 outside [0,1]`,
		},
		{
			name: "state before sequential memory",
			tc: Case{
				Name: "priority-state", Events: []*event.Event{validEvent},
				SessionState: invalidState,
				Memories:     invalidMemory, ConcurrentMemories: invalidConcurrentMemory,
			},
			want: `session state for case "priority-state" has key "app:bad" with disallowed prefix "app:"`,
		},
		{
			name: "sequential before concurrent memory",
			tc: Case{
				Name: "priority-sequential", Events: []*event.Event{validEvent},
				Memories: invalidMemory, ConcurrentMemories: invalidConcurrentMemory,
			},
			want: `memory operation 0 for case "priority-sequential": unknown memory operation "replace" (bad)`,
		},
		{
			name: "concurrent memory last",
			tc: Case{
				Name: "priority-concurrent", Events: []*event.Event{validEvent},
				ConcurrentMemories: invalidConcurrentMemory,
			},
			want: `concurrent memory operations for case "priority-concurrent": concurrent memory operation 0: unsupported concurrent memory operation "update"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend, sessionService, calls := newPreflightRecordingBackend(t)
			tc := test.tc
			addPreflightProbeState(&tc)

			_, err := Run(context.Background(), testRunNamespace, backend, tc)
			require.EqualError(t, err, test.want)
			require.Empty(t, calls.snapshot())
			got, getErr := sessionService.GetSession(
				context.Background(), replayKey(testRunNamespace, tc.Name),
			)
			require.NoError(t, getErr)
			require.Nil(t, got)
		})
	}
}

func TestValidateSequentialMemoryOperationPreservesAliasSemantics(t *testing.T) {
	aliases := newMemoryAliasRegistry[canonicalMemoryIdentity]()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryAdd, ResultAlias: "current",
	}))
	current, err := aliases.resolve("current")
	require.NoError(t, err)

	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryUpdate, Ref: "current", Content: "updated", ResultAlias: "next",
	}))
	next, err := aliases.resolve("next")
	require.NoError(t, err)
	require.Same(t, current, next)

	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryDelete, Ref: "current",
	}))
	_, err = aliases.resolve("current")
	require.EqualError(t, err, `memory alias "current" refers to deleted memory`)
	_, err = aliases.resolve("next")
	require.EqualError(t, err, `memory alias "next" refers to deleted memory`)

	// A later Add may explicitly rebind a dead alias without reviving the other
	// aliases that belonged to the deleted group.
	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryAdd, Content: "replacement", ResultAlias: "current",
	}))
	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryUpdate, Ref: "current", Content: "replacement-2",
	}))
	_, err = aliases.resolve("next")
	require.EqualError(t, err, `memory alias "next" refers to deleted memory`)
}

func TestValidateSequentialMemoryOperationSharesIdempotentIdentityGroup(t *testing.T) {
	aliases := newMemoryAliasRegistry[canonicalMemoryIdentity]()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryAdd, Content: "same", Topics: []string{"one"}, ResultAlias: "first",
	}))
	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryAdd, Content: "same", Topics: []string{"two"}, ResultAlias: "second",
	}))
	first, err := aliases.resolve("first")
	require.NoError(t, err)
	second, err := aliases.resolve("second")
	require.NoError(t, err)
	require.Same(t, first, second)
	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryDelete, Ref: "second",
	}))
	_, err = aliases.resolve("first")
	require.EqualError(t, err, `memory alias "first" refers to deleted memory`)
}

func TestValidateSequentialMemoryOperationInheritsMetadataOnUpdate(t *testing.T) {
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	eventTime := time.Date(2026, time.July, 1, 1, 2, 3, 0, time.FixedZone("UTC+8", 8*60*60))
	metadata := &memory.Metadata{
		Kind:         memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"User", "Ada"},
		Location:     " Shenzhen ",
	}

	// The backend keeps all metadata when UpdateMemory is called without an
	// update metadata patch. The preflight identity must do the same.
	aliases := newMemoryAliasRegistry[canonicalMemoryIdentity]()
	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryAdd, Content: "before", Metadata: metadata, ResultAlias: "first",
	}))
	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryUpdate, Ref: "first", Content: "after",
	}))
	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryAdd, Content: "after", Metadata: metadata, ResultAlias: "second",
	}))

	first, err := aliases.resolve("first")
	require.NoError(t, err)
	second, err := aliases.resolve("second")
	require.NoError(t, err)
	require.Same(t, first, second)

	// Deleting either alias must invalidate the shared group.
	require.NoError(t, validateSequentialMemoryOperation(aliases, userKey, MemoryOp{
		Operation: MemoryDelete, Ref: "second",
	}))
	_, err = aliases.resolve("first")
	require.EqualError(t, err, `memory alias "first" refers to deleted memory`)
}

func TestCanonicalMemoryUpdateIdentityAppliesMetadataPatch(t *testing.T) {
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	eventTime := time.Date(2026, time.July, 1, 1, 2, 3, 0, time.UTC)
	previous := canonicalMemoryOpIdentity(userKey, MemoryOp{
		Content: "before",
		Metadata: &memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Alice", "Bob"},
			Location:     "Kyoto",
		},
	})
	got := canonicalMemoryUpdateIdentity(userKey, MemoryOp{
		Content:  "after",
		Metadata: &memory.Metadata{Location: "Osaka"},
	}, previous)
	want := canonicalMemoryOpIdentity(userKey, MemoryOp{
		Content: "after",
		Metadata: &memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Alice", "Bob"},
			Location:     "Osaka",
		},
	})
	require.Equal(t, want, got)
}

func TestRunRejectsDeletedMemoryAliasesBeforeBackendCalls(t *testing.T) {
	tests := []struct {
		name string
		tc   Case
		want string
	}{
		{
			name: "idempotent aliases share delete lifecycle",
			tc: Case{
				Name: "deleted-idempotent-alias",
				Memories: []MemoryOp{
					{Operation: MemoryAdd, Content: "same", ResultAlias: "first"},
					{Operation: MemoryAdd, Content: "same", Topics: []string{"different"}, ResultAlias: "second"},
					{Operation: MemoryDelete, Ref: "second"},
					{Operation: MemoryUpdate, Ref: "first", Content: "updated"},
				},
			},
			want: `memory operation 3 for case "deleted-idempotent-alias": memory alias "first" refers to deleted memory`,
		},
		{
			name: "repeated delete",
			tc: Case{
				Name: "deleted-repeated-delete",
				Memories: []MemoryOp{
					{Operation: MemoryAdd, Content: "same", ResultAlias: "memory"},
					{Operation: MemoryDelete, Ref: "memory"},
					{Operation: MemoryDelete, Ref: "memory"},
				},
			},
			want: `memory operation 2 for case "deleted-repeated-delete": memory alias "memory" refers to deleted memory`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend, sessionService, calls := newPreflightRecordingBackend(t)
			_, err := Run(context.Background(), testRunNamespace, backend, test.tc)
			require.EqualError(t, err, test.want)
			require.Empty(t, calls.snapshot())
			got, getErr := sessionService.GetSession(context.Background(), replayKey(testRunNamespace, test.tc.Name))
			require.NoError(t, getErr)
			require.Nil(t, got)
		})
	}
}

func TestApplyMemoriesConcurrentlyAggregatesErrorsDeterministically(t *testing.T) {
	firstErr := errors.New("first add failed")
	thirdErr := errors.New("third add failed")
	service := &concurrentFailureMemoryService{
		Service: meminmemory.NewMemoryService(),
		failures: map[string]error{
			"first": firstErr,
			"third": thirdErr,
		},
	}
	defer service.Close()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	ops := []MemoryOp{
		{Operation: MemoryAdd, Content: "first"},
		{Operation: MemoryAdd, Content: "success"},
		{Operation: MemoryAdd, Content: "third"},
	}
	want := "concurrent memory operation 0: first add failed\n" +
		"concurrent memory operation 2: third add failed"

	for i := 0; i < 20; i++ {
		err := applyMemoriesConcurrently(context.Background(), service, userKey, ops)
		require.EqualError(t, err, want)
		require.ErrorIs(t, err, firstErr)
		require.ErrorIs(t, err, thirdErr)
	}
}

func TestPrepareSummaryTargets(t *testing.T) {
	zero, one, two, negative, tooLarge := 0, 1, 2, -1, 3
	tests := []struct {
		name      string
		summaries []SummaryStep
		want      []int
		wantErr   string
	}{
		{name: "no summaries", want: []int{}},
		{name: "nil means all", summaries: []SummaryStep{{}}, want: []int{2}},
		{name: "zero", summaries: []SummaryStep{{EventPrefix: &zero}}, want: []int{0}},
		{
			name: "same and increasing prefixes",
			summaries: []SummaryStep{
				{EventPrefix: &one},
				{EventPrefix: &one},
				{EventPrefix: &two},
				{},
			},
			want: []int{1, 1, 2, 2},
		},
		{
			name: "negative", summaries: []SummaryStep{{EventPrefix: &negative}},
			wantErr: `summary step 0 for case "timeline" has event prefix -1 outside [0,2]`,
		},
		{
			name: "later prefix too large",
			summaries: []SummaryStep{
				{EventPrefix: &one},
				{EventPrefix: &tooLarge},
			},
			wantErr: `summary step 1 for case "timeline" has event prefix 3 outside [0,2]`,
		},
		{
			name: "later prefix decreases",
			summaries: []SummaryStep{
				{EventPrefix: &two},
				{EventPrefix: &one},
			},
			wantErr: `summary step 1 for case "timeline" has event prefix 1 before already appended prefix 2`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tc := Case{
				Name: "timeline", Events: []*event.Event{{}, {}}, Summaries: test.summaries,
			}
			got, err := prepareSummaryTargets(tc)
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestRunInterleavesEventsAndSummaries(t *testing.T) {
	summarizer := &recordingSummarizer{}
	baseSessionService := sessinmemory.NewSessionService(sessinmemory.WithSummarizer(summarizer))
	defer baseSessionService.Close()
	sessionService := &recordingSummarySessionService{Service: baseSessionService}
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	firstPrefix, finalPrefix := 2, 4
	baseTime := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	events := make([]*event.Event, 0, finalPrefix)
	for i, id := range []string{"event-0", "event-1", "event-2", "event-3"} {
		events = append(events, replayTimelineEvent(id, baseTime.Add(time.Duration(i)*time.Second)))
	}
	result, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "in_memory", SessionService: sessionService, MemoryService: memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
		CreateSummary: func(ctx context.Context, sess *session.Session, step SummaryStep) error {
			return summarizer.createSummary(ctx, sessionService, sess, step)
		},
	}, Case{
		Name:   "summary-timeline",
		Events: events,
		Summaries: []SummaryStep{
			{Name: "first", Force: true, Text: "first summary", EventPrefix: &firstPrefix},
			{Name: "second", Force: true, Text: "second summary", EventPrefix: &finalPrefix},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []int{2, 4}, sessionService.eventCounts)
	require.Len(t, result.Snapshot.Events, 4)
	summary := result.Snapshot.Summary[session.SummaryFilterKeyAllContents]
	require.Equal(t, "second summary", summary.Summary)
	require.NotNil(t, summary.Boundary)
	require.Equal(t, finalPrefix-1, *summary.Boundary.LastEventIndex)
	require.Equal(t, normalizeTime(baseTime.Add(3*time.Second)), summary.Boundary.CutoffAt)
}

func TestRunScopesSummaryTextAcrossConcurrentRuns(t *testing.T) {
	firstNamespace := "summary-concurrent-first"
	secondNamespace := "summary-concurrent-second"
	baseTime := time.Now().UTC().Add(time.Minute)
	firstCase := Case{
		Name: "summary-first",
		Events: []*event.Event{
			replayTimelineEvent("first-event", baseTime),
		},
		Summaries: []SummaryStep{{Force: true, Text: "first summary"}},
	}
	secondCase := Case{
		Name: "summary-second",
		Events: []*event.Event{
			replayTimelineEvent("second-event", baseTime.Add(time.Second)),
		},
		Summaries: []SummaryStep{{Force: true, Text: "second summary"}},
	}

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseFirst) })
	}
	defer release()
	firstKey := replayKey(firstNamespace, firstCase.Name)
	summarizer := &recordingSummarizer{
		blockSessionID: firstKey.SessionID + ":",
		entered:        firstEntered,
		release:        releaseFirst,
	}
	sessionService := sessinmemory.NewSessionService(sessinmemory.WithSummarizer(summarizer))
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()
	backend := Backend{
		Name: "in_memory", SessionService: sessionService, MemoryService: memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
		CreateSummary: func(ctx context.Context, sess *session.Session, step SummaryStep) error {
			return summarizer.createSummary(ctx, sessionService, sess, step)
		},
	}

	type runResult struct {
		result Result
		err    error
	}
	firstResult := make(chan runResult, 1)
	secondResult := make(chan runResult, 1)
	go func() {
		result, err := Run(context.Background(), firstNamespace, backend, firstCase)
		firstResult <- runResult{result: result, err: err}
	}()

	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first summary did not reach the coordinated interleaving point")
	}
	go func() {
		result, err := Run(context.Background(), secondNamespace, backend, secondCase)
		secondResult <- runResult{result: result, err: err}
	}()

	var second runResult
	select {
	case second = <-secondResult:
	case <-time.After(5 * time.Second):
		t.Fatal("second summary did not complete while the first was blocked")
	}
	require.NoError(t, second.err)
	require.Equal(t, "second summary", second.result.Snapshot.Summary[session.SummaryFilterKeyAllContents].Summary)
	release()

	var first runResult
	select {
	case first = <-firstResult:
	case <-time.After(5 * time.Second):
		t.Fatal("first summary did not complete after release")
	}
	require.NoError(t, first.err)
	require.Equal(t, "first summary", first.result.Snapshot.Summary[session.SummaryFilterKeyAllContents].Summary)
}

func TestRunWrapsCreateSummaryCallbackError(t *testing.T) {
	summaryErr := errors.New("summary callback failed")
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	_, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "summary_callback", SessionService: sessionService, MemoryService: memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
		CreateSummary: func(context.Context, *session.Session, SummaryStep) error {
			return summaryErr
		},
	}, Case{
		Name: "summary-callback-error",
		Events: []*event.Event{
			replayTimelineEvent("event", time.Now().UTC()),
		},
		Summaries: []SummaryStep{{Text: "summary"}},
	})
	require.ErrorIs(t, err, summaryErr)
	require.ErrorContains(t, err, `summary step 0 for case "summary-callback-error"`)
}

func TestRunAcceptsFullTrackPayloadDomain(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	result, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "in_memory", SessionService: sessionService,
		TrackService: sessionService, MemoryService: memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{
		Name: "track-payload-domain",
		Tracks: []TrackSpec{
			{Name: "domain", Payload: map[string]any{"value": json.Number("9007199254740993")}},
			{Name: "domain", Payload: []any{"array", json.Number("2")}},
			{Name: "domain", Payload: "scalar"},
			{Name: "domain", Payload: json.Number("9007199254740993")},
			{Name: "domain", Payload: true},
			{Name: "domain", Payload: nil},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Snapshot.Tracks, 1)
	track := result.Snapshot.Tracks[0]
	require.Equal(t, "domain", track.Name)
	require.Equal(t, "domain", track.Track)
	require.Equal(t, []TrackPayloadSnapshot{
		{Kind: "json", Value: map[string]any{"value": json.Number("9007199254740993")}},
		{Kind: "json", Value: []any{"array", json.Number("2")}},
		{Kind: "json", Value: "scalar"},
		{Kind: "json", Value: json.Number("9007199254740993")},
		{Kind: "json", Value: true},
		{Kind: "json", Value: nil},
	}, trackPayloads(track.Events))
}

func TestRunRejectsNilSessionBeforeTrackAppend(t *testing.T) {
	tests := []struct {
		name                  string
		caseName              string
		trackCount            int
		nilGetSessionCall     int
		wantGetSessionCalls   int
		wantTrackServiceCalls int
		wantErr               string
	}{
		{
			name:                  "nil on first track read",
			caseName:              "nil-track-session-first",
			trackCount:            1,
			nilGetSessionCall:     1,
			wantGetSessionCalls:   1,
			wantTrackServiceCalls: 0,
			wantErr:               `get session before track 0 for case "nil-track-session-first" returned nil`,
		},
		{
			name:                  "nil on second track read",
			caseName:              "nil-track-session-second",
			trackCount:            2,
			nilGetSessionCall:     2,
			wantGetSessionCalls:   2,
			wantTrackServiceCalls: 1,
			wantErr:               `get session before track 1 for case "nil-track-session-second" returned nil`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getSessionCalls := 0
			sessionService := sessinmemory.NewSessionService(
				sessinmemory.WithGetSessionHook(func(
					_ *session.GetSessionContext,
					next func() (*session.Session, error),
				) (*session.Session, error) {
					getSessionCalls++
					if getSessionCalls == test.nilGetSessionCall {
						return nil, nil
					}
					return next()
				}),
			)
			defer sessionService.Close()
			memoryService := meminmemory.NewMemoryService()
			defer memoryService.Close()
			trackService := &recordingTrackService{delegate: sessionService}
			tracks := make([]TrackSpec, test.trackCount)
			for i := range tracks {
				tracks[i] = TrackSpec{
					Name:      "nil-session-track",
					Payload:   map[string]any{"index": i},
					Timestamp: time.Date(2026, 8, 4, 0, 0, i, 0, time.UTC),
				}
			}

			_, err := Run(context.Background(), testRunNamespace, Backend{
				Name:            "in_memory",
				SessionService:  sessionService,
				TrackService:    trackService,
				MemoryService:   memoryService,
				ReadAllMemories: completeMemoryReader(memoryService),
			}, Case{Name: test.caseName, Tracks: tracks})
			require.EqualError(t, err, test.wantErr)
			require.Equal(t, test.wantGetSessionCalls, getSessionCalls)
			require.Equal(t, test.wantTrackServiceCalls, trackService.calls)
		})
	}
}

func TestRunPropagatesBackendErrors(t *testing.T) {
	tests := []struct {
		name string
		wrap func(*failingMemoryService)
		tc   Case
		want string
	}{
		{
			name: "add memory",
			wrap: func(service *failingMemoryService) { service.addErr = errors.New("add failed") },
			tc: Case{Name: "add", Memories: []MemoryOp{{
				Operation: MemoryAdd, Content: "memory",
			}}},
			want: `memory operation 0 for case "add": add failed`,
		},
		{
			name: "search memory",
			wrap: func(service *failingMemoryService) { service.searchErr = errors.New("search failed") },
			tc: Case{Name: "search", Queries: []MemoryQuery{{
				Query: "memory",
			}}},
			want: `memory query 0 for case "search": search failed`,
		},
		{
			name: "read final memories",
			wrap: func(service *failingMemoryService) { service.readErr = errors.New("read failed") },
			tc:   Case{Name: "read"},
			want: `read final memories for case "read": read failed`,
		},
		{
			name: "read memories for alias",
			wrap: func(service *failingMemoryService) { service.readErr = errors.New("alias read failed") },
			tc: Case{Name: "read-alias", Memories: []MemoryOp{{
				Operation: MemoryAdd, Content: "memory", ResultAlias: "memory",
			}}},
			want: `memory operation 0 for case "read-alias": alias read failed`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessionService := sessinmemory.NewSessionService()
			defer sessionService.Close()
			memoryService := &failingMemoryService{Service: meminmemory.NewMemoryService()}
			defer memoryService.Close()
			test.wrap(memoryService)
			_, err := Run(context.Background(), testRunNamespace, Backend{
				Name:            "in_memory",
				SessionService:  sessionService,
				MemoryService:   memoryService,
				ReadAllMemories: completeMemoryReader(memoryService),
			}, test.tc)
			require.EqualError(t, err, test.want)
		})
	}
}

func TestRunAddsAndDeletesAliasedMemory(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	result, err := Run(context.Background(), testRunNamespace, Backend{
		Name:            "in_memory",
		SessionService:  sessionService,
		MemoryService:   memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{
		Name: "delete-memory",
		Memories: []MemoryOp{
			{
				Name: "add", Operation: MemoryAdd,
				Content: "temporary memory", ResultAlias: "temporary",
			},
			{
				Name: "delete", Operation: MemoryDelete, Ref: "temporary",
			},
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Snapshot.Memory)
}

func TestRunRefreshesUpdatedMemoryAlias(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	result, err := Run(context.Background(), testRunNamespace, Backend{
		Name:            "in_memory",
		SessionService:  sessionService,
		MemoryService:   memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{
		Name: "update-memory-alias",
		Memories: []MemoryOp{
			{Operation: MemoryAdd, Content: "memory v1", ResultAlias: "memory"},
			{Operation: MemoryUpdate, Ref: "memory", Content: "memory v2"},
			{Operation: MemoryUpdate, Ref: "memory", Content: "memory v3"},
			{Operation: MemoryDelete, Ref: "memory"},
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Snapshot.Memory)
}

func TestRunMigratesAllAliasesAfterMemoryIDRotation(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	result, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "in_memory", SessionService: sessionService,
		MemoryService: memoryService, ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{
		Name: "rotated-alias-group",
		Memories: []MemoryOp{
			{Operation: MemoryAdd, Content: "v1", ResultAlias: "first"},
			{Operation: MemoryUpdate, Ref: "first", Content: "v2", ResultAlias: "second"},
			{Operation: MemoryUpdate, Ref: "second", Content: "v3"},
			{Operation: MemoryDelete, Ref: "first"},
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Snapshot.Memory)
}

func TestRuntimeMemoryAliasRegistryRejectsDeadAliasesBeforeServiceCall(t *testing.T) {
	base := meminmemory.NewMemoryService()
	defer base.Close()
	service := &recordingMemoryService{Service: base}
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	aliases := newMemoryAliasRegistry[string]()
	readAll := completeMemoryReader(service)

	require.NoError(t, applyMemoryOp(context.Background(), service, readAll, userKey, aliases, MemoryOp{
		Operation: MemoryAdd, Content: "memory", ResultAlias: "first",
	}))
	require.NoError(t, applyMemoryOp(context.Background(), service, readAll, userKey, aliases, MemoryOp{
		Operation: MemoryDelete, Ref: "first",
	}))
	before := service.updateCalls
	err := applyMemoryOp(context.Background(), service, readAll, userKey, aliases, MemoryOp{
		Operation: MemoryUpdate, Ref: "first", Content: "should-not-call",
	})
	require.EqualError(t, err, `memory alias "first" refers to deleted memory`)
	require.Equal(t, before, service.updateCalls)
}

func TestRuntimeMemoryAliasRegistryPreservesStateWhenDeleteFails(t *testing.T) {
	base := meminmemory.NewMemoryService()
	service := &failDeleteOnceMemoryService{Service: base, err: errors.New("delete failed")}
	defer service.Close()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	aliases := newMemoryAliasRegistry[string]()
	readAll := completeMemoryReader(service)

	require.NoError(t, applyMemoryOp(context.Background(), service, readAll, userKey, aliases, MemoryOp{
		Operation: MemoryAdd, Content: "retry-delete", ResultAlias: "memory",
	}))
	err := applyMemoryOp(context.Background(), service, readAll, userKey, aliases, MemoryOp{
		Operation: MemoryDelete, Ref: "memory",
	})
	require.EqualError(t, err, "delete failed")
	_, err = aliases.resolve("memory")
	require.NoError(t, err)
	require.NoError(t, applyMemoryOp(context.Background(), service, readAll, userKey, aliases, MemoryOp{
		Operation: MemoryDelete, Ref: "memory",
	}))
	_, err = aliases.resolve("memory")
	require.EqualError(t, err, `memory alias "memory" refers to deleted memory`)
	require.Equal(t, 2, service.calls)
}

func TestRuntimeMemoryAliasRegistryPreservesStateOnEmptyUpdateID(t *testing.T) {
	base := meminmemory.NewMemoryService()
	service := &emptyUpdateResultMemoryService{Service: base}
	defer service.Close()
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	aliases := newMemoryAliasRegistry[string]()
	readAll := completeMemoryReader(service)

	require.NoError(t, applyMemoryOp(context.Background(), service, readAll, userKey, aliases, MemoryOp{
		Operation: MemoryAdd, Content: "before-empty-id", ResultAlias: "memory",
	}))
	group, err := aliases.resolve("memory")
	require.NoError(t, err)
	oldID := group.key
	err = applyMemoryOp(context.Background(), service, readAll, userKey, aliases, MemoryOp{
		Operation: MemoryUpdate, Ref: "memory", Content: "after-empty-id",
	})
	require.EqualError(t, err, "memory update returned empty ID")
	group, err = aliases.resolve("memory")
	require.NoError(t, err)
	require.Equal(t, oldID, group.key)
}

func TestRunRejectsEmptyUpdatedMemoryID(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := &emptyUpdateResultMemoryService{Service: meminmemory.NewMemoryService()}
	defer memoryService.Close()

	_, err := Run(context.Background(), testRunNamespace, Backend{
		Name:            "empty_update_result",
		SessionService:  sessionService,
		MemoryService:   memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{
		Name: "empty-update-result",
		Memories: []MemoryOp{
			{Operation: MemoryAdd, Content: "memory v1", ResultAlias: "memory"},
			{Operation: MemoryUpdate, Ref: "memory", Content: "memory v2"},
		},
	})
	require.EqualError(t, err, `memory operation 1 for case "empty-update-result": memory update returned empty ID`)
}

func TestRunResolvesAddAliasByCanonicalIdentity(t *testing.T) {
	firstTime := time.Date(2026, time.July, 1, 1, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Hour)
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := &orderedReadMemoryService{Service: meminmemory.NewMemoryService()}
	defer memoryService.Close()

	result, err := Run(context.Background(), testRunNamespace, Backend{
		Name:            "ordered_memory",
		SessionService:  sessionService,
		MemoryService:   memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{
		Name: "same-content-episodes",
		Memories: []MemoryOp{
			{
				Operation: MemoryAdd, Content: "User attended a project review.", ResultAlias: "first",
				Metadata: &memory.Metadata{
					Kind: memory.KindEpisode, EventTime: &firstTime,
					Participants: []string{"User", "Ada"}, Location: "Shenzhen",
				},
			},
			{
				Operation: MemoryAdd, Content: "User attended a project review.", ResultAlias: "second",
				Metadata: &memory.Metadata{
					Kind: memory.KindEpisode, EventTime: &secondTime,
					Participants: []string{"User", "Bob"}, Location: "Guangzhou",
				},
			},
			{Operation: MemoryDelete, Ref: "second"},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Snapshot.Memory, 1)
	remaining := result.Snapshot.Memory[0]
	require.Equal(t, normalizeTime(firstTime), remaining.EventTime)
	require.Equal(t, []string{"Ada", "User"}, remaining.Participants)
	require.Equal(t, "Shenzhen", remaining.Location)
}

func TestRunResolvesIdempotentAddAlias(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	result, err := Run(context.Background(), testRunNamespace, Backend{
		Name:            "in_memory",
		SessionService:  sessionService,
		MemoryService:   memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{
		Name: "idempotent-add-alias",
		Memories: []MemoryOp{
			{Operation: MemoryAdd, Content: "same memory", ResultAlias: "first"},
			{Operation: MemoryAdd, Content: "same memory", ResultAlias: "second"},
			{Operation: MemoryDelete, Ref: "second"},
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Snapshot.Memory)
}

func TestFindMemoryIDRejectsAmbiguousIdentity(t *testing.T) {
	at := time.Date(2026, time.July, 1, 1, 0, 0, 0, time.UTC)
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	newEntry := func(id string) *memory.Entry {
		return &memory.Entry{
			ID: id, AppName: userKey.AppName, UserID: userKey.UserID,
			Memory: &memory.Memory{
				Memory: "same episode", Kind: memory.KindEpisode, EventTime: &at,
				Participants: []string{"Alice"}, Location: "Kyoto",
			},
		}
	}
	service := &fixedReadMemoryService{entries: []*memory.Entry{newEntry("z-id"), newEntry("a-id")}}
	_, err := findMemoryID(context.Background(), completeMemoryReader(service), userKey, MemoryOp{
		Content: "same episode",
		Metadata: &memory.Metadata{
			Kind: memory.KindEpisode, EventTime: &at,
			Participants: []string{"Alice"}, Location: "Kyoto",
		},
	})
	require.ErrorContains(t, err, `ambiguous across IDs ["a-id" "z-id"]`)
}

func TestFindMemoryIDReportsResolutionErrors(t *testing.T) {
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	op := MemoryOp{Content: "target"}
	tests := []struct {
		name    string
		service *fixedReadMemoryService
		want    string
	}{
		{
			name:    "read failure",
			service: &fixedReadMemoryService{readErr: errors.New("read failed")},
			want:    "read failed",
		},
		{
			name:    "nil entry",
			service: &fixedReadMemoryService{entries: []*memory.Entry{nil}},
			want:    "memory entry 0 is nil",
		},
		{
			name: "nil memory",
			service: &fixedReadMemoryService{entries: []*memory.Entry{{
				ID: "nil-memory", AppName: "app", UserID: "user",
			}}},
			want: "memory entry 0 has nil Memory",
		},
		{
			name: "identity not found",
			service: &fixedReadMemoryService{entries: []*memory.Entry{
				{ID: "other", AppName: "app", UserID: "user", Memory: &memory.Memory{Memory: "other"}},
			}},
			want: "not found",
		},
		{
			name: "empty id",
			service: &fixedReadMemoryService{entries: []*memory.Entry{{
				AppName: "app", UserID: "user", Memory: &memory.Memory{Memory: "target"},
			}}},
			want: "resolved to empty ID",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := findMemoryID(context.Background(), completeMemoryReader(test.service), userKey, op)
			require.ErrorContains(t, err, test.want)
		})
	}
	require.Equal(t, canonicalMemoryIdentity{AppName: "app", UserID: "user"}, newCanonicalMemoryIdentity("app", "user", nil))
	require.Equal(t, memory.Kind("custom"), canonicalIdentityKind(memory.Kind("custom"), false, false, false))
}

func TestCanonicalMemoryIdentityMatchesRuntimeIDs(t *testing.T) {
	baseTime := time.Date(2026, time.July, 1, 1, 2, 3, 100, time.UTC)
	nextDay := baseTime.Add(24 * time.Hour)
	sameInstant := baseTime.In(time.FixedZone("UTC+8", 8*60*60))
	sameSecond := baseTime.Add(500 * time.Millisecond)
	defaultKey := memory.UserKey{AppName: "app", UserID: "user"}

	tests := []struct {
		name      string
		leftKey   memory.UserKey
		rightKey  memory.UserKey
		left      MemoryOp
		right     MemoryOp
		wantEqual bool
	}{
		{
			name: "content differs", leftKey: defaultKey, rightKey: defaultKey,
			left: MemoryOp{Content: "coffee"}, right: MemoryOp{Content: "tea"},
		},
		{
			name: "topics excluded", leftKey: defaultKey, rightKey: defaultKey,
			left:  MemoryOp{Content: "coffee", Topics: []string{"drink"}},
			right: MemoryOp{Content: "coffee", Topics: []string{"preference"}}, wantEqual: true,
		},
		{
			name: "episode time differs", leftKey: defaultKey, rightKey: defaultKey,
			left:  MemoryOp{Content: "meeting", Metadata: &memory.Metadata{Kind: memory.KindEpisode, EventTime: &baseTime}},
			right: MemoryOp{Content: "meeting", Metadata: &memory.Metadata{Kind: memory.KindEpisode, EventTime: &nextDay}},
		},
		{
			name: "participants canonicalized", leftKey: defaultKey, rightKey: defaultKey,
			left:  MemoryOp{Content: "meeting", Metadata: &memory.Metadata{Kind: memory.KindEpisode, Participants: []string{"Alice", "Bob"}}},
			right: MemoryOp{Content: "meeting", Metadata: &memory.Metadata{Kind: memory.KindEpisode, Participants: []string{"bob", " Alice ", "Bob"}}}, wantEqual: true,
		},
		{
			name: "user differs", leftKey: defaultKey,
			rightKey: memory.UserKey{AppName: "app", UserID: "other"},
			left:     MemoryOp{Content: "coffee"}, right: MemoryOp{Content: "coffee"},
		},
		{
			name: "app differs", leftKey: defaultKey,
			rightKey: memory.UserKey{AppName: "other", UserID: "user"},
			left:     MemoryOp{Content: "coffee"}, right: MemoryOp{Content: "coffee"},
		},
		{
			name: "explicit fact keeps legacy identity", leftKey: defaultKey, rightKey: defaultKey,
			left:  MemoryOp{Content: "coffee"},
			right: MemoryOp{Content: "coffee", Metadata: &memory.Metadata{Kind: memory.KindFact}}, wantEqual: true,
		},
		{
			name: "blank participants are removed before identity", leftKey: defaultKey, rightKey: defaultKey,
			left:  MemoryOp{Content: "coffee"},
			right: MemoryOp{Content: "coffee", Metadata: &memory.Metadata{Participants: []string{" "}}}, wantEqual: true,
		},
		{
			name: "equivalent timezone", leftKey: defaultKey, rightKey: defaultKey,
			left:  MemoryOp{Content: "meeting", Metadata: &memory.Metadata{Kind: memory.KindEpisode, EventTime: &baseTime}},
			right: MemoryOp{Content: "meeting", Metadata: &memory.Metadata{Kind: memory.KindEpisode, EventTime: &sameInstant}}, wantEqual: true,
		},
		{
			name: "subsecond excluded", leftKey: defaultKey, rightKey: defaultKey,
			left:  MemoryOp{Content: "meeting", Metadata: &memory.Metadata{Kind: memory.KindEpisode, EventTime: &baseTime}},
			right: MemoryOp{Content: "meeting", Metadata: &memory.Metadata{Kind: memory.KindEpisode, EventTime: &sameSecond}}, wantEqual: true,
		},
		{
			name: "location trimmed", leftKey: defaultKey, rightKey: defaultKey,
			left:  MemoryOp{Content: "meeting", Metadata: &memory.Metadata{Kind: memory.KindEpisode, Location: "Kyoto"}},
			right: MemoryOp{Content: "meeting", Metadata: &memory.Metadata{Kind: memory.KindEpisode, Location: " Kyoto "}}, wantEqual: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			leftIdentity := canonicalMemoryOpIdentity(test.leftKey, test.left)
			rightIdentity := canonicalMemoryOpIdentity(test.rightKey, test.right)
			require.Equal(t, test.wantEqual, leftIdentity == rightIdentity)

			leftID := addAndReadMemoryID(t, test.leftKey, test.left)
			rightID := addAndReadMemoryID(t, test.rightKey, test.right)
			require.Equal(t, test.wantEqual, leftID == rightID)
		})
	}
}

func TestRunValidatesStateScopesAndCleansPeer(t *testing.T) {
	ctx := context.Background()
	tc := replayStateScopeCase("state-scope-success")
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	_, err := Run(ctx, testRunNamespace, Backend{
		Name: "in_memory", SessionService: sessionService, MemoryService: memoryService,
		ReadAllMemories: completeMemoryReader(memoryService),
	}, tc)
	require.NoError(t, err)
	peerKey := replayKey(testRunNamespace, tc.Name)
	peerKey.SessionID += "-scope-peer"
	peer, err := sessionService.GetSession(ctx, peerKey)
	require.NoError(t, err)
	require.Nil(t, peer)
	canonical, err := canonicalizeScopedState(
		session.StateMap{session.StateAppPrefix + "nil": nil}, session.StateAppPrefix,
	)
	require.NoError(t, err)
	require.Equal(t, session.StateMap{"nil": nil}, canonical)
	require.Equal(t, session.StateMap{
		session.StateAppPrefix + "nil":  nil,
		session.StateUserPrefix + "nil": nil,
	}, mergeScopedState(session.StateMap{"nil": nil}, session.StateMap{"nil": nil}))
}

func TestPreparedDirectStateScopesIgnoreEventDeltas(t *testing.T) {
	tc := Case{
		AppState: session.StateMap{
			session.StateAppPrefix + "direct": []byte(`"app"`),
		},
		UserState: session.StateMap{
			session.StateUserPrefix + "direct": []byte(`"user"`),
		},
		Events: []*event.Event{
			{StateDelta: session.StateMap{
				session.StateAppPrefix + "direct":   []byte(`"event"`),
				session.StateAppPrefix + "event":    []byte(`"app-event"`),
				session.StateUserPrefix + "event":   []byte(`"user-event"`),
				"session:local":                     []byte(`"ignored"`),
				session.StateTempPrefix + "scratch": []byte(`"ignored"`),
			}},
		},
	}

	appState, userState, err := prepareExpectedDirectStateMaps(tc)
	require.NoError(t, err)
	require.Equal(t, session.StateMap{"direct": []byte(`"app"`)}, appState)
	require.Equal(t, session.StateMap{"direct": []byte(`"user"`)}, userState)
	prepared, err := prepareCase(Backend{}, tc)
	require.NoError(t, err)
	require.True(t, prepared.validateDirectStateScopes)

	appState, userState, err = prepareExpectedDirectStateMaps(Case{
		Events: []*event.Event{{StateDelta: session.StateMap{
			session.StateAppPrefix + "event":    []byte("value"),
			session.StateUserPrefix + "event":   []byte("value"),
			"session:local":                     []byte("value"),
			session.StateTempPrefix + "scratch": []byte("value"),
		}}},
	})
	require.NoError(t, err)
	require.Empty(t, appState)
	require.Empty(t, userState)
	prepared, err = prepareCase(Backend{}, Case{
		Events: []*event.Event{{StateDelta: session.StateMap{
			session.StateAppPrefix + "event":    []byte("value"),
			session.StateUserPrefix + "event":   []byte("value"),
			"session:local":                     []byte("value"),
			session.StateTempPrefix + "scratch": []byte("value"),
		}}},
	})
	require.NoError(t, err)
	require.False(t, prepared.validateDirectStateScopes)

	appState, userState, err = prepareExpectedDirectStateMaps(Case{
		SessionState: session.StateMap{"session:mode": []byte(`"direct"`)},
	})
	require.NoError(t, err)
	require.Empty(t, appState)
	require.Empty(t, userState)
	prepared, err = prepareCase(Backend{}, Case{
		SessionState: session.StateMap{"session:mode": []byte(`"direct"`)},
	})
	require.NoError(t, err)
	require.True(t, prepared.validateDirectStateScopes)
}

func TestRunCleansStateScopePeerAfterCallerCancellation(t *testing.T) {
	type contextKey string
	const (
		traceKey   contextKey = "trace"
		traceValue            = "state-scope-cleanup"
	)
	cleanupErr := errors.New("cleanup failed")
	tests := []struct {
		name      string
		deleteErr error
	}{
		{name: "cleanup succeeds"},
		{name: "cleanup error is joined", deleteErr: cleanupErr},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := sessinmemory.NewSessionService()
			defer base.Close()
			memoryService := meminmemory.NewMemoryService()
			defer memoryService.Close()

			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), traceKey, traceValue))
			defer cancel()
			tc := replayStateScopeCase(strings.ReplaceAll(test.name, " ", "-"))
			sessionService := &scopeSessionService{
				Service:                      base,
				cancelAfterPeerCreate:        cancel,
				honorPeerContextCancellation: true,
				deleteErr:                    test.deleteErr,
				deleteContextValueKey:        traceKey,
			}

			_, err := Run(ctx, testRunNamespace, Backend{
				Name: "scope_fake", SessionService: sessionService, MemoryService: memoryService,
				ReadAllMemories: completeMemoryReader(memoryService),
			}, tc)
			require.ErrorIs(t, err, context.Canceled)
			if test.deleteErr != nil {
				require.ErrorIs(t, err, cleanupErr)
				require.ErrorContains(t, err, "delete state-scope peer")
			}
			require.Equal(t, 1, sessionService.deleteCalls)
			require.True(t, sessionService.deleteDelegated)
			require.NoError(t, sessionService.deleteContextErr)
			require.True(t, sessionService.deleteContextHasDeadline)
			require.True(t, sessionService.deleteContextDeadlineActive)
			require.Equal(t, traceValue, sessionService.deleteContextValue)

			peerKey := replayKey(testRunNamespace, tc.Name)
			peerKey.SessionID += "-scope-peer"
			peer, getErr := base.GetSession(context.Background(), peerKey)
			require.NoError(t, getErr)
			require.Nil(t, peer)
		})
	}
}

func TestRunRejectsIncorrectStateScopes(t *testing.T) {
	tests := []struct {
		name            string
		configure       func(*scopeSessionService)
		wantErrors      []string
		wantDeleteCalls int
	}{
		{
			name:       "app state read mismatch",
			configure:  func(service *scopeSessionService) { service.emptyAppState = true },
			wantErrors: []string{"app state for case"},
		},
		{
			name: "app state read failure",
			configure: func(service *scopeSessionService) {
				service.listAppErr = errors.New("list app failed")
			},
			wantErrors: []string{"list app state", "list app failed"},
		},
		{
			name: "user state read failure",
			configure: func(service *scopeSessionService) {
				service.listUserErr = errors.New("list user failed")
			},
			wantErrors: []string{"list user state", "list user failed"},
		},
		{
			name: "peer create failure",
			configure: func(service *scopeSessionService) {
				service.createPeerErr = errors.New("create peer failed")
			},
			wantErrors: []string{"create state-scope peer", "create peer failed"}, wantDeleteCalls: 1,
		},
		{
			name: "peer create fails after write",
			configure: func(service *scopeSessionService) {
				service.createPeerAfterWriteErr = errors.New("create peer response failed")
			},
			wantErrors: []string{"create state-scope peer", "create peer response failed"}, wantDeleteCalls: 1,
		},
		{
			name: "peer create and cleanup failures",
			configure: func(service *scopeSessionService) {
				service.createPeerAfterWriteErr = errors.New("create peer response failed")
				service.deleteErr = errors.New("cleanup failed")
			},
			wantErrors: []string{
				"create state-scope peer", "create peer response failed",
				"delete state-scope peer", "cleanup failed",
			}, wantDeleteCalls: 1,
		},
		{
			name:            "peer create returns nil",
			configure:       func(service *scopeSessionService) { service.nilCreatePeer = true },
			wantErrors:      []string{"create state-scope peer", "returned nil"},
			wantDeleteCalls: 1,
		},
		{
			name: "peer read failure",
			configure: func(service *scopeSessionService) {
				service.getPeerErr = errors.New("get peer failed")
			},
			wantErrors:      []string{"get state-scope peer", "get peer failed"},
			wantDeleteCalls: 1,
		},
		{
			name:            "peer read returns nil",
			configure:       func(service *scopeSessionService) { service.nilGetPeer = true },
			wantErrors:      []string{"get state-scope peer", "returned nil"},
			wantDeleteCalls: 1,
		},
		{
			name:       "peer does not inherit",
			configure:  func(service *scopeSessionService) { service.stripPeerState = true },
			wantErrors: []string{"peer state for case"}, wantDeleteCalls: 1,
		},
		{
			name:       "cleanup failure",
			configure:  func(service *scopeSessionService) { service.deleteErr = errors.New("cleanup failed") },
			wantErrors: []string{"delete state-scope peer", "cleanup failed"}, wantDeleteCalls: 1,
		},
		{
			name: "validation and cleanup failures",
			configure: func(service *scopeSessionService) {
				service.stripPeerState = true
				service.deleteErr = errors.New("cleanup failed")
			},
			wantErrors: []string{"peer state for case", "delete state-scope peer", "cleanup failed"}, wantDeleteCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := sessinmemory.NewSessionService()
			defer base.Close()
			sessionService := &scopeSessionService{Service: base}
			test.configure(sessionService)
			memoryService := meminmemory.NewMemoryService()
			defer memoryService.Close()

			_, err := Run(context.Background(), testRunNamespace, Backend{
				Name: "scope_fake", SessionService: sessionService, MemoryService: memoryService,
				ReadAllMemories: completeMemoryReader(memoryService),
			}, replayStateScopeCase(strings.ReplaceAll(test.name, " ", "-")))
			require.Error(t, err)
			for _, want := range test.wantErrors {
				require.ErrorContains(t, err, want)
			}
			require.Equal(t, test.wantDeleteCalls, sessionService.deleteCalls)
			if test.wantDeleteCalls > 0 {
				peerKey := replayKey(testRunNamespace, replayStateScopeCase(
					strings.ReplaceAll(test.name, " ", "-"),
				).Name)
				peerKey.SessionID += "-scope-peer"
				peer, getErr := base.GetSession(context.Background(), peerKey)
				require.NoError(t, getErr)
				require.Nil(t, peer)
			}
		})
	}
}

type emptyUpdateResultMemoryService struct {
	memory.Service
}

type fixtureCallRecorder struct {
	mu    sync.Mutex
	calls map[string]int
}

func (r *fixtureCallRecorder) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = make(map[string]int)
	}
	r.calls[name]++
}

func (r *fixtureCallRecorder) snapshot() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.calls))
	for name, count := range r.calls {
		out[name] = count
	}
	return out
}

type preflightRecordingSessionService struct {
	session.Service
	calls *fixtureCallRecorder
}

func (s *preflightRecordingSessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	s.calls.record("session.create")
	return s.Service.CreateSession(ctx, key, state, opts...)
}

func (s *preflightRecordingSessionService) UpdateAppState(
	ctx context.Context,
	appName string,
	state session.StateMap,
) error {
	s.calls.record("session.update_app_state")
	return s.Service.UpdateAppState(ctx, appName, state)
}

func (s *preflightRecordingSessionService) UpdateUserState(
	ctx context.Context,
	key session.UserKey,
	state session.StateMap,
) error {
	s.calls.record("session.update_user_state")
	return s.Service.UpdateUserState(ctx, key, state)
}

func (s *preflightRecordingSessionService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	s.calls.record("session.update_session_state")
	return s.Service.UpdateSessionState(ctx, key, state)
}

func (s *preflightRecordingSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	s.calls.record("session.append_event")
	return s.Service.AppendEvent(ctx, sess, evt, opts...)
}

func (s *preflightRecordingSessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	s.calls.record("session.create_summary")
	return s.Service.CreateSessionSummary(ctx, sess, filterKey, force)
}

type preflightRecordingMemoryService struct {
	memory.Service
	calls *fixtureCallRecorder
}

func (s *preflightRecordingMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	content string,
	topics []string,
	opts ...memory.AddOption,
) error {
	s.calls.record("memory.add")
	return s.Service.AddMemory(ctx, userKey, content, topics, opts...)
}

func (s *preflightRecordingMemoryService) UpdateMemory(
	ctx context.Context,
	key memory.Key,
	content string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	s.calls.record("memory.update")
	return s.Service.UpdateMemory(ctx, key, content, topics, opts...)
}

func (s *preflightRecordingMemoryService) DeleteMemory(
	ctx context.Context,
	key memory.Key,
) error {
	s.calls.record("memory.delete")
	return s.Service.DeleteMemory(ctx, key)
}

func (s *preflightRecordingMemoryService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	s.calls.record("memory.search")
	return s.Service.SearchMemories(ctx, userKey, query, opts...)
}

type preflightRecordingTrackService struct {
	delegate session.TrackService
	calls    *fixtureCallRecorder
}

func (s *preflightRecordingTrackService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	s.calls.record("track.append")
	return s.delegate.AppendTrackEvent(ctx, sess, trackEvent, opts...)
}

func newPreflightRecordingBackend(
	t *testing.T,
) (Backend, session.Service, *fixtureCallRecorder) {
	t.Helper()
	calls := &fixtureCallRecorder{}
	summarizer := &recordingSummarizer{}
	baseSessionService := sessinmemory.NewSessionService(sessinmemory.WithSummarizer(summarizer))
	baseMemoryService := meminmemory.NewMemoryService()
	t.Cleanup(func() { require.NoError(t, baseMemoryService.Close()) })
	t.Cleanup(func() { require.NoError(t, baseSessionService.Close()) })

	sessionService := &preflightRecordingSessionService{
		Service: baseSessionService,
		calls:   calls,
	}
	memoryService := &preflightRecordingMemoryService{
		Service: baseMemoryService,
		calls:   calls,
	}
	trackService := &preflightRecordingTrackService{
		delegate: baseSessionService,
		calls:    calls,
	}
	backend := Backend{
		Name:           "preflight_recording",
		SessionService: sessionService,
		TrackService:   trackService,
		MemoryService:  memoryService,
		ReadAllMemories: func(ctx context.Context, userKey memory.UserKey) ([]*memory.Entry, bool, error) {
			calls.record("memory.read_all")
			entries, err := baseMemoryService.ReadMemories(ctx, userKey, 0)
			return entries, err == nil, err
		},
		CreateSummary: func(ctx context.Context, sess *session.Session, step SummaryStep) error {
			calls.record("summary.callback")
			return summarizer.createSummary(ctx, sessionService, sess, step)
		},
	}
	return backend, baseSessionService, calls
}

func addPreflightProbeState(tc *Case) {
	add := func(state session.StateMap, key string, value []byte) session.StateMap {
		out := cloneStateMap(state)
		if out == nil {
			out = make(session.StateMap)
		}
		out[key] = append([]byte(nil), value...)
		return out
	}
	tc.InitialState = add(tc.InitialState, "initial", []byte(`"probe"`))
	tc.AppState = add(tc.AppState, session.StateAppPrefix+"probe", []byte(`"app"`))
	tc.UserState = add(tc.UserState, session.StateUserPrefix+"probe", []byte(`"user"`))
	tc.SessionState = add(tc.SessionState, "session:probe", []byte(`"session"`))
}

func (s *emptyUpdateResultMemoryService) UpdateMemory(
	ctx context.Context,
	key memory.Key,
	content string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	if err := s.Service.UpdateMemory(ctx, key, content, topics, opts...); err != nil {
		return err
	}
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = ""
	}
	return nil
}

type orderedReadMemoryService struct {
	memory.Service
}

type concurrentFailureMemoryService struct {
	memory.Service
	failures map[string]error
}

func (s *concurrentFailureMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	content string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if err := s.failures[content]; err != nil {
		return err
	}
	return s.Service.AddMemory(ctx, userKey, content, topics, opts...)
}

func (s *orderedReadMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	entries, err := s.Service.ReadMemories(ctx, userKey, limit)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left := entries[i].Memory.EventTime
		right := entries[j].Memory.EventTime
		if left == nil || right == nil {
			return entries[i].ID < entries[j].ID
		}
		return left.Before(*right)
	})
	return entries, nil
}

type fixedReadMemoryService struct {
	memory.Service
	entries []*memory.Entry
	readErr error
}

func (s *fixedReadMemoryService) ReadMemories(context.Context, memory.UserKey, int) ([]*memory.Entry, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.entries, nil
}

func addAndReadMemoryID(t *testing.T, userKey memory.UserKey, op MemoryOp) string {
	t.Helper()
	service := meminmemory.NewMemoryService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	var opts []memory.AddOption
	if op.Metadata != nil {
		opts = append(opts, memory.WithMetadata(op.Metadata))
	}
	require.NoError(t, service.AddMemory(
		context.Background(), userKey, op.Content, append([]string(nil), op.Topics...), opts...,
	))
	entries, err := service.ReadMemories(context.Background(), userKey, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	return entries[0].ID
}

func replayStateScopeCase(name string) Case {
	return Case{
		Name: name,
		InitialState: session.StateMap{
			"session:init": []byte(`{"ready":true}`),
		},
		AppState: session.StateMap{
			session.StateAppPrefix + "feature": []byte(`{"enabled":true}`),
		},
		UserState: session.StateMap{
			session.StateUserPrefix + "locale": []byte(`"zh-CN"`),
		},
		SessionState: session.StateMap{
			session.StateTempPrefix + "scratch": []byte("working"),
			"session:mode":                      []byte(`{"name":"matrix"}`),
		},
	}
}

type scopeSessionService struct {
	session.Service
	emptyAppState                bool
	stripPeerState               bool
	deleteErr                    error
	listAppErr                   error
	listUserErr                  error
	createPeerErr                error
	createPeerAfterWriteErr      error
	getPeerErr                   error
	nilCreatePeer                bool
	nilGetPeer                   bool
	cancelAfterPeerCreate        context.CancelFunc
	honorPeerContextCancellation bool
	deleteCalls                  int
	deleteDelegated              bool
	deleteContextErr             error
	deleteContextHasDeadline     bool
	deleteContextDeadlineActive  bool
	deleteContextValueKey        any
	deleteContextValue           any
}

type recordingTrackService struct {
	delegate session.TrackService
	calls    int
}

func (s *recordingTrackService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	s.calls++
	return s.delegate.AppendTrackEvent(ctx, sess, trackEvent, opts...)
}

type malformedSnapshotSessionService struct {
	session.Service
}

type nilSummarySnapshotSessionService struct {
	session.Service
}

type nilTrackSnapshotSessionService struct {
	session.Service
}

func (s *malformedSnapshotSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	got, err := s.Service.GetSession(ctx, key, opts...)
	if err != nil || got == nil {
		return got, err
	}
	got.Events = append(got.Events, *replayMalformedEvent())
	return got, nil
}

func (s *nilSummarySnapshotSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	got, err := s.Service.GetSession(ctx, key, opts...)
	if err != nil || got == nil {
		return got, err
	}
	got.SummariesMu.Lock()
	if got.Summaries == nil {
		got.Summaries = make(map[string]*session.Summary)
	}
	got.Summaries["branch"] = nil
	got.SummariesMu.Unlock()
	return got, nil
}

func (s *nilTrackSnapshotSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	got, err := s.Service.GetSession(ctx, key, opts...)
	if err != nil || got == nil {
		return got, err
	}
	got.TracksMu.Lock()
	if got.Tracks == nil {
		got.Tracks = make(map[session.Track]*session.TrackEvents)
	}
	got.Tracks["broken"] = nil
	got.TracksMu.Unlock()
	return got, nil
}

func (s *scopeSessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if s.listAppErr != nil {
		return nil, s.listAppErr
	}
	if s.emptyAppState {
		return session.StateMap{}, nil
	}
	return s.Service.ListAppStates(ctx, appName)
}

func (s *scopeSessionService) ListUserStates(
	ctx context.Context,
	key session.UserKey,
) (session.StateMap, error) {
	if s.listUserErr != nil {
		return nil, s.listUserErr
	}
	return s.Service.ListUserStates(ctx, key)
}

func (s *scopeSessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	if strings.HasSuffix(key.SessionID, "-scope-peer") && s.createPeerErr != nil {
		return nil, s.createPeerErr
	}
	got, err := s.Service.CreateSession(ctx, key, state, opts...)
	if err == nil && strings.HasSuffix(key.SessionID, "-scope-peer") {
		if s.cancelAfterPeerCreate != nil {
			s.cancelAfterPeerCreate()
		}
		if s.nilCreatePeer {
			return nil, nil
		}
		if s.createPeerAfterWriteErr != nil {
			return nil, s.createPeerAfterWriteErr
		}
	}
	return got, err
}

func (s *scopeSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	if strings.HasSuffix(key.SessionID, "-scope-peer") && s.getPeerErr != nil {
		return nil, s.getPeerErr
	}
	if strings.HasSuffix(key.SessionID, "-scope-peer") && s.honorPeerContextCancellation {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	got, err := s.Service.GetSession(ctx, key, opts...)
	if err == nil && strings.HasSuffix(key.SessionID, "-scope-peer") && s.nilGetPeer {
		return nil, nil
	}
	if err != nil || got == nil || !s.stripPeerState || !strings.HasSuffix(key.SessionID, "-scope-peer") {
		return got, err
	}
	for stateKey := range got.SnapshotState() {
		if strings.HasPrefix(stateKey, session.StateAppPrefix) || strings.HasPrefix(stateKey, session.StateUserPrefix) {
			got.DeleteState(stateKey)
		}
	}
	return got, nil
}

func (s *scopeSessionService) DeleteSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) error {
	s.deleteCalls++
	s.deleteContextErr = ctx.Err()
	deadline, ok := ctx.Deadline()
	s.deleteContextHasDeadline = ok
	s.deleteContextDeadlineActive = ok && time.Until(deadline) > 0
	if s.deleteContextValueKey != nil {
		s.deleteContextValue = ctx.Value(s.deleteContextValueKey)
	}
	if strings.HasSuffix(key.SessionID, "-scope-peer") && s.honorPeerContextCancellation {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	s.deleteDelegated = true
	if err := s.Service.DeleteSession(ctx, key, opts...); err != nil {
		return err
	}
	return s.deleteErr
}

type failingMemoryService struct {
	memory.Service
	addErr          error
	readErr         error
	searchErr       error
	searchResults   []*memory.Entry
	useSearchResult bool
}

type recordingMemoryService struct {
	memory.Service
	updateCalls int
}

func (s *recordingMemoryService) UpdateMemory(
	ctx context.Context,
	key memory.Key,
	content string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	s.updateCalls++
	return s.Service.UpdateMemory(ctx, key, content, topics, opts...)
}

type failDeleteOnceMemoryService struct {
	memory.Service
	err   error
	calls int
}

func (s *failDeleteOnceMemoryService) DeleteMemory(ctx context.Context, key memory.Key) error {
	s.calls++
	if s.calls == 1 && s.err != nil {
		return s.err
	}
	return s.Service.DeleteMemory(ctx, key)
}

func (s *failingMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	content string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if s.addErr != nil {
		return s.addErr
	}
	return s.Service.AddMemory(ctx, userKey, content, topics, opts...)
}

func (s *failingMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.Service.ReadMemories(ctx, userKey, limit)
}

func (s *failingMemoryService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	if s.useSearchResult {
		return s.searchResults, nil
	}
	return s.Service.SearchMemories(ctx, userKey, query, opts...)
}

func TestAssertMemoryQueriesValidatesExactContents(t *testing.T) {
	entry := func(content string) *memory.Entry {
		return &memory.Entry{Memory: &memory.Memory{Memory: content}}
	}
	tests := []struct {
		name     string
		results  []*memory.Entry
		expected []string
		wantErr  string
	}{
		{
			name:     "wrong content",
			results:  []*memory.Entry{entry("wrong")},
			expected: []string{"expected"},
			wantErr:  `memory query 0 for case "wrong content" returned contents ["wrong"], want ["expected"]`,
		},
		{
			name:     "unexpected extra content",
			results:  []*memory.Entry{entry("expected"), entry("unrelated")},
			expected: []string{"expected"},
			wantErr:  `memory query 0 for case "unexpected extra content" returned contents ["expected" "unrelated"], want ["expected"]`,
		},
		{
			name:     "order ignored",
			results:  []*memory.Entry{entry("second"), entry("first")},
			expected: []string{"first", "second"},
		},
		{
			name:     "nil result",
			results:  []*memory.Entry{nil},
			expected: []string{"expected"},
			wantErr:  `memory query 0 for case "nil result" returned nil result at index 0, want contents ["expected"]`,
		},
		{
			name:     "nil memory",
			results:  []*memory.Entry{{}},
			expected: []string{"expected"},
			wantErr:  `memory query 0 for case "nil memory" returned result with nil memory at index 0, want contents ["expected"]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &failingMemoryService{
				Service:         meminmemory.NewMemoryService(),
				searchResults:   test.results,
				useSearchResult: true,
			}
			defer service.Close()
			err := assertMemoryQueries(
				context.Background(),
				Backend{MemoryService: service},
				memory.UserKey{AppName: "app", UserID: "user"},
				Case{Name: test.name, Queries: []MemoryQuery{{
					Query: "query", ExpectedContents: test.expected,
				}}},
			)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, test.wantErr)
		})
	}
}

func TestBuildSnapshotNormalizesGeneratedEventFields(t *testing.T) {
	timestamp := time.Unix(1, 0)
	leftSession := replayTestSession("left", timestamp)
	rightSession := replayTestSession("right", timestamp)
	left := buildSnapshotForTest(t, leftSession, nil)
	right := buildSnapshotForTest(t, rightSession, nil)
	diffs := compareSnapshotsForTest(t,
		"generated",
		"session-1",
		"left",
		"right",
		left,
		right,
		nil,
	)
	require.Empty(t, diffs)
}

func TestBuildSnapshotPreservesResponseMetadata(t *testing.T) {
	timestamp := time.Date(2026, time.July, 1, 1, 2, 3, 4, time.FixedZone("UTC+8", 8*60*60))
	base := replayTestSession("same", time.Unix(1, 0))
	base.Events[0].Response.Timestamp = timestamp
	base.Events[0].Response.ID = "response-one"
	base.Events[0].Response.Created = 100

	cloneWith := func(mut func(*event.Event)) *session.Session {
		clone := base.Clone()
		mut(&clone.Events[0])
		return clone
	}
	tests := []struct {
		name       string
		mutate     func(*event.Event)
		wantPath   string
		wantLeft   any
		wantRight  any
		wantNoDiff bool
	}{
		{
			name:     "response id",
			mutate:   func(evt *event.Event) { evt.Response.ID = "response-two" },
			wantPath: "$.events[0].response.id", wantLeft: "response-one", wantRight: "response-two",
		},
		{
			name:     "response created",
			mutate:   func(evt *event.Event) { evt.Response.Created = 101 },
			wantPath: "$.events[0].created", wantLeft: json.Number("100"), wantRight: json.Number("101"),
		},
		{
			name:       "same response instant different zone",
			mutate:     func(evt *event.Event) { evt.Response.Timestamp = timestamp.UTC() },
			wantNoDiff: true,
		},
		{
			name:     "response timestamp",
			mutate:   func(evt *event.Event) { evt.Response.Timestamp = timestamp.Add(time.Second) },
			wantPath: "$.events[0].response.timestamp",
			wantLeft: normalizeTime(timestamp), wantRight: normalizeTime(timestamp.Add(time.Second)),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := buildSnapshotForTest(t, base, nil)
			right := buildSnapshotForTest(t, cloneWith(test.mutate), nil)
			diffs := compareSnapshotsForTest(t, "response-metadata", "session-1", "left", "right", left, right, nil)
			if test.wantNoDiff {
				require.Empty(t, diffs)
				return
			}
			require.Len(t, diffs, 1)
			require.Equal(t, test.wantPath, diffs[0].Path)
			require.Equal(t, test.wantLeft, diffs[0].Left)
			require.Equal(t, test.wantRight, diffs[0].Right)
			require.False(t, diffs[0].Allowed)
		})
	}
}

func TestNormalizeEventDistinguishesNilAndEmptyResponse(t *testing.T) {
	nilEvent := event.Event{ID: "nil", Timestamp: time.Unix(1, 0), Author: "agent"}
	emptyEvent := event.Event{
		ID: "empty", Timestamp: time.Unix(1, 0), Author: "agent", Response: &model.Response{},
	}
	nilSnapshot, err := normalizeEvent(0, nilEvent)
	require.NoError(t, err)
	emptySnapshot, err := normalizeEvent(0, emptyEvent)
	require.NoError(t, err)
	_, nilHasResponse := nilSnapshot["response"]
	_, nilHasCreated := nilSnapshot["created"]
	_, emptyHasResponse := emptySnapshot["response"]
	_, emptyHasCreated := emptySnapshot["created"]
	require.False(t, nilHasResponse)
	require.False(t, nilHasCreated)
	require.True(t, emptyHasResponse)
	require.True(t, emptyHasCreated)
	response, ok := emptySnapshot["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "", response["timestamp"])
}

func TestBuildSnapshotNormalizesMemoriesWithNilSession(t *testing.T) {
	t.Run("no memories preserves empty snapshot", func(t *testing.T) {
		got, err := BuildSnapshot(nil, nil)
		require.NoError(t, err)
		require.Equal(t, Snapshot{
			State: map[string]any{}, Memory: []MemorySnapshot{},
			Summary: map[string]SummaryEntry{}, Tracks: []TrackSnapshot{},
		}, got)
	})

	t.Run("valid memory is preserved", func(t *testing.T) {
		got, err := BuildSnapshot(nil, []*memory.Entry{{
			ID: "generated-id", AppName: "app", UserID: "user",
			Memory: &memory.Memory{Memory: "remembered", Topics: []string{"second", "first"}},
		}})
		require.NoError(t, err)
		require.Len(t, got.Memory, 1)
		require.Equal(t, "app", got.Memory[0].App)
		require.Equal(t, "user", got.Memory[0].UserID)
		require.Equal(t, "remembered", got.Memory[0].Content)
		require.Equal(t, []string{"first", "second"}, got.Memory[0].Topics)
		require.Empty(t, got.Session)
		require.Empty(t, got.Events)
		require.Empty(t, got.State)
		require.Empty(t, got.Summary)
		require.Empty(t, got.Tracks)
	})
}

func TestBuildSnapshotReturnsNormalizationErrors(t *testing.T) {
	t.Run("malformed event", func(t *testing.T) {
		badEvent := replayMalformedEvent()
		sess := session.NewSession(
			"app", "user", "session",
			session.WithSessionEvents([]event.Event{*badEvent}),
		)
		_, err := BuildSnapshot(sess, nil)
		require.ErrorContains(t, err, "normalize event 0: marshal")
		require.ErrorContains(t, err, "unsupported type: chan int")
	})

	t.Run("nil memory entry", func(t *testing.T) {
		sess := session.NewSession("app", "user", "session")
		entry := &memory.Entry{
			ID: "memory", AppName: "app", UserID: "user",
			Memory: &memory.Memory{Memory: "valid"},
		}
		_, err := BuildSnapshot(sess, []*memory.Entry{entry, nil, entry})
		require.EqualError(t, err, "memory entry 1 is nil")
	})

	t.Run("nil nested memory", func(t *testing.T) {
		sess := session.NewSession("app", "user", "session")
		_, err := BuildSnapshot(sess, []*memory.Entry{{
			ID: "broken", AppName: "app", UserID: "user",
		}})
		require.EqualError(t, err, "memory entry 0 has nil Memory")
	})

	t.Run("nil session with nil memory entry", func(t *testing.T) {
		_, err := BuildSnapshot(nil, []*memory.Entry{nil})
		require.EqualError(t, err, "memory entry 0 is nil")
	})

	t.Run("nil session with nil nested memory", func(t *testing.T) {
		_, err := BuildSnapshot(nil, []*memory.Entry{{
			ID: "broken", AppName: "app", UserID: "user",
		}})
		require.EqualError(t, err, "memory entry 0 has nil Memory")
	})

	t.Run("event error precedes memory error", func(t *testing.T) {
		badEvent := replayMalformedEvent()
		sess := session.NewSession(
			"app", "user", "session",
			session.WithSessionEvents([]event.Event{*badEvent}),
		)
		_, err := BuildSnapshot(sess, []*memory.Entry{nil})
		require.ErrorContains(t, err, "normalize event 0: marshal")
		require.NotContains(t, err.Error(), "memory entry")
	})
}

func TestBuildSnapshotRejectsNilSummaryEntriesDeterministically(t *testing.T) {
	tests := []struct {
		name      string
		summaries map[string]*session.Summary
		want      string
	}{
		{
			name: "single nil entry",
			summaries: map[string]*session.Summary{
				"branch": nil,
			},
			want: `summary entry "branch" is nil`,
		},
		{
			name: "nil entry alongside valid summary",
			summaries: map[string]*session.Summary{
				"valid":  {Summary: "kept"},
				"branch": nil,
			},
			want: `summary entry "branch" is nil`,
		},
		{
			name: "multiple nil entries use first sorted key",
			summaries: map[string]*session.Summary{
				"z-last":  nil,
				"a-first": nil,
			},
			want: `summary entry "a-first" is nil`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sess := session.NewSession("app", "user", "session")
			sess.Summaries = test.summaries
			_, err := BuildSnapshot(sess, nil)
			require.EqualError(t, err, test.want)
		})
	}
}

func TestBuildSnapshotAcceptsValidSummaryMaps(t *testing.T) {
	t.Run("nil map", func(t *testing.T) {
		sess := session.NewSession("app", "user", "session")
		sess.Summaries = nil
		got := buildSnapshotForTest(t, sess, nil)
		require.NotNil(t, got.Summary)
		require.Empty(t, got.Summary)
	})

	t.Run("empty map", func(t *testing.T) {
		sess := session.NewSession("app", "user", "session")
		got := buildSnapshotForTest(t, sess, nil)
		require.NotNil(t, got.Summary)
		require.Empty(t, got.Summary)
	})

	t.Run("valid summary", func(t *testing.T) {
		sess := session.NewSession("app", "user", "session")
		sess.Summaries["branch"] = &session.Summary{
			Summary: "summary", Topics: []string{"second", "first"},
		}
		got := buildSnapshotForTest(t, sess, nil)
		require.Equal(t, map[string]SummaryEntry{
			"branch": {Summary: "summary", Topics: []string{"first", "second"}},
		}, got.Summary)
	})
}

func TestBuildSnapshotRejectsNilTrackEntriesDeterministically(t *testing.T) {
	tests := []struct {
		name   string
		tracks map[session.Track]*session.TrackEvents
		want   string
	}{
		{
			name:   "single nil entry",
			tracks: map[session.Track]*session.TrackEvents{"branch": nil},
			want:   `track entry "branch" is nil`,
		},
		{
			name: "nil entry alongside valid track",
			tracks: map[session.Track]*session.TrackEvents{
				"valid":  {Track: "valid"},
				"branch": nil,
			},
			want: `track entry "branch" is nil`,
		},
		{
			name: "multiple nil entries use first sorted key",
			tracks: map[session.Track]*session.TrackEvents{
				"z-last":  nil,
				"a-first": nil,
			},
			want: `track entry "a-first" is nil`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for i := 0; i < 20; i++ {
				sess := session.NewSession("app", "user", "session")
				sess.Tracks = test.tracks
				_, err := BuildSnapshot(sess, nil)
				require.EqualError(t, err, test.want)
			}
		})
	}

	t.Run("valid empty container", func(t *testing.T) {
		sess := session.NewSession("app", "user", "session")
		sess.Tracks = map[session.Track]*session.TrackEvents{
			"empty": {Track: "empty"},
		}
		got := buildSnapshotForTest(t, sess, nil)
		require.Equal(t, []TrackSnapshot{{Name: "empty", Track: "empty"}}, got.Tracks)
	})

	t.Run("summary error precedes track error", func(t *testing.T) {
		sess := session.NewSession("app", "user", "session")
		sess.Summaries["branch"] = nil
		sess.Tracks = map[session.Track]*session.TrackEvents{"broken": nil}
		_, err := BuildSnapshot(sess, nil)
		require.EqualError(t, err, `summary entry "branch" is nil`)
	})
}

func TestRunReturnsWrappedSnapshotNormalizationError(t *testing.T) {
	baseSessionService := sessinmemory.NewSessionService()
	defer baseSessionService.Close()
	sessionService := &malformedSnapshotSessionService{Service: baseSessionService}
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	_, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "in_memory", SessionService: sessionService,
		MemoryService: memoryService, ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{Name: "malformed-event"})
	require.ErrorContains(t, err,
		`build final snapshot for case "malformed-event" on backend "in_memory": normalize event 0: marshal`)
	require.ErrorContains(t, err, "unsupported type: chan int")
}

func TestRunReturnsWrappedNilSummaryNormalizationError(t *testing.T) {
	baseSessionService := sessinmemory.NewSessionService()
	defer baseSessionService.Close()
	sessionService := &nilSummarySnapshotSessionService{Service: baseSessionService}
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	_, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "in_memory", SessionService: sessionService,
		MemoryService: memoryService, ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{Name: "nil-summary"})
	require.EqualError(t, err,
		`build final snapshot for case "nil-summary" on backend "in_memory": summary entry "branch" is nil`)
}

func TestRunReturnsWrappedNilTrackNormalizationError(t *testing.T) {
	baseSessionService := sessinmemory.NewSessionService()
	defer baseSessionService.Close()
	sessionService := &nilTrackSnapshotSessionService{Service: baseSessionService}
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	_, err := Run(context.Background(), testRunNamespace, Backend{
		Name: "in_memory", SessionService: sessionService,
		MemoryService: memoryService, ReadAllMemories: completeMemoryReader(memoryService),
	}, Case{Name: "nil-track"})
	require.EqualError(t, err,
		`build final snapshot for case "nil-track" on backend "in_memory": track entry "broken" is nil`)
}

func TestBuildSnapshotPreservesSuppliedEventTimestamp(t *testing.T) {
	leftTime := time.Date(2026, time.July, 24, 8, 9, 10, 123, time.FixedZone("UTC+8", 8*60*60))
	rightTime := leftTime.Add(time.Second)
	left := buildSnapshotForTest(t, replayTestSession("same", leftTime), nil)
	right := buildSnapshotForTest(t, replayTestSession("same", rightTime), nil)

	require.Equal(t, normalizeTime(leftTime), left.Events[0]["timestamp"])
	diffs := compareSnapshotsForTest(t, "timestamp", "session-1", "left", "right", left, right, nil)
	require.Len(t, diffs, 1)
	require.Equal(t, "events", diffs[0].Section)
	require.Equal(t, "$.events[0].timestamp", diffs[0].Path)
	require.Equal(t, normalizeTime(leftTime), diffs[0].Left)
	require.Equal(t, normalizeTime(rightTime), diffs[0].Right)
	require.Equal(t, 0, diffs[0].Context["event_index"])
	require.False(t, diffs[0].Allowed)

	sameInstant := buildSnapshotForTest(t, replayTestSession("same", leftTime.UTC()), nil)
	require.Empty(t, compareSnapshotsForTest(t, "timestamp", "session-1", "left", "right", left, sameInstant, nil))
}

func TestNormalizeStatePreservesJSONNumbersAndByteKinds(t *testing.T) {
	state := normalizeState(session.StateMap{
		"large":  []byte(`{"value":9007199254740993}`),
		"plain":  []byte("hello"),
		"quoted": []byte(`"hello"`),
		"nil":    nil,
	})
	large, ok := state["large"].(StateBytesSnapshot)
	require.True(t, ok)
	require.Equal(t, "json", large.Kind)
	require.Equal(t, json.Number("9007199254740993"), large.Value.(map[string]any)["value"])
	require.NotEqual(t, state["plain"], state["quoted"])
	require.Equal(t, StateBytesSnapshot{Kind: "nil"}, state["nil"])
}

func TestNormalizationHandlesInvalidAndEmptyInputs(t *testing.T) {
	empty := buildSnapshotForTest(t, nil, nil)
	require.Empty(t, empty.Events)
	require.Empty(t, empty.State)
	require.Empty(t, empty.Memory)
	require.Empty(t, empty.Summary)
	require.Empty(t, empty.Tracks)

	binary := normalizeBytes([]byte{0xff, 0x00})
	require.Equal(t, StateBytesSnapshot{Kind: "base64", Value: "/wA="}, binary)

	nilPayload := normalizeTrackPayload(nil)
	emptyPayload := normalizeTrackPayload(json.RawMessage{})
	nullPayload := normalizeTrackPayload(json.RawMessage("null"))
	utf8Payload := normalizeTrackPayload(json.RawMessage("{"))
	binaryPayload := normalizeTrackPayload(json.RawMessage{0xff, 0x00})
	require.Equal(t, TrackPayloadSnapshot{Kind: "nil"}, nilPayload)
	require.Equal(t, TrackPayloadSnapshot{Kind: "empty"}, emptyPayload)
	require.Equal(t, TrackPayloadSnapshot{Kind: "json"}, nullPayload)
	require.Equal(t, TrackPayloadSnapshot{Kind: "utf8", Value: "{"}, utf8Payload)
	require.Equal(t, TrackPayloadSnapshot{Kind: "base64", Value: "/wA="}, binaryPayload)
	require.NotEqual(t, nilPayload, emptyPayload)
	require.NotEqual(t, nilPayload, nullPayload)
	require.Equal(
		t,
		TrackPayloadSnapshot{Kind: "json", Value: map[string]any{
			"a": json.Number("1"), "b": json.Number("2"),
		}},
		normalizeTrackPayload(json.RawMessage(` {"b":2,"a":1} `)),
	)

	var decoded any
	require.EqualError(t, decodeJSON([]byte(`{} []`), &decoded), "unexpected trailing JSON value")
	require.ErrorContains(t, decodeJSON([]byte(`{} {`), &decoded), "decode trailing JSON value")
}

func TestTrackSnapshotsPreserveContainerAndPayloadIdentity(t *testing.T) {
	leftSession := replayTrackSession("tool", json.RawMessage("null"))
	rightSession := replayTrackSession("wrong-container", json.RawMessage("null"))
	left := buildSnapshotForTest(t, leftSession, nil)
	right := buildSnapshotForTest(t, rightSession, nil)

	require.Equal(t, "tool", left.Tracks[0].Name)
	require.Equal(t, "tool", left.Tracks[0].Track)
	require.Equal(t, "tool", left.Tracks[0].Events[0].Track)
	diffs := compareSnapshotsForTest(t, "container", "session-1", "left", "right", left, right, nil)
	require.Len(t, diffs, 1)
	require.Equal(t, "tracks", diffs[0].Section)
	require.Equal(t, "$.tracks[0].track", diffs[0].Path)
	require.Equal(t, "tool", diffs[0].Context["track_name"])
	require.False(t, diffs[0].Allowed)

	rightSession = replayTrackSession("tool", nil)
	right = buildSnapshotForTest(t, rightSession, nil)
	diffs = compareSnapshotsForTest(t, "payload", "session-1", "left", "right", left, right, nil)
	require.Len(t, diffs, 1)
	require.Equal(t, "$.tracks[0].events[0].payload.kind", diffs[0].Path)
	require.Equal(t, 0, diffs[0].Context["track_event_index"])
	require.False(t, diffs[0].Allowed)
}

func TestPathParsersRejectMalformedPaths(t *testing.T) {
	_, ok := pathIndex("$.events", "$.events")
	require.False(t, ok)
	_, ok = pathIndex("$.events[", "$.events")
	require.False(t, ok)
	_, ok = pathIndex("$.events[x]", "$.events")
	require.False(t, ok)

	_, ok = nestedPathIndex("$.tracks[0].name", ".events")
	require.False(t, ok)
	_, ok = nestedPathIndex("$.tracks[0].events[", ".events")
	require.False(t, ok)
	_, ok = nestedPathIndex("$.tracks[0].events[x]", ".events")
	require.False(t, ok)

	_, ok = summaryFilterKey(`$.summary["unterminated"`)
	require.False(t, ok)
	_, ok = summaryFilterKey(`$.summary["bad\x"].summary`)
	require.False(t, ok)
	_, ok = summaryFilterKey(`$.summary["key"]suffix`)
	require.False(t, ok)
	_, ok = summaryFilterKey(`$.summary[0].summary`)
	require.False(t, ok)
	_, ok = summaryFilterKey("$.events[0]")
	require.False(t, ok)
	key, ok := summaryFilterKey("$.summary.branch.boundary.version")
	require.True(t, ok)
	require.Equal(t, "branch", key)
	key, ok = summaryFilterKey("$.summary.branch[0]")
	require.True(t, ok)
	require.Equal(t, "branch", key)
}

func TestCompareSnapshotsPreservesSpecialSummaryFilterKeyContext(t *testing.T) {
	filterKeys := []string{
		"root/tools]weather",
		`root/"quoted"`,
		`root\tools`,
		"\u6839/\u5de5\u5177/\u5929\u6c14",
		`root/]"quoted"\tools`,
	}

	for _, filterKey := range filterKeys {
		t.Run(filterKey, func(t *testing.T) {
			left := Snapshot{Summary: map[string]SummaryEntry{
				filterKey: {Summary: "left"},
			}}
			right := Snapshot{Summary: map[string]SummaryEntry{
				filterKey: {Summary: "right"},
			}}

			diffs := compareSnapshotsForTest(t,
				"special-summary-key", "session-1", "left", "right",
				left, right, nil,
			)
			require.Len(t, diffs, 1)
			require.Equal(t, appendPath("$.summary", filterKey)+".summary", diffs[0].Path)
			require.Equal(t, filterKey, diffs[0].Context["summary_filter_key"])
		})
	}
}

func TestCompareSnapshotsAddsContextAndAppliesExplicitRule(t *testing.T) {
	left := buildSnapshotForTest(t, replayTestSession("same", time.Unix(1, 0)), nil)
	right := buildSnapshotForTest(t, replayTestSession("same", time.Unix(1, 0)), nil)
	right.Events[0]["author"] = "different"
	rules := []AllowedDiffRule{{
		Section: "events", Path: "$.events[0].author",
		BackendA: "left", BackendB: "right", Reason: "fixture drift",
	}}
	diffs := compareSnapshotsForTest(t, "case", "session-1", "left", "right", left, right, rules)
	require.Len(t, diffs, 1)
	require.True(t, diffs[0].Allowed)
	require.Equal(t, "fixture drift", diffs[0].Reason)
	require.Equal(t, 0, diffs[0].Context["event_index"])
	require.False(t, HasUnallowedDiffs(diffs))
}

func TestCompareSnapshotsDistinguishesMissingValues(t *testing.T) {
	legacySentinel := map[string]any{"replay": "missing"}
	tests := []struct {
		name                              string
		left, right                       map[string]any
		wantPath                          string
		wantLeft, wantRight               any
		wantLeftMissing, wantRightMissing bool
	}{
		{
			name: "map left missing versus legacy sentinel",
			left: map[string]any{}, right: map[string]any{"value": legacySentinel},
			wantPath: "$.state.value", wantLeft: nil, wantRight: legacySentinel,
			wantLeftMissing: true,
		},
		{
			name: "map right missing versus legacy sentinel",
			left: map[string]any{"value": legacySentinel}, right: map[string]any{},
			wantPath: "$.state.value", wantLeft: legacySentinel, wantRight: nil,
			wantRightMissing: true,
		},
		{
			name: "list left missing versus legacy sentinel",
			left: map[string]any{"items": []any{}}, right: map[string]any{"items": []any{legacySentinel}},
			wantPath: "$.state.items[0]", wantLeft: nil, wantRight: legacySentinel,
			wantLeftMissing: true,
		},
		{
			name: "list right missing versus legacy sentinel",
			left: map[string]any{"items": []any{legacySentinel}}, right: map[string]any{"items": []any{}},
			wantPath: "$.state.items[0]", wantLeft: legacySentinel, wantRight: nil,
			wantRightMissing: true,
		},
		{
			name: "map left missing versus json null",
			left: map[string]any{}, right: map[string]any{"value": nil},
			wantPath: "$.state.value", wantLeft: nil, wantRight: nil,
			wantLeftMissing: true,
		},
		{
			name: "map right missing versus json null",
			left: map[string]any{"value": nil}, right: map[string]any{},
			wantPath: "$.state.value", wantLeft: nil, wantRight: nil,
			wantRightMissing: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diffs := compareSnapshotsForTest(
				t, test.name, "session-1", "left", "right",
				Snapshot{State: test.left}, Snapshot{State: test.right}, nil,
			)
			require.Len(t, diffs, 1)
			diff := diffs[0]
			require.Equal(t, test.wantPath, diff.Path)
			if test.wantLeft == nil {
				require.Nil(t, diff.Left)
			} else {
				require.Equal(t, test.wantLeft, diff.Left)
			}
			if test.wantRight == nil {
				require.Nil(t, diff.Right)
			} else {
				require.Equal(t, test.wantRight, diff.Right)
			}
			require.Equal(t, test.wantLeftMissing, diff.LeftMissing)
			require.Equal(t, test.wantRightMissing, diff.RightMissing)
			require.False(t, diff.LeftMissing && diff.RightMissing)
		})
	}

	t.Run("normal value diff has no missing side", func(t *testing.T) {
		diffs := compareSnapshotsForTest(
			t, "normal", "session-1", "left", "right",
			Snapshot{State: map[string]any{"value": "left"}},
			Snapshot{State: map[string]any{"value": "right"}}, nil,
		)
		require.Len(t, diffs, 1)
		require.False(t, diffs[0].LeftMissing)
		require.False(t, diffs[0].RightMissing)
	})

	t.Run("allowed rule still applies to missing diff", func(t *testing.T) {
		diffs := compareSnapshotsForTest(
			t, "allowed-missing", "session-1", "left", "right",
			Snapshot{State: map[string]any{}},
			Snapshot{State: map[string]any{"value": legacySentinel}},
			[]AllowedDiffRule{{
				Section: "state", Path: "$.state.value",
				BackendA: "left", BackendB: "right", Reason: "known missing value",
			}},
		)
		require.Len(t, diffs, 1)
		require.True(t, diffs[0].LeftMissing)
		require.True(t, diffs[0].Allowed)
		require.Equal(t, "known missing value", diffs[0].Reason)
		require.False(t, HasUnallowedDiffs(diffs))
	})
}

func TestCompareSnapshotsKeepsSectionNilAsPresentValue(t *testing.T) {
	tests := []struct {
		name      string
		right     map[string]any
		wantRight map[string]any
	}{
		{name: "nil versus empty map", right: map[string]any{}, wantRight: map[string]any{}},
		{
			name:  "nil versus populated map",
			right: map[string]any{"value": "right"}, wantRight: map[string]any{"value": "right"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diffs := compareSnapshotsForTest(
				t, test.name, "session-1", "left", "right",
				Snapshot{State: nil}, Snapshot{State: test.right}, nil,
			)
			require.Len(t, diffs, 1)
			require.Equal(t, "$.state", diffs[0].Path)
			require.Nil(t, diffs[0].Left)
			require.Equal(t, test.wantRight, diffs[0].Right)
			require.False(t, diffs[0].LeftMissing)
			require.False(t, diffs[0].RightMissing)
		})
	}
}

func TestCompareSnapshotsReturnsJSONConversionErrors(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "channel", value: make(chan int), want: "unsupported type: chan int"},
		{name: "function", value: func() {}, want: "unsupported type: func()"},
		{name: "nan", value: math.NaN(), want: "unsupported value: NaN"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diffs, err := CompareSnapshots(
				"invalid", "session-1", "left", "right",
				Snapshot{State: map[string]any{"bad": test.value}},
				Snapshot{State: map[string]any{"bad": "safe"}},
				nil,
			)
			require.Nil(t, diffs)
			require.ErrorContains(t, err, `normalize left state section for backend "left"`)
			require.ErrorContains(t, err, test.want)
		})
	}

	diffs, err := CompareSnapshots(
		"invalid-right", "session-1", "left", "right",
		Snapshot{State: map[string]any{"bad": "safe"}},
		Snapshot{State: map[string]any{"bad": make(chan int)}},
		nil,
	)
	require.Nil(t, diffs)
	require.ErrorContains(t, err, `normalize right state section for backend "right"`)

	t.Run("fixed section and side order", func(t *testing.T) {
		diffs, err := CompareSnapshots(
			"ordered", "session-1", "left", "right",
			Snapshot{
				Events: []EventSnapshot{{"bad": make(chan int)}},
				State:  map[string]any{"bad": make(chan int)},
			},
			Snapshot{Events: []EventSnapshot{{"bad": func() {}}}},
			nil,
		)
		require.Nil(t, diffs)
		require.ErrorContains(t, err, `normalize left events section for backend "left"`)
	})
}

func TestCompareReturnsErrorWithoutPartialDiffs(t *testing.T) {
	results := []Result{
		{
			Backend: "first", Key: session.Key{SessionID: "session-1"},
			Snapshot: Snapshot{State: map[string]any{"value": "first"}},
		},
		{
			Backend: "second", Key: session.Key{SessionID: "session-1"},
			Snapshot: Snapshot{State: map[string]any{"value": "second"}},
		},
		{
			Backend: "invalid", Key: session.Key{SessionID: "session-1"},
			Snapshot: Snapshot{State: map[string]any{"value": make(chan int)}},
		},
	}

	diffs, err := Compare(Case{Name: "pairwise"}, results)
	require.Nil(t, diffs)
	require.ErrorContains(t, err, `compare case "pairwise" between backends "first" and "invalid"`)
	require.ErrorContains(t, err, `normalize right state section for backend "invalid"`)
}

func TestAllowedDiffRulesRequireConcreteSegmentBelowSectionRoot(t *testing.T) {
	for _, section := range []string{"session", "events", "state", "memory", "summary", "tracks"} {
		for _, suffix := range []string{"", "*", ".*", "[*]"} {
			path := "$." + section + suffix
			require.Falsef(t, allowedPathHasConcreteSegment(section, path), "section=%q path=%q", section, path)
		}
	}
	for _, path := range []string{
		"$", "$*", "$**", "$.*", "$[*]", "*", "**", "***",
		"$.memory[", "$.memory[abc]", "$.memory..content", "$.memory.content]",
		`$.summary["*"].*`, `$.summary["**"].***`, `$.summary["\u002a"].*`,
	} {
		section := "memory"
		if strings.HasPrefix(path, "$.summary") {
			section = "summary"
		}
		require.Falsef(t, allowedPathHasConcreteSegment(section, path), "path=%q", path)
	}
	require.False(t, allowedPathHasConcreteSegment("summary", "$.memory[0].content"))
	for _, test := range []struct {
		section string
		path    string
	}{
		{section: "memory", path: "$.memory[0]"},
		{section: "memory", path: "$.memory[0].content"},
		{section: "memory", path: "$.memory[*].content"},
		{section: "state", path: "$.state.allowed"},
		{section: "summary", path: `$.summary["filter]key"]`},
		{section: "summary", path: `$.summary["*"].summary`},
		{section: "summary", path: `$.summary[""].summary`},
		{section: "summary", path: `$.summary["root/*"].*`},
	} {
		require.Truef(t, allowedPathHasConcreteSegment(test.section, test.path),
			"section=%q path=%q", test.section, test.path)
	}

	newEntries := func() []Diff {
		return []Diff{
			{Section: "memory", Path: "$.memory[0].content", BackendA: "left", BackendB: "right"},
			{Section: "memory", Path: "$.memory[0].topics[0]", BackendA: "left", BackendB: "right"},
		}
	}
	for _, path := range []string{"$.memory", "$.memory*", "$.memory.*", "$.memory[*]"} {
		rootWildcardEntries := newEntries()
		applyAllowedDiffRules(rootWildcardEntries, []AllowedDiffRule{{
			Section: "memory", Path: path, BackendA: "left", BackendB: "right", Reason: "too broad",
		}})
		require.Falsef(t, rootWildcardEntries[0].Allowed, "path=%q", path)
		require.Falsef(t, rootWildcardEntries[1].Allowed, "path=%q", path)
	}

	partialWildcardEntries := newEntries()
	applyAllowedDiffRules(partialWildcardEntries, []AllowedDiffRule{{
		Section: "memory", Path: "$.memory[*].content", BackendA: "left", BackendB: "right", Reason: "known content drift",
	}})
	require.True(t, partialWildcardEntries[0].Allowed)
	require.False(t, partialWildcardEntries[1].Allowed)
}

func TestAllowedDiffRulesRejectWildcardOnlyQuotedSummaryKeys(t *testing.T) {
	left := Snapshot{Summary: map[string]SummaryEntry{
		"root/tools/weather": {Summary: "left weather"},
		"other.domain":       {Summary: "left other"},
	}}
	right := Snapshot{Summary: map[string]SummaryEntry{
		"root/tools/weather": {Summary: "right weather"},
		"other.domain":       {Summary: "right other"},
	}}
	broadRule := []AllowedDiffRule{{
		Section: "summary", Path: `$.summary["*"].*`,
		BackendA: "left", BackendB: "right", Reason: "too broad",
	}}
	diffs := compareSnapshotsForTest(t, "quoted-wildcard", "session-1", "left", "right", left, right, broadRule)
	require.Len(t, diffs, 2)
	for _, diff := range diffs {
		require.False(t, diff.Allowed, "diff=%+v", diff)
	}

	literalRule := []AllowedDiffRule{{
		Section: "summary", Path: `$.summary["root/*"].summary`,
		BackendA: "left", BackendB: "right", Reason: "known root summary drift",
	}}
	diffs = compareSnapshotsForTest(t, "quoted-literal", "session-1", "left", "right", left, right, literalRule)
	require.Len(t, diffs, 2)
	allowedByKey := make(map[string]bool, len(diffs))
	for _, diff := range diffs {
		key, ok := diff.Context["summary_filter_key"].(string)
		require.True(t, ok)
		allowedByKey[key] = diff.Allowed
	}
	require.True(t, allowedByKey["root/tools/weather"])
	require.False(t, allowedByKey["other.domain"])
}

func TestWildcardMatchHandlesRepeatedAndMissingSegments(t *testing.T) {
	require.True(t, MatchAllowedDiffPath("events.**.id", "events.item.id"))
	require.False(t, MatchAllowedDiffPath("events.*.missing", "events.item.id"))
}

func TestWriteReportUsesEmptyArrayForNilDiffs(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, WriteReport(&out, nil))
	require.Equal(t, "[]\n", out.String())
	require.Error(t, WriteReport(nil, nil))
	require.ErrorContains(t, WriteReport(errorWriter{}, []Diff{{Case: "case"}}), "encode replay diff report")
}

func TestWriteReportEncodesMissingValuesUnambiguously(t *testing.T) {
	legacySentinel := map[string]any{"replay": "missing"}
	encode := func(t *testing.T, diffs []Diff) map[string]any {
		t.Helper()
		var out bytes.Buffer
		require.NoError(t, WriteReport(&out, diffs))
		var report []map[string]any
		require.NoError(t, json.Unmarshal(out.Bytes(), &report))
		require.Len(t, report, 1)
		return report[0]
	}

	t.Run("missing versus json null", func(t *testing.T) {
		diffs := compareSnapshotsForTest(
			t, "missing-null", "session-1", "left", "right",
			Snapshot{State: map[string]any{}},
			Snapshot{State: map[string]any{"value": nil}}, nil,
		)
		entry := encode(t, diffs)
		require.Nil(t, entry["left"])
		require.Nil(t, entry["right"])
		require.Equal(t, true, entry["left_missing"])
		require.NotContains(t, entry, "right_missing")
	})

	t.Run("missing versus legacy sentinel value", func(t *testing.T) {
		diffs := compareSnapshotsForTest(
			t, "missing-sentinel", "session-1", "left", "right",
			Snapshot{State: map[string]any{}},
			Snapshot{State: map[string]any{"value": legacySentinel}}, nil,
		)
		entry := encode(t, diffs)
		require.Nil(t, entry["left"])
		require.Equal(t, legacySentinel, entry["right"])
		require.Equal(t, true, entry["left_missing"])
		require.NotContains(t, entry, "right_missing")
	})

	t.Run("legacy sentinel value versus right missing", func(t *testing.T) {
		diffs := compareSnapshotsForTest(
			t, "sentinel-missing", "session-1", "left", "right",
			Snapshot{State: map[string]any{"value": legacySentinel}},
			Snapshot{State: map[string]any{}}, nil,
		)
		entry := encode(t, diffs)
		require.Equal(t, legacySentinel, entry["left"])
		require.Nil(t, entry["right"])
		require.NotContains(t, entry, "left_missing")
		require.Equal(t, true, entry["right_missing"])
	})

	t.Run("normal diff omits missing fields", func(t *testing.T) {
		diffs := compareSnapshotsForTest(
			t, "normal", "session-1", "left", "right",
			Snapshot{State: map[string]any{"value": "left"}},
			Snapshot{State: map[string]any{"value": "right"}}, nil,
		)
		entry := encode(t, diffs)
		require.NotContains(t, entry, "left_missing")
		require.NotContains(t, entry, "right_missing")
	})
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type recordingSummarizer struct {
	mu             sync.RWMutex
	textBySession  map[session.Key]string
	blockSessionID string
	entered        chan struct{}
	release        <-chan struct{}
	enteredOnce    sync.Once
}

func (s *recordingSummarizer) ShouldSummarize(*session.Session) bool { return true }

func (s *recordingSummarizer) Summarize(_ context.Context, sess *session.Session) (string, error) {
	if sess.ID == s.blockSessionID {
		s.enteredOnce.Do(func() {
			if s.entered != nil {
				close(s.entered)
			}
		})
		if s.release != nil {
			<-s.release
		}
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	s.mu.RLock()
	text, ok := s.textBySession[key]
	s.mu.RUnlock()
	if !ok || text == "" {
		return "smoke summary", nil
	}
	return text, nil
}

func (s *recordingSummarizer) createSummary(
	ctx context.Context,
	service session.Service,
	sess *session.Session,
	step SummaryStep,
) error {
	keys := recordingSummaryKeys(sess, step.FilterKey)
	s.mu.Lock()
	if s.textBySession == nil {
		s.textBySession = make(map[session.Key]string)
	}
	for _, key := range keys {
		s.textBySession[key] = step.Text
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		for _, key := range keys {
			delete(s.textBySession, key)
		}
		s.mu.Unlock()
	}()
	return service.CreateSessionSummary(ctx, sess, step.FilterKey, step.Force)
}

func recordingSummaryKeys(sess *session.Session, filterKey string) []session.Key {
	base := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	keys := []session.Key{
		base,
		{AppName: base.AppName, UserID: base.UserID, SessionID: base.SessionID + ":" + filterKey},
	}
	if filterKey != session.SummaryFilterKeyAllContents {
		keys = append(keys, session.Key{
			AppName: base.AppName, UserID: base.UserID, SessionID: base.SessionID + ":",
		})
	}
	return keys
}

func (*recordingSummarizer) SetPrompt(string) {}

func (*recordingSummarizer) SetModel(model.Model) {}

func (*recordingSummarizer) Metadata() map[string]any { return map[string]any{} }

type recordingSummarySessionService struct {
	session.Service
	eventCounts []int
}

func (s *recordingSummarySessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	s.eventCounts = append(s.eventCounts, len(sess.GetEvents()))
	return s.Service.CreateSessionSummary(ctx, sess, filterKey, force)
}

func replayTimelineEvent(id string, timestamp time.Time) *event.Event {
	return &event.Event{
		ID: id, InvocationID: "timeline", Author: "user", Timestamp: timestamp,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.NewUserMessage(id),
		}}},
	}
}

func trackPayloads(events []TrackEventSnapshot) []TrackPayloadSnapshot {
	out := make([]TrackPayloadSnapshot, 0, len(events))
	for _, evt := range events {
		out = append(out, evt.Payload)
	}
	return out
}

func replayTrackSession(container session.Track, payload json.RawMessage) *session.Session {
	sess := session.NewSession("app", "user", "session-1")
	sess.Tracks = map[session.Track]*session.TrackEvents{
		"tool": {
			Track: container,
			Events: []session.TrackEvent{{
				Track: "tool", Payload: payload, Timestamp: time.Unix(1, 0),
			}},
		},
	}
	return sess
}

func replayMalformedEvent() *event.Event {
	return replayMalformedEventWithExtra(make(chan int))
}

func replayMalformedEventWithExtra(value any) *event.Event {
	return &event.Event{
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ExtraFields: map[string]any{
						"unsupported": value,
					},
				}},
			},
		}}},
		InvocationID: "malformed",
		Author:       "assistant",
	}
}

func replayTestSession(generated string, timestamp time.Time) *session.Session {
	evt := event.Event{
		Response: &model.Response{
			ID:        "response-stable",
			Object:    model.ObjectTypeChatCompletion,
			Created:   1700000000,
			Timestamp: time.Date(2026, time.July, 1, 1, 2, 3, 4, time.FixedZone("UTC+8", 8*60*60)),
			Done:      true,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.NewAssistantMessage("same content"),
			}},
		},
		RequestID:    "request-1",
		InvocationID: "invocation-1",
		Author:       "agent",
		ID:           "event-" + generated,
		Timestamp:    timestamp,
		Branch:       "root",
		FilterKey:    "root",
		Version:      event.CurrentVersion,
	}
	return session.NewSession(
		"app",
		"user",
		"session-1",
		session.WithSessionEvents([]event.Event{evt}),
	)
}
