//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func conversationEvent(role model.Role, text, filter string) event.Event {
	return event.Event{Version: event.CurrentVersion, FilterKey: filter, Response: &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: role, Content: text}}},
	}}
}

func TestDecisionComponentsUseSessionConversation(t *testing.T) {
	t.Parallel()
	sess := &session.Session{Events: []event.Event{
		conversationEvent(model.RoleUser, "find flights", "app"),
		conversationEvent(model.RoleAssistant, "checking flights", "app/agent"),
		conversationEvent(model.RoleTool, "secret tool output", "app/agent"),
		conversationEvent(model.RoleUser, "other branch secret", "app/other"),
		conversationEvent(model.RoleUser, "continue", "app/agent"),
		conversationEvent(model.RoleAssistant, "more details", "app/agent"),
	}}
	inv := agent.NewInvocation(agent.WithInvocationSession(sess), agent.WithInvocationEventFilterKey("app/agent"),
		agent.WithInvocationMessage(model.NewUserMessage("continue")))
	sess.Events[len(sess.Events)-2].InvocationID = inv.InvocationID
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolsServer, toolsRec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	cb, err := NewToolCallback(toolsServer.URL, WithHTTPClient(toolsServer.Client()), WithMinToolCount(0))
	require.NoError(t, err)
	req := newUserRequest("continue", newToolMap(&stubTool{name: "flights", description: "find flights"}))
	_, err = cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, 1, toolsRec.requests())

	skillServer, skillRec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	sf, err := NewSkillFilter(skillServer.URL, &fakeSkillRepo{summaries: testSkillSummaries()}, WithHTTPClient(skillServer.Client()))
	require.NoError(t, err)
	sf.VisibilityFilter()(ctx, testSkillSummaries()[0])

	router, routerRec := newRoutingEnv(t, http.StatusOK, choiceResponder("fast"),
		WithModelCandidate("fast", &stubModel{name: "fast"}, "simple"))
	_, err = router.Select(ctx, inv)
	require.NoError(t, err)

	toolState := toolsRec.lastState().(map[string]any)["user_message"].(string)
	skillState := skillRec.lastState().(map[string]any)["user_message"].(string)
	routeState := routerRec.lastState().(string)
	require.Equal(t, toolState, skillState)
	require.Equal(t, toolState, routeState)
	require.Contains(t, toolState, "find flights")
	require.Contains(t, toolState, "checking flights")
	require.Contains(t, toolState, "assistant: more details")
	require.NotContains(t, toolState, "secret")
	require.Equal(t, 1, strings.Count(toolState, "continue"))
}

func TestDecisionConversationBudgetAndRepeatedInput(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("旧", stateClipLen)
	inv := agent.NewInvocation(agent.WithInvocationSession(&session.Session{Events: []event.Event{
		conversationEvent(model.RoleUser, long, ""),
		conversationEvent(model.RoleUser, "continue", ""),
	}}), agent.WithInvocationMessage(model.NewUserMessage("continue")))
	state, _ := decisionConversation(inv)
	require.LessOrEqual(t, len([]rune(state)), stateClipLen)
	require.Equal(t, 2, strings.Count(state, "continue"))
	require.True(t, strings.HasSuffix(state, "user: continue"))
}

func TestDecisionConversationCurrentMessageBeforeAssistant(t *testing.T) {
	t.Parallel()
	sess := &session.Session{Events: []event.Event{
		conversationEvent(model.RoleUser, "continue", ""),
		conversationEvent(model.RoleAssistant, "more details", ""),
	}}
	inv := agent.NewInvocation(agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage("continue")))
	sess.Events[0].InvocationID = inv.InvocationID
	state, users := decisionConversation(inv)
	require.Equal(t, "user: continue\nassistant: more details", state)
	require.Equal(t, "continue", users)
}

func TestDecisionConversationContentParts(t *testing.T) {
	t.Parallel()
	text := "find flights"
	inv := agent.NewInvocation(agent.WithInvocationMessage(model.Message{Role: model.RoleUser,
		ContentParts: []model.ContentPart{{Type: model.ContentTypeText, Text: &text}},
	}))
	state, users := decisionConversation(inv)
	require.Equal(t, "user: find flights", state)
	require.Equal(t, "find flights", users)
}

func TestTokenlessPartialEmbeddingFailureKeepsCatalogs(t *testing.T) {
	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	emb := &stubEmbedder{vectors: map[string][]float64{
		"x": {1, 0}, "alpha\nfirst": {1, 0},
	}}
	opts := []Option{WithHTTPClient(server.Client()), WithShortlistLimit(1), WithShortlistEmbedder(emb)}
	cb, err := NewToolCallback(server.URL, append(opts, WithMinToolCount(0))...)
	require.NoError(t, err)
	summaries := []skill.Summary{{Name: "alpha", Description: "first"}, {Name: "beta", Description: "second"}}
	sf, err := NewSkillFilter(server.URL, &fakeSkillRepo{summaries: summaries}, opts...)
	require.NoError(t, err)
	inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("x")))
	ctx := agent.NewInvocationContext(context.Background(), inv)
	tools := newToolMap(&stubTool{name: "alpha", description: "first"}, &stubTool{name: "beta", description: "second"})
	req := newUserRequest("x", tools)
	_, err = cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, tools, req.Tools)
	for _, summary := range summaries {
		require.True(t, sf.VisibilityFilter()(ctx, summary), summary.Name)
	}
	require.Zero(t, rec.requests())
}
