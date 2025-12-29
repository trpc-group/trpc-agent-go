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
	return content.Content
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
