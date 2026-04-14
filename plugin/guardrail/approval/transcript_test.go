//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package approval

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestTranscriptContent_CoversOptionalBranches(t *testing.T) {
	text := "hello"
	require.Equal(
		t,
		"hello",
		transcriptContent(model.Message{
			Role:         model.RoleAssistant,
			ContentParts: []model.ContentPart{{Type: model.ContentTypeText, Text: &text}},
		}),
	)
	require.Equal(t, "", transcriptContent(model.Message{Role: model.RoleUser, Content: " "}))
	require.Equal(
		t,
		"tool result: [empty tool result]",
		transcriptContent(model.Message{Role: model.RoleTool}),
	)
}

func TestToolCallSummary_UsesFallbacks(t *testing.T) {
	assert.Equal(
		t,
		"tool unknown call: {}",
		toolCallSummary(model.ToolCall{}),
	)
	assert.Equal(
		t,
		`tool shell call: {`,
		toolCallSummary(model.ToolCall{
			Function: model.FunctionDefinitionParam{
				Name:      "shell",
				Arguments: []byte("{"),
			},
		}),
	)
}

func TestCloneJSON_ClonesBytesAndHandlesEmptyInput(t *testing.T) {
	require.Nil(t, cloneJSON(nil))
	original := []byte(`{"command":"pwd"}`)
	cloned := cloneJSON(original)
	require.NotNil(t, cloned)
	original[0] = '['
	assert.NotEqual(t, original[0], cloned[0])
}

func TestCompactJSON_InvalidJSONReturnsOriginal(t *testing.T) {
	assert.Equal(t, "{", compactJSON([]byte("{")))
}

func TestBuildTranscript_EmptyEventsReturnsNil(t *testing.T) {
	p := &Plugin{tokenCounter: model.NewSimpleTokenCounter()}
	invocation := invocationWithEvents(t, nil)
	assert.Nil(t, p.buildTranscript(context.Background(), invocation))
}
