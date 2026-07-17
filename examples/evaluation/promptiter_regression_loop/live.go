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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

type generationUsage struct {
	Calls        int
	InputTokens  int
	OutputTokens int
	CostCNY      float64
}

type generationResult struct {
	Text  string
	Usage generationUsage
}

type textGenerator interface {
	Generate(ctx context.Context, prompt, input string) (generationResult, error)
}

type liveGenerator struct {
	model model.Model
	cfg   liveConfig
	gate  gateFileConfig

	mu    sync.Mutex
	usage generationUsage
}

func newLiveGenerator(cfg liveConfig, gate gateFileConfig, apiKey string) (*liveGenerator, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%s is empty", cfg.APIKeyEnv)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("live model is empty")
	}
	options := []openai.Option{
		openai.WithAPIKey(apiKey),
		openai.WithVariant(openai.VariantDeepSeek),
	}
	if strings.TrimSpace(cfg.BaseURL) != "" {
		options = append(options, openai.WithBaseURL(strings.TrimSpace(cfg.BaseURL)))
	}
	return &liveGenerator{
		model: openai.New(cfg.Model, options...),
		cfg:   cfg,
		gate:  gate,
	}, nil
}

func (g *liveGenerator) Generate(
	ctx context.Context,
	prompt string,
	input string,
) (generationResult, error) {
	var lastErr error
	attempts := 0
	estimatedInputTokens := len([]byte(prompt)) + len([]byte(input)) + 32
	const estimatedOutputTokens = 512
	estimatedTokens := estimatedInputTokens + estimatedOutputTokens
	estimatedCost := float64(estimatedInputTokens)*g.cfg.InputCNYPerMillion/1_000_000 +
		float64(estimatedOutputTokens)*g.cfg.OutputCNYPerMillion/1_000_000
	for attempt := 0; attempt <= g.cfg.MaxRetries; attempt++ {
		if err := g.reserveCall(estimatedTokens, estimatedCost); err != nil {
			return generationResult{Usage: generationUsage{Calls: attempts}}, err
		}
		attempts++
		if attempt > 0 {
			delay := time.Duration(1<<(attempt-1)) * 250 * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return generationResult{}, ctx.Err()
			case <-timer.C:
			}
		}
		result, err := g.generateOnce(ctx, prompt, input)
		if err == nil {
			result.Usage.Calls = attempts
			if budgetErr := g.recordUsage(result.Usage); budgetErr != nil {
				return result, budgetErr
			}
			return result, nil
		}
		lastErr = err
		if !isRetryableModelError(err) {
			return generationResult{Usage: generationUsage{Calls: attempts}}, fmt.Errorf("non-retryable model error: %w", err)
		}
	}
	return generationResult{Usage: generationUsage{Calls: attempts}}, fmt.Errorf("model call failed after retries: %w", lastErr)
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
	usage := generationUsage{Calls: 1}
	for response := range responses {
		if response == nil {
			continue
		}
		if response.Error != nil {
			return generationResult{}, response.Error
		}
		for _, choice := range response.Choices {
			if choice.Message.Content != "" {
				content.WriteString(choice.Message.Content)
			} else if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
			}
		}
		if response.Usage != nil {
			usage.InputTokens += response.Usage.PromptTokens
			usage.OutputTokens += response.Usage.CompletionTokens
		}
	}
	if strings.TrimSpace(content.String()) == "" {
		return generationResult{}, errors.New("model returned empty content")
	}
	usage.CostCNY = float64(usage.InputTokens)*g.cfg.InputCNYPerMillion/1_000_000 +
		float64(usage.OutputTokens)*g.cfg.OutputCNYPerMillion/1_000_000
	return generationResult{Text: strings.TrimSpace(content.String()), Usage: usage}, nil
}

func (g *liveGenerator) reserveCall(estimatedTokens int, estimatedCost float64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.gate.MaxCalls > 0 && g.usage.Calls >= g.gate.MaxCalls {
		return fmt.Errorf("live call budget exhausted: %d calls", g.gate.MaxCalls)
	}
	if g.gate.MaxCostCNY > 0 && g.usage.CostCNY >= g.gate.MaxCostCNY {
		return fmt.Errorf("live cost budget exhausted: %.4f CNY", g.gate.MaxCostCNY)
	}
	if g.gate.MaxTokens > 0 && g.usage.InputTokens+g.usage.OutputTokens+estimatedTokens > g.gate.MaxTokens {
		return fmt.Errorf("live token budget cannot reserve %d tokens within limit %d", estimatedTokens, g.gate.MaxTokens)
	}
	if g.gate.MaxCostCNY > 0 && g.usage.CostCNY+estimatedCost > g.gate.MaxCostCNY {
		return fmt.Errorf("live cost budget cannot reserve %.4f CNY within limit %.4f", estimatedCost, g.gate.MaxCostCNY)
	}
	g.usage.Calls++
	return nil
}

func (g *liveGenerator) recordUsage(usage generationUsage) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.usage.InputTokens += usage.InputTokens
	g.usage.OutputTokens += usage.OutputTokens
	g.usage.CostCNY += usage.CostCNY
	if g.gate.MaxCalls > 0 && g.usage.Calls > g.gate.MaxCalls {
		return fmt.Errorf("live call budget exceeded: %d > %d", g.usage.Calls, g.gate.MaxCalls)
	}
	if g.gate.MaxTokens > 0 && g.usage.InputTokens+g.usage.OutputTokens > g.gate.MaxTokens {
		return fmt.Errorf("live token budget exceeded: %d > %d", g.usage.InputTokens+g.usage.OutputTokens, g.gate.MaxTokens)
	}
	if g.gate.MaxCostCNY > 0 && g.usage.CostCNY > g.gate.MaxCostCNY {
		return fmt.Errorf("live cost budget exceeded: %.4f > %.4f CNY", g.usage.CostCNY, g.gate.MaxCostCNY)
	}
	return nil
}
