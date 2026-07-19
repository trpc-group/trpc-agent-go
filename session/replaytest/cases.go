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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// PublicCases returns the standard replay consistency matrix.
func PublicCases() []ReplayCase {
	return []ReplayCase{
		singleTurnCase(), multiTurnCase(), toolConversationCase(), stateLifecycleCase(),
		memoryWriteReadCase(), memoryLifecycleCase(), summaryUpdateCase(),
		summaryWindowCase(), trackCase(), concurrentCausalCase(), recoveryCase(),
		identityPreservationCase(),
	}
}

func singleTurnCase() ReplayCase {
	return ReplayCase{Name: "single_turn_dialogue", Required: caps(CapabilityEvents), Operations: []Operation{
		create(), userEvent("turn-1-user", "hello"), assistantEvent("turn-1-assistant", "hello, how can I help?"),
	}}
}

func multiTurnCase() ReplayCase {
	return ReplayCase{Name: "multi_turn_ordering", Required: caps(CapabilityEvents), Operations: []Operation{
		create(), userEvent("turn-1-user", "remember my project"), assistantEvent("turn-1-assistant", "noted"),
		userEvent("turn-2-user", "what did I say?"), assistantEvent("turn-2-assistant", "your project"),
		userEvent("turn-3-user", "thanks"), assistantEvent("turn-3-assistant", "welcome"),
	}}
}

func toolConversationCase() ReplayCase {
	return ReplayCase{Name: "tool_call_response_extensions", Required: caps(CapabilityEvents, CapabilityState), Operations: []Operation{
		create(), userEvent("tool-user", "weather in Shenzhen"),
		AppendEventOperation{ID: "tool-call-event", Spec: EventSpec{
			Author: "assistant", Role: model.RoleAssistant, Branch: "root/tools", Tag: "weather",
			FilterKey: "root/tools/weather", ToolCalls: []ToolCallSpec{{
				LogicalID: "weather-call", Name: "weather", Arguments: raw(`{"city":"Shenzhen","units":"celsius"}`),
			}},
		}},
		AppendEventOperation{ID: "tool-result-event", Spec: EventSpec{
			Author: "weather", Role: model.RoleTool, ToolResponseID: "weather-call", ToolResponseName: "weather",
			Content: `{"temperature":31}`, Branch: "root/tools", Tag: "weather-result",
			FilterKey: "root/tools/weather", StateDelta: session.StateMap{"last_city": []byte(`"Shenzhen"`)},
			ToolCallArgs: map[string]json.RawMessage{"weather-call": raw(`{"city":"Shenzhen","units":"celsius"}`)},
			Extensions:   map[string]json.RawMessage{"example.trace": raw(`{"attempt":1,"large":9007199254740993}`)},
			Actions:      &event.EventActions{SkipSummarization: true},
		}},
	}}
}

func stateLifecycleCase() ReplayCase {
	return ReplayCase{Name: "state_lifecycle", Required: caps(
		CapabilityEvents, CapabilityState, CapabilityAppState, CapabilityUserState,
		CapabilityStateDelete, CapabilityStateClear,
	), Operations: []Operation{
		CreateSessionOperation{ID: "create", State: session.StateMap{"phase": []byte(`"created"`), "binary": {0xff, 0x00}}},
		SetStateOperation{ID: "session-set", Scope: StateScopeSession, Values: session.StateMap{"phase": []byte(`"running"`), "explicit_null": nil}},
		SetStateOperation{ID: "app-set", Scope: StateScopeApp, Values: session.StateMap{"app_keep": []byte(`1`), "app_drop": []byte(`2`)}},
		SetStateOperation{ID: "user-set", Scope: StateScopeUser, Values: session.StateMap{"user_a": []byte(`true`), "user_b": []byte(`false`)}},
		CheckpointOperation{ID: "state-after-set", Name: "after_state_set"},
		SetStateOperation{ID: "session-overwrite", Scope: StateScopeSession, Values: session.StateMap{"phase": []byte(`"complete"`)}},
		DeleteStateOperation{ID: "app-delete", Scope: StateScopeApp, Keys: []string{"app_drop"}},
		ClearStateOperation{ID: "user-clear", Scope: StateScopeUser},
		CheckpointOperation{ID: "state-after-delete", Name: "after_state_delete_clear"},
	}}
}

func memoryWriteReadCase() ReplayCase {
	eventTime := time.Date(2026, 7, 1, 8, 30, 0, 0, time.UTC)
	return ReplayCase{Name: "memory_write_read", Required: caps(CapabilityEvents, CapabilityMemory, CapabilityMemorySearch), Normalize: NormalizeOptions{
		MemoryOrder: MemoryOrderUnordered, MemoryQueryOrders: map[string]MemoryOrder{"search-go": MemoryOrderUnordered},
	}, Operations: []Operation{
		create(), AddMemoryOperation{ID: "preference", Content: "User prefers Go for backend services.", Topics: []string{"preference", "go"}, Metadata: &memory.Metadata{Kind: memory.KindFact}},
		AddMemoryOperation{ID: "episode", Content: "User shipped a replay harness with Alice.", Topics: []string{"project", "replay"}, Metadata: &memory.Metadata{
			Kind: memory.KindEpisode, EventTime: &eventTime, Participants: []string{"Alice", "User"}, Location: "Shenzhen",
		}},
		SearchMemoryOperation{ID: "search-go", Query: "Go backend"}, CheckpointOperation{ID: "memory-written", Name: "after_memory_write_search"},
	}}
}

func memoryLifecycleCase() ReplayCase {
	return ReplayCase{Name: "memory_update_delete", Required: caps(CapabilityMemory, CapabilityMemorySearch), Normalize: NormalizeOptions{
		MemoryOrder: MemoryOrderUnordered, MemoryQueryOrders: map[string]MemoryOrder{"search-updated": MemoryOrderUnordered},
	}, Operations: []Operation{
		create(), AddMemoryOperation{ID: "mutable", Content: "User likes databases.", Topics: []string{"database"}},
		UpdateMemoryOperation{ID: "update-mutable", MemoryID: "mutable", Content: "User likes SQLite and PostgreSQL.", Topics: []string{"database", "sql"}},
		AddMemoryOperation{ID: "temporary", Content: "Temporary task note.", Topics: []string{"temporary"}},
		DeleteMemoryOperation{ID: "delete-temporary", MemoryID: "temporary"},
		SearchMemoryOperation{ID: "search-updated", Query: "SQLite PostgreSQL"},
	}}
}

func summaryUpdateCase() ReplayCase {
	return ReplayCase{Name: "summary_filter_and_overwrite", Required: caps(CapabilityEvents, CapabilitySummary), Operations: []Operation{
		create(), filteredUser("summary-user-1", "root/tools/weather", "I prefer Celsius."),
		filteredAssistant("summary-assistant-1", "root/tools/weather", "Preference recorded."),
		CreateSummaryOperation{ID: "summary-create", FilterKey: "root/tools/weather", Force: true},
		WaitSummaryOperation{
			ID:                         "summary-wait-1",
			FilterKey:                  "root/tools/weather",
			ExpectedLastEventLogicalID: "summary-assistant-1",
		},
		CheckpointOperation{ID: "summary-first", Name: "after_first_summary"},
		filteredUser("summary-user-2", "root/tools/weather", "Also include humidity."),
		CreateSummaryOperation{ID: "summary-update", FilterKey: "root/tools/weather", Force: true},
		WaitSummaryOperation{
			ID:                         "summary-wait-2",
			FilterKey:                  "root/tools/weather",
			ExpectedLastEventLogicalID: "summary-user-2",
		},
		CheckpointOperation{ID: "summary-second", Name: "after_summary_overwrite"},
	}}
}

func summaryWindowCase() ReplayCase {
	return ReplayCase{Name: "summary_event_window_recovery", Required: caps(CapabilityEvents, CapabilitySummary), Operations: []Operation{
		create(), userEvent("window-user-1", "first"), assistantEvent("window-assistant-1", "one"),
		userEvent("window-user-2", "second"), assistantEvent("window-assistant-2", "two"),
		userEvent("window-user-3", "third"), assistantEvent("window-assistant-3", "three"),
		CreateSummaryOperation{ID: "window-summary", FilterKey: session.SummaryFilterKeyAllContents, Force: true},
		WaitSummaryOperation{
			ID:                         "window-wait",
			FilterKey:                  session.SummaryFilterKeyAllContents,
			ExpectedLastEventLogicalID: "window-assistant-3",
		},
		userEvent("window-user-followup", "continue after summary"), assistantEvent("window-assistant-followup", "continued"),
		SessionWindowCheckpointOperation{ID: "window-read", Name: "summary_plus_tail_events", EventNum: 2},
	}}
}

func trackCase() ReplayCase {
	return ReplayCase{Name: "track_status_error_invocation", Required: caps(CapabilityTracks), Operations: []Operation{
		create(), AppendTrackOperation{ID: "track-start", Track: "tool.weather", Payload: raw(`{"event_type":"started","invocation_id":"root","duration_ms":12}`)},
		AppendTrackOperation{ID: "track-fail", Track: "tool.weather", Payload: raw(`{"event_type":"failed","invocation_id":"root","error":"timeout","duration_ms":99}`)},
		AppendTrackOperation{ID: "subtask-complete", Track: "subtask", Payload: raw(`{"event_type":"completed","invocation_id":"child-1","status":"ok"}`)},
	}}
}

func concurrentCausalCase() ReplayCase {
	ids := []string{"parallel-a-call", "parallel-b-call", "parallel-c-call"}
	return ReplayCase{Name: "concurrent_causal_order", Required: caps(CapabilityEvents), Order: EventOrderContract{
		ExactLogicalIDs: append([]string{"parallel-user"}, ids...),
		HappensBefore:   [][2]string{{"parallel-user", "parallel-a-call"}, {"parallel-user", "parallel-b-call"}, {"parallel-user", "parallel-c-call"}},
	}, Operations: []Operation{
		create(), userEvent("parallel-user", "run three tools"), ParallelOperation{ID: "parallel-group", Operations: []Operation{
			assistantEvent("parallel-a-call", "tool A"), assistantEvent("parallel-b-call", "tool B"), assistantEvent("parallel-c-call", "tool C"),
		}}, CheckpointOperation{ID: "parallel-complete", Name: "after_parallel_writes"},
	}}
}

func recoveryCase() ReplayCase {
	return ReplayCase{Name: "failure_retry_ack_loss", Required: caps(
		CapabilityEvents, CapabilityState, CapabilityEventStateDeltaNull,
	), Operations: []Operation{
		CreateSessionOperation{ID: "create", State: session.StateMap{"phase": []byte(`"started"`), "pending": []byte(`true`)}},
		userEvent("recovery-user", "start recovery"), RecoveryAppendEventOperation{ID: "recovery-assistant", Spec: EventSpec{
			Author: "assistant", Role: model.RoleAssistant, Content: "committed after retry",
			StateDelta: session.StateMap{"phase": []byte(`"committed"`), "pending": nil},
		}}, CheckpointOperation{ID: "recovery-checkpoint", Name: "after_ack_loss_recovery"},
	}}
}

func identityPreservationCase() ReplayCase {
	return ReplayCase{Name: "identity_duplicate_preservation", Required: caps(CapabilityEvents), Operations: []Operation{
		create(), userEvent("duplicate-user-1", "same text"), userEvent("duplicate-user-2", "same text"),
		AppendEventOperation{ID: "identity-tool-call", Spec: EventSpec{Author: "assistant", Role: model.RoleAssistant, ToolCalls: []ToolCallSpec{{LogicalID: "identity-call", Name: "echo", Arguments: raw(`{"value":"same"}`)}}}},
		AppendEventOperation{ID: "identity-tool-result", Spec: EventSpec{Author: "echo", Role: model.RoleTool, Content: `{"value":"same"}`, ToolResponseID: "identity-call", ToolResponseName: "echo"}},
	}}
}

func caps(values ...CapabilityName) []CapabilityName { return values }
func create() Operation                              { return CreateSessionOperation{ID: "create", State: session.StateMap{}} }
func userEvent(id, content string) Operation {
	return AppendEventOperation{ID: id, Spec: EventSpec{Author: "user", Role: model.RoleUser, Content: content}}
}
func assistantEvent(id, content string) Operation {
	return AppendEventOperation{ID: id, Spec: EventSpec{Author: "assistant", Role: model.RoleAssistant, Content: content}}
}
func filteredUser(id, filterKey, content string) Operation {
	return AppendEventOperation{ID: id, Spec: EventSpec{Author: "user", Role: model.RoleUser, Content: content, Branch: filterKey, FilterKey: filterKey}}
}
func filteredAssistant(id, filterKey, content string) Operation {
	return AppendEventOperation{ID: id, Spec: EventSpec{Author: "assistant", Role: model.RoleAssistant, Content: content, Branch: filterKey, FilterKey: filterKey}}
}
func raw(value string) json.RawMessage { return json.RawMessage(value) }
