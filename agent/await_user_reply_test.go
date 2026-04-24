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
	require.Error(t, MarkAwaitingUserReply(nil))
	require.Error(t, MarkAwaitingUserReply(&Invocation{}))
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

func TestCurrentAwaitUserReplyRoute_InvalidRoute(t *testing.T) {
	inv := &Invocation{}
	inv.SetState(
		awaitUserReplyInvocationStateKey,
		AwaitUserReplyRoute{},
	)

	_, ok := CurrentAwaitUserReplyRoute(inv)
	require.False(t, ok)
}

func TestPendingAwaitUserReplyRoute_EdgeCases(t *testing.T) {
	t.Run("nil session", func(t *testing.T) {
		_, ok, err := PendingAwaitUserReplyRoute(nil)
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("missing state", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")

		_, ok, err := PendingAwaitUserReplyRoute(sess)
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("invalid normalized route", func(t *testing.T) {
		sess := session.NewSession(
			"app",
			"user",
			"sess",
			session.WithSessionState(session.StateMap{
				awaitUserReplySessionStateKey: []byte(
					`{"agent_name":" "}`,
				),
			}),
		)

		_, ok, err := PendingAwaitUserReplyRoute(sess)
		require.False(t, ok)
		require.Error(t, err)
	})
}

func TestSetAwaitUserReplyRootLookupName_ClearsState(t *testing.T) {
	inv := &Invocation{}

	SetAwaitUserReplyRootLookupName(inv, " coordinator ")
	rootName, ok := GetStateValue[string](
		inv,
		awaitUserReplyRootLookupStateKey,
	)
	require.True(t, ok)
	require.Equal(t, "coordinator", rootName)

	SetAwaitUserReplyRootLookupName(inv, " ")
	_, ok = GetStateValue[string](inv, awaitUserReplyRootLookupStateKey)
	require.False(t, ok)

	SetAwaitUserReplyRootLookupName(nil, "coordinator")
}

func TestClearAwaitUserReplyRouteState(t *testing.T) {
	state := ClearAwaitUserReplyRouteState()
	value, ok := state[awaitUserReplySessionStateKey]
	require.True(t, ok)
	require.Nil(t, value)
}

func TestAwaitUserReplyRoute_StateAndAttachEventErrors(t *testing.T) {
	_, err := (AwaitUserReplyRoute{}).State()
	require.Error(t, err)

	require.NoError(t, (AwaitUserReplyRoute{}).AttachEvent(nil))

	evt := event.NewResponseEvent(
		"inv-1",
		"clarifier",
		&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Please share your city.",
				},
			}},
		},
	)
	err = (AwaitUserReplyRoute{}).AttachEvent(evt)
	require.Error(t, err)
}

func TestAwaitUserReplyRoute_StateInfersAgentNameFromPath(t *testing.T) {
	state, err := (AwaitUserReplyRoute{
		LookupPath: "parent/clarifier",
	}).State()
	require.NoError(t, err)

	sess := session.NewSession(
		"app",
		"user",
		"sess",
		session.WithSessionState(state),
	)
	route, ok, routeErr := PendingAwaitUserReplyRoute(sess)
	require.NoError(t, routeErr)
	require.True(t, ok)
	require.Equal(t, "clarifier", route.AgentName)
}

func TestBuildAwaitUserReplyLookupPath(t *testing.T) {
	require.Empty(t, buildAwaitUserReplyLookupPath(nil))

	inv := &Invocation{}
	SetAwaitUserReplyRootLookupName(inv, "coordinator")
	require.Equal(
		t,
		"coordinator",
		buildAwaitUserReplyLookupPath(inv),
	)

	inv.AgentName = "clarifier"
	inv.Branch = " runtime-root / clarifier "
	require.Equal(
		t,
		"coordinator/clarifier",
		buildAwaitUserReplyLookupPath(inv),
	)
}

func TestAttachAwaitUserReplyRoute_WithoutPendingRoute(t *testing.T) {
	inv := &Invocation{AgentName: "clarifier"}
	evt := event.NewResponseEvent(
		"inv-1",
		"clarifier",
		&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Please share your city.",
				},
			}},
		},
	)

	attachAwaitUserReplyRoute(inv, evt)

	sess := session.NewSession("app", "user", "sess")
	sess.ApplyEventStateDelta(evt)
	_, ok, err := PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestAwaitUserReplyRoute_AttachEventPreservesStateDelta(t *testing.T) {
	evt := event.NewResponseEvent(
		"inv-1",
		"clarifier",
		&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Need your city.",
				},
			}},
		},
	)
	evt.StateDelta = session.StateMap{
		"existing": []byte("value"),
	}

	err := (AwaitUserReplyRoute{
		AgentName: "clarifier",
	}).AttachEvent(evt)
	require.NoError(t, err)
	require.Contains(t, evt.StateDelta, "existing")
	require.Contains(t, evt.StateDelta, awaitUserReplySessionStateKey)
}
