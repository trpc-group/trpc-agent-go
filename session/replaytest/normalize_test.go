//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestNormalizeEventsCanonicalizesProviderData(t *testing.T) {
	timestamp := time.Date(
		2026, time.January, 2, 3, 4, 5, 987654321, time.FixedZone("test", 8*60*60),
	)
	events := []event.Event{{
		ID:           "b3614d0a-8790-4e0d-98ec-76ff0f49dc10",
		InvocationID: "3adbe718-f1a1-450e-bdb8-67cf712e6200",
		Author:       "assistant",
		Timestamp:    timestamp,
		Branch:       "agent/tool",
		Tag:          "beta; alpha",
		FilterKey:    "agent/tool",
		StateDelta: map[string][]byte{
			"count": []byte(`1`),
			"prefs": []byte(`{"b":2,"a":1}`),
		},
		Extensions: map[string]json.RawMessage{
			ExtensionLogicalID: json.RawMessage(`"tool-event"`),
			ExtensionSequence:  json.RawMessage(`2`),
			"custom":           json.RawMessage(`{"z":1,"a":[2,1]}`),
		},
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      "lookup",
						Arguments: []byte(`{"city":"Shenzhen","units":"c"}`),
					},
				}},
			},
		}}},
	}}

	normalized, observed := NormalizeEvents(events, false)
	require.Equal(t, []string{"tool-event"}, observed)
	require.Len(t, normalized, 1)
	got := normalized[0]
	require.Equal(t, "tool-event", got.ID)
	require.Equal(t, 2, got.Sequence)
	require.Equal(t, "alpha;beta", got.Tag)
	require.Equal(t, "2026-01-01T19:04:05.987Z", got.Timestamp)
	require.Equal(t, map[string]any{
		"z": int64(1),
		"a": []any{int64(2), int64(1)},
	}, got.Extensions["custom"])
	require.NotContains(t, got.Extensions, ExtensionLogicalID)
	require.Equal(t, int64(1), got.StateDelta["count"])
	require.Equal(t, map[string]any{
		"a": int64(1),
		"b": int64(2),
	}, got.StateDelta["prefs"])
	require.Equal(t, map[string]any{
		"city":  "Shenzhen",
		"units": "c",
	}, got.ToolCalls[0].Arguments)
}

func TestNormalizeEventsCollapsesClickHouseExtensionObjects(t *testing.T) {
	base := event.Event{
		ID:        "tool-response",
		Author:    "tool",
		Timestamp: time.Unix(1, 0),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleTool,
				Content: "sunny",
				ToolID:  "call-weather",
			},
		}}},
	}
	direct := base
	direct.Extensions = map[string]json.RawMessage{
		ExtensionLogicalID: json.RawMessage(`"tool-response"`),
		ExtensionSequence:  json.RawMessage(`3`),
		event.ToolCallArgsExtensionKey: json.RawMessage(
			`{"call-weather":{"city":"Shenzhen"}}`,
		),
	}
	expanded := base
	expanded.Extensions = map[string]json.RawMessage{
		"trpc_agent": json.RawMessage(
			`{"replay":{"logical_id":"tool-response","sequence":3},` +
				`"tool_call_args":{"call-weather":{"city":"Shenzhen"}}}`,
		),
	}

	directEvents, _ := NormalizeEvents([]event.Event{direct}, false)
	expandedEvents, _ := NormalizeEvents([]event.Event{expanded}, false)
	require.Equal(t, directEvents, expandedEvents)
	require.Equal(t, 3, expandedEvents[0].Sequence)
	require.Equal(t, map[string]any{
		"call-weather": map[string]any{"city": "Shenzhen"},
	}, expandedEvents[0].Extensions[event.ToolCallArgsExtensionKey])
}

func TestNormalizeEventsUsesLogicalSequenceForConcurrentReplay(t *testing.T) {
	makeEvent := func(id string, sequence int) event.Event {
		sequenceJSON, err := json.Marshal(sequence)
		require.NoError(t, err)
		return event.Event{
			ID:        id,
			Author:    "assistant",
			Timestamp: time.Unix(int64(sequence), 0),
			Extensions: map[string]json.RawMessage{
				ExtensionLogicalID: json.RawMessage(`"` + id + `"`),
				ExtensionSequence:  sequenceJSON,
			},
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: id,
				},
			}}},
		}
	}
	events := []event.Event{
		makeEvent("event-2", 2),
		makeEvent("event-1", 1),
	}

	normalized, observed := NormalizeEvents(events, true)
	require.Equal(t, []string{"event-2", "event-1"}, observed)
	require.Equal(t, "event-1", normalized[0].ID)
	require.Equal(t, 0, normalized[0].Index)
	require.Equal(t, "event-2", normalized[1].ID)
	require.Equal(t, 1, normalized[1].Index)
}

func TestNormalizeEventsStabilizesGeneratedIDsAcrossCommitOrder(t *testing.T) {
	makeEvent := func(
		id string,
		invocationID string,
		sequence int,
	) event.Event {
		sequenceJSON, err := json.Marshal(sequence)
		require.NoError(t, err)
		return event.Event{
			ID:           id,
			InvocationID: invocationID,
			Author:       "assistant",
			Timestamp:    time.Unix(int64(sequence), 0),
			Extensions: map[string]json.RawMessage{
				ExtensionSequence: sequenceJSON,
			},
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: intString(sequence),
				},
			}}},
		}
	}
	backendA := []event.Event{
		makeEvent(
			"ce7e0963-e8a4-4a90-8d4f-0e154db706ad",
			"d02cc5b8-6fc6-4ac5-a1f4-da0c92a220f3",
			2,
		),
		makeEvent(
			"84dfa68e-35ee-4d68-a462-33158bbdf6bf",
			"3b25c48a-98f5-4280-afb6-a89c4bab12c4",
			1,
		),
	}
	backendB := []event.Event{
		makeEvent(
			"346f4a3d-012e-45b3-8013-e9f26272f15a",
			"5266a7de-76b3-4f2a-8757-d5b9f7e98f75",
			1,
		),
		makeEvent(
			"fa2add5f-5564-4d16-a927-9c5bb7ded8c5",
			"39b6bed1-6b43-42ea-a95c-e5fafc344bec",
			2,
		),
	}

	normalizedA, _ := NormalizeEvents(backendA, true)
	normalizedB, _ := NormalizeEvents(backendB, true)
	require.Equal(t, normalizedA, normalizedB)
}

func TestNormalizeStateDropsTrackIndex(t *testing.T) {
	got := NormalizeState(session.StateMap{
		"tracks": []byte(`["tools"]`),
		"prefs":  []byte(`{"theme":"dark"}`),
		"raw":    []byte("not-json"),
		"empty":  nil,
	})
	require.NotContains(t, got, "tracks")
	require.Equal(t, map[string]any{"theme": "dark"}, got["prefs"])
	require.Equal(t, "not-json", got["raw"])
	require.Nil(t, got["empty"])
}

func TestNormalizeMemoryUsesSemanticIdentity(t *testing.T) {
	eventTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	entryA := &memory.Entry{
		ID:      "backend-id-a",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:       "User prefers Go",
			Topics:       []string{"language", "preference"},
			Kind:         memory.KindFact,
			EventTime:    &eventTime,
			Participants: []string{"Bob", "Alice"},
			Location:     "Shenzhen",
		},
		Score: 0.81234567,
	}
	entryB := *entryA
	entryB.ID = "backend-id-b"

	gotA := NormalizeMemory(entryA)
	gotB := NormalizeMemory(&entryB)
	require.Equal(t, gotA.ID, gotB.ID)
	require.Equal(t, []string{"language", "preference"}, gotA.Topics)
	require.Equal(t, []string{"Alice", "Bob"}, gotA.Participants)
	require.Equal(t, 0.812346, gotA.Score)
}

func TestNormalizeTrackExtractsPortableFields(t *testing.T) {
	got := NormalizeTrackEvent(session.TrackEvent{
		Track: "tools",
		Payload: json.RawMessage(
			`{"duration_ms":12.34567,"error":"timeout",` +
				`"event_type":"failed","invocation_id":"inv-1"}`,
		),
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 999999999, time.UTC),
	}, 0)

	require.Equal(t, "failed", got.EventType)
	require.Equal(t, "inv-1", got.InvocationID)
	require.Equal(t, "timeout", got.Error)
	require.Equal(t, 12.346, got.DurationMS)
	require.Equal(t, "2026-01-02T03:04:05.999Z", got.Timestamp)
}
