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
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestHarnessReferenceRun(t *testing.T) {
	sessionSvc, memorySvc, profile, err := InMemoryFactory()()
	require.NoError(t, err)
	defer sessionSvc.Close()
	defer memorySvc.Close()

	h := NewHarness(DefaultHarnessOpts())
	h.AddBackend(NamedBackend{
		Name:           "inmemory",
		Profile:        profile,
		SessionService: sessionSvc,
		MemoryService:  memorySvc,
	})
	report, err := h.Run([]ReplayCase{CaseSingleTurnText})
	require.NoError(t, err)
	require.Equal(t, 1, report.PassedCases)
	require.Equal(t, 0, report.FailedCases)
	require.Equal(t, "inmemory", report.Results[0].Comparisons[0].Reference)
}

func TestHarnessReferenceModeUsesStableSnapshotOrder(t *testing.T) {
	h := NewHarness(DefaultHarnessOpts())
	snapshots := map[string]*SessionSnapshot{
		"z":        harnessTestSnapshot("z"),
		"inmemory": harnessTestSnapshot("inmemory"),
		"a":        harnessTestSnapshot("a"),
	}
	profiles := map[string]BackendProfile{
		"z":        InMemoryProfile(),
		"inmemory": InMemoryProfile(),
		"a":        InMemoryProfile(),
	}

	comparisons := h.compareSnapshots(CaseSingleTurnText, snapshots, profiles)
	require.Len(t, comparisons, 2)
	require.Equal(t, "a", comparisons[0].BackendB)
	require.Equal(t, "z", comparisons[1].BackendB)
	require.Equal(t, "inmemory", comparisons[0].Reference)
	require.Equal(t, "inmemory", comparisons[1].Reference)
}

func TestNewHarnessHonorsExplicitComparisonOptions(t *testing.T) {
	h := NewHarness(HarnessOpts{
		ComparisonMode:   ComparisonPairs,
		ReferenceBackend: "sqlite",
	})
	require.Equal(t, ComparisonPairs, h.mode)
	require.Equal(t, "sqlite", h.reference)
}

func TestHarnessComparisonPairsComparesAllBackends(t *testing.T) {
	h := NewHarness(HarnessOpts{ComparisonMode: ComparisonPairs})
	snapshots := map[string]*SessionSnapshot{
		"a": harnessTestSnapshot("a"),
		"b": harnessTestSnapshot("b"),
		"c": harnessTestSnapshot("c"),
	}
	profiles := map[string]BackendProfile{
		"a": InMemoryProfile(),
		"b": InMemoryProfile(),
		"c": InMemoryProfile(),
	}

	comparisons := h.compareSnapshots(CaseSingleTurnText, snapshots, profiles)
	require.Len(t, comparisons, 3)
	require.Equal(t, "a", comparisons[0].BackendA)
	require.Equal(t, "b", comparisons[0].BackendB)
	require.Equal(t, "a", comparisons[1].BackendA)
	require.Equal(t, "c", comparisons[1].BackendB)
	require.Equal(t, "b", comparisons[2].BackendA)
	require.Equal(t, "c", comparisons[2].BackendB)
}

func TestEnsureSessionFallsBackToExistingSession(t *testing.T) {
	ctx := context.Background()
	createErr := errors.New("create failed")
	sessionSvc := sessioninmemory.NewSessionService()
	defer sessionSvc.Close()
	expected, err := sessionSvc.CreateSession(ctx, defaultSessionKey, nil)
	require.NoError(t, err)

	exec := &caseExecutor{
		backend: NamedBackend{
			Name: "fallback",
			SessionService: createErrorSessionService{
				SessionService: sessionSvc,
				err:            createErr,
			},
		},
		sessions: map[session.Key]*session.Session{},
		snapshot: &SessionSnapshot{BackendName: "fallback"},
	}
	got, err := exec.ensureSession(ctx, defaultSessionKey)
	require.NoError(t, err)
	require.Equal(t, expected.ID, got.ID)
	require.Equal(t, got, exec.sessions[defaultSessionKey])
}

func TestExecuteAddMemoryPropagatesReadError(t *testing.T) {
	readErr := errors.New("read memories failed")
	sessionSvc := sessioninmemory.NewSessionService()
	defer sessionSvc.Close()

	_, err := executeCase(context.Background(), ReplayCase{
		Name: "memory_read_error",
		Steps: []ReplayStep{
			AddMemoryStep{
				Key:     "memory.add",
				UserKey: memory.UserKey{AppName: "app", UserID: "user"},
				Memory:  "remember this",
			},
		},
	}, NamedBackend{
		Name:           "memory-read-error",
		Profile:        InMemoryProfile(),
		SessionService: sessionSvc,
		MemoryService:  readErrorMemoryService{err: readErr},
	})
	require.ErrorIs(t, err, readErr)
}

func TestExecuteSearchMemoryAppliesStepLimit(t *testing.T) {
	sessionSvc := sessioninmemory.NewSessionService()
	defer sessionSvc.Close()

	snapshot, err := executeCase(context.Background(), ReplayCase{
		Name: "memory_search_limit",
		Steps: []ReplayStep{
			SearchMemoryStep{
				Key:     "memory.search",
				UserKey: memory.UserKey{AppName: "app", UserID: "user"},
				Query:   "go",
				Limit:   1,
			},
		},
	}, NamedBackend{
		Name:           "memory-search-limit",
		Profile:        InMemoryProfile(),
		SessionService: sessionSvc,
		MemoryService: readErrorMemoryService{searchResults: []*memory.Entry{
			{ID: "first", Memory: &memory.Memory{Memory: "go first"}},
			{ID: "second", Memory: &memory.Memory{Memory: "go second"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, snapshot.MemSearchResults, 1)
	require.Equal(t, "first", snapshot.MemSearchResults[0].ID)
}

func TestExecuteAppendTrackCapturesPersistedTrack(t *testing.T) {
	sessionSvc := sessioninmemory.NewSessionService()
	defer sessionSvc.Close()

	snapshot, err := executeCase(context.Background(), ReplayCase{
		Name: "append_track",
		Steps: []ReplayStep{
			AppendTrackStep{
				Key:        "track.append",
				SessionKey: defaultSessionKey,
				Event: &session.TrackEvent{
					Track:     "tool",
					Payload:   json.RawMessage(`{"status":"ok"}`),
					Timestamp: time.Unix(1, 0).UTC(),
				},
			},
			GetSessionStep{Key: "track.get", SessionKey: defaultSessionKey},
		},
	}, NamedBackend{
		Name:           "track",
		Profile:        InMemoryProfile(),
		SessionService: sessionSvc,
	})
	require.NoError(t, err)
	require.Contains(t, snapshot.TrackEvents, "tool")
	require.Len(t, snapshot.TrackEvents["tool"].Events, 1)
	require.JSONEq(t, `{"status":"ok"}`, string(snapshot.TrackEvents["tool"].Events[0].Payload))
}

func TestExecuteAppendTrackIgnoresSessionServiceWithoutTrackSupport(t *testing.T) {
	sessionSvc := sessioninmemory.NewSessionService()
	defer sessionSvc.Close()

	snapshot, err := executeCase(context.Background(), ReplayCase{
		Name: "append_track_unsupported",
		Steps: []ReplayStep{
			AppendTrackStep{
				Key:        "track.append",
				SessionKey: defaultSessionKey,
				Event: &session.TrackEvent{
					Track:     "tool",
					Payload:   json.RawMessage(`{"status":"ok"}`),
					Timestamp: time.Unix(1, 0).UTC(),
				},
			},
		},
	}, NamedBackend{
		Name:           "session-only",
		Profile:        InMemoryProfile(),
		SessionService: sessionOnlyService{Service: sessionSvc},
	})
	require.NoError(t, err)
	require.Empty(t, snapshot.TrackEvents)
}

func TestHarnessNoMatchingBackend(t *testing.T) {
	sessionSvc, memorySvc, profile, err := InMemoryFactory()()
	require.NoError(t, err)
	defer sessionSvc.Close()
	defer memorySvc.Close()
	profile.SupportsTrack = false

	h := NewHarness(DefaultHarnessOpts())
	h.AddBackend(NamedBackend{
		Name:           "limited",
		Profile:        profile,
		SessionService: sessionSvc,
		MemoryService:  memorySvc,
	})
	report, err := h.Run([]ReplayCase{CaseTrackEvents})
	require.NoError(t, err)
	require.Equal(t, 1, report.SkippedCases)
	require.Equal(t, StatusSkipped, report.Results[0].OverallStatus)
}

func TestHarnessMissingMemoryServiceSkipsMemoryCase(t *testing.T) {
	sessionSvc, memorySvc, profile, err := InMemoryFactory()()
	require.NoError(t, err)
	defer sessionSvc.Close()
	defer memorySvc.Close()

	h := NewHarness(DefaultHarnessOpts())
	h.AddBackend(NamedBackend{
		Name:           "session-only",
		Profile:        profile,
		SessionService: sessionSvc,
	})
	report, err := h.Run([]ReplayCase{CaseMemoryWriteAndRead})
	require.NoError(t, err)
	require.Equal(t, 1, report.SkippedCases)
	require.Equal(t, StatusSkipped, report.Results[0].OverallStatus)
	require.Equal(t, "memory", report.Unsupported[0].Feature)
}

func harnessTestSnapshot(backend string) *SessionSnapshot {
	return &SessionSnapshot{
		BackendName: backend,
		Session:     session.NewSession("app", "user", "session"),
	}
}

type readErrorMemoryService struct {
	err           error
	searchResults []*memory.Entry
}

func (s readErrorMemoryService) AddMemory(
	context.Context,
	memory.UserKey,
	string,
	[]string,
	...memory.AddOption,
) error {
	return nil
}

func (s readErrorMemoryService) UpdateMemory(
	context.Context,
	memory.Key,
	string,
	[]string,
	...memory.UpdateOption,
) error {
	return nil
}

func (s readErrorMemoryService) DeleteMemory(context.Context, memory.Key) error {
	return nil
}

func (s readErrorMemoryService) ClearMemories(context.Context, memory.UserKey) error {
	return nil
}

func (s readErrorMemoryService) ReadMemories(
	context.Context,
	memory.UserKey,
	int,
) ([]*memory.Entry, error) {
	return nil, s.err
}

func (s readErrorMemoryService) SearchMemories(
	context.Context,
	memory.UserKey,
	string,
	...memory.SearchOption,
) ([]*memory.Entry, error) {
	return s.searchResults, nil
}

func (s readErrorMemoryService) Tools() []tool.Tool {
	return nil
}

func (s readErrorMemoryService) EnqueueAutoMemoryJob(context.Context, *session.Session) error {
	return nil
}

func (s readErrorMemoryService) Close() error {
	return nil
}

type createErrorSessionService struct {
	*sessioninmemory.SessionService
	err error
}

func (s createErrorSessionService) CreateSession(
	context.Context,
	session.Key,
	session.StateMap,
	...session.Option,
) (*session.Session, error) {
	return nil, s.err
}

type sessionOnlyService struct {
	session.Service
}
