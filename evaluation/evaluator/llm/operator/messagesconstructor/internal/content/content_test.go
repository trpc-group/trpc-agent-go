//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package content

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestExtractTextFromContent(t *testing.T) {
	content := &model.Message{Content: "hello world"}
	assert.Equal(t, "hello world", ExtractTextFromContent(content))
	assert.Equal(t, "", ExtractTextFromContent(&model.Message{}))
}

func TestExtractTextFromContentCombinesContentAndParts(t *testing.T) {
	partText := "part text"
	content := &model.Message{
		Content: "base content",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &partText},
		},
	}
	assert.Equal(t, "base content\npart text", ExtractTextFromContent(content))
}

func TestExtractTextFromContentFormatsNonTextParts(t *testing.T) {
	content := &model.Message{
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.com/a.png"}},
			{Type: model.ContentTypeFile, File: &model.File{Name: "a.txt"}},
		},
	}
	out := ExtractTextFromContent(content)
	assert.Contains(t, out, "[image:https://example.com/a.png]")
	assert.Contains(t, out, "[file:a.txt]")
}

func TestFormatContextMessagesIncludesRolePrefixes(t *testing.T) {
	context := []*model.Message{
		{Role: model.RoleSystem, Content: "sys"},
		{Role: model.RoleUser, Content: "u"},
	}
	out := FormatContextMessages(context)
	assert.Contains(t, out, "[system] sys")
	assert.Contains(t, out, "[user] u")
}

func TestExtractRubrics(t *testing.T) {
	rubrics := []*llm.Rubric{
		{ID: "1", Content: &llm.RubricContent{Text: "foo"}},
		nil,
		{ID: "skip", Content: nil},
		{ID: "2", Content: &llm.RubricContent{Text: "bar"}},
	}
	assert.Equal(t, "1: foo\n2: bar\n", ExtractRubrics(rubrics))
	assert.Equal(t, "", ExtractRubrics(nil))
}
