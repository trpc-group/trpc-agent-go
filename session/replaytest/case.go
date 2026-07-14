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

// OpType enumerates the supported operations.
type OpType string

const (
	OpCreateSession      OpType = "create_session"
	OpGetSession         OpType = "get_session"
	OpDeleteSession      OpType = "delete_session"
	OpAppendEvent        OpType = "append_event"
	OpUpdateSessionState OpType = "update_session_state"
	OpCreateSummary      OpType = "create_summary"
	OpGetSummaryText     OpType = "get_summary_text"
	OpAppendTrackEvent   OpType = "append_track_event"
	OpGetTrackEvents     OpType = "get_track_events"
	OpAddMemory          OpType = "add_memory"
	OpReadMemories       OpType = "read_memories"
	OpSearchMemories     OpType = "search_memories"
	OpUpdateMemory       OpType = "update_memory"
	OpDeleteMemory       OpType = "delete_memory"
	OpClearMemories      OpType = "clear_memories"
	OpGetSummary         OpType = "get_summary"
	OpUpdateAppState     OpType = "update_app_state"
	OpDeleteAppState     OpType = "delete_app_state"
	OpListAppStates      OpType = "list_app_states"
	OpUpdateUserState    OpType = "update_user_state"
	OpDeleteUserState    OpType = "delete_user_state"
	OpListUserStates     OpType = "list_user_states"
)

// Op represents a single atomic operation to execute on a backend.
type Op struct {
	Type        OpType              `json:"type"`
	Description string              `json:"description,omitempty"`
	Key         session.Key         `json:"key,omitempty"`
	AppName     string              `json:"app_name,omitempty"`
	UserKey     memory.UserKey      `json:"user_key,omitempty"`
	SessUserKey session.UserKey     `json:"sess_user_key,omitempty"`
	State       session.StateMap    `json:"state,omitempty"`
	DeleteKey   string              `json:"delete_key,omitempty"`
	Event       *event.Event        `json:"event,omitempty"`
	TrackEvent  *session.TrackEvent `json:"track_event,omitempty"`
	Track       session.Track       `json:"track,omitempty"`
	FilterKey   string              `json:"filter_key,omitempty"`
	Force       bool                `json:"force,omitempty"`
	Async       bool                `json:"async,omitempty"`
	EventNum    int                 `json:"event_num,omitempty"`
	EventTime   time.Time           `json:"event_time,omitempty"`
	MemoryStr   string              `json:"memory_str,omitempty"`
	Topics      []string            `json:"topics,omitempty"`
	MemoryKey   memory.Key          `json:"memory_key,omitempty"`
	Limit       int                 `json:"limit,omitempty"`
	Query       string              `json:"query,omitempty"`
	MemoryMeta  *memory.Metadata    `json:"memory_meta,omitempty"`
}

// --- Event constructors ---

func sessKey(id string) session.Key {
	return session.Key{AppName: "replay-test", UserID: "user", SessionID: id}
}

func userKey() memory.UserKey {
	return memory.UserKey{AppName: "replay-test", UserID: "user"}
}

// newResponseEvent creates an event with a model.Response payload.
// This is the shared boilerplate for all response-based event constructors.
func newResponseEvent(author string, msg model.Message) *event.Event {
	return event.New("inv-001", author,
		event.WithResponse(&model.Response{
			Object:  model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{Index: 0, Message: msg}},
		}),
	)
}

func newUserEvent(content string) *event.Event {
	return newResponseEvent("user", model.Message{Role: model.RoleUser, Content: content})
}

func newAssistantEvent(content string) *event.Event {
	return newResponseEvent("assistant", model.Message{Role: model.RoleAssistant, Content: content})
}

func newAssistantEventWithExtensions(content string, extensions map[string]json.RawMessage) *event.Event {
	e := newResponseEvent("assistant", model.Message{Role: model.RoleAssistant, Content: content})
	e.Extensions = extensions
	return e
}

func newAssistantEventWithStateDelta(content string, delta map[string][]byte) *event.Event {
	e := newResponseEvent("assistant", model.Message{Role: model.RoleAssistant, Content: content})
	e.StateDelta = delta
	return e
}

func newToolCallEvent(toolName, argsJSON, toolCallID string) *event.Event {
	return newResponseEvent("assistant", model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			ID:   toolCallID,
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      toolName,
				Arguments: []byte(argsJSON),
			},
		}},
	})
}

func newToolResponseEvent(toolCallID, toolName, resultJSON string) *event.Event {
	return newResponseEvent("tool", model.Message{
		Role:    model.RoleTool,
		Content: resultJSON,
		ToolID:  toolCallID,
	})
}

func newEventWithBranchTagFilterKey(author, branch, tag, filterKey, content string) *event.Event {
	e := newResponseEvent(author, model.Message{Role: model.Role(author), Content: content})
	e.Branch = branch
	e.Tag = tag
	e.FilterKey = filterKey
	return e
}

func newTrackEvent(track string, payloadJSON string) *session.TrackEvent {
	return &session.TrackEvent{
		Track:     session.Track(track),
		Payload:   json.RawMessage(payloadJSON),
		Timestamp: time.Now(),
	}
}

// errStr returns the error message or empty string.
func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
