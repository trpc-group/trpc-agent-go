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
}

type fakeSummarizer struct {
	allow bool
	out   string
}

func (f *fakeSummarizer) ShouldSummarize(sess *session.Session) bool { return f.allow }
func (f *fakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
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
			name: "all-contents summary exists but empty, should pick other non-empty",
			summaries: map[string]*session.Summary{
				"":        {Summary: ""},
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "",
			wantText:  "filtered summary 1",
			wantOk:    true,
		},
		{
			name: "all-contents summary is nil, should pick other non-empty",
			summaries: map[string]*session.Summary{
				"":        nil,
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "",
			wantText:  "filtered summary 1",
			wantOk:    true,
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
			name: "no all-contents summary, pick first non-empty",
			summaries: map[string]*session.Summary{
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "",
			wantText:  "filtered summary 1",
			wantOk:    true,
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
			name: "mixed nil and empty summaries, pick first non-empty",
			summaries: map[string]*session.Summary{
				"":        nil,
				"filter1": {Summary: ""},
				"filter2": {Summary: "valid summary"},
			},
			filterKey: "",
			wantText:  "valid summary",
			wantOk:    true,
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
			name: "specific filter key not found, no full fallback",
			summaries: map[string]*session.Summary{
				"filter1": {Summary: "filtered summary 1"},
			},
			filterKey: "nonexistent",
			wantText:  "filtered summary 1",
			wantOk:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotOk := PickSummaryText(tt.summaries, tt.filterKey)
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
	tests := []struct {
		name              string
		filterKey         string
		force             bool
		expectCalls       []string
		expectError       bool
		createSummaryFunc func(context.Context, *session.Session, string, bool) error
	}{
		{
			name:        "filterKey is empty, only call once",
			filterKey:   "",
			force:       false,
			expectCalls: []string{""},
			expectError: false,
			createSummaryFunc: func(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
				return nil
			},
		},
		{
			name:        "filterKey is user-messages, call twice",
			filterKey:   "user-messages",
			force:       false,
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
			sess := &session.Session{
				ID:      "test-session",
				AppName: "test-app",
				UserID:  "test-user",
			}

			mockFunc := func(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
				calls = append(calls, filterKey)
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
	// Test using method value (like s.CreateSessionSummary)
	type mockService struct {
		summaries map[string]string
	}

	mockSvc := &mockService{}

	createFunc := func(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
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
	}

	err := CreateSessionSummaryWithCascade(context.Background(), sess, "user-messages", false, createFunc)
	require.NoError(t, err)

	// Should have created both summaries
	require.Equal(t, "summary-user-messages", mockSvc.summaries["user-messages"])
	require.Equal(t, "summary-", mockSvc.summaries[""])
}
