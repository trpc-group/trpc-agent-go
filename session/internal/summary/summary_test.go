//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type mockSummarizerWithTs struct {
	last time.Time
}

func (m *mockSummarizerWithTs) ShouldSummarize(_ *session.Session) bool { return true }

func (m *mockSummarizerWithTs) Summarize(
	_ context.Context, sess *session.Session,
) (string, error) {
	if m.last.IsZero() && len(sess.Events) > 0 {
		m.last = sess.Events[len(sess.Events)-1].Timestamp
	}
	if sess.State == nil {
		sess.State = make(session.StateMap)
	}
	sess.State[lastIncludedTsKey] = []byte(m.last.UTC().Format(time.RFC3339Nano))
	return "ok", nil
}

func (m *mockSummarizerWithTs) FilterEventsForSummary(
	events []event.Event,
) []event.Event {
	return events
}

func (m *mockSummarizerWithTs) SetPrompt(prompt string) {}

func (m *mockSummarizerWithTs) SetModel(mdl model.Model) {}

func (m *mockSummarizerWithTs) Metadata() map[string]any { return nil }

func TestSummarizeSession_UsesLastIncludedTimestamp(t *testing.T) {
	t1 := time.Date(2023, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2023, 1, 1, 11, 0, 0, 0, time.UTC)
	sess := &session.Session{
		ID: "s1",
		Events: []event.Event{
			{Author: "user", Timestamp: t1, Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "e1"}}}}},
			{Author: "user", Timestamp: t2, Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "e2"}}}}},
		},
	}

	ms := &mockSummarizerWithTs{}
	updated, err := SummarizeSession(context.Background(), ms, sess, "", false)
	require.NoError(t, err)
	require.True(t, updated)

	sess.SummariesMu.RLock()
	sum := sess.Summaries[""]
	sess.SummariesMu.RUnlock()
	require.NotNil(t, sum)
	assert.True(t, sum.UpdatedAt.Equal(t2.UTC()))
}

func TestSelectUpdatedAt_Fallbacks(t *testing.T) {
	prev := time.Date(2023, 1, 2, 9, 0, 0, 0, time.UTC)
	latest := time.Date(2023, 1, 2, 10, 0, 0, 0, time.UTC)

	t.Run("no delta keeps prev", func(t *testing.T) {
		got := selectUpdatedAt(nil, prev, latest, false)
		assert.True(t, got.Equal(prev.UTC()))
	})

	t.Run("invalid ts falls back to latest", func(t *testing.T) {
		tmp := &session.Session{State: session.StateMap{
			lastIncludedTsKey: []byte("bad-ts"),
		}}
		got := selectUpdatedAt(tmp, prev, latest, true)
		assert.True(t, got.Equal(latest.UTC()))
	})

	t.Run("nil session with delta falls back to latest", func(t *testing.T) {
		got := selectUpdatedAt(nil, prev, latest, true)
		assert.True(t, got.Equal(latest.UTC()))
	})

	t.Run("zero latestTs with delta keeps prev", func(t *testing.T) {
		got := selectUpdatedAt(nil, prev, time.Time{}, true)
		assert.True(t, got.Equal(prev.UTC()))
	})
}

type fakeSummarizer struct {
	allow bool
	out   string
	err   error
}

func (f *fakeSummarizer) ShouldSummarize(sess *session.Session) bool { return f.allow }
func (f *fakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}
func (f *fakeSummarizer) SetPrompt(prompt string)  {}
func (f *fakeSummarizer) SetModel(m model.Model)   {}
func (f *fakeSummarizer) Metadata() map[string]any { return map[string]any{} }

type fakeSummarizerWithTs struct {
	out string
	ts  time.Time
}

func (f *fakeSummarizerWithTs) ShouldSummarize(sess *session.Session) bool { return true }
func (f *fakeSummarizerWithTs) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if sess.State == nil {
		sess.State = make(session.StateMap)
	}
	if !f.ts.IsZero() {
		sess.State[lastIncludedTsKey] = []byte(f.ts.UTC().Format(time.RFC3339Nano))
	}
	return f.out, nil
}
func (f *fakeSummarizerWithTs) SetPrompt(prompt string)  {}
func (f *fakeSummarizerWithTs) SetModel(m model.Model)   {}
func (f *fakeSummarizerWithTs) Metadata() map[string]any { return map[string]any{} }

func makeEvent(content string, ts time.Time, filterKey string) event.Event {
	return event.Event{
		Branch:    filterKey,
		FilterKey: filterKey,
		Timestamp: ts,
		Response:  &model.Response{Choices: []model.Choice{{Message: model.Message{Content: content}}}},
	}
}

func TestSummarizeSession_FilteredKey_RespectsDeltaAndShould(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	base.Events = []event.Event{
		makeEvent("old", now.Add(-2*time.Minute), "b1"),
		makeEvent("new", now.Add(-1*time.Second), "b1"),
	}

	// allow=false and force=false should skip.
	s := &fakeSummarizer{allow: false, out: "sum"}
	updated, err := SummarizeSession(context.Background(), s, base, "b1", false)
	require.NoError(t, err)
	require.False(t, updated)

	// allow=true should write.
	s.allow = true
	updated, err = SummarizeSession(context.Background(), s, base, "b1", false)
	require.NoError(t, err)
	require.True(t, updated)
	require.NotNil(t, base.Summaries)
	require.Equal(t, "sum", base.Summaries["b1"].Summary)

	// force=true should write even when ShouldSummarize=false.
	s.allow = false
	updated, err = SummarizeSession(context.Background(), s, base, "b1", true)
	require.NoError(t, err)
	require.True(t, updated)
	require.Equal(t, "sum", base.Summaries["b1"].Summary)
}

func TestSummarizeSession_FullSession_SingleWrite(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	base.Events = []event.Event{
		makeEvent("e1", now.Add(-1*time.Minute), "b1"),
		makeEvent("e2", now.Add(-30*time.Second), "b2"),
	}
	s := &fakeSummarizer{allow: true, out: "sum"}
	updated, err := SummarizeSession(context.Background(), s, base, "", false)
	require.NoError(t, err)
	require.True(t, updated)
	require.NotNil(t, base.Summaries)
	require.Equal(t, "sum", base.Summaries[""].Summary)
}

func TestSummarizeSession_NilSummarizer(t *testing.T) {
	base := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	updated, err := SummarizeSession(context.Background(), nil, base, "", false)
	require.NoError(t, err)
	require.False(t, updated)
}

func TestSummarizeSession_NilSession(t *testing.T) {
	s := &fakeSummarizer{allow: true, out: "sum"}
	updated, err := SummarizeSession(context.Background(), s, nil, "", false)
	require.NoError(t, err)
	require.False(t, updated)
}

func TestSummarizeSession_EmptyDelta_NoForce(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	base.Events = []event.Event{
		makeEvent("e1", now.Add(-1*time.Minute), "b1"),
	}

	// First summarization.
	s := &fakeSummarizer{allow: true, out: "sum1"}
	updated, err := SummarizeSession(context.Background(), s, base, "b1", false)
	require.NoError(t, err)
	require.True(t, updated)

	// Second summarization without new events - should skip.
	s.out = "sum2"
	updated, err = SummarizeSession(context.Background(), s, base, "b1", false)
	require.NoError(t, err)
	require.False(t, updated)
	require.Equal(t, "sum1", base.Summaries["b1"].Summary)
}

func TestSummarizeSession_EmptyDelta_WithForce(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	base.Events = []event.Event{
		makeEvent("e1", now.Add(-1*time.Minute), "b1"),
	}

	// First summarization.
	s := &fakeSummarizer{allow: true, out: "sum1"}
	updated, err := SummarizeSession(context.Background(), s, base, "b1", false)
	require.NoError(t, err)
	require.True(t, updated)

	// Second summarization without new events but with force - should update.
	s.out = "sum2"
	updated, err = SummarizeSession(context.Background(), s, base, "b1", true)
	require.NoError(t, err)
	require.True(t, updated)
	require.Equal(t, "sum2", base.Summaries["b1"].Summary)
}

func TestSummarizeSession_EmptySummary_NotUpdated(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	base.Events = []event.Event{
		makeEvent("e1", now.Add(-1*time.Minute), "b1"),
	}

	// Summarizer returns empty string - should not update.
	s := &fakeSummarizer{allow: true, out: ""}
	updated, err := SummarizeSession(context.Background(), s, base, "b1", false)
	require.NoError(t, err)
	require.False(t, updated)
}

func TestSummarizeSession_SummarizerError_ReturnsError(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "test-session-123", AppName: "a", UserID: "u"}
	base.Events = []event.Event{
		makeEvent("e1", now.Add(-1*time.Minute), "b1"),
	}

	// Summarizer returns an error - should propagate with session ID.
	summarizerErr := errors.New("model API error: rate limit exceeded")
	s := &fakeSummarizer{allow: true, out: "", err: summarizerErr}
	updated, err := SummarizeSession(context.Background(), s, base, "b1", false)
	require.Error(t, err)
	require.False(t, updated)
	require.Contains(t, err.Error(), "summarize session test-session-123 failed")
	require.Contains(t, err.Error(), "model API error: rate limit exceeded")
	require.ErrorIs(t, err, summarizerErr)
}

func TestSummarizeSession_SummarizerError_FullSession(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "full-session-456", AppName: "app", UserID: "user"}
	base.Events = []event.Event{
		makeEvent("e1", now.Add(-2*time.Minute), ""),
		makeEvent("e2", now.Add(-1*time.Minute), ""),
	}

	// Summarizer returns an error for full session summary.
	summarizerErr := errors.New("empty summary generated")
	s := &fakeSummarizer{allow: true, out: "", err: summarizerErr}
	updated, err := SummarizeSession(context.Background(), s, base, "", false)
	require.Error(t, err)
	require.False(t, updated)
	require.Contains(t, err.Error(), "summarize session full-session-456 failed")
	require.ErrorIs(t, err, summarizerErr)
}

func TestComputeDeltaSince_WithFilterKey(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "s1"}
	base.Events = []event.Event{
		makeEvent("e1", now.Add(-3*time.Minute), "b1"),
		makeEvent("e2", now.Add(-2*time.Minute), "b2"),
		makeEvent("e3", now.Add(-1*time.Minute), "b1"),
	}

	// Filter by "b1" filterKey.
	delta, latestTs := computeDeltaSince(base, time.Time{}, "b1")
	require.Len(t, delta, 2)
	require.Equal(t, "e1", delta[0].Response.Choices[0].Message.Content)
	require.Equal(t, "e3", delta[1].Response.Choices[0].Message.Content)
	require.Equal(t, base.Events[2].Timestamp, latestTs)
}

func TestComputeDeltaSince_WithTime(t *testing.T) {
	now := time.Now()
	base := &session.Session{ID: "s1"}
	base.Events = []event.Event{
		makeEvent("e1", now.Add(-3*time.Minute), "b1"),
		makeEvent("e2", now.Add(-2*time.Minute), "b1"),
		makeEvent("e3", now.Add(-1*time.Minute), "b1"),
	}

	// Filter by time after e1 (strictly after, so e2 timestamp needs to be > since).
	since := now.Add(-2*time.Minute - 1*time.Second)
	delta, latestTs := computeDeltaSince(base, since, "")
	require.Len(t, delta, 2)
	require.Equal(t, "e2", delta[0].Response.Choices[0].Message.Content)
	require.Equal(t, "e3", delta[1].Response.Choices[0].Message.Content)
	require.Equal(t, base.Events[2].Timestamp, latestTs)
}

func TestSummarizeSession_UsesLastIncludedTimestampWhenProvided(t *testing.T) {
	now := time.Now()
	t1 := now.Add(-3 * time.Minute)
	t2 := now.Add(-2 * time.Minute)
	t3 := now.Add(-1 * time.Minute)

	base := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	base.Events = []event.Event{
		makeEvent("e1", t1, ""),
		makeEvent("e2", t2, ""),
		makeEvent("e3", t3, ""),
	}

	s := &fakeSummarizerWithTs{
		out: "sum",
		ts:  t2, // simulate summarizer skipping the latest event and using t2 as last included
	}

	updated, err := SummarizeSession(context.Background(), s, base, "", false)
	require.NoError(t, err)
	require.True(t, updated)
	require.NotNil(t, base.Summaries)
	require.Equal(t, "sum", base.Summaries[""].Summary)
	require.Equal(t, t2.UTC(), base.Summaries[""].UpdatedAt)
}

func TestMeetsTimeCriteria(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name     string
		sum      *session.Summary
		minTime  time.Time
		expected bool
	}{
		{
			name:     "nil summary should return false",
			sum:      nil,
			minTime:  now,
			expected: false,
		},
		{
			name:     "zero minTime should return true for any non-nil summary",
			sum:      &session.Summary{Summary: "test", UpdatedAt: past},
			minTime:  time.Time{},
			expected: true,
		},
		{
			name:     "summary UpdatedAt equals minTime should return true",
			sum:      &session.Summary{Summary: "test", UpdatedAt: now},
			minTime:  now,
			expected: true,
		},
		{
			name:     "summary UpdatedAt after minTime should return true",
			sum:      &session.Summary{Summary: "test", UpdatedAt: future},
			minTime:  now,
			expected: true,
		},
		{
			name:     "summary UpdatedAt before minTime should return false",
			sum:      &session.Summary{Summary: "test", UpdatedAt: past},
			minTime:  now,
			expected: false,
		},
		{
			name:     "nil summary with zero minTime should return false",
			sum:      nil,
			minTime:  time.Time{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := meetsTimeCriteria(tt.sum, tt.minTime)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestPickSummaryText(t *testing.T) {
	tests := []struct {
		name      string
		summaries map[string]*session.Summary
		filterKey string
		wantText  string
		wantOk    bool
	}{
		{
			name:      "nil summaries",
			summaries: nil,
			filterKey: "",
			wantText:  "",
			wantOk:    false,
		},
		{
			name:      "empty summaries",
			summaries: map[string]*session.Summary{},
			filterKey: "",
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "prefer all-contents summary when available",
			summaries: map[string]*session.Summary{
				"":        {Summary: "full summary"},
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "",
			wantText:  "full summary",
			wantOk:    true,
		},
		{
			name: "all-contents summary exists but empty, should return false",
			summaries: map[string]*session.Summary{
				"":        {Summary: ""},
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "",
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "all-contents summary is nil, should return false",
			summaries: map[string]*session.Summary{
				"":        nil,
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "",
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "only all-contents summary exists and is non-empty",
			summaries: map[string]*session.Summary{
				"": {Summary: "full summary"},
			},
			filterKey: "",
			wantText:  "full summary",
			wantOk:    true,
		},
		{
			name: "only all-contents summary exists but is empty",
			summaries: map[string]*session.Summary{
				"": {Summary: ""},
			},
			filterKey: "",
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "only all-contents summary exists but is nil",
			summaries: map[string]*session.Summary{
				"": nil,
			},
			filterKey: "",
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "no all-contents summary, should return false",
			summaries: map[string]*session.Summary{
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "",
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "all summaries are empty",
			summaries: map[string]*session.Summary{
				"":        {Summary: ""},
				"filter1": {Summary: ""},
				"filter2": {Summary: ""},
			},
			filterKey: "",
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "all summaries are nil",
			summaries: map[string]*session.Summary{
				"":        nil,
				"filter1": nil,
				"filter2": nil,
			},
			filterKey: "",
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "mixed nil and empty summaries, no fallback",
			summaries: map[string]*session.Summary{
				"":        nil,
				"filter1": {Summary: ""},
				"filter2": {Summary: "valid summary"},
			},
			filterKey: "",
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "specific filter key exists",
			summaries: map[string]*session.Summary{
				"":        {Summary: "full summary"},
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "filter1",
			wantText:  "filtered summary 1",
			wantOk:    true,
		},
		{
			name: "specific filter key not found, fallback to full",
			summaries: map[string]*session.Summary{
				"":        {Summary: "full summary"},
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "nonexistent",
			wantText:  "full summary",
			wantOk:    true,
		},
		{
			name: "specific filter key not found, no full fallback, should return false",
			summaries: map[string]*session.Summary{
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "nonexistent",
			wantText:  "",
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotOk := PickSummaryText(tt.summaries, tt.filterKey, time.Time{})
			require.Equal(t, tt.wantText, gotText)
			require.Equal(t, tt.wantOk, gotOk)
		})
	}
}

func TestPickSummaryText_WithMinTime(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name      string
		summaries map[string]*session.Summary
		filterKey string
		minTime   time.Time
		wantText  string
		wantOk    bool
	}{
		{
			name: "filter by minTime: summary before minTime should be excluded",
			summaries: map[string]*session.Summary{
				"": {Summary: "old summary", UpdatedAt: past},
			},
			filterKey: "",
			minTime:   now,
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "filter by minTime: summary equal to minTime should be included",
			summaries: map[string]*session.Summary{
				"": {Summary: "current summary", UpdatedAt: now},
			},
			filterKey: "",
			minTime:   now,
			wantText:  "current summary",
			wantOk:    true,
		},
		{
			name: "filter by minTime: summary after minTime should be included",
			summaries: map[string]*session.Summary{
				"": {Summary: "new summary", UpdatedAt: future},
			},
			filterKey: "",
			minTime:   now,
			wantText:  "new summary",
			wantOk:    true,
		},
		{
			name: "filter by minTime: fallback when filterKey is too old",
			summaries: map[string]*session.Summary{
				"":        {Summary: "full summary", UpdatedAt: future},
				"filter1": {Summary: "old filtered summary", UpdatedAt: past},
			},
			filterKey: "filter1",
			minTime:   now,
			wantText:  "full summary",
			wantOk:    true,
		},
		{
			name: "filter by minTime: both filterKey and fallback are too old",
			summaries: map[string]*session.Summary{
				"":        {Summary: "old full summary", UpdatedAt: past},
				"filter1": {Summary: "old filtered summary", UpdatedAt: past},
			},
			filterKey: "filter1",
			minTime:   now,
			wantText:  "",
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotOk := PickSummaryText(tt.summaries, tt.filterKey, tt.minTime)
			require.Equal(t, tt.wantText, gotText)
			require.Equal(t, tt.wantOk, gotOk)
		})
	}
}

func TestGetSummaryTextFromSession(t *testing.T) {
	tests := []struct {
		name     string
		session  *session.Session
		opts     []session.SummaryOption
		wantText string
		wantOk   bool
	}{
		{
			name:     "nil session",
			session:  nil,
			opts:     nil,
			wantText: "",
			wantOk:   false,
		},
		{
			name: "session with no summaries",
			session: &session.Session{
				ID:        "s1",
				AppName:   "app",
				UserID:    "user",
				Summaries: nil,
			},
			opts:     nil,
			wantText: "",
			wantOk:   false,
		},
		{
			name: "session with empty summaries",
			session: &session.Session{
				ID:        "s1",
				AppName:   "app",
				UserID:    "user",
				Summaries: map[string]*session.Summary{},
			},
			opts:     nil,
			wantText: "",
			wantOk:   false,
		},
		{
			name: "default filter key (empty string)",
			session: &session.Session{
				ID:      "s1",
				AppName: "app",
				UserID:  "user",
				Summaries: map[string]*session.Summary{
					"":        {Summary: "full summary"},
					"filter1": {Summary: "filtered summary"},
				},
			},
			opts:     nil,
			wantText: "full summary",
			wantOk:   true,
		},
		{
			name: "specific filter key",
			session: &session.Session{
				ID:      "s1",
				AppName: "app",
				UserID:  "user",
				Summaries: map[string]*session.Summary{
					"":        {Summary: "full summary"},
					"filter1": {Summary: "filtered summary"},
				},
			},
			opts:     []session.SummaryOption{session.WithSummaryFilterKey("filter1")},
			wantText: "filtered summary",
			wantOk:   true,
		},
		{
			name: "non-existent filter key with fallback",
			session: &session.Session{
				ID:      "s1",
				AppName: "app",
				UserID:  "user",
				Summaries: map[string]*session.Summary{
					"":        {Summary: "full summary"},
					"filter1": {Summary: "filtered summary"},
				},
			},
			opts:     []session.SummaryOption{session.WithSummaryFilterKey("nonexistent")},
			wantText: "full summary",
			wantOk:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotOk := GetSummaryTextFromSession(tt.session, tt.opts...)
			require.Equal(t, tt.wantText, gotText)
			require.Equal(t, tt.wantOk, gotOk)
		})
	}
}

func TestGetFilterKeyFromOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    []session.SummaryOption
		wantKey string
	}{
		{
			name:    "no options",
			opts:    nil,
			wantKey: "",
		},
		{
			name:    "empty options",
			opts:    []session.SummaryOption{},
			wantKey: "",
		},
		{
			name:    "with filter key",
			opts:    []session.SummaryOption{session.WithSummaryFilterKey("test-key")},
			wantKey: "test-key",
		},
		{
			name:    "with empty filter key",
			opts:    []session.SummaryOption{session.WithSummaryFilterKey("")},
			wantKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKey := GetFilterKeyFromOptions(tt.opts...)
			require.Equal(t, tt.wantKey, gotKey)
		})
	}
}

func TestCreateSessionSummaryWithCascade(t *testing.T) {
	now := time.Now()
	// Events with multiple filterKeys to ensure parallel calls are made.
	multiFilterKeyEvents := []event.Event{
		makeEvent("e1", now.Add(-2*time.Minute), "user-messages"),
		makeEvent("e2", now.Add(-1*time.Minute), "tool-calls"),
	}

	tests := []struct {
		name              string
		filterKey         string
		force             bool
		events            []event.Event
		expectCalls       []string
		expectError       bool
		createSummaryFunc func(context.Context, *session.Session, string, bool) error
	}{
		{
			name:        "filterKey is empty, only call once",
			filterKey:   "",
			force:       false,
			events:      multiFilterKeyEvents,
			expectCalls: []string{""},
			expectError: false,
			createSummaryFunc: func(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
				return nil
			},
		},
		{
			name:        "filterKey is user-messages, call twice (multiple filterKeys in session)",
			filterKey:   "user-messages",
			force:       false,
			events:      multiFilterKeyEvents,
			expectCalls: []string{"user-messages", ""},
			expectError: false,
			createSummaryFunc: func(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
				return nil
			},
		},
		{
			name:        "first call fails, return error",
			filterKey:   "user-messages",
			force:       false,
			events:      multiFilterKeyEvents,
			expectCalls: []string{"user-messages", ""},
			expectError: true,
			createSummaryFunc: func(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
				if filterKey == "user-messages" {
					return errors.New("first call failed")
				}
				return nil
			},
		},
		{
			name:        "second call fails, return error",
			filterKey:   "user-messages",
			force:       false,
			events:      multiFilterKeyEvents,
			expectCalls: []string{"user-messages", ""},
			expectError: true,
			createSummaryFunc: func(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
				if filterKey == "" {
					return errors.New("second call failed")
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var callsMu sync.Mutex
			sess := &session.Session{
				ID:      "test-session",
				AppName: "test-app",
				UserID:  "test-user",
				Events:  tt.events,
			}

			mockFunc := func(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
				callsMu.Lock()
				calls = append(calls, filterKey)
				callsMu.Unlock()
				return tt.createSummaryFunc(ctx, sess, filterKey, force)
			}

			err := CreateSessionSummaryWithCascade(context.Background(), sess, tt.filterKey, tt.force, mockFunc)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, len(tt.expectCalls), len(calls))
			for _, expectedCall := range tt.expectCalls {
				require.Contains(t, calls, expectedCall)
			}
		})
	}
}

func TestCreateSessionSummaryWithCascade_MethodValue(t *testing.T) {
	// Test using method value (like s.CreateSessionSummary).
	now := time.Now()
	type mockService struct {
		mu        sync.Mutex
		summaries map[string]string
	}

	mockSvc := &mockService{}

	createFunc := func(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
		mockSvc.mu.Lock()
		defer mockSvc.mu.Unlock()
		if mockSvc.summaries == nil {
			mockSvc.summaries = make(map[string]string)
		}
		mockSvc.summaries[filterKey] = "summary-" + filterKey
		return nil
	}

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		// Multiple filterKeys to ensure parallel calls.
		Events: []event.Event{
			makeEvent("e1", now.Add(-2*time.Minute), "user-messages"),
			makeEvent("e2", now.Add(-1*time.Minute), "tool-calls"),
		},
	}

	err := CreateSessionSummaryWithCascade(context.Background(), sess, "user-messages", false, createFunc)
	require.NoError(t, err)

	// Should have created both summaries.
	mockSvc.mu.Lock()
	defer mockSvc.mu.Unlock()
	require.Equal(t, "summary-user-messages", mockSvc.summaries["user-messages"])
	require.Equal(t, "summary-", mockSvc.summaries[""])
}

func TestIsSingleFilterKey(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		session   *session.Session
		targetKey string
		expected  bool
	}{
		{
			name:      "nil session",
			session:   nil,
			targetKey: "app/test",
			expected:  false,
		},
		{
			name: "empty targetKey",
			session: &session.Session{
				ID: "s1",
				Events: []event.Event{
					makeEvent("e1", now, "app/test"),
				},
			},
			targetKey: "",
			expected:  false,
		},
		{
			name: "all events match targetKey",
			session: &session.Session{
				ID: "s1",
				Events: []event.Event{
					makeEvent("e1", now.Add(-2*time.Minute), "app/test"),
					makeEvent("e2", now.Add(-1*time.Minute), "app/test"),
					makeEvent("e3", now, "app/test"),
				},
			},
			targetKey: "app/test",
			expected:  true,
		},
		{
			name: "some events do not match targetKey",
			session: &session.Session{
				ID: "s1",
				Events: []event.Event{
					makeEvent("e1", now.Add(-2*time.Minute), "app/test"),
					makeEvent("e2", now.Add(-1*time.Minute), "app/other"),
					makeEvent("e3", now, "app/test"),
				},
			},
			targetKey: "app/test",
			expected:  false,
		},
		{
			name: "empty session events",
			session: &session.Session{
				ID:     "s1",
				Events: []event.Event{},
			},
			targetKey: "app/test",
			expected:  true,
		},
		{
			name: "events with empty filterKey match any targetKey",
			session: &session.Session{
				ID: "s1",
				Events: []event.Event{
					makeEvent("e1", now.Add(-1*time.Minute), ""),
					makeEvent("e2", now, ""),
				},
			},
			targetKey: "app/test",
			expected:  true,
		},
		{
			name: "mixed empty and matching filterKeys",
			session: &session.Session{
				ID: "s1",
				Events: []event.Event{
					makeEvent("e1", now.Add(-2*time.Minute), "app/test"),
					makeEvent("e2", now.Add(-1*time.Minute), ""),
					makeEvent("e3", now, "app/test"),
				},
			},
			targetKey: "app/test",
			expected:  true,
		},
		{
			name: "prefix matching - child matches parent",
			session: &session.Session{
				ID: "s1",
				Events: []event.Event{
					makeEvent("e1", now.Add(-1*time.Minute), "app/test/sub"),
					makeEvent("e2", now, "app/test/other"),
				},
			},
			targetKey: "app/test",
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSingleFilterKey(tt.session, tt.targetKey)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestCopySummaryToKey(t *testing.T) {
	now := time.Now()

	t.Run("nil session", func(t *testing.T) {
		// Should not panic.
		copySummaryToKey(nil, "src", "dst")
	})

	t.Run("nil summaries", func(t *testing.T) {
		sess := &session.Session{ID: "s1"}
		copySummaryToKey(sess, "src", "dst")
		require.Nil(t, sess.Summaries)
	})

	t.Run("source key not found", func(t *testing.T) {
		sess := &session.Session{
			ID: "s1",
			Summaries: map[string]*session.Summary{
				"other": {Summary: "other summary", UpdatedAt: now},
			},
		}
		copySummaryToKey(sess, "src", "dst")
		_, ok := sess.Summaries["dst"]
		require.False(t, ok)
	})

	t.Run("source key is nil", func(t *testing.T) {
		sess := &session.Session{
			ID: "s1",
			Summaries: map[string]*session.Summary{
				"src": nil,
			},
		}
		copySummaryToKey(sess, "src", "dst")
		_, ok := sess.Summaries["dst"]
		require.False(t, ok)
	})

	t.Run("successful copy", func(t *testing.T) {
		sess := &session.Session{
			ID: "s1",
			Summaries: map[string]*session.Summary{
				"src": {Summary: "source summary", UpdatedAt: now},
			},
		}
		copySummaryToKey(sess, "src", "dst")
		require.NotNil(t, sess.Summaries["dst"])
		require.Equal(t, "source summary", sess.Summaries["dst"].Summary)
		// UpdatedAt is set to zero to mark as needing persistence.
		require.True(t, sess.Summaries["dst"].UpdatedAt.IsZero())
	})

	t.Run("overwrite existing destination", func(t *testing.T) {
		oldTime := now.Add(-time.Hour)
		sess := &session.Session{
			ID: "s1",
			Summaries: map[string]*session.Summary{
				"src": {Summary: "new summary", UpdatedAt: now},
				"dst": {Summary: "old summary", UpdatedAt: oldTime},
			},
		}
		copySummaryToKey(sess, "src", "dst")
		require.Equal(t, "new summary", sess.Summaries["dst"].Summary)
		// UpdatedAt is set to zero to mark as needing persistence.
		require.True(t, sess.Summaries["dst"].UpdatedAt.IsZero())
	})

	t.Run("copies Topics field", func(t *testing.T) {
		sess := &session.Session{
			ID: "s1",
			Summaries: map[string]*session.Summary{
				"src": {
					Summary:   "summary with topics",
					Topics:    []string{"topic1", "topic2", "topic3"},
					UpdatedAt: now,
				},
			},
		}
		copySummaryToKey(sess, "src", "dst")
		require.NotNil(t, sess.Summaries["dst"])
		require.Equal(t, "summary with topics", sess.Summaries["dst"].Summary)
		require.Equal(t, []string{"topic1", "topic2", "topic3"}, sess.Summaries["dst"].Topics)
		// Verify Topics slice is a copy, not shared reference.
		sess.Summaries["src"].Topics[0] = "modified"
		require.Equal(t, "topic1", sess.Summaries["dst"].Topics[0])
	})

	t.Run("handles nil Topics", func(t *testing.T) {
		sess := &session.Session{
			ID: "s1",
			Summaries: map[string]*session.Summary{
				"src": {Summary: "summary without topics", UpdatedAt: now},
			},
		}
		copySummaryToKey(sess, "src", "dst")
		require.NotNil(t, sess.Summaries["dst"])
		require.Equal(t, "summary without topics", sess.Summaries["dst"].Summary)
		require.Nil(t, sess.Summaries["dst"].Topics)
	})

	t.Run("handles empty Topics slice", func(t *testing.T) {
		sess := &session.Session{
			ID: "s1",
			Summaries: map[string]*session.Summary{
				"src": {Summary: "summary with empty topics", Topics: []string{}, UpdatedAt: now},
			},
		}
		copySummaryToKey(sess, "src", "dst")
		require.NotNil(t, sess.Summaries["dst"])
		require.Equal(t, "summary with empty topics", sess.Summaries["dst"].Summary)
		require.Nil(t, sess.Summaries["dst"].Topics) // Empty slice becomes nil.
	})
}

func TestSummarizeSession_NeedsPersistOnly(t *testing.T) {
	now := time.Now()

	t.Run("copied summary with zero UpdatedAt triggers persist only", func(t *testing.T) {
		base := &session.Session{
			ID:      "s1",
			AppName: "a",
			UserID:  "u",
			Events: []event.Event{
				makeEvent("e1", now.Add(-2*time.Minute), ""),
				makeEvent("e2", now.Add(-1*time.Minute), ""),
			},
			Summaries: map[string]*session.Summary{
				// Simulate a copied summary with zero UpdatedAt.
				"": {Summary: "copied summary", UpdatedAt: time.Time{}},
			},
		}

		s := &fakeSummarizer{allow: true, out: "should not be called"}
		updated, err := SummarizeSession(context.Background(), s, base, "", false)
		require.NoError(t, err)
		require.True(t, updated)

		// Verify UpdatedAt was set to latest event timestamp.
		base.SummariesMu.RLock()
		sum := base.Summaries[""]
		base.SummariesMu.RUnlock()
		require.NotNil(t, sum)
		require.Equal(t, "copied summary", sum.Summary) // Summary unchanged.
		require.False(t, sum.UpdatedAt.IsZero())        // UpdatedAt now set.
	})

	t.Run("copied summary with no events uses current time", func(t *testing.T) {
		base := &session.Session{
			ID:      "s1",
			AppName: "a",
			UserID:  "u",
			Events:  []event.Event{}, // No events.
			Summaries: map[string]*session.Summary{
				"": {Summary: "copied summary", UpdatedAt: time.Time{}},
			},
		}

		s := &fakeSummarizer{allow: true, out: "should not be called"}
		before := time.Now()
		updated, err := SummarizeSession(context.Background(), s, base, "", false)
		after := time.Now()
		require.NoError(t, err)
		require.True(t, updated)

		// Verify UpdatedAt was set to approximately current time.
		base.SummariesMu.RLock()
		sum := base.Summaries[""]
		base.SummariesMu.RUnlock()
		require.NotNil(t, sum)
		require.False(t, sum.UpdatedAt.IsZero())
		require.True(t, sum.UpdatedAt.After(before.Add(-time.Second)) ||
			sum.UpdatedAt.Equal(before.Add(-time.Second)))
		require.True(t, sum.UpdatedAt.Before(after.Add(time.Second)) ||
			sum.UpdatedAt.Equal(after.Add(time.Second)))
	})

	t.Run("copied summary with filterKey uses filtered events timestamp", func(t *testing.T) {
		base := &session.Session{
			ID:      "s1",
			AppName: "a",
			UserID:  "u",
			Events: []event.Event{
				makeEvent("e1", now.Add(-2*time.Minute), "b1"),
				makeEvent("e2", now.Add(-1*time.Minute), "b2"), // Different filterKey.
			},
			Summaries: map[string]*session.Summary{
				"b1": {Summary: "copied summary for b1", UpdatedAt: time.Time{}},
			},
		}

		s := &fakeSummarizer{allow: true, out: "should not be called"}
		updated, err := SummarizeSession(context.Background(), s, base, "b1", false)
		require.NoError(t, err)
		require.True(t, updated)

		// Verify UpdatedAt was set to latest event timestamp matching b1.
		base.SummariesMu.RLock()
		sum := base.Summaries["b1"]
		base.SummariesMu.RUnlock()
		require.NotNil(t, sum)
		require.Equal(t, "copied summary for b1", sum.Summary)
		require.True(t, sum.UpdatedAt.Equal(now.Add(-2*time.Minute).UTC()))
	})

	t.Run("normal summary with non-zero UpdatedAt follows normal path", func(t *testing.T) {
		base := &session.Session{
			ID:      "s1",
			AppName: "a",
			UserID:  "u",
			Events: []event.Event{
				makeEvent("e1", now.Add(-2*time.Minute), ""),
			},
			Summaries: map[string]*session.Summary{
				// Normal summary with non-zero UpdatedAt.
				"": {Summary: "existing summary", UpdatedAt: now.Add(-3 * time.Minute)},
			},
		}

		s := &fakeSummarizer{allow: true, out: "new summary"}
		updated, err := SummarizeSession(context.Background(), s, base, "", false)
		require.NoError(t, err)
		require.True(t, updated)

		// Verify summary was updated via LLM.
		base.SummariesMu.RLock()
		sum := base.Summaries[""]
		base.SummariesMu.RUnlock()
		require.NotNil(t, sum)
		require.Equal(t, "new summary", sum.Summary) // LLM generated new summary.
	})

	t.Run("empty summary with zero UpdatedAt does not trigger persist only", func(t *testing.T) {
		base := &session.Session{
			ID:      "s1",
			AppName: "a",
			UserID:  "u",
			Events: []event.Event{
				makeEvent("e1", now.Add(-1*time.Minute), ""),
			},
			Summaries: map[string]*session.Summary{
				// Empty summary with zero UpdatedAt should not trigger persist only.
				"": {Summary: "", UpdatedAt: time.Time{}},
			},
		}

		s := &fakeSummarizer{allow: true, out: "new summary"}
		updated, err := SummarizeSession(context.Background(), s, base, "", false)
		require.NoError(t, err)
		require.True(t, updated)

		// Verify summary was generated via LLM.
		base.SummariesMu.RLock()
		sum := base.Summaries[""]
		base.SummariesMu.RUnlock()
		require.NotNil(t, sum)
		require.Equal(t, "new summary", sum.Summary)
	})
}

func TestCreateSessionSummaryWithCascade_SingleFilterKeyOptimization(t *testing.T) {
	now := time.Now()

	t.Run("single filterKey - LLM call once, persist twice", func(t *testing.T) {
		var callCount int
		var callsMu sync.Mutex

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
			Events: []event.Event{
				makeEvent("e1", now.Add(-2*time.Minute), "app/math"),
				makeEvent("e2", now.Add(-1*time.Minute), "app/math"),
				makeEvent("e3", now, "app/math"),
			},
			Summaries: make(map[string]*session.Summary),
		}

		createFunc := func(ctx context.Context, s *session.Session, filterKey string, force bool) error {
			callsMu.Lock()
			callCount++
			callsMu.Unlock()
			// Simulate SummarizeSession behavior: only generate if not already present
			// with zero UpdatedAt (copied summary).
			s.SummariesMu.Lock()
			if existing := s.Summaries[filterKey]; existing != nil && existing.UpdatedAt.IsZero() {
				// Copied summary: just set proper UpdatedAt, no LLM call.
				existing.UpdatedAt = now
			} else {
				// New summary: generate via LLM.
				s.Summaries[filterKey] = &session.Summary{
					Summary:   "summary for " + filterKey,
					UpdatedAt: now,
				}
			}
			s.SummariesMu.Unlock()
			return nil
		}

		err := CreateSessionSummaryWithCascade(context.Background(), sess, "app/math", false, createFunc)
		require.NoError(t, err)

		// Should call createFunc twice: once for filterKey, once for full-session (persist only).
		require.Equal(t, 2, callCount)

		// Both keys should have summaries.
		sess.SummariesMu.RLock()
		defer sess.SummariesMu.RUnlock()
		require.NotNil(t, sess.Summaries["app/math"])
		require.NotNil(t, sess.Summaries[""])
		require.Equal(t, sess.Summaries["app/math"].Summary, sess.Summaries[""].Summary)
	})

	t.Run("multiple filterKeys - two LLM calls", func(t *testing.T) {
		var calls []string
		var callsMu sync.Mutex

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
			Events: []event.Event{
				makeEvent("e1", now.Add(-2*time.Minute), "app/math"),
				makeEvent("e2", now.Add(-1*time.Minute), "app/science"),
				makeEvent("e3", now, "app/math"),
			},
			Summaries: make(map[string]*session.Summary),
		}

		createFunc := func(ctx context.Context, s *session.Session, filterKey string, force bool) error {
			callsMu.Lock()
			calls = append(calls, filterKey)
			callsMu.Unlock()
			s.SummariesMu.Lock()
			s.Summaries[filterKey] = &session.Summary{
				Summary:   "summary for " + filterKey,
				UpdatedAt: now,
			}
			s.SummariesMu.Unlock()
			return nil
		}

		err := CreateSessionSummaryWithCascade(context.Background(), sess, "app/math", false, createFunc)
		require.NoError(t, err)

		// Should call createFunc twice (no optimization).
		require.Equal(t, 2, len(calls))
		require.Contains(t, calls, "app/math")
		require.Contains(t, calls, "")
	})

	t.Run("single filterKey with error - error propagated", func(t *testing.T) {
		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
			Events: []event.Event{
				makeEvent("e1", now, "app/math"),
			},
		}

		createFunc := func(ctx context.Context, s *session.Session, filterKey string, force bool) error {
			return errors.New("LLM error")
		}

		err := CreateSessionSummaryWithCascade(context.Background(), sess, "app/math", false, createFunc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "LLM error")
	})

	t.Run("single filterKey persist full-session fails - error propagated", func(t *testing.T) {
		var callCount int

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
			Events: []event.Event{
				makeEvent("e1", now, "app/math"),
			},
			Summaries: make(map[string]*session.Summary),
		}

		createFunc := func(ctx context.Context, s *session.Session, filterKey string, force bool) error {
			callCount++
			if filterKey == session.SummaryFilterKeyAllContents {
				// Second call (persist full-session) fails.
				return errors.New("persist error")
			}
			// First call succeeds.
			s.SummariesMu.Lock()
			s.Summaries[filterKey] = &session.Summary{
				Summary:   "summary for " + filterKey,
				UpdatedAt: now,
			}
			s.SummariesMu.Unlock()
			return nil
		}

		err := CreateSessionSummaryWithCascade(context.Background(), sess, "app/math", false, createFunc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "persist full-session summary failed")
		require.Contains(t, err.Error(), "persist error")
		require.Equal(t, 2, callCount)
	})

	t.Run("empty filterKey - no optimization needed", func(t *testing.T) {
		var callCount int

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
			Events: []event.Event{
				makeEvent("e1", now, "app/math"),
			},
		}

		createFunc := func(ctx context.Context, s *session.Session, filterKey string, force bool) error {
			callCount++
			return nil
		}

		err := CreateSessionSummaryWithCascade(context.Background(), sess, "", false, createFunc)
		require.NoError(t, err)
		require.Equal(t, 1, callCount)
	})
}
