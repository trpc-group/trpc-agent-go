//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestRunnerReplaysStandardCasesWithoutFalsePositives(t *testing.T) {
	cases := StandardCases()
	require.Len(t, cases, 10)

	runner := Runner{
		BaselineBackend: "inmemory-a",
		Backends: []BackendFactory{
			inMemoryFactory("inmemory-a"),
			inMemoryFactory("inmemory-b"),
		},
	}
	started := time.Now()
	result, err := runner.Run(context.Background(), cases)
	require.NoError(t, err)
	require.Less(t, time.Since(started), 30*time.Second)
	require.Len(t, result.Report.Cases, 10)
	require.Zero(t, result.Report.Summary.Failed)
	require.Equal(t, 10, result.Report.Summary.Passed)
	require.Zero(t, result.Report.Summary.DisallowedDiffs)
	for _, comparison := range result.Report.Cases {
		require.Equal(t, ComparisonPassed, comparison.Status)
	}

	recovery := result.Snapshots["failure_recovery"]["inmemory-a"]
	require.Len(t, recovery.Events, 2)
	require.Len(t, recovery.Memories, 1)
	require.NotEmpty(t, recovery.Recoveries)

	compressed := result.Snapshots["summary_event_truncation"]["inmemory-a"].Contexts["agent/long"]
	require.Equal(
		t,
		[]string{"long-u2", "long-a2"},
		compressed.RetainedEvents,
	)
	require.Equal(
		t,
		[]string{"long-u3", "long-a3"},
		compressed.EventsAfterCutoff,
	)
}

func TestRunnerReportsDisabledBackend(t *testing.T) {
	runner := Runner{
		BaselineBackend: "inmemory",
		Backends: []BackendFactory{
			inMemoryFactory("inmemory"),
			{
				Name:           "postgres",
				Capabilities:   CoreCapabilities(),
				DisabledReason: "REPLAY_POSTGRES_DSN is not set",
			},
		},
	}
	result, err := runner.Run(
		context.Background(),
		StandardCases()[:1],
	)
	require.NoError(t, err)
	require.Len(t, result.Report.Cases, 1)
	comparison := result.Report.Cases[0]
	require.Equal(t, ComparisonUnsupported, comparison.Status)
	require.Len(t, comparison.Unsupported, 1)
	require.True(t, comparison.Unsupported[0].AllowedDiff)
	require.Contains(t, comparison.Unsupported[0].Reason, "REPLAY_POSTGRES_DSN")
}

func TestStandardCasesDetectInjectedInconsistency(t *testing.T) {
	cases := StandardCases()
	runner := Runner{
		BaselineBackend: "inmemory-a",
		Backends: []BackendFactory{
			inMemoryFactory("inmemory-a"),
			inMemoryFactory("inmemory-b"),
		},
	}
	result, err := runner.Run(context.Background(), cases)
	require.NoError(t, err)

	mutators := map[string]func(*Snapshot){
		"single_turn_dialogue": func(s *Snapshot) {
			s.Events[0].Author = "wrong-author"
		},
		"multi_turn_order": func(s *Snapshot) {
			s.Events[1], s.Events[2] = s.Events[2], s.Events[1]
		},
		"tool_call_round_trip": func(s *Snapshot) {
			s.Events[1].ToolCalls[0].Arguments = map[string]any{
				"city": "wrong",
			}
		},
		"state_lifecycle": func(s *Snapshot) {
			s.State["counter"] = int64(999)
		},
		"memory_lifecycle": func(s *Snapshot) {
			s.Memories[0].Content = "polluted memory"
		},
		"summary_update": func(s *Snapshot) {
			summary := s.Summaries["agent/research"]
			summary.Text = "stale summary"
			s.Summaries["agent/research"] = summary
		},
		"summary_event_truncation": func(s *Snapshot) {
			delete(s.Summaries, "agent/long")
		},
		"track_observability": func(s *Snapshot) {
			s.Tracks["tools"][1].Error = "injected error"
		},
		"concurrent_interleaving": func(s *Snapshot) {
			s.Events[0].Content = "wrong concurrent result"
		},
		"failure_recovery": func(s *Snapshot) {
			s.Events = append(s.Events, s.Events[0])
		},
	}

	detected := 0
	for _, replayCase := range cases {
		baseline := result.Snapshots[replayCase.Name]["inmemory-a"]
		target := cloneSnapshotForTest(t, baseline)
		target.Backend = "inmemory-b"
		mutators[replayCase.Name](target)
		diffs := CompareSnapshots(
			replayCase.Name,
			baseline,
			target,
			replayCase.AllowedDiffs,
		)
		require.Truef(
			t,
			hasDisallowedDifference(diffs),
			"case %s did not detect injected inconsistency",
			replayCase.Name,
		)
		detected++
	}
	require.Equal(t, len(cases), detected)
	t.Logf(
		"detected %d/%d injected replay inconsistencies",
		detected,
		len(cases),
	)
}

func TestSummaryAnomaliesAreAlwaysDetected(t *testing.T) {
	cases := StandardCases()
	var summaryCase ReplayCase
	for _, replayCase := range cases {
		if replayCase.Name == "summary_update" {
			summaryCase = replayCase
			break
		}
	}
	require.NotEmpty(t, summaryCase.Name)

	runner := Runner{
		BaselineBackend: "inmemory-a",
		Backends: []BackendFactory{
			inMemoryFactory("inmemory-a"),
			inMemoryFactory("inmemory-b"),
		},
	}
	result, err := runner.Run(
		context.Background(),
		[]ReplayCase{summaryCase},
	)
	require.NoError(t, err)
	baseline := result.Snapshots[summaryCase.Name]["inmemory-a"]

	tests := map[string]func(*Snapshot){
		"missing": func(s *Snapshot) {
			delete(s.Summaries, "agent/research")
		},
		"overwrite": func(s *Snapshot) {
			item := s.Summaries["agent/research"]
			item.Text = "first revision"
			s.Summaries["agent/research"] = item
		},
		"session ownership": func(s *Snapshot) {
			item := s.Summaries["agent/research"]
			item.SessionID = "another-session"
			s.Summaries["agent/research"] = item
		},
		"filter key": func(s *Snapshot) {
			item := s.Summaries["agent/research"]
			delete(s.Summaries, "agent/research")
			item.FilterKey = "agent/wrong"
			s.Summaries["agent/wrong"] = item
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			target := cloneSnapshotForTest(t, baseline)
			target.Backend = "inmemory-b"
			mutate(target)
			diffs := CompareSnapshots(
				summaryCase.Name,
				baseline,
				target,
				nil,
			)
			require.True(t, hasDisallowedDifference(diffs))
		})
	}
	t.Log("detected summary missing, overwrite, session ownership, and filter-key anomalies")
}

func TestValidateExpectationsUsesDedicatedDifferenceSource(t *testing.T) {
	expected := 1
	diffs := validateExpectations(
		ReplayCase{
			Name: "expectation-source",
			Expected: Expectations{
				EventCount: &expected,
			},
		},
		&Snapshot{
			Backend:   "sqlite",
			SessionID: "session-1",
		},
	)
	require.Len(t, diffs, 1)
	require.Equal(t, DifferenceSourceExpectation, diffs[0].Source)
	require.Empty(t, diffs[0].BaselineBackend)
	require.Equal(t, "expectations.event_count", diffs[0].FieldPath)
}

func TestRunnerRejectsInvalidConfigurations(t *testing.T) {
	enabled := func(
		context.Context,
		ReplayCase,
	) (*Backend, error) {
		return nil, nil
	}
	tests := []struct {
		name   string
		runner Runner
	}{
		{
			name: "too few backends",
			runner: Runner{
				BaselineBackend: "one",
				Backends: []BackendFactory{{
					Name: "one",
					Open: enabled,
				}},
			},
		},
		{
			name: "missing baseline name",
			runner: Runner{
				Backends: []BackendFactory{
					{Name: "one", Open: enabled},
					{Name: "two", Open: enabled},
				},
			},
		},
		{
			name: "empty backend name",
			runner: Runner{
				BaselineBackend: "one",
				Backends: []BackendFactory{
					{Name: "one", Open: enabled},
					{Open: enabled},
				},
			},
		},
		{
			name: "duplicate backend",
			runner: Runner{
				BaselineBackend: "one",
				Backends: []BackendFactory{
					{Name: "one", Open: enabled},
					{Name: "one", Open: enabled},
				},
			},
		},
		{
			name: "unregistered baseline",
			runner: Runner{
				BaselineBackend: "missing",
				Backends: []BackendFactory{
					{Name: "one", Open: enabled},
					{Name: "two", Open: enabled},
				},
			},
		},
		{
			name: "disabled baseline",
			runner: Runner{
				BaselineBackend: "one",
				Backends: []BackendFactory{
					{
						Name:           "one",
						DisabledReason: "disabled",
					},
					{Name: "two", Open: enabled},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.runner.Run(context.Background(), nil)
			require.Error(t, err)
		})
	}
}

func TestConfigureAndCloseReplayBackend(t *testing.T) {
	service := sessioninmemory.NewSessionService()
	defer service.Close()

	backend := &Backend{Session: service}
	require.NoError(t, configureReplayBackend(
		backend,
		BackendFactory{
			Name: "configured",
			Capabilities: Capabilities{
				Events: true,
			},
		},
	))
	require.Equal(t, "configured", backend.Name)
	require.True(t, backend.Capabilities.Events)

	require.Error(t, configureReplayBackend(
		&Backend{},
		BackendFactory{Name: "missing-session"},
	))
	require.Error(t, configureReplayBackend(
		&Backend{
			Session:      service,
			Capabilities: Capabilities{Memory: true},
		},
		BackendFactory{Name: "missing-memory"},
	))

	var runErr error
	closeReplayBackend(&Backend{}, &runErr)
	require.NoError(t, runErr)

	closeReplayBackend(&Backend{
		Close: func() error {
			return errors.New("close failed")
		},
	}, &runErr)
	require.EqualError(t, runErr, "close backend: close failed")

	existing := errors.New("run failed")
	closeReplayBackend(&Backend{
		Close: func() error {
			return errors.New("close failed")
		},
	}, &existing)
	require.EqualError(t, existing, "run failed")
}

func inMemoryFactory(name string) BackendFactory {
	capabilities := CoreCapabilities()
	capabilities.TTL = true
	return BackendFactory{
		Name:         name,
		Capabilities: capabilities,
		Open: func(
			ctx context.Context,
			replayCase ReplayCase,
		) (*Backend, error) {
			eventLimit := replayCase.EventLimit
			if eventLimit == 0 {
				eventLimit = 1000
			}
			sessionService := sessioninmemory.NewSessionService(
				sessioninmemory.WithSessionEventLimit(eventLimit),
				sessioninmemory.WithSummarizer(
					NewTranscriptSummarizer(),
				),
				sessioninmemory.WithSummaryFilterAllowlist(
					SummaryFilterKeys(replayCase)...,
				),
				sessioninmemory.WithCascadeFullSessionSummary(false),
			)
			memoryService := memoryinmemory.NewMemoryService(
				memoryinmemory.WithMinSearchScore(0),
				memoryinmemory.WithMaxResults(100),
			)
			return &Backend{
				Name:         name,
				Session:      sessionService,
				Memory:       memoryService,
				Capabilities: capabilities,
				Close: func() error {
					if err := memoryService.Close(); err != nil {
						_ = sessionService.Close()
						return err
					}
					return sessionService.Close()
				},
			}, nil
		},
	}
}

func hasDisallowedDifference(diffs []Difference) bool {
	for _, diff := range diffs {
		if !diff.AllowedDiff {
			return true
		}
	}
	return false
}
