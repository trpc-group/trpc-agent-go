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
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestCountedModelHardLimitStopsBeforeProviderCall(t *testing.T) {
	zero := 0
	ledger := newLedger()
	ledger.setModelCallLimit(&zero)
	base := &countingModel{}
	counted := newCountedModel("candidate", "baseline", base, ledger, pricing{})

	responses, err := counted.GenerateContent(context.Background(), &model.Request{})

	assert.Nil(t, responses)
	require.ErrorContains(t, err, "model call budget")
	assert.Zero(t, base.calls.Load())
	assert.Zero(t, ledger.snapshot().ModelCalls.Value)
}

func TestCountedModelHardLimitIsAtomicAcrossConcurrentCalls(t *testing.T) {
	one := 1
	ledger := newLedger()
	ledger.setModelCallLimit(&one)
	base := &countingModel{}
	counted := newCountedModel("candidate", "validation", base, ledger, pricing{})

	var successes atomic.Int32
	var failures atomic.Int32
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			responses, err := counted.GenerateContent(context.Background(), &model.Request{})
			if err != nil {
				failures.Add(1)
				return
			}
			for range responses {
			}
			successes.Add(1)
		}()
	}
	group.Wait()

	assert.Equal(t, int32(1), successes.Load())
	assert.Equal(t, int32(1), failures.Load())
	assert.Equal(t, int32(1), base.calls.Load())
	assert.Equal(t, 1, ledger.snapshot().ModelCalls.Value)
}

func TestCountedModelCancellationClosesOutputAndRecordsReceivedUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	responses := make(chan *model.Response)
	base := &channelModel{responses: responses}
	ledger := newLedger()
	counted := newCountedModel("candidate", "baseline", base, ledger, pricing{})

	output, err := counted.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	responses <- &model.Response{
		Usage: &model.Usage{PromptTokens: 7, CompletionTokens: 3},
	}
	require.NotNil(t, <-output)
	cancel()

	select {
	case _, ok := <-output:
		assert.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("counted model did not stop forwarding after cancellation")
	}
	usage := ledger.snapshot()
	assert.True(t, usage.Tokens.Known)
	assert.Equal(t, 10, usage.Tokens.Value)
	require.Len(t, ledger.errors(), 1)
	assert.Equal(t, context.Canceled.Error(), ledger.errors()[0].Message)
}

func TestCountedModelCancellationUnblocksBlockedForward(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{}
	ledger := newLedger()
	counted := newCountedModel(
		"candidate", "baseline", &channelModel{responses: responses}, ledger, pricing{},
	)

	output, err := counted.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	cancel()

	select {
	case _, ok := <-output:
		if ok {
			select {
			case _, ok = <-output:
				assert.False(t, ok)
			case <-time.After(time.Second):
				t.Fatal("counted model remained blocked after cancellation")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("counted model remained blocked after cancellation")
	}
	assert.True(t, ledger.snapshot().LatencyMillis.Known)
}

type testModel struct {
	responses []*model.Response
	err       error
}

type channelModel struct {
	responses <-chan *model.Response
}

type countingModel struct {
	calls atomic.Int32
}

func (m *countingModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	m.calls.Add(1)
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{Done: true, Usage: &model.Usage{}}
	close(responses)
	return responses, nil
}

func (*countingModel) Info() model.Info {
	return model.Info{Name: "counting"}
}

func (m *channelModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	return m.responses, nil
}

func (*channelModel) Info() model.Info {
	return model.Info{Name: "channel"}
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
