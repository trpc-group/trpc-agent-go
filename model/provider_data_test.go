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
	"testing"

	"github.com/stretchr/testify/require"
)

const providerDataTestProtocol = "test.responses"
const providerDataTestModel = "reasoning-model"

func TestInfoJSONUsesCurrentShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		info Info
		want string
	}{
		{name: "zero", want: `{"Name":"","ContextWindow":0}`},
		{name: "base", info: Info{Name: "chat", ContextWindow: 8192}, want: `{"Name":"chat","ContextWindow":8192}`},
		{name: "native", info: Info{Name: "native", ProviderDataProtocol: providerDataTestProtocol}, want: `{"Name":"native","ContextWindow":0,"ProviderDataProtocol":"test.responses"}`},
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

func providerDataTestMessage() Message {
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

func providerDataTestPayload() json.RawMessage {
	return json.RawMessage(`[
		{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque-state"},
		{"type":"message","id":"msg_1","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Looking up the record."}]},
		{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"record\":1}"}
	]`)
}

func TestProviderDataSurvivesMessageJSON(t *testing.T) {
	msg := providerDataTestMessage()
	payload := providerDataTestPayload()
	msg.ProviderData = &ProviderData{Protocol: providerDataTestProtocol, Model: providerDataTestModel, Payload: payload}
	encoded, err := json.Marshal(msg)
	require.NoError(t, err)
	var stored map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &stored))
	require.Contains(t, stored, "provider_data")
	var storedProviderData map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(stored["provider_data"], &storedProviderData))
	require.JSONEq(t, string(payload), string(storedProviderData["payload"]))
	var restored Message
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.NotNil(t, restored.ProviderData)
	got := restored.ProviderData.Payload
	require.JSONEq(t, string(payload), string(got))
	require.Equal(t, msg.Content, restored.Content)
	require.Equal(t, msg.ToolCalls, restored.ToolCalls)
}

func TestProviderDataReadsCurrentSchemaFixture(t *testing.T) {
	stored, err := os.ReadFile("testdata/provider_data.json")
	require.NoError(t, err)
	var restored Message
	require.NoError(t, json.Unmarshal(stored, &restored))
	require.NotNil(t, restored.ProviderData)
	payload := restored.ProviderData.Payload
	require.JSONEq(t, string(providerDataTestPayload()), string(payload))
	projection := restored
	projection.ProviderData = nil
	require.Equal(t, providerDataTestMessage(), projection)
	encoded, err := json.Marshal(restored)
	require.NoError(t, err)
	require.JSONEq(t, string(stored), string(encoded))
	var roundTrip Message
	require.NoError(t, json.Unmarshal(encoded, &roundTrip))
	require.NotNil(t, roundTrip.ProviderData)
	got := roundTrip.ProviderData.Payload
	require.JSONEq(t, string(providerDataTestPayload()), string(got))
}

func TestProviderDataAllowsProjectionEdits(t *testing.T) {
	tests := map[string]func(*Message){
		"text":      func(m *Message) { m.Content = "edited" },
		"reasoning": func(m *Message) { m.ReasoningContent = "edited" },
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
			msg := providerDataTestMessage()
			msg.ProviderData = &ProviderData{
				Protocol: providerDataTestProtocol,
				Model:    providerDataTestModel,
				Payload:  providerDataTestPayload(),
			}
			edit(&msg)
			require.NotNil(t, msg.ProviderData)
			payload := msg.ProviderData.Payload
			require.JSONEq(t, string(providerDataTestPayload()), string(payload))
			encoded, err := json.Marshal(msg)
			require.NoError(t, err)
			var restored Message
			require.NoError(t, json.Unmarshal(encoded, &restored))
			require.NotNil(t, restored.ProviderData)
			payload = restored.ProviderData.Payload
			require.JSONEq(t, string(providerDataTestPayload()), string(payload))
		})
	}
}

func TestProviderDataJSONRoundTrip(t *testing.T) {
	msg := providerDataTestMessage()
	msg.ProviderData = &ProviderData{
		Protocol: providerDataTestProtocol,
		Model:    providerDataTestModel,
		Payload:  providerDataTestPayload(),
	}
	encoded, err := json.Marshal(msg.ProviderData)
	require.NoError(t, err)
	require.JSONEq(t,
		`{"provider":"test.responses","model":"reasoning-model","payload":`+string(providerDataTestPayload())+`}`,
		string(encoded),
	)
	var restored ProviderData
	require.NoError(t, json.Unmarshal(encoded, &restored))
	roundTrip, err := json.Marshal(restored)
	require.NoError(t, err)
	require.JSONEq(t, string(encoded), string(roundTrip))
}

func TestProviderDataOnlyMessageHasValidContent(t *testing.T) {
	msg := NewAssistantMessage("")
	msg.ProviderData = &ProviderData{
		Protocol: providerDataTestProtocol,
		Model:    providerDataTestModel,
		Payload:  json.RawMessage(`[{"type":"reasoning","summary":[],"encrypted_content":"state"}]`),
	}
	require.True(t, HasPayload(msg))
	rsp := &Response{Choices: []Choice{{Message: msg}}}
	require.True(t, rsp.IsValidContent())
	encoded, err := json.Marshal(rsp)
	require.NoError(t, err)
	var restored Response
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.True(t, restored.IsValidContent())
	require.NotNil(t, restored.Choices[0].Message.ProviderData)
}

func TestLegacyMessageDoesNotAcquireProviderData(t *testing.T) {
	msg := NewAssistantMessage("legacy")
	require.Nil(t, msg.ProviderData)
	encoded, err := json.Marshal(msg)
	require.NoError(t, err)
	require.JSONEq(t, `{"role":"assistant","content":"legacy"}`, string(encoded))
}

func TestProviderDataRejectsCorruptRecords(t *testing.T) {
	zero := NewAssistantMessage("answer")
	zero.ProviderData = &ProviderData{}
	_, err := json.Marshal(zero)
	require.ErrorContains(t, err, "requires a protocol and model identity")

	for name, record := range map[string]string{
		"not an object":        `"record"`,
		"missing identity":     `{"provider":"","model":"m","payload":[]}`,
		"scalar payload":       `{"provider":"p","model":"m","payload":"text"}`,
		"invalid json payload": `{"provider":"p","model":"m","payload":{broken}}`,
	} {
		var restored ProviderData
		require.Error(t, json.Unmarshal([]byte(record), &restored), name)
		_, err := restored.MarshalJSON()
		require.Error(t, err, "%s must leave the receiver unchanged", name)
	}
}

func TestSimpleTokenCounterAccountsForProviderData(t *testing.T) {
	msg := NewAssistantMessage("")
	msg.ProviderData = &ProviderData{
		Protocol: providerDataTestProtocol,
		Model:    providerDataTestModel,
		Payload:  providerDataTestPayload(),
	}
	counter := NewSimpleTokenCounter()
	tokens, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, tokens, 1, "opaque-only history must not appear to consume zero context")
	msg.Content = "changed"
	_, err = counter.CountTokensRange(context.Background(), []Message{msg}, 0, 1)
	require.NoError(t, err, "display edits do not invalidate native provider data")
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
			native.ProviderData = &ProviderData{
				Protocol: providerDataTestProtocol,
				Model:    providerDataTestModel,
				Payload:  tc.payload,
			}
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
					require.NotNil(t, tailored[1].ProviderData)
					payload := tailored[1].ProviderData.Payload
					require.JSONEq(t, string(tc.payload), string(payload))
				})
			}
		})
	}
}

func TestResponseCloneIsolatesProviderDataEdits(t *testing.T) {
	original := providerDataTestMessage()
	original.ProviderData = &ProviderData{
		Protocol: providerDataTestProtocol,
		Model:    providerDataTestModel,
		Payload:  providerDataTestPayload(),
	}
	replacement := NewAssistantMessage("replacement")
	replacement.ProviderData = &ProviderData{
		Protocol: providerDataTestProtocol,
		Model:    providerDataTestModel,
		Payload:  json.RawMessage(`[]`),
	}
	encoded, err := json.Marshal(replacement.ProviderData)
	require.NoError(t, err)
	response := &Response{Choices: []Choice{{Message: original, Delta: original}}}
	cloned := response.Clone()
	for _, msg := range []*Message{&cloned.Choices[0].Message, &cloned.Choices[0].Delta} {
		require.NotSame(t, original.ProviderData, msg.ProviderData)
		msg.ProviderData.Payload[0] = '!'
		require.NoError(t, json.Unmarshal(encoded, msg.ProviderData))
	}
	for _, msg := range []Message{response.Choices[0].Message, response.Choices[0].Delta} {
		require.JSONEq(t, string(providerDataTestPayload()), string(msg.ProviderData.Payload),
			"editing a cloned record must not alter the source response")
	}
}
