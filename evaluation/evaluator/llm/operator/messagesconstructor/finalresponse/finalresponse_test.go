//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finalresponse

import (
	"context"
	"fmt"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConstructMessagesBuildsPrompt(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "user prompt"},
		FinalResponse: &model.Message{Content: "actual answer"},
	}
	expected := &evalset.Invocation{
		FinalResponse: &model.Message{Content: "expected answer"},
	}
	messages, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, []*evalset.Invocation{expected}, &metric.EvalMetric{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, model.RoleUser, messages[0].Role)
	assert.Contains(t, messages[0].Content, "user prompt")
	assert.Contains(t, messages[0].Content, "actual answer")
	assert.Contains(t, messages[0].Content, "expected answer")
}

func TestConstructMessagesTemplateError(t *testing.T) {
	original := finalResponsePromptTemplate
	t.Cleanup(func() { finalResponsePromptTemplate = original })
	finalResponsePromptTemplate = template.Must(template.New("err").Funcs(template.FuncMap{
		"explode": func() (string, error) { return "", fmt.Errorf("boom") },
	}).Parse(`{{explode}}`))

	constructor := New()
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{}, []*evalset.Invocation{}, nil)
	require.Error(t, err)
}
