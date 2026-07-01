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
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestFakeSummarizerDeterministic(t *testing.T) {
	summarizer := NewFakeSummarizer()
	sess := session.NewSession("app", "user", "sess", session.WithSessionEvents(eventForSummary("one", "two")))

	first, err := summarizer.Summarize(context.Background(), sess)
	require.NoError(t, err)
	second, err := summarizer.Summarize(context.Background(), sess)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Contains(t, first, "events=2")
	require.Contains(t, first, "last='two'")
}

func TestFakeSummarizerDifferentInput(t *testing.T) {
	summarizer := NewFakeSummarizer()
	one := session.NewSession("app", "user", "sess", session.WithSessionEvents(eventForSummary("one")))
	two := session.NewSession("app", "user", "sess", session.WithSessionEvents(eventForSummary("one", "two")))

	oneText, err := summarizer.Summarize(context.Background(), one)
	require.NoError(t, err)
	twoText, err := summarizer.Summarize(context.Background(), two)
	require.NoError(t, err)

	require.NotEqual(t, oneText, twoText)
}

func TestFakeSummarizerOptions(t *testing.T) {
	summarizerErr := errors.New("summarize failed")
	summarizer := NewFakeSummarizer(
		WithShouldSummarize(false),
		WithSummaryText("fixed"),
		WithSummarizeError(summarizerErr),
	)

	require.False(t, summarizer.ShouldSummarize(session.NewSession("app", "user", "sess")))
	text, err := summarizer.Summarize(context.Background(), session.NewSession("app", "user", "sess"))
	require.ErrorIs(t, err, summarizerErr)
	require.Empty(t, text)
	require.Equal(t, "replaytest_fake", summarizer.Metadata()["name"])
}

func TestSummaryReplayCasesRunWithFakeSummarizer(t *testing.T) {
	h := NewHarness(DefaultHarnessOpts())
	sessionSvc, memorySvc := newSummaryReplayServices()
	defer sessionSvc.Close()
	defer memorySvc.Close()

	h.AddBackend(summaryNamedBackend(sessionSvc, memorySvc))
	report, err := h.Run([]ReplayCase{CaseSummaryGeneration, CaseSummaryWithTruncation})
	require.NoError(t, err)
	require.Equal(t, 2, report.PassedCases)

	for _, tc := range []ReplayCase{CaseSummaryGeneration, CaseSummaryWithTruncation} {
		sessionSvc, memorySvc := newSummaryReplayServices()
		defer sessionSvc.Close()
		defer memorySvc.Close()
		snapshot, err := executeCase(context.Background(), tc, summaryNamedBackend(sessionSvc, memorySvc))
		require.NoError(t, err)
		require.NotEmpty(t, snapshot.Session.Events, tc.Name)
		require.NotEmpty(t, snapshot.Session.Summaries, tc.Name)
		require.Contains(t, snapshot.Session.Summaries, session.SummaryFilterKeyAllContents, tc.Name)
		require.NotEmpty(t, snapshot.Session.Summaries[session.SummaryFilterKeyAllContents].Summary, tc.Name)
		text, ok := sessionSvc.GetSessionSummaryText(context.Background(), snapshot.Session)
		require.True(t, ok, tc.Name)
		require.NotEmpty(t, text, tc.Name)
	}
}

func TestSummaryFaultDetection(t *testing.T) {
	cases := []struct {
		name string
		a    map[string]*session.Summary
		b    map[string]*session.Summary
	}{
		{
			name: "lost",
			a:    map[string]*session.Summary{"": {Summary: "summary"}},
			b:    map[string]*session.Summary{},
		},
		{
			name: "overwritten",
			a:    map[string]*session.Summary{"": {Summary: "summary"}},
			b:    map[string]*session.Summary{"": {Summary: "changed"}},
		},
		{
			name: "wrong_filter_key",
			a:    map[string]*session.Summary{"": {Summary: "summary"}},
			b:    map[string]*session.Summary{"branch": {Summary: "summary"}},
		},
		{
			name: "wrong_session",
			a:    map[string]*session.Summary{"": {Summary: "summary"}},
			b:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := summarySnapshot("a", tc.a)
			b := summarySnapshot("b", tc.b)
			result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
			require.Equal(t, StatusFailed, result.Status)
			require.NotEmpty(t, result.Diffs)
			require.Equal(t, "summaries", result.Diffs[0].Path)
		})
	}
}

func TestSummaryInjectFault(t *testing.T) {
	faults := []map[string]*session.Summary{
		{},
		{"": {Summary: "changed"}},
		{"branch": {Summary: "summary"}},
		nil,
	}
	for _, fault := range faults {
		result := NewComparator().Compare(
			summarySnapshot("a", map[string]*session.Summary{"": {Summary: "summary"}}),
			summarySnapshot("b", fault),
			nil,
			InMemoryProfile(),
			InMemoryProfile(),
		)
		require.Equal(t, StatusFailed, result.Status)
	}
}

func TestSummaryAsyncPipeline(t *testing.T) {
	sessionSvc := sessioninmemory.NewSessionService(
		sessioninmemory.WithSummarizer(NewFakeSummarizer()),
		sessioninmemory.WithAsyncSummaryNum(1),
		sessioninmemory.WithSummaryJobTimeout(time.Second),
	)
	defer sessionSvc.Close()

	_, err := executeCase(context.Background(), CaseSummaryAsyncPipeline, NamedBackend{
		Name:           "inmemory",
		Profile:        InMemoryProfile(),
		SessionService: sessionSvc,
	})
	require.NoError(t, err)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := sessionSvc.GetSession(context.Background(), defaultSessionKey)
		require.NoError(t, err)
		if text, ok := sessionSvc.GetSessionSummaryText(context.Background(), got); ok {
			require.NotEmpty(t, text)
			require.Contains(t, text, "events=2")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("summary was not persisted before timeout")
}

func TestSummaryAsyncCaseFailsWhenSummaryMissing(t *testing.T) {
	sessionSvc := sessioninmemory.NewSessionService(
		sessioninmemory.WithAsyncSummaryNum(1),
		sessioninmemory.WithSummaryJobTimeout(time.Second),
	)
	defer sessionSvc.Close()

	tc := CaseSummaryAsyncPipeline
	tc.Steps = append([]ReplayStep(nil), tc.Steps...)
	tc.Steps[len(tc.Steps)-1] = WaitSummaryStep{
		Key:          "c11.wait",
		SessionKey:   defaultSessionKey,
		Timeout:      20 * time.Millisecond,
		PollInterval: time.Millisecond,
	}
	_, err := executeCase(context.Background(), tc, NamedBackend{
		Name:           "inmemory",
		Profile:        InMemoryProfile(),
		SessionService: sessionSvc,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "summary not available")
}

func TestComparatorDetectsAsyncSummaryMissing(t *testing.T) {
	result := NewComparator().Compare(
		summarySnapshot("a", map[string]*session.Summary{"": {Summary: "summary"}}),
		summarySnapshot("b", map[string]*session.Summary{}),
		nil,
		InMemoryProfile(),
		InMemoryProfile(),
	)
	require.Equal(t, StatusFailed, result.Status)
	require.Equal(t, "summaries", result.Diffs[0].Path)
}

func eventForSummary(contents ...string) []event.Event {
	events := make([]event.Event, 0, len(contents))
	for i, content := range contents {
		events = append(events, *testEvent("summary.event."+string(rune('a'+i)), "", content))
	}
	return events
}

func summarySnapshot(backend string, summaries map[string]*session.Summary) *SessionSnapshot {
	sess := session.NewSession("app", "user", "sess")
	sess.Summaries = summaries
	return &SessionSnapshot{BackendName: backend, Session: sess}
}

func newSummaryReplayServices() (*sessioninmemory.SessionService, *memoryinmemory.MemoryService) {
	return sessioninmemory.NewSessionService(
		sessioninmemory.WithSummarizer(NewFakeSummarizer()),
	), memoryinmemory.NewMemoryService()
}

func summaryNamedBackend(
	sessionSvc *sessioninmemory.SessionService,
	memorySvc *memoryinmemory.MemoryService,
) NamedBackend {
	return NamedBackend{
		Name:           "inmemory",
		Profile:        InMemoryProfile(),
		SessionService: sessionSvc,
		MemoryService:  memorySvc,
	}
}
