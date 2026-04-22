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

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

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

func TestFromRawEventSupportsMapPayload(t *testing.T) {
	metadata, ok := FromRawEvent(map[string]any{
		"eventId":            "evt-1",
		"author":             "member-a",
		"invocationId":       "inv-1",
		"parentInvocationId": "parent-1",
		"branch":             "root.member-a",
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

func TestBuildSnapshotMetadataIndexesMessagesAndToolCalls(t *testing.T) {
	assistantMetadata := Metadata{
		EventID:      "evt-tool-call",
		Author:       "member-a",
		InvocationID: "inv-assistant",
		Branch:       "root.member-a",
	}
	toolMetadata := Metadata{
		EventID:      "evt-tool-result",
		Author:       "member-a",
		InvocationID: "inv-tool",
		Branch:       "root.member-a",
	}

	trackEvents := []session.TrackEvent{
		newTrackEvent(t, withRawEvent(
			aguievents.NewToolCallStartEvent(
				"call-1",
				"search",
				aguievents.WithParentMessageID("assistant-1"),
			),
			assistantMetadata,
		)),
		newTrackEvent(t, withRawEvent(
			aguievents.NewToolCallResultEvent(
				"tool-msg-1",
				"call-1",
				"done",
			),
			toolMetadata,
		)),
	}

	metadata := BuildSnapshotMetadata(trackEvents)
	assert.Equal(t, SnapshotMetadata{
		Messages: map[string]Metadata{
			"assistant-1": assistantMetadata,
			"tool-msg-1":  toolMetadata,
		},
		ToolCalls: map[string]Metadata{
			"call-1": assistantMetadata,
		},
	}, metadata)
}

func TestBuildSnapshotMetadataIgnoresInvalidEntries(t *testing.T) {
	metadata := BuildSnapshotMetadata([]session.TrackEvent{
		{},
		{Payload: []byte("{")},
		newTrackEvent(t, aguievents.NewRunFinishedEvent("thread", "run")),
	})
	assert.True(t, metadata.IsZero())
}

func TestBuildSnapshotMetadataFallsBackToToolResultSource(t *testing.T) {
	toolMetadata := Metadata{
		EventID:      "evt-tool-result",
		Author:       "member-a",
		InvocationID: "inv-tool",
		Branch:       "root.member-a",
	}

	trackEvents := []session.TrackEvent{
		newTrackEvent(t, withRawEvent(
			aguievents.NewToolCallResultEvent(
				"tool-msg-1",
				"call-1",
				"done",
			),
			toolMetadata,
		)),
	}

	metadata := BuildSnapshotMetadata(trackEvents)
	assert.Equal(t, SnapshotMetadata{
		Messages: map[string]Metadata{
			"tool-msg-1": toolMetadata,
		},
		ToolCalls: map[string]Metadata{
			"call-1": toolMetadata,
		},
	}, metadata)
}

func newTrackEvent(t *testing.T, event aguievents.Event) session.TrackEvent {
	t.Helper()

	payload, err := json.Marshal(event)
	require.NoError(t, err)
	return session.TrackEvent{Payload: payload}
}

func withRawEvent(
	event aguievents.Event,
	metadata Metadata,
) aguievents.Event {
	event.GetBaseEvent().RawEvent = metadata
	return event
}
