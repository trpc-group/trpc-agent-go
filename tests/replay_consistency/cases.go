package replayconsistency

import (
	"encoding/json"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func replayNow() time.Time { return time.Now().Add(time.Hour).Truncate(time.Second) }

func DefaultReplayCases() []ReplayCase {
	return []ReplayCase{
		{
			Name:        "single_turn_text",
			Description: "single user message and assistant text event",
			Operations: []Operation{{
				Kind:  OperationKindAppendEvent,
				Event: sampleAssistantEvent("assistant-1", "hello world", "session-a", "user-a", "app-a"),
			}},
		},
		{
			Name:        "multi_turn_sequence",
			Description: "multiple user and assistant events appended in order",
			Operations:  []Operation{{Kind: OperationKindAppendEvent, Event: sampleUserEvent("user-1", "first", "session-a", "user-a", "app-a")}, {Kind: OperationKindAppendEvent, Event: sampleAssistantEvent("assistant-1", "reply-1", "session-a", "user-a", "app-a")}, {Kind: OperationKindAppendEvent, Event: sampleUserEvent("user-2", "second", "session-a", "user-a", "app-a")}, {Kind: OperationKindAppendEvent, Event: sampleAssistantEvent("assistant-2", "reply-2", "session-a", "user-a", "app-a")}},
		},
		{
			Name:        "tool_call_roundtrip",
			Description: "tool call, tool response, and call args extension normalization",
			Operations:  []Operation{{Kind: OperationKindAppendEvent, Event: sampleToolCallEvent("tool-call-1", "search", `{"query":"go replay"}`, "session-a", "user-a", "app-a")}, {Kind: OperationKindAppendEvent, Event: sampleToolResponseEvent("tool-response-1", "search", "ok", "tool-call-1", "session-a", "user-a", "app-a")}},
		},
		{
			Name:        "state_update_cycle",
			Description: "state write, overwrite, delete, and clear semantics",
			Operations:  []Operation{{Kind: OperationKindUpdateState, StatePatch: session.StateMap{"name": []byte("alice"), "step": []byte("1")}}, {Kind: OperationKindUpdateState, StatePatch: session.StateMap{"step": []byte("2")}}, {Kind: OperationKindDeleteState, StateDelete: []string{"name"}}, {Kind: OperationKindClearState, ClearState: true}},
		},
		{
			Name:        "memory_write_read",
			Description: "memory add, update, and search normalization",
			Operations:  []Operation{{Kind: OperationKindAddMemory, MemoryAdd: sampleMemoryWrite("user-a", "pref-1", "prefers concise answers", []string{"preference"})}, {Kind: OperationKindUpdateMemory, MemoryUpdate: sampleMemoryWrite("user-a", "pref-1", "prefers concise and direct answers", []string{"preference", "style"})}},
		},
		{
			Name:        "summary_generation_update",
			Description: "summary content, filter key, version, and updated time comparison",
			Operations: []Operation{
				{Kind: OperationKindAppendEvent, Event: sampleUserEvent("evt-summary-base", "base event for summary", "session-a", "user-a", "app-a")},
				{Kind: OperationKindAppendEvent, Event: sampleBranchEvent("evt-branch-a", "branch specific event", "session-a", "user-a", "branch-a")},
				{Kind: OperationKindCreateSummary, FilterKey: ""},
				{Kind: OperationKindCreateSummary, FilterKey: "branch-a"},
			},
		},
		{
			Name:        "summary_truncation_replay",
			Description: "summary plus truncated events and new continuation events",
			Operations: []Operation{
				{Kind: OperationKindAppendEvent, Event: sampleUserEvent("evt-pre-summary", "event before summary", "session-a", "user-a", "app-a")},
				{Kind: OperationKindCreateSummary, FilterKey: ""},
				{Kind: OperationKindAppendEvent, Event: sampleUserEvent("user-post-summary", "continue", "session-a", "user-a", "app-a")},
			},
		},
		{
			Name:        "track_event_timeline",
			Description: "track event ordering and payload normalization",
			Operations:  []Operation{{Kind: OperationKindAppendTrackEvent, Track: sampleTrackEvent("tool-exec", "{\"duration_ms\":12,\"status\":\"ok\"}")}, {Kind: OperationKindAppendTrackEvent, Track: sampleTrackEvent("subtask", "{\"status\":\"failed\",\"error\":\"boom\"}")}},
		},
		{
			Name:        "interleaved_concurrency",
			Description: "interleaved tool events and branch writes normalized by index and key",
			Operations:  []Operation{{Kind: OperationKindAppendEvent, Event: sampleUserEvent("branch-a-1", "branch a", "session-a", "user-a", "app-a")}, {Kind: OperationKindAppendEvent, Event: sampleUserEvent("branch-b-1", "branch b", "session-a", "user-a", "app-a")}, {Kind: OperationKindAppendEvent, Event: sampleAssistantEvent("branch-a-2", "done a", "session-a", "user-a", "app-a")}},
		},
		{
			Name:        "failure_retry_dedup",
			Description: "duplicate write and retry should not create duplicate normalized output",
			Operations:  []Operation{{Kind: OperationKindAppendEvent, Event: sampleUserEvent("dup-1", "retry me", "session-a", "user-a", "app-a")}, {Kind: OperationKindAppendEvent, Event: sampleUserEvent("dup-1", "retry me", "session-a", "user-a", "app-a")}},
		},
	}
}

func sampleUserEvent(id, content, sessionID, userID, appName string) *event.Event {
	return &event.Event{
		ID:        id,
		Author:    userID,
		Timestamp: replayNow(),
		Branch:    "main",
		FilterKey: appName,
		Version:   event.CurrentVersion,
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: content},
			}},
		},
	}
}

func sampleAssistantEvent(id, content, sessionID, userID, appName string) *event.Event {
	return &event.Event{
		ID:        id,
		Author:    "assistant",
		Timestamp: replayNow(),
		Branch:    "main",
		FilterKey: appName,
		Version:   event.CurrentVersion,
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: content},
			}},
		},
	}
}

func sampleToolCallEvent(id, toolName, args, sessionID, userID, appName string) *event.Event {
	return &event.Event{
		ID:        id,
		Author:    "assistant",
		Timestamp: replayNow(),
		Branch:    "main",
		FilterKey: appName,
		Version:   event.CurrentVersion,
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{{
				Delta: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      toolName,
							Arguments: []byte(args),
						},
					}},
				},
			}},
		},
		Extensions: map[string]json.RawMessage{event.ToolCallArgsExtensionKey: json.RawMessage(args)},
	}
}

func sampleToolResponseEvent(id, toolName, content, toolCallID, sessionID, userID, appName string) *event.Event {
	return &event.Event{
		ID:        id,
		Author:    "tool",
		Timestamp: replayNow(),
		Branch:    "main",
		FilterKey: appName,
		Version:   event.CurrentVersion,
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleTool, Content: content, ToolID: toolCallID, ToolName: toolName},
			}},
		},
	}
}

func sampleMemoryWrite(userID, memoryID, content string, topics []string) *MemoryWrite {
	return &MemoryWrite{UserKey: memory.UserKey{AppName: "app-a", UserID: userID}, MemoryID: memoryID, Content: content, Topics: topics}
}

func sampleSummary(sessionID, filterKey, text string) *session.Summary {
	return &session.Summary{Summary: text, UpdatedAt: replayNow(), Boundary: session.NewSummaryBoundaryWithEventID(filterKey, replayNow(), "event-last")}
}

func sampleTrackEvent(track string, payload string) *session.TrackEvent {
	return &session.TrackEvent{Track: session.Track(track), Timestamp: replayNow(), Payload: json.RawMessage(payload)}
}

func sampleBranchEvent(id, content, sessionID, userID, filterKey string) *event.Event {
	return &event.Event{
		ID:        id,
		Author:    "assistant",
		Timestamp: replayNow(),
		Branch:    "main",
		FilterKey: filterKey,
		Version:   event.CurrentVersion,
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: content},
			}},
		},
	}
}
