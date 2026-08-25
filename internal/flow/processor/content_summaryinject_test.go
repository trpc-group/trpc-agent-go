//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryinject"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const injectSummaryText = "SUMMARY-CONTENT-THAT-MUST-REACH-THE-MODEL"

func injectSession(summaries map[string]*session.Summary) *session.Session {
	return &session.Session{
		ID:        "session",
		Summaries: summaries,
		Events: []event.Event{{
			Author:    "user",
			Timestamp: time.Date(2023, 1, 1, 11, 0, 0, 0, time.UTC),
			FilterKey: "test-agent",
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewUserMessage("earlier question"),
			}}},
		}},
	}
}

func injectInvocation(sess *session.Session) *agent.Invocation {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("test-agent"),
		agent.WithInvocationMessage(model.NewUserMessage("current request")),
	)
	inv.AgentName = "test-agent"
	return inv
}

func TestSummaryInjectionRecordsExactSelection(t *testing.T) {
	sess := injectSession(map[string]*session.Summary{
		"test-agent": {
			Summary:   injectSummaryText,
			UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	})
	inv := injectInvocation(sess)
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("existing system prompt"),
	}}

	NewContentRequestProcessor(WithAddSessionSummary(true)).
		ProcessRequest(context.Background(), inv, req, nil)

	selection, ok := summaryinject.FromInvocation(inv)
	require.True(t, ok)
	require.Equal(t, summaryinject.LookupStrategyPrefix, selection.LookupStrategy)
	require.Equal(t, summaryinject.LookupResultExact, selection.LookupResult)
	require.True(t, selection.Selected)
	require.Equal(t, 1, selection.StoredSummaries)
	require.Equal(t, 1, selection.MatchingCandidates)
	require.Equal(t, 1, selection.SessionEvents)
	require.True(t, selection.BlockPresent(req.Messages),
		"the selected summary must be detected in the assembled request")
}

// TestSummaryInjectionDetectsDroppedSummary proves that the reported injection
// state is derived from the request that will actually be sent, not from the
// selection decision. A later stage that rewrites the request must be visible.
func TestSummaryInjectionDetectsDroppedSummary(t *testing.T) {
	sess := injectSession(map[string]*session.Summary{
		"test-agent": {
			Summary:   injectSummaryText,
			UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	})
	inv := injectInvocation(sess)
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("existing system prompt"),
	}}

	NewContentRequestProcessor(WithAddSessionSummary(true)).
		ProcessRequest(context.Background(), inv, req, nil)

	selection, ok := summaryinject.FromInvocation(inv)
	require.True(t, ok)
	require.True(t, selection.BlockPresent(req.Messages))

	// A provider adapter or token tailoring rebuilds the request without the
	// summary block.
	tailored := []model.Message{
		model.NewSystemMessage("existing system prompt"),
		model.NewUserMessage("current request"),
	}
	require.False(t, selection.BlockPresent(tailored))
}

func TestSummaryInjectionRecordsUserModeSelection(t *testing.T) {
	sess := injectSession(map[string]*session.Summary{
		"test-agent": {
			Summary:   injectSummaryText,
			UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	})
	inv := injectInvocation(sess)
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("existing system prompt"),
	}}

	NewContentRequestProcessor(
		WithAddSessionSummary(true),
		WithSessionSummaryInjectionMode(SessionSummaryInjectionUser),
	).ProcessRequest(context.Background(), inv, req, nil)

	selection, ok := summaryinject.FromInvocation(inv)
	require.True(t, ok)
	require.True(t, selection.Selected)
	require.True(t, selection.BlockPresent(req.Messages),
		"user-mode summaries are merged into a user message and must still be detected")
}

// TestSummaryInjectionRecordsLookupMiss covers a session that stores summaries
// under keys this request does not read.
func TestSummaryInjectionRecordsLookupMiss(t *testing.T) {
	sess := injectSession(map[string]*session.Summary{
		"other-agent": {
			Summary:   injectSummaryText,
			UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	})
	inv := injectInvocation(sess)
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("existing system prompt"),
	}}

	NewContentRequestProcessor(WithAddSessionSummary(true)).
		ProcessRequest(context.Background(), inv, req, nil)

	selection, ok := summaryinject.FromInvocation(inv)
	require.True(t, ok)
	require.False(t, selection.Selected)
	require.Equal(t, summaryinject.LookupResultNone, selection.LookupResult)
	require.Equal(t, 1, selection.StoredSummaries,
		"stored summaries under other keys must stay visible to operators")
	require.Zero(t, selection.MatchingCandidates)
	require.False(t, selection.FullSessionPresent)
	require.False(t, selection.ScopeMismatch(),
		"an unrelated branch summary is a legitimate miss, not a scope mismatch")
	require.False(t, selection.BlockPresent(req.Messages))
}

// TestSummaryInjectionRecordsScopeMismatch covers a branch-scoped request that
// finds nothing in its configured scope while a full-session summary exists
// outside it. The unused summary is recorded; it does not mean the scoped
// history was dropped from this request.
func TestSummaryInjectionRecordsScopeMismatch(t *testing.T) {
	sess := injectSession(map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {
			Summary:   injectSummaryText,
			UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	})
	inv := injectInvocation(sess)
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("existing system prompt"),
	}}

	NewContentRequestProcessor(WithAddSessionSummary(true)).
		ProcessRequest(context.Background(), inv, req, nil)

	selection, ok := summaryinject.FromInvocation(inv)
	require.True(t, ok)
	require.False(t, selection.Selected)
	require.Equal(t, summaryinject.LookupStrategyPrefix, selection.LookupStrategy)
	require.Equal(t, summaryinject.LookupResultNone, selection.LookupResult)
	require.Equal(t, 1, selection.StoredSummaries)
	require.Zero(t, selection.MatchingCandidates)
	require.True(t, selection.FullSessionPresent)
	require.True(t, selection.ScopedRequest)
	require.True(t, selection.ScopeMismatch())
}

// TestSummaryInjectionReadsFullSessionSummaryInAllMode is the counterpart:
// the same stored state is in scope when the request reads all contents, so
// there is nothing to report.
func TestSummaryInjectionReadsFullSessionSummaryInAllMode(t *testing.T) {
	sess := injectSession(map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {
			Summary:   injectSummaryText,
			UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	})
	inv := injectInvocation(sess)
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("existing system prompt"),
	}}

	NewContentRequestProcessor(
		WithAddSessionSummary(true),
		WithBranchFilterMode(BranchFilterModeAll),
	).ProcessRequest(context.Background(), inv, req, nil)

	selection, ok := summaryinject.FromInvocation(inv)
	require.True(t, ok)
	require.True(t, selection.Selected)
	require.Equal(t, summaryinject.LookupStrategyAll, selection.LookupStrategy)
	require.Equal(t, summaryinject.LookupResultExact, selection.LookupResult)
	require.False(t, selection.ScopedRequest)
	require.False(t, selection.ScopeMismatch())
}

// TestSummaryInjectionExactModeReportsScopeMismatch guards the exact-key
// strategy, where a branch request cannot fall back to any other key.
func TestSummaryInjectionExactModeReportsScopeMismatch(t *testing.T) {
	sess := injectSession(map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {
			Summary:   injectSummaryText,
			UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	})
	inv := injectInvocation(sess)
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("existing system prompt"),
	}}

	NewContentRequestProcessor(
		WithAddSessionSummary(true),
		WithBranchFilterMode(BranchFilterModeExact),
	).ProcessRequest(context.Background(), inv, req, nil)

	selection, ok := summaryinject.FromInvocation(inv)
	require.True(t, ok)
	require.Equal(t, summaryinject.LookupStrategyExact, selection.LookupStrategy)
	require.True(t, selection.ScopeMismatch())
}

func TestSummaryInjectionRecordsNothingWhenSummariesAreDisabled(t *testing.T) {
	sess := injectSession(map[string]*session.Summary{
		"test-agent": {
			Summary:   injectSummaryText,
			UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	})
	inv := injectInvocation(sess)
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("existing system prompt"),
	}}

	NewContentRequestProcessor(WithAddSessionSummary(false)).
		ProcessRequest(context.Background(), inv, req, nil)

	_, ok := summaryinject.FromInvocation(inv)
	require.False(t, ok,
		"requests that do not use session summaries must report nothing")
}

// TestSummaryInjectionIsClearedBetweenRequests guards against a stale selection
// from an earlier model request being reported for a later one.
func TestSummaryInjectionIsClearedBetweenRequests(t *testing.T) {
	sess := injectSession(map[string]*session.Summary{
		"test-agent": {
			Summary:   injectSummaryText,
			UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	})
	inv := injectInvocation(sess)
	processor := NewContentRequestProcessor(WithAddSessionSummary(true))

	first := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("existing system prompt"),
	}}
	processor.ProcessRequest(context.Background(), inv, first, nil)
	selection, ok := summaryinject.FromInvocation(inv)
	require.True(t, ok)
	require.True(t, selection.Selected)

	sess.Summaries = map[string]*session.Summary{}
	second := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("existing system prompt"),
	}}
	processor.ProcessRequest(context.Background(), inv, second, nil)
	selection, ok = summaryinject.FromInvocation(inv)
	require.True(t, ok)
	require.False(t, selection.Selected)
	require.Zero(t, selection.StoredSummaries)
}
