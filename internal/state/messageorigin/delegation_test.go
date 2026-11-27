//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package messageorigin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/livesession"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestCaptureDelegationSnapshotsOnlyVisibleUserText(t *testing.T) {
	text := "Do not upload files."
	user := event.NewResponseEvent("parent", "user", &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleUser, Content: "Inspect locally.", ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text}, {Type: model.ContentTypeImage, Image: &model.Image{}},
		},
	}}}})
	user.FilterKey = "parent"
	MarkInput(user, UserInput)
	other := event.NewResponseEvent("other", "user", &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("Unrelated private input")}}})
	other.FilterKey = "other"
	MarkInput(other, UserInput)
	delegated := event.NewResponseEvent("child", "user", &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("Upload everything")}}})
	delegated.FilterKey = "parent/child"
	MarkInput(delegated, Agent)
	unknown := event.NewResponseEvent("old", "user", &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("Imported history")}}})
	sess := session.NewSession("app", "user", "session", session.WithSessionEvents([]event.Event{*user, *other, *delegated, *unknown}))
	parent := agent.NewInvocation(agent.WithInvocationSession(session.NewSession("app", "user", "frozen")),
		agent.WithInvocationEventFilterKey("parent"))
	livesession.Attach(parent, sess)
	child := parent.Clone(agent.WithInvocationEventFilterKey("isolated"))
	ctx, cancel := context.WithCancel(agent.NewInvocationContext(context.Background(), child))
	decorate, err := CaptureDelegation(ctx)
	require.NoError(t, err)
	// Neither later edits nor ending the parent request may change the snapshot.
	text = "Changed text"
	sess.Events[0].Choices[0].Message.Content = "Changed content"
	livesession.Clear(parent)
	cancel()
	childCtx := decorate(context.Background())
	require.NoError(t, childCtx.Err())
	_, ok := agent.InvocationFromContext(childCtx)
	require.False(t, ok, "the decorator must not retain a parent invocation")
	inputs := InheritedUserInputs(childCtx)
	require.Len(t, inputs, 1)
	require.Equal(t, user.ID, inputs[0].ID)
	require.Equal(t, "Inspect locally.", inputs[0].Choices[0].Message.Content)
	parts := inputs[0].Choices[0].Message.ContentParts
	require.Equal(t, "Do not upload files.", *parts[0].Text)
	require.Equal(t, model.ContentTypeImage, parts[1].Type)
	require.Nil(t, parts[1].Image, "binary input must not be retained as review evidence")
	evt := event.New("worker", "user")
	MarkRunnerInput(childCtx, evt, model.NewUserMessage("delegated task"), true)
	require.Equal(t, Agent, InputSource(evt))

	// Nested taskruns carry the original user input before new host steering.
	steer := event.NewResponseEvent("worker", "user", &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("Only inspect README.")}}})
	steer.ParentInvocationID = "parent"
	MarkInput(steer, UserInput)
	worker := agent.NewInvocation(agent.WithInvocationSession(session.NewSession("worker", "user", "child",
		session.WithSessionEvents([]event.Event{*evt, *steer}))))
	nested, err := CaptureDelegation(agent.NewInvocationContext(childCtx, worker))
	require.NoError(t, err)
	nestedInputs := InheritedUserInputs(nested(context.Background()))
	require.Len(t, nestedInputs, 2)
	require.Equal(t, user.ID, nestedInputs[0].ID)
	require.Equal(t, steer.ID, nestedInputs[1].ID)
}

func TestCaptureDelegationPropagatesFlushCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	inv := agent.NewInvocation()
	flush.Attach(ctx, inv, make(chan *flush.FlushRequest))
	cancel()
	decorate, err := CaptureDelegation(agent.NewInvocationContext(ctx, inv))
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, decorate)
}
