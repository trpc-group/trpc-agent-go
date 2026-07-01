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
	"errors"
	"testing"

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
	err error
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
	return nil, nil
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
