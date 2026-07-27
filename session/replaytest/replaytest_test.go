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
	"sort"
	"strings"
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
			_, err := Run(context.Background(), Backend{Name: test.name}, Case{Name: "invalid"})
			require.EqualError(t, err, test.want)
		})
	}
}

func TestRunRejectsMissingServices(t *testing.T) {
	ctx := context.Background()
	_, err := Run(ctx, Backend{Name: "missing-session"}, Case{Name: "invalid"})
	require.EqualError(t, err, `replay backend "missing-session" has nil session service`)

	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	_, err = Run(ctx, Backend{
		Name:           "missing-memory",
		SessionService: sessionService,
	}, Case{Name: "invalid"})
	require.EqualError(t, err, `replay backend "missing-memory" has nil memory service`)
}

func TestRunReportsInvalidCaseOperations(t *testing.T) {
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
			want: `concurrent memory operations for case "concurrent-update": unsupported concurrent memory operation "update"`,
		},
		{
			name: "query contents mismatch",
			tc: Case{Name: "query", Queries: []MemoryQuery{{
				Query: "missing", ExpectedContents: []string{"missing memory"},
			}}},
			want: `memory query 0 for case "query" returned contents [], want ["missing memory"]`,
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
			_, err := Run(context.Background(), Backend{
				Name:           "in_memory",
				SessionService: sessionService,
				TrackService:   trackService,
				MemoryService:  memoryService,
			}, test.tc)
			require.EqualError(t, err, test.want)
		})
	}
}

func TestSummaryEventPrefixValidation(t *testing.T) {
	zero, one, two, negative, tooLarge := 0, 1, 2, -1, 3
	tests := []struct {
		name     string
		prefix   *int
		appended int
		want     int
		wantErr  string
	}{
		{name: "nil means all", want: 2},
		{name: "zero", prefix: &zero, want: 0},
		{name: "same prefix", prefix: &one, appended: 1, want: 1},
		{name: "increasing prefix", prefix: &two, appended: 1, want: 2},
		{
			name: "negative", prefix: &negative,
			wantErr: `summary step 0 for case "timeline" has event prefix -1 outside [0,2]`,
		},
		{
			name: "too large", prefix: &tooLarge,
			wantErr: `summary step 0 for case "timeline" has event prefix 3 outside [0,2]`,
		},
		{
			name: "decreasing", prefix: &one, appended: 2,
			wantErr: `summary step 0 for case "timeline" has event prefix 1 before already appended prefix 2`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tc := Case{
				Name: "timeline", Events: []*event.Event{{}, {}},
				Summaries: []SummaryStep{{EventPrefix: test.prefix}},
			}
			got, err := summaryEventPrefix(tc, 0, test.appended)
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
	baseTime := time.Date(2026, time.July, 28, 1, 2, 3, 0, time.UTC)
	events := make([]*event.Event, 0, finalPrefix)
	for i, id := range []string{"event-0", "event-1", "event-2", "event-3"} {
		events = append(events, replayTimelineEvent(id, baseTime.Add(time.Duration(i)*time.Second)))
	}
	result, err := Run(context.Background(), Backend{
		Name: "in_memory", SessionService: sessionService, MemoryService: memoryService,
		SetSummaryText: func(text string) { summarizer.text = text },
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

func TestRunAcceptsFullTrackPayloadDomain(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := meminmemory.NewMemoryService()
	defer memoryService.Close()

	result, err := Run(context.Background(), Backend{
		Name: "in_memory", SessionService: sessionService,
		TrackService: sessionService, MemoryService: memoryService,
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessionService := sessinmemory.NewSessionService()
			defer sessionService.Close()
			memoryService := &failingMemoryService{Service: meminmemory.NewMemoryService()}
			defer memoryService.Close()
			test.wrap(memoryService)
			_, err := Run(context.Background(), Backend{
				Name:           "in_memory",
				SessionService: sessionService,
				MemoryService:  memoryService,
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

	result, err := Run(context.Background(), Backend{
		Name:           "in_memory",
		SessionService: sessionService,
		MemoryService:  memoryService,
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

	result, err := Run(context.Background(), Backend{
		Name:           "in_memory",
		SessionService: sessionService,
		MemoryService:  memoryService,
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

func TestRunRejectsEmptyUpdatedMemoryID(t *testing.T) {
	sessionService := sessinmemory.NewSessionService()
	defer sessionService.Close()
	memoryService := &emptyUpdateResultMemoryService{Service: meminmemory.NewMemoryService()}
	defer memoryService.Close()

	_, err := Run(context.Background(), Backend{
		Name:           "empty_update_result",
		SessionService: sessionService,
		MemoryService:  memoryService,
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

	result, err := Run(context.Background(), Backend{
		Name:           "ordered_memory",
		SessionService: sessionService,
		MemoryService:  memoryService,
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

	result, err := Run(context.Background(), Backend{
		Name:           "in_memory",
		SessionService: sessionService,
		MemoryService:  memoryService,
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
	_, err := findMemoryID(context.Background(), service, userKey, MemoryOp{
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
			name: "identity not found",
			service: &fixedReadMemoryService{entries: []*memory.Entry{
				nil,
				{ID: "nil-memory", AppName: "app", UserID: "user"},
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
			_, err := findMemoryID(context.Background(), test.service, userKey, op)
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

	_, err := Run(ctx, Backend{
		Name: "in_memory", SessionService: sessionService, MemoryService: memoryService,
	}, tc)
	require.NoError(t, err)
	peerKey := replayKey(tc.Name)
	peerKey.SessionID += "-scope-peer"
	peer, err := sessionService.GetSession(ctx, peerKey)
	require.NoError(t, err)
	require.Nil(t, peer)
	require.Equal(t, session.StateMap{"nil": nil}, stateWithoutPrefix(
		session.StateMap{session.StateAppPrefix + "nil": nil}, session.StateAppPrefix,
	))
	require.Equal(t, session.StateMap{
		session.StateAppPrefix + "nil":  nil,
		session.StateUserPrefix + "nil": nil,
	}, mergeScopedState(session.StateMap{"nil": nil}, session.StateMap{"nil": nil}))
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
			wantErrors: []string{"create state-scope peer", "create peer failed"},
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

			_, err := Run(context.Background(), Backend{
				Name: "scope_fake", SessionService: sessionService, MemoryService: memoryService,
			}, replayStateScopeCase(strings.ReplaceAll(test.name, " ", "-")))
			require.Error(t, err)
			for _, want := range test.wantErrors {
				require.ErrorContains(t, err, want)
			}
			require.Equal(t, test.wantDeleteCalls, sessionService.deleteCalls)
		})
	}
}

type emptyUpdateResultMemoryService struct {
	memory.Service
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
	emptyAppState  bool
	stripPeerState bool
	deleteErr      error
	listAppErr     error
	listUserErr    error
	createPeerErr  error
	getPeerErr     error
	nilCreatePeer  bool
	nilGetPeer     bool
	deleteCalls    int
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
	if err == nil && strings.HasSuffix(key.SessionID, "-scope-peer") && s.nilCreatePeer {
		return nil, nil
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
	rightSession.Events[0].Response.Timestamp = timestamp.Add(time.Second)
	left := BuildSnapshot(leftSession, nil)
	right := BuildSnapshot(rightSession, nil)
	diffs := CompareSnapshots(
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

func TestBuildSnapshotPreservesSuppliedEventTimestamp(t *testing.T) {
	leftTime := time.Date(2026, time.July, 24, 8, 9, 10, 123, time.FixedZone("UTC+8", 8*60*60))
	rightTime := leftTime.Add(time.Second)
	left := BuildSnapshot(replayTestSession("same", leftTime), nil)
	right := BuildSnapshot(replayTestSession("same", rightTime), nil)

	require.Equal(t, normalizeTime(leftTime), left.Events[0]["timestamp"])
	diffs := CompareSnapshots("timestamp", "session-1", "left", "right", left, right, nil)
	require.Len(t, diffs, 1)
	require.Equal(t, "events", diffs[0].Section)
	require.Equal(t, "$.events[0].timestamp", diffs[0].Path)
	require.Equal(t, normalizeTime(leftTime), diffs[0].Left)
	require.Equal(t, normalizeTime(rightTime), diffs[0].Right)
	require.Equal(t, 0, diffs[0].Context["event_index"])
	require.False(t, diffs[0].Allowed)

	sameInstant := BuildSnapshot(replayTestSession("same", leftTime.UTC()), nil)
	require.Empty(t, CompareSnapshots("timestamp", "session-1", "left", "right", left, sameInstant, nil))
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
	empty := BuildSnapshot(nil, nil)
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
	left := BuildSnapshot(leftSession, nil)
	right := BuildSnapshot(rightSession, nil)

	require.Equal(t, "tool", left.Tracks[0].Name)
	require.Equal(t, "tool", left.Tracks[0].Track)
	require.Equal(t, "tool", left.Tracks[0].Events[0].Track)
	diffs := CompareSnapshots("container", "session-1", "left", "right", left, right, nil)
	require.Len(t, diffs, 1)
	require.Equal(t, "tracks", diffs[0].Section)
	require.Equal(t, "$.tracks[0].track", diffs[0].Path)
	require.Equal(t, "tool", diffs[0].Context["track_name"])
	require.False(t, diffs[0].Allowed)

	rightSession = replayTrackSession("tool", nil)
	right = BuildSnapshot(rightSession, nil)
	diffs = CompareSnapshots("payload", "session-1", "left", "right", left, right, nil)
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
	_, ok = summaryFilterKey("$.events[0]")
	require.False(t, ok)
	key, ok := summaryFilterKey("$.summary.branch.boundary.version")
	require.True(t, ok)
	require.Equal(t, "branch", key)
	key, ok = summaryFilterKey("$.summary.branch[0]")
	require.True(t, ok)
	require.Equal(t, "branch", key)
}

func TestCompareSnapshotsAddsContextAndAppliesExplicitRule(t *testing.T) {
	left := BuildSnapshot(replayTestSession("same", time.Unix(1, 0)), nil)
	right := BuildSnapshot(replayTestSession("same", time.Unix(1, 0)), nil)
	right.Events[0]["author"] = "different"
	rules := []AllowedDiffRule{{
		Section: "events", Path: "$.events[0].author",
		BackendA: "left", BackendB: "right", Reason: "fixture drift",
	}}
	diffs := CompareSnapshots("case", "session-1", "left", "right", left, right, rules)
	require.Len(t, diffs, 1)
	require.True(t, diffs[0].Allowed)
	require.Equal(t, "fixture drift", diffs[0].Reason)
	require.Equal(t, 0, diffs[0].Context["event_index"])
	require.False(t, HasUnallowedDiffs(diffs))
}

func TestAllowedDiffRulesRejectRootOnlyWildcards(t *testing.T) {
	invalid := []string{"$", "$*", "$**", "$.*", "$[*]", "*", "**", "***"}
	for _, path := range invalid {
		require.Falsef(t, allowedPathHasConcreteSegment(path), "path=%q", path)
	}
	require.True(t, allowedPathHasConcreteSegment("$.memory[0].content"))
	require.True(t, allowedPathHasConcreteSegment("$.memory[*].content"))

	newEntries := func() []Diff {
		return []Diff{
			{Section: "memory", Path: "$.memory[0].content", BackendA: "left", BackendB: "right"},
			{Section: "memory", Path: "$.memory[0].topics[0]", BackendA: "left", BackendB: "right"},
		}
	}
	rootWildcardEntries := newEntries()
	applyAllowedDiffRules(rootWildcardEntries, []AllowedDiffRule{{
		Section: "memory", Path: "$*", BackendA: "left", BackendB: "right", Reason: "too broad",
	}})
	require.False(t, rootWildcardEntries[0].Allowed)
	require.False(t, rootWildcardEntries[1].Allowed)

	partialWildcardEntries := newEntries()
	applyAllowedDiffRules(partialWildcardEntries, []AllowedDiffRule{{
		Section: "memory", Path: "$.memory[*].content", BackendA: "left", BackendB: "right", Reason: "known content drift",
	}})
	require.True(t, partialWildcardEntries[0].Allowed)
	require.False(t, partialWildcardEntries[1].Allowed)
}

func TestWildcardMatchHandlesRepeatedAndMissingSegments(t *testing.T) {
	require.True(t, wildcardMatch("events.**.id", "events.item.id"))
	require.False(t, wildcardMatch("events.*.missing", "events.item.id"))
}

func TestWriteReportUsesEmptyArrayForNilDiffs(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, WriteReport(&out, nil))
	require.Equal(t, "[]\n", out.String())
	require.Error(t, WriteReport(nil, nil))
	require.ErrorContains(t, WriteReport(errorWriter{}, []Diff{{Case: "case"}}), "encode replay diff report")
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type recordingSummarizer struct {
	text string
}

func (s *recordingSummarizer) ShouldSummarize(*session.Session) bool { return true }

func (s *recordingSummarizer) Summarize(_ context.Context, _ *session.Session) (string, error) {
	return s.text, nil
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

func replayTestSession(generated string, timestamp time.Time) *session.Session {
	evt := event.Event{
		Response: &model.Response{
			ID:        "response-" + generated,
			Object:    model.ObjectTypeChatCompletion,
			Timestamp: timestamp,
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
