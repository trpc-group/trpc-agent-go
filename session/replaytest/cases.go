//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var defaultSessionKey = session.Key{AppName: "replaytest", UserID: "user", SessionID: "session"}
var defaultUserKey = session.UserKey{AppName: "replaytest", UserID: "user"}
var fixedTime = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

// CaseSingleTurnText covers one user message and one assistant response.
var CaseSingleTurnText = ReplayCase{
	Name:        "single_turn_text",
	Description: "single user and assistant turn",
	Steps: []ReplayStep{
		AppendEventStep{Key: "c1.user.1", Event: userEvent("c1.user.1", "hello")},
		AppendEventStep{Key: "c1.assistant.1", Event: assistantEvent("c1.assistant.1", "hello back")},
		GetSessionStep{Key: "c1.get", SessionKey: defaultSessionKey},
	},
}

// CaseMultiTurnConversation covers sequential multi-turn event ordering.
var CaseMultiTurnConversation = ReplayCase{
	Name:        "multi_turn_conversation",
	Description: "three user assistant turns",
	Steps: []ReplayStep{
		AppendEventStep{Key: "c2.user.1", Event: userEvent("c2.user.1", "u1")},
		AppendEventStep{Key: "c2.assistant.1", Event: assistantEvent("c2.assistant.1", "a1")},
		AppendEventStep{Key: "c2.user.2", Event: userEvent("c2.user.2", "u2")},
		AppendEventStep{Key: "c2.assistant.2", Event: assistantEvent("c2.assistant.2", "a2")},
		AppendEventStep{Key: "c2.user.3", Event: userEvent("c2.user.3", "u3")},
		AppendEventStep{Key: "c2.assistant.3", Event: assistantEvent("c2.assistant.3", "a3")},
		GetSessionStep{Key: "c2.get", SessionKey: defaultSessionKey},
	},
}

// CaseToolCallConversation covers tool-call and tool-response payloads.
var CaseToolCallConversation = ReplayCase{
	Name:        "tool_call_conversation",
	Description: "assistant tool call and tool response",
	Steps: []ReplayStep{
		AppendEventStep{Key: "c3.tool_call.1", Event: toolCallEvent("c3.tool_call.1")},
		AppendEventStep{Key: "c3.tool_response.1", Event: toolResponseEvent("c3.tool_response.1")},
		GetSessionStep{Key: "c3.get", SessionKey: defaultSessionKey},
	},
}

// CaseStateCRUD covers session state updates and overwrite behavior.
var CaseStateCRUD = ReplayCase{
	Name:        "state_crud",
	Description: "session state update and overwrite",
	Steps: []ReplayStep{
		UpdateStateStep{Key: "c4.state.1", Scope: ScopeSession, SessionKey: defaultSessionKey, State: session.StateMap{"color": []byte("blue")}},
		AppendEventStep{Key: "c4.event.1", Event: stateEvent("c4.event.1")},
		UpdateStateStep{Key: "c4.state.2", Scope: ScopeSession, SessionKey: defaultSessionKey, State: session.StateMap{"color": []byte("green")}},
		GetSessionStep{Key: "c4.get", SessionKey: defaultSessionKey},
	},
}

// CaseStateScopes covers app, user, and session state scopes.
var CaseStateScopes = ReplayCase{
	Name:        "state_scopes",
	Description: "app user session scoped state",
	Steps: []ReplayStep{
		UpdateStateStep{Key: "c5.session", Scope: ScopeSession, SessionKey: defaultSessionKey, State: session.StateMap{"session_key": []byte("s")}},
		UpdateStateStep{Key: "c5.app", Scope: ScopeApp, AppName: defaultSessionKey.AppName, State: session.StateMap{"app_key": []byte("a")}},
		UpdateStateStep{Key: "c5.user", Scope: ScopeUser, UserKey: defaultUserKey, State: session.StateMap{"user_key": []byte("u")}},
		ListAppStatesStep{Key: "c5.app.list", AppName: defaultSessionKey.AppName},
		ListUserStatesStep{Key: "c5.user.list", UserKey: defaultUserKey},
		GetSessionStep{Key: "c5.get", SessionKey: defaultSessionKey},
	},
}

// CaseMemoryWriteAndRead covers memory add, read, and search.
var CaseMemoryWriteAndRead = ReplayCase{
	Name:         "memory_write_and_read",
	Description:  "memory add and sentinel search",
	RequiredCaps: RequiredCapabilities{NeedsMemory: true},
	Steps: []ReplayStep{
		AddMemoryStep{Key: "c6.add", UserKey: memoryUserKey(), Memory: "User likes Go replay tests", Topics: []string{"go", "replay"}},
		SearchMemoryStep{Key: "c6.search", UserKey: memoryUserKey(), Query: "Go replay", Limit: 3},
	},
}

// CaseSummaryGeneration covers summary creation request shape.
var CaseSummaryGeneration = ReplayCase{
	Name:        "summary_generation",
	Description: "summary creation",
	Steps: []ReplayStep{
		AppendEventStep{Key: "c7.user.1", Event: userEvent("c7.user.1", "summarize this")},
		AppendEventStep{Key: "c7.assistant.1", Event: assistantEvent("c7.assistant.1", "noted")},
		CreateSummaryStep{Key: "c7.summary", SessionKey: defaultSessionKey, FilterKey: "", Force: true},
		GetSessionStep{Key: "c7.get", SessionKey: defaultSessionKey},
	},
}

// CaseSummaryWithTruncation covers summary boundary plus later events.
var CaseSummaryWithTruncation = ReplayCase{
	Name:        "summary_with_truncation",
	Description: "summary then later event",
	Steps: []ReplayStep{
		AppendEventStep{Key: "c8.user.1", Event: userEvent("c8.user.1", "old")},
		CreateSummaryStep{Key: "c8.summary", SessionKey: defaultSessionKey, FilterKey: "", Force: true},
		AppendEventStep{Key: "c8.user.2", Event: userEvent("c8.user.2", "new")},
		GetSessionStep{Key: "c8.get", SessionKey: defaultSessionKey},
	},
}

// CaseTrackEvents covers track event persistence.
var CaseTrackEvents = ReplayCase{
	Name:         "track_events",
	Description:  "track event append and read",
	RequiredCaps: RequiredCapabilities{NeedsTrack: true},
	Steps: []ReplayStep{
		AppendTrackStep{Key: "c9.track.1", SessionKey: defaultSessionKey, Event: trackEvent("tool", `{"duration_ms":5}`)},
		AppendTrackStep{Key: "c9.track.2", SessionKey: defaultSessionKey, Event: trackEvent("tool", `{"status":"ok"}`)},
		GetSessionStep{Key: "c9.get", SessionKey: defaultSessionKey},
	},
}

// CaseConcurrentInterleaved covers branch-local order under interleaving.
var CaseConcurrentInterleaved = ReplayCase{
	Name:        "concurrent_interleaved_writes",
	Description: "interleaved branch events",
	Steps: []ReplayStep{
		AppendEventStep{Key: "c10.agent_x.step_1", Event: branchEvent("c10.agent_x.step_1", "agent_x", "x1")},
		AppendEventStep{Key: "c10.agent_y.step_1", Event: branchEvent("c10.agent_y.step_1", "agent_y", "y1")},
		AppendEventStep{Key: "c10.agent_x.step_2", Event: branchEvent("c10.agent_x.step_2", "agent_x", "x2")},
		GetSessionStep{Key: "c10.get", SessionKey: defaultSessionKey},
	},
}

// CaseSummaryAsyncPipeline covers async summary enqueue and persistence.
var CaseSummaryAsyncPipeline = ReplayCase{
	Name:         "summary_async_pipeline",
	Description:  "async summary enqueue and persist",
	RequiredCaps: RequiredCapabilities{NeedsAsyncSummary: true},
	Steps: []ReplayStep{
		AppendEventStep{Key: "c11.user.1", Event: userEvent("c11.user.1", "async summary input")},
		AppendEventStep{Key: "c11.assistant.1", Event: assistantEvent("c11.assistant.1", "async summary result")},
		CreateSummaryStep{Key: "c11.enqueue", SessionKey: defaultSessionKey, FilterKey: "", Force: false, Async: true},
	},
}

// CaseMemoryMulti covers multiple memory writes and a sentinel search.
var CaseMemoryMulti = ReplayCase{
	Name:         "memory_multi",
	Description:  "multiple memory add and search",
	RequiredCaps: RequiredCapabilities{NeedsMemory: true},
	Steps: []ReplayStep{
		AddMemoryStep{Key: "c12.add.1", UserKey: memoryUserKey(), Memory: "User prefers dark mode", Topics: []string{"ui"}},
		AddMemoryStep{Key: "c12.add.2", UserKey: memoryUserKey(), Memory: "User writes Go daily", Topics: []string{"coding", "go"}},
		AddMemoryStep{Key: "c12.add.3", UserKey: memoryUserKey(), Memory: "User lives in Shenzhen", Topics: []string{"location"}},
		SearchMemoryStep{Key: "c12.search.coding", UserKey: memoryUserKey(), Query: "Go programming", Limit: 5},
	},
}

// AllCases returns all built-in replay cases.
func AllCases() []ReplayCase {
	return []ReplayCase{
		CaseSingleTurnText,
		CaseMultiTurnConversation,
		CaseToolCallConversation,
		CaseStateCRUD,
		CaseStateScopes,
		CaseMemoryWriteAndRead,
		CaseSummaryGeneration,
		CaseSummaryWithTruncation,
		CaseTrackEvents,
		CaseConcurrentInterleaved,
		CaseSummaryAsyncPipeline,
		CaseMemoryMulti,
	}
}

func userEvent(key, content string) *event.Event {
	evt := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{Message: model.NewUserMessage(content)}},
	}, event.WithTag(key))
	evt.Timestamp = fixedTime
	return evt
}

func assistantEvent(key, content string) *event.Event {
	evt := event.NewResponseEvent("inv", "assistant", &model.Response{
		Choices: []model.Choice{{Message: model.NewAssistantMessage(content)}},
	}, event.WithTag(key))
	evt.Timestamp = fixedTime
	return evt
}

func branchEvent(key, branch, content string) *event.Event {
	evt := assistantEvent(key, content)
	evt.Author = branch
	evt.Branch = branch
	return evt
}

func toolCallEvent(key string) *event.Event {
	evt := assistantEvent(key, "")
	evt.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
		ID:   "call_weather",
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      "weather",
			Arguments: []byte(`{"city":"Shenzhen"}`),
		},
	}}
	return evt
}

func toolResponseEvent(key string) *event.Event {
	evt := event.NewResponseEvent("inv", "tool", &model.Response{
		Object: model.ObjectTypeToolResponse,
		Choices: []model.Choice{{
			Message: model.NewToolMessage("call_weather", "weather", `{"temp":30}`),
		}},
	}, event.WithTag(key))
	evt.Timestamp = fixedTime
	return evt
}

func stateEvent(key string) *event.Event {
	evt := assistantEvent(key, "state updated")
	evt.StateDelta = map[string][]byte{"delta_key": []byte("delta")}
	return evt
}

func trackEvent(track session.Track, payload string) *session.TrackEvent {
	return &session.TrackEvent{
		Track:     track,
		Payload:   json.RawMessage(payload),
		Timestamp: fixedTime,
	}
}

func memoryUserKey() memory.UserKey {
	return memory.UserKey{AppName: defaultUserKey.AppName, UserID: defaultUserKey.UserID}
}
