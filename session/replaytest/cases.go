//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var baseTime = time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)

// StandardCases returns the public replay suite required by the consistency
// harness. The cases are deterministic and safe for lightweight local runs.
func StandardCases() []ReplayCase {
	return []ReplayCase{
		singleTurnCase(),
		multiTurnCase(),
		toolCallCase(),
		stateUpdateCase(),
		memoryCase(),
		summaryCase(),
		summaryWithEventWindowCase(),
		trackCase(),
		outOfOrderCase(),
		retryRecoveryCase(),
	}
}

func singleTurnCase() ReplayCase {
	return ReplayCase{
		Name:        "single_turn_dialog",
		Description: "single user message and assistant text event",
		Operations: []Operation{
			createSession(map[string]string{"locale": "en-US"}),
			appendEvent(messageEvent("evt-01-user", 1, "inv-1", "user", model.NewUserMessage("hello"))),
			appendEvent(messageEvent("evt-02-assistant", 2, "inv-1", "assistant", model.NewAssistantMessage("hi there"))),
		},
	}
}

func multiTurnCase() ReplayCase {
	ops := []Operation{createSession(nil)}
	for i := 0; i < 3; i++ {
		ops = append(
			ops,
			appendEvent(messageEvent(
				fmt.Sprintf("evt-multi-%d-user", i),
				i*2+1,
				"inv-multi",
				"user",
				model.NewUserMessage(fmt.Sprintf("question %d", i+1)),
			)),
			appendEvent(messageEvent(
				fmt.Sprintf("evt-multi-%d-assistant", i),
				i*2+2,
				"inv-multi",
				"assistant",
				model.NewAssistantMessage(fmt.Sprintf("answer %d", i+1)),
			)),
		)
	}
	return ReplayCase{
		Name:        "multi_turn_order",
		Description: "multiple user and assistant events must replay in append order",
		Operations:  ops,
	}
}

func toolCallCase() ReplayCase {
	toolCall := model.ToolCall{
		ID:   "call-weather-1",
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      "weather_lookup",
			Arguments: []byte(`{"city":"Shenzhen","unit":"celsius"}`),
		},
	}
	toolMsg := model.Message{
		Role:      model.RoleAssistant,
		Content:   "I will check the weather.",
		ToolCalls: []model.ToolCall{toolCall},
	}
	toolResult := model.NewToolMessage("call-weather-1", "weather_lookup", `{"temp":31,"condition":"sunny"}`)
	toolResultEvent := messageEvent("evt-tool-result", 3, "inv-tool", "tool", toolResult)
	_ = event.SetExtension(toolResultEvent, event.ToolCallArgsExtensionKey, map[string]any{
		"call-weather-1": map[string]any{
			"city": "Shenzhen",
			"unit": "celsius",
		},
	})
	return ReplayCase{
		Name:        "tool_call_round_trip",
		Description: "tool call, tool response, and tool call args extension",
		Operations: []Operation{
			createSession(nil),
			appendEvent(messageEvent("evt-tool-user", 1, "inv-tool", "user", model.NewUserMessage("what is the weather?"))),
			appendEvent(messageEvent("evt-tool-call", 2, "inv-tool", "assistant", toolMsg)),
			appendEvent(toolResultEvent),
			appendEvent(messageEvent("evt-tool-final", 4, "inv-tool", "assistant", model.NewAssistantMessage("It is sunny and 31C."))),
		},
	}
}

func stateUpdateCase() ReplayCase {
	return ReplayCase{
		Name:        "state_overwrite_tombstone",
		Description: "session state writes, overwrites, and nil tombstone deletion semantics",
		Operations: []Operation{
			createSession(map[string]string{"theme": "light", "obsolete": "yes"}),
			updateState(map[string][]byte{
				"theme": []byte("dark"),
				"step":  []byte(`{"n":1}`),
			}),
			appendEvent(messageEventWithState(
				"evt-state-delta",
				1,
				"inv-state",
				"assistant",
				model.NewAssistantMessage("state updated"),
				session.StateMap{
					"step":     []byte(`{"n":2}`),
					"obsolete": nil,
				},
			)),
			updateState(map[string][]byte{
				"theme":    []byte("contrast"),
				"clear_me": nil,
			}),
		},
	}
}

func memoryCase() ReplayCase {
	eventTime := baseTime.Add(-24 * time.Hour)
	return ReplayCase{
		Name:        "memory_write_read_search",
		Description: "fact and episodic memory writes with metadata",
		Operations: []Operation{
			createSession(nil),
			addMemory("User prefers concise answers.", []string{"preference"}, &memory.Metadata{Kind: memory.KindFact}),
			addMemory(
				"User debugged a payment webhook timeout with Lee.",
				[]string{"task", "debugging"},
				&memory.Metadata{
					Kind:         memory.KindEpisode,
					EventTime:    &eventTime,
					Participants: []string{"user", "Lee"},
					Location:     "Shenzhen",
				},
			),
		},
	}
}

func summaryCase() ReplayCase {
	return ReplayCase{
		Name:        "summary_filter_key_update",
		Description: "summary generation, filter-key ownership, overwrite, version, and updated_at",
		Operations: []Operation{
			createSession(nil),
			appendEvent(branchEvent("evt-summary-u1", 1, "inv-sum", "user", model.NewUserMessage("plan branch work"), "agent/main")),
			appendEvent(branchEvent("evt-summary-a1", 2, "inv-sum", "assistant", model.NewAssistantMessage("branch work planned"), "agent/main")),
			createSummary("agent/main", true),
			appendEvent(branchEvent("evt-summary-u2", 3, "inv-sum", "user", model.NewUserMessage("revise the branch plan"), "agent/main")),
			createSummary("agent/main", true),
		},
	}
}

func summaryWithEventWindowCase() ReplayCase {
	ops := []Operation{createSession(nil)}
	for i := 0; i < 6; i++ {
		ops = append(ops, appendEvent(messageEvent(
			fmt.Sprintf("evt-window-%d", i),
			i+1,
			"inv-window",
			"user",
			model.NewUserMessage(fmt.Sprintf("long history line %d", i+1)),
		)))
	}
	ops = append(
		ops,
		createSummary("", true),
		appendEvent(messageEvent("evt-window-tail-user", 7, "inv-window", "user", model.NewUserMessage("new question after summary"))),
		appendEvent(messageEvent("evt-window-tail-assistant", 8, "inv-window", "assistant", model.NewAssistantMessage("new answer after summary"))),
	)
	return ReplayCase{
		Name:             "summary_with_event_window",
		Description:      "summary plus truncated event window must still replay context",
		SnapshotEventNum: 4,
		Operations:       ops,
	}
}

func trackCase() ReplayCase {
	return ReplayCase{
		Name:        "track_observability",
		Description: "tool duration, subtask status, and error track payloads",
		Operations: []Operation{
			createSession(nil),
			appendTrack("tool/weather", 1, map[string]any{
				"event_type":  "tool.start",
				"invocation":  "inv-track",
				"duration_ms": 0,
			}),
			appendTrack("tool/weather", 2, map[string]any{
				"event_type":  "tool.done",
				"invocation":  "inv-track",
				"duration_ms": 12.3456789,
			}),
			appendTrack("subtask/research", 3, map[string]any{
				"event_type": "subtask.error",
				"invocation": "inv-child",
				"error":      "timeout",
			}),
		},
	}
}

func outOfOrderCase() ReplayCase {
	return ReplayCase{
		Name:        "out_of_order_interleaving",
		Description: "interleaved tool and sub-agent events preserve append order despite timestamps",
		Operations: []Operation{
			createSession(nil),
			appendEvent(messageEvent("evt-interleave-user", 0, "inv-root", "user", model.NewUserMessage("run tools A and B"))),
			appendEvent(branchEvent("evt-interleave-3", 3, "inv-a", "assistant", model.NewAssistantMessage("tool A started"), "tools/a")),
			appendEvent(branchEvent("evt-interleave-1", 1, "inv-b", "assistant", model.NewAssistantMessage("tool B started"), "tools/b")),
			appendEvent(branchEvent("evt-interleave-2", 2, "inv-a", "assistant", model.NewAssistantMessage("tool A finished"), "tools/a")),
			updateState(map[string][]byte{"interleaving": []byte(`{"mode":"append-order"}`)}),
		},
	}
}

func retryRecoveryCase() ReplayCase {
	return ReplayCase{
		Name:        "retry_recovery_idempotence",
		Description: "duplicate add-memory retry and repeated summary update do not create duplicate logical state",
		Operations: []Operation{
			createSession(nil),
			appendEvent(messageEvent("evt-retry-user", 1, "inv-retry", "user", model.NewUserMessage("remember this preference"))),
			{
				Kind:       OperationRetry,
				RetryCount: 2,
				Operations: []Operation{
					addMemory("User wants retry-safe writes.", []string{"reliability"}, &memory.Metadata{Kind: memory.KindFact}),
				},
			},
			appendEvent(messageEvent("evt-retry-assistant", 2, "inv-retry", "assistant", model.NewAssistantMessage("preference recorded"))),
			{
				Kind:       OperationRetry,
				RetryCount: 2,
				Operations: []Operation{
					createSummary("", true),
				},
			},
		},
	}
}

func createSession(state map[string]string) Operation {
	out := make(session.StateMap)
	for k, v := range state {
		out[k] = []byte(v)
	}
	return Operation{Kind: OperationCreateSession, State: out}
}

func updateState(state map[string][]byte) Operation {
	return Operation{Kind: OperationUpdateSessionState, State: session.StateMap(state)}
}

func appendEvent(evt *event.Event) Operation {
	return Operation{Kind: OperationAppendEvent, Event: evt}
}

func addMemory(content string, topics []string, metadata *memory.Metadata) Operation {
	return Operation{
		Kind: OperationAddMemory,
		Memory: &MemoryOperation{
			Content:  content,
			Topics:   topics,
			Metadata: metadata,
		},
	}
}

func createSummary(filterKey string, force bool) Operation {
	return Operation{
		Kind:    OperationCreateSummary,
		Summary: &SummaryOperation{FilterKey: filterKey, Force: force},
	}
}

func appendTrack(track session.Track, offset int, payload map[string]any) Operation {
	raw, _ := json.Marshal(payload)
	return Operation{
		Kind: OperationAppendTrack,
		Track: &session.TrackEvent{
			Track:     track,
			Payload:   raw,
			Timestamp: baseTime.Add(time.Duration(offset) * time.Second),
		},
	}
}

func branchEvent(id string, offset int, invocationID, author string, msg model.Message, filterKey string) *event.Event {
	evt := messageEvent(id, offset, invocationID, author, msg)
	evt.Branch = filterKey
	evt.FilterKey = filterKey
	return evt
}

func messageEvent(id string, offset int, invocationID, author string, msg model.Message) *event.Event {
	return messageEventWithState(id, offset, invocationID, author, msg, nil)
}

func messageEventWithState(
	id string,
	offset int,
	invocationID string,
	author string,
	msg model.Message,
	state session.StateMap,
) *event.Event {
	return &event.Event{
		Response: &model.Response{
			ID:      id + "-response",
			Object:  model.ObjectTypeChatCompletion,
			Created: baseTime.Add(time.Duration(offset) * time.Second).Unix(),
			Model:   "replay-model",
			Choices: []model.Choice{{
				Index:   0,
				Message: msg,
			}},
			Done: true,
		},
		InvocationID: invocationID,
		Author:       author,
		ID:           id,
		Timestamp:    baseTime.Add(time.Duration(offset) * time.Second),
		StateDelta:   state,
		Version:      event.CurrentVersion,
	}
}
