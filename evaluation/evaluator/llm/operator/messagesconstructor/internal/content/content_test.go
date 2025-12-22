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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestExtractTextFromContent(t *testing.T) {
	content := &model.Message{Content: "hello world"}
	assert.Equal(t, "hello world", ExtractTextFromContent(content))
	assert.Equal(t, "", ExtractTextFromContent(&model.Message{}))
}

func TestExtractIntermediateData(t *testing.T) {
	data := &evalset.IntermediateData{
		ToolCalls: []*model.ToolCall{
			{ID: "1", Type: "function", Function: model.FunctionDefinitionParam{Name: "tool", Arguments: []byte(`{"k": "v"}`)}},
		},
		ToolResponses: []*model.Message{
			{ToolID: "1", ToolName: "tool", Content: "{\"r\": \"v\"}"},
		},
	}
	result, err := ExtractIntermediateData(data)
	assert.NoError(t, err)
	assert.Contains(t, result, `"toolCalls"`)
	assert.Contains(t, result, `"toolResponses"`)

	// Marshal error path.
	_, err = ExtractIntermediateData(&evalset.IntermediateData{
		ToolCalls: []*model.ToolCall{{Type: "function", Function: model.FunctionDefinitionParam{Name: "tool", Arguments: []byte(`{"x": make(chan int)}`)}}},
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
