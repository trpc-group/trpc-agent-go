//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
)

type options struct {
	structureSource structureSource
	agentEvaluator  evaluation.AgentEvaluator
	backwarder      backwarder.Backwarder
	aggregator      aggregator.Aggregator
	optimizer       optimizer.Optimizer
	observer        Observer
}

// Option configures PromptIter engine construction or run behavior.
type Option func(*options)

// WithTargetAgent sets the target agent used to export the optimizable structure.
func WithTargetAgent(targetAgent agent.Agent) Option {
	return func(opts *options) {
		opts.structureSource = agentStructureSource{targetAgent: targetAgent}
	}
}

// WithStructureSnapshot sets a fixed optimizable structure snapshot.
func WithStructureSnapshot(snapshot *astructure.Snapshot) Option {
	return func(opts *options) {
		opts.structureSource = snapshotStructureSource{snapshot: snapshot}
	}
}

// WithAgentEvaluator sets the evaluator used for train and validation runs.
func WithAgentEvaluator(agentEvaluator evaluation.AgentEvaluator) Option {
	return func(opts *options) {
		opts.agentEvaluator = agentEvaluator
	}
}

// WithBackwarder sets the backwarder used to generate sample-level gradients.
func WithBackwarder(backwarderInstance backwarder.Backwarder) Option {
	return func(opts *options) {
		opts.backwarder = backwarderInstance
	}
}

// WithAggregator sets the aggregator used to merge gradients by surface.
func WithAggregator(aggregatorInstance aggregator.Aggregator) Option {
	return func(opts *options) {
		opts.aggregator = aggregatorInstance
	}
}

// WithOptimizer sets the optimizer used to generate candidate patches.
func WithOptimizer(optimizerInstance optimizer.Optimizer) Option {
	return func(opts *options) {
		opts.optimizer = optimizerInstance
	}
}

// WithObserver appends one runtime observer to the run.
func WithObserver(observer Observer) Option {
	return func(opts *options) {
		opts.observer = observer
	}
}

func newOptions(opts ...Option) *options {
	options := &options{}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}
	return options
}
