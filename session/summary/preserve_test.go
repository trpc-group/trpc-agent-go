//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func userEvt(content string) event.Event {
	return event.Event{
		Author: authorUser,
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: content,
				},
			}},
		},
	}
}

func systemEvt(content string) event.Event {
	return event.Event{
		Author: authorSystem,
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleSystem,
					Content: content,
				},
			}},
		},
	}
}

func TestFormatDetailedSummaryOutput(t *testing.T) {
	t.Parallel()
	t.Run("removes analysis and unwraps summary tag", func(t *testing.T) {
		got := formatDetailedSummaryOutput(
			"<analysis>scratch</analysis>\n<summary>kept</summary>",
		)
		assert.Equal(t, "kept", got)
	})
	t.Run("returns trimmed text when no tags", func(t *testing.T) {
		assert.Equal(t, "plain summary", formatDetailedSummaryOutput("  plain summary  "))
	})
	t.Run("strips analysis even without summary wrapper", func(t *testing.T) {
		got := formatDetailedSummaryOutput("<analysis>x</analysis>\nplain")
		assert.Equal(t, "plain", got)
	})
}

func TestExtractPreservedUserMessages(t *testing.T) {
	t.Parallel()
	t.Run("returns nil when no marker", func(t *testing.T) {
		assert.Nil(t, extractPreservedUserMessages("no marker here"))
	})
	t.Run("ignores unterminated marker", func(t *testing.T) {
		text := preservedUserMessagesStart + "\n[]" // no end tag
		assert.Nil(t, extractPreservedUserMessages(text))
	})
	t.Run("recovers from invalid JSON payload", func(t *testing.T) {
		text := preservedUserMessagesStart + "\nnot-json\n" + preservedUserMessagesEnd
		// Invalid JSON is silently dropped; subsequent valid blocks still parse.
		text += "\n" + appendPreservedUserMessages("", []string{"second"})
		got := extractPreservedUserMessages(text)
		assert.Equal(t, []string{"second"}, got)
	})
	t.Run("parses multiple blocks", func(t *testing.T) {
		summary := appendPreservedUserMessages(
			appendPreservedUserMessages("", []string{"a"}),
			[]string{"b"},
		)
		// appendPreservedUserMessages strips previous marker before adding,
		// so this only contains the latest set.
		got := extractPreservedUserMessages(summary)
		assert.Equal(t, []string{"b"}, got)
	})
}

func TestExtractPreservedUserMessagesFromEvents(t *testing.T) {
	t.Parallel()
	t.Run("ignores non-system carriers", func(t *testing.T) {
		evt := userEvt(appendPreservedUserMessages("", []string{"hidden in user"}))
		assert.Nil(t, extractPreservedUserMessagesFromEvents([]event.Event{evt}))
	})
	t.Run("ignores events without response", func(t *testing.T) {
		assert.Nil(t, extractPreservedUserMessagesFromEvents([]event.Event{{Author: authorSystem}}))
	})
	t.Run("collects from system carriers", func(t *testing.T) {
		evt := systemEvt(appendPreservedUserMessages("prev summary", []string{"carried"}))
		got := extractPreservedUserMessagesFromEvents([]event.Event{evt})
		assert.Equal(t, []string{"carried"}, got)
	})
}

func TestStripPreservedUserMessagesFromEvents(t *testing.T) {
	t.Parallel()
	t.Run("returns same slice when nothing changes", func(t *testing.T) {
		events := []event.Event{userEvt("clean"), systemEvt("no marker")}
		got := stripPreservedUserMessagesFromEvents(events)
		assert.Equal(t, events, got)
	})
	t.Run("returns empty when input empty", func(t *testing.T) {
		assert.Equal(t, []event.Event{}, stripPreservedUserMessagesFromEvents([]event.Event{}))
	})
	t.Run("removes preserved block from system carrier and clones response", func(t *testing.T) {
		original := appendPreservedUserMessages("base", []string{"x"})
		events := []event.Event{systemEvt(original)}
		got := stripPreservedUserMessagesFromEvents(events)
		require.Len(t, got, 1)
		assert.NotContains(t, got[0].Response.Choices[0].Message.Content, preservedUserMessagesStart)
		// Original event must remain untouched (clone semantics).
		assert.Contains(t, events[0].Response.Choices[0].Message.Content, preservedUserMessagesStart)
	})
}

func TestAppendPreservedUserMessages(t *testing.T) {
	t.Parallel()
	t.Run("returns trimmed summary when no messages", func(t *testing.T) {
		assert.Equal(t, "summary", appendPreservedUserMessages("  summary  ", nil))
	})
	t.Run("strips previous marker before re-appending", func(t *testing.T) {
		first := appendPreservedUserMessages("base", []string{"old"})
		second := appendPreservedUserMessages(first, []string{"new"})
		// Only one marker block should remain.
		got := extractPreservedUserMessages(second)
		assert.Equal(t, []string{"new"}, got)
	})
}

func TestExtractUserMessagesAndHelpers(t *testing.T) {
	t.Parallel()
	t.Run("skips tool-id messages", func(t *testing.T) {
		evt := event.Event{
			Author: authorUser,
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "tool reply",
						ToolID:  "tool-1",
					},
				}},
			},
		}
		assert.Nil(t, extractUserMessages([]event.Event{evt}))
	})
	t.Run("skips events with empty content", func(t *testing.T) {
		evt := userEvt("")
		assert.Nil(t, extractUserMessages([]event.Event{evt}))
	})
	t.Run("collects content parts including attachments", func(t *testing.T) {
		text := "actual text"
		evt := event.Event{
			Author: authorUser,
			Response: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role: model.RoleUser,
						ContentParts: []model.ContentPart{
							{Type: model.ContentTypeText, Text: &text},
							{Type: model.ContentTypeImage},
							{Type: model.ContentTypeAudio},
							{Type: model.ContentTypeFile, File: &model.File{Name: "doc.txt"}},
						},
					},
				}},
			},
		}
		got := extractUserMessages([]event.Event{evt})
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "actual text")
		assert.Contains(t, got[0], "[image attachment]")
		assert.Contains(t, got[0], "[audio attachment]")
		assert.Contains(t, got[0], "[file attachment: doc.txt]")
	})
}

func TestFilePartSummary(t *testing.T) {
	t.Parallel()
	t.Run("nil file", func(t *testing.T) {
		assert.Equal(t, "[file attachment]", filePartSummary(nil))
	})
	t.Run("name", func(t *testing.T) {
		assert.Equal(t, "[file attachment: a.pdf]", filePartSummary(&model.File{Name: "a.pdf"}))
	})
	t.Run("url fallback", func(t *testing.T) {
		assert.Equal(t, "[file attachment: https://x/y]", filePartSummary(&model.File{URL: "https://x/y"}))
	})
	t.Run("file id fallback", func(t *testing.T) {
		assert.Equal(t, "[file attachment: file-123]", filePartSummary(&model.File{FileID: "file-123"}))
	})
	t.Run("blank file", func(t *testing.T) {
		assert.Equal(t, "[file attachment]", filePartSummary(&model.File{}))
	})
}

func TestPrepareSummaryEventsAndUserMessages(t *testing.T) {
	t.Parallel()
	previous := appendPreservedUserMessages("prev", []string{"old"})
	events := []event.Event{
		systemEvt(previous),
		userEvt("new1"),
		userEvt("new2"),
	}
	stripped, carried := prepareSummaryEventsAndUserMessages(events)
	require.Len(t, stripped, 3)
	assert.NotContains(
		t,
		stripped[0].Response.Choices[0].Message.Content,
		preservedUserMessagesStart,
	)
	assert.Equal(t, []string{"old"}, carried)
}
