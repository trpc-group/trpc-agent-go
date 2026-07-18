//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package registry provides runtime registration for LLM evaluator operators.
package registry

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
)

const (
	// ResponseScorerSingleScoreName identifies the scalar score response scorer.
	ResponseScorerSingleScoreName = "single_score"
	// ResponseScorerRubricScoresName identifies the rubric scores response scorer.
	ResponseScorerRubricScoresName = "rubric_scores"
	// ResponseScorerBooleanName identifies the boolean response scorer.
	ResponseScorerBooleanName = "boolean"
	// ResponseScorerCategoricalName identifies the categorical response scorer.
	ResponseScorerCategoricalName = "categorical"
	// StructuredOutputSingleScoreName identifies the scalar score structured output.
	StructuredOutputSingleScoreName = ResponseScorerSingleScoreName
	// StructuredOutputRubricScoresName identifies the rubric scores structured output.
	StructuredOutputRubricScoresName = ResponseScorerRubricScoresName
	// StructuredOutputBooleanName identifies the boolean structured output.
	StructuredOutputBooleanName = ResponseScorerBooleanName
	// StructuredOutputCategoricalName identifies the categorical structured output.
	StructuredOutputCategoricalName = ResponseScorerCategoricalName
	// SampleAggregatorMajorityVoteName identifies the default samples aggregator.
	SampleAggregatorMajorityVoteName = "majority_vote"
	// InvocationAggregatorAverageName identifies the default invocations aggregator.
	InvocationAggregatorAverageName = "average"
)

// Registry resolves runtime LLM evaluator operators from registered names.
type Registry interface {
	// RegisterResponseScorer registers a named response scorer.
	RegisterResponseScorer(name string, scorer responsescorer.ResponseScorer) error
	// RegisterStructuredOutput registers a named structured output provider.
	RegisterStructuredOutput(name string, provider messagesconstructor.StructuredOutputProvider) error
	// RegisterSamplesAggregator registers a named samples aggregator.
	RegisterSamplesAggregator(name string, aggregator samplesaggregator.SamplesAggregator) error
	// RegisterInvocationsAggregator registers a named invocations aggregator.
	RegisterInvocationsAggregator(name string, aggregator invocationsaggregator.InvocationsAggregator) error
	// Resolve resolves runtime operators for template evaluation.
	Resolve(evalMetric *metric.EvalMetric) (*ResolvedOperators, error)
}

// ResolvedOperators contains operators resolved from a template metric for framework use.
type ResolvedOperators struct {
	ResponseScorer           responsescorer.ResponseScorer
	StructuredOutputProvider messagesconstructor.StructuredOutputProvider
	SamplesAggregator        samplesaggregator.SamplesAggregator
	InvocationsAggregator    invocationsaggregator.InvocationsAggregator
}

type registry struct {
	mu                     sync.RWMutex
	responseScorers        map[string]responsescorer.ResponseScorer
	structuredOutputs      map[string]messagesconstructor.StructuredOutputProvider
	samplesAggregators     map[string]samplesaggregator.SamplesAggregator
	invocationsAggregators map[string]invocationsaggregator.InvocationsAggregator
}

// New creates a LLM operator registry with built-in operators.
func New() Registry {
	r := &registry{
		responseScorers:        make(map[string]responsescorer.ResponseScorer),
		structuredOutputs:      make(map[string]messagesconstructor.StructuredOutputProvider),
		samplesAggregators:     make(map[string]samplesaggregator.SamplesAggregator),
		invocationsAggregators: make(map[string]invocationsaggregator.InvocationsAggregator),
	}
	registerBuiltins(r)
	return r
}

// RegisterResponseScorer registers a named response scorer.
func (r *registry) RegisterResponseScorer(name string, scorer responsescorer.ResponseScorer) error {
	if name == "" {
		return errors.New("response scorer name is empty")
	}
	if scorer == nil {
		return errors.New("response scorer is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.responseScorers[name] = scorer
	return nil
}

// RegisterStructuredOutput registers a named structured output provider.
func (r *registry) RegisterStructuredOutput(name string, provider messagesconstructor.StructuredOutputProvider) error {
	if name == "" {
		return errors.New("structured output name is empty")
	}
	if provider == nil {
		return errors.New("structured output provider is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.structuredOutputs[name] = provider
	return nil
}

// RegisterSamplesAggregator registers a named samples aggregator.
func (r *registry) RegisterSamplesAggregator(name string, aggregator samplesaggregator.SamplesAggregator) error {
	if name == "" {
		return errors.New("samples aggregator name is empty")
	}
	if aggregator == nil {
		return errors.New("samples aggregator is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samplesAggregators[name] = aggregator
	return nil
}

// RegisterInvocationsAggregator registers a named invocations aggregator.
func (r *registry) RegisterInvocationsAggregator(name string, aggregator invocationsaggregator.InvocationsAggregator) error {
	if name == "" {
		return errors.New("invocations aggregator name is empty")
	}
	if aggregator == nil {
		return errors.New("invocations aggregator is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.invocationsAggregators[name] = aggregator
	return nil
}

// Resolve resolves runtime operators for template evaluation.
func (r *registry) Resolve(evalMetric *metric.EvalMetric) (*ResolvedOperators, error) {
	templateOptions, err := judgeTemplateOptions(evalMetric)
	if err != nil {
		return nil, err
	}
	if templateOptions.ResponseScorerName == "" {
		return nil, errors.New("template responseScorerName is empty")
	}
	responseScorer, err := r.lookupResponseScorer(templateOptions.ResponseScorerName)
	if err != nil {
		return nil, err
	}
	samplesAggregator, err := r.lookupSamplesAggregator(sampleAggregatorName(templateOptions))
	if err != nil {
		return nil, err
	}
	invocationsAggregator, err := r.lookupInvocationsAggregator(invocationAggregatorName(templateOptions))
	if err != nil {
		return nil, err
	}
	structuredOutputProvider, err := r.structuredOutputProvider(templateOptions)
	if err != nil {
		return nil, err
	}
	return &ResolvedOperators{
		ResponseScorer:           responseScorer,
		StructuredOutputProvider: structuredOutputProvider,
		SamplesAggregator:        samplesAggregator,
		InvocationsAggregator:    invocationsAggregator,
	}, nil
}

func (r *registry) lookupResponseScorer(name string) (responsescorer.ResponseScorer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if scorer, ok := r.responseScorers[name]; ok {
		return scorer, nil
	}
	return nil, fmt.Errorf("unsupported response scorer %q: %w", name, os.ErrNotExist)
}

func (r *registry) lookupStructuredOutput(name string) (messagesconstructor.StructuredOutputProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if output, ok := r.structuredOutputs[name]; ok {
		return output, nil
	}
	return nil, fmt.Errorf("unsupported structured output %q: %w", name, os.ErrNotExist)
}

func (r *registry) lookupSamplesAggregator(name string) (samplesaggregator.SamplesAggregator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if aggregator, ok := r.samplesAggregators[name]; ok {
		return aggregator, nil
	}
	return nil, fmt.Errorf("unsupported samples aggregator %q: %w", name, os.ErrNotExist)
}

func (r *registry) lookupInvocationsAggregator(name string) (invocationsaggregator.InvocationsAggregator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if aggregator, ok := r.invocationsAggregators[name]; ok {
		return aggregator, nil
	}
	return nil, fmt.Errorf("unsupported invocations aggregator %q: %w", name, os.ErrNotExist)
}

func (r *registry) structuredOutputProvider(templateOptions *criterionllm.JudgeTemplateOptions) (
	messagesconstructor.StructuredOutputProvider, error) {
	name := templateOptions.StructuredOutputName
	if name != "" {
		return r.lookupStructuredOutput(name)
	}
	if templateOptions.ResponseScorerName == "" {
		return nil, errors.New("template responseScorerName is empty")
	}
	if _, err := r.lookupResponseScorer(templateOptions.ResponseScorerName); err != nil {
		return nil, err
	}
	provider, err := r.lookupStructuredOutput(templateOptions.ResponseScorerName)
	if err != nil {
		return nil, nil
	}
	return provider, nil
}

func sampleAggregatorName(templateOptions *criterionllm.JudgeTemplateOptions) string {
	if templateOptions.SampleAggregatorName == "" {
		return SampleAggregatorMajorityVoteName
	}
	return templateOptions.SampleAggregatorName
}

func invocationAggregatorName(templateOptions *criterionllm.JudgeTemplateOptions) string {
	if templateOptions.InvocationAggregatorName == "" {
		return InvocationAggregatorAverageName
	}
	return templateOptions.InvocationAggregatorName
}

func judgeTemplateOptions(evalMetric *metric.EvalMetric) (*criterionllm.JudgeTemplateOptions, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, errors.New("missing llm judge criterion")
	}
	if evalMetric.Criterion.LLMJudge.Template == nil {
		return nil, errors.New("template is nil")
	}
	return evalMetric.Criterion.LLMJudge.Template, nil
}
