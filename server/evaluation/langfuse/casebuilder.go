//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// CaseSpec is the framework-native case definition derived from a dataset item.
type CaseSpec struct {
	DatasetItemID string
	TraceName     string
	TraceInput    any
	SessionID     string
	UserID        string
	TraceMetadata map[string]any
	EvalCase      *evalset.EvalCase
}

// CaseBuilder converts one Langfuse dataset item into a framework-native case specification.
type CaseBuilder func(ctx context.Context, item *DatasetItem) (*CaseSpec, error)

func buildCaseSpec(_ context.Context, item *DatasetItem) (*CaseSpec, error) {
	if item == nil {
		return nil, errors.New("dataset item is nil")
	}
	prompt, err := stringifyValue("input", item.Input)
	if err != nil {
		return nil, fmt.Errorf("resolve input: %w", err)
	}
	expectedOutput, err := stringifyValue("expected output", item.ExpectedOutput)
	if err != nil {
		return nil, fmt.Errorf("resolve expected output: %w", err)
	}
	invocation := &evalset.Invocation{
		UserContent: &model.Message{
			Role:    model.RoleUser,
			Content: prompt,
		},
		FinalResponse: &model.Message{
			Role:    model.RoleAssistant,
			Content: expectedOutput,
		},
	}
	return &CaseSpec{
		DatasetItemID: item.ID,
		TraceInput:    item.Input,
		EvalCase: &evalset.EvalCase{
			EvalID:       item.ID,
			Conversation: []*evalset.Invocation{invocation},
		},
	}, nil
}

func stringifyValue(fieldName string, raw any) (string, error) {
	if raw == nil {
		return "", nil
	}
	switch value := raw.(type) {
	case string:
		return value, nil
	default:
		payloadBytes, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("%s must be JSON serializable: %w", fieldName, err)
		}
		return string(payloadBytes), nil
	}
}
