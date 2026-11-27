//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestPromptContentFromMessageSupportsTextAndResourceLinks(t *testing.T) {
	t.Parallel()

	text := "second part"
	message := model.Message{
		Content: "first part",
		ContentParts: []model.ContentPart{
			{
				Type: model.ContentTypeText,
				Text: &text,
			},
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     "notes.txt",
					URL:      "file:///tmp/notes.txt",
					MimeType: "text/plain",
				},
			},
		},
	}
	blocks, err := promptContentFromMessage(message)
	require.NoError(t, err)
	require.Len(t, blocks, 3)
	require.Equal(t, "first part", blocks[0].Text.Text)
	require.Equal(t, "second part", blocks[1].Text.Text)
	require.Equal(t, "notes.txt", blocks[2].ResourceLink.Name)
	require.Equal(t, "file:///tmp/notes.txt", blocks[2].ResourceLink.Uri)
	require.Equal(t, "text/plain", *blocks[2].ResourceLink.MimeType)
}

func TestPromptContentFromMessageRejectsUnsupportedContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message model.Message
		want    string
	}{
		{name: "empty", want: "prompt is empty"},
		{
			name: "image",
			message: model.Message{
				ContentParts: []model.ContentPart{{
					Type:  model.ContentTypeImage,
					Image: &model.Image{URL: "https://example.test/image.png"},
				}},
			},
			want: "is not supported",
		},
		{
			name: "file without URL",
			message: model.Message{
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeFile,
					File: &model.File{Name: "notes.txt"},
				}},
			},
			want: "resource link URL",
		},
		{
			name: "nil file",
			message: model.Message{
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeFile,
				}},
			},
			want: "has no file",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := promptContentFromMessage(test.message)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestSessionUpdateTranslatorMapsStreamingEvents(t *testing.T) {
	t.Parallel()

	translator := newSessionUpdateTranslator("invocation-1", "agent")

	messageEvents, err := translator.translate(acpsdk.SessionNotification{
		Update: acpsdk.UpdateAgentMessageText("hello"),
	})
	require.NoError(t, err)
	require.Len(t, messageEvents, 1)
	require.True(t, messageEvents[0].Response.IsPartial)
	require.Equal(t, "hello", messageEvents[0].Response.Choices[0].Delta.Content)

	link := acpsdk.ResourceLinkBlock("notes.txt", "file:///tmp/notes.txt")
	mimeType := "text/plain"
	link.ResourceLink.MimeType = &mimeType
	linkEvents, err := translator.translate(acpsdk.SessionNotification{
		Update: acpsdk.UpdateAgentMessage(link),
	})
	require.NoError(t, err)
	require.Len(t, linkEvents, 1)
	linkParts := linkEvents[0].Response.Choices[0].Delta.ContentParts
	require.Len(t, linkParts, 1)
	require.Equal(t, model.ContentTypeFile, linkParts[0].Type)
	require.Equal(t, "file:///tmp/notes.txt", linkParts[0].File.URL)
	require.Equal(t, "text/plain", linkParts[0].File.MimeType)

	thoughtEvents, err := translator.translate(acpsdk.SessionNotification{
		Update: acpsdk.UpdateAgentThoughtText("thinking"),
	})
	require.NoError(t, err)
	require.Len(t, thoughtEvents, 1)
	require.Equal(t, "thinking", thoughtEvents[0].Response.Choices[0].Delta.ReasoningContent)

	toolEvents, err := translator.translate(acpsdk.SessionNotification{
		Update: acpsdk.StartToolCall(
			"tool-1",
			"Read file",
			acpsdk.WithStartRawInput(map[string]any{"path": "file.txt"}),
		),
	})
	require.NoError(t, err)
	require.Len(t, toolEvents, 1)
	toolCalls := toolEvents[0].Response.Choices[0].Message.ToolCalls
	require.Len(t, toolCalls, 1)
	require.Equal(t, "tool-1", toolCalls[0].ID)
	require.Equal(t, "Read file", toolCalls[0].Function.Name)
	require.JSONEq(t, `{"path":"file.txt"}`, string(toolCalls[0].Function.Arguments))
	require.Contains(t, toolEvents[0].Extensions, acpToolUpdateExtension)

	planEvents, err := translator.translate(acpsdk.SessionNotification{
		Update: acpsdk.UpdatePlan(acpsdk.PlanEntry{
			Content:  "Inspect file",
			Priority: acpsdk.PlanEntryPriorityHigh,
			Status:   acpsdk.PlanEntryStatusInProgress,
		}),
	})
	require.NoError(t, err)
	require.Len(t, planEvents, 1)
	require.Contains(t, planEvents[0].Extensions, acpPlanExtension)

	ignored, err := translator.translate(acpsdk.SessionNotification{
		Update: acpsdk.SessionUpdate{},
	})
	require.NoError(t, err)
	require.Empty(t, ignored)
}

func TestSessionUpdateTranslatorBuildsSingleFinalResponse(t *testing.T) {
	t.Parallel()

	translator := newSessionUpdateTranslator("invocation-1", "agent")
	_, err := translator.translate(acpsdk.SessionNotification{
		Update: acpsdk.UpdateAgentMessageText("hello"),
	})
	require.NoError(t, err)
	_, err = translator.translate(acpsdk.SessionNotification{
		Update: acpsdk.UpdateAgentMessageText(" world"),
	})
	require.NoError(t, err)
	_, err = translator.translate(acpsdk.SessionNotification{
		Update: acpsdk.UpdateAgentThoughtText("thinking"),
	})
	require.NoError(t, err)

	final := translator.finalEvent(acpsdk.PromptResponse{
		StopReason: acpsdk.StopReasonEndTurn,
		Usage: &acpsdk.Usage{
			InputTokens:  3,
			OutputTokens: 5,
			TotalTokens:  8,
		},
	})
	require.NotNil(t, final)
	require.True(t, final.Response.Done)
	require.False(t, final.Response.IsPartial)
	require.Equal(t, model.ObjectTypeChatCompletion, final.Response.Object)
	require.NotEqual(t, model.ObjectTypeRunnerCompletion, final.Response.Object)
	require.Equal(t, "hello world", final.Response.Choices[0].Message.Content)
	require.Equal(t, "thinking", final.Response.Choices[0].Message.ReasoningContent)
	require.Equal(t, 3, final.Response.Usage.PromptTokens)
	require.Equal(t, 5, final.Response.Usage.CompletionTokens)
	require.Equal(t, 8, final.Response.Usage.TotalTokens)
}
