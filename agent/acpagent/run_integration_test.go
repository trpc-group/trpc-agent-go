//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestRunStreamsPromptUpdatesAndEmitsSingleFinalEvent(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"prompt_stream",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	invocation := newSessionInvocation(t, service, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "prompt-stream",
	})
	invocation.Message = model.NewUserMessage("hello")

	events, err := acpAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	var got []*event.Event
	for evt := range events {
		got = append(got, evt)
	}

	var assistantText string
	var reasoning string
	var finals []*event.Event
	var toolEvents int
	var planEvents int
	var stateDeltas int
	for _, evt := range got {
		require.NotEqual(t, model.ObjectTypeRunnerCompletion, evt.Response.Object)
		if len(evt.StateDelta) > 0 {
			stateDeltas++
		}
		if evt.Response.IsPartial && len(evt.Response.Choices) > 0 {
			assistantText += evt.Response.Choices[0].Delta.Content
			reasoning += evt.Response.Choices[0].Delta.ReasoningContent
		}
		if _, ok := evt.Extensions[acpToolUpdateExtension]; ok {
			toolEvents++
		}
		if _, ok := evt.Extensions[acpPlanExtension]; ok {
			planEvents++
		}
		if evt.Response.Done && !evt.Response.IsPartial &&
			evt.Response.Object == model.ObjectTypeChatCompletion {
			finals = append(finals, evt)
		}
	}
	require.Len(t, finals, 1)
	require.Equal(t, "hello world", finals[0].Response.Choices[0].Message.Content)
	require.Equal(t, "thinking", finals[0].Response.Choices[0].Message.ReasoningContent)
	require.Equal(t, "hello world", assistantText)
	require.Equal(t, "thinking", reasoning)
	require.Equal(t, 3, finals[0].Response.Usage.PromptTokens)
	require.Equal(t, 5, finals[0].Response.Usage.CompletionTokens)
	require.Equal(t, 8, finals[0].Response.Usage.TotalTokens)
	require.Equal(t, 1, toolEvents)
	require.Equal(t, 1, planEvents)
	require.Equal(t, 1, stateDeltas)
}

func TestRunReturnsExplicitErrorOnSessionUpdateOverflow(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(
		t,
		"prompt_overflow",
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	invocation := newSessionInvocation(t, service, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "prompt-overflow",
	})
	invocation.Message = model.NewUserMessage("hello")

	events, err := acpAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	var overflowErr *event.Event
	for evt := range events {
		if evt.Error != nil && evt.Error.Message == errSessionUpdateOverflow.Error() {
			overflowErr = evt
		}
	}
	require.NotNil(t, overflowErr)
}
