//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package source

import (
	"encoding/json"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/multimodal"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	testAuthor             = "member-a"
	testInvocationID       = "inv-1"
	testParentInvocationID = "parent-1"
	testBranch             = "root.member-a"
	testMessageID          = "msg-1"
	testParentMessageID    = "assistant-1"
	testToolCallID         = "call-1"
)

func TestMetadataIsZero(t *testing.T) {
	assert.True(t, Metadata{}.IsZero())
	assert.False(t, testMetadata("evt-1").IsZero())
}

func TestSnapshotMetadataIsZero(t *testing.T) {
	assert.True(t, SnapshotMetadata{}.IsZero())
	assert.False(t, SnapshotMetadata{
		Messages: map[string]Metadata{
			testMessageID: testMetadata("evt-1"),
		},
	}.IsZero())
}

func TestFromEventHandlesNilAndMissingOverride(t *testing.T) {
	metadata, ok := FromEvent(nil)
	assert.False(t, ok)
	assert.Equal(t, Metadata{}, metadata)

	ev := agentevent.NewResponseEvent(
		testInvocationID,
		testAuthor,
		&model.Response{},
	)
	ev.ID = "evt-1"
	ev.ParentInvocationID = testParentInvocationID
	ev.Branch = testBranch
	ev.Extensions = map[string]json.RawMessage{
		"other": json.RawMessage(`{"author":"ignored"}`),
	}

	metadata, ok = FromEvent(ev)
	require.True(t, ok)
	assert.Equal(t, testMetadata("evt-1"), metadata)
}

func TestSetEventOverrideHandlesNilEvent(t *testing.T) {
	assert.NoError(t, SetEventOverride(nil, testMetadata("evt-1")))
}

func TestFromEventUsesEventFields(t *testing.T) {
	metadata, ok := FromEvent(&agentevent.Event{
		ID:                 "evt-1",
		Author:             "member-a",
		InvocationID:       "inv-1",
		ParentInvocationID: "parent-1",
		Branch:             "root.member-a",
	})
	require.True(t, ok)
	assert.Equal(t, Metadata{
		EventID:            "evt-1",
		Author:             "member-a",
		InvocationID:       "inv-1",
		ParentInvocationID: "parent-1",
		Branch:             "root.member-a",
	}, metadata)
}

func TestFromEventOverrideSuppressesFallback(t *testing.T) {
	ev := agentevent.NewResponseEvent("inv-1", "agui.runner", &model.Response{})
	ev.ID = "evt-1"

	require.NoError(t, SetEventOverride(ev, Metadata{}))

	metadata, ok := FromEvent(ev)
	assert.False(t, ok)
	assert.Equal(t, Metadata{}, metadata)
}

func TestFromEventMalformedOverrideFallsBackToFields(t *testing.T) {
	ev := agentevent.NewResponseEvent("inv-1", "member-a", &model.Response{})
	ev.ID = "evt-1"
	ev.ParentInvocationID = "parent-1"
	ev.Branch = "root.member-a"
	ev.Extensions = map[string]json.RawMessage{
		ExtensionKey: json.RawMessage("{"),
	}

	metadata, ok := FromEvent(ev)
	require.True(t, ok)
	assert.Equal(t, Metadata{
		EventID:            "evt-1",
		Author:             "member-a",
		InvocationID:       "inv-1",
		ParentInvocationID: "parent-1",
		Branch:             "root.member-a",
	}, metadata)
}

func TestFromRawEventSupportsDirectMetadataValues(t *testing.T) {
	want := testMetadata("evt-1")

	metadata, ok := FromRawEvent(want)
	require.True(t, ok)
	assert.Equal(t, want, metadata)

	metadata, ok = FromRawEvent(&want)
	require.True(t, ok)
	assert.Equal(t, want, metadata)
}

func TestFromRawEventRejectsUnsupportedValues(t *testing.T) {
	metadata, ok := FromRawEvent((*Metadata)(nil))
	assert.False(t, ok)
	assert.Equal(t, Metadata{}, metadata)

	metadata, ok = FromRawEvent(map[string]any{
		"author": true,
	})
	assert.False(t, ok)
	assert.Equal(t, Metadata{}, metadata)

	metadata, ok = FromRawEvent("invalid")
	assert.False(t, ok)
	assert.Equal(t, Metadata{}, metadata)

	metadata, ok = FromRawEvent(func() {})
	assert.False(t, ok)
	assert.Equal(t, Metadata{}, metadata)
}

func TestFromRawEventSupportsMapPayload(t *testing.T) {
	timestamp := int64(1781258400000)
	metadata, ok := FromRawEvent(map[string]any{
		"eventId":            "evt-1",
		"author":             "member-a",
		"invocationId":       "inv-1",
		"parentInvocationId": "parent-1",
		"branch":             "root.member-a",
		"timestamp":          float64(timestamp),
	})
	require.True(t, ok)
	assert.Equal(t, Metadata{
		EventID:            "evt-1",
		Author:             "member-a",
		InvocationID:       "inv-1",
		ParentInvocationID: "parent-1",
		Branch:             "root.member-a",
		Timestamp:          &timestamp,
	}, metadata)
}

func TestRecordSnapshotMetadataIndexesSupportedEvents(t *testing.T) {
	metadata := testMetadata("evt-1")
	messageID := testMessageID
	role := "assistant"
	textDelta := "delta"
	reasoningDelta := "trace"

	stepStarted := aguievents.NewStepStartedEvent("step-1")
	stepFinished := aguievents.NewStepFinishedEvent("step-1")
	stateSnapshot := aguievents.NewStateSnapshotEvent(map[string]any{
		"foo": "bar",
	})
	stateDelta := aguievents.NewStateDeltaEvent(
		[]aguievents.JSONPatchOperation{{
			Op:    "add",
			Path:  "/foo",
			Value: "bar",
		}},
	)
	messagesSnapshot := aguievents.NewMessagesSnapshotEvent(
		[]aguievents.Message{{
			ID:   testMessageID,
			Role: aguitypes.RoleAssistant,
		}},
	)
	runStarted := aguievents.NewRunStartedEvent("thread", "run")
	runFinished := aguievents.NewRunFinishedEvent("thread", "run")
	runError := aguievents.NewRunErrorEvent("boom")
	customEvent := aguievents.NewCustomEvent("custom")
	rawEvent := aguievents.NewRawEvent(map[string]any{
		"foo": "bar",
	})

	tests := []struct {
		name          string
		event         aguievents.Event
		wantMessages  map[string]Metadata
		wantToolCalls map[string]Metadata
	}{
		{
			name: "text start",
			event: aguievents.NewTextMessageStartEvent(
				testMessageID,
			),
			wantMessages: map[string]Metadata{
				testMessageID: metadata,
			},
		},
		{
			name: "text content",
			event: aguievents.NewTextMessageContentEvent(
				testMessageID,
				textDelta,
			),
			wantMessages: map[string]Metadata{
				testMessageID: metadata,
			},
		},
		{
			name: "text end",
			event: aguievents.NewTextMessageEndEvent(
				testMessageID,
			),
			wantMessages: map[string]Metadata{
				testMessageID: metadata,
			},
		},
		{
			name: "text chunk",
			event: aguievents.NewTextMessageChunkEvent(
				&messageID,
				&role,
				&textDelta,
			),
			wantMessages: map[string]Metadata{
				testMessageID: metadata,
			},
		},
		{
			name: "reasoning start",
			event: aguievents.NewReasoningMessageStartEvent(
				testMessageID,
				role,
			),
			wantMessages: map[string]Metadata{
				testMessageID: metadata,
			},
		},
		{
			name: "reasoning content",
			event: aguievents.NewReasoningMessageContentEvent(
				testMessageID,
				reasoningDelta,
			),
			wantMessages: map[string]Metadata{
				testMessageID: metadata,
			},
		},
		{
			name: "reasoning end",
			event: aguievents.NewReasoningMessageEndEvent(
				testMessageID,
			),
			wantMessages: map[string]Metadata{
				testMessageID: metadata,
			},
		},
		{
			name: "reasoning chunk",
			event: aguievents.NewReasoningMessageChunkEvent(
				&messageID,
				&reasoningDelta,
			),
			wantMessages: map[string]Metadata{
				testMessageID: metadata,
			},
		},
		{
			name: "tool call start",
			event: aguievents.NewToolCallStartEvent(
				testToolCallID,
				"search",
				aguievents.WithParentMessageID(
					testParentMessageID,
				),
			),
			wantMessages: map[string]Metadata{
				testParentMessageID: metadata,
			},
			wantToolCalls: map[string]Metadata{
				testToolCallID: metadata,
			},
		},
		{
			name: "tool call args",
			event: aguievents.NewToolCallArgsEvent(
				testToolCallID,
				textDelta,
			),
			wantToolCalls: map[string]Metadata{
				testToolCallID: metadata,
			},
		},
		{
			name: "tool call end",
			event: aguievents.NewToolCallEndEvent(
				testToolCallID,
			),
			wantToolCalls: map[string]Metadata{
				testToolCallID: metadata,
			},
		},
		{
			name: "activity snapshot",
			event: aguievents.NewActivitySnapshotEvent(
				testMessageID,
				"status",
				map[string]any{"state": "running"},
			),
			wantMessages: map[string]Metadata{
				testMessageID: metadata,
			},
		},
		{
			name: "activity delta",
			event: aguievents.NewActivityDeltaEvent(
				testMessageID,
				"status",
				[]aguievents.JSONPatchOperation{{
					Op:    "replace",
					Path:  "/state",
					Value: "done",
				}},
			),
			wantMessages: map[string]Metadata{
				testMessageID: metadata,
			},
		},
		{
			name:  "step started",
			event: stepStarted,
			wantMessages: map[string]Metadata{
				stepStarted.ID(): metadata,
			},
		},
		{
			name:  "step finished",
			event: stepFinished,
			wantMessages: map[string]Metadata{
				stepFinished.ID(): metadata,
			},
		},
		{
			name:  "state snapshot",
			event: stateSnapshot,
			wantMessages: map[string]Metadata{
				stateSnapshot.ID(): metadata,
			},
		},
		{
			name:  "state delta",
			event: stateDelta,
			wantMessages: map[string]Metadata{
				stateDelta.ID(): metadata,
			},
		},
		{
			name:  "messages snapshot",
			event: messagesSnapshot,
			wantMessages: map[string]Metadata{
				messagesSnapshot.ID(): metadata,
			},
		},
		{
			name:  "run started",
			event: runStarted,
			wantMessages: map[string]Metadata{
				runStarted.ID(): metadata,
			},
		},
		{
			name:  "run finished",
			event: runFinished,
			wantMessages: map[string]Metadata{
				runFinished.ID(): metadata,
			},
		},
		{
			name:  "run error",
			event: runError,
			wantMessages: map[string]Metadata{
				runError.ID(): metadata,
			},
		},
		{
			name:  "custom event",
			event: customEvent,
			wantMessages: map[string]Metadata{
				customEvent.ID(): metadata,
			},
		},
		{
			name:  "raw event",
			event: rawEvent,
			wantMessages: map[string]Metadata{
				rawEvent.ID(): metadata,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := make(map[string]Metadata)
			toolCalls := make(map[string]Metadata)

			recordSnapshotMetadata(
				messages,
				toolCalls,
				tt.event,
				metadata,
			)

			if tt.wantMessages == nil {
				assert.Empty(t, messages)
			} else {
				assert.Equal(t, tt.wantMessages, messages)
			}
			if tt.wantToolCalls == nil {
				assert.Empty(t, toolCalls)
			} else {
				assert.Equal(t, tt.wantToolCalls, toolCalls)
			}
		})
	}
}

func TestRecordSnapshotMetadataSkipsChunkEventsWithoutMessageID(
	t *testing.T,
) {
	metadata := testMetadata("evt-1")
	messageID := ""
	role := "assistant"
	textDelta := "delta"
	reasoningDelta := "trace"

	tests := []aguievents.Event{
		aguievents.NewTextMessageChunkEvent(
			&messageID,
			&role,
			&textDelta,
		),
		aguievents.NewReasoningMessageChunkEvent(
			&messageID,
			&reasoningDelta,
		),
		aguievents.NewToolCallStartEvent(
			testToolCallID,
			"search",
		),
	}

	for _, event := range tests {
		messages := make(map[string]Metadata)
		toolCalls := make(map[string]Metadata)

		recordSnapshotMetadata(
			messages,
			toolCalls,
			event,
			metadata,
		)

		assert.Empty(t, messages)
		if _, ok := event.(*aguievents.ToolCallStartEvent); ok {
			assert.Equal(t, map[string]Metadata{
				testToolCallID: metadata,
			}, toolCalls)
			continue
		}
		assert.Empty(t, toolCalls)
	}
}

func TestBuildSnapshotMetadataIndexesMessagesAndToolCalls(t *testing.T) {
	assistantTime := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	toolTime := assistantTime.Add(time.Second)
	assistantMetadata := Metadata{
		EventID:      "evt-tool-call",
		Author:       "member-a",
		InvocationID: "inv-assistant",
		Branch:       "root.member-a",
	}
	wantAssistantMetadata := assistantMetadata
	assistantTimestamp := assistantTime.UnixMilli()
	wantAssistantMetadata.Timestamp = &assistantTimestamp
	toolMetadata := Metadata{
		EventID:      "evt-tool-result",
		Author:       "member-a",
		InvocationID: "inv-tool",
		Branch:       "root.member-a",
	}
	wantToolMetadata := toolMetadata
	toolTimestamp := toolTime.UnixMilli()
	wantToolMetadata.Timestamp = &toolTimestamp

	trackEvents := []session.TrackEvent{
		newTrackEvent(t, withRawEvent(
			withTimestamp(aguievents.NewToolCallStartEvent(
				"call-1",
				"search",
				aguievents.WithParentMessageID("assistant-1"),
			), assistantTime),
			assistantMetadata,
		)),
		newTrackEvent(t, withRawEvent(
			withTimestamp(aguievents.NewToolCallResultEvent(
				"tool-msg-1",
				"call-1",
				"done",
			), toolTime),
			toolMetadata,
		)),
	}

	metadata := BuildSnapshotMetadata(trackEvents)
	assert.Equal(t, SnapshotMetadata{
		Messages: map[string]Metadata{
			"assistant-1": wantAssistantMetadata,
			"tool-msg-1":  wantToolMetadata,
		},
		ToolCalls: map[string]Metadata{
			"call-1": wantAssistantMetadata,
		},
	}, metadata)
}

func TestBuildSnapshotMetadataIgnoresInvalidEntries(t *testing.T) {
	runFinished := aguievents.NewRunFinishedEvent("thread", "run")
	runFinished.GetBaseEvent().TimestampMs = nil
	metadata := BuildSnapshotMetadata([]session.TrackEvent{
		{},
		{Payload: []byte("{")},
		newTrackEvent(t, runFinished),
	})
	assert.True(t, metadata.IsZero())
}

func TestBuildSnapshotMetadataFallsBackToToolResultSource(t *testing.T) {
	timestampTime := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	toolMetadata := Metadata{
		EventID:      "evt-tool-result",
		Author:       "member-a",
		InvocationID: "inv-tool",
		Branch:       "root.member-a",
	}
	wantToolMetadata := toolMetadata
	timestamp := timestampTime.UnixMilli()
	wantToolMetadata.Timestamp = &timestamp

	trackEvents := []session.TrackEvent{
		newTrackEvent(t, withRawEvent(
			withTimestamp(aguievents.NewToolCallResultEvent(
				"tool-msg-1",
				"call-1",
				"done",
			), timestampTime),
			toolMetadata,
		)),
	}

	metadata := BuildSnapshotMetadata(trackEvents)
	assert.Equal(t, SnapshotMetadata{
		Messages: map[string]Metadata{
			"tool-msg-1": wantToolMetadata,
		},
		ToolCalls: map[string]Metadata{
			"call-1": wantToolMetadata,
		},
	}, metadata)
}

func TestBuildSnapshotMetadataUsesEventTimestamps(t *testing.T) {
	startedAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	updatedAt := startedAt.Add(2 * time.Second)
	toolStartedAt := startedAt.Add(3 * time.Second)
	trackEvents := []session.TrackEvent{
		newTrackEventAt(t, withTimestamp(aguievents.NewTextMessageStartEvent(
			"assistant-1",
			aguievents.WithRole("assistant"),
		), startedAt), startedAt.Add(time.Hour)),
		newTrackEventAt(t, withTimestamp(aguievents.NewTextMessageContentEvent(
			"assistant-1",
			"hello",
		), startedAt.Add(time.Second)), startedAt.Add(time.Hour)),
		newTrackEventAt(t, withTimestamp(aguievents.NewTextMessageEndEvent(
			"assistant-1",
		), updatedAt), startedAt.Add(time.Hour)),
		newTrackEventAt(t, withTimestamp(aguievents.NewToolCallStartEvent(
			"call-1",
			"search",
			aguievents.WithParentMessageID("assistant-1"),
		), toolStartedAt), startedAt.Add(time.Hour)),
	}
	metadata := BuildSnapshotMetadata(trackEvents)
	require.Contains(t, metadata.Messages, "assistant-1")
	assistant := metadata.Messages["assistant-1"]
	require.NotNil(t, assistant.Timestamp)
	assert.Equal(t, startedAt.UnixMilli(), *assistant.Timestamp)
	require.Contains(t, metadata.ToolCalls, "call-1")
	toolCall := metadata.ToolCalls["call-1"]
	require.NotNil(t, toolCall.Timestamp)
	assert.Equal(t, toolStartedAt.UnixMilli(), *toolCall.Timestamp)
}

func TestBuildSnapshotMetadataFallsBackToTrackEventTimestamp(t *testing.T) {
	timestampTime := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	event := aguievents.NewTextMessageStartEvent(
		"assistant-1",
		aguievents.WithRole("assistant"),
	)
	event.GetBaseEvent().TimestampMs = nil
	metadata := BuildSnapshotMetadata([]session.TrackEvent{
		newTrackEventAt(t, event, timestampTime),
	})
	require.Contains(t, metadata.Messages, "assistant-1")
	got := metadata.Messages["assistant-1"]
	require.NotNil(t, got.Timestamp)
	assert.Equal(t, timestampTime.UnixMilli(), *got.Timestamp)
}

func TestBuildSnapshotMetadataIndexesCustomUserMessageID(t *testing.T) {
	timestampTime := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	userMessage := aguitypes.Message{
		ID:      "user-1",
		Role:    aguitypes.RoleUser,
		Content: "hello",
	}
	userEvent := aguievents.NewCustomEvent(
		multimodal.CustomEventNameUserMessage,
		aguievents.WithValue(userMessage),
	)
	userEvent.GetBaseEvent().SetTimestamp(timestampTime.UnixMilli())
	metadata := BuildSnapshotMetadata([]session.TrackEvent{
		newTrackEventAt(t, aguievents.NewCustomEvent(
			"ignored",
		), timestampTime.Add(time.Hour)),
		newTrackEventAt(t, userEvent, timestampTime.Add(time.Hour)),
	})
	require.Contains(t, metadata.Messages, "user-1")
	require.NotContains(t, metadata.Messages, userEvent.ID())
	got := metadata.Messages["user-1"]
	require.NotNil(t, got.Timestamp)
	assert.Equal(t, timestampTime.UnixMilli(), *got.Timestamp)
}

func testMetadata(eventID string) Metadata {
	return Metadata{
		EventID:            eventID,
		Author:             testAuthor,
		InvocationID:       testInvocationID,
		ParentInvocationID: testParentInvocationID,
		Branch:             testBranch,
	}
}

func newTrackEvent(t *testing.T, event aguievents.Event) session.TrackEvent {
	t.Helper()

	payload, err := json.Marshal(event)
	require.NoError(t, err)
	return session.TrackEvent{Payload: payload}
}

func newTrackEventAt(
	t *testing.T,
	event aguievents.Event,
	timestamp time.Time,
) session.TrackEvent {
	t.Helper()
	trackEvent := newTrackEvent(t, event)
	trackEvent.Timestamp = timestamp
	return trackEvent
}

func withTimestamp(event aguievents.Event, timestamp time.Time) aguievents.Event {
	event.GetBaseEvent().SetTimestamp(timestamp.UnixMilli())
	return event
}

func withRawEvent(
	event aguievents.Event,
	metadata Metadata,
) aguievents.Event {
	event.GetBaseEvent().RawEvent = metadata
	return event
}
