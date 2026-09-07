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
	"regexp"
	"strconv"
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

var errMissingModelUsage = errors.New("model response missing usable token usage")

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
	estimate generationUsage,
) (generationUsage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	reservation := estimate
	reservation.Calls = 1
	stageUsage := b.byStage[stage]
	if err := checkBudget(
		"live",
		b.total,
		reservation,
		b.gate.MaxCalls,
		b.gate.MaxTokens,
		b.gate.MaxCostCNY,
	); err != nil {
		return generationUsage{}, err
	}
	if stage == budgetStageOptimizer {
		if err := checkBudget(
			"live optimizer",
			stageUsage,
			reservation,
			b.optimizer.MaxCalls,
			b.optimizer.MaxTokens,
			b.optimizer.MaxCostCNY,
		); err != nil {
			return generationUsage{}, err
		}
		if err := b.checkEvaluationReserve(estimate); err != nil {
			return generationUsage{}, err
		}
	}
	b.total = b.total.add(reservation)
	stageUsage = stageUsage.add(reservation)
	b.byStage[stage] = stageUsage
	return reservation, nil
}

func (b *liveBudget) checkEvaluationReserve(estimate generationUsage) error {
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
		b.total.tokens()+estimate.tokens()+reserve.Tokens > b.gate.MaxTokens {
		return fmt.Errorf(
			"live optimizer cannot preserve %d evaluation tokens within global limit %d",
			reserve.Tokens,
			b.gate.MaxTokens,
		)
	}
	if b.gate.MaxCostCNY > 0 &&
		b.total.CostCNY+estimate.CostCNY+reserve.CostCNY > b.gate.MaxCostCNY {
		return fmt.Errorf(
			"live optimizer cannot preserve %.4f CNY evaluation budget within global limit %.4f",
			reserve.CostCNY,
			b.gate.MaxCostCNY,
		)
	}
	return nil
}

func (b *liveBudget) recordUsage(
	stage budgetStage,
	reservation generationUsage,
	usage generationUsage,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	accounted := accountedAttemptUsage(reservation, usage)
	b.total = b.total.subtract(reservation).add(accounted)
	stageUsage := b.byStage[stage]
	stageUsage = stageUsage.subtract(reservation).add(accounted)
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

// accountedAttemptUsage reconciles a conservative preflight reservation with
// provider usage. Missing or partial usage cannot safely price the absent
// dimension, so it retains the full reservation instead of understating the
// ledger or making token and cost headroom reusable by a retry.
func accountedAttemptUsage(
	reservation generationUsage,
	usage generationUsage,
) generationUsage {
	if usage.InputTokens <= 0 || usage.OutputTokens <= 0 {
		return reservation
	}
	usage.Calls = reservation.Calls
	return usage
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
	estimate := estimateTextRequest(
		prompt,
		input,
		512,
		g.cfg.InputCNYPerMillion,
		g.cfg.OutputCNYPerMillion,
	)
	for attempt := 0; attempt <= g.cfg.MaxRetries; attempt++ {
		if err := waitForRetry(ctx, attempt); err != nil {
			return generationResult{Usage: accumulated}, err
		}
		reservation, err := g.budget.reserveCall(
			budgetStageEvaluation,
			estimate,
		)
		if err != nil {
			return generationResult{Usage: accumulated}, err
		}
		result, err := g.generateOnce(ctx, prompt, input)
		accounted := accountedAttemptUsage(reservation, result.Usage)
		accumulated = accumulated.add(accounted)
		if budgetErr := g.budget.recordUsage(
			budgetStageEvaluation,
			reservation,
			result.Usage,
		); budgetErr != nil {
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

type transportModelError struct {
	err error
}

func (e *transportModelError) Error() string { return e.err.Error() }
func (e *transportModelError) Unwrap() error { return e.err }

var httpStatusPattern = regexp.MustCompile(
	`(?i)\b([1-5][0-9]{2})\s+(?:bad request|unauthorized|payment required|forbidden|not found|request timeout|conflict|unprocessable (?:content|entity)|too many requests|internal server error|not implemented|bad gateway|service unavailable|gateway timeout)\b`,
)

func isRetryableModelError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errMissingModelUsage) {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if status, ok := modelHTTPStatus(err); ok {
		return status == 408 || status == 409 || status == 429 || status >= 500
	}
	var transportErr *transportModelError
	return errors.As(err, &transportErr)
}

func modelHTTPStatus(err error) (int, bool) {
	match := httpStatusPattern.FindStringSubmatch(err.Error())
	if len(match) != 2 {
		return 0, false
	}
	status, parseErr := strconv.Atoi(match[1])
	return status, parseErr == nil
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
		return generationResult{}, &transportModelError{err: err}
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
		if ctxErr := callCtx.Err(); ctxErr != nil {
			return generationResult{Usage: usage}, &transportModelError{err: ctxErr}
		}
		return generationResult{Usage: usage}, errors.New("model returned empty content")
	}
	if usage.tokens() <= 0 {
		return generationResult{Usage: usage}, errMissingModelUsage
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
	estimate := estimateModelRequest(
		request,
		m.inputCNYPerMillion,
		m.outputCNYPerMillion,
	)
	var lastErr error
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		if err := waitForRetry(ctx, attempt); err != nil {
			return nil, err
		}
		reservation, err := m.budget.reserveCall(
			budgetStageOptimizer,
			estimate,
		)
		if err != nil {
			return nil, err
		}
		responses, usage, err := m.generateOnce(ctx, request)
		if budgetErr := m.budget.recordUsage(
			budgetStageOptimizer,
			reservation,
			usage,
		); budgetErr != nil {
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
		return nil, generationUsage{}, &transportModelError{err: err}
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
		if ctxErr := callCtx.Err(); ctxErr != nil {
			return nil, usage, &transportModelError{err: ctxErr}
		}
		return nil, usage, errors.New("model returned no responses")
	}
	if usage.tokens() <= 0 {
		return responses, usage, errMissingModelUsage
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
) generationUsage {
	inputTokens := conservativeTokenUpperBound(
		len([]byte(prompt)) + len([]byte(input)) + 128,
	)
	return generationUsage{
		InputTokens:  inputTokens,
		OutputTokens: maxOutputTokens,
		CostCNY: float64(inputTokens)*inputCNYPerMillion/1_000_000 +
			float64(maxOutputTokens)*outputCNYPerMillion/1_000_000,
	}
}

func estimateModelRequest(
	request *model.Request,
	inputCNYPerMillion float64,
	outputCNYPerMillion float64,
) generationUsage {
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

// conservativeTokenUpperBound assumes every UTF-8 byte may become one token.
// OpenAI-compatible tokenizers encode non-empty byte sequences, so this is a
// provider-independent preflight upper bound rather than an average estimate.
// Provider-reported usage remains the source of truth after a request.
func conservativeTokenUpperBound(byteCount int) int {
	if byteCount <= 0 {
		return 1
	}
	return byteCount
}

func usageCost(
	usage generationUsage,
	inputCNYPerMillion float64,
	outputCNYPerMillion float64,
) float64 {
	return float64(usage.InputTokens)*inputCNYPerMillion/1_000_000 +
		float64(usage.OutputTokens)*outputCNYPerMillion/1_000_000
}
