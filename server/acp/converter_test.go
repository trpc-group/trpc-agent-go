//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acp

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestPromptToMessage(t *testing.T) {
	mimeType := "text/plain"
	message, err := promptToMessage([]acpsdk.ContentBlock{
		acpsdk.TextBlock("explain "),
		{
			ResourceLink: &acpsdk.ContentBlockResourceLink{
				Name:     "README.md",
				Uri:      "file:///workspace/README.md",
				MimeType: &mimeType,
				Type:     "resource_link",
			},
		},
		acpsdk.TextBlock("briefly"),
	})
	require.NoError(t, err)
	assert.Equal(t, model.RoleUser, message.Role)
	assert.Equal(t, "explain briefly", message.Content)
	require.Len(t, message.ContentParts, 1)
	require.NotNil(t, message.ContentParts[0].File)
	assert.Equal(t, "README.md", message.ContentParts[0].File.Name)
	assert.Equal(t, "file:///workspace/README.md", message.ContentParts[0].File.URL)
	assert.Equal(t, mimeType, message.ContentParts[0].File.MimeType)
}

func TestPromptToMessageRejectsUnadvertisedContent(t *testing.T) {
	tests := []struct {
		name  string
		block acpsdk.ContentBlock
		err   string
	}{
		{
			name:  "image",
			block: acpsdk.ImageBlock("aGVsbG8=", "image/png"),
			err:   "image content is not supported",
		},
		{
			name:  "audio",
			block: acpsdk.AudioBlock("aGVsbG8=", "audio/wav"),
			err:   "audio content is not supported",
		},
		{
			name: "embedded resource",
			block: acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
				TextResourceContents: &acpsdk.TextResourceContents{
					Uri:  "file:///workspace/README.md",
					Text: "contents",
				},
			}),
			err: "embedded resource content is not supported",
		},
		{
			name: "empty",
			err:  "empty content block",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := promptToMessage([]acpsdk.ContentBlock{test.block})
			assert.ErrorContains(t, err, test.err)
		})
	}
}

func TestTurnStateTranslatesRunnerEvents(t *testing.T) {
	state := newTurnState(true)
	finishReason := "length"
	events := []*event.Event{
		{
			InvocationID: "invocation",
			Response: &model.Response{
				ID:        "response",
				IsPartial: true,
				Choices: []model.Choice{{
					Delta: model.Message{
						Content:          "hell",
						ReasoningContent: "think",
					},
				}},
			},
		},
		{
			InvocationID: "invocation",
			Response: &model.Response{
				ID: "response",
				Choices: []model.Choice{{
					Message: model.Message{
						Content:          "hello",
						ReasoningContent: "thinking",
						ToolCalls: []model.ToolCall{{
							ID: "call-1",
							Function: model.FunctionDefinitionParam{
								Name:      "search",
								Arguments: []byte(`{"query":"ACP"}`),
							},
						}},
					},
					FinishReason: &finishReason,
				}},
				Usage: &model.Usage{
					PromptTokens:     3,
					CompletionTokens: 5,
					TotalTokens:      8,
				},
			},
		},
		{
			InvocationID: "invocation",
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  "call-1",
						Content: "result",
					},
				}},
			},
		},
	}

	var updates []acpsdk.SessionUpdate
	for _, evt := range events {
		translated, err := state.translate(evt)
		require.NoError(t, err)
		updates = append(updates, translated...)
	}
	require.Len(t, updates, 6)
	assert.Equal(t, "think", updates[0].AgentThoughtChunk.Content.Text.Text)
	assert.Equal(t, "hell", updates[1].AgentMessageChunk.Content.Text.Text)
	assert.Equal(t, "ing", updates[2].AgentThoughtChunk.Content.Text.Text)
	assert.Equal(t, "o", updates[3].AgentMessageChunk.Content.Text.Text)
	require.NotNil(t, updates[4].ToolCall)
	assert.Equal(t, acpsdk.ToolCallId("call-1"), updates[4].ToolCall.ToolCallId)
	require.NotNil(t, updates[5].ToolCallUpdate)
	assert.Equal(t, acpsdk.ToolCallStatusCompleted, *updates[5].ToolCallUpdate.Status)

	response := state.response(nil)
	assert.Equal(t, acpsdk.StopReasonMaxTokens, response.StopReason)
	require.NotNil(t, response.Usage)
	assert.Equal(t, 8, response.Usage.TotalTokens)
}

func TestTurnStateAccumulatesUsageAcrossModelCalls(t *testing.T) {
	state := newTurnState(false)
	for _, usage := range []*model.Usage{
		{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5},
		{PromptTokens: 4, CompletionTokens: 1, TotalTokens: 5},
	} {
		_, err := state.translate(&event.Event{
			Response: &model.Response{Usage: usage},
		})
		require.NoError(t, err)
	}

	response := state.response(nil)
	require.NotNil(t, response.Usage)
	assert.Equal(t, 6, response.Usage.InputTokens)
	assert.Equal(t, 4, response.Usage.OutputTokens)
	assert.Equal(t, 10, response.Usage.TotalTokens)
}

func TestTurnStateSeparatesCompletedResponsesWithoutIDs(t *testing.T) {
	state := newTurnState(false)
	for _, text := range []string{"first", "second"} {
		updates, err := state.translate(&event.Event{
			InvocationID: "invocation",
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{Content: text},
				}},
			},
		})
		require.NoError(t, err)
		require.Len(t, updates, 1)
		assert.Equal(t, text, updates[0].AgentMessageChunk.Content.Text.Text)
	}
}

func TestTurnStateHandlesErrorsAndEmptyEvents(t *testing.T) {
	state := newTurnState(false)
	for _, evt := range []*event.Event{nil, {}} {
		updates, err := state.translate(evt)
		require.NoError(t, err)
		assert.Empty(t, updates)
	}

	wantErr := &model.ResponseError{Message: "model failed"}
	_, err := state.translate(&event.Event{
		Response: &model.Response{
			Done:  true,
			Error: wantErr,
		},
	})
	assert.ErrorIs(t, err, wantErr)
}

func TestTurnStateUpdatesToolCalls(t *testing.T) {
	state := newTurnState(false)
	toolCall := model.ToolCall{
		ID: "call-1",
		Function: model.FunctionDefinitionParam{
			Arguments: []byte(`{"query":"ACP"}`),
		},
	}

	updates := state.translateToolCalls([]model.ToolCall{{}, toolCall})
	require.Len(t, updates, 1)
	assert.Equal(t, "Tool call", updates[0].ToolCall.Title)

	assert.Empty(t, state.translateToolCalls([]model.ToolCall{toolCall}))
	toolCall.Function.Name = "search"
	toolCall.Function.Arguments = []byte("incomplete JSON")
	updates = state.translateToolCalls([]model.ToolCall{toolCall})
	require.Len(t, updates, 1)
	require.NotNil(t, updates[0].ToolCallUpdate)
	assert.Equal(t, "search", *updates[0].ToolCallUpdate.Title)
}

func TestTurnStateMapsFinishReasons(t *testing.T) {
	tests := []struct {
		reason string
		want   acpsdk.StopReason
	}{
		{reason: "content_filter", want: acpsdk.StopReasonRefusal},
		{reason: "refusal", want: acpsdk.StopReasonRefusal},
		{reason: "cancelled", want: acpsdk.StopReasonCancelled},
		{reason: "canceled", want: acpsdk.StopReasonCancelled},
		{reason: "stop", want: acpsdk.StopReasonEndTurn},
	}
	for _, test := range tests {
		t.Run(test.reason, func(t *testing.T) {
			state := newTurnState(false)
			state.applyFinishReason(nil)
			state.applyFinishReason(&test.reason)
			assert.Equal(t, test.want, state.response(nil).StopReason)
		})
	}
}
