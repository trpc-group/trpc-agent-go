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
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Judge runs the configured judge and returns its final response.
func Judge(ctx context.Context, messages []model.Message, evalMetric *metric.EvalMetric, opt ...Option) (*model.Response, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, fmt.Errorf("missing required fields in eval metric")
	}
	opts := newOptions(opt...)
	judgeCriterion := evalMetric.Criterion.LLMJudge
	if judgeRunnerOptions := judgeCriterion.JudgeRunnerOptions; judgeRunnerOptions != nil && judgeRunnerOptions.Runner != nil {
		return judgeWithRunner(ctx, judgeRunnerOptions.Runner, messages, opts)
	}
	return judgeWithModel(ctx, judgeCriterion.JudgeModel, messages, opts)
}

func judgeWithModel(ctx context.Context, judgeModel *criterionllm.JudgeModelOptions,
	messages []model.Message, opts *options) (*model.Response, error) {
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
	req.StructuredOutput = opts.structuredOutput
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

func judgeWithRunner(ctx context.Context, judgeRunner runner.Runner, messages []model.Message, opts *options) (*model.Response, error) {
	if judgeRunner == nil {
		return nil, fmt.Errorf("judge runner is nil")
	}
	runOpts, err := buildRunnerOptions(opts)
	if err != nil {
		return nil, err
	}
	events, err := runner.RunWithMessages(
		ctx,
		judgeRunner,
		uuid.NewString(),
		uuid.NewString(),
		messages,
		runOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	var finalResponse *model.Response
	var structuredOutputPayload any
	for event := range events {
		if event == nil {
			continue
		}
		if event.Error != nil {
			return nil, fmt.Errorf("event: %v", event.Error)
		}
		if event.StructuredOutput != nil {
			structuredOutputPayload = event.StructuredOutput
		}
		if event.Response != nil && event.IsFinalResponse() {
			finalResponse = event.Response.Clone()
		}
	}
	if finalResponse == nil {
		return nil, fmt.Errorf("no final response")
	}
	if err := materializeStructuredOutputContent(finalResponse, structuredOutputPayload, opts); err != nil {
		return nil, err
	}
	return finalResponse, nil
}

func buildRunnerOptions(opts *options) ([]agent.RunOption, error) {
	if opts == nil || opts.structuredOutput == nil {
		return nil, nil
	}
	if opts.structuredOutput.Type != model.StructuredOutputJSONSchema || opts.structuredOutput.JSONSchema == nil {
		return nil, fmt.Errorf("unsupported structured output for judge runner")
	}
	schema := opts.structuredOutput.JSONSchema
	return []agent.RunOption{
		agent.WithStructuredOutputJSONSchema(
			schema.Name,
			schema.Schema,
			schema.Strict,
			schema.Description,
		),
	}, nil
}

func materializeStructuredOutputContent(resp *model.Response, payload any, opts *options) error {
	if opts == nil || opts.structuredOutput == nil {
		return nil
	}
	if resp == nil {
		return fmt.Errorf("response is nil")
	}
	if payload == nil {
		return fmt.Errorf("structured output payload is missing")
	}
	if len(resp.Choices) == 0 {
		resp.Choices = []model.Choice{{Message: model.Message{}}}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal structured output payload: %w", err)
	}
	resp.Choices[0].Message.Content = string(raw)
	return nil
}
