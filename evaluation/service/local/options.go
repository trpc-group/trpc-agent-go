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
		EvalSetManager: s.evalSetManager,
		RunOptions:     append([]agent.RunOption(nil), s.runOptions...),
	}
	for _, o := range opt {
		o(callOpts)
	}
	if callOpts.EvalSetManager == nil {
		return nil, errors.New("eval set manager is nil")
	}
	return callOpts, nil
}

func (s *local) resolveEvaluateOptions(opt ...service.Option) *service.Options {
	callOpts := &service.Options{
		EvalSetManager: s.evalSetManager,
		Registry:       s.registry,
	}
	for _, o := range opt {
		o(callOpts)
	}
	return callOpts
}
