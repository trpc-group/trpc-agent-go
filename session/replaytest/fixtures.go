//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ReplayFixtures returns all 10 predefined replay cases for cross-backend
// consistency testing. Each case tests a specific scenario.
func ReplayFixtures() []ReplayCase {
	return []ReplayCase{
		Case1_SingleTurn(),
		Case2_MultiTurn(),
		Case3_ToolCall(),
		Case4_StateUpdate(),
		Case5_Memory(),
		Case6_Summary(),
		Case7_SummaryTruncation(),
		Case8_TrackEvents(),
		Case9_ConcurrentWrites(),
		Case10_Idempotency(),
	}
}

// Case1_SingleTurn covers basic session creation and event appending.
// Trap verification point: event content/order/role tampering.
func Case1_SingleTurn() ReplayCase {
	key := session.Key{AppName: "fixture", UserID: "user1", SessionID: "case1"}
	return ReplayCase{
		Name: "case1_single_turn",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv1", "user", "user", "Hello, what's the weather today?")}},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv2", "assistant", "assistant", "The weather is sunny with a high of 25°C.")}},
			{Type: OpGetSession, Key: key},
		},
		Want: WantResult{
			ExpectedDiffKeys: []string{"events[0].content", "events[1].content"},
			ExpectedDiffCount: 2,
		},
	}
}

// Case2_MultiTurn covers continuous event appending across multiple turns.
// Trap verification point: event swapping/loss.
func Case2_MultiTurn() ReplayCase {
	key := session.Key{AppName: "fixture", UserID: "user1", SessionID: "case2"}
	events := []*event.Event{
		NewEvent("inv1", "user", "user", "What is the capital of France?"),
		NewEvent("inv2", "assistant", "assistant", "The capital of France is Paris."),
		NewEvent("inv3", "user", "user", "What about Japan?"),
		NewEvent("inv4", "assistant", "assistant", "The capital of Japan is Tokyo."),
		NewEvent("inv5", "user", "user", "And Australia?"),
		NewEvent("inv6", "assistant", "assistant", "The capital of Australia is Canberra."),
	}

	ops := []ReplayOp{{Type: OpCreateSession, Key: key}}
	for _, e := range events {
		ops = append(ops, ReplayOp{Type: OpAppendEvent, Key: key, Data: EventData{Event: e}})
	}
	ops = append(ops, ReplayOp{Type: OpGetSession, Key: key})

	return ReplayCase{
		Name: "case2_multi_turn",
		Ops:  ops,
		Want: WantResult{
			ExpectedDiffKeys: []string{"events[0].content", "events[1].content"},
			ExpectedDiffCount: 2,
		},
	}
}

// Case3_ToolCall covers tool call + tool response + arguments extension.
// Trap verification point: tool_call args tampering / tool response content tampering.
func Case3_ToolCall() ReplayCase {
	key := session.Key{AppName: "fixture", UserID: "user1", SessionID: "case3"}

	tcEvent := NewToolCallEvent("inv2", "assistant", "call_weather_1", "get_weather",
		[]byte(`{"city":"London","units":"metric","days":3}`))
	trEvent := NewToolResponseEvent("inv2", "tool", "call_weather_1", "get_weather",
		`{"temperature":22,"condition":"Sunny","humidity":60}`)

	return ReplayCase{
		Name: "case3_tool_call",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv1", "user", "user", "What's the weather in London?")}},
			{Type: OpAppendEvent, Key: key, Data: EventData{Event: tcEvent}},
			{Type: OpAppendEvent, Key: key, Data: EventData{Event: trEvent}},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv3", "assistant", "assistant", "The weather in London is sunny at 22°C.")}},
			{Type: OpGetSession, Key: key},
		},
		Want: WantResult{
			ExpectedDiffKeys: []string{"events[1].toolCalls[0].function.arguments"},
			ExpectedDiffCount: 1,
		},
	}
}

// Case4_StateUpdate covers writing, overwriting, deleting, and clearing state.
// Trap verification point: state value tampering / key loss.
func Case4_StateUpdate() ReplayCase {
	key := session.Key{AppName: "fixture", UserID: "user1", SessionID: "case4"}
	return ReplayCase{
		Name: "case4_state_update",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			{Type: OpUpdateSessionState, Key: key,
				Data: StateData{State: session.StateMap{"score": []byte("100"), "level": []byte("5")}}},
			{Type: OpUpdateSessionState, Key: key,
				Data: StateData{State: session.StateMap{"score": []byte("200")}}},
			// Delete one key.
			{Type: OpUpdateSessionState, Key: key,
				Data: StateData{State: session.StateMap{"level": nil}}},
			{Type: OpGetSession, Key: key}, // Refresh to get the updated state.
		},
		Want: WantResult{
			ExpectedDiffKeys: []string{"session.state"},
			ExpectedDiffCount: 1,
		},
	}
}

// Case5_Memory covers adding, reading, and searching memories.
// Trap verification point: memory content tampering / scope loss / similarity drift.
func Case5_Memory() ReplayCase {
	key := session.Key{AppName: "fixture", UserID: "user1", SessionID: "case5"}
	uk := memory.UserKey{AppName: "fixture", UserID: "user1"}
	return ReplayCase{
		Name: "case5_memory",
		Ops: []ReplayOp{
			{Type: OpAddMemory, Key: key,
				Data: MemoryData{
					UserKey: uk,
					Memory:  "User prefers dark mode over light mode.",
					Topics:  []string{"preference", "ui"},
					Metadata: &memory.Metadata{
						Kind: "fact",
					},
				}},
			{Type: OpAddMemory, Key: key,
				Data: MemoryData{
					UserKey: uk,
					Memory:  "User completed the onboarding tutorial yesterday.",
					Topics:  []string{"progress", "onboarding"},
					Metadata: &memory.Metadata{
						Kind: "episode",
					},
				}},
			{Type: OpReadMemories, Key: key, Data: uk},
			{Type: OpSearchMemories, Key: key, Data: uk},
		},
		Want: WantResult{
			ExpectedDiffKeys: []string{"memories[0].memory.memory"},
			ExpectedDiffCount: 1,
		},
	}
}

// Case6_Summary covers summary generation and update with filter-key support.
// Trap verification point: summary content loss / filter-key tampering / ownership error.
func Case6_Summary() ReplayCase {
	key := session.Key{AppName: "fixture", UserID: "user1", SessionID: "case6"}
	return ReplayCase{
		Name: "case6_summary",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv1", "user", "user", "Tell me about machine learning.")}},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv2", "assistant", "assistant", "Machine learning is a subset of AI.")}},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv3", "user", "user", "What about deep learning?")}},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv4", "assistant", "assistant", "Deep learning is a subset of machine learning.")}},
			{Type: OpCreateSessionSummary, Key: key,
				Data: SummaryData{FilterKey: "", Force: true}},
			{Type: OpCreateSessionSummary, Key: key,
				Data: SummaryData{FilterKey: "branch-a", Force: true}},
			{Type: OpGetSessionSummaryText, Key: key},
			{Type: OpGetSession, Key: key},
		},
		Want: WantResult{
			ExpectedDiffKeys: []string{"session.summaries"},
			ExpectedDiffCount: 1,
		},
	}
}

// Case7_SummaryTruncation covers long conversation compaction and context restoration.
// Trap verification point: event loss after truncation / summary overwrite error.
func Case7_SummaryTruncation() ReplayCase {
	key := session.Key{AppName: "fixture", UserID: "user1", SessionID: "case7"}

	events := make([]*event.Event, 0, 12)
	for i := 0; i < 5; i++ {
		events = append(events,
			NewEvent(fmtInv("inv_%d", i*2), "user", "user",
				fmt.Sprintf("Question %d: What is %d+%d?", i+1, i, i+1)),
			NewEvent(fmtInv("inv_%d", i*2+1), "assistant", "assistant",
				fmt.Sprintf("Answer %d: The result is %d.", i+1, i*2+1)),
		)
	}

	ops := []ReplayOp{{Type: OpCreateSession, Key: key}}
	for _, e := range events {
		ops = append(ops, ReplayOp{Type: OpAppendEvent, Key: key, Data: EventData{Event: e}})
	}
	ops = append(ops, ReplayOp{
		Type: OpCreateSessionSummary, Key: key,
		Data: SummaryData{FilterKey: "", Force: true},
	})
	// Add two more events after summary.
	ops = append(ops,
		ReplayOp{Type: OpAppendEvent, Key: key,
			Data: EventData{Event: NewEvent("inv_extra_1", "user", "user", "One more question.")}},
		ReplayOp{Type: OpAppendEvent, Key: key,
			Data: EventData{Event: NewEvent("inv_extra_2", "assistant", "assistant", "Sure, ask away!")}},
		ReplayOp{Type: OpGetSession, Key: key},
	)

	return ReplayCase{
		Name: "case7_summary_truncation",
		Ops:  ops,
		Want: WantResult{
			ExpectedDiffKeys: []string{"session.summaries"},
			ExpectedDiffCount: 1,
		},
	}
}

// Case8_TrackEvents covers track event appending and retrieval.
// Trap verification point: track time offset / event type tampering / invocation loss.
func Case8_TrackEvents() ReplayCase {
	key := session.Key{AppName: "fixture", UserID: "user1", SessionID: "case8"}
	return ReplayCase{
		Name: "case8_track_events",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			{Type: OpAppendTrackEvent, Key: key,
				Data: TrackEventData{
					Event: &session.TrackEvent{
						Track:   "tool_execution",
						Payload: []byte(`{"event_type":"tool_execution","tool_name":"get_weather","duration_ms":1200,"status":"success"}`),
					},
				}},
			{Type: OpAppendTrackEvent, Key: key,
				Data: TrackEventData{
					Event: &session.TrackEvent{
						Track:   "subtask",
						Payload: []byte(`{"event_type":"subtask","name":"parse_location","duration_ms":300,"status":"success"}`),
					},
				}},
			{Type: OpAppendTrackEvent, Key: key,
				Data: TrackEventData{
					Event: &session.TrackEvent{
						Track:   "tool_execution",
						Payload: []byte(`{"event_type":"error","tool_name":"send_email","error_message":"connection timeout","duration_ms":5000,"status":"failed"}`),
					},
				}},
			{Type: OpGetSession, Key: key},
		},
		Want: WantResult{
			ExpectedDiffKeys: []string{"tracks"},
			ExpectedDiffCount: 1,
		},
	}
}

// Case9_ConcurrentWrites simulates concurrent event appending and verifies
// the final normalized order is consistent across backends.
// Trap verification point: final order disorder / event duplication.
func Case9_ConcurrentWrites() ReplayCase {
	key := session.Key{AppName: "fixture", UserID: "user1", SessionID: "case9"}

	// Sequential emulation of concurrent writes (5 events appended in order).
	events := make([]*event.Event, 5)
	for i := 0; i < 5; i++ {
		events[i] = NewEvent(fmtInv("con_inv_%d", i), "user", "user",
			fmt.Sprintf("Concurrent message %d.", i+1))
	}

	ops := []ReplayOp{{Type: OpCreateSession, Key: key}}
	for _, e := range events {
		ops = append(ops, ReplayOp{Type: OpAppendEvent, Key: key, Data: EventData{Event: e}})
	}
	ops = append(ops, ReplayOp{Type: OpGetSession, Key: key})

	return ReplayCase{
		Name: "case9_concurrent_writes",
		Ops:  ops,
		Want: WantResult{
			ExpectedDiffKeys: []string{"events"},
			ExpectedDiffCount: 1,
		},
	}
}

// Case10_Idempotency covers duplicate/idempotent writes and retry scenarios.
// Trap verification point: duplicate event / dirty state / duplicate memory.
func Case10_Idempotency() ReplayCase {
	key := session.Key{AppName: "fixture", UserID: "user1", SessionID: "case10"}
	uk := memory.UserKey{AppName: "fixture", UserID: "user1"}

	event1 := NewEvent("inv1", "user", "user", "Hello, this is a test.")

	return ReplayCase{
		Name: "case10_idempotency",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			// Append same event twice (idempotent).
			{Type: OpAppendEvent, Key: key, Data: EventData{Event: event1}},
			{Type: OpAppendEvent, Key: key, Data: EventData{Event: event1}},
			// Add memory with same content twice.
			{Type: OpAddMemory, Key: key,
				Data: MemoryData{
					UserKey: uk,
					Memory:  "User likes programming.",
					Topics:  []string{"hobby"},
				}},
			{Type: OpAddMemory, Key: key,
				Data: MemoryData{
					UserKey: uk,
					Memory:  "User likes programming.",
					Topics:  []string{"hobby"},
				}},
			{Type: OpGetSession, Key: key},
			{Type: OpReadMemories, Key: key, Data: uk},
		},
		Want: WantResult{
			ExpectedDiffKeys: []string{"events", "memories"},
			ExpectedDiffCount: 2,
		},
	}
}

// fmtInv is a helper to format invocation IDs.
func fmtInv(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}