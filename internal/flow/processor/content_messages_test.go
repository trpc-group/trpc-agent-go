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

	// Default behavior rewrites same-branch assistant events as user context.
	defaultReq := &model.Request{}
	defaultProc := NewContentRequestProcessor()
	defaultProc.ProcessRequest(context.Background(), makeInvocation(sess), defaultReq, nil)
	require.Equal(t, 2, len(defaultReq.Messages))
	require.Equal(t, model.RoleUser, defaultReq.Messages[0].Role)
	require.Equal(t, model.RoleUser, defaultReq.Messages[1].Role)
	require.Contains(t, defaultReq.Messages[1].Content, "For context")

	// Enabling preserve option keeps assistant/tool roles intact for same-branch events.
	preserveReq := &model.Request{}
	preserveProc := NewContentRequestProcessor(WithPreserveSameBranch(true))
	preserveProc.ProcessRequest(context.Background(), makeInvocation(sess), preserveReq, nil)
	require.Equal(t, 2, len(preserveReq.Messages))
	require.Equal(t, model.RoleUser, preserveReq.Messages[0].Role)
	require.Equal(t, model.RoleAssistant, preserveReq.Messages[1].Role)
	require.Equal(t, assistantMsg.Content, preserveReq.Messages[1].Content)
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
