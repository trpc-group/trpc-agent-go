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

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
)

func TestExtractTextFromContent(t *testing.T) {
	content := &genai.Content{
		Parts: []*genai.Part{
			{Text: "hello "},
			{Text: "world"},
		},
	}
	assert.Equal(t, "hello world", ExtractTextFromContent(content))
	assert.Equal(t, "", ExtractTextFromContent(nil))
}

func TestExtractIntermediateData(t *testing.T) {
	data := &evalset.IntermediateData{
		ToolUses: []*genai.FunctionCall{
			{ID: "1", Name: "tool", Args: map[string]any{"k": "v"}},
		},
		ToolResponses: []*genai.FunctionResponse{
			{ID: "1", Name: "tool", Response: map[string]any{"r": "v"}},
		},
	}
	result, err := ExtractIntermediateData(data)
	assert.NoError(t, err)
	assert.Contains(t, result, `"toolUses"`)
	assert.Contains(t, result, `"toolResponses"`)

	// Marshal error path.
	_, err = ExtractIntermediateData(&evalset.IntermediateData{
		ToolUses: []*genai.FunctionCall{{Args: map[string]any{"x": make(chan int)}}},
	})
	assert.Error(t, err)
}

func TestExtractRubrics(t *testing.T) {
	rubrics := []*llm.Rubric{
		{ID: "1", Content: &llm.RubricContent{Text: "foo"}},
		{ID: "2", Content: &llm.RubricContent{Text: "bar"}},
	}
	assert.Equal(t, "1: foo\n2: bar\n", ExtractRubrics(rubrics))
	assert.Equal(t, "", ExtractRubrics(nil))
}
