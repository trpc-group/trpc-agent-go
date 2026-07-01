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
	"testing"

	"github.com/stretchr/testify/require"
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
