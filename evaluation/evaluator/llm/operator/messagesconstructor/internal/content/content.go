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
	"encoding/json"
	"strings"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
)

// ExtractTextFromContent extracts plain text from genai content.
func ExtractTextFromContent(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var text strings.Builder
	for _, part := range content.Parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

// ExtractIntermediateData extracts intermediate data from evalset.IntermediateData.
func ExtractIntermediateData(intermediateData *evalset.IntermediateData) (string, error) {
	data, err := json.Marshal(intermediateData)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ExtractRubrics extracts rubrics from llm.Rubric.
func ExtractRubrics(rubrics []*llm.Rubric) string {
	if rubrics == nil {
		return ""
	}
	var text strings.Builder
	for _, rubric := range rubrics {
		text.WriteString(rubric.ID)
		text.WriteString(": ")
		text.WriteString(rubric.Content.Text)
		text.WriteString("\n")
	}
	return text.String()
}
