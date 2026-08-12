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
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ---------------------------------------------------------------------------
// Event construction helpers
// ---------------------------------------------------------------------------

func mkUserEvent(content string) *event.Event {
	return &event.Event{
		Author:    "user",
		ID:        uuid.NewString(),
		Timestamp: time.Now(),
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleUser, Content: content}},
			},
		},
	}
}

func mkAssistantEvent(content string) *event.Event {
	return &event.Event{
		Author:    "agent",
		ID:        uuid.NewString(),
		Timestamp: time.Now(),
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleAssistant, Content: content}},
			},
		},
	}
}

func mkToolCallEvent(toolName, toolID, args string) *event.Event {
	return &event.Event{
		Author:    "agent",
		ID:        uuid.NewString(),
		Timestamp: time.Now(),
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								Type: "function",
								ID:   toolID,
								Function: model.FunctionDefinitionParam{
									Name:      toolName,
									Arguments: []byte(args),
								},
							},
						},
					},
				},
			},
		},
	}
}

func mkToolResponseEvent(toolID, toolName, content string) *event.Event {
	return &event.Event{
		Author:    "tool",
		ID:        uuid.NewString(),
		Timestamp: time.Now(),
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:     model.RoleTool,
						Content:  content,
						ToolID:   toolID,
						ToolName: toolName,
					},
				},
			},
		},
	}
}

func mkTrackEvent(track, payload string) *session.TrackEvent {
	return &session.TrackEvent{
		Track:     session.Track(track),
		Payload:   json.RawMessage(payload),
		Timestamp: time.Now(),
	}
}

// ---------------------------------------------------------------------------
// Public replay cases (10+)
// ---------------------------------------------------------------------------

// AllReplayCases returns the full set of replay cases.
func AllReplayCases() []ReplayCase {
	return []ReplayCase{
		caseSingleTurnConversation(),
		caseMultiTurnConversation(),
		caseToolCallConversation(),
		caseStateUpdates(),
		caseMemoryWriteRead(),
		caseSummaryGeneration(),
		caseSummaryWithEventTruncation(),
		caseTrackEvents(),
		caseConcurrentWrites(),
		caseErrorRecovery(),
		caseStateDeleteAndClear(),
		caseMultipleMemoryEntries(),
	}
}

// 1. Single-turn conversation: one user message + one assistant reply.
func caseSingleTurnConversation() ReplayCase {
	return ReplayCase{
		Name:         "single_turn_conversation",
		Description:  "One user message and one assistant reply",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpAppendEvent, Event: mkUserEvent("Hello, how are you?")},
			{Type: OpAppendEvent, Event: mkAssistantEvent("I'm doing great, thanks!")},
		},
	}
}

// 2. Multi-turn conversation: 3 rounds of user/assistant exchange.
func caseMultiTurnConversation() ReplayCase {
	return ReplayCase{
		Name:         "multi_turn_conversation",
		Description:  "Three rounds of user/assistant exchange",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpAppendEvent, Event: mkUserEvent("What is Go?")},
			{Type: OpAppendEvent, Event: mkAssistantEvent("Go is a programming language.")},
			{Type: OpAppendEvent, Event: mkUserEvent("Who created it?")},
			{Type: OpAppendEvent, Event: mkAssistantEvent("It was created at Google.")},
			{Type: OpAppendEvent, Event: mkUserEvent("What year?")},
			{Type: OpAppendEvent, Event: mkAssistantEvent("Go was announced in 2009.")},
		},
	}
}

// 3. Tool call conversation: assistant calls a tool, gets a response.
func caseToolCallConversation() ReplayCase {
	toolID := "call_abc123"
	return ReplayCase{
		Name:         "tool_call_conversation",
		Description:  "Tool call with args, tool response, and follow-up",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpAppendEvent, Event: mkUserEvent("What's the weather in Beijing?")},
			{Type: OpAppendEvent, Event: mkToolCallEvent("get_weather", toolID, `{"city":"Beijing"}`)},
			{Type: OpAppendEvent, Event: mkToolResponseEvent(toolID, "get_weather", `{"temp":25,"unit":"C"}`)},
			{Type: OpAppendEvent, Event: mkAssistantEvent("The weather in Beijing is 25°C.")},
		},
	}
}

// 4. State updates: write, overwrite, and delete state keys.
func caseStateUpdates() ReplayCase {
	return ReplayCase{
		Name:         "state_updates",
		Description:  "Write, overwrite, and delete session state keys",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpUpdateSessionState, StateMap: session.StateMap{
				"counter":  []byte("1"),
				"language": []byte("go"),
			}},
			{Type: OpUpdateSessionState, StateMap: session.StateMap{
				"counter": []byte("2"),
				"mode":    []byte("fast"),
			}},
			{Type: OpDeleteSessionState, StateKey: "language"},
			{Type: OpAppendEvent, Event: mkUserEvent("check state")},
		},
	}
}

// 5. Memory write and read.
func caseMemoryWriteRead() ReplayCase {
	return ReplayCase{
		Name:        "memory_write_read",
		Description: "Add memory entries and verify they are readable",
		Operations: []ReplayOperation{
			{Type: OpAddMemory, MemoryContent: "User prefers dark mode", MemoryTopics: []string{"preference"}},
			{Type: OpAddMemory, MemoryContent: "User is a Go developer", MemoryTopics: []string{"background"}},
		},
	}
}

// 6. Summary generation: add events then trigger summary creation.
func caseSummaryGeneration() ReplayCase {
	return ReplayCase{
		Name:         "summary_generation",
		Description:  "Append events then trigger session summary",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpAppendEvent, Event: mkUserEvent("Let's talk about Go.")},
			{Type: OpAppendEvent, Event: mkAssistantEvent("Sure, Go is great!")},
			{Type: OpAppendEvent, Event: mkUserEvent("What about concurrency?")},
			{Type: OpAppendEvent, Event: mkAssistantEvent("Go has goroutines and channels.")},
			{Type: OpCreateSummary, SummaryFilterKey: "", SummaryForce: true},
		},
	}
}

// 7. Summary with event truncation: long conversation followed by summary.
func caseSummaryWithEventTruncation() ReplayCase {
	ops := []ReplayOperation{}
	for i := 0; i < 8; i++ {
		ops = append(ops, ReplayOperation{Type: OpAppendEvent, Event: mkUserEvent("Question " + string(rune('A'+i)))})
		ops = append(ops, ReplayOperation{Type: OpAppendEvent, Event: mkAssistantEvent("Answer " + string(rune('A'+i)))})
	}
	ops = append(ops, ReplayOperation{Type: OpCreateSummary, SummaryFilterKey: "", SummaryForce: true})
	ops = append(ops, ReplayOperation{Type: OpAppendEvent, Event: mkUserEvent("One more question after summary")})
	ops = append(ops, ReplayOperation{Type: OpAppendEvent, Event: mkAssistantEvent("Final answer after summary")})

	return ReplayCase{
		Name:         "summary_event_truncation",
		Description:  "Long conversation compressed with summary, then new events appended",
		SkipMemories: true,
		Operations:   ops,
	}
}

// 8. Track events: append track events for tool timing and errors.
func caseTrackEvents() ReplayCase {
	return ReplayCase{
		Name:         "track_events",
		Description:  "Append track events for tool execution timing and error records",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpAppendEvent, Event: mkUserEvent("Run the tool")},
			{Type: OpAppendTrackEvent, TrackEvent: mkTrackEvent("tool_exec", `{"tool":"search","duration_ms":120,"status":"ok"}`)},
			{Type: OpAppendTrackEvent, TrackEvent: mkTrackEvent("tool_exec", `{"tool":"search","duration_ms":350,"status":"ok"}`)},
			{Type: OpAppendTrackEvent, TrackEvent: mkTrackEvent("subtask", `{"task":"validate","status":"error","error":"timeout"}`)},
		},
	}
}

// 9. Concurrent / interleaved writes: simulate multiple tool calls.
func caseConcurrentWrites() ReplayCase {
	return ReplayCase{
		Name:         "concurrent_writes",
		Description:  "Multiple interleaved tool call and response events",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpAppendEvent, Event: mkUserEvent("Run parallel tasks")},
			{Type: OpAppendEvent, Event: mkToolCallEvent("task_a", "call_a", `{"x":1}`)},
			{Type: OpAppendEvent, Event: mkToolCallEvent("task_b", "call_b", `{"x":2}`)},
			{Type: OpAppendEvent, Event: mkToolResponseEvent("call_a", "task_a", `{"result":"a_done"}`)},
			{Type: OpAppendEvent, Event: mkToolResponseEvent("call_b", "task_b", `{"result":"b_done"}`)},
			{Type: OpAppendEvent, Event: mkAssistantEvent("Both tasks completed.")},
		},
	}
}

// 10. Error recovery: simulate a write failure and retry.
func caseErrorRecovery() ReplayCase {
	return ReplayCase{
		Name:         "error_recovery",
		Description:  "Simulate write failure then retry, verify no duplicate events",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpAppendEvent, Event: mkUserEvent("Before failure")},
			{Type: OpAppendEvent, Event: mkAssistantEvent("OK before")},
			{SimulateWriteError: true},
			{Type: OpAppendEvent, Event: mkUserEvent("After retry")},
			{Type: OpAppendEvent, Event: mkAssistantEvent("OK after")},
		},
	}
}

// 11. State delete and clear: write, delete specific key, then clear all.
func caseStateDeleteAndClear() ReplayCase {
	return ReplayCase{
		Name:         "state_delete_clear",
		Description:  "Write state, delete one key, then overwrite all remaining to nil",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpUpdateSessionState, StateMap: session.StateMap{
				"a": []byte("1"),
				"b": []byte("2"),
				"c": []byte("3"),
			}},
			{Type: OpDeleteSessionState, StateKey: "b"},
			{Type: OpUpdateSessionState, StateMap: session.StateMap{
				"a": nil,
				"c": nil,
			}},
		},
	}
}

// 12. Multiple memory entries: add several, clear, verify empty.
func caseMultipleMemoryEntries() ReplayCase {
	return ReplayCase{
		Name:        "multiple_memory_entries",
		Description: "Add multiple memories, clear all, verify empty",
		Operations: []ReplayOperation{
			{Type: OpAddMemory, MemoryContent: "Memory one", MemoryTopics: []string{"t1"}},
			{Type: OpAddMemory, MemoryContent: "Memory two", MemoryTopics: []string{"t2"}},
			{Type: OpAddMemory, MemoryContent: "Memory three", MemoryTopics: []string{"t3"}},
			{Type: OpClearMemories},
		},
	}
}
