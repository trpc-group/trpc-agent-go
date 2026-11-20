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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type capturingModel struct {
	mu       sync.Mutex
	content  string
	requests []*model.Request
}

func (m *capturingModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, request)
	m.mu.Unlock()
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(m.content),
		}},
		Usage: &model.Usage{
			PromptTokens:     120,
			CompletionTokens: 80,
			TotalTokens:      200,
		},
		Done: true,
	}
	close(responses)
	return responses, nil
}

func (m *capturingModel) Info() model.Info {
	return model.Info{Name: "capturing"}
}

func (m *capturingModel) requestJSON(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	require.NotEmpty(t, m.requests)
	data, err := json.Marshal(m.requests)
	require.NoError(t, err)
	return string(data)
}

func TestLivePromptOptimizerUsesOfficialOptimizerWithoutValidationLeakage(t *testing.T) {
	cfg, err := loadConfig("data/config.json")
	require.NoError(t, err)
	const validationSentinel = "VALIDATION_SENTINEL_MUST_NOT_REACH_OPTIMIZER"
	cfg.Validation.EvalCases[0].Conversation[0].UserContent.Content += validationSentinel
	cfg.Validation.EvalCases[0].Conversation[0].FinalResponse.Content += validationSentinel
	cfg.Live.Optimizer.InputCNYPerMillion = 10
	cfg.Live.Optimizer.OutputCNYPerMillion = 20

	candidate := cfg.Prompt + `
1. ROUTE_EXPLICITLY: select the route that matches the user's intent.
2. VALIDATE_TOOL_ARGUMENTS: verify required arguments and types before every tool call.
3. OUTPUT_JSON_WHEN_REQUESTED: emit valid JSON with no surrounding prose when JSON is requested.
4. GROUND_IN_PROVIDED_CONTEXT: never invent facts absent from supplied context.
5. PRESERVE_SAFETY_CONSTRAINTS: never reveal credentials or secrets.
6. REPORT_ENVIRONMENT_FAILURES: distinguish dependency failures from model errors.
7. NOVEL_LLM_POLICY: state uncertainty before offering a fallback.`
	response, err := json.Marshal(map[string]any{
		"Value":  map[string]any{"Text": candidate},
		"Reason": "combine the observed training gradients into one auditable instruction",
	})
	require.NoError(t, err)
	capturing := &capturingModel{content: string(response)}
	budget := newLiveBudget(cfg.Gate, cfg.Live.Optimizer.Budget)
	runtime, err := newLivePromptOptimizerWithModel(
		context.Background(),
		cfg,
		budget,
		capturing,
	)
	require.NoError(t, err)
	defer runtime.close()

	got, audit, err := runPromptIter(
		context.Background(),
		cfg,
		runtime.optimizer,
		candidateSourceLiveLLM,
		func() Usage {
			return budget.snapshot(budgetStageOptimizer).reportUsage()
		},
	)
	require.NoError(t, err)
	assert.Equal(t, candidate, got)
	assert.True(t, audit.Completed)
	assert.Equal(t, candidateSourceLiveLLM, audit.Source)
	assert.Contains(t, got, "NOVEL_LLM_POLICY")
	assert.NotContains(t, capturing.requestJSON(t), validationSentinel)
	assert.Equal(t, 1, audit.Usage.Calls)
	assert.Equal(t, 120, audit.Usage.InputTokens)
	assert.Equal(t, 80, audit.Usage.OutputTokens)
	assert.InDelta(t, 0.0028, audit.Usage.CostCNY, 1e-12)
	assert.InDelta(t, 0.0028, budget.snapshot("").CostCNY, 1e-12)
}

func TestIndependentEndpointsReceiveOnlyTheirOwnCredentials(t *testing.T) {
	cfg, err := loadConfig("data/config.json")
	require.NoError(t, err)

	var authMu sync.Mutex
	var evaluationAuth []string
	var optimizerAuth []string
	evaluationServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			authMu.Lock()
			evaluationAuth = append(evaluationAuth, r.Header.Get("Authorization"))
			authMu.Unlock()
			writeChatCompletion(t, w, "evaluation-model", "ok", 10, 2)
		},
	))
	defer evaluationServer.Close()

	candidate := cfg.Prompt + "\n8. USE_THE_INDEPENDENT_OPTIMIZER_CREDENTIAL."
	optimizerResponse, err := json.Marshal(map[string]any{
		"Value":  map[string]any{"Text": candidate},
		"Reason": "use the independent optimizer endpoint",
	})
	require.NoError(t, err)
	optimizerServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			authMu.Lock()
			optimizerAuth = append(optimizerAuth, r.Header.Get("Authorization"))
			authMu.Unlock()
			writeChatCompletion(
				t,
				w,
				"optimizer-model",
				string(optimizerResponse),
				12,
				4,
			)
		},
	))
	defer optimizerServer.Close()

	cfg.Live.Model = "evaluation-model"
	cfg.Live.BaseURL = evaluationServer.URL
	cfg.Live.APIKeyEnv = "EVALUATION_TEST_API_KEY"
	cfg.Live.MaxRetries = 0
	cfg.Live.Optimizer.Model = "optimizer-model"
	cfg.Live.Optimizer.BaseURL = optimizerServer.URL
	cfg.Live.Optimizer.APIKeyEnv = "OPTIMIZER_TEST_API_KEY"
	cfg.Live.Optimizer.InputCNYPerMillion = 5
	cfg.Live.Optimizer.OutputCNYPerMillion = 9
	cfg.Live.Optimizer.MaxRetries = 0
	require.NoError(t, validateLiveConfig(cfg.Live))
	t.Setenv(cfg.Live.APIKeyEnv, "evaluation-secret")
	t.Setenv(cfg.Live.Optimizer.APIKeyEnv, "optimizer-secret")

	budget := newLiveBudget(cfg.Gate, cfg.Live.Optimizer.Budget)
	evaluationGenerator, err := newLiveGeneratorWithBudget(
		cfg.Live,
		budget,
		os.Getenv(cfg.Live.APIKeyEnv),
	)
	require.NoError(t, err)
	_, err = evaluationGenerator.Generate(
		context.Background(),
		cfg.Prompt,
		"evaluate this case",
	)
	require.NoError(t, err)

	runtime, err := newLivePromptOptimizer(
		context.Background(),
		cfg,
		budget,
		os.Getenv(cfg.Live.Optimizer.APIKeyEnv),
	)
	require.NoError(t, err)
	defer runtime.close()
	got, _, err := runPromptIter(
		context.Background(),
		cfg,
		runtime.optimizer,
		candidateSourceLiveLLM,
		func() Usage {
			return budget.snapshot(budgetStageOptimizer).reportUsage()
		},
	)
	require.NoError(t, err)
	assert.Equal(t, candidate, got)

	authMu.Lock()
	defer authMu.Unlock()
	require.NotEmpty(t, evaluationAuth)
	require.NotEmpty(t, optimizerAuth)
	for _, authorization := range evaluationAuth {
		assert.Equal(t, "Bearer evaluation-secret", authorization)
		assert.NotContains(t, authorization, "optimizer-secret")
	}
	for _, authorization := range optimizerAuth {
		assert.Equal(t, "Bearer optimizer-secret", authorization)
		assert.NotContains(t, authorization, "evaluation-secret")
	}
}

func writeChatCompletion(
	t *testing.T,
	w http.ResponseWriter,
	modelName string,
	content string,
	promptTokens int,
	completionTokens int,
) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"id":      "test-response",
		"object":  "chat.completion",
		"created": 1,
		"model":   modelName,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]string{
				"role":    "assistant",
				"content": content,
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      promptTokens + completionTokens,
		},
	}))
}

func TestOptimizerBudgetPreservesCandidateEvaluationCapacity(t *testing.T) {
	capturing := &capturingModel{content: `{}`}
	budget := newLiveBudget(
		gateFileConfig{MaxCalls: 5, MaxTokens: 10000, MaxCostCNY: 10},
		optimizerBudgetConfig{MaxCalls: 3, MaxTokens: 16384, MaxCostCNY: 1},
	)
	budget.setEvaluationReserve(resourceReservation{Calls: 5})
	retrying := &budgetedRetryModel{
		model:               capturing,
		timeoutSeconds:      1,
		inputCNYPerMillion:  1,
		outputCNYPerMillion: 2,
		budget:              budget,
	}

	_, err := retrying.GenerateContent(context.Background(), &model.Request{})
	assert.ErrorContains(t, err, "cannot preserve 5 evaluation calls")
	assert.Empty(t, capturing.requests)
	assert.Zero(t, budget.snapshot(budgetStageOptimizer).Calls)
}
