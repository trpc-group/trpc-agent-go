//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestMarkAwaitingUserReply_AttachesToFinalEvent(t *testing.T) {
	inv := &Invocation{
		AgentName:    "clarifier",
		InvocationID: "inv-1",
	}
	require.NoError(t, MarkAwaitingUserReply(inv))

	ch := make(chan *event.Event, 1)
	err := EmitEvent(
		context.Background(),
		inv,
		ch,
		event.NewResponseEvent(
			"inv-1",
			"clarifier",
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "Please provide your account id.",
					},
				}},
			},
		),
	)
	require.NoError(t, err)
	close(ch)

	evt := <-ch
	require.NotNil(t, evt)

	sess := session.NewSession("app", "user", "sess")
	sess.ApplyEventStateDelta(evt)
	route, ok, err := PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "clarifier", route.AgentName)

	ext, ok, err := event.GetExtension[AwaitUserReplyRoute](
		evt,
		awaitUserReplyEventExtensionKey,
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "clarifier", ext.AgentName)
}

func TestMarkAwaitingUserReply_EmptyAgentName(t *testing.T) {
	err := MarkAwaitingUserReply(&Invocation{})
	require.Error(t, err)
}

func TestPendingAwaitUserReplyRoute_InvalidState(t *testing.T) {
	sess := session.NewSession("app", "user", "sess")
	sess.SetState(awaitUserReplySessionStateKey, []byte("{"))

	_, ok, err := PendingAwaitUserReplyRoute(sess)
	require.False(t, ok)
	require.Error(t, err)
}
