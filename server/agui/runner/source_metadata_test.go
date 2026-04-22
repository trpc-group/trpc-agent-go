//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"encoding/json"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/source"
	tracksvc "trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestToolCallSourceMetadataMatchesTrackedToolCall(t *testing.T) {
	metadata := source.Metadata{
		EventID:      "evt-tool-call",
		Author:       "member-a",
		InvocationID: "inv-1",
		Branch:       "root.member-a",
	}
	payload, err := json.Marshal(withTrackedRawEvent(
		aguievents.NewToolCallStartEvent(
			"call-1",
			"search",
			aguievents.WithParentMessageID("assistant-1"),
		),
		metadata,
	))
	require.NoError(t, err)

	got, ok := toolCallSourceMetadata(payload, "call-1")
	require.True(t, ok)
	assert.Equal(t, metadata, got)
}

func TestToolCallSourceMetadataRejectsInvalidPayloads(t *testing.T) {
	_, ok := toolCallSourceMetadata(nil, "call-1")
	assert.False(t, ok)

	_, ok = toolCallSourceMetadata([]byte("{"), "call-1")
	assert.False(t, ok)

	payload, err := json.Marshal(withTrackedRawEvent(
		aguievents.NewRunFinishedEvent("thread", "run"),
		source.Metadata{Author: "member-a"},
	))
	require.NoError(t, err)

	_, ok = toolCallSourceMetadata(payload, "call-1")
	assert.False(t, ok)
}

func TestRunToolResultInputTranslationReusesTrackedSourceMetadata(
	t *testing.T,
) {
	ctx := context.Background()
	svc := inmemory.NewSessionService()
	tracker, err := tracksvc.New(svc)
	require.NoError(t, err)
	key := session.Key{
		AppName:   "demo",
		UserID:    "user",
		SessionID: "thread",
	}
	want := source.Metadata{
		EventID:      "evt-tool-call",
		Author:       "member-a",
		InvocationID: "inv-1",
		Branch:       "root.member-a",
	}
	require.NoError(t, tracker.AppendEvent(ctx, key, withTrackedRawEvent(
		aguievents.NewToolCallStartEvent(
			"call-1",
			"calculator",
			aguievents.WithParentMessageID("assistant-1"),
		),
		want,
	)))

	rr, ok := New(
		&fakeRunner{
			run: func(context.Context, string, string, model.Message,
				...agent.RunOption) (<-chan *agentevent.Event, error) {
				ch := make(chan *agentevent.Event)
				close(ch)
				return ch, nil
			},
		},
		WithAppName("demo"),
		WithSessionService(svc),
		WithEventSourceMetadataEnabled(true),
		WithToolResultInputTranslationEnabled(true),
	).(*runner)
	require.True(t, ok)

	eventsCh, err := rr.Run(ctx, &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{
			ID:         "tool-msg-1",
			Role:       types.RoleTool,
			Content:    "done",
			Name:       "calculator",
			ToolCallID: "call-1",
		}},
	})
	require.NoError(t, err)

	events := collectEvents(t, eventsCh)
	require.Len(t, events, 2)
	result, ok := events[1].(*aguievents.ToolCallResultEvent)
	require.True(t, ok)
	got, ok := result.GetBaseEvent().RawEvent.(source.Metadata)
	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestRunToolResultInputTranslationSuppressesSyntheticSourceMetadata(
	t *testing.T,
) {
	rr, ok := New(
		&fakeRunner{
			run: func(context.Context, string, string, model.Message,
				...agent.RunOption) (<-chan *agentevent.Event, error) {
				ch := make(chan *agentevent.Event)
				close(ch)
				return ch, nil
			},
		},
		WithAppName("demo"),
		WithEventSourceMetadataEnabled(true),
		WithToolResultInputTranslationEnabled(true),
	).(*runner)
	require.True(t, ok)

	eventsCh, err := rr.Run(context.Background(), &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{
			ID:         "tool-msg-1",
			Role:       types.RoleTool,
			Content:    "done",
			Name:       "calculator",
			ToolCallID: "call-1",
		}},
	})
	require.NoError(t, err)

	events := collectEvents(t, eventsCh)
	require.Len(t, events, 2)
	result, ok := events[1].(*aguievents.ToolCallResultEvent)
	require.True(t, ok)
	assert.Nil(t, result.GetBaseEvent().RawEvent)
}

func withTrackedRawEvent(
	event aguievents.Event,
	metadata source.Metadata,
) aguievents.Event {
	event.GetBaseEvent().RawEvent = metadata
	return event
}
