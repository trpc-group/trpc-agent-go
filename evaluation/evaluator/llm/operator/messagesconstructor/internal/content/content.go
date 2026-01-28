//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package content extracts and formats conversation artifacts for judge prompts.
package content

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ExtractTextFromContent extracts plain text from model message.
func ExtractTextFromContent(content *model.Message) string {
	if content == nil {
		return ""
	}
	var parts []string
	if strings.TrimSpace(content.Content) != "" {
		parts = append(parts, content.Content)
	}
	for _, part := range content.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil && strings.TrimSpace(*part.Text) != "" {
				parts = append(parts, *part.Text)
			}
		case model.ContentTypeImage:
			if part.Image != nil && strings.TrimSpace(part.Image.URL) != "" {
				parts = append(parts, "[image:"+part.Image.URL+"]")
			} else {
				parts = append(parts, "[image]")
			}
		case model.ContentTypeAudio:
			parts = append(parts, "[audio]")
		case model.ContentTypeFile:
			if part.File != nil && strings.TrimSpace(part.File.Name) != "" {
				parts = append(parts, "[file:"+part.File.Name+"]")
			} else {
				parts = append(parts, "[file]")
			}
		default:
			parts = append(parts, "[content]")
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// FormatContextMessages formats context messages into a judge-friendly text block.
func FormatContextMessages(messages []*model.Message) string {
	if len(messages) == 0 {
		return ""
	}
	var text strings.Builder
	for _, message := range messages {
		if message == nil {
			continue
		}
		msgContent := ExtractTextFromContent(message)
		if strings.TrimSpace(msgContent) == "" {
			continue
		}
		text.WriteString("[")
		text.WriteString(message.Role.String())
		text.WriteString("] ")
		text.WriteString(msgContent)
		text.WriteString("\n")
	}
	return strings.TrimSpace(text.String())
}

// ExtractRubrics extracts rubrics from llm.Rubric.
func ExtractRubrics(rubrics []*llm.Rubric) string {
	if rubrics == nil {
		return ""
	}
	var text strings.Builder
	for _, rubric := range rubrics {
		if rubric == nil || rubric.Content == nil {
			continue
		}
		text.WriteString(rubric.ID)
		text.WriteString(": ")
		text.WriteString(rubric.Content.Text)
		text.WriteString("\n")
	}
	return text.String()
}
