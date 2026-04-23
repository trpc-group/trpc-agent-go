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
		Branch:       "runtime-root/clarifier",
		InvocationID: "inv-1",
	}
	SetAwaitUserReplyRootLookupName(inv, "coordinator")
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
	require.Equal(t, "coordinator/clarifier", route.LookupPath)

	ext, ok, err := event.GetExtension[AwaitUserReplyRoute](
		evt,
		awaitUserReplyEventExtensionKey,
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "clarifier", ext.AgentName)
	require.Equal(t, "coordinator/clarifier", ext.LookupPath)
}

func TestAwaitUserReplyRoute_AttachEventNormalizesExtension(t *testing.T) {
	evt := event.NewResponseEvent(
		"inv-1",
		"clarifier",
		&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Need your phone number.",
				},
			}},
		},
	)

	err := (AwaitUserReplyRoute{
		AgentName:  " clarifier ",
		LookupPath: " coordinator / clarifier ",
	}).AttachEvent(evt)
	require.NoError(t, err)

	sess := session.NewSession("app", "user", "sess")
	sess.ApplyEventStateDelta(evt)

	route, ok, err := PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "clarifier", route.AgentName)
	require.Equal(t, "coordinator/clarifier", route.LookupPath)

	ext, ok, err := event.GetExtension[AwaitUserReplyRoute](
		evt,
		awaitUserReplyEventExtensionKey,
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "clarifier", ext.AgentName)
	require.Equal(t, "coordinator/clarifier", ext.LookupPath)
}

func TestMarkAwaitingUserReply_EmptyAgentName(t *testing.T) {
	err := MarkAwaitingUserReply(&Invocation{})
	require.Error(t, err)
}

func TestMarkAwaitingUserReply_UsesBranchPath(t *testing.T) {
	inv := &Invocation{
		AgentName: "clarifier",
		Branch:    "parent/clarifier",
	}

	require.NoError(t, MarkAwaitingUserReply(inv))

	route, ok := CurrentAwaitUserReplyRoute(inv)
	require.True(t, ok)
	require.Equal(t, "clarifier", route.AgentName)
	require.Equal(t, "parent/clarifier", route.LookupPath)
}

func TestMarkAwaitingUserReply_DoesNotAttachToPartialEvent(t *testing.T) {
	inv := &Invocation{
		AgentName:    "clarifier",
		InvocationID: "inv-partial",
	}
	require.NoError(t, MarkAwaitingUserReply(inv))

	ch := make(chan *event.Event, 1)
	err := EmitEvent(
		context.Background(),
		inv,
		ch,
		event.NewResponseEvent(
			"inv-partial",
			"clarifier",
			&model.Response{
				Done:      false,
				IsPartial: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "thinking...",
					},
				}},
			},
		),
	)
	require.NoError(t, err)
	close(ch)

	evt := <-ch
	sess := session.NewSession("app", "user", "sess")
	sess.ApplyEventStateDelta(evt)

	_, ok, err := PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = event.GetExtension[AwaitUserReplyRoute](
		evt,
		awaitUserReplyEventExtensionKey,
	)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestMarkAwaitingUserReply_DoesNotAttachToToolResult(t *testing.T) {
	inv := &Invocation{
		AgentName:    "clarifier",
		InvocationID: "inv-tool-result",
	}
	require.NoError(t, MarkAwaitingUserReply(inv))

	ch := make(chan *event.Event, 1)
	err := EmitEvent(
		context.Background(),
		inv,
		ch,
		event.NewResponseEvent(
			"inv-tool-result",
			"clarifier",
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  "call-1",
						Content: "ok",
					},
				}},
			},
		),
	)
	require.NoError(t, err)
	close(ch)

	evt := <-ch
	sess := session.NewSession("app", "user", "sess")
	sess.ApplyEventStateDelta(evt)

	_, ok, err := PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = event.GetExtension[AwaitUserReplyRoute](
		evt,
		awaitUserReplyEventExtensionKey,
	)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestPendingAwaitUserReplyRoute_InvalidState(t *testing.T) {
	sess := session.NewSession("app", "user", "sess")
	sess.SetState(awaitUserReplySessionStateKey, []byte("{"))

	_, ok, err := PendingAwaitUserReplyRoute(sess)
	require.False(t, ok)
	require.Error(t, err)
}
