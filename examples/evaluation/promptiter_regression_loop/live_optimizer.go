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
	"fmt"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	promptiteroptimizer "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const liveOptimizerAgentName = "promptiter-live-optimizer"

type trackingOptimizer struct {
	delegate promptiteroptimizer.Optimizer

	mu      sync.RWMutex
	current string
	reason  string
}

func newTrackingOptimizer(
	baseline string,
	delegate promptiteroptimizer.Optimizer,
) *trackingOptimizer {
	return &trackingOptimizer{
		delegate: delegate,
		current:  strings.TrimSpace(baseline),
	}
}

func (o *trackingOptimizer) currentPrompt() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.current
}

func (o *trackingOptimizer) lastReason() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.reason
}

func (o *trackingOptimizer) Optimize(
	ctx context.Context,
	request *promptiteroptimizer.Request,
) (*promptiteroptimizer.Result, error) {
	if o.delegate == nil {
		return nil, errors.New("live optimizer delegate is nil")
	}
	result, err := o.delegate.Optimize(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Patch == nil || result.Patch.Value.Text == nil {
		return nil, errors.New("live optimizer returned no instruction patch")
	}
	candidate := strings.TrimSpace(*result.Patch.Value.Text)
	if candidate == "" {
		return nil, errors.New("live optimizer returned an empty instruction")
	}
	o.mu.Lock()
	o.current = candidate
	o.reason = strings.TrimSpace(result.Patch.Reason)
	o.mu.Unlock()
	return result, nil
}

type liveOptimizerRuntime struct {
	optimizer *trackingOptimizer
	model     modelAudit
	close     func() error
}

func newLivePromptOptimizer(
	ctx context.Context,
	cfg *loadedConfig,
	budget *liveBudget,
	apiKey string,
) (*liveOptimizerRuntime, error) {
	if cfg == nil {
		return nil, errors.New("loaded config is nil")
	}
	optimizerModel, err := newOpenAICompatibleModel(
		cfg.Live.Optimizer.Model,
		cfg.Live.Optimizer.BaseURL,
		cfg.Live.APIKeyEnv,
		apiKey,
	)
	if err != nil {
		return nil, fmt.Errorf("create live optimizer model: %w", err)
	}
	return newLivePromptOptimizerWithModel(ctx, cfg, budget, optimizerModel)
}

func newLivePromptOptimizerWithModel(
	ctx context.Context,
	cfg *loadedConfig,
	budget *liveBudget,
	optimizerModel model.Model,
) (*liveOptimizerRuntime, error) {
	if cfg == nil {
		return nil, errors.New("loaded config is nil")
	}
	if budget == nil {
		return nil, errors.New("live budget is nil")
	}
	if optimizerModel == nil {
		return nil, errors.New("live optimizer model is nil")
	}
	budgetedModel := &budgetedRetryModel{
		model:               optimizerModel,
		timeoutSeconds:      cfg.Live.Optimizer.TimeoutSeconds,
		maxRetries:          cfg.Live.Optimizer.MaxRetries,
		inputCNYPerMillion:  cfg.Live.InputCNYPerMillion,
		outputCNYPerMillion: cfg.Live.OutputCNYPerMillion,
		budget:              budget,
	}
	maxTokens := cfg.Live.Optimizer.MaxOutputTokens
	temperature := cfg.Live.Optimizer.Temperature
	stream := false
	thinking := false
	optimizerAgent := llmagent.New(
		liveOptimizerAgentName,
		llmagent.WithModel(budgetedModel),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:       &maxTokens,
			Temperature:     &temperature,
			Stream:          stream,
			ThinkingEnabled: &thinking,
		}),
	)
	optimizerRunner := runner.NewRunner(
		"promptiter-regression-live-optimizer",
		optimizerAgent,
	)
	officialOptimizer, err := promptiteroptimizer.New(ctx, optimizerRunner)
	if err != nil {
		_ = optimizerRunner.Close()
		return nil, fmt.Errorf("create PromptIter optimizer: %w", err)
	}
	return &liveOptimizerRuntime{
		optimizer: newTrackingOptimizer(cfg.Prompt, officialOptimizer),
		model: modelAudit{
			Provider: "deepseek",
			Name:     cfg.Live.Optimizer.Model,
			BaseURL:  cfg.Live.Optimizer.BaseURL,
		},
		close: optimizerRunner.Close,
	}, nil
}
