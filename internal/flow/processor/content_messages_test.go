//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestProcessRequest_IgnoresRunOptionsMessages_UsesSessionOnly(t *testing.T) {
	// Even if RunOptions carries messages, content processor should only read from session.
	seed := []model.Message{
		model.NewSystemMessage("system guidance"),
		model.NewUserMessage("hello"),
		model.NewAssistantMessage("hi"),
	}

	sess := &session.Session{}
	sess.Events = append(sess.Events,
		newSessionEvent("user", model.NewUserMessage("hello")),
		newSessionEvent("test-agent", model.NewAssistantMessage("hi")),
		newSessionEvent("test-agent", model.NewAssistantMessage("latest from session")),
	)

	inv := &agent.Invocation{
		InvocationID: "inv-seed",
		AgentName:    "test-agent",
		Session:      sess,
		Message:      model.NewUserMessage("hello"),
		RunOptions:   agent.RunOptions{Messages: seed},
	}

	req := &model.Request{}
	ch := make(chan *event.Event, 2)
	p := NewContentRequestProcessor()

	p.ProcessRequest(context.Background(), inv, req, ch)

	// Expect only session-derived messages (3 entries), not the seed.
	require.Equal(t, 3, len(req.Messages))
	require.True(t, model.MessagesEqual(model.NewUserMessage("hello"), req.Messages[0]))
	require.True(t, model.MessagesEqual(model.NewAssistantMessage("hi"), req.Messages[1]))
	require.True(t, model.MessagesEqual(model.NewAssistantMessage("latest from session"), req.Messages[2]))
}

func TestProcessRequest_IncludeInvocationMessage_WhenNoSession(t *testing.T) {
	// When no session or empty, include invocation.Message as the only message.
	inv := &agent.Invocation{
		InvocationID: "inv-empty",
		AgentName:    "test-agent",
		Session:      &session.Session{},
		Message:      model.NewUserMessage("hi there"),
	}

	req := &model.Request{}
	ch := make(chan *event.Event, 1)
	p := NewContentRequestProcessor()

	p.ProcessRequest(context.Background(), inv, req, ch)
	require.Equal(t, 1, len(req.Messages))
	require.True(t, model.MessagesEqual(model.NewUserMessage("hi there"), req.Messages[0]))
}

// When session exists but has no events for the current branch, the invocation
// message should still be included so sub agent gets the tool args.
func TestProcessRequest_IncludeInvocationMessage_WhenNoBranchEvents(t *testing.T) {
	// Session has events, but authored under a different filter key/branch.
	sess := &session.Session{}
	// Event authored by other-agent; with IncludeContentsFiltered and filterKey
	// set to current agent, this should be filtered out.
	sess.Events = append(sess.Events, event.Event{
		Response: &model.Response{
			Done:    true,
			Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("context")}},
		},
		Author:    "other-agent",
		FilterKey: "other-agent",
		Version:   event.CurrentVersion,
	})

	// Build invocation explicitly with filter key set to sub-agent branch.
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage("{\\\"target\\\":\\\"svc\\\"}")),
		agent.WithInvocationEventFilterKey("sub-agent"),
	)
	inv.AgentName = "sub-agent"

	req := &model.Request{}
	ch := make(chan *event.Event, 1)
	p := NewContentRequestProcessor()

	p.ProcessRequest(context.Background(), inv, req, ch)

	// The other-agent event is filtered out; invocation message must be added.
	require.Equal(t, 1, len(req.Messages))
	require.True(t, model.MessagesEqual(inv.Message, req.Messages[0]))
}

func TestProcessRequest_PreserveSameBranchKeepsRoles(t *testing.T) {
	makeInvocation := func(sess *session.Session) *agent.Invocation {
		inv := agent.NewInvocation(
			agent.WithInvocationSession(sess),
			agent.WithInvocationMessage(
				model.NewUserMessage("latest request"),
			),
			agent.WithInvocationEventFilterKey("graph-agent"),
		)
		inv.AgentName = "graph-agent"
		inv.Branch = "graph-agent"
		return inv
	}

	assistantMsg := model.NewAssistantMessage("node produced answer")
	sess := &session.Session{}
	sess.Events = append(sess.Events,
		newSessionEventWithBranch("user", "graph-agent", "graph-agent", model.NewUserMessage("hi")),
		newSessionEventWithBranch("graph-node", "graph-agent", "graph-agent/graph-node", assistantMsg),
	)

	// Default behavior now preserves same-branch assistant/tool roles.
	// Explicitly enabling preserve keeps assistant role.
	preserveReq := &model.Request{}
	preserveProc := NewContentRequestProcessor(
		WithPreserveSameBranch(true),
	)
	preserveProc.ProcessRequest(
		context.Background(), makeInvocation(sess), preserveReq, nil,
	)
	require.Equal(t, 3, len(preserveReq.Messages))
	require.Equal(t, model.RoleUser, preserveReq.Messages[0].Role)
	require.Equal(t, model.RoleAssistant, preserveReq.Messages[1].Role)
	require.Equal(t, assistantMsg.Content, preserveReq.Messages[1].Content)

	// Disabling preserve rewrites same-branch events as user context.
	optOutReq := &model.Request{}
	optOutProc := NewContentRequestProcessor(
		WithPreserveSameBranch(false),
	)
	optOutProc.ProcessRequest(
		context.Background(), makeInvocation(sess), optOutReq, nil,
	)
	require.Equal(t, 3, len(optOutReq.Messages))
	require.Equal(t, model.RoleUser, optOutReq.Messages[0].Role)
	require.Equal(t, model.RoleUser, optOutReq.Messages[1].Role)
	require.Contains(t, optOutReq.Messages[1].Content, "For context")
}

// When the historical event branch is an ancestor or descendant of the current
// branch, PreserveSameBranch=true should keep assistant roles.
func TestProcessRequest_PreserveSameBranch_AncestorDescendant(t *testing.T) {
	makeInvocation := func(sess *session.Session) *agent.Invocation {
		inv := agent.NewInvocation(
			agent.WithInvocationSession(sess),
			agent.WithInvocationMessage(
				model.NewUserMessage("latest request"),
			),
			agent.WithInvocationEventFilterKey("graph-agent"),
		)
		inv.AgentName = "graph-agent"
		inv.Branch = "graph-agent/child"
		return inv
	}

	// ancestor: graph-agent
	// descendant: graph-agent/child/grandchild
	msgAncestor := model.NewAssistantMessage("from ancestor")
	msgDesc := model.NewAssistantMessage("from descendant")

	sess := &session.Session{}
	sess.Events = append(sess.Events,
		newSessionEventWithBranch(
			"graph-root", "graph-agent", "graph-agent", msgAncestor,
		),
		newSessionEventWithBranch(
			"graph-leaf", "graph-agent",
			"graph-agent/child/grandchild", msgDesc,
		),
	)

	req := &model.Request{}
	p := NewContentRequestProcessor(WithPreserveSameBranch(true))
	p.ProcessRequest(context.Background(), makeInvocation(sess), req, nil)

	require.Equal(t, 3, len(req.Messages))
	require.Equal(t, model.RoleAssistant, req.Messages[0].Role)
	require.Equal(t, msgAncestor.Content, req.Messages[0].Content)
	require.Equal(t, model.RoleAssistant, req.Messages[1].Role)
	require.Equal(t, msgDesc.Content, req.Messages[1].Content)
}

// When the historical event is on a different branch lineage, it should be
// converted to user context even when preserve is true (default).
func TestProcessRequest_CrossBranch_RewritesToUser(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationMessage(model.NewUserMessage("ask")),
		agent.WithInvocationEventFilterKey("graph-agent"),
	)
	inv.AgentName = "graph-agent"
	inv.Branch = "graph-agent"

	// Cross-branch event (not same lineage). Use the same filter key so it is
	// included by IncludeContentsFiltered.
	msg := model.NewAssistantMessage("foreign content")
	evt := newSessionEventWithBranch(
		"other-agent", "graph-agent", "other-root", msg,
	)

	sess := &session.Session{}
	sess.Events = append(sess.Events, evt)
	inv.Session = sess

	req := &model.Request{}
	p := NewContentRequestProcessor(WithPreserveSameBranch(true))
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Equal(t, 2, len(req.Messages))
	require.Equal(t, model.RoleUser, req.Messages[0].Role)
	require.Contains(t, req.Messages[0].Content, "For context")
}

func newSessionEvent(author string, msg model.Message) event.Event {
	return event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Index: 0, Message: msg},
			},
		},
		Author: author,
	}
}

// Test that session summary is inserted as a separate system message after the first system message.
func TestProcessRequest_SessionSummary_InsertAsSeparateSystemMessage(t *testing.T) {
	// Create session with summary
	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			"test-agent": {
				Summary:   "Session summary content",
				UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	// Test case 1: Request has system message followed by user message
	req1 := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("existing system prompt"),
			model.NewUserMessage("user question"),
		},
	}

	inv1 := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("test-agent"),
		agent.WithInvocationMessage(model.NewUserMessage("current request")),
	)
	inv1.AgentName = "test-agent"

	p1 := NewContentRequestProcessor(WithAddSessionSummary(true))
	p1.ProcessRequest(context.Background(), inv1, req1, nil)

	// Should have 4 messages: system, summary system, user, current request
	require.Equal(t, 4, len(req1.Messages))
	require.Equal(t, model.RoleSystem, req1.Messages[0].Role)
	require.Equal(t, "existing system prompt", req1.Messages[0].Content)
	require.Equal(t, model.RoleSystem, req1.Messages[1].Role)
	require.Equal(t, formatSummaryContent("Session summary content"), req1.Messages[1].Content)
	require.Equal(t, model.RoleUser, req1.Messages[2].Role)
	require.Equal(t, "user question", req1.Messages[2].Content)
	require.Equal(t, model.RoleUser, req1.Messages[3].Role)
	require.Equal(t, "current request", req1.Messages[3].Content)

	// Test case 2: Request has only user message (no system message)
	req2 := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("user question"),
		},
	}

	inv2 := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("test-agent"),
		agent.WithInvocationMessage(model.NewUserMessage("current request")),
	)
	inv2.AgentName = "test-agent"

	p2 := NewContentRequestProcessor(WithAddSessionSummary(true))
	p2.ProcessRequest(context.Background(), inv2, req2, nil)

	// Should have 3 messages: summary system, user, current request
	require.Equal(t, 3, len(req2.Messages))
	require.Equal(t, model.RoleSystem, req2.Messages[0].Role)
	require.Equal(t, formatSummaryContent("Session summary content"), req2.Messages[0].Content)
	require.Equal(t, model.RoleUser, req2.Messages[1].Role)
	require.Equal(t, "user question", req2.Messages[1].Content)
	require.Equal(t, model.RoleUser, req2.Messages[2].Role)
	require.Equal(t, "current request", req2.Messages[2].Content)

	// Test case 3: Request has multiple system messages
	req3 := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system 1"),
			model.NewSystemMessage("system 2"),
			model.NewUserMessage("user question"),
		},
	}

	inv3 := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("test-agent"),
		agent.WithInvocationMessage(model.NewUserMessage("current request")),
	)
	inv3.AgentName = "test-agent"

	p3 := NewContentRequestProcessor(WithAddSessionSummary(true))
	p3.ProcessRequest(context.Background(), inv3, req3, nil)

	// Should have 5 messages: system1, summary system, system2, user, current request
	require.Equal(t, 5, len(req3.Messages))
	require.Equal(t, model.RoleSystem, req3.Messages[0].Role)
	require.Equal(t, "system 1", req3.Messages[0].Content)
	require.Equal(t, model.RoleSystem, req3.Messages[1].Role)
	require.Equal(t, formatSummaryContent("Session summary content"), req3.Messages[1].Content)
	require.Equal(t, model.RoleSystem, req3.Messages[2].Role)
	require.Equal(t, "system 2", req3.Messages[2].Content)
	require.Equal(t, model.RoleUser, req3.Messages[3].Role)
	require.Equal(t, "user question", req3.Messages[3].Content)
	require.Equal(t, model.RoleUser, req3.Messages[4].Role)
	require.Equal(t, "current request", req3.Messages[4].Content)
}

// Test additional edge cases for session summary insertion.
func TestProcessRequest_SessionSummary_EdgeCases(t *testing.T) {
	// Create session with summary
	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			"test-agent": {
				Summary:   "Session summary content",
				UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	// Test case 1: Empty request messages
	req1 := &model.Request{
		Messages: []model.Message{},
	}

	inv1 := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("test-agent"),
		agent.WithInvocationMessage(model.NewUserMessage("current request")),
	)
	inv1.AgentName = "test-agent"

	p1 := NewContentRequestProcessor(WithAddSessionSummary(true))
	p1.ProcessRequest(context.Background(), inv1, req1, nil)

	// Should have 2 messages: summary system, current request
	require.Equal(t, 2, len(req1.Messages))
	require.Equal(t, model.RoleSystem, req1.Messages[0].Role)
	require.Equal(t, formatSummaryContent("Session summary content"), req1.Messages[0].Content)
	require.Equal(t, model.RoleUser, req1.Messages[1].Role)
	require.Equal(t, "current request", req1.Messages[1].Content)

	// Test case 2: Only system messages
	req2 := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system prompt"),
		},
	}

	inv2 := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("test-agent"),
		agent.WithInvocationMessage(model.NewUserMessage("current request")),
	)
	inv2.AgentName = "test-agent"

	p2 := NewContentRequestProcessor(WithAddSessionSummary(true))
	p2.ProcessRequest(context.Background(), inv2, req2, nil)

	// Should have 3 messages: system, summary system, current request
	require.Equal(t, 3, len(req2.Messages))
	require.Equal(t, model.RoleSystem, req2.Messages[0].Role)
	require.Equal(t, "system prompt", req2.Messages[0].Content)
	require.Equal(t, model.RoleSystem, req2.Messages[1].Role)
	require.Equal(t, formatSummaryContent("Session summary content"), req2.Messages[1].Content)
	require.Equal(t, model.RoleUser, req2.Messages[2].Role)
	require.Equal(t, "current request", req2.Messages[2].Content)
}

func newSessionEventWithBranch(author, filterKey, branch string, msg model.Message) event.Event {
	return event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Index: 0, Message: msg},
			},
		},
		Author:    author,
		FilterKey: filterKey,
		Branch:    branch,
		Version:   event.CurrentVersion,
	}
}
