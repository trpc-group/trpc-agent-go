//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

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
