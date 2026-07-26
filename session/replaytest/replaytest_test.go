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
			name: "insufficient query results",
			tc: Case{Name: "query", Queries: []MemoryQuery{{
				Query: "missing", MinResults: 1,
			}}},
			want: `memory query 0 for case "query" returned 0 results, want at least 1`,
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

type failingMemoryService struct {
	memory.Service
	addErr    error
	readErr   error
	searchErr error
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
	return s.Service.SearchMemories(ctx, userKey, query, opts...)
}

func TestBuildSnapshotNormalizesGeneratedEventFields(t *testing.T) {
	left := BuildSnapshot(replayTestSession("left", time.Unix(1, 0)), nil)
	right := BuildSnapshot(replayTestSession("right", time.Unix(2, 0)), nil)
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
	require.Nil(t, normalizeRawJSON(nil))
	require.Equal(t, StateBytesSnapshot{Kind: "utf8", Value: "{"}, normalizeRawJSON(json.RawMessage("{")))

	var decoded any
	require.EqualError(t, decodeJSON([]byte(`{} []`), &decoded), "unexpected trailing JSON value")
	require.ErrorContains(t, decodeJSON([]byte(`{} {`), &decoded), "decode trailing JSON value")
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
