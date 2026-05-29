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

	"trpc.group/trpc-go/trpc-agent-go/model"
)

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

func TestMessageContentForSummary(t *testing.T) {
	t.Parallel()
	text := "actual text"
	got := messageContentForSummary(model.Message{
		Content: "lead",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
			{Type: model.ContentTypeImage},
			{Type: model.ContentTypeAudio},
			{Type: model.ContentTypeFile, File: &model.File{Name: "doc.txt"}},
		},
	})
	assert.Contains(t, got, "lead")
	assert.Contains(t, got, "actual text")
	assert.Contains(t, got, "[image attachment]")
	assert.Contains(t, got, "[audio attachment]")
	assert.Contains(t, got, "[file attachment: doc.txt]")
	require.Empty(t, messageContentForSummary(model.Message{}))

	// A text part mirroring Content must not be emitted twice.
	mirror := "same text"
	dedup := messageContentForSummary(model.Message{
		Content: "same text",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &mirror},
		},
	})
	assert.Equal(t, "same text", dedup)
}

func TestFilePartSummary(t *testing.T) {
	t.Parallel()
	t.Run("nil file", func(t *testing.T) {
		assert.Equal(t, "[file attachment]", filePartSummary(nil))
	})
	t.Run("name", func(t *testing.T) {
		assert.Equal(t, "[file attachment: a.pdf]", filePartSummary(&model.File{Name: "a.pdf"}))
	})
	t.Run("raw url is not persisted", func(t *testing.T) {
		assert.Equal(
			t,
			"[file attachment]",
			filePartSummary(&model.File{URL: "https://x/y?token=secret"}),
		)
	})
	t.Run("file id fallback", func(t *testing.T) {
		assert.Equal(t, "[file attachment: file-123]", filePartSummary(&model.File{FileID: "file-123"}))
	})
	t.Run("prefers file id over url", func(t *testing.T) {
		assert.Equal(
			t,
			"[file attachment: file-123]",
			filePartSummary(&model.File{URL: "https://x/y", FileID: "file-123"}),
		)
	})
	t.Run("blank file", func(t *testing.T) {
		assert.Equal(t, "[file attachment]", filePartSummary(&model.File{}))
	})
}
