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
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/internal/content"
	operatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/jsonpath"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/prompt"
)

type templateMessagesConstructor struct {
	operatorRegistry operatorregistry.Registry
}

// Option configures the template messages constructor.
type Option func(*templateMessagesConstructor)

// WithOperatorRegistry sets the LLM operator registry.
func WithOperatorRegistry(registry operatorregistry.Registry) Option {
	return func(c *templateMessagesConstructor) {
		c.operatorRegistry = registry
	}
}

// New returns a messages constructor for template prompts.
func New(opt ...Option) messagesconstructor.MessagesConstructor {
	c := &templateMessagesConstructor{}
	for _, o := range opt {
		o(c)
	}
	if c.operatorRegistry == nil {
		c.operatorRegistry = operatorregistry.New()
	}
	return c
}

// ConstructMessages renders the configured judge template into a user message.
func (c *templateMessagesConstructor) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	templateOptions, err := judgeTemplateOptions(evalMetric)
	if err != nil {
		return nil, err
	}
	if templateOptions.Prompt == "" {
		return nil, fmt.Errorf("template prompt is empty")
	}
	if templateOptions.ResponseScorerName == "" {
		return nil, fmt.Errorf("template responseScorerName is empty")
	}
	values, err := resolveTemplateValues(actuals, expecteds, evalMetric, templateOptions.VariableBindings)
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

// StructuredOutput returns the configured structured output schema.
func (c *templateMessagesConstructor) StructuredOutput(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*model.StructuredOutput, error) {
	operators, err := c.operatorRegistry.Resolve(evalMetric)
	if err != nil || operators.StructuredOutputProvider == nil {
		return nil, err
	}
	return operators.StructuredOutputProvider.StructuredOutput(ctx, actuals, expecteds, evalMetric)
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

func resolveTemplateValues(actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric,
	bindings []*metricllm.TemplateVariableBinding) (prompt.Vars, error) {
	values := make(prompt.Vars, len(bindings))
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			return nil, fmt.Errorf("template binding is nil")
		}
		name := binding.TemplateVariable
		if name == "" {
			return nil, fmt.Errorf("templateVariable is empty")
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("templateVariable %q is duplicated", name)
		}
		seen[name] = struct{}{}
		value, err := resolveBindingValue(actuals, expecteds, evalMetric, binding.Source)
		if err != nil {
			return nil, fmt.Errorf("resolve template variable %q: %w", name, err)
		}
		values[name] = value
	}
	return values, nil
}

func resolveBindingValue(actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric,
	source *metricllm.TemplateVariableSource) (string, error) {
	if source == nil {
		return "", fmt.Errorf("source is nil")
	}
	var value string
	var err error
	switch source.Scope {
	case metricllm.TemplateVariableScopeActual:
		value, err = resolveActualValue(actuals, source)
	case metricllm.TemplateVariableScopeExpected:
		value, err = resolveExpectedValue(expecteds, source)
	case metricllm.TemplateVariableScopeMetric:
		value, err = resolveMetricValue(evalMetric, source)
	default:
		return "", fmt.Errorf("unsupported source %s.%s", source.Scope, source.Field)
	}
	if err != nil {
		return "", err
	}
	if source.Path == "" {
		return value, nil
	}
	return jsonpath.Extract(value, source.Path)
}

func resolveActualValue(actuals []*evalset.Invocation, source *metricllm.TemplateVariableSource) (string, error) {
	if len(actuals) == 0 {
		return "", fmt.Errorf("actuals is empty")
	}
	actual := actuals[len(actuals)-1]
	if actual == nil {
		return "", fmt.Errorf("actual invocation is nil")
	}
	switch source.Field {
	case metricllm.TemplateVariableFieldUserContent:
		return content.ExtractTextFromContent(actual.UserContent), nil
	case metricllm.TemplateVariableFieldFinalResponse:
		return content.ExtractTextFromContent(actual.FinalResponse), nil
	case metricllm.TemplateVariableFieldTraceStepInput:
		return resolveTraceStepSnapshot(actuals, source, true)
	case metricllm.TemplateVariableFieldTraceStepOutput:
		return resolveTraceStepSnapshot(actuals, source, false)
	default:
		return "", fmt.Errorf("unsupported source %s.%s",
			metricllm.TemplateVariableScopeActual, source.Field)
	}
}

func resolveExpectedValue(expecteds []*evalset.Invocation, source *metricllm.TemplateVariableSource) (string, error) {
	if len(expecteds) == 0 {
		return "", fmt.Errorf("expecteds is empty")
	}
	expected := expecteds[len(expecteds)-1]
	if expected == nil {
		return "", fmt.Errorf("expected invocation is nil")
	}
	switch source.Field {
	case metricllm.TemplateVariableFieldFinalResponse:
		if expected.FinalResponse == nil {
			return "", fmt.Errorf("expected finalResponse is empty")
		}
		return content.ExtractTextFromContent(expected.FinalResponse), nil
	default:
		return "", fmt.Errorf("unsupported source %s.%s",
			metricllm.TemplateVariableScopeExpected, source.Field)
	}
}

func resolveMetricValue(evalMetric *metric.EvalMetric, source *metricllm.TemplateVariableSource) (string, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return "", fmt.Errorf("llm judge criterion is required")
	}
	switch source.Field {
	case metricllm.TemplateVariableFieldRubrics:
		rubrics := visibleRubrics(evalMetric.Criterion.LLMJudge.Rubrics)
		if len(rubrics) == 0 {
			return "", fmt.Errorf("metric rubrics are empty")
		}
		raw, err := json.Marshal(rubrics)
		if err != nil {
			return "", fmt.Errorf("marshal metric rubrics: %w", err)
		}
		return string(raw), nil
	default:
		return "", fmt.Errorf("unsupported source %s.%s",
			metricllm.TemplateVariableScopeMetric, source.Field)
	}
}

func visibleRubrics(rubrics []*metricllm.Rubric) []*metricllm.Rubric {
	visible := make([]*metricllm.Rubric, 0, len(rubrics))
	for _, rubric := range rubrics {
		if rubric == nil || rubric.Content == nil {
			continue
		}
		visible = append(visible, rubric)
	}
	return visible
}

func resolveTraceStepSnapshot(actuals []*evalset.Invocation, source *metricllm.TemplateVariableSource, input bool) (string, error) {
	nodeID := ""
	if source.Selector != nil {
		nodeID = source.Selector.NodeID
	}
	if nodeID == "" {
		return "", fmt.Errorf("trace selector nodeID is required")
	}
	index := len(actuals) - 1
	actual := actuals[index]
	if actual.ExecutionTrace == nil {
		return "", fmt.Errorf("executionTrace is empty for %s.%s at invocation index %d",
			source.Scope, source.Field, index)
	}
	stepIndex := -1
	for i := range actual.ExecutionTrace.Steps {
		if actual.ExecutionTrace.Steps[i].NodeID == nodeID {
			stepIndex = i
		}
	}
	if stepIndex < 0 {
		return "", fmt.Errorf("trace step not found for %s.%s nodeID %q at invocation index %d",
			source.Scope, source.Field, nodeID, index)
	}
	step := actual.ExecutionTrace.Steps[stepIndex]
	snapshot := step.Output
	if input {
		snapshot = step.Input
	}
	if snapshot == nil || snapshot.Text == "" {
		return "", fmt.Errorf("trace snapshot is empty for %s.%s nodeID %q at invocation index %d",
			source.Scope, source.Field, nodeID, index)
	}
	return snapshot.Text, nil
}
