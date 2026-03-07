//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local

import (
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

func (s *local) resolveInferenceOptions(opt ...service.Option) (*service.Options, error) {
	callOpts := &service.Options{
		EvalSetManager:                    s.evalSetManager,
		SessionIDSupplier:                 s.sessionIDSupplier,
		Callbacks:                         s.callbacks,
		RunOptions:                        append([]agent.RunOption(nil), s.runOptions...),
		EvalCaseParallelism:               s.evalCaseParallelism,
		EvalCaseParallelInferenceEnabled:  s.evalCaseParallelInferenceEnabled,
		EvalCaseParallelEvaluationEnabled: s.evalCaseParallelEvaluationEnabled,
	}
	for _, o := range opt {
		o(callOpts)
	}
	if callOpts.EvalSetManager == nil {
		return nil, errors.New("eval set manager is nil")
	}
	if callOpts.SessionIDSupplier == nil {
		return nil, errors.New("session id supplier is nil")
	}
	if callOpts.EvalCaseParallelInferenceEnabled {
		if callOpts.EvalCaseParallelism <= 0 {
			return nil, errors.New("eval case parallelism must be greater than 0")
		}
		if _, err := s.ensureEvalCaseInferencePool(callOpts.EvalCaseParallelism); err != nil {
			return nil, err
		}
	}
	return callOpts, nil
}

func (s *local) resolveEvaluateOptions(opt ...service.Option) (*service.Options, error) {
	callOpts := &service.Options{
		EvalSetManager:                    s.evalSetManager,
		Registry:                          s.registry,
		Callbacks:                         s.callbacks,
		EvalCaseParallelism:               s.evalCaseParallelism,
		EvalCaseParallelInferenceEnabled:  s.evalCaseParallelInferenceEnabled,
		EvalCaseParallelEvaluationEnabled: s.evalCaseParallelEvaluationEnabled,
	}
	for _, o := range opt {
		o(callOpts)
	}
	if callOpts.EvalSetManager == nil {
		return nil, errors.New("eval set manager is nil")
	}
	if callOpts.Registry == nil {
		return nil, errors.New("registry is nil")
	}
	if callOpts.EvalCaseParallelEvaluationEnabled {
		if callOpts.EvalCaseParallelism <= 0 {
			return nil, errors.New("eval case parallelism must be greater than 0")
		}
		if _, err := s.ensureEvalCaseEvaluationPool(callOpts.EvalCaseParallelism); err != nil {
			return nil, err
		}
	}
	return callOpts, nil
}
