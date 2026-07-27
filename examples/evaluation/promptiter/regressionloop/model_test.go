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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestCountedModelMarksMissingUsageUnknown(t *testing.T) {
	base := &testModel{responses: []*model.Response{{Done: true}}}
	ledger := newLedger()
	counted := newCountedModel("candidate", "baseline", base, ledger, pricing{})
	drainModel(t, counted)

	assert.False(t, ledger.snapshot().Tokens.Known)
}

func TestCountedModelRecordsUsageCostAndResponseError(t *testing.T) {
	inputPrice := 2.0
	outputPrice := 4.0
	base := &testModel{responses: []*model.Response{{
		Done:  true,
		Usage: &model.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		Error: &model.ResponseError{Message: "rate limited"},
	}}}
	ledger := newLedger()
	counted := newCountedModel("judge", "validation", base, ledger, pricing{
		InputPerM:  &inputPrice,
		OutputPerM: &outputPrice,
	})
	drainModel(t, counted)

	usage := ledger.snapshot()
	assert.Equal(t, 1, usage.ModelCalls.Value)
	assert.Equal(t, 15, usage.Tokens.Value)
	assert.InDelta(t, 0.00004, usage.EstimatedCost.Value, 1e-12)
	assert.Len(t, ledger.errors(), 1)
}

func TestCountedModelRecordsFunctionErrorAsCall(t *testing.T) {
	ledger := newLedger()
	counted := newCountedModel("worker", "round-1", &testModel{err: errors.New("offline")}, ledger, pricing{})

	_, err := counted.GenerateContent(context.Background(), &model.Request{})
	require.ErrorContains(t, err, "offline")
	assert.Equal(t, 1, ledger.snapshot().ModelCalls.Value)
	assert.False(t, ledger.snapshot().Tokens.Known)
}

type testModel struct {
	responses []*model.Response
	err       error
}

func (m *testModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	responses := make(chan *model.Response, len(m.responses))
	for _, response := range m.responses {
		responses <- response
	}
	close(responses)
	return responses, nil
}

func (*testModel) Info() model.Info {
	return model.Info{Name: "test"}
}

func drainModel(t *testing.T, counted model.Model) {
	t.Helper()
	responses, err := counted.GenerateContent(context.Background(), &model.Request{})
	require.NoError(t, err)
	for range responses {
	}
}
