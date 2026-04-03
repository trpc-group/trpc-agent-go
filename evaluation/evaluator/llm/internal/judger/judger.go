//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package judger runs judge requests for LLM-based evaluators.
package judger

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Judge runs the configured judge and returns its final response.
func Judge(ctx context.Context, messages []model.Message, evalMetric *metric.EvalMetric) (*model.Response, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, fmt.Errorf("missing required fields in eval metric")
	}
	judgeCriterion := evalMetric.Criterion.LLMJudge
	if judgeRunnerOptions := judgeCriterion.JudgeRunnerOptions; judgeRunnerOptions != nil && judgeRunnerOptions.Runner != nil {
		return judgeWithRunner(ctx, judgeRunnerOptions.Runner, messages)
	}
	return judgeWithModel(ctx, judgeCriterion.JudgeModel, messages)
}

func judgeWithModel(ctx context.Context, judgeModel *criterionllm.JudgeModelOptions,
	messages []model.Message) (*model.Response, error) {
	if judgeModel == nil {
		return nil, fmt.Errorf("judge model is nil")
	}
	generation := judgeModel.Generation
	if generation == nil {
		generation = &criterionllm.DefaultGeneration
	}
	req := model.Request{
		Messages:         messages,
		GenerationConfig: *generation,
	}
	req.GenerationConfig.Stream = false
	modelInstance, err := provider.Model(
		judgeModel.ProviderName,
		judgeModel.ModelName,
		provider.WithVariant(judgeModel.Variant),
		provider.WithAPIKey(judgeModel.APIKey),
		provider.WithBaseURL(judgeModel.BaseURL),
		provider.WithExtraFields(judgeModel.ExtraFields),
	)
	if err != nil {
		return nil, fmt.Errorf("create model instance: %w", err)
	}
	responses, err := modelInstance.GenerateContent(ctx, &req)
	if err != nil {
		return nil, fmt.Errorf("generate response: %w", err)
	}
	for response := range responses {
		if response.Error != nil {
			return nil, fmt.Errorf("response error: %v", response.Error)
		}
		if response.IsFinalResponse() {
			return response, nil
		}
	}
	return nil, fmt.Errorf("no final response")
}

func judgeWithRunner(ctx context.Context, judgeRunner runner.Runner, messages []model.Message) (*model.Response, error) {
	if judgeRunner == nil {
		return nil, fmt.Errorf("judge runner is nil")
	}
	events, err := runner.RunWithMessages(
		ctx,
		judgeRunner,
		uuid.NewString(),
		uuid.NewString(),
		messages,
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	var finalResponse *model.Response
	for event := range events {
		if event == nil {
			continue
		}
		if event.Error != nil {
			return nil, fmt.Errorf("event: %v", event.Error)
		}
		if event.Response != nil && event.IsFinalResponse() {
			finalResponse = event.Response.Clone()
		}
	}
	if finalResponse == nil {
		return nil, fmt.Errorf("no final response")
	}
	return finalResponse, nil
}
