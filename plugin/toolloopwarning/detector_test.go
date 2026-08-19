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

func TestFingerprintRoundDetectsSemanticFieldsChanging(t *testing.T) {
	baseFingerprint, ok := fingerprintRound(
		[]model.ToolCall{newToolCall("call-1", "search", `{"query":"x"}`)},
		[]model.Message{model.NewToolMessage("call-1", "search", "same")},
	)
	require.True(t, ok)

	tests := map[string]struct {
		call   model.ToolCall
		result model.Message
	}{
		"tool name": {
			call:   newToolCall("call-2", "read", `{"query":"x"}`),
			result: model.NewToolMessage("call-2", "read", "same"),
		},
		"arguments": {
			call:   newToolCall("call-2", "search", `{"query":"y"}`),
			result: model.NewToolMessage("call-2", "search", "same"),
		},
		"result content": {
			call:   newToolCall("call-2", "search", `{"query":"x"}`),
			result: model.NewToolMessage("call-2", "search", "changed"),
		},
		"result tool name": {
			call:   newToolCall("call-2", "search", `{"query":"x"}`),
			result: model.NewToolMessage("call-2", "different", "same"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fingerprint, ok := fingerprintRound(
				[]model.ToolCall{test.call},
				[]model.Message{test.result},
			)
			require.True(t, ok)
			require.NotEqual(t, baseFingerprint, fingerprint)
		})
	}
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
	require.NotEqual(
		t,
		canonicalArguments([]byte(`{"query":"a  b",}`)),
		canonicalArguments([]byte(`{"query":"a b",}`)),
	)
	require.Equal(t, `1 2`, canonicalArguments([]byte(` 1 2 `)))
}

func TestDetectorRejectsMalformedRounds(t *testing.T) {
	_, ok := toolRoundIdentity(nil)
	require.False(t, ok)
	_, ok = toolRoundIdentity([]model.ToolCall{newToolCall("", "search", `{}`)})
	require.False(t, ok)
	_, ok = toolRoundIdentity([]model.ToolCall{newToolCall("call", "", `{}`)})
	require.False(t, ok)
	_, ok = toolRoundIdentity([]model.ToolCall{
		newToolCall("call", "search", `{}`),
		newToolCall("call", "read", `{}`),
	})
	require.False(t, ok)

	toolCalls := []model.ToolCall{newToolCall("call", "search", `{}`)}
	invalidResults := []model.Message{
		model.NewToolMessage("", "search", "same"),
		model.NewToolMessage("other", "search", "same"),
		model.NewAssistantMessage("same"),
		{Role: model.RoleTool, ToolID: "call", Content: "same"},
	}
	for _, result := range invalidResults {
		state := &detectorState{}
		require.False(t, state.observeToolMessages(
			toolCalls,
			[]model.Message{result},
		))
		require.Empty(t, state.previous)
		require.Nil(t, state.pending)
	}
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
	changedFingerprint, ok := fingerprintRound(
		toolCalls,
		[]model.Message{changedResult},
	)
	require.True(t, ok)
	require.NotEqual(t, fingerprint, changedFingerprint)
	require.Nil(t, digestBytes(nil))
	require.Len(t, digestBytes([]byte{}), sha256Size)
}

func TestFingerprintRoundIgnoresUnexpectedResultToolCalls(t *testing.T) {
	toolCalls := []model.ToolCall{newToolCall("call-1", "search", `{}`)}
	base := model.NewToolMessage("call-1", "search", "same")
	baseFingerprint, ok := fingerprintRound(toolCalls, []model.Message{base})
	require.True(t, ok)

	withToolCalls := base
	withToolCalls.ToolCalls = []model.ToolCall{{
		Function: model.FunctionDefinitionParam{
			Arguments: bytes.Repeat([]byte("argument"), 1<<16),
		},
		ExtraFields: map[string]any{"unsupported": make(chan int)},
	}}
	fingerprint, ok := fingerprintRound(
		toolCalls,
		[]model.Message{withToolCalls},
	)
	require.True(t, ok)
	require.Equal(t, baseFingerprint, fingerprint)
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
