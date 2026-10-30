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
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const replayTestProtocol = "test.responses"
const replayTestModel = "reasoning-model"

func TestInfoJSONKeepsLegacyShapeAndNativeIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		info Info
		want string
	}{
		{name: "zero", want: `{"Name":"","ContextWindow":0}`},
		{name: "legacy", info: Info{Name: "chat", ContextWindow: 8192}, want: `{"Name":"chat","ContextWindow":8192}`},
		{name: "native", info: Info{Name: "native", ReplayProtocol: replayTestProtocol}, want: `{"Name":"native","ContextWindow":0,"ReplayProtocol":"test.responses"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.info)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(encoded))
			var restored Info
			require.NoError(t, json.Unmarshal(encoded, &restored))
			require.Equal(t, tc.info, restored)
		})
	}
}

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

func TestReplayDataReadsV1HistoryFixture(t *testing.T) {
	// Keep this fixture and its fingerprint fixed. Generating both with the
	// current encoder would miss changes that invalidate persisted v1 history.
	stored, err := os.ReadFile("testdata/replay_v1.json")
	require.NoError(t, err)
	var restored Message
	require.NoError(t, json.Unmarshal(stored, &restored))
	payload, err := restored.ReplayDataFor(replayTestProtocol, replayTestModel)
	require.NoError(t, err)
	require.JSONEq(t, string(replayTestPayload()), string(payload))
	projection := restored
	projection.Replay = nil
	require.Equal(t, replayTestMessage(), projection)

	encoded, err := json.Marshal(restored)
	require.NoError(t, err)
	require.JSONEq(t, string(stored), string(encoded))
	var roundTrip Message
	require.NoError(t, json.Unmarshal(encoded, &roundTrip))
	replayed, err := roundTrip.ReplayDataFor(replayTestProtocol, replayTestModel)
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(replayed))
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

func TestReplayDataRejectsCorruptRecords(t *testing.T) {
	zero := NewAssistantMessage("answer")
	zero.Replay = &ReplayData{}
	_, err := zero.ReplayDataFor(replayTestProtocol, replayTestModel)
	require.ErrorContains(t, err, "requires a protocol and model identity")
	_, err = json.Marshal(zero)
	require.Error(t, err, "a zero record must not be persisted")

	unencodable := replayTestMessage()
	unencodable.ToolCalls[0].ExtraFields = map[string]any{"bad": func() {}}
	require.ErrorContains(t,
		unencodable.SetReplayData(replayTestProtocol, replayTestModel, replayTestPayload()),
		"encode replay message projection")
	require.Nil(t, unencodable.Replay)

	msg := replayTestMessage()
	require.NoError(t, msg.SetReplayData(replayTestProtocol, replayTestModel, replayTestPayload()))
	msg.ToolCalls[0].ExtraFields = map[string]any{"bad": func() {}}
	_, err = msg.ReplayDataFor(replayTestProtocol, replayTestModel)
	require.ErrorContains(t, err, "encode replay message projection")

	for name, record := range map[string]string{
		"not an object":    `"record"`,
		"missing identity": `{"version":1,"provider":"","model":"m","payload":[],"projection_sha256":"00"}`,
		"scalar payload": `{"version":1,"provider":"p","model":"m","payload":"text",` +
			`"projection_sha256":"` + strings.Repeat("0", 64) + `"}`,
		"short fingerprint": `{"version":1,"provider":"p","model":"m","payload":[],"projection_sha256":"00"}`,
		"non-hex fingerprint": `{"version":1,"provider":"p","model":"m","payload":[],` +
			`"projection_sha256":"` + strings.Repeat("z", 64) + `"}`,
	} {
		var restored ReplayData
		require.Error(t, json.Unmarshal([]byte(record), &restored), name)
		_, err := restored.MarshalJSON()
		require.Error(t, err, "%s must leave the receiver unchanged", name)
	}
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

func TestTokenTailoringPreservesNativeRecordsWithoutText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message Message
		payload json.RawMessage
		results []Message
	}{
		{
			name:    "opaque reasoning only",
			message: NewAssistantMessage(""),
			payload: json.RawMessage(`[{"type":"reasoning","summary":[],"encrypted_content":"opaque"}]`),
		},
		{
			name: "reasoning projection only",
			message: Message{
				Role: RoleAssistant, ReasoningContent: "thinking",
			},
			payload: json.RawMessage(`[{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}],"encrypted_content":"opaque"}]`),
		},
		{
			name: "function call only",
			message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{
				Type: "function", ID: "call_1",
				Function: FunctionDefinitionParam{
					Name: "lookup", Arguments: json.RawMessage(`{"record":1}`),
				},
			}}},
			payload: json.RawMessage(`[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"record\":1}"}]`),
			results: []Message{NewToolMessage("call_1", "lookup", "result")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := tc.message
			require.NoError(t, native.SetReplayData(replayTestProtocol, replayTestModel, tc.payload))
			messages := []Message{NewUserMessage("first"), native}
			messages = append(messages, tc.results...)
			messages = append(messages, NewUserMessage("next"))
			counter := NewSimpleTokenCounter()
			budget, err := counter.CountTokensRange(context.Background(), messages, 0, len(messages))
			require.NoError(t, err)
			for _, strategy := range []struct {
				name string
				impl TailoringStrategy
			}{
				{name: "middle out", impl: NewMiddleOutStrategy(counter)},
				{name: "head out", impl: NewHeadOutStrategy(counter)},
				{name: "tail out", impl: NewTailOutStrategy(counter)},
			} {
				t.Run(strategy.name, func(t *testing.T) {
					tailored, err := strategy.impl.TailorMessages(context.Background(), messages, budget+1)
					require.NoError(t, err)
					require.Equal(t, messages, tailored, "history within budget must preserve native records and projections")
					payload, err := tailored[1].ReplayDataFor(replayTestProtocol, replayTestModel)
					require.NoError(t, err)
					require.JSONEq(t, string(tc.payload), string(payload))
				})
			}
		})
	}
}

func TestResponseCloneIsolatesReplayDecode(t *testing.T) {
	original := replayTestMessage()
	require.NoError(t, original.SetReplayData(replayTestProtocol, replayTestModel, replayTestPayload()))
	replacement := NewAssistantMessage("replacement")
	require.NoError(t, replacement.SetReplayData(replayTestProtocol, replayTestModel, json.RawMessage(`[]`)))
	encoded, err := json.Marshal(replacement.Replay)
	require.NoError(t, err)
	response := &Response{Choices: []Choice{{Message: original, Delta: original}}}
	cloned := response.Clone()
	for _, msg := range []*Message{&cloned.Choices[0].Message, &cloned.Choices[0].Delta} {
		require.NotSame(t, original.Replay, msg.Replay)
		require.NoError(t, json.Unmarshal(encoded, msg.Replay))
	}
	for _, msg := range []Message{response.Choices[0].Message, response.Choices[0].Delta} {
		payload, err := msg.ReplayDataFor(replayTestProtocol, replayTestModel)
		require.NoError(t, err, "decoding a cloned record must not alter the source response")
		require.JSONEq(t, string(replayTestPayload()), string(payload))
	}
}
