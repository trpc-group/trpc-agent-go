//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolloopwarning

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestMatchingTrailingRoundFingerprintUsesRequestTail(t *testing.T) {
	messages := []model.Message{model.NewUserMessage("run")}
	messages = append(messages, roundMessages(
		[]model.ToolCall{
			newToolCall("search-1", "search", `{"query":"x","limit":9007199254740993}`),
			newToolCall("read-1", "read", `{"path":"a.go"}`),
		},
		[]model.Message{
			model.NewToolMessage("read-1", "read", "file"),
			model.NewToolMessage("search-1", "search", "matches"),
		},
	)...)
	messages = append(messages, roundMessages(
		[]model.ToolCall{
			newToolCall("search-2", "search", ` { "limit": 9007199254740993, "query": "x" } `),
			newToolCall("read-2", "read", `{"path":"a.go"}`),
		},
		[]model.Message{
			model.NewToolMessage("search-2", "search", "matches"),
			model.NewToolMessage("read-2", "read", "file"),
		},
	)...)

	fingerprint, ok := matchingTrailingRoundFingerprint(messages, nil)
	require.True(t, ok)
	require.NotEmpty(t, fingerprint)

	messages = append(messages, model.NewUserMessage("intervene"))
	_, ok = matchingTrailingRoundFingerprint(messages, nil)
	require.False(t, ok)
}

func TestMatchingTrailingRoundFingerprintDetectsChangesAndExclusions(t *testing.T) {
	base := []model.Message{model.NewUserMessage("run")}
	base = append(base, roundMessages(
		[]model.ToolCall{newToolCall("call-1", "search", `{"query":"x"}`)},
		[]model.Message{model.NewToolMessage("call-1", "search", "same")},
	)...)

	tests := map[string]struct {
		call     model.ToolCall
		result   model.Message
		excluded map[string]struct{}
	}{
		"tool": {
			call:   newToolCall("call-2", "read", `{"query":"x"}`),
			result: model.NewToolMessage("call-2", "read", "same"),
		},
		"arguments": {
			call:   newToolCall("call-2", "search", `{"query":"y"}`),
			result: model.NewToolMessage("call-2", "search", "same"),
		},
		"result": {
			call:   newToolCall("call-2", "search", `{"query":"x"}`),
			result: model.NewToolMessage("call-2", "search", "changed"),
		},
		"excluded": {
			call:     newToolCall("call-2", "search", `{"query":"x"}`),
			result:   model.NewToolMessage("call-2", "search", "same"),
			excluded: map[string]struct{}{"search": {}},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			messages := append([]model.Message(nil), base...)
			messages = append(messages, roundMessages(
				[]model.ToolCall{test.call},
				[]model.Message{test.result},
			)...)
			_, ok := matchingTrailingRoundFingerprint(messages, test.excluded)
			require.False(t, ok)
		})
	}
}

func TestParseTrailingToolRoundRejectsMalformedTranscripts(t *testing.T) {
	validCall := newToolCall("call-1", "search", `{}`)
	validResult := model.NewToolMessage("call-1", "search", "same")
	tests := map[string][]model.Message{
		"empty": nil,
		"no result": {
			model.NewAssistantMessage("done"),
		},
		"missing result": {
			assistantToolMessage(
				validCall,
				newToolCall("call-2", "read", `{}`),
			),
			validResult,
		},
		"unknown result": {
			assistantToolMessage(validCall),
			model.NewToolMessage("other", "search", "same"),
		},
		"duplicate result": {
			assistantToolMessage(validCall),
			validResult,
			validResult,
		},
		"empty result": {
			assistantToolMessage(validCall),
			{Role: model.RoleTool, ToolID: "call-1"},
		},
		"duplicate call id": {
			assistantToolMessage(
				validCall,
				newToolCall("call-1", "read", `{}`),
			),
			validResult,
		},
		"missing call id": {
			assistantToolMessage(newToolCall("", "search", `{}`)),
			validResult,
		},
		"missing tool name": {
			assistantToolMessage(newToolCall("call-1", "", `{}`)),
			validResult,
		},
		"non-tool boundary": {
			assistantToolMessage(validCall),
			validResult,
			model.NewAssistantMessage("done"),
		},
	}
	for name, messages := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, ok := parseTrailingToolRound(messages, len(messages))
			require.False(t, ok)
		})
	}

	round, start, ok := parseTrailingToolRound(
		[]model.Message{
			model.NewUserMessage("run"),
			assistantToolMessage(validCall),
			validResult,
		},
		3,
	)
	require.True(t, ok)
	require.Equal(t, 1, start)
	require.Equal(t, "call-1", round.toolCalls[0].ID)
	require.Equal(t, "call-1", round.results[0].ToolID)
}

func TestFingerprintRoundCanonicalizesArgumentsAndIgnoresIDs(t *testing.T) {
	callsA := []model.ToolCall{
		newToolCall(
			"call-search-a",
			"search",
			`{"query":"x","limit":9007199254740993}`,
		),
		newToolCall("call-read-a", "read", `{"path":"a.go"}`),
	}
	callsB := []model.ToolCall{
		newToolCall(
			"call-search-b",
			"search",
			` { "limit": 9007199254740993, "query": "x" } `,
		),
		newToolCall("call-read-b", "read", `{"path":"a.go"}`),
	}
	resultsA := []model.Message{
		model.NewToolMessage("call-search-a", "search", "matches"),
		model.NewToolMessage("call-read-a", "read", "file"),
	}
	resultsB := []model.Message{
		model.NewToolMessage("call-search-b", "search", "matches"),
		model.NewToolMessage("call-read-b", "read", "file"),
	}

	fingerprintA, ok := fingerprintRound(callsA, resultsA)
	require.True(t, ok)
	fingerprintB, ok := fingerprintRound(callsB, resultsB)
	require.True(t, ok)
	require.Equal(t, fingerprintA, fingerprintB)
}

func TestCanonicalArgumentsPreservesNumbersAndInvalidJSON(t *testing.T) {
	require.Equal(t, "", canonicalArguments(nil))
	require.Equal(t, "", canonicalArguments([]byte(" \n\t")))
	require.Equal(
		t,
		`{"a":1,"n":9007199254740993}`,
		canonicalArguments([]byte(` { "n": 9007199254740993, "a": 1 } `)),
	)
	require.NotEqual(
		t,
		canonicalArguments([]byte(`{"n":9007199254740992}`)),
		canonicalArguments([]byte(`{"n":9007199254740993}`)),
	)
	require.Equal(
		t,
		`{"query":"a  b",}`,
		canonicalArguments([]byte(`  {"query":"a  b",}  `)),
	)
	require.Equal(t, `1 2`, canonicalArguments([]byte(` 1 2 `)))
}

func TestFingerprintRoundBoundsMultimodalPayloads(t *testing.T) {
	text := strings.Repeat("text", 1<<16)
	binary := bytes.Repeat([]byte{0x5a}, 1<<20)
	result := model.NewToolMessage("call-1", "inspect", strings.Repeat("result", 1<<15))
	result.ReasoningContent = "reasoning"
	result.ReasoningSignature = "signature"
	result.ContentParts = []model.ContentPart{
		{Type: model.ContentTypeText, Text: &text},
		{Type: model.ContentTypeImage, Image: &model.Image{Data: binary, Format: "png"}},
		{Type: model.ContentTypeAudio, Audio: &model.Audio{Data: binary, Format: "wav"}},
		{Type: model.ContentTypeVideo, Video: &model.Video{Data: binary, Format: "mp4"}},
		{Type: model.ContentTypeFile, File: &model.File{Data: binary, Name: "data.bin"}},
		{Type: model.ContentTypeFile, ContentRef: &model.ContentRef{ArtifactRef: "artifact://data@0"}},
	}
	toolCalls := []model.ToolCall{newToolCall("call-1", "inspect", `{}`)}
	fingerprint, ok := fingerprintRound(toolCalls, []model.Message{result})
	require.True(t, ok)
	require.NotEmpty(t, fingerprint)
	require.Len(t, result.ContentParts[1].Image.Data, len(binary))

	changedResult := result
	changedResult.ContentParts = append([]model.ContentPart(nil), result.ContentParts...)
	changedImage := *result.ContentParts[1].Image
	changedImage.Data = append([]byte(nil), binary...)
	changedImage.Data[len(changedImage.Data)-1]++
	changedResult.ContentParts[1].Image = &changedImage
	changedFingerprint, ok := fingerprintRound(toolCalls, []model.Message{changedResult})
	require.True(t, ok)
	require.NotEqual(t, fingerprint, changedFingerprint)
	require.Nil(t, digestBytes(nil))
	require.Len(t, digestBytes([]byte{}), sha256Size)
}

func TestFingerprintRoundIgnoresUnexpectedResultFields(t *testing.T) {
	toolCalls := []model.ToolCall{newToolCall("call-1", "search", `{}`)}
	base := model.NewToolMessage("call-1", "search", "same")
	baseFingerprint, ok := fingerprintRound(toolCalls, []model.Message{base})
	require.True(t, ok)

	unexpected := base
	unexpected.ToolName = "different"
	unexpected.ToolCalls = []model.ToolCall{{
		Function: model.FunctionDefinitionParam{
			Arguments: bytes.Repeat([]byte("argument"), 1<<16),
		},
		ExtraFields: map[string]any{"unsupported": make(chan int)},
	}}
	fingerprint, ok := fingerprintRound(toolCalls, []model.Message{unexpected})
	require.True(t, ok)
	require.Equal(t, baseFingerprint, fingerprint)
}

func roundMessages(
	toolCalls []model.ToolCall,
	results []model.Message,
) []model.Message {
	messages := []model.Message{{
		Role:      model.RoleAssistant,
		ToolCalls: toolCalls,
	}}
	return append(messages, results...)
}

func assistantToolMessage(toolCalls ...model.ToolCall) model.Message {
	return model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: toolCalls,
	}
}

const sha256Size = 32

func newToolCall(id, name, arguments string) model.ToolCall {
	return model.ToolCall{
		ID:   id,
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      name,
			Arguments: []byte(arguments),
		},
	}
}
