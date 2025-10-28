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
			agent.WithInvocationMessage(model.NewUserMessage("latest request")),
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
	defaultReq := &model.Request{}
	defaultProc := NewContentRequestProcessor()
	defaultProc.ProcessRequest(
		context.Background(), makeInvocation(sess), defaultReq, nil,
	)
	require.Equal(t, 2, len(defaultReq.Messages))
	require.Equal(t, model.RoleUser, defaultReq.Messages[0].Role)
	require.Equal(t, model.RoleAssistant, defaultReq.Messages[1].Role)
	require.Equal(t, assistantMsg.Content, defaultReq.Messages[1].Content)

	// Explicitly enabling preserve matches the default behavior.
	preserveReq := &model.Request{}
	preserveProc := NewContentRequestProcessor(
		WithPreserveSameBranch(true),
	)
	preserveProc.ProcessRequest(
		context.Background(), makeInvocation(sess), preserveReq, nil,
	)
	require.Equal(t, 2, len(preserveReq.Messages))
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
	require.Equal(t, 2, len(optOutReq.Messages))
	require.Equal(t, model.RoleUser, optOutReq.Messages[0].Role)
	require.Equal(t, model.RoleUser, optOutReq.Messages[1].Role)
	require.Contains(t, optOutReq.Messages[1].Content, "For context")
}

// When the historical event branch is an ancestor or descendant of the current
// branch, PreserveSameBranch (default: true) should keep assistant roles.
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
	p := NewContentRequestProcessor() // preserve=true by default
	p.ProcessRequest(context.Background(), makeInvocation(sess), req, nil)

	require.Equal(t, 2, len(req.Messages))
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
	p := NewContentRequestProcessor() // preserve=true by default
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Equal(t, 1, len(req.Messages))
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
