//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

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

func TestMemoryService_GetSessionSummaryText_LocalPreferred(t *testing.T) {
	s := NewSessionService()
	sess := &session.Session{Summaries: map[string]*session.Summary{
		"":   {Summary: "full", UpdatedAt: time.Now()},
		"b1": {Summary: "branch", UpdatedAt: time.Now()},
	}}
	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	require.Equal(t, "full", text)
}

func TestMemoryService_CreateSessionSummary_NoSummarizer_NoOp(t *testing.T) {
	s := NewSessionService()
	sess := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "b1", false))
}

func TestMemoryService_CreateSessionSummary_NoUpdate_SkipPersist(t *testing.T) {
	s := NewSessionService()
	// Create stored session first because CreateSessionSummary looks it up under lock.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	stored, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)
	require.NotNil(t, stored)

	// summarizer allow=false leads to updated=false; should return without persisting.
	s.opts.summarizer = &fakeSummarizer{allow: false, out: "sum"}
	require.NoError(t, s.CreateSessionSummary(context.Background(), &session.Session{
		ID: key.SessionID, AppName: key.AppName, UserID: key.UserID,
	}, "b1", false))

	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	_, exists := got.Summaries["b1"]
	require.False(t, exists)
}

func TestMemoryService_CreateSessionSummary_UpdateAndPersist(t *testing.T) {
	s := NewSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid2"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append one valid event so delta is non-empty.
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Enable summarizer and create summary under filterKey "" (full-session).
	s.opts.summarizer = &fakeSummarizer{allow: true, out: "summary-text"}
	require.NoError(t, s.CreateSessionSummary(context.Background(), &session.Session{
		ID: key.SessionID, AppName: key.AppName, UserID: key.UserID,
	}, "", false))

	// Verify stored summary exists and GetSessionSummaryText returns it.
	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	sum, ok := got.Summaries[""]
	require.True(t, ok)
	require.Equal(t, "summary-text", sum.Summary)

	text, ok := s.GetSessionSummaryText(context.Background(), got)
	require.True(t, ok)
	require.Equal(t, "summary-text", text)
}
