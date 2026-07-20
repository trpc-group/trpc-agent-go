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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type scriptedModel struct {
	failures int
	calls    int
	err      error
}

func (m *scriptedModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	if m.calls <= m.failures {
		return nil, errors.New("temporary model failure")
	}
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{
		Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}},
		Usage:   &model.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
		Done:    true,
	}
	close(responses)
	return responses, nil
}

func (m *scriptedModel) Info() model.Info { return model.Info{Name: "scripted"} }

func TestLiveGeneratorCountsRetriesAndUsage(t *testing.T) {
	client := &scriptedModel{failures: 1}
	generator := &liveGenerator{
		model: client,
		cfg: liveConfig{
			TimeoutSeconds: 1, MaxRetries: 1,
			InputCNYPerMillion: 1, OutputCNYPerMillion: 2,
		},
		gate: gateFileConfig{MaxCalls: 3, MaxTokens: 1000, MaxCostCNY: 1},
	}
	result, err := generator.Generate(context.Background(), "prompt", "input")
	require.NoError(t, err)
	assert.Equal(t, 2, result.Usage.Calls)
	assert.Equal(t, 10, result.Usage.InputTokens)
	assert.Equal(t, 2, result.Usage.OutputTokens)
	assert.Equal(t, 2, client.calls)
}

func TestLiveGeneratorStopsAtCallBudget(t *testing.T) {
	client := &scriptedModel{}
	generator := &liveGenerator{
		model: client,
		cfg:   liveConfig{TimeoutSeconds: 1},
		gate:  gateFileConfig{MaxCalls: 1},
	}
	_, err := generator.Generate(context.Background(), "prompt", "input")
	require.NoError(t, err)
	_, err = generator.Generate(context.Background(), "prompt", "input")
	assert.ErrorContains(t, err, "call budget exhausted")
}

func TestLiveGeneratorDoesNotRetryAuthenticationFailure(t *testing.T) {
	client := &scriptedModel{err: errors.New("401 Unauthorized: authentication error")}
	generator := &liveGenerator{
		model: client,
		cfg:   liveConfig{TimeoutSeconds: 1, MaxRetries: 3},
		gate:  gateFileConfig{MaxCalls: 10},
	}
	_, err := generator.Generate(context.Background(), "prompt", "input")
	assert.ErrorContains(t, err, "non-retryable model error")
	assert.Equal(t, 1, client.calls)
}

func TestLiveGeneratorReservesBudgetBeforeCalling(t *testing.T) {
	client := &scriptedModel{}
	generator := &liveGenerator{
		model: client,
		cfg: liveConfig{
			TimeoutSeconds:      1,
			InputCNYPerMillion:  1,
			OutputCNYPerMillion: 2,
		},
		gate: gateFileConfig{MaxCalls: 10, MaxTokens: 10, MaxCostCNY: 20},
	}
	_, err := generator.Generate(context.Background(), "prompt", "input")
	assert.ErrorContains(t, err, "cannot reserve")
	assert.Zero(t, client.calls)
}

func TestLiveGeneratorOwnsEveryHTTPRetry(t *testing.T) {
	tests := []struct {
		name       string
		maxRetries int
		wantCalls  int32
	}{
		{name: "zero retries", maxRetries: 0, wantCalls: 1},
		{name: "two retries", maxRetries: 2, wantCalls: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"message":"temporary failure","type":"server_error"}}`))
			}))
			defer server.Close()

			generator, err := newLiveGenerator(liveConfig{
				Model:               "test-model",
				BaseURL:             server.URL,
				APIKeyEnv:           "TEST_API_KEY",
				TimeoutSeconds:      2,
				MaxRetries:          test.maxRetries,
				InputCNYPerMillion:  1,
				OutputCNYPerMillion: 2,
			}, gateFileConfig{MaxCalls: 3, MaxTokens: 10_000, MaxCostCNY: 1}, "test-key")
			require.NoError(t, err)

			_, err = generator.Generate(context.Background(), "prompt", "input")
			require.Error(t, err)
			assert.Equal(t, test.wantCalls, calls.Load())
		})
	}
}
