//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestRunQueuedUserMessageConsumedTrackedInMessagesSnapshot(t *testing.T) {
	sessionService := inmemory.NewSessionService()
	queued := agentevent.NewResponseEvent("inv-1", "user", &model.Response{
		ID: "queued-user-1",
		Choices: []model.Choice{{
			Message: model.NewUserMessage("Only use the first three chapters"),
		}},
	})
	queued.ID = "event-queued-user-1"
	queued.RequestID = "request-1"
	require.NoError(t, agentevent.SetExtension(
		queued,
		steer.ExtensionKeyQueuedUserMessage,
		steer.QueuedUserMessageMetadata{
			Status: steer.QueuedUserMessageStatusConsumed,
		},
	))
	completion := agentevent.NewResponseEvent("inv-1", "agui.runner", &model.Response{
		ID:     "runner-completion-1",
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	})
	agentEvents := make(chan *agentevent.Event, 2)
	agentEvents <- queued
	agentEvents <- completion
	close(agentEvents)

	underlying := &fakeRunner{
		run: func(context.Context, string, string, model.Message, ...agent.RunOption) (<-chan *agentevent.Event, error) {
			return agentEvents, nil
		},
	}
	rr, ok := New(
		underlying,
		WithAppName("demo"),
		WithSessionService(sessionService),
	).(*runner)
	require.True(t, ok)

	eventsCh, err := rr.Run(context.Background(), &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "Analyze the report"}},
	})
	require.NoError(t, err)
	events := collectEvents(t, eventsCh)

	var consumed *aguievents.ActivitySnapshotEvent
	for _, evt := range events {
		activity, ok := evt.(*aguievents.ActivitySnapshotEvent)
		if ok && activity.ActivityType == "steer.consumed" {
			consumed = activity
			break
		}
	}
	require.NotNil(t, consumed)
	assert.Equal(t, "activity-steer-consumed-queued-user-1", consumed.MessageID)

	snapshotMessages := loadSnapshotMessages(t, rr)
	var foundQueuedUser bool
	for _, msg := range snapshotMessages {
		if msg.Role != types.RoleUser {
			continue
		}
		if toolSnapshotContentString(t, msg) == "Only use the first three chapters" {
			foundQueuedUser = true
			break
		}
	}
	require.True(t, foundQueuedUser)
}
