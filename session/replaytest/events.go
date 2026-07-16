// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"encoding/json"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// FixedTimestamp is used by all deterministic fixtures.
var FixedTimestamp = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

// DefaultApp / DefaultUser are shared fixture identity values.
const (
	DefaultApp  = "replaytest"
	DefaultUser = "user"
)

// SessionKeyFor returns a per-case session key.
func SessionKeyFor(caseName string) session.Key {
	return session.Key{
		AppName:   DefaultApp,
		UserID:    DefaultUser,
		SessionID: "session-" + caseName,
	}
}

// UserKeyDefault returns the default session user key.
func UserKeyDefault() session.UserKey {
	return session.UserKey{AppName: DefaultApp, UserID: DefaultUser}
}

// MemoryUserKeyDefault returns the default memory user key.
func MemoryUserKeyDefault() memory.UserKey {
	return memory.UserKey{AppName: DefaultApp, UserID: DefaultUser}
}

// UserEvent builds a tagged user message event.
func UserEvent(key, content string, opts ...event.Option) *event.Event {
	opts = append([]event.Option{event.WithTag(key)}, opts...)
	evt := event.NewResponseEvent("inv", "user", &model.Response{
		Object:  model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{Message: model.NewUserMessage(content)}},
	}, opts...)
	evt.Timestamp = FixedTimestamp
	return evt
}

// AssistantEvent builds a tagged assistant message event.
func AssistantEvent(key, content string, opts ...event.Option) *event.Event {
	opts = append([]event.Option{event.WithTag(key)}, opts...)
	evt := event.NewResponseEvent("inv", "assistant", &model.Response{
		Object:  model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{Message: model.NewAssistantMessage(content)}},
	}, opts...)
	evt.Timestamp = FixedTimestamp
	return evt
}

// BranchEvent builds a branch-scoped event for concurrent cases.
func BranchEvent(key, branch, content string) *event.Event {
	evt := UserEvent(key, content, event.WithBranch(branch))
	evt.Author = branch
	evt.FilterKey = branch
	return evt
}

// ToolCallEvent builds an assistant tool-call event.
func ToolCallEvent(key string) *event.Event {
	evt := AssistantEvent(key, "")
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

// ToolResponseEvent builds a tool response event.
func ToolResponseEvent(key string) *event.Event {
	evt := event.NewResponseEvent("inv", "tool", &model.Response{
		Object: model.ObjectTypeToolResponse,
		Choices: []model.Choice{{
			Message: model.NewToolMessage("call_weather", "weather", `{"temp":30}`),
		}},
	}, event.WithTag(key))
	evt.Timestamp = FixedTimestamp
	return evt
}

// StateDeltaEvent builds an event carrying a state delta.
func StateDeltaEvent(key string, delta map[string][]byte) *event.Event {
	evt := AssistantEvent(key, "state updated", event.WithStateDelta(delta))
	return evt
}

// TrackPayload builds a track event with fixed timestamp.
func TrackPayload(track session.Track, payload string) *session.TrackEvent {
	return &session.TrackEvent{
		Track:     track,
		Payload:   json.RawMessage(payload),
		Timestamp: FixedTimestamp,
	}
}
