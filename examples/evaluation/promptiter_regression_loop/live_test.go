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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type scriptedModel struct {
	failures  int
	calls     int
	err       error
	omitUsage bool
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
	response := &model.Response{
		Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}},
		Done:    true,
	}
	if !m.omitUsage {
		response.Usage = &model.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}
	}
	responses <- response
	close(responses)
	return responses, nil
}

func (m *scriptedModel) Info() model.Info { return model.Info{Name: "scripted"} }

type signalingFailureModel struct {
	calls     atomic.Int32
	firstCall chan struct{}
}

func (m *signalingFailureModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	if m.calls.Add(1) == 1 {
		close(m.firstCall)
	}
	return nil, errors.New("temporary model failure")
}

func (m *signalingFailureModel) Info() model.Info {
	return model.Info{Name: "signaling-failure"}
}

func TestLiveGeneratorCountsRetriesAndUsage(t *testing.T) {
	client := &scriptedModel{failures: 1}
	gate := gateFileConfig{MaxCalls: 3, MaxTokens: 2000, MaxCostCNY: 1}
	generator := &liveGenerator{
		model: client,
		cfg: liveConfig{
			TimeoutSeconds: 1, MaxRetries: 1,
			InputCNYPerMillion: 1, OutputCNYPerMillion: 2,
		},
		budget: newLiveBudget(gate, optimizerBudgetConfig{}),
	}
	result, err := generator.Generate(context.Background(), "prompt", "input")
	require.NoError(t, err)
	assert.Equal(t, 2, result.Usage.Calls)
	estimate := estimateTextRequest("prompt", "input", 512, 1, 2)
	assert.Equal(t, estimate.InputTokens+10, result.Usage.InputTokens)
	assert.Equal(t, estimate.OutputTokens+2, result.Usage.OutputTokens)
	assert.Equal(t, 2, client.calls)
}

func TestRetryCancellationDoesNotCountUnsentCalls(t *testing.T) {
	t.Run("evaluation", func(t *testing.T) {
		client := &signalingFailureModel{firstCall: make(chan struct{})}
		budget := newLiveBudget(
			gateFileConfig{MaxCalls: 10, MaxTokens: 10000, MaxCostCNY: 10},
			optimizerBudgetConfig{},
		)
		generator := &liveGenerator{
			model: client,
			cfg: liveConfig{
				TimeoutSeconds: 1, MaxRetries: 2,
				InputCNYPerMillion: 1, OutputCNYPerMillion: 2,
			},
			budget: budget,
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := generator.Generate(ctx, "prompt", "input")
			done <- err
		}()

		<-client.firstCall
		time.Sleep(25 * time.Millisecond)
		cancel()
		err := <-done

		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), client.calls.Load())
		assert.Equal(t, 1, budget.snapshot(budgetStageEvaluation).Calls)
		assert.Equal(t, 1, budget.snapshot("").Calls)
	})

	t.Run("optimizer", func(t *testing.T) {
		client := &signalingFailureModel{firstCall: make(chan struct{})}
		budget := newLiveBudget(
			gateFileConfig{MaxCalls: 10, MaxTokens: 10000, MaxCostCNY: 10},
			optimizerBudgetConfig{MaxCalls: 3, MaxTokens: 10000, MaxCostCNY: 1},
		)
		retrying := &budgetedRetryModel{
			model: client, timeoutSeconds: 1, maxRetries: 2,
			inputCNYPerMillion: 1, outputCNYPerMillion: 2,
			budget: budget,
		}
		maxTokens := 32
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := retrying.GenerateContent(ctx, &model.Request{
				Messages: []model.Message{
					model.NewUserMessage("optimize"),
				},
				GenerationConfig: model.GenerationConfig{MaxTokens: &maxTokens},
			})
			done <- err
		}()

		<-client.firstCall
		time.Sleep(25 * time.Millisecond)
		cancel()
		err := <-done

		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), client.calls.Load())
		assert.Equal(t, 1, budget.snapshot(budgetStageOptimizer).Calls)
		assert.Equal(t, 1, budget.snapshot("").Calls)
	})
}

func TestLiveGeneratorStopsAtCallBudget(t *testing.T) {
	client := &scriptedModel{}
	generator := &liveGenerator{
		model:  client,
		cfg:    liveConfig{TimeoutSeconds: 1},
		budget: newLiveBudget(gateFileConfig{MaxCalls: 1}, optimizerBudgetConfig{}),
	}
	_, err := generator.Generate(context.Background(), "prompt", "input")
	require.NoError(t, err)
	_, err = generator.Generate(context.Background(), "prompt", "input")
	assert.ErrorContains(t, err, "call budget exhausted")
}

func TestLiveGeneratorFailsClosedWhenUsageIsMissing(t *testing.T) {
	client := &scriptedModel{omitUsage: true}
	budget := newLiveBudget(
		gateFileConfig{MaxCalls: 10, MaxTokens: 1000, MaxCostCNY: 1},
		optimizerBudgetConfig{},
	)
	generator := &liveGenerator{
		model: client,
		cfg: liveConfig{
			TimeoutSeconds: 1, MaxRetries: 2,
			InputCNYPerMillion: 1, OutputCNYPerMillion: 2,
		},
		budget: budget,
	}

	_, err := generator.Generate(context.Background(), "prompt", "input")

	assert.ErrorIs(t, err, errMissingModelUsage)
	assert.Equal(t, 1, client.calls, "missing usage must fail without retries")
	assert.Equal(t, 1, budget.snapshot("").Calls)
}

func TestLiveGeneratorDoesNotRetryAuthenticationFailure(t *testing.T) {
	client := &scriptedModel{err: errors.New("401 Unauthorized: authentication error")}
	generator := &liveGenerator{
		model:  client,
		cfg:    liveConfig{TimeoutSeconds: 1, MaxRetries: 3},
		budget: newLiveBudget(gateFileConfig{MaxCalls: 10}, optimizerBudgetConfig{}),
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
		budget: newLiveBudget(
			gateFileConfig{MaxCalls: 10, MaxTokens: 10, MaxCostCNY: 20},
			optimizerBudgetConfig{},
		),
	}
	_, err := generator.Generate(context.Background(), "prompt", "input")
	assert.ErrorContains(t, err, "cannot reserve")
	assert.Zero(t, client.calls)
}

func TestLiveGeneratorConservativePreflightRejectsNonASCII(t *testing.T) {
	client := &scriptedModel{}
	generator := &liveGenerator{
		model: client,
		cfg: liveConfig{
			TimeoutSeconds:      1,
			InputCNYPerMillion:  1,
			OutputCNYPerMillion: 2,
		},
		budget: newLiveBudget(
			gateFileConfig{MaxCalls: 10, MaxTokens: 600, MaxCostCNY: 20},
			optimizerBudgetConfig{},
		),
	}

	_, err := generator.Generate(
		context.Background(),
		"prompt",
		strings.Repeat("界", 4),
	)

	assert.ErrorContains(t, err, "cannot reserve")
	assert.Zero(t, client.calls)
}

func TestLiveGeneratorOwnsEveryHTTPRetry(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		maxRetries int
		wantCalls  int32
	}{
		{name: "zero retries", status: http.StatusInternalServerError, maxRetries: 0, wantCalls: 1},
		{name: "retry 500", status: http.StatusInternalServerError, maxRetries: 2, wantCalls: 3},
		{name: "do not retry 404", status: http.StatusNotFound, maxRetries: 2, wantCalls: 1},
		{name: "do not retry 422", status: http.StatusUnprocessableEntity, maxRetries: 2, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
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

func TestPerAttemptDeadlineRetriesAndAccountsEveryRequest(t *testing.T) {
	newDelayedServer := func(t *testing.T) (*httptest.Server, *atomic.Int32) {
		t.Helper()
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter,
			_ *http.Request,
		) {
			calls.Add(1)
			time.Sleep(1500 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"error":{"message":"delayed response"}}`))
		}))
		return server, &calls
	}

	t.Run("evaluation", func(t *testing.T) {
		server, calls := newDelayedServer(t)
		defer server.Close()
		budget := newLiveBudget(
			gateFileConfig{MaxCalls: 2, MaxTokens: 10_000, MaxCostCNY: 1},
			optimizerBudgetConfig{},
		)
		generator, err := newLiveGeneratorWithBudget(liveConfig{
			Model: "evaluation-model", BaseURL: server.URL,
			APIKeyEnv: "EVALUATION_TEST_API_KEY", TimeoutSeconds: 1,
			MaxRetries: 1, InputCNYPerMillion: 1, OutputCNYPerMillion: 2,
		}, budget, "evaluation-test-key")
		require.NoError(t, err)

		_, err = generator.Generate(context.Background(), "prompt", "input")

		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, int32(2), calls.Load())
		usage := budget.snapshot(budgetStageEvaluation)
		assert.Equal(t, 2, usage.Calls)
		estimate := estimateTextRequest("prompt", "input", 512, 1, 2)
		assert.Equal(t, 2*estimate.InputTokens, usage.InputTokens)
		assert.Equal(t, 2*estimate.OutputTokens, usage.OutputTokens)
	})

	t.Run("optimizer", func(t *testing.T) {
		server, calls := newDelayedServer(t)
		defer server.Close()
		client, err := newOpenAICompatibleModel(
			"optimizer-model",
			server.URL,
			"OPTIMIZER_TEST_API_KEY",
			"optimizer-test-key",
		)
		require.NoError(t, err)
		budget := newLiveBudget(
			gateFileConfig{MaxCalls: 2, MaxTokens: 10_000, MaxCostCNY: 1},
			optimizerBudgetConfig{MaxCalls: 2, MaxTokens: 10_000, MaxCostCNY: 1},
		)
		retrying := &budgetedRetryModel{
			model: client, timeoutSeconds: 1, maxRetries: 1,
			inputCNYPerMillion: 1, outputCNYPerMillion: 2,
			budget: budget,
		}
		maxTokens := 32
		request := &model.Request{
			Messages:         []model.Message{model.NewUserMessage("optimize")},
			GenerationConfig: model.GenerationConfig{MaxTokens: &maxTokens},
		}

		_, err = retrying.GenerateContent(context.Background(), request)

		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, int32(2), calls.Load())
		usage := budget.snapshot(budgetStageOptimizer)
		assert.Equal(t, 2, usage.Calls)
		estimate := estimateModelRequest(request, 1, 2)
		assert.Equal(t, 2*estimate.InputTokens, usage.InputTokens)
		assert.Equal(t, 2*estimate.OutputTokens, usage.OutputTokens)
	})
}

func TestFailedEvaluationAttemptRetainsEstimatedBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"temporary failure","type":"server_error"}}`))
	}))
	defer server.Close()

	estimate := estimateTextRequest("prompt", "input", 512, 1, 2)
	budget := newLiveBudget(
		gateFileConfig{
			MaxCalls: 3, MaxTokens: estimate.tokens(), MaxCostCNY: estimate.CostCNY,
		},
		optimizerBudgetConfig{},
	)
	generator, err := newLiveGeneratorWithBudget(liveConfig{
		Model: "test-model", BaseURL: server.URL, APIKeyEnv: "TEST_API_KEY",
		TimeoutSeconds: 2, MaxRetries: 2,
		InputCNYPerMillion: 1, OutputCNYPerMillion: 2,
	}, budget, "test-key")
	require.NoError(t, err)

	_, err = generator.Generate(context.Background(), "prompt", "input")

	assert.ErrorContains(t, err, "cannot reserve")
	assert.Equal(t, int32(1), calls.Load())
	usage := budget.snapshot(budgetStageEvaluation)
	assert.Equal(t, 1, usage.Calls)
	assert.Equal(t, estimate.InputTokens, usage.InputTokens)
	assert.Equal(t, estimate.OutputTokens, usage.OutputTokens)
	assert.InDelta(t, estimate.CostCNY, usage.CostCNY, 1e-12)
}

func TestFailedOptimizerAttemptRetainsEstimatedBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"temporary failure","type":"server_error"}}`))
	}))
	defer server.Close()

	client, err := newOpenAICompatibleModel(
		"optimizer-model",
		server.URL,
		"OPTIMIZER_TEST_API_KEY",
		"optimizer-test-key",
	)
	require.NoError(t, err)
	maxTokens := 32
	request := &model.Request{
		Messages:         []model.Message{model.NewUserMessage("optimize")},
		GenerationConfig: model.GenerationConfig{MaxTokens: &maxTokens},
	}
	estimate := estimateModelRequest(request, 1, 2)
	budget := newLiveBudget(
		gateFileConfig{
			MaxCalls: 3, MaxTokens: estimate.tokens(), MaxCostCNY: estimate.CostCNY,
		},
		optimizerBudgetConfig{
			MaxCalls: 3, MaxTokens: estimate.tokens(), MaxCostCNY: estimate.CostCNY,
		},
	)
	retrying := &budgetedRetryModel{
		model: client, timeoutSeconds: 2, maxRetries: 2,
		inputCNYPerMillion: 1, outputCNYPerMillion: 2,
		budget: budget,
	}

	_, err = retrying.GenerateContent(context.Background(), request)

	assert.ErrorContains(t, err, "cannot reserve")
	assert.Equal(t, int32(1), calls.Load())
	usage := budget.snapshot(budgetStageOptimizer)
	assert.Equal(t, 1, usage.Calls)
	assert.Equal(t, estimate.InputTokens, usage.InputTokens)
	assert.Equal(t, estimate.OutputTokens, usage.OutputTokens)
	assert.InDelta(t, estimate.CostCNY, usage.CostCNY, 1e-12)
}

func TestBudgetedOptimizerModelCountsRetriesInSharedBudget(t *testing.T) {
	client := &scriptedModel{failures: 1}
	budget := newLiveBudget(
		gateFileConfig{MaxCalls: 10, MaxTokens: 10000, MaxCostCNY: 10},
		optimizerBudgetConfig{MaxCalls: 3, MaxTokens: 10000, MaxCostCNY: 1},
	)
	retrying := &budgetedRetryModel{
		model:               client,
		timeoutSeconds:      1,
		maxRetries:          1,
		inputCNYPerMillion:  1,
		outputCNYPerMillion: 2,
		budget:              budget,
	}
	maxTokens := 32
	request := &model.Request{
		Messages:         []model.Message{model.NewUserMessage("optimize")},
		GenerationConfig: model.GenerationConfig{MaxTokens: &maxTokens},
	}
	responses, err := retrying.GenerateContent(context.Background(), request)
	require.NoError(t, err)
	for range responses {
	}

	optimizerUsage := budget.snapshot(budgetStageOptimizer)
	assert.Equal(t, 2, client.calls)
	assert.Equal(t, 2, optimizerUsage.Calls)
	estimate := estimateModelRequest(request, 1, 2)
	assert.Equal(t, estimate.InputTokens+10, optimizerUsage.InputTokens)
	assert.Equal(t, estimate.OutputTokens+2, optimizerUsage.OutputTokens)
	assert.Equal(t, optimizerUsage, budget.snapshot(""))
}

func TestBudgetedOptimizerFailsClosedWhenUsageIsMissing(t *testing.T) {
	client := &scriptedModel{omitUsage: true}
	budget := newLiveBudget(
		gateFileConfig{MaxCalls: 10, MaxTokens: 10000, MaxCostCNY: 10},
		optimizerBudgetConfig{MaxCalls: 3, MaxTokens: 10000, MaxCostCNY: 1},
	)
	retrying := &budgetedRetryModel{
		model: client, timeoutSeconds: 1, maxRetries: 2,
		inputCNYPerMillion: 1, outputCNYPerMillion: 2,
		budget: budget,
	}
	maxTokens := 32

	_, err := retrying.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("optimize")},
		GenerationConfig: model.GenerationConfig{MaxTokens: &maxTokens},
	})

	assert.ErrorIs(t, err, errMissingModelUsage)
	assert.Equal(t, 1, client.calls, "missing usage must fail without retries")
	assert.Equal(t, 1, budget.snapshot(budgetStageOptimizer).Calls)
}

func TestEvaluationAndOptimizerUseIndependentPrices(t *testing.T) {
	budget := newLiveBudget(
		gateFileConfig{MaxCalls: 10, MaxTokens: 10000, MaxCostCNY: 10},
		optimizerBudgetConfig{MaxCalls: 3, MaxTokens: 10000, MaxCostCNY: 1},
	)
	evaluation := &liveGenerator{
		model: &scriptedModel{},
		cfg: liveConfig{
			TimeoutSeconds:      1,
			InputCNYPerMillion:  1,
			OutputCNYPerMillion: 2,
		},
		budget: budget,
	}
	_, err := evaluation.Generate(context.Background(), "prompt", "input")
	require.NoError(t, err)

	optimizer := &budgetedRetryModel{
		model:               &scriptedModel{},
		timeoutSeconds:      1,
		inputCNYPerMillion:  10,
		outputCNYPerMillion: 20,
		budget:              budget,
	}
	maxTokens := 32
	responses, err := optimizer.GenerateContent(
		context.Background(),
		&model.Request{
			Messages: []model.Message{model.NewUserMessage("optimize")},
			GenerationConfig: model.GenerationConfig{
				MaxTokens: &maxTokens,
			},
		},
	)
	require.NoError(t, err)
	for range responses {
	}

	evaluationUsage := budget.snapshot(budgetStageEvaluation)
	optimizerUsage := budget.snapshot(budgetStageOptimizer)
	totalUsage := budget.snapshot("")
	assert.InDelta(t, 0.000014, evaluationUsage.CostCNY, 1e-12)
	assert.InDelta(t, 0.00014, optimizerUsage.CostCNY, 1e-12)
	assert.InDelta(t, 0.000154, totalUsage.CostCNY, 1e-12)
}
