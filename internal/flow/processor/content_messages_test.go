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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
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

func TestProcessRequest_FiltersEmptyAssistantMessages(t *testing.T) {
	sess := &session.Session{}
	sess.Events = append(sess.Events,
		newSessionEvent("user", model.NewUserMessage("hello")),
		event.Event{
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{
					{Index: 0, Message: model.Message{Role: model.RoleAssistant}},
					{Index: 1, Message: model.NewAssistantMessage("hi")},
				},
			},
			Author: "test-agent",
		},
	)

	inv := &agent.Invocation{
		InvocationID: "inv-empty-assistant",
		AgentName:    "test-agent",
		Session:      sess,
	}

	req := &model.Request{}
	NewContentRequestProcessor().ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, 2)
	require.True(t, model.MessagesEqual(model.NewUserMessage("hello"), req.Messages[0]))
	require.True(t, model.MessagesEqual(model.NewAssistantMessage("hi"), req.Messages[1]))
}

func TestProcessRequest_FiltersEmptyAssistantMessages_ToolCallResponse(t *testing.T) {
	toolCallMsg := model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{
				Type: "function",
				ID:   "call_1",
				Function: model.FunctionDefinitionParam{
					Name:      "get_user_phone",
					Arguments: []byte(`{"purpose":"test"}`),
				},
			},
		},
	}

	sess := &session.Session{}
	sess.Events = append(sess.Events,
		newSessionEvent("user", model.NewUserMessage("hi")),
		event.Event{
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{
					{Index: 0, Message: toolCallMsg},
					{Index: 1, Message: model.Message{Role: model.RoleAssistant}},
				},
			},
			Author: "test-agent",
		},
	)

	inv := &agent.Invocation{
		InvocationID: "inv-empty-assistant-toolcall",
		AgentName:    "test-agent",
		Session:      sess,
	}

	req := &model.Request{}
	NewContentRequestProcessor().ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, 2)
	require.True(t, model.MessagesEqual(model.NewUserMessage("hi"), req.Messages[0]))
	require.True(t, model.MessagesEqual(toolCallMsg, req.Messages[1]))
}

func TestProcessRequest_IncludeContentsNone_FiltersEmptyAssistantMessages(t *testing.T) {
	sess := &session.Session{}

	userEvt := newSessionEvent("user", model.NewUserMessage("hello"))
	userEvt.InvocationID = "inv-include-none"
	assistantEvt := event.Event{
		InvocationID: "inv-include-none",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Index: 0, Message: model.Message{Role: model.RoleAssistant}},
				{Index: 1, Message: model.NewAssistantMessage("hi")},
			},
		},
		Author: "test-agent",
	}
	sess.Events = append(sess.Events, userEvt, assistantEvt)

	inv := &agent.Invocation{
		InvocationID: "inv-include-none",
		AgentName:    "test-agent",
		Session:      sess,
		RunOptions: agent.RunOptions{
			RuntimeState: map[string]any{
				graph.CfgKeyIncludeContents: "none",
			},
		},
	}

	req := &model.Request{}
	NewContentRequestProcessor().ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, 2)
	require.True(t, model.MessagesEqual(model.NewUserMessage("hello"), req.Messages[0]))
	require.True(t, model.MessagesEqual(model.NewAssistantMessage("hi"), req.Messages[1]))
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

func TestProcessRequest_InsertsInjectedContextMessages_AfterSystemMessages(t *testing.T) {
	requestContext := []model.Message{
		model.NewSystemMessage("ctx system"),
		model.NewUserMessage("ctx user"),
		model.NewAssistantMessage("ctx assistant"),
	}

	inv := &agent.Invocation{
		InvocationID: "inv-ctx",
		AgentName:    "test-agent",
		Session:      &session.Session{},
		Message:      model.NewUserMessage("hi there"),
		RunOptions: agent.RunOptions{
			InjectedContextMessages: requestContext,
		},
	}

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("agent system prompt"),
			model.NewSystemMessage("session summary"),
		},
	}
	p := NewContentRequestProcessor()
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, 2+len(requestContext)+1)
	require.True(t, model.MessagesEqual(model.NewSystemMessage("agent system prompt"), req.Messages[0]))
	require.True(t, model.MessagesEqual(model.NewSystemMessage("session summary"), req.Messages[1]))
	require.True(t, model.MessagesEqual(model.NewSystemMessage("ctx system"), req.Messages[2]))
	require.True(t, model.MessagesEqual(model.NewUserMessage("ctx user"), req.Messages[3]))
	require.True(t, model.MessagesEqual(model.NewAssistantMessage("ctx assistant"), req.Messages[4]))
	require.True(t, model.MessagesEqual(model.NewUserMessage("hi there"), req.Messages[5]))
}

func TestProcessRequest_InsertsInjectedContextMessages_WhenNoSystemMessages(t *testing.T) {
	requestContext := []model.Message{
		model.NewUserMessage("ctx user"),
		model.NewAssistantMessage("ctx assistant"),
	}

	inv := &agent.Invocation{
		InvocationID: "inv-ctx-no-system",
		AgentName:    "test-agent",
		Message:      model.NewUserMessage("hi there"),
		RunOptions: agent.RunOptions{
			InjectedContextMessages: requestContext,
		},
	}

	req := &model.Request{}
	p := NewContentRequestProcessor()
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, len(requestContext)+1)
	require.True(t, model.MessagesEqual(model.NewUserMessage("ctx user"), req.Messages[0]))
	require.True(t, model.MessagesEqual(model.NewAssistantMessage("ctx assistant"), req.Messages[1]))
	require.True(t, model.MessagesEqual(model.NewUserMessage("hi there"), req.Messages[2]))
}

func TestProcessRequest_InsertsInjectedContextMessages_BeforeSessionHistory(t *testing.T) {
	requestContext := []model.Message{
		model.NewSystemMessage("ctx system"),
		model.NewUserMessage("ctx user"),
	}

	sess := &session.Session{}
	sess.Events = append(sess.Events,
		newSessionEvent("user", model.NewUserMessage("hello")),
		newSessionEvent("test-agent", model.NewAssistantMessage("hi")),
	)

	inv := &agent.Invocation{
		InvocationID: "inv-ctx-history",
		AgentName:    "test-agent",
		Session:      sess,
		Message:      model.NewUserMessage("current"),
		RunOptions: agent.RunOptions{
			InjectedContextMessages: requestContext,
		},
	}

	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("agent system prompt")},
	}
	p := NewContentRequestProcessor()
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, 1+len(requestContext)+3)
	require.True(t, model.MessagesEqual(model.NewSystemMessage("agent system prompt"), req.Messages[0]))
	require.True(t, model.MessagesEqual(model.NewSystemMessage("ctx system"), req.Messages[1]))
	require.True(t, model.MessagesEqual(model.NewUserMessage("ctx user"), req.Messages[2]))
	require.True(t, model.MessagesEqual(model.NewUserMessage("hello"), req.Messages[3]))
	require.True(t, model.MessagesEqual(model.NewAssistantMessage("hi"), req.Messages[4]))
	require.True(t, model.MessagesEqual(model.NewUserMessage("current"), req.Messages[5]))
}

func TestProcessRequest_NoDuplicateInvocationToolMessage(t *testing.T) {
	const (
		requestID   = "req-tool-message"
		toolCallID  = "call-1"
		toolName    = "external_tool"
		toolContent = `{"status":"ok"}`
	)

	msg := model.NewToolMessage(toolCallID, toolName, toolContent)
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID: requestID,
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{
						{Index: 0, Message: msg},
					},
				},
				Author: "user",
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(msg),
		agent.WithInvocationRunOptions(
			agent.RunOptions{RequestID: requestID},
		),
	)
	inv.AgentName = "test-agent"

	req := &model.Request{}
	p := NewContentRequestProcessor()
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, 1)
	require.True(t, model.MessagesEqual(msg, req.Messages[0]))
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

func TestProcessRequest_IncludeInvocationMessage_WhenNoBranchEvents_Multimodal(t *testing.T) {
	// Session has events, but authored under a different filter key/branch.
	sess := &session.Session{}
	sess.Events = append(sess.Events, event.Event{
		Response: &model.Response{
			Done:    true,
			Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("context")}},
		},
		Author:    "other-agent",
		FilterKey: "other-agent",
		Version:   event.CurrentVersion,
	})

	msg := model.NewUserMessage("")
	msg.AddImageURL("https://example.com/image.png", "auto")

	// Build invocation explicitly with filter key set to sub-agent branch.
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(msg),
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

// Test that session summary is inserted as a separate system message after the
// last system message.
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
	raw, ok := inv1.GetState(contentHasSessionSummaryStateKey)
	require.True(t, ok)
	require.Equal(t, true, raw)

	// Should have 4 messages: system, summary system, user, current request
	require.Equal(t, 4, len(req1.Messages))
	require.Equal(t, model.RoleSystem, req1.Messages[0].Role)
	require.Equal(t, "existing system prompt", req1.Messages[0].Content)
	require.Equal(t, model.RoleSystem, req1.Messages[1].Role)
	require.Equal(t, NewContentRequestProcessor().formatSummary("Session summary content"), req1.Messages[1].Content)
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
	raw, ok = inv2.GetState(contentHasSessionSummaryStateKey)
	require.True(t, ok)
	require.Equal(t, true, raw)

	// Should have 3 messages: summary system, user, current request
	require.Equal(t, 3, len(req2.Messages))
	require.Equal(t, model.RoleSystem, req2.Messages[0].Role)
	require.Equal(t, NewContentRequestProcessor().formatSummary("Session summary content"), req2.Messages[0].Content)
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
	raw, ok = inv3.GetState(contentHasSessionSummaryStateKey)
	require.True(t, ok)
	require.Equal(t, true, raw)

	// Should have 5 messages: system1, system2, summary system, user, current
	// request.
	require.Equal(t, 5, len(req3.Messages))
	require.Equal(t, model.RoleSystem, req3.Messages[0].Role)
	require.Equal(t, "system 1", req3.Messages[0].Content)
	require.Equal(t, model.RoleSystem, req3.Messages[1].Role)
	require.Equal(t, "system 2", req3.Messages[1].Content)
	require.Equal(t, model.RoleSystem, req3.Messages[2].Role)
	require.Equal(
		t,
		NewContentRequestProcessor().formatSummary(
			"Session summary content",
		),
		req3.Messages[2].Content,
	)
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
	raw, ok := inv1.GetState(contentHasSessionSummaryStateKey)
	require.True(t, ok)
	require.Equal(t, true, raw)

	// Should have 2 messages: summary system, current request
	require.Equal(t, 2, len(req1.Messages))
	require.Equal(t, model.RoleSystem, req1.Messages[0].Role)
	require.Equal(t, NewContentRequestProcessor().formatSummary("Session summary content"), req1.Messages[0].Content)
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
	raw, ok = inv2.GetState(contentHasSessionSummaryStateKey)
	require.True(t, ok)
	require.Equal(t, true, raw)

	// Should have 3 messages: system, summary system, current request
	require.Equal(t, 3, len(req2.Messages))
	require.Equal(t, model.RoleSystem, req2.Messages[0].Role)
	require.Equal(t, "system prompt", req2.Messages[0].Content)
	require.Equal(t, model.RoleSystem, req2.Messages[1].Role)
	require.Equal(t, NewContentRequestProcessor().formatSummary("Session summary content"), req2.Messages[1].Content)
	require.Equal(t, model.RoleUser, req2.Messages[2].Role)
	require.Equal(t, "current request", req2.Messages[2].Content)
}

func TestContentRequestProcessor_AggregatePrefixSummaries_Sorted(
	t *testing.T,
) {
	p := NewContentRequestProcessor()
	summaries := map[string]*session.Summary{
		"app/b": {
			Summary: "b",
			UpdatedAt: time.Date(
				2023, 1, 2, 12, 0, 0, 0, time.UTC,
			),
		},
		"app": {
			Summary: "root",
			UpdatedAt: time.Date(
				2023, 1, 1, 12, 0, 0, 0, time.UTC,
			),
		},
		"app/a": {
			Summary: "a",
			UpdatedAt: time.Date(
				2023, 1, 3, 12, 0, 0, 0, time.UTC,
			),
		},
		"other": {
			Summary: "ignored",
			UpdatedAt: time.Date(
				2023, 1, 4, 12, 0, 0, 0, time.UTC,
			),
		},
	}

	got, updatedAt := p.aggregatePrefixSummaries(summaries, "app")
	require.Equal(t, "root\n\na\n\nb", got)
	require.Equal(t,
		time.Date(2023, 1, 3, 12, 0, 0, 0, time.UTC),
		updatedAt,
	)
}

func TestPromptCachePrefixStability_DynamicSystemTail(t *testing.T) {
	const (
		approxRunesPerToken = 4
		cachePrefixTokens   = 1024
		stableSysATokens    = 900
		stableSysBTokens    = 300

		stableSysAChar = "A"
		stableSysBChar = "B"

		summaryRun1 = "summary-run-1"
		summaryRun2 = "summary-run-2"
	)

	cachePrefixRunes := cachePrefixTokens * approxRunesPerToken

	sysA := strings.Repeat(
		stableSysAChar,
		stableSysATokens*approxRunesPerToken,
	)
	sysB := strings.Repeat(
		stableSysBChar,
		stableSysBTokens*approxRunesPerToken,
	)

	build := func(summaryText string) *model.Request {
		sess := &session.Session{
			Summaries: map[string]*session.Summary{
				"test-agent": {
					Summary:   summaryText,
					UpdatedAt: time.Now(),
				},
			},
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(sess),
			agent.WithInvocationEventFilterKey("test-agent"),
			agent.WithInvocationMessage(model.NewUserMessage("hi")),
		)
		inv.AgentName = "test-agent"

		req := &model.Request{
			Messages: []model.Message{
				model.NewSystemMessage(sysA),
				model.NewSystemMessage(sysB),
			},
		}

		p := NewContentRequestProcessor(WithAddSessionSummary(true))
		p.ProcessRequest(context.Background(), inv, req, nil)
		return req
	}

	render := func(messages []model.Message) string {
		var b strings.Builder
		for _, msg := range messages {
			b.WriteString(msg.Role.String())
			b.WriteString(":")
			b.WriteString(msg.Content)
			b.WriteString("\n")
		}
		return b.String()
	}

	firstRunes := func(text string, maxRunes int) string {
		if maxRunes <= 0 {
			return ""
		}
		r := []rune(text)
		if len(r) <= maxRunes {
			return text
		}
		return string(r[:maxRunes])
	}

	reqRun1 := build(summaryRun1)
	reqRun2 := build(summaryRun2)

	prefixRun1 := firstRunes(render(reqRun1.Messages), cachePrefixRunes)
	prefixRun2 := firstRunes(render(reqRun2.Messages), cachePrefixRunes)

	// New behavior: summary is appended after all stable system messages, so
	// the cacheable prefix stays stable across runs.
	require.Equal(t, prefixRun1, prefixRun2)
	require.NotContains(t, prefixRun1, summaryRun1)
	require.NotContains(t, prefixRun2, summaryRun2)

	legacyMessages := func(summaryText string) []model.Message {
		msgs := []model.Message{
			model.NewSystemMessage(sysA),
			model.NewSystemMessage(sysB),
			model.NewUserMessage("hi"),
		}
		idx := findSystemMessageIndex(msgs)
		summaryMsg := model.NewSystemMessage(summaryText)
		if idx < 0 {
			return append([]model.Message{summaryMsg}, msgs...)
		}
		out := append([]model.Message{}, msgs[:idx+1]...)
		out = append(out, summaryMsg)
		out = append(out, msgs[idx+1:]...)
		return out
	}

	legacyPrefix1 := firstRunes(
		render(legacyMessages(summaryRun1)),
		cachePrefixRunes,
	)
	legacyPrefix2 := firstRunes(
		render(legacyMessages(summaryRun2)),
		cachePrefixRunes,
	)

	// Old behavior: summary inserted after the first system message, likely
	// landing in the first ~1024 tokens and invalidating the cache prefix.
	require.NotEqual(t, legacyPrefix1, legacyPrefix2)
	require.Contains(t, legacyPrefix1, summaryRun1)
	require.Contains(t, legacyPrefix2, summaryRun2)
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
