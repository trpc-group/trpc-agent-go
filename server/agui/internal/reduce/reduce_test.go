//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reduce

import (
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/multimodal"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	testAppName = "demo-app"
	testUserID  = "user-42"
)

func TestBuildMessagesHappyPath(t *testing.T) {
	events := []session.TrackEvent{
		newTrackEvent(aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user"))),
		newTrackEvent(aguievents.NewTextMessageContentEvent("user-1", "hello ")),
		newTrackEvent(aguievents.NewTextMessageContentEvent("user-1", "world")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("user-1")),
		newTrackEvent(aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant"))),
		newTrackEvent(aguievents.NewTextMessageContentEvent("assistant-1", "thinking")),
		newTrackEvent(aguievents.NewTextMessageContentEvent("assistant-1", "...done")),
		newTrackEvent(aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1"))),
		newTrackEvent(aguievents.NewToolCallArgsEvent("tool-call-1", "{\"a\":1}")),
		newTrackEvent(aguievents.NewToolCallEndEvent("tool-call-1")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("assistant-1")),
		newTrackEvent(aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "42")),
	}

	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	// User message assertions.
	user := msgs[0]
	assert.Equal(t, "user-1", user.ID)
	assert.Equal(t, types.RoleUser, user.Role)
	require.NotNil(t, user.Name)
	assert.Equal(t, testUserID, user.Name)
	require.NotNil(t, user.Content)
	content, ok := user.ContentString()
	require.True(t, ok)
	assert.Equal(t, "hello world", content)
	assert.Empty(t, user.ToolCalls)

	// Assistant message assertions.
	assistant := msgs[1]
	assert.Equal(t, "assistant-1", assistant.ID)
	assert.Equal(t, types.RoleAssistant, assistant.Role)
	require.NotNil(t, assistant.Name)
	assert.Equal(t, testAppName, assistant.Name)
	require.NotNil(t, assistant.Content)
	content, ok = assistant.ContentString()
	require.True(t, ok)
	assert.Equal(t, "thinking...done", content)
	require.Len(t, assistant.ToolCalls, 1)
	call := assistant.ToolCalls[0]
	assert.Equal(t, "tool-call-1", call.ID)
	assert.Equal(t, "function", call.Type)
	assert.Equal(t, "calc", call.Function.Name)
	assert.Equal(t, "{\"a\":1}", call.Function.Arguments)

	// Tool result assertions.
	tool := msgs[2]
	assert.Equal(t, "tool-msg-1", tool.ID)
	assert.Equal(t, types.RoleTool, tool.Role)
	require.NotNil(t, tool.Content)
	content, ok = tool.ContentString()
	require.True(t, ok)
	assert.Equal(t, "42", content)
	require.NotNil(t, tool.ToolCallID)
	assert.Equal(t, "tool-call-1", tool.ToolCallID)
}

func TestReduceUserMessageCustomEventWithInputContents(t *testing.T) {
	user := types.Message{
		ID:   "user-1",
		Role: types.RoleUser,
		Content: []types.InputContent{
			{Type: types.InputContentTypeBinary, MimeType: "image/jpeg", URL: "https://example.com/images/1.jpeg"},
			{Type: types.InputContentTypeText, Text: "图中有哪些信息?"},
		},
	}
	customEvent := aguievents.NewCustomEvent(multimodal.CustomEventNameUserMessage, aguievents.WithValue(user))
	msgs, err := Reduce(testAppName, testUserID, trackEventsFrom(customEvent))
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	got := msgs[0]
	assert.Equal(t, "user-1", got.ID)
	assert.Equal(t, types.RoleUser, got.Role)
	require.NotNil(t, got.Name)
	assert.Equal(t, testUserID, got.Name)

	contents, ok := got.ContentInputContents()
	require.True(t, ok)
	require.Len(t, contents, 2)
	assert.Equal(t, types.InputContentTypeBinary, contents[0].Type)
	assert.Equal(t, "image/jpeg", contents[0].MimeType)
	assert.Equal(t, "https://example.com/images/1.jpeg", contents[0].URL)
	assert.Equal(t, types.InputContentTypeText, contents[1].Type)
	assert.Equal(t, "图中有哪些信息?", contents[1].Text)
}

func TestReduceUserMessageCustomEventWithStringContent(t *testing.T) {
	user := types.Message{
		ID:      "user-1",
		Role:    types.RoleUser,
		Name:    "alice",
		Content: "hi",
	}
	customEvent := aguievents.NewCustomEvent(multimodal.CustomEventNameUserMessage, aguievents.WithValue(user))
	msgs, err := Reduce(testAppName, testUserID, trackEventsFrom(customEvent))
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	got := msgs[0]
	assert.Equal(t, "user-1", got.ID)
	assert.Equal(t, types.RoleUser, got.Role)
	assert.Equal(t, "alice", got.Name)
	content, ok := got.ContentString()
	require.True(t, ok)
	assert.Equal(t, "hi", content)
}

func TestHandleUserMessageCustomEventErrors(t *testing.T) {
	tests := []struct {
		name  string
		event *aguievents.CustomEvent
		want  string
	}{
		{
			name:  "missing value",
			event: aguievents.NewCustomEvent(multimodal.CustomEventNameUserMessage),
			want:  "user message custom event missing value",
		},
		{
			name:  "marshal value error",
			event: aguievents.NewCustomEvent(multimodal.CustomEventNameUserMessage, aguievents.WithValue(make(chan int))),
			want:  "marshal user message custom event value",
		},
		{
			name:  "unmarshal value error",
			event: aguievents.NewCustomEvent(multimodal.CustomEventNameUserMessage, aguievents.WithValue("invalid")),
			want:  "unmarshal user message custom event value",
		},
		{
			name: "role mismatch",
			event: aguievents.NewCustomEvent(
				multimodal.CustomEventNameUserMessage,
				aguievents.WithValue(types.Message{ID: "msg-1", Role: types.RoleAssistant, Content: "hi"}),
			),
			want: "user message custom event role must be user",
		},
		{
			name: "missing message id",
			event: aguievents.NewCustomEvent(
				multimodal.CustomEventNameUserMessage,
				aguievents.WithValue(types.Message{Role: types.RoleUser, Content: "hi"}),
			),
			want: "user message custom event missing message id",
		},
		{
			name: "invalid content",
			event: aguievents.NewCustomEvent(
				multimodal.CustomEventNameUserMessage,
				aguievents.WithValue(types.Message{ID: "msg-1", Role: types.RoleUser, Content: map[string]any{"invalid": "payload"}}),
			),
			want: "user message custom event content is invalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := new(testAppName, testUserID)
			err := r.reduceEvent(tt.event)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestReduceReturnsMessagesOnReduceError(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")),
		aguievents.NewTextMessageContentEvent("user-1", "hello"),
		aguievents.NewTextMessageEndEvent("user-1"),
		aguievents.NewTextMessageContentEvent("user-1", "!"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reduce: text message content after end: user-1")
	require.Len(t, msgs, 1)
	require.NotNil(t, msgs[0].Content)
	content, ok := msgs[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "hello", content)
}

func TestReduceAllowsUnclosedTextMessage(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")),
		aguievents.NewTextMessageContentEvent("user-1", "hello"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.NotNil(t, msgs[0].Content)
	content, ok := msgs[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "hello", content)
}

func TestReduceReasoningMessageLifecycle(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageStartEvent("reasoning-msg-1", "assistant"),
		aguievents.NewReasoningMessageContentEvent("reasoning-msg-1", "a"),
		aguievents.NewReasoningMessageContentEvent("reasoning-msg-1", "b"),
		aguievents.NewReasoningMessageEndEvent("reasoning-msg-1"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	msg := msgs[0]
	assert.Equal(t, "reasoning-msg-1", msg.ID)
	assert.Equal(t, types.RoleReasoning, msg.Role)
	require.NotNil(t, msg.Name)
	assert.Equal(t, testAppName, msg.Name)
	content, ok := msg.ContentString()
	require.True(t, ok)
	assert.Equal(t, "ab", content)
}

func TestReduceReasoningMessageLifecycleWithStartEndEvents(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningStartEvent("reasoning-msg-1"),
		aguievents.NewReasoningMessageStartEvent("reasoning-msg-1", "assistant"),
		aguievents.NewReasoningMessageContentEvent("reasoning-msg-1", "summary"),
		aguievents.NewReasoningMessageEndEvent("reasoning-msg-1"),
		aguievents.NewReasoningEndEvent("reasoning-msg-1"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	content, ok := msgs[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "summary", content)
}

func TestReduceReasoningMessageStartDefaultsRole(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageStartEvent("reasoning-msg-1", ""),
		aguievents.NewReasoningMessageContentEvent("reasoning-msg-1", "hello"),
		aguievents.NewReasoningMessageEndEvent("reasoning-msg-1"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, types.RoleReasoning, msgs[0].Role)
}

func TestReduceReasoningMessageStartDuplicate(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageStartEvent("reasoning-msg-1", "assistant"),
		aguievents.NewReasoningMessageStartEvent("reasoning-msg-1", "assistant"),
	)
	assertReduceError(t, events, "duplicate reasoning message start: reasoning-msg-1")
}

func TestReduceReasoningMessageStartMissingID(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageStartEvent("", "assistant"),
	)
	assertReduceError(t, events, "reasoning message start missing id")
}

func TestReduceReasoningMessageRoleMustBeAssistant(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageStartEvent("reasoning-msg-1", "user"),
	)
	assertReduceError(t, events, "unsupported role: user")
}

func TestReduceReasoningContentWithoutStart(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageContentEvent("reasoning-msg-1", "hello"),
	)
	assertReduceError(t, events, "reasoning message content without start: reasoning-msg-1")
}

func TestReduceReasoningContentAfterEnd(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageStartEvent("reasoning-msg-1", "assistant"),
		aguievents.NewReasoningMessageEndEvent("reasoning-msg-1"),
		aguievents.NewReasoningMessageContentEvent("reasoning-msg-1", "late"),
	)
	assertReduceError(t, events, "reasoning message content after end: reasoning-msg-1")
}

func TestReduceReasoningEndWithoutStart(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageEndEvent("reasoning-msg-1"),
	)
	assertReduceError(t, events, "reasoning message end without start: reasoning-msg-1")
}

func TestReduceReasoningEndDuplicate(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageStartEvent("reasoning-msg-1", "assistant"),
		aguievents.NewReasoningMessageEndEvent("reasoning-msg-1"),
		aguievents.NewReasoningMessageEndEvent("reasoning-msg-1"),
	)
	assertReduceError(t, events, "duplicate reasoning message end: reasoning-msg-1")
}

func TestReduceAllowsUnclosedReasoningMessage(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageStartEvent("reasoning-msg-1", "assistant"),
		aguievents.NewReasoningMessageContentEvent("reasoning-msg-1", "hello"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.NotNil(t, msgs[0].Content)
	content, ok := msgs[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "hello", content)
}

func TestReduceReasoningMessageChunk(t *testing.T) {
	messageID := "reasoning-msg-1"
	delta := "chunk"
	chunk := aguievents.NewReasoningMessageChunkEvent(&messageID, &delta)
	msgs, err := Reduce(testAppName, testUserID, trackEventsFrom(chunk))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "reasoning-msg-1", msgs[0].ID)
	assert.Equal(t, types.RoleReasoning, msgs[0].Role)
	require.NotNil(t, msgs[0].Name)
	assert.Equal(t, testAppName, msgs[0].Name)
	content, ok := msgs[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "chunk", content)
}

func TestReduceReasoningMessageChunkUsesPreviousID(t *testing.T) {
	messageID := "reasoning-msg-1"
	first := "a"
	second := "b"
	start := aguievents.NewReasoningMessageChunkEvent(&messageID, &first)
	next := aguievents.NewReasoningMessageChunkEvent(nil, &second)
	msgs, err := Reduce(testAppName, testUserID, trackEventsFrom(start, next))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "reasoning-msg-1", msgs[0].ID)
	content, ok := msgs[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "ab", content)
}

func TestReduceReasoningMessageChunkEmptyDeltaClosesMessage(t *testing.T) {
	messageID := "reasoning-msg-1"
	first := "a"
	end := ""
	after := "b"

	start := aguievents.NewReasoningMessageChunkEvent(&messageID, &first)
	close := aguievents.NewReasoningMessageChunkEvent(&messageID, &end)
	next := aguievents.NewReasoningMessageChunkEvent(&messageID, &after)

	msgs, err := Reduce(testAppName, testUserID, trackEventsFrom(start, close, next))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reasoning message chunk after end: reasoning-msg-1")
	require.Len(t, msgs, 1)
	content, ok := msgs[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "a", content)
}

func TestReduceReasoningMessageChunkRequiresIDInitially(t *testing.T) {
	delta := "chunk"
	chunk := aguievents.NewReasoningMessageChunkEvent(nil, &delta)
	assertReduceError(t, trackEventsFrom(chunk), "reasoning message chunk missing id")
}

func TestReduceReasoningMessageChunkClearsContextOnEnd(t *testing.T) {
	messageID := "reasoning-msg-1"
	delta := "a"
	next := "b"

	events := trackEventsFrom(
		aguievents.NewReasoningMessageStartEvent(messageID, "assistant"),
		aguievents.NewReasoningMessageChunkEvent(&messageID, &delta),
		aguievents.NewReasoningMessageEndEvent(messageID),
		aguievents.NewReasoningMessageChunkEvent(nil, &next),
	)
	assertReduceError(t, events, "reasoning message chunk missing id")
}

func TestReduceReasoningMessageChunkAllowsNilDelta(t *testing.T) {
	messageID := "reasoning-msg-1"
	chunk := aguievents.NewReasoningMessageChunkEvent(&messageID, nil)
	msgs, err := Reduce(testAppName, testUserID, trackEventsFrom(chunk))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "reasoning-msg-1", msgs[0].ID)
	assert.Nil(t, msgs[0].Content)
}

func TestReduceReasoningEncryptedValue(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningMessageStartEvent("reasoning-msg-1", "assistant"),
		aguievents.NewReasoningMessageContentEvent("reasoning-msg-1", "summary"),
		aguievents.NewReasoningMessageEndEvent("reasoning-msg-1"),
		aguievents.NewReasoningEncryptedValueEvent(aguievents.ReasoningEncryptedValueSubtypeMessage, "reasoning-msg-1", "encrypted"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	content, ok := msgs[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "summary", content)
	assert.Equal(t, "encrypted", msgs[0].EncryptedValue)
}

func TestReduceReasoningEncryptedValueCreatesMessageWhenMissing(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningEncryptedValueEvent(aguievents.ReasoningEncryptedValueSubtypeMessage, "reasoning-msg-1", "encrypted"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "reasoning-msg-1", msgs[0].ID)
	assert.Equal(t, types.RoleReasoning, msgs[0].Role)
	assert.Equal(t, "encrypted", msgs[0].EncryptedValue)
}

func TestReduceReasoningEncryptedValueIgnoresToolCallSubtype(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningEncryptedValueEvent(aguievents.ReasoningEncryptedValueSubtypeToolCall, "tool-call-1", "encrypted"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Empty(t, msgs)
}

func TestReduceReasoningEncryptedValueMissingEntityID(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningEncryptedValueEvent(aguievents.ReasoningEncryptedValueSubtypeMessage, "", "encrypted"),
	)
	assertReduceError(t, events, "reasoning encrypted value missing entity id")
}

func TestReduceReasoningEncryptedValueMissingEncryptedValue(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewReasoningEncryptedValueEvent(aguievents.ReasoningEncryptedValueSubtypeMessage, "reasoning-msg-1", ""),
	)
	assertReduceError(t, events, "reasoning encrypted value missing encrypted value")
}

func TestReduceAllowsUnclosedToolCallArgs(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
		aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
		aguievents.NewToolCallArgsEvent("tool-call-1", "{\"a\":"),
		aguievents.NewToolCallArgsEvent("tool-call-1", "1}"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].ToolCalls, 1)
	assert.Equal(t, "tool-call-1", msgs[0].ToolCalls[0].ID)
	assert.Equal(t, "calc", msgs[0].ToolCalls[0].Function.Name)
	assert.Equal(t, "{\"a\":1}", msgs[0].ToolCalls[0].Function.Arguments)
}

func TestHandleTextChunkSuccess(t *testing.T) {
	tests := []struct {
		name        string
		chunk       *aguievents.TextMessageChunkEvent
		wantRole    string
		wantName    string
		wantContent string
	}{
		{
			name:        "assistant default role empty delta",
			chunk:       aguievents.NewTextMessageChunkEvent(stringPtr("msg-1"), stringPtr("assistant"), stringPtr("")),
			wantRole:    "assistant",
			wantName:    testAppName,
			wantContent: "",
		},
		{
			name:        "user role with delta",
			chunk:       aguievents.NewTextMessageChunkEvent(stringPtr("msg-2"), stringPtr("user"), stringPtr("hi")),
			wantRole:    "user",
			wantName:    testUserID,
			wantContent: "hi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := new(testAppName, testUserID)
			require.NoError(t, r.handleTextChunk(tt.chunk))
			require.Len(t, r.messages, 1)
			msg := r.messages[0]
			assert.Equal(t, types.Role(tt.wantRole), msg.Role)
			require.NotNil(t, msg.Name)
			assert.Equal(t, tt.wantName, msg.Name)
			require.NotNil(t, msg.Content)
			content, ok := msg.ContentString()
			require.True(t, ok)
			assert.Equal(t, tt.wantContent, content)
			require.NotNil(t, tt.chunk.MessageID)
			state, ok := r.texts[*tt.chunk.MessageID]
			require.True(t, ok)
			assert.Equal(t, textEnded, state.phase)
			assert.Equal(t, tt.wantContent, state.content.String())
			assert.Equal(t, 0, state.index)
		})
	}
}

func stringPtr(s string) *string {
	return &s
}

func TestHandleTextChunkErrors(t *testing.T) {
	t.Run("missing id", func(t *testing.T) {
		chunk := aguievents.NewTextMessageChunkEvent(stringPtr(""), stringPtr("assistant"), stringPtr(""))
		r := new(testAppName, testUserID)
		err := r.handleTextChunk(chunk)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "text message chunk missing id")
	})
	t.Run("duplicate id", func(t *testing.T) {
		chunk := aguievents.NewTextMessageChunkEvent(stringPtr("msg-1"), stringPtr("assistant"), stringPtr(""))
		r := new(testAppName, testUserID)
		require.NoError(t, r.handleTextChunk(chunk))
		err := r.handleTextChunk(chunk)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate text message chunk: msg-1")
	})
	t.Run("unsupported role", func(t *testing.T) {
		chunk := aguievents.NewTextMessageChunkEvent(stringPtr("msg-3"), stringPtr("tool"), stringPtr(""))
		r := new(testAppName, testUserID)
		err := r.handleTextChunk(chunk)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported role: tool")
	})
	t.Run("empty string id pointer", func(t *testing.T) {
		chunk := aguievents.NewTextMessageChunkEvent(stringPtr(""), stringPtr("assistant"), stringPtr(""))
		empty := ""
		chunk.MessageID = &empty
		r := new(testAppName, testUserID)
		err := r.handleTextChunk(chunk)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "text message chunk missing id")
	})
}

func TestReduceEventDispatchesChunk(t *testing.T) {
	r := new(testAppName, testUserID)
	chunk := aguievents.NewTextMessageChunkEvent(stringPtr("msg-1"), stringPtr("assistant"), stringPtr("hi"))
	require.NoError(t, r.reduceEvent(chunk))
	require.Len(t, r.messages, 1)
	require.NotNil(t, r.messages[0].Content)
	content, ok := r.messages[0].ContentString()
	require.True(t, ok)
	assert.Equal(t, "hi", content)
}

func TestAssistantOnlyToolCall(t *testing.T) {
	events := []session.TrackEvent{
		newTrackEvent(aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant"))),
		newTrackEvent(aguievents.NewToolCallStartEvent("tool-call-1", "search", aguievents.WithParentMessageID("assistant-1"))),
		newTrackEvent(aguievents.NewToolCallArgsEvent("tool-call-1", "{}")),
		newTrackEvent(aguievents.NewToolCallEndEvent("tool-call-1")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("assistant-1")),
		newTrackEvent(aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "reply")),
	}

	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assistant := msgs[0]
	require.Len(t, assistant.ToolCalls, 1)
	call := assistant.ToolCalls[0]
	assert.Equal(t, "tool-call-1", call.ID)
	assert.Equal(t, "search", call.Function.Name)
	assert.Equal(t, "{}", call.Function.Arguments)
	tool := msgs[1]
	require.NotNil(t, tool.ToolCallID)
	assert.Equal(t, "tool-call-1", tool.ToolCallID)
	require.NotNil(t, tool.Content)
	content, ok := tool.ContentString()
	require.True(t, ok)
	assert.Equal(t, "reply", content)
}

func TestAssistantInterleavesTextAfterToolEnd(t *testing.T) {
	events := []session.TrackEvent{
		newTrackEvent(aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant"))),
		newTrackEvent(aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1"))),
		newTrackEvent(aguievents.NewToolCallArgsEvent("tool-call-1", "{\"x\":1}")),
		newTrackEvent(aguievents.NewToolCallEndEvent("tool-call-1")),
		newTrackEvent(aguievents.NewTextMessageContentEvent("assistant-1", "waiting")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("assistant-1")),
		newTrackEvent(aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "done")),
	}

	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assistant := msgs[0]
	require.NotNil(t, assistant.Content)
	content, ok := assistant.ContentString()
	require.True(t, ok)
	assert.Equal(t, "waiting", content)
	require.Len(t, assistant.ToolCalls, 1)
	assert.Equal(t, "{\"x\":1}", assistant.ToolCalls[0].Function.Arguments)
	tool := msgs[1]
	require.NotNil(t, tool.Content)
	content, ok = tool.ContentString()
	require.True(t, ok)
	assert.Equal(t, "done", content)
	require.NotNil(t, tool.ToolCallID)
	assert.Equal(t, "tool-call-1", tool.ToolCallID)
}

func TestAssistantMultipleToolCalls(t *testing.T) {
	events := []session.TrackEvent{
		newTrackEvent(aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant"))),
		newTrackEvent(aguievents.NewToolCallStartEvent("call-1", "alpha", aguievents.WithParentMessageID("assistant-1"))),
		newTrackEvent(aguievents.NewToolCallArgsEvent("call-1", "{\"step\":1}")),
		newTrackEvent(aguievents.NewToolCallEndEvent("call-1")),
		newTrackEvent(aguievents.NewToolCallStartEvent("call-2", "beta", aguievents.WithParentMessageID("assistant-1"))),
		newTrackEvent(aguievents.NewToolCallArgsEvent("call-2", "{\"step\":2}")),
		newTrackEvent(aguievents.NewToolCallEndEvent("call-2")),
		newTrackEvent(aguievents.NewTextMessageEndEvent("assistant-1")),
		newTrackEvent(aguievents.NewToolCallResultEvent("tool-msg-1", "call-1", "first-result")),
		newTrackEvent(aguievents.NewToolCallResultEvent("tool-msg-2", "call-2", "second-result")),
	}

	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	assistant := msgs[0]
	require.Len(t, assistant.ToolCalls, 2)
	assert.Equal(t, "call-1", assistant.ToolCalls[0].ID)
	assert.Equal(t, "alpha", assistant.ToolCalls[0].Function.Name)
	assert.Equal(t, "{\"step\":1}", assistant.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "call-2", assistant.ToolCalls[1].ID)
	assert.Equal(t, "beta", assistant.ToolCalls[1].Function.Name)
	assert.Equal(t, "{\"step\":2}", assistant.ToolCalls[1].Function.Arguments)

	first := msgs[1]
	require.NotNil(t, first.ToolCallID)
	assert.Equal(t, "call-1", first.ToolCallID)
	require.NotNil(t, first.Content)
	content, ok := first.ContentString()
	require.True(t, ok)
	assert.Equal(t, "first-result", content)
	second := msgs[2]
	require.NotNil(t, second.ToolCallID)
	assert.Equal(t, "call-2", second.ToolCallID)
	require.NotNil(t, second.Content)
	content, ok = second.ContentString()
	require.True(t, ok)
	assert.Equal(t, "second-result", content)
}

func TestReduceTextStartErrors(t *testing.T) {
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing id",
			events: trackEventsFrom(aguievents.NewTextMessageStartEvent("", aguievents.WithRole("user"))),
			want:   "text message start missing id",
		},
		{
			name: "duplicate id",
			events: trackEventsFrom(
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("user")),
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
			),
			want: "duplicate text message start: msg-1",
		},
		{
			name:   "unsupported role",
			events: trackEventsFrom(aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("tool"))),
			want:   "unsupported role: tool",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceTextContentErrors(t *testing.T) {
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing start",
			events: trackEventsFrom(aguievents.NewTextMessageContentEvent("ghost", "hi")),
			want:   "text message content without start: ghost",
		},
		{
			name: "after end",
			events: trackEventsFrom(
				aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")),
				aguievents.NewTextMessageContentEvent("user-1", "hello"),
				aguievents.NewTextMessageEndEvent("user-1"),
				aguievents.NewTextMessageContentEvent("user-1", "!"),
			),
			want: "text message content after end: user-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceTextEndErrors(t *testing.T) {
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing start",
			events: trackEventsFrom(aguievents.NewTextMessageEndEvent("ghost")),
			want:   "text message end without start: ghost",
		},
		{
			name: "duplicate end",
			events: trackEventsFrom(
				aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")),
				aguievents.NewTextMessageEndEvent("user-1"),
				aguievents.NewTextMessageEndEvent("user-1"),
			),
			want: "duplicate text message end: user-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceDoesNotValidateFinalizeState(t *testing.T) {
	t.Run("text not closed", func(t *testing.T) {
		events := trackEventsFrom(aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")))

		msgs, err := Reduce(testAppName, testUserID, events)

		require.NoError(t, err)
		require.Len(t, msgs, 1)
		assert.Nil(t, msgs[0].Content)
	})
	t.Run("tool not completed", func(t *testing.T) {
		events := trackEventsFrom(
			aguievents.NewTextMessageStartEvent("user-1", aguievents.WithRole("user")),
			aguievents.NewTextMessageEndEvent("user-1"),
			aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
			aguievents.NewTextMessageEndEvent("assistant-1"),
			aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
			aguievents.NewToolCallArgsEvent("tool-call-1", "{}"),
			aguievents.NewToolCallEndEvent("tool-call-1"),
		)

		msgs, err := Reduce(testAppName, testUserID, events)

		require.NoError(t, err)
		require.Len(t, msgs, 2)
		require.Len(t, msgs[1].ToolCalls, 1)
		assert.Equal(t, "tool-call-1", msgs[1].ToolCalls[0].ID)
		assert.Equal(t, "calc", msgs[1].ToolCalls[0].Function.Name)
		assert.Equal(t, "{}", msgs[1].ToolCalls[0].Function.Arguments)
	})
}

func TestReduceToolStartErrors(t *testing.T) {
	assistant := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
		aguievents.NewTextMessageEndEvent("assistant-1"),
	)
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name: "missing id",
			events: combineEvents(assistant,
				trackEventsFrom(aguievents.NewToolCallStartEvent("", "calc", aguievents.WithParentMessageID("assistant-1"))),
			),
			want: "tool call start missing id",
		},
		{
			name: "duplicate start",
			events: combineEvents(assistant, trackEventsFrom(
				aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
				aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
			)),
			want: "duplicate tool call start: tool-call-1",
		},
		{
			name: "missing parent",
			events: combineEvents(assistant,
				trackEventsFrom(aguievents.NewToolCallStartEvent("tool-call-2", "calc")),
			),
			want: "tool call start missing parent message id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceToolStartCreatesParentMessage(t *testing.T) {
	events := trackEventsFrom(
		aguievents.NewToolCallStartEvent("tool-call-1", "search", aguievents.WithParentMessageID("assistant-ghost")),
		aguievents.NewToolCallArgsEvent("tool-call-1", "{\"q\":\"hi\"}"),
		aguievents.NewToolCallEndEvent("tool-call-1"),
		aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "done"),
	)
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	first := msgs[0]
	assert.Equal(t, "assistant-ghost", first.ID)
	assert.Equal(t, types.RoleAssistant, first.Role)
	require.NotNil(t, first.Name)
	assert.Equal(t, testAppName, first.Name)
	require.Len(t, first.ToolCalls, 1)
	assert.Equal(t, "search", first.ToolCalls[0].Function.Name)
	assert.Equal(t, "{\"q\":\"hi\"}", first.ToolCalls[0].Function.Arguments)
	second := msgs[1]
	assert.Equal(t, types.RoleTool, second.Role)
	require.NotNil(t, second.ToolCallID)
	assert.Equal(t, "tool-call-1", second.ToolCallID)
	require.NotNil(t, second.Content)
	content, ok := second.ContentString()
	require.True(t, ok)
	assert.Equal(t, "done", content)
}

func TestReduceToolArgsErrors(t *testing.T) {
	assistant := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
		aguievents.NewTextMessageEndEvent("assistant-1"),
	)
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing start",
			events: trackEventsFrom(aguievents.NewToolCallArgsEvent("ghost", "{}")),
			want:   "tool call args without start: ghost",
		},
		{
			name: "invalid phase",
			events: combineEvents(assistant, trackEventsFrom(
				aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
				aguievents.NewToolCallArgsEvent("tool-call-1", "{}"),
				aguievents.NewToolCallEndEvent("tool-call-1"),
				aguievents.NewToolCallArgsEvent("tool-call-1", "{}"),
			)),
			want: "tool call args invalid phase: tool-call-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceToolEndErrors(t *testing.T) {
	assistant := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
		aguievents.NewTextMessageEndEvent("assistant-1"),
	)
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing start",
			events: trackEventsFrom(aguievents.NewToolCallEndEvent("ghost")),
			want:   "tool call end without start: ghost",
		},
		{
			name: "duplicate end",
			events: combineEvents(assistant, trackEventsFrom(
				aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
				aguievents.NewToolCallArgsEvent("tool-call-1", "{}"),
				aguievents.NewToolCallEndEvent("tool-call-1"),
				aguievents.NewToolCallEndEvent("tool-call-1"),
			)),
			want: "duplicate tool call end: tool-call-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceToolResultErrors(t *testing.T) {
	assistant := trackEventsFrom(
		aguievents.NewTextMessageStartEvent("assistant-1", aguievents.WithRole("assistant")),
		aguievents.NewTextMessageEndEvent("assistant-1"),
	)
	tests := []struct {
		name   string
		events []session.TrackEvent
		want   string
	}{
		{
			name:   "missing identifiers",
			events: trackEventsFrom(aguievents.NewToolCallResultEvent("", "", "oops")),
			want:   "tool call result missing identifiers",
		},
		{
			name: "missing completion",
			events: combineEvents(assistant, trackEventsFrom(
				aguievents.NewToolCallStartEvent("tool-call-1", "calc", aguievents.WithParentMessageID("assistant-1")),
				aguievents.NewToolCallArgsEvent("tool-call-1", "{}"),
				aguievents.NewToolCallResultEvent("tool-msg-1", "tool-call-1", "oops"),
			)),
			want: "tool call result without completed call: tool-call-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertReduceError(t, tt.events, tt.want)
		})
	}
}

func TestReduceIgnoresEmptyPayload(t *testing.T) {
	msgs, err := Reduce(testAppName, testUserID, []session.TrackEvent{{}})
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

func TestReduceInvalidPayload(t *testing.T) {
	event := session.TrackEvent{Payload: []byte("{")}
	assertReduceError(t, []session.TrackEvent{event}, "unmarshal track event payload")
}

func TestReduceIgnoresUnknownEvents(t *testing.T) {
	events := trackEventsFrom(aguievents.NewRunStartedEvent("thread", "run"))
	msgs, err := Reduce(testAppName, testUserID, events)
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

func TestHandleActivityAllCases(t *testing.T) {
	stepStarted := aguievents.NewStepStartedEvent("prep")
	stepFinished := aguievents.NewStepFinishedEvent("cleanup")
	stateSnapshot := aguievents.NewStateSnapshotEvent(map[string]any{"status": "ok"})
	stateDeltaOps := []aguievents.JSONPatchOperation{{Op: "add", Path: "/count", Value: 1}}
	stateDelta := aguievents.NewStateDeltaEvent(stateDeltaOps)
	messageSnapshotEvent := aguievents.NewMessagesSnapshotEvent([]aguievents.Message{
		{ID: "msg-1", Role: "assistant"},
	})
	activitySnapshot := aguievents.NewActivitySnapshotEvent("activity-1", "PLAN", map[string]any{"status": "draft"}).WithReplace(false)
	activityDeltaOps := []aguievents.JSONPatchOperation{{Op: "replace", Path: "/status", Value: "done"}}
	activityDelta := aguievents.NewActivityDeltaEvent("activity-2", "PLAN", activityDeltaOps)
	customEvent := aguievents.NewCustomEvent("custom-event", aguievents.WithValue(map[string]any{"k": "v"}))
	rawEvent := aguievents.NewRawEvent(map[string]any{"raw": true}, aguievents.WithSource("unit-test"))
	runStarted := aguievents.NewRunStartedEvent("thread-1", "run-1")

	tests := []struct {
		name        string
		event       aguievents.Event
		wantCount   int
		wantID      string
		wantType    string
		wantContent map[string]any
	}{
		{
			name:        "step started",
			event:       stepStarted,
			wantCount:   1,
			wantID:      stepStarted.ID(),
			wantType:    string(stepStarted.Type()),
			wantContent: map[string]any{"stepName": stepStarted.StepName},
		},
		{
			name:        "step finished",
			event:       stepFinished,
			wantCount:   1,
			wantID:      stepFinished.ID(),
			wantType:    string(stepFinished.Type()),
			wantContent: map[string]any{"stepName": stepFinished.StepName},
		},
		{
			name:        "state snapshot",
			event:       stateSnapshot,
			wantCount:   1,
			wantID:      stateSnapshot.ID(),
			wantType:    string(stateSnapshot.Type()),
			wantContent: map[string]any{"snapshot": stateSnapshot.Snapshot},
		},
		{
			name:        "state delta",
			event:       stateDelta,
			wantCount:   1,
			wantID:      stateDelta.ID(),
			wantType:    string(stateDelta.Type()),
			wantContent: map[string]any{"delta": stateDelta.Delta},
		},
		{
			name:        "messages snapshot",
			event:       messageSnapshotEvent,
			wantCount:   1,
			wantID:      messageSnapshotEvent.ID(),
			wantType:    string(messageSnapshotEvent.Type()),
			wantContent: map[string]any{"messages": messageSnapshotEvent.Messages},
		},
		{
			name:      "activity snapshot",
			event:     activitySnapshot,
			wantCount: 1,
			wantID:    activitySnapshot.ID(),
			wantType:  string(activitySnapshot.Type()),
			wantContent: map[string]any{
				"messageId":    activitySnapshot.MessageID,
				"activityType": activitySnapshot.ActivityType,
				"content":      activitySnapshot.Content,
				"replace":      activitySnapshot.Replace,
			},
		},
		{
			name:      "activity delta",
			event:     activityDelta,
			wantCount: 1,
			wantID:    activityDelta.ID(),
			wantType:  string(activityDelta.Type()),
			wantContent: map[string]any{
				"messageId":    activityDelta.MessageID,
				"activityType": activityDelta.ActivityType,
				"patch":        activityDelta.Patch,
			},
		},
		{
			name:      "custom event",
			event:     customEvent,
			wantCount: 1,
			wantID:    customEvent.ID(),
			wantType:  string(customEvent.Type()),
			wantContent: map[string]any{
				"name":  customEvent.Name,
				"value": customEvent.Value,
			},
		},
		{
			name:      "raw event",
			event:     rawEvent,
			wantCount: 1,
			wantID:    rawEvent.ID(),
			wantType:  string(rawEvent.Type()),
			wantContent: map[string]any{
				"source": rawEvent.Source,
				"event":  rawEvent.Event,
			},
		},
		{
			name:        "default passthrough",
			event:       runStarted,
			wantCount:   0,
			wantID:      runStarted.GetBaseEvent().ID(),
			wantType:    string(runStarted.Type()),
			wantContent: map[string]any{"content": runStarted},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := new(testAppName, testUserID)
			require.NoError(t, r.handleActivity(tt.event))
			require.Len(t, r.messages, tt.wantCount)
			if tt.wantCount == 0 {
				return
			}
			msg := r.messages[0]
			assert.Equal(t, types.RoleActivity, msg.Role)
			assert.Equal(t, tt.wantID, msg.ID)
			assert.Equal(t, tt.wantType, msg.ActivityType)
			assert.NotEmpty(t, msg.Content)
			content, ok := msg.ContentActivity()
			require.True(t, ok)
			assert.Equal(t, tt.wantContent, content)
		})
	}
}

func TestHandleToolEndMissingParent(t *testing.T) {
	r := new(testAppName, testUserID)
	r.toolCalls["tool-call-1"] = &toolCallState{
		messageID: "ghost-parent",
		phase:     toolAwaitingArgs,
	}
	err := r.handleToolEnd(aguievents.NewToolCallEndEvent("tool-call-1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool call end missing parent message: ghost-parent")
}

func newTrackEvent(evt aguievents.Event) session.TrackEvent {
	payload, _ := evt.ToJSON()
	return session.TrackEvent{
		Track:     session.Track("agui"),
		Payload:   payload,
		Timestamp: time.Now(),
	}
}

func trackEventsFrom(evts ...aguievents.Event) []session.TrackEvent {
	out := make([]session.TrackEvent, 0, len(evts))
	for _, evt := range evts {
		out = append(out, newTrackEvent(evt))
	}
	return out
}

func combineEvents(chunks ...[]session.TrackEvent) []session.TrackEvent {
	total := 0
	for _, chunk := range chunks {
		total += len(chunk)
	}
	out := make([]session.TrackEvent, 0, total)
	for _, chunk := range chunks {
		out = append(out, chunk...)
	}
	return out
}

func assertReduceError(t *testing.T, events []session.TrackEvent, want string) {
	t.Helper()
	_, err := Reduce(testAppName, testUserID, events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), want)
}
