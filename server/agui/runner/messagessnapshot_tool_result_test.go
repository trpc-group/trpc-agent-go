//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestMessagesSnapshotFollowsToolResultAfterLostStartup(t *testing.T) {
	for _, tt := range []struct {
		name   string
		legacy bool
	}{
		{name: "legacy writer", legacy: true},
		{name: "tagged writer after legacy history"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			svc := inmemory.NewSessionService()
			key := session.Key{AppName: "demo", UserID: "user", SessionID: "thread"}
			sess, err := svc.CreateSession(ctx, key, session.StateMap{})
			require.NoError(t, err)
			seed := []session.TrackEvent{
				newUserMessageTrackEvent(t, "user-1", "use the tool"),
				newTrackEvent(t, aguievents.NewRunStartedEvent("thread", "older")),
				newTrackEvent(t, aguievents.NewToolCallStartEvent("call-1", "calc", aguievents.WithParentMessageID("assistant-1"))),
				newTrackEvent(t, aguievents.NewToolCallArgsEvent("call-1", "{}")),
				newTrackEvent(t, aguievents.NewToolCallEndEvent("call-1")),
				newTrackEvent(t, aguievents.NewRunFinishedEvent("thread", "older")),
			}
			for i := range seed {
				require.NoError(t, svc.AppendTrackEvent(ctx, sess, &seed[i]))
			}

			agentEvents := make(chan *event.Event, 1)
			owner := New(
				&streamingWaitRunner{started: make(chan struct{}), events: agentEvents},
				WithAppName("demo"),
				WithSessionService(&toolResultHistoryService{Service: svc, TrackService: svc, legacy: tt.legacy}),
				WithFlushInterval(time.Hour),
			).(*runner)
			ownerStream, err := owner.Run(ctx, &adapter.RunAgentInput{
				ThreadID: "thread",
				RunID:    "newer",
				Messages: []types.Message{{
					ID: "tool-result", Role: types.RoleTool, Content: "result", Name: "calc", ToolCallID: "call-1",
				}},
			})
			require.NoError(t, err)
			defer func() {
				close(agentEvents)
				for range ownerStream {
				}
			}()
			require.IsType(t, (*aguievents.RunStartedEvent)(nil), nextAGUIEvent(t, ownerStream))
			require.IsType(t, (*aguievents.ToolCallResultEvent)(nil), nextAGUIEvent(t, ownerStream))
			require.NoError(t, owner.tracker.Flush(ctx, key))
			persisted, err := svc.GetTrackEvents(ctx, key, track.TrackAGUI)
			require.NoError(t, err)
			require.Len(t, persisted.Events, len(seed)+1)
			result := persisted.Events[len(seed)]
			stored, err := aguievents.EventFromJSON(result.Payload)
			require.NoError(t, err)
			require.IsType(t, (*aguievents.ToolCallResultEvent)(nil), stored)
			require.Equal(t, tt.legacy, track.HistoryRunID(result.Payload) == "")

			follower := New(
				noopBaseRunner{},
				WithAppName("demo"),
				WithSessionService(svc),
				WithFlushInterval(time.Millisecond),
				WithMessagesSnapshotFollowEnabled(true),
				WithMessagesSnapshotFollowMaxDuration(time.Second),
			).(*runner)
			stream, err := follower.MessagesSnapshot(ctx, &adapter.RunAgentInput{ThreadID: "thread", RunID: "snapshot"})
			require.NoError(t, err)
			require.IsType(t, (*aguievents.RunStartedEvent)(nil), nextAGUIEvent(t, stream))
			require.IsType(t, (*aguievents.MessagesSnapshotEvent)(nil), nextAGUIEvent(t, stream))

			agentEvents <- &event.Event{Response: &model.Response{
				ID: "answer", Object: model.ObjectTypeChatCompletionChunk, IsPartial: true,
				Choices: []model.Choice{{Delta: model.Message{Role: model.RoleAssistant, Content: "answer after tool"}}},
			}}
			waitForTextMessageContent(t, ownerStream)
			require.NoError(t, owner.tracker.Flush(ctx, key))
			require.IsType(t, (*aguievents.TextMessageStartEvent)(nil), nextAGUIEvent(t, stream),
				"history must forward the newer tool run's answer before finishing")
			content, ok := nextAGUIEvent(t, stream).(*aguievents.TextMessageContentEvent)
			require.True(t, ok)
			require.Equal(t, "answer after tool", content.Delta)
		})
	}
}

type toolResultHistoryService struct {
	session.Service
	session.TrackService
	legacy bool
	first  sync.Once
}

func (s *toolResultHistoryService) AppendTrackEvent(
	ctx context.Context, sess *session.Session, evt *session.TrackEvent, opts ...session.Option,
) error {
	var drop bool
	s.first.Do(func() { drop = true })
	if drop {
		return errors.New("simulated startup write failure")
	}
	if !s.legacy {
		return s.TrackService.AppendTrackEvent(ctx, sess, evt, opts...)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(evt.Payload, &fields); err != nil {
		return err
	}
	delete(fields, "trpcRunId")
	delete(fields, "trpcRunOrder")
	delete(fields, "trpcRunEpoch")
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	legacyEvent := *evt
	legacyEvent.Payload = payload
	return s.TrackService.AppendTrackEvent(ctx, sess, &legacyEvent, opts...)
}
