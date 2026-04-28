//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package template assembles judge prompts from template configuration.
package template

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/internal/content"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/prompt"
)

type templateMessagesConstructor struct {
}

// New returns a messages constructor for template prompts.
func New() messagesconstructor.MessagesConstructor {
	return &templateMessagesConstructor{}
}

// ConstructMessages renders the configured judge template into a user message.
func (c *templateMessagesConstructor) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	templateOptions, err := judgeTemplateOptions(evalMetric)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(templateOptions.Prompt) == "" {
		return nil, fmt.Errorf("template prompt is empty")
	}
	if strings.TrimSpace(templateOptions.ResponseScorerName) == "" {
		return nil, fmt.Errorf("template responseScorerName is empty")
	}
	values, err := resolveTemplateValues(actuals, expecteds, templateOptions.VariableBindings)
	if err != nil {
		return nil, err
	}
	rendered, err := prompt.Text{
		Template: templateOptions.Prompt,
		Syntax:   prompt.SyntaxDoubleBrace,
	}.Render(prompt.RenderEnv{
		Vars: values,
	}, prompt.WithUnknownBehavior(prompt.ErrorOnUnknown))
	if err != nil {
		return nil, fmt.Errorf("render template prompt: %w", err)
	}
	return []model.Message{{
		Role:    model.RoleUser,
		Content: rendered,
	}}, nil
}

func judgeTemplateOptions(evalMetric *metric.EvalMetric) (*metricllm.JudgeTemplateOptions, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, fmt.Errorf("missing llm judge criterion")
	}
	if evalMetric.Criterion.LLMJudge.Template == nil {
		return nil, fmt.Errorf("template is nil")
	}
	return evalMetric.Criterion.LLMJudge.Template, nil
}

func resolveTemplateValues(actuals, expecteds []*evalset.Invocation,
	bindings []*metricllm.TemplateVariableBinding) (prompt.Vars, error) {
	values := make(prompt.Vars, len(bindings))
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			return nil, fmt.Errorf("template binding is nil")
		}
		name := strings.TrimSpace(binding.TemplateVariable)
		if name == "" {
			return nil, fmt.Errorf("templateVariable is empty")
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("templateVariable %q is duplicated", name)
		}
		seen[name] = struct{}{}
		value, err := resolveBindingValue(actuals, expecteds, binding.Source)
		if err != nil {
			return nil, fmt.Errorf("resolve template variable %q: %w", name, err)
		}
		values[name] = value
	}
	return values, nil
}

func resolveBindingValue(actuals, expecteds []*evalset.Invocation,
	source *metricllm.TemplateVariableSource) (string, error) {
	if source == nil {
		return "", fmt.Errorf("source is nil")
	}
	switch source.Scope {
	case metricllm.TemplateVariableScopeActual:
		return resolveActualValue(actuals, source.Field)
	case metricllm.TemplateVariableScopeExpected:
		return resolveExpectedValue(expecteds, source.Field)
	default:
		return "", fmt.Errorf("unsupported source %s.%s", source.Scope, source.Field)
	}
}

func resolveActualValue(actuals []*evalset.Invocation, field metricllm.TemplateVariableField) (string, error) {
	if len(actuals) == 0 {
		return "", fmt.Errorf("actuals is empty")
	}
	actual := actuals[len(actuals)-1]
	if actual == nil {
		return "", fmt.Errorf("actual invocation is nil")
	}
	switch field {
	case metricllm.TemplateVariableFieldUserContent:
		return content.ExtractTextFromContent(actual.UserContent), nil
	case metricllm.TemplateVariableFieldFinalResponse:
		return content.ExtractTextFromContent(actual.FinalResponse), nil
	default:
		return "", fmt.Errorf("unsupported source %s.%s",
			metricllm.TemplateVariableScopeActual, field)
	}
}

func resolveExpectedValue(expecteds []*evalset.Invocation, field metricllm.TemplateVariableField) (string, error) {
	if len(expecteds) == 0 {
		return "", fmt.Errorf("expecteds is empty")
	}
	expected := expecteds[len(expecteds)-1]
	if expected == nil {
		return "", fmt.Errorf("expected invocation is nil")
	}
	switch field {
	case metricllm.TemplateVariableFieldFinalResponse:
		if expected.FinalResponse == nil {
			return "", fmt.Errorf("expected finalResponse is empty")
		}
		return content.ExtractTextFromContent(expected.FinalResponse), nil
	default:
		return "", fmt.Errorf("unsupported source %s.%s",
			metricllm.TemplateVariableScopeExpected, field)
	}
}
