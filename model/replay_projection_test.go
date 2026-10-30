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
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func readMultimodalReplayFixture(t *testing.T) ([]byte, Message) {
	t.Helper()
	// Generated with the original v1 Message encoder. Do not regenerate this
	// fixture with a new projection encoder or replace its stored fingerprint.
	stored, err := os.ReadFile("testdata/replay_v1_multimodal.json")
	require.NoError(t, err)
	var message Message
	require.NoError(t, json.Unmarshal(stored, &message))
	return stored, message
}

func TestReplayProjectionV1ReadsNestedHistoryFixture(t *testing.T) {
	stored, message := readMultimodalReplayFixture(t)
	require.Equal(t, "a0f2e67b0f6e48161b754c6cfee01de883904a31b6d170f03679c6921c35d961", message.Replay.projection)
	payload, err := message.ReplayDataFor("test.multimodal", "fixture-model")
	require.NoError(t, err)
	require.JSONEq(t, `[{"type":"message","id":"msg_1","content":"synthetic fixture"}]`, string(payload))
	encoded, err := json.Marshal(message)
	require.NoError(t, err)
	require.JSONEq(t, string(stored), string(encoded))
	var restored Message
	require.NoError(t, json.Unmarshal(encoded, &restored))
	replayed, err := restored.ReplayDataFor("test.multimodal", "fixture-model")
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(replayed))
}

func TestReplayProjectionV1KeepsFixedEncoding(t *testing.T) {
	// Captured from the original public encoder. This oracle must not follow
	// changes to Message.MarshalJSON or its nested public types.
	stored, err := os.ReadFile("testdata/replay_v1_projection_cases.json")
	require.NoError(t, err)
	var expected []json.RawMessage
	require.NoError(t, json.Unmarshal(stored, &expected))
	empty := ""
	index := 0
	cases := []Message{
		{Role: RoleAssistant},
		{Role: RoleAssistant, ContentParts: []ContentPart{}, ToolCalls: []ToolCall{}},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{}}},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{Index: &index, ExtraFields: map[string]any{}, Function: FunctionDefinitionParam{Arguments: []byte("\ninvalid JSON remains a string\n")}}}},
		{Role: RoleAssistant, ContentParts: []ContentPart{{Type: ContentTypeText, Text: &empty}, {Image: &Image{}}, {Audio: &Audio{}}, {Video: &Video{}}, {File: &File{}}, {ContentRef: &ContentRef{}}}},
		{Role: RoleAssistant, ContentParts: []ContentPart{{Image: &Image{Data: []byte{}}}, {Audio: &Audio{Data: []byte{}}}, {Video: &Video{Data: []byte{}}}, {File: &File{Data: []byte{}}}}},
	}
	require.Len(t, expected, len(cases))
	for i, message := range cases {
		var want bytes.Buffer
		require.NoError(t, json.Compact(&want, expected[i]))
		frozen, err := marshalReplayProjectionV1(&message)
		require.NoError(t, err)
		require.Equal(t, want.String(), string(frozen), "v1 bytes differ in case %d", i)
	}
}

func TestReplayProjectionV1RejectsNestedEdits(t *testing.T) {
	cases := map[string]func(*Message){
		"tool id":       func(m *Message) { m.ToolCalls[0].ID = "other" },
		"strict":        func(m *Message) { m.ToolCalls[0].Function.Strict = false },
		"description":   func(m *Message) { m.ToolCalls[0].Function.Description = "other" },
		"index":         func(m *Message) { *m.ToolCalls[0].Index = 0 },
		"extra fields":  func(m *Message) { m.ToolCalls[0].ExtraFields["nested"].(map[string]any)["enabled"] = false },
		"text":          func(m *Message) { *m.ContentParts[0].Text = "other" },
		"image bytes":   func(m *Message) { m.ContentParts[1].Image.Data[0] = 1 },
		"image detail":  func(m *Message) { m.ContentParts[1].Image.Detail = "low" },
		"audio format":  func(m *Message) { m.ContentParts[2].Audio.Format = "mp3" },
		"video url":     func(m *Message) { m.ContentParts[3].Video.URL = "https://example.com/other.mp4" },
		"file id":       func(m *Message) { m.ContentParts[4].File.FileID = "file_other" },
		"reference":     func(m *Message) { m.ContentParts[5].ContentRef.ArtifactVersion++ },
		"signature":     func(m *Message) { m.ReasoningSignature = "other" },
		"tool metadata": func(m *Message) { m.ToolName = "other" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			_, message := readMultimodalReplayFixture(t)
			edit(&message)
			_, err := message.ReplayDataFor("test.multimodal", "fixture-model")
			require.ErrorContains(t, err, "projection changed")
			encoded, err := json.Marshal(message)
			require.NoError(t, err)
			var restored Message
			require.NoError(t, json.Unmarshal(encoded, &restored))
			_, err = restored.ReplayDataFor("test.multimodal", "fixture-model")
			require.ErrorContains(t, err, "projection changed")
		})
	}
}

func TestReplayProjectionFieldsRequireBindingPolicyReview(t *testing.T) {
	// This guard is intentionally tied to the public field set, not its JSON
	// encoder. A new field must be classified as projection-only or protected
	// by a new binding version. Do not extend the frozen v1 types automatically.
	cases := []struct {
		public, frozen any
		fields         []string
	}{
		{Message{}, replayProjectionV1{}, []string{"Role", "Content", "ContentParts", "ToolID", "ToolName", "ToolCalls", "ReasoningContent", "ReasoningSignature"}},
		{ToolCall{}, replayToolCallV1{}, []string{"Type", "Function", "ID", "Index", "ExtraFields"}},
		{FunctionDefinitionParam{}, replayFunctionV1{}, []string{"Name", "Strict", "Description", "Arguments"}},
		{ContentPart{}, replayContentPartV1{}, []string{"Type", "Text", "Image", "Audio", "Video", "File", "ContentRef"}},
		{Image{}, replayImageV1{}, []string{"URL", "Data", "Detail", "Format"}},
		{Audio{}, replayAudioV1{}, []string{"URL", "Data", "Format"}},
		{Video{}, replayVideoV1{}, []string{"URL", "Data", "Format"}},
		{File{}, replayFileV1{}, []string{"Name", "URL", "Data", "FileID", "MimeType"}},
		{ContentRef{}, replayContentRefV1{}, []string{"ArtifactRef", "ArtifactName", "ArtifactVersion", "MimeType", "SizeBytes", "SHA256", "OriginalName", "EventID", "RequestID"}},
	}
	fields := func(value any) []string {
		typ := reflect.TypeOf(value)
		var names []string
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			if typ == reflect.TypeOf(Message{}) && name == "Replay" {
				continue
			}
			names = append(names, name)
		}
		return names
	}
	for _, tc := range cases {
		require.ElementsMatch(t, tc.fields, fields(tc.frozen),
			"%T is the frozen v1 schema and must not grow with public types", tc.frozen)
		require.ElementsMatch(t, tc.fields, fields(tc.public),
			"%T fields changed: review the native replay binding policy", tc.public)
	}
}
