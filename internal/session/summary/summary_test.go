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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type fakeSummarizer struct {
	allow bool
	out   string
}

func (f *fakeSummarizer) ShouldSummarize(sess *session.Session) bool { return f.allow }
func (f *fakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	return f.out, nil
}
func (f *fakeSummarizer) Metadata() map[string]any { return map[string]any{} }

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
