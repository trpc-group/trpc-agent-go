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
	"os"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator"
	promptiterevaluator "trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type workflow struct {
	cfg          Config
	iter         promptiterator.PromptIterator
	candidate    runner.Runner
	teacher      runner.Runner
	judge        runner.Runner
	aggregator   runner.Runner
	optimizer    runner.Runner
	evalSetMgr   evalset.Manager
	metricMgr    metric.Manager
	evaluatorReg registry.Registry
	evalSetIDs   []string
}

func newWorkflow(ctx context.Context, cfg Config) (*workflow, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	wf := &workflow{
		cfg:          cfg,
		evalSetMgr:   evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir)),
		metricMgr:    metriclocal.New(metric.WithBaseDir(cfg.DataDir)),
		evaluatorReg: registry.New(),
	}
	evalSetIDs, err := resolveEvalSetIDs(ctx, wf.evalSetMgr, cfg.AppName, cfg.EvalSetIDs)
	if err != nil {
		return nil, err
	}
	wf.evalSetIDs = evalSetIDs
	// Create candidate.
	candidateRunner, err := newCandidateRunner(cfg)
	if err != nil {
		return nil, fmt.Errorf("create candidate runner: %w", err)
	}
	wf.candidate = candidateRunner
	// Create teacher.
	expectedRunner, err := newTeacherRunner(cfg)
	if err != nil {
		return nil, fmt.Errorf("create teacher runner: %w", err)
	}
	wf.teacher = expectedRunner
	// Create aggregator.
	aggRunner, agg, err := newAggregator(cfg)
	if err != nil {
		return nil, err
	}
	wf.aggregator = aggRunner
	// Create optimizer.
	optRunner, opt, err := newOptimizer(cfg)
	if err != nil {
		return nil, err
	}
	wf.optimizer = optRunner
	// Create evaluators.
	jsonSchemaEvaluator, err := promptiterevaluator.NewJSONSchema(cfg.SchemaPath)
	if err != nil {
		return nil, fmt.Errorf("create schema evaluator: %w", err)
	}
	llmCriticEvaluator, err := promptiterevaluator.NewLLMCritic(cfg.JudgePromptPath, cfg.JudgeOutputSchemaPath)
	if err != nil {
		return nil, fmt.Errorf("create critic evaluator: %w", err)
	}
	if err := wf.evaluatorReg.Register(jsonSchemaEvaluator.Name(), jsonSchemaEvaluator); err != nil {
		return nil, fmt.Errorf("register evaluator %s: %w", jsonSchemaEvaluator.Name(), err)
	}
	if err := wf.evaluatorReg.Register(llmCriticEvaluator.Name(), llmCriticEvaluator); err != nil {
		return nil, fmt.Errorf("register evaluator %s: %w", llmCriticEvaluator.Name(), err)
	}
	// Create judge runner used by LLM judge evaluators.
	judgeRunner, err := newJudgeRunner(cfg)
	if err != nil {
		return nil, err
	}
	wf.judge = judgeRunner
	// Create prompt iterator.
	issueExtractor := newIssueExtractor(jsonSchemaEvaluator.Name(), llmCriticEvaluator.Name())
	iterOpts := []promptiterator.Option{
		promptiterator.WithExpectedRunner(expectedRunner),
		promptiterator.WithEvalSetManager(wf.evalSetMgr),
		promptiterator.WithMetricManager(wf.metricMgr),
		promptiterator.WithRegistry(wf.evaluatorReg),
		promptiterator.WithIssueExtractor(issueExtractor),
		promptiterator.WithAggregator(agg),
		promptiterator.WithOptimizer(opt),
	}
	if judgeRunner != nil {
		iterOpts = append(iterOpts, promptiterator.WithJudgeRunner(judgeRunner))
	}
	iter, err := promptiterator.New(cfg.AppName, candidateRunner, iterOpts...)
	if err != nil {
		return nil, fmt.Errorf("create prompt iterator: %w", err)
	}
	wf.iter = iter
	return wf, nil
}

func (w *workflow) Run(ctx context.Context) (*promptiterator.Result, error) {
	if w.iter == nil {
		return nil, errors.New("workflow is not initialized")
	}
	promptBytes, err := os.ReadFile(w.cfg.TargetPromptPath)
	if err != nil {
		return nil, fmt.Errorf("read target prompt: %w", err)
	}
	result, err := w.iter.Run(ctx,
		string(promptBytes),
		w.evalSetIDs,
		promptiterator.WithMaxOptimizationRounds(w.cfg.MaxIters),
	)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (w *workflow) Close() error {
	var overallErr error
	if w.iter != nil {
		if err := w.iter.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close prompt iterator: %w", err))
		}
		w.iter = nil
		w.evalSetMgr = nil
		w.metricMgr = nil
		w.evaluatorReg = nil
	}
	if w.evalSetMgr != nil {
		if err := w.evalSetMgr.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close eval set manager: %w", err))
		}
		w.evalSetMgr = nil
	}
	if w.metricMgr != nil {
		if err := w.metricMgr.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close metric manager: %w", err))
		}
		w.metricMgr = nil
	}
	if w.judge != nil {
		if err := w.judge.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close judge runner: %w", err))
		}
		w.judge = nil
	}
	if w.optimizer != nil {
		if err := w.optimizer.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close optimizer runner: %w", err))
		}
		w.optimizer = nil
	}
	if w.aggregator != nil {
		if err := w.aggregator.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close aggregator runner: %w", err))
		}
		w.aggregator = nil
	}
	if w.teacher != nil {
		if err := w.teacher.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close teacher runner: %w", err))
		}
		w.teacher = nil
	}
	if w.candidate != nil {
		if err := w.candidate.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close candidate runner: %w", err))
		}
		w.candidate = nil
	}
	return overallErr
}

func resolveEvalSetIDs(ctx context.Context, mgr evalset.Manager, appName string, configured []string) ([]string, error) {
	if mgr == nil {
		return nil, errors.New("eval set manager is nil")
	}
	if len(configured) != 0 {
		seen := make(map[string]struct{}, len(configured))
		out := make([]string, 0, len(configured))
		for _, id := range configured {
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		if len(out) == 0 {
			return nil, errors.New("eval set IDs are empty")
		}
		return out, nil
	}
	ids, err := mgr.List(ctx, appName)
	if err != nil {
		return nil, fmt.Errorf("list eval sets: %w", err)
	}
	if len(ids) == 0 {
		return nil, errors.New("no eval sets found")
	}
	sort.Strings(ids)
	return ids, nil
}
