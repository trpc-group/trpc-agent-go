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
	"encoding/base64"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/steerext"
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
		steerext.QueuedUserMessageExtensionKey,
		steerext.QueuedUserMessageMetadata{
			Status: steerext.QueuedUserMessageStatusConsumed,
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

func TestRunQueuedUserMessageContentPartsTrackedInMessagesSnapshot(t *testing.T) {
	sessionService := inmemory.NewSessionService()
	text := "图中有哪些信息?"
	queued := agentevent.NewResponseEvent("inv-1", "user", &model.Response{
		ID: "queued-user-multimodal",
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{
					{
						Type: model.ContentTypeImage,
						Image: &model.Image{
							URL:    "https://example.com/images/1.jpeg",
							Format: "image/jpeg",
						},
					},
					{
						Type: model.ContentTypeFile,
						File: &model.File{
							Name:     "report.txt",
							Data:     []byte("report"),
							MimeType: "text/plain",
						},
					},
					{
						Type: model.ContentTypeText,
						Text: &text,
					},
				},
			},
		}},
	})
	require.NoError(t, agentevent.SetExtension(
		queued,
		steerext.QueuedUserMessageExtensionKey,
		steerext.QueuedUserMessageMetadata{
			Status: steerext.QueuedUserMessageStatusConsumed,
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
		Messages: []types.Message{{Role: types.RoleUser, Content: "Analyze the image"}},
	})
	require.NoError(t, err)
	collectEvents(t, eventsCh)

	snapshotMessages := loadSnapshotMessages(t, rr)
	var foundQueuedUser bool
	for _, msg := range snapshotMessages {
		if msg.ID != "queued-user-multimodal" || msg.Role != types.RoleUser {
			continue
		}
		contents, ok := msg.ContentInputContents()
		require.True(t, ok)
		require.Len(t, contents, 3)
		assert.Equal(t, types.InputContentTypeBinary, contents[0].Type)
		assert.Equal(t, "image/jpeg", contents[0].MimeType)
		assert.Equal(t, "https://example.com/images/1.jpeg", contents[0].URL)
		assert.Equal(t, types.InputContentTypeBinary, contents[1].Type)
		assert.Equal(t, "text/plain", contents[1].MimeType)
		assert.Equal(t, "report.txt", contents[1].Filename)
		assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("report")), contents[1].Data)
		assert.Equal(t, types.InputContentTypeText, contents[2].Type)
		assert.Equal(t, text, contents[2].Text)
		foundQueuedUser = true
		break
	}
	require.True(t, foundQueuedUser)
}
