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
