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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestJSONSessionServicePersistenceAndFiltering(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "session.json")
	svc := NewJSONSessionService(
		WithJSONSessionPath(path),
		WithJSONSessionSummarizer(staticSummary{}),
		WithJSONSessionSummaryFilterAllowlist("agent/main"),
		WithJSONSessionCascadeFullSessionSummary(true),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	key := session.Key{AppName: "app", UserID: "user", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, session.StateMap{"local": []byte(`{"n":1}`)})
	require.NoError(t, err)
	require.NotNil(t, sess)
	sess.CreatedAt = baseTime.Add(-time.Hour)
	require.NoError(t, svc.withSessionStore(true, func(store *jsonSessionStore) error {
		store.Sessions[sessionKeyString(key)].CreatedAt = sess.CreatedAt
		return nil
	}))

	require.NoError(t, svc.UpdateAppState(ctx, key.AppName, session.StateMap{
		session.StateAppPrefix + "feature": []byte(`"on"`),
	}))
	require.NoError(t, svc.UpdateUserState(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID}, session.StateMap{
		session.StateUserPrefix + "tier": []byte(`"gold"`),
	}))
	require.NoError(t, svc.UpdateSessionState(ctx, key, session.StateMap{
		"local":   []byte(`{"n":2}`),
		"deleted": nil,
	}))

	require.NoError(t, svc.AppendEvent(ctx, sess, messageEvent("json-user", 1, "inv", "user", model.NewUserMessage("hello"))))
	require.NoError(t, svc.AppendEvent(ctx, sess, branchEvent("json-branch", 2, "inv", "assistant", model.NewAssistantMessage("branch"), "agent/main")))
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "tool/demo",
		Payload:   mustJSONRaw(t, map[string]any{"event_type": "start", "duration_ms": 1}),
		Timestamp: baseTime.Add(time.Second),
	}))
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track:     "tool/demo",
		Payload:   mustJSONRaw(t, map[string]any{"event_type": "done", "duration_ms": 2}),
		Timestamp: baseTime.Add(2 * time.Second),
	}))
	require.NoError(t, svc.CreateSessionSummary(ctx, sess, "agent/main", true))
	require.NoError(t, svc.EnqueueSummaryJob(ctx, sess, "agent/main", true))
	require.NoError(t, svc.CreateSessionSummary(ctx, sess, "agent/blocked", true))

	reopened := NewJSONSessionService(
		WithJSONSessionPath(path),
		WithJSONSessionSummarizer(staticSummary{}),
		WithJSONSessionSummaryFilterAllowlist("agent/main"),
		WithJSONSessionCascadeFullSessionSummary(true),
	)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })

	got, err := reopened.GetSession(ctx, key, session.WithEventNum(1))
	require.NoError(t, err)
	require.Len(t, got.Events, 2)
	require.Equal(t, "json-user", got.Events[0].ID)
	require.Equal(t, "json-branch", got.Events[1].ID)
	require.Len(t, got.Tracks["tool/demo"].Events, 1)
	require.JSONEq(t, `{"n":2}`, string(got.State["local"]))
	require.JSONEq(t, `"on"`, string(got.State[session.StateAppPrefix+"feature"]))
	require.JSONEq(t, `"gold"`, string(got.State[session.StateUserPrefix+"tier"]))
	require.Nil(t, got.State["deleted"])

	summaryReader := session.NewSession(
		key.AppName,
		key.UserID,
		key.SessionID,
		session.WithSessionCreatedAt(baseTime.Add(-time.Hour)),
	)
	text, ok := reopened.GetSessionSummaryText(ctx, summaryReader, session.WithSummaryFilterKey("agent/main"))
	require.True(t, ok)
	require.Contains(t, text, "summary:s1:agent/main")
	owners, err := reopened.SummaryOwnerIDs(ctx, key)
	require.NoError(t, err)
	require.Equal(t, key.SessionID, owners["agent/main"])
	require.NotContains(t, got.Summaries, "agent/blocked")

	meta, err := reopened.ListSessions(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID}, session.WithListSessionOnlyMeta())
	require.NoError(t, err)
	require.Len(t, meta, 1)
	require.Empty(t, meta[0].Events)
	require.Nil(t, meta[0].Tracks)

	_, err = reopened.ListSessions(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID}, session.WithListSessionPage(-1, 1))
	require.Error(t, err)
	_, err = reopened.GetSession(ctx, key, session.WithGetSessionEventPage(0, 1))
	require.ErrorIs(t, err, session.ErrEventPageUnsupported)

	require.NoError(t, reopened.DeleteAppState(ctx, key.AppName, session.StateAppPrefix+"feature"))
	require.NoError(t, reopened.DeleteUserState(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID}, session.StateUserPrefix+"tier"))
	appState, err := reopened.ListAppStates(ctx, key.AppName)
	require.NoError(t, err)
	require.Empty(t, appState)
	userState, err := reopened.ListUserStates(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
	require.NoError(t, err)
	require.Empty(t, userState)

	require.NoError(t, reopened.DeleteSession(ctx, key))
	deleted, err := reopened.GetSession(ctx, key)
	require.NoError(t, err)
	require.Nil(t, deleted)
}

func TestJSONSessionServiceErrorsAndOwnedClose(t *testing.T) {
	ctx := context.Background()
	invalid := NewJSONSessionService()
	_, err := invalid.CreateSession(ctx, session.Key{}, nil)
	require.Error(t, err)
	require.NoError(t, invalid.Close())

	owned := NewJSONSessionService(WithJSONSessionSummarizer(staticSummary{}))
	key := session.Key{AppName: "app", UserID: "user", SessionID: "owned"}
	sess, err := owned.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NoError(t, owned.Close())
	require.NoError(t, owned.Close())

	path := filepath.Join(t.TempDir(), "broken.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	broken := NewJSONSessionService(WithJSONSessionPath(path))
	_, err = broken.GetSession(ctx, key)
	require.Error(t, err)

	marker := filepath.Join(t.TempDir(), "marker")
	require.NoError(t, os.WriteFile(marker, []byte("x"), 0o600))
	badPath := filepath.Join(marker, "session.json")
	_, err = NewJSONSessionService(WithJSONSessionPath(badPath)).CreateSession(ctx, key, nil)
	require.Error(t, err)
}

func TestJSONMemoryServiceCRUDSearchAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "memory.json")
	svc := NewJSONMemoryService(WithJSONMemoryPath(path))
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	userKey := memory.UserKey{AppName: "app", UserID: "user"}
	eventTime := baseTime.Add(-time.Hour)

	require.Error(t, svc.AddMemory(ctx, memory.UserKey{}, "bad", nil))
	require.NoError(t, svc.AddMemory(ctx, userKey, "User prefers concise answers.", []string{"style"}, memory.WithMetadata(&memory.Metadata{
		Kind:         memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{" Lee ", "lee", "User"},
		Location:     " Shenzhen ",
	})))
	require.NoError(t, svc.AddMemory(ctx, userKey, "Payment webhook timeout was fixed.", []string{"task"}, memory.WithMetadata(&memory.Metadata{
		Kind: memory.KindFact,
	})))

	limited, err := svc.ReadMemories(ctx, userKey, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
	all, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Len(t, all, 2)

	var firstID string
	for _, entry := range all {
		if entry.Memory.Memory == "User prefers concise answers." {
			firstID = entry.ID
			require.Equal(t, []string{"Lee", "User"}, entry.Memory.Participants)
			require.Equal(t, "Shenzhen", entry.Memory.Location)
		}
	}
	require.NotEmpty(t, firstID)

	updateResult := &memory.UpdateResult{}
	require.NoError(t, svc.UpdateMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: firstID}, "User prefers brief answers.", []string{"style", "preference"}, memory.WithUpdateMetadata(&memory.Metadata{
		Kind:     memory.KindFact,
		Location: "Remote",
	}), memory.WithUpdateResult(updateResult)))
	require.NotEmpty(t, updateResult.MemoryID)
	require.NotEqual(t, firstID, updateResult.MemoryID)

	reopened := NewJSONMemoryService(WithJSONMemoryPath(path))
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	searches, err := reopened.SearchMemories(ctx, userKey, "brief answers")
	require.NoError(t, err)
	require.NotEmpty(t, searches)
	require.Contains(t, searches[0].Memory.Memory, "brief")
	require.NotEmpty(t, reopened.Tools())
	require.NoError(t, reopened.EnqueueAutoMemoryJob(ctx, session.NewSession("app", "user", "s1")))

	require.Error(t, reopened.DeleteMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: firstID}))
	require.NoError(t, reopened.DeleteMemory(ctx, memory.Key{AppName: "app", UserID: "user", MemoryID: updateResult.MemoryID}))
	require.NoError(t, reopened.ClearMemories(ctx, userKey))
	empty, err := reopened.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	require.Empty(t, empty)

	owned := NewJSONMemoryService()
	require.NoError(t, owned.Close())
	require.NoError(t, owned.Close())
}

func TestJSONMemoryServiceErrors(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "broken-memory.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	broken := NewJSONMemoryService(WithJSONMemoryPath(path))
	_, err := broken.ReadMemories(ctx, memory.UserKey{AppName: "app", UserID: "user"}, 0)
	require.Error(t, err)

	marker := filepath.Join(t.TempDir(), "marker")
	require.NoError(t, os.WriteFile(marker, []byte("x"), 0o600))
	badPath := filepath.Join(marker, "memory.json")
	err = NewJSONMemoryService(WithJSONMemoryPath(badPath)).AddMemory(ctx, memory.UserKey{AppName: "app", UserID: "user"}, "memory", nil)
	require.Error(t, err)
}

func TestReplayRuntimeBranches(t *testing.T) {
	ctx := context.Background()
	cfg := RunConfig{AppName: "app", UserID: "user", SessionID: "runtime"}
	_, err := Run(ctx, Backend{Name: "missing"}, cfg, singleTurnCase())
	require.Error(t, err)
	_, err = Run(ctx, jsonReplayBackend(), RunConfig{}, singleTurnCase())
	require.Error(t, err)

	backend := jsonReplayBackend()
	t.Cleanup(func() {
		require.NoError(t, backend.Session.Close())
		require.NoError(t, backend.Memory.Close())
	})
	rt := &replayRuntime{backend: backend, cfg: cfg}
	require.Error(t, rt.apply(ctx, Operation{Kind: "unknown"}))
	require.Error(t, rt.apply(ctx, appendEvent(nil)))
	require.Error(t, rt.apply(ctx, Operation{Kind: OperationCreateSummary}))
	require.Error(t, rt.apply(ctx, Operation{Kind: OperationAddMemory}))
	require.NoError(t, rt.apply(ctx, addMemory("runtime memory", nil, nil)))
	require.NoError(t, rt.apply(ctx, Operation{
		Kind:   OperationUpdateMemory,
		Memory: &MemoryOperation{Content: "runtime memory updated"},
	}))
	require.NoError(t, rt.apply(ctx, Operation{
		Kind:   OperationDeleteMemory,
		Memory: &MemoryOperation{Content: "runtime memory updated"},
	}))
	_, err = rt.resolveMemoryID(ctx, &MemoryOperation{Content: "missing"})
	require.Error(t, err)
	require.Error(t, rt.expectError(ctx, Operation{Operations: []Operation{createSession(nil)}}))
	require.Error(t, rt.applyConcurrent(ctx, []Operation{appendEvent(nil)}))

	noMemory := &replayRuntime{backend: Backend{Name: "no-memory", Session: backend.Session}, cfg: RunConfig{AppName: "app", UserID: "user", SessionID: "no-memory"}}
	require.NoError(t, noMemory.apply(ctx, addMemory("ignored", nil, nil)))

	noTrack := &replayRuntime{
		backend: Backend{Name: "no-track", Session: sessionOnlyService{Service: backend.Session}},
		cfg:     RunConfig{AppName: "app", UserID: "user", SessionID: "no-track"},
	}
	require.NoError(t, noTrack.apply(ctx, appendTrack("ignored", 1, map[string]any{"event_type": "ignored"})))
}

func TestComparatorAndNormalizerBranches(t *testing.T) {
	smallDelta := 0.0005
	tests := []struct {
		name    string
		path    string
		a       any
		b       any
		allowed bool
	}{
		{name: "score", path: "/memories/0/score", a: 0.1, b: 0.9, allowed: true},
		{name: "duration int", path: "/tracks/tool/0/duration_ms", a: 1, b: 1.0005, allowed: true},
		{name: "duration int64", path: "/tracks/tool/0/elapsed_ms", a: int64(2), b: 2.0005, allowed: true},
		{name: "duration float32", path: "/tracks/tool/0/durationMs", a: float32(3), b: 3.0005, allowed: true},
		{name: "duration pointer", path: "/tracks/tool/0/duration_ms", a: &smallDelta, b: 0.001, allowed: true},
		{name: "not allowed", path: "/events/0/content", a: "a", b: "b", allowed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, _ := allowedScalarDiff(tt.path, tt.a, tt.b)
			require.Equal(t, tt.allowed, allowed)
		})
	}

	require.Equal(t, "Fallback", jsonFieldName("Fallback", ""))
	require.Equal(t, "name", jsonFieldName("Fallback", "name,omitempty"))
	require.Equal(t, "-", jsonFieldName("Fallback", "-"))
	require.Equal(t, "a~1b~0c", escapePath("a/b~c"))
	require.Equal(t, "a/b~c", unescapePath("a~1b~0c"))
	require.Equal(t, NormalizedValue{Value: "not-json"}, normalizeBytes([]byte("not-json")))
	require.Equal(t, `["a","b"]`, normalizeTag("b"+event.TagDelimiter+"a"))
	require.Empty(t, normalizeTag(""))
	_, ok := eventMessage(event.Event{})
	require.False(t, ok)

	payload := NormalizedValue{Value: map[string]any{
		"durationMs": int64(7),
		"event_type": "done",
	}}
	require.Equal(t, "done", payloadString(payload, "event_type"))
	require.Equal(t, 7.0, *payloadFloat(payload, "durationMs"))
	require.Nil(t, payloadFloat(NormalizedValue{Value: "x"}, "durationMs"))

	cmp := snapshotComparator{}
	cmp.compare("/typed", 1, "1")
	require.Len(t, cmp.diffs, 1)

	baseline := Snapshot{
		CaseName:  "locators",
		Backend:   "a",
		SessionID: "s",
		Memories:  []NormalizedMemory{{ID: "m0", Content: "a"}},
		MemorySearches: map[string][]NormalizedMemory{
			"a/b": {{ID: "ms0", Content: "hit"}},
		},
		Tracks: map[string][]NormalizedTrack{
			"tool/demo": {{TrackName: "tool/demo", Timestamp: "t1"}},
		},
		Unsupported: []UnsupportedCapability{{Capability: CapabilityTTL, AllowedDiff: true, Explanation: "ttl"}},
	}
	candidate := cloneSnapshot(t, baseline)
	candidate.Backend = "b"
	candidate.Memories = append(candidate.Memories, NormalizedMemory{ID: "m1", Content: "b"})
	candidate.MemorySearches["a/b"][0].Content = "changed"
	candidate.Tracks["tool/demo"][0].Timestamp = "t2"
	diffs := CompareSnapshots(baseline, candidate)
	require.True(t, hasDiffAtPath(diffs, "/memories/1"))
	require.True(t, hasDiffAtPath(diffs, "/memory_searches/a~1b/0/content"))
	require.True(t, hasDiffAtPath(diffs, "/tracks/tool~1demo/0/timestamp"))
	require.True(t, hasDiffAtPath(diffs, "/capabilities/ttl"))
}

func TestWriteReportErrors(t *testing.T) {
	err := WriteReport(filepath.Join(t.TempDir(), "bad.json"), Report{
		Diffs: []Diff{{Baseline: func() {}}},
	})
	require.Error(t, err)

	marker := filepath.Join(t.TempDir(), "marker")
	require.NoError(t, os.WriteFile(marker, []byte("x"), 0o600))
	err = WriteReport(filepath.Join(marker, "report.json"), Report{})
	require.Error(t, err)
}

type sessionOnlyService struct {
	session.Service
}

func mustJSONRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
}
