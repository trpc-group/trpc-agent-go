//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

const replayTestProtocol = "test.responses"
const replayTestModel = "reasoning-model"

func replayTestMessage() Message {
	return Message{
		Role:             RoleAssistant,
		Content:          "Looking up the record.",
		ReasoningContent: "Need the current value.",
		ToolCalls: []ToolCall{{
			Type: "function",
			ID:   "call_1",
			Function: FunctionDefinitionParam{
				Name:      "lookup",
				Arguments: json.RawMessage(`{"record":1}`),
			},
		}},
	}
}

func replayTestPayload() json.RawMessage {
	return json.RawMessage(`[
		{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque-state"},
		{"type":"message","id":"msg_1","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Looking up the record."}]},
		{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"record\":1}"}
	]`)
}

func TestReplayDataSurvivesMessageJSON(t *testing.T) {
	msg := replayTestMessage()
	payload := replayTestPayload()
	require.NoError(t, msg.SetReplayData(replayTestProtocol, replayTestModel, payload))
	encoded, err := json.Marshal(msg)
	require.NoError(t, err)
	var restored Message
	require.NoError(t, json.Unmarshal(encoded, &restored))
	replayed, err := restored.ReplayDataFor(replayTestProtocol, replayTestModel)
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(replayed))
	require.Equal(t, msg.Content, restored.Content)
	require.Equal(t, msg.ToolCalls, restored.ToolCalls)
}

func TestReplayDataRejectsProjectionEdits(t *testing.T) {
	tests := map[string]func(*Message){
		"text":      func(m *Message) { m.Content = "edited" },
		"reasoning": func(m *Message) { m.ReasoningContent = "edited" },
		"role":      func(m *Message) { m.Role = RoleUser },
		"call name": func(m *Message) { m.ToolCalls[0].Function.Name = "different" },
		"arguments with same call id": func(m *Message) {
			m.ToolCalls[0].Function.Arguments = json.RawMessage(`{"record":2}`)
		},
		"removed call": func(m *Message) { m.ToolCalls = nil },
		"new content part": func(m *Message) {
			text := "extra"
			m.ContentParts = []ContentPart{{Type: ContentTypeText, Text: &text}}
		},
	}
	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			msg := replayTestMessage()
			require.NoError(t, msg.SetReplayData(replayTestProtocol, replayTestModel, replayTestPayload()))
			edit(&msg)
			_, err := msg.ReplayDataFor(replayTestProtocol, replayTestModel)
			require.ErrorContains(t, err, "projection changed")
			// Persistence must not bless edits by recalculating the binding.
			encoded, err := json.Marshal(msg)
			require.NoError(t, err)
			var restored Message
			require.NoError(t, json.Unmarshal(encoded, &restored))
			_, err = restored.ReplayDataFor(replayTestProtocol, replayTestModel)
			require.ErrorContains(t, err, "projection changed")
		})
	}
}

func TestReplayDataRejectsAnotherProducer(t *testing.T) {
	msg := replayTestMessage()
	require.NoError(t, msg.SetReplayData(replayTestProtocol, replayTestModel, replayTestPayload()))
	for _, producer := range [][2]string{
		{"another.protocol", replayTestModel},
		{replayTestProtocol, "another-model"},
	} {
		payload, err := msg.ReplayDataFor(producer[0], producer[1])
		require.Nil(t, payload)
		require.ErrorContains(t, err, "belongs to protocol")
	}
}

func TestReplayDataCopiesPayloadAtBothBoundaries(t *testing.T) {
	msg := replayTestMessage()
	payload := replayTestPayload()
	want := string(payload)
	require.NoError(t, msg.SetReplayData(replayTestProtocol, replayTestModel, payload))
	payload[0] = '!'
	got, err := msg.ReplayDataFor(replayTestProtocol, replayTestModel)
	require.NoError(t, err)
	require.Equal(t, want, string(got))
	got[0] = '!'
	got, err = msg.ReplayDataFor(replayTestProtocol, replayTestModel)
	require.NoError(t, err)
	require.Equal(t, want, string(got))
}

func TestReplayOnlyMessageHasValidContent(t *testing.T) {
	msg := NewAssistantMessage("")
	require.NoError(t, msg.SetReplayData(replayTestProtocol, replayTestModel,
		json.RawMessage(`[{"type":"reasoning","summary":[],"encrypted_content":"state"}]`)))
	require.True(t, HasPayload(msg))
	rsp := &Response{Choices: []Choice{{Message: msg}}}
	require.True(t, rsp.IsValidContent())
	encoded, err := json.Marshal(rsp)
	require.NoError(t, err)
	var restored Response
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.True(t, restored.IsValidContent())
	_, err = restored.Choices[0].Message.ReplayDataFor(replayTestProtocol, replayTestModel)
	require.NoError(t, err)
}

func TestReplayDataInvalidBindingDoesNotReplaceExistingState(t *testing.T) {
	msg := replayTestMessage()
	require.NoError(t, msg.SetReplayData(replayTestProtocol, replayTestModel, replayTestPayload()))
	original := msg.Replay
	for _, payload := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`"text"`), json.RawMessage(`{`)} {
		require.Error(t, msg.SetReplayData(replayTestProtocol, replayTestModel, payload))
		require.Same(t, original, msg.Replay)
	}
	require.Error(t, msg.SetReplayData("", replayTestModel, replayTestPayload()))
	require.Error(t, msg.SetReplayData(replayTestProtocol, "", replayTestPayload()))
	require.Same(t, original, msg.Replay)
	user := NewUserMessage("hello")
	require.Error(t, user.SetReplayData(replayTestProtocol, replayTestModel, replayTestPayload()))
	var absent *Message
	require.Error(t, absent.SetReplayData(replayTestProtocol, replayTestModel, replayTestPayload()))
}

func TestReplayDataRejectsUnsupportedPersistentFormat(t *testing.T) {
	msg := replayTestMessage()
	require.NoError(t, msg.SetReplayData(replayTestProtocol, replayTestModel, replayTestPayload()))
	encoded, err := json.Marshal(msg)
	require.NoError(t, err)
	var stored map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &stored))
	var record map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(stored["replay"], &record))
	record["version"] = json.RawMessage(`2`)
	stored["replay"], err = json.Marshal(record)
	require.NoError(t, err)
	encoded, err = json.Marshal(stored)
	require.NoError(t, err)
	var restored Message
	require.ErrorContains(t, json.Unmarshal(encoded, &restored), "unsupported replay data version")
}

func TestLegacyMessageDoesNotAcquireReplayState(t *testing.T) {
	msg := NewAssistantMessage("legacy")
	payload, err := msg.ReplayDataFor("any.protocol", "any-model")
	require.NoError(t, err)
	require.Nil(t, payload)
	encoded, err := json.Marshal(msg)
	require.NoError(t, err)
	require.JSONEq(t, `{"role":"assistant","content":"legacy"}`, string(encoded))
}

func TestSimpleTokenCounterAccountsForNativeReplay(t *testing.T) {
	msg := NewAssistantMessage("")
	require.NoError(t, msg.SetReplayData(replayTestProtocol, replayTestModel, replayTestPayload()))
	counter := NewSimpleTokenCounter()
	tokens, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, tokens, 1, "opaque-only history must not appear to consume zero context")
	msg.Content = "changed"
	_, err = counter.CountTokensRange(context.Background(), []Message{msg}, 0, 1)
	require.ErrorContains(t, err, "projection changed")
}
