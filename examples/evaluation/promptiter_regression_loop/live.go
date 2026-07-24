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
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	openaiopt "github.com/openai/openai-go/option"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

type generationUsage struct {
	Calls        int
	InputTokens  int
	OutputTokens int
	CostCNY      float64
}

func (u generationUsage) add(other generationUsage) generationUsage {
	return generationUsage{
		Calls:        u.Calls + other.Calls,
		InputTokens:  u.InputTokens + other.InputTokens,
		OutputTokens: u.OutputTokens + other.OutputTokens,
		CostCNY:      u.CostCNY + other.CostCNY,
	}
}

func (u generationUsage) subtract(other generationUsage) generationUsage {
	return generationUsage{
		Calls:        u.Calls - other.Calls,
		InputTokens:  u.InputTokens - other.InputTokens,
		OutputTokens: u.OutputTokens - other.OutputTokens,
		CostCNY:      u.CostCNY - other.CostCNY,
	}
}

func (u generationUsage) tokens() int {
	return u.InputTokens + u.OutputTokens
}

func (u generationUsage) reportUsage() Usage {
	return Usage{
		Calls:        u.Calls,
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CostCNY:      u.CostCNY,
	}
}

type generationResult struct {
	Text  string
	Usage generationUsage
}

type textGenerator interface {
	Generate(ctx context.Context, prompt, input string) (generationResult, error)
}

type budgetStage string

const (
	budgetStageEvaluation budgetStage = "evaluation"
	budgetStageOptimizer  budgetStage = "optimizer"
)

type resourceReservation struct {
	Calls   int
	Tokens  int
	CostCNY float64
}

type liveBudget struct {
	gate      gateFileConfig
	optimizer optimizerBudgetConfig

	mu                sync.Mutex
	total             generationUsage
	byStage           map[budgetStage]generationUsage
	evaluationReserve resourceReservation
}

func newLiveBudget(gate gateFileConfig, optimizer optimizerBudgetConfig) *liveBudget {
	return &liveBudget{
		gate:      gate,
		optimizer: optimizer,
		byStage:   make(map[budgetStage]generationUsage),
	}
}

func (b *liveBudget) setEvaluationReserve(reservation resourceReservation) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.evaluationReserve = reservation
}

func (b *liveBudget) clearEvaluationReserve() {
	b.setEvaluationReserve(resourceReservation{})
}

func (b *liveBudget) snapshot(stage budgetStage) generationUsage {
	b.mu.Lock()
	defer b.mu.Unlock()
	if stage == "" {
		return b.total
	}
	return b.byStage[stage]
}

func (b *liveBudget) reserveCall(
	stage budgetStage,
	estimatedTokens int,
	estimatedCost float64,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	stageUsage := b.byStage[stage]
	if err := checkBudget(
		"live",
		b.total,
		generationUsage{Calls: 1, InputTokens: estimatedTokens, CostCNY: estimatedCost},
		b.gate.MaxCalls,
		b.gate.MaxTokens,
		b.gate.MaxCostCNY,
	); err != nil {
		return err
	}
	if stage == budgetStageOptimizer {
		if err := checkBudget(
			"live optimizer",
			stageUsage,
			generationUsage{Calls: 1, InputTokens: estimatedTokens, CostCNY: estimatedCost},
			b.optimizer.MaxCalls,
			b.optimizer.MaxTokens,
			b.optimizer.MaxCostCNY,
		); err != nil {
			return err
		}
		if err := b.checkEvaluationReserve(estimatedTokens, estimatedCost); err != nil {
			return err
		}
	}
	b.total.Calls++
	stageUsage.Calls++
	b.byStage[stage] = stageUsage
	return nil
}

func (b *liveBudget) checkEvaluationReserve(estimatedTokens int, estimatedCost float64) error {
	reserve := b.evaluationReserve
	if b.gate.MaxCalls > 0 &&
		b.total.Calls+1+reserve.Calls > b.gate.MaxCalls {
		return fmt.Errorf(
			"live optimizer cannot preserve %d evaluation calls within global limit %d",
			reserve.Calls,
			b.gate.MaxCalls,
		)
	}
	if b.gate.MaxTokens > 0 &&
		b.total.tokens()+estimatedTokens+reserve.Tokens > b.gate.MaxTokens {
		return fmt.Errorf(
			"live optimizer cannot preserve %d evaluation tokens within global limit %d",
			reserve.Tokens,
			b.gate.MaxTokens,
		)
	}
	if b.gate.MaxCostCNY > 0 &&
		b.total.CostCNY+estimatedCost+reserve.CostCNY > b.gate.MaxCostCNY {
		return fmt.Errorf(
			"live optimizer cannot preserve %.4f CNY evaluation budget within global limit %.4f",
			reserve.CostCNY,
			b.gate.MaxCostCNY,
		)
	}
	return nil
}

func (b *liveBudget) recordUsage(stage budgetStage, usage generationUsage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total.InputTokens += usage.InputTokens
	b.total.OutputTokens += usage.OutputTokens
	b.total.CostCNY += usage.CostCNY
	stageUsage := b.byStage[stage]
	stageUsage.InputTokens += usage.InputTokens
	stageUsage.OutputTokens += usage.OutputTokens
	stageUsage.CostCNY += usage.CostCNY
	b.byStage[stage] = stageUsage
	if err := checkBudget(
		"live",
		b.total,
		generationUsage{},
		b.gate.MaxCalls,
		b.gate.MaxTokens,
		b.gate.MaxCostCNY,
	); err != nil {
		return err
	}
	if stage == budgetStageOptimizer {
		return checkBudget(
			"live optimizer",
			stageUsage,
			generationUsage{},
			b.optimizer.MaxCalls,
			b.optimizer.MaxTokens,
			b.optimizer.MaxCostCNY,
		)
	}
	return nil
}

func checkBudget(
	name string,
	current generationUsage,
	addition generationUsage,
	maxCalls int,
	maxTokens int,
	maxCostCNY float64,
) error {
	next := current.add(addition)
	switch {
	case maxCalls > 0 && next.Calls > maxCalls:
		return fmt.Errorf("%s call budget exhausted: %d calls", name, maxCalls)
	case maxTokens > 0 && next.tokens() > maxTokens:
		return fmt.Errorf(
			"%s token budget cannot reserve %d tokens within limit %d",
			name,
			addition.tokens(),
			maxTokens,
		)
	case maxCostCNY > 0 && next.CostCNY > maxCostCNY:
		return fmt.Errorf(
			"%s cost budget cannot reserve %.4f CNY within limit %.4f",
			name,
			addition.CostCNY,
			maxCostCNY,
		)
	default:
		return nil
	}
}

type liveGenerator struct {
	model  model.Model
	cfg    liveConfig
	budget *liveBudget
}

func newLiveGenerator(cfg liveConfig, gate gateFileConfig, apiKey string) (*liveGenerator, error) {
	return newLiveGeneratorWithBudget(
		cfg,
		newLiveBudget(gate, cfg.Optimizer.Budget),
		apiKey,
	)
}

func newLiveGeneratorWithBudget(
	cfg liveConfig,
	budget *liveBudget,
	apiKey string,
) (*liveGenerator, error) {
	liveModel, err := newOpenAICompatibleModel(cfg.Model, cfg.BaseURL, cfg.APIKeyEnv, apiKey)
	if err != nil {
		return nil, err
	}
	if budget == nil {
		return nil, errors.New("live budget is nil")
	}
	return &liveGenerator{
		model:  liveModel,
		cfg:    cfg,
		budget: budget,
	}, nil
}

func newOpenAICompatibleModel(
	modelName string,
	baseURL string,
	apiKeyEnv string,
	apiKey string,
) (model.Model, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%s is empty", apiKeyEnv)
	}
	if strings.TrimSpace(modelName) == "" {
		return nil, errors.New("live model is empty")
	}
	options := []openai.Option{
		openai.WithAPIKey(apiKey),
		openai.WithVariant(openai.VariantDeepSeek),
		openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
	}
	if strings.TrimSpace(baseURL) != "" {
		options = append(options, openai.WithBaseURL(strings.TrimSpace(baseURL)))
	}
	return openai.New(modelName, options...), nil
}

func (g *liveGenerator) Generate(
	ctx context.Context,
	prompt string,
	input string,
) (generationResult, error) {
	var lastErr error
	var accumulated generationUsage
	estimatedTokens, estimatedCost := estimateTextRequest(
		prompt,
		input,
		512,
		g.cfg.InputCNYPerMillion,
		g.cfg.OutputCNYPerMillion,
	)
	for attempt := 0; attempt <= g.cfg.MaxRetries; attempt++ {
		if err := g.budget.reserveCall(
			budgetStageEvaluation,
			estimatedTokens,
			estimatedCost,
		); err != nil {
			accumulated.Calls = attempt
			return generationResult{Usage: accumulated}, err
		}
		accumulated.Calls++
		if err := waitForRetry(ctx, attempt); err != nil {
			return generationResult{Usage: accumulated}, err
		}
		result, err := g.generateOnce(ctx, prompt, input)
		accumulated.InputTokens += result.Usage.InputTokens
		accumulated.OutputTokens += result.Usage.OutputTokens
		accumulated.CostCNY += result.Usage.CostCNY
		if budgetErr := g.budget.recordUsage(budgetStageEvaluation, result.Usage); budgetErr != nil {
			return generationResult{Text: result.Text, Usage: accumulated}, budgetErr
		}
		if err == nil {
			result.Usage = accumulated
			return result, nil
		}
		lastErr = err
		if !isRetryableModelError(err) {
			return generationResult{Usage: accumulated}, fmt.Errorf(
				"non-retryable model error: %w",
				err,
			)
		}
	}
	return generationResult{Usage: accumulated}, fmt.Errorf(
		"model call failed after retries: %w",
		lastErr,
	)
}

func isRetryableModelError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"400 bad request", "401", "403", "unauthorized", "forbidden",
		"authentication", "invalid api key", "invalid_request_error",
	} {
		if strings.Contains(message, marker) {
			return false
		}
	}
	return true
}

func waitForRetry(ctx context.Context, attempt int) error {
	if attempt == 0 {
		return nil
	}
	delay := time.Duration(1<<(attempt-1)) * 250 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (g *liveGenerator) generateOnce(
	ctx context.Context,
	prompt string,
	input string,
) (generationResult, error) {
	timeout := time.Duration(g.cfg.TimeoutSeconds) * time.Second
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	maxTokens := 512
	temperature := 0.0
	thinking := false
	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(prompt),
			model.NewUserMessage(input),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:       &maxTokens,
			Temperature:     &temperature,
			Stream:          false,
			ThinkingEnabled: &thinking,
		},
	}
	responses, err := g.model.GenerateContent(callCtx, request)
	if err != nil {
		return generationResult{}, err
	}
	var content strings.Builder
	var usage generationUsage
	for response := range responses {
		if response == nil {
			continue
		}
		if response.Usage != nil {
			usage.InputTokens += response.Usage.PromptTokens
			usage.OutputTokens += response.Usage.CompletionTokens
		}
		if response.Error != nil {
			usage.CostCNY = usageCost(
				usage,
				g.cfg.InputCNYPerMillion,
				g.cfg.OutputCNYPerMillion,
			)
			return generationResult{Usage: usage}, response.Error
		}
		for _, choice := range response.Choices {
			if choice.Message.Content != "" {
				content.WriteString(choice.Message.Content)
			} else if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
			}
		}
	}
	usage.CostCNY = usageCost(
		usage,
		g.cfg.InputCNYPerMillion,
		g.cfg.OutputCNYPerMillion,
	)
	if strings.TrimSpace(content.String()) == "" {
		return generationResult{Usage: usage}, errors.New("model returned empty content")
	}
	return generationResult{Text: strings.TrimSpace(content.String()), Usage: usage}, nil
}

type budgetedRetryModel struct {
	model               model.Model
	timeoutSeconds      int
	maxRetries          int
	inputCNYPerMillion  float64
	outputCNYPerMillion float64
	budget              *liveBudget
}

func (m *budgetedRetryModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	estimatedTokens, estimatedCost := estimateModelRequest(
		request,
		m.inputCNYPerMillion,
		m.outputCNYPerMillion,
	)
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		if err := m.budget.reserveCall(
			budgetStageOptimizer,
			estimatedTokens,
			estimatedCost,
		); err != nil {
			return nil, err
		}
		if err := waitForRetry(ctx, attempt); err != nil {
			return nil, err
		}
		responses, usage, err := m.generateOnce(ctx, request)
		if budgetErr := m.budget.recordUsage(budgetStageOptimizer, usage); budgetErr != nil {
			return nil, budgetErr
		}
		if err == nil {
			return replayResponses(responses), nil
		}
		lastErr = err
		if !isRetryableModelError(err) {
			return nil, fmt.Errorf("non-retryable model error: %w", err)
		}
	}
	return nil, fmt.Errorf("model call failed after retries: %w", lastErr)
}

func (m *budgetedRetryModel) generateOnce(
	ctx context.Context,
	request *model.Request,
) ([]*model.Response, generationUsage, error) {
	callCtx, cancel := context.WithTimeout(
		ctx,
		time.Duration(m.timeoutSeconds)*time.Second,
	)
	defer cancel()
	responseChannel, err := m.model.GenerateContent(callCtx, request)
	if err != nil {
		return nil, generationUsage{}, err
	}
	var responses []*model.Response
	var usage generationUsage
	for response := range responseChannel {
		if response == nil {
			continue
		}
		responses = append(responses, response)
		if response.Usage != nil {
			usage.InputTokens += response.Usage.PromptTokens
			usage.OutputTokens += response.Usage.CompletionTokens
		}
		if response.Error != nil {
			usage.CostCNY = usageCost(
				usage,
				m.inputCNYPerMillion,
				m.outputCNYPerMillion,
			)
			return responses, usage, response.Error
		}
	}
	usage.CostCNY = usageCost(
		usage,
		m.inputCNYPerMillion,
		m.outputCNYPerMillion,
	)
	if len(responses) == 0 {
		return nil, usage, errors.New("model returned no responses")
	}
	return responses, usage, nil
}

func (m *budgetedRetryModel) Info() model.Info {
	return m.model.Info()
}

func replayResponses(responses []*model.Response) <-chan *model.Response {
	channel := make(chan *model.Response, len(responses))
	for _, response := range responses {
		channel <- response
	}
	close(channel)
	return channel
}

func estimateTextRequest(
	prompt string,
	input string,
	maxOutputTokens int,
	inputCNYPerMillion float64,
	outputCNYPerMillion float64,
) (int, float64) {
	inputTokens := approximateTokens(len([]byte(prompt)) + len([]byte(input)) + 128)
	return inputTokens + maxOutputTokens,
		float64(inputTokens)*inputCNYPerMillion/1_000_000 +
			float64(maxOutputTokens)*outputCNYPerMillion/1_000_000
}

func estimateModelRequest(
	request *model.Request,
	inputCNYPerMillion float64,
	outputCNYPerMillion float64,
) (int, float64) {
	data, _ := json.Marshal(request)
	maxOutputTokens := 1024
	if request != nil && request.GenerationConfig.MaxTokens != nil {
		maxOutputTokens = *request.GenerationConfig.MaxTokens
	}
	return estimateTextRequest(
		string(data),
		"",
		maxOutputTokens,
		inputCNYPerMillion,
		outputCNYPerMillion,
	)
}

func approximateTokens(byteCount int) int {
	if byteCount <= 0 {
		return 1
	}
	return (byteCount + 3) / 4
}

func usageCost(
	usage generationUsage,
	inputCNYPerMillion float64,
	outputCNYPerMillion float64,
) float64 {
	return float64(usage.InputTokens)*inputCNYPerMillion/1_000_000 +
		float64(usage.OutputTokens)*outputCNYPerMillion/1_000_000
}
