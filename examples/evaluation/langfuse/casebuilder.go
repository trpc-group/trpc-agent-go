//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
	langfuseeval "trpc.group/trpc-go/trpc-agent-go/server/evaluation/langfuse"
)

func buildCaseSpec(_ context.Context, item *langfuseeval.DatasetItem) (*langfuseeval.CaseSpec, error) {
	if item == nil {
		return nil, fmt.Errorf("dataset item is nil")
	}
	question, err := requiredStringField(item.Input, "input", "question")
	if err != nil {
		return nil, err
	}
	answer, err := requiredStringField(item.ExpectedOutput, "expectedOutput", "answer")
	if err != nil {
		return nil, err
	}
	expectedTools, err := expectedToolsFromMetadata(item.Metadata)
	if err != nil {
		return nil, err
	}
	return &langfuseeval.CaseSpec{
		DatasetItemID: item.ID,
		TraceInput:    item.Input,
		EvalCase: &evalset.EvalCase{
			EvalID: item.ID,
			Conversation: []*evalset.Invocation{
				{
					UserContent: &model.Message{
						Role:    model.RoleUser,
						Content: question,
					},
					FinalResponse: &model.Message{
						Role:    model.RoleAssistant,
						Content: answer,
					},
					Tools: expectedTools,
				},
			},
		},
	}, nil
}

func requiredStringField(raw any, objectName string, key string) (string, error) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return "", fmt.Errorf("%s must be a JSON object", objectName)
	}
	value, ok := fields[key].(string)
	if !ok {
		return "", fmt.Errorf("%s.%s must be a string", objectName, key)
	}
	return value, nil
}

func expectedToolsFromMetadata(raw any) ([]*evalset.Tool, error) {
	if raw == nil {
		return nil, nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("metadata must be a JSON object")
	}
	rawTools, ok := fields["expectedTools"]
	if !ok || rawTools == nil {
		return nil, nil
	}
	entries, ok := rawTools.([]any)
	if !ok {
		return nil, fmt.Errorf("metadata.expectedTools must be an array")
	}
	tools := make([]*evalset.Tool, 0, len(entries))
	for idx, entry := range entries {
		toolFields, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("metadata.expectedTools[%d] must be a JSON object", idx)
		}
		name, ok := toolFields["name"].(string)
		if !ok {
			return nil, fmt.Errorf("metadata.expectedTools[%d].name must be a string", idx)
		}
		tools = append(tools, &evalset.Tool{
			Name:      name,
			Arguments: toolFields["arguments"],
			Result:    toolFields["result"],
		})
	}
	return tools, nil
}
