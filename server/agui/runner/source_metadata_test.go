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
	"errors"
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

const (
	runnerAppName   = "demo"
	runnerThreadID  = "thread"
	runnerUserID    = "user"
	runnerToolCall  = "call-1"
	runnerToolName  = "calculator"
	runnerMessageID = "tool-msg-1"
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

func TestToolCallSourceMetadataRejectsMissingRawEventAndMismatches(
	t *testing.T,
) {
	payload, err := json.Marshal(aguievents.NewToolCallStartEvent(
		runnerToolCall,
		runnerToolName,
	))
	require.NoError(t, err)

	_, ok := toolCallSourceMetadata(payload, runnerToolCall)
	assert.False(t, ok)

	payload, err = json.Marshal(withTrackedRawEvent(
		aguievents.NewToolCallEndEvent(runnerToolCall),
		source.Metadata{Author: "member-a"},
	))
	require.NoError(t, err)

	_, ok = toolCallSourceMetadata(payload, "other")
	assert.False(t, ok)
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

func TestMatchesToolCallID(t *testing.T) {
	tests := []struct {
		name   string
		event  aguievents.Event
		want   bool
		target string
	}{
		{
			name: "tool call start",
			event: aguievents.NewToolCallStartEvent(
				runnerToolCall,
				runnerToolName,
			),
			target: runnerToolCall,
			want:   true,
		},
		{
			name: "tool call args",
			event: aguievents.NewToolCallArgsEvent(
				runnerToolCall,
				"{}",
			),
			target: runnerToolCall,
			want:   true,
		},
		{
			name: "tool call end",
			event: aguievents.NewToolCallEndEvent(
				runnerToolCall,
			),
			target: runnerToolCall,
			want:   true,
		},
		{
			name: "tool call result",
			event: aguievents.NewToolCallResultEvent(
				runnerMessageID,
				runnerToolCall,
				"done",
			),
			target: runnerToolCall,
			want:   true,
		},
		{
			name: "non tool event",
			event: aguievents.NewRunStartedEvent(
				runnerThreadID,
				"run",
			),
			target: runnerToolCall,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(
				t,
				tt.want,
				matchesToolCallID(tt.event, tt.target),
			)
		})
	}
}

func TestLookupToolCallSourceMetadataGuardClauses(t *testing.T) {
	ctx := context.Background()
	key := session.Key{
		AppName:   runnerAppName,
		UserID:    runnerUserID,
		SessionID: runnerThreadID,
	}

	var nilRunner *runner
	metadata, ok, err := nilRunner.lookupToolCallSourceMetadata(
		ctx,
		key,
		runnerToolCall,
	)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, source.Metadata{}, metadata)

	rr := &runner{}
	metadata, ok, err = rr.lookupToolCallSourceMetadata(
		ctx,
		key,
		runnerToolCall,
	)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, source.Metadata{}, metadata)

	rr.tracker = &staticTrackEventsTracker{}
	metadata, ok, err = rr.lookupToolCallSourceMetadata(
		ctx,
		key,
		"",
	)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, source.Metadata{}, metadata)
}

func TestLookupToolCallSourceMetadataHandlesTrackerResponses(t *testing.T) {
	ctx := context.Background()
	key := session.Key{
		AppName:   runnerAppName,
		UserID:    runnerUserID,
		SessionID: runnerThreadID,
	}
	want := source.Metadata{
		EventID:      "evt-new",
		Author:       "member-a",
		InvocationID: "inv-2",
		Branch:       "root.member-a",
	}
	older := source.Metadata{
		EventID:      "evt-old",
		Author:       "member-a",
		InvocationID: "inv-1",
		Branch:       "root.member-a",
	}

	rr := &runner{
		tracker: &staticTrackEventsTracker{
			events: &session.TrackEvents{Events: []session.TrackEvent{
				newTrackedTrackEvent(t, withTrackedRawEvent(
					aguievents.NewToolCallStartEvent(
						runnerToolCall,
						runnerToolName,
					),
					older,
				)),
				newTrackedTrackEvent(t, withTrackedRawEvent(
					aguievents.NewToolCallArgsEvent(
						"other",
						"{}",
					),
					source.Metadata{Author: "ignored"},
				)),
				newTrackedTrackEvent(t, withTrackedRawEvent(
					aguievents.NewToolCallEndEvent(
						runnerToolCall,
					),
					want,
				)),
			}},
		},
	}

	got, ok, err := rr.lookupToolCallSourceMetadata(
		ctx,
		key,
		runnerToolCall,
	)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestLookupToolCallSourceMetadataHandlesMissAndError(t *testing.T) {
	ctx := context.Background()
	key := session.Key{
		AppName:   runnerAppName,
		UserID:    runnerUserID,
		SessionID: runnerThreadID,
	}

	rr := &runner{tracker: &staticTrackEventsTracker{}}
	metadata, ok, err := rr.lookupToolCallSourceMetadata(
		ctx,
		key,
		runnerToolCall,
	)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, source.Metadata{}, metadata)

	rr.tracker = &staticTrackEventsTracker{
		events: &session.TrackEvents{},
	}
	metadata, ok, err = rr.lookupToolCallSourceMetadata(
		ctx,
		key,
		runnerToolCall,
	)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, source.Metadata{}, metadata)

	rr.tracker = &getEventsErrorTracker{
		err: errors.New("boom"),
	}
	metadata, ok, err = rr.lookupToolCallSourceMetadata(
		ctx,
		key,
		runnerToolCall,
	)
	require.Error(t, err)
	assert.False(t, ok)
	assert.Equal(t, source.Metadata{}, metadata)
}

func TestAttachToolResultInputSourceMetadata(t *testing.T) {
	ctx := context.Background()
	key := session.Key{
		AppName:   runnerAppName,
		UserID:    runnerUserID,
		SessionID: runnerThreadID,
	}

	t.Run("disabled runner leaves event untouched", func(t *testing.T) {
		ev := agentevent.NewResponseEvent(
			"inv-1",
			toolResultInputEventAuthor,
			&model.Response{},
		)
		rr := &runner{}

		rr.attachToolResultInputSourceMetadata(
			ctx,
			key,
			ev,
			runnerToolCall,
		)

		assert.Nil(t, ev.Extensions)
	})

	t.Run("missing source stores zero override", func(t *testing.T) {
		ev := agentevent.NewResponseEvent(
			"inv-1",
			toolResultInputEventAuthor,
			&model.Response{},
		)
		rr := &runner{
			eventSourceMetadataEnabled: true,
			tracker:                    &staticTrackEventsTracker{},
		}

		rr.attachToolResultInputSourceMetadata(
			ctx,
			key,
			ev,
			runnerToolCall,
		)

		metadata, ok := source.FromEvent(ev)
		assert.False(t, ok)
		assert.Equal(t, source.Metadata{}, metadata)
	})

	t.Run("lookup hit reuses tracked metadata", func(t *testing.T) {
		want := source.Metadata{
			EventID:      "evt-tool-call",
			Author:       "member-a",
			InvocationID: "inv-1",
			Branch:       "root.member-a",
		}
		ev := agentevent.NewResponseEvent(
			"inv-1",
			toolResultInputEventAuthor,
			&model.Response{},
		)
		rr := &runner{
			eventSourceMetadataEnabled: true,
			tracker: &staticTrackEventsTracker{
				events: &session.TrackEvents{
					Events: []session.TrackEvent{
						newTrackedTrackEvent(t, withTrackedRawEvent(
							aguievents.NewToolCallStartEvent(
								runnerToolCall,
								runnerToolName,
							),
							want,
						)),
					},
				},
			},
		}

		rr.attachToolResultInputSourceMetadata(
			ctx,
			key,
			ev,
			runnerToolCall,
		)

		metadata, ok := source.FromEvent(ev)
		require.True(t, ok)
		assert.Equal(t, want, metadata)
	})

	t.Run("lookup error still suppresses fallback", func(t *testing.T) {
		ev := agentevent.NewResponseEvent(
			"inv-1",
			toolResultInputEventAuthor,
			&model.Response{},
		)
		rr := &runner{
			eventSourceMetadataEnabled: true,
			tracker: &getEventsErrorTracker{
				err: errors.New("boom"),
			},
		}

		rr.attachToolResultInputSourceMetadata(
			ctx,
			key,
			ev,
			runnerToolCall,
		)

		metadata, ok := source.FromEvent(ev)
		assert.False(t, ok)
		assert.Equal(t, source.Metadata{}, metadata)
	})
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

type staticTrackEventsTracker struct {
	events *session.TrackEvents
}

func (s *staticTrackEventsTracker) AppendEvent(
	context.Context,
	session.Key,
	aguievents.Event,
) error {
	return nil
}

func (s *staticTrackEventsTracker) GetEvents(
	context.Context,
	session.Key,
	...session.Option,
) (*session.TrackEvents, error) {
	return s.events, nil
}

func (s *staticTrackEventsTracker) Flush(
	context.Context,
	session.Key,
) error {
	return nil
}

type getEventsErrorTracker struct {
	err error
}

func (g *getEventsErrorTracker) AppendEvent(
	context.Context,
	session.Key,
	aguievents.Event,
) error {
	return nil
}

func (g *getEventsErrorTracker) GetEvents(
	context.Context,
	session.Key,
	...session.Option,
) (*session.TrackEvents, error) {
	return nil, g.err
}

func (g *getEventsErrorTracker) Flush(
	context.Context,
	session.Key,
) error {
	return nil
}

func newTrackedTrackEvent(
	t *testing.T,
	event aguievents.Event,
) session.TrackEvent {
	t.Helper()

	payload, err := json.Marshal(event)
	require.NoError(t, err)
	return session.TrackEvent{Payload: payload}
}

func withTrackedRawEvent(
	event aguievents.Event,
	metadata source.Metadata,
) aguievents.Event {
	event.GetBaseEvent().RawEvent = metadata
	return event
}
