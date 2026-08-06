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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func runReplayCaseReport(t *testing.T, tc ReplayCase) *Report {
	t.Helper()
	backend, cleanup := newReplayCaseBackend(t)
	defer cleanup()

	h := NewHarness(DefaultHarnessOpts())
	h.AddBackend(backend)
	report, err := h.Run([]ReplayCase{tc})
	require.NoError(t, err)
	return report
}

func runReplayCaseSnapshot(t *testing.T, tc ReplayCase) *SessionSnapshot {
	t.Helper()
	backend, cleanup := newReplayCaseBackend(t)
	defer cleanup()

	snapshot, err := executeCase(context.Background(), tc, backend)
	require.NoError(t, err)
	return snapshot
}

func newReplayCaseBackend(t *testing.T) (NamedBackend, func()) {
	t.Helper()
	sessionSvc, memorySvc, profile, err := InMemoryFactory()()
	require.NoError(t, err)
	return NamedBackend{
			Name:           "inmemory",
			Profile:        profile,
			SessionService: sessionSvc,
			MemoryService:  memorySvc,
		}, func() {
			require.NoError(t, sessionSvc.Close())
			require.NoError(t, memorySvc.Close())
		}
}

func newFullReplayBackend(t *testing.T) (NamedBackend, func()) {
	t.Helper()
	sessionSvc := sessioninmemory.NewSessionService(
		sessioninmemory.WithSummarizer(NewFakeSummarizer()),
		sessioninmemory.WithAsyncSummaryNum(1),
		sessioninmemory.WithSummaryJobTimeout(time.Second),
	)
	memorySvc := memoryinmemory.NewMemoryService()
	return NamedBackend{
			Name:           "inmemory",
			Profile:        InMemoryProfile(),
			SessionService: sessionSvc,
			MemoryService:  memorySvc,
		}, func() {
			require.NoError(t, sessionSvc.Close())
			require.NoError(t, memorySvc.Close())
		}
}
