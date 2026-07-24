//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package registry

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	operatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/registry"
	llmtemplate "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/template"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/tooltrajectory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type stubEvaluator struct {
	name        string
	description string
}

func (s *stubEvaluator) Name() string {
	return s.name
}

func (s *stubEvaluator) Description() string {
	return s.description
}

func (s *stubEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	return &evaluator.EvaluateResult{
		OverallScore:  42,
		OverallStatus: status.EvalStatusPassed,
	}, nil
}

func TestRegistryDefaults(t *testing.T) {
	reg := New()
	defaultName := tooltrajectory.New().Name()
	defaultEval, err := reg.Get(defaultName)
	assert.NoError(t, err)
	assert.NotNil(t, defaultEval)
	assert.Equal(t, defaultName, defaultEval.Name())
	assert.Contains(t, reg.List(), "llm_rubric_critic")
	assert.Contains(t, reg.List(), "llm_rubric_reference_critic")
	assert.Contains(t, reg.List(), "llm_hallucinations")
	assert.Contains(t, reg.List(), "llm_judge_template")
	assert.Contains(t, reg.List(), "llm_verifier_pairwise")
}

func TestRegistryWithLLMOperatorRegistryInjectsTemplateEvaluator(t *testing.T) {
	operatorRegistry := operatorregistry.New()
	err := operatorRegistry.RegisterResponseScorer("custom_score", fixedTemplateScorer{})
	assert.NoError(t, err)
	reg := New(WithLLMOperatorRegistry(operatorRegistry))
	templateEval, err := reg.Get(llmtemplate.EvaluatorName)
	assert.NoError(t, err)
	scorer, ok := templateEval.(interface {
		ScoreBasedOnResponse(context.Context, *model.Response, *metric.EvalMetric) (*evaluator.ScoreResult, error)
	})
	assert.True(t, ok)
	result, err := scorer.ScoreBasedOnResponse(context.Background(), &model.Response{}, buildTemplateMetric("custom_score"))
	assert.NoError(t, err)
	assert.Equal(t, 0.75, result.Score)
	assert.Equal(t, "custom scorer", result.Reason)
}

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := New()
	custom := &stubEvaluator{name: "custom", description: "custom evaluator"}
	err := reg.Register("custom", custom)
	assert.NoError(t, err)
	got, err := reg.Get("custom")
	assert.NoError(t, err)
	assert.Equal(t, custom, got)
}

func TestRegistryOverwrite(t *testing.T) {
	reg := New()
	first := &stubEvaluator{name: "duplicate"}
	err := reg.Register("duplicate", first)
	assert.NoError(t, err)
	second := &stubEvaluator{name: "duplicate"}
	err = reg.Register("duplicate", second)
	assert.NoError(t, err)
	got, err := reg.Get("duplicate")
	assert.NoError(t, err)
	assert.Equal(t, second, got)
}

func TestRegistryRegisterDeriveName(t *testing.T) {
	reg := New()
	custom := &stubEvaluator{name: "derived"}
	err := reg.Register("", custom)
	assert.NoError(t, err)
	got, err := reg.Get("derived")
	assert.NoError(t, err)
	assert.Equal(t, custom, got)
}

func TestRegistryRegisterErrors(t *testing.T) {
	reg := New()
	err := reg.Register("nil", nil)
	assert.Error(t, err)
	err = reg.Register("", &stubEvaluator{})
	assert.Error(t, err)
}

func TestRegistryGetMissing(t *testing.T) {
	reg := New()
	_, err := reg.Get("missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestRegistryListSorted(t *testing.T) {
	reg := New()
	_ = reg.Register("zzz", &stubEvaluator{name: "zzz"})
	_ = reg.Register("aaa", &stubEvaluator{name: "aaa"})
	names := reg.List()
	assert.True(t, sort.StringsAreSorted(names))
	assert.Contains(t, names, "aaa")
	assert.Contains(t, names, "zzz")
}

func TestRegistryListSorting(t *testing.T) {
	reg := &registry{
		evaluators: map[string]evaluator.Evaluator{
			"b-eval": &stubEvaluator{name: "b-eval"},
			"a-eval": &stubEvaluator{name: "a-eval"},
		},
	}
	assert.Equal(t, []string{"a-eval", "b-eval"}, reg.List())
}

type fixedTemplateScorer struct{}

func (fixedTemplateScorer) ScoreBasedOnResponse(context.Context, *model.Response,
	*metric.EvalMetric) (*evaluator.ScoreResult, error) {
	return &evaluator.ScoreResult{
		Score:  0.75,
		Reason: "custom scorer",
	}, nil
}

func buildTemplateMetric(responseScorerName string) *metric.EvalMetric {
	return &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					ResponseScorerName: responseScorerName,
				},
			},
		},
	}
}
