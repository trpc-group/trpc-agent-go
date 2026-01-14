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
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/panjf2000/ants/v2"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

type evalCaseInferenceParam struct {
	idx       int
	ctx       context.Context
	evalSetID string
	evalCase  *evalset.EvalCase
	svc       *local
	results   []*service.InferenceResult
	errs      []error
	wg        *sync.WaitGroup
}

func (p *evalCaseInferenceParam) reset() {
	p.idx = 0
	p.ctx = nil
	p.evalSetID = ""
	p.evalCase = nil
	p.svc = nil
	p.results = nil
	p.errs = nil
	p.wg = nil
}

var evalCaseInferenceParamPool = &sync.Pool{
	New: func() any { return new(evalCaseInferenceParam) },
}

func createEvalCaseInferencePool(size int) (*ants.PoolWithFunc, error) {
	if size <= 0 {
		return nil, errors.New("pool size must be greater than 0")
	}
	pool, err := ants.NewPoolWithFunc(size, func(args any) {
		param, ok := args.(*evalCaseInferenceParam)
		if !ok {
			panic("eval case inference pool args type error")
		}
		wg := param.wg
		defer func() {
			wg.Done()
			param.reset()
			evalCaseInferenceParamPool.Put(param)
		}()
		inferenceResult, err := param.svc.inferenceEvalCase(param.ctx, param.evalSetID, param.evalCase)
		if err != nil {
			param.errs[param.idx] = fmt.Errorf("run inference for eval case %s: %w", param.evalCase.EvalID, err)
			return
		}
		param.results[param.idx] = inferenceResult
	})
	if err != nil {
		return nil, fmt.Errorf("create eval case inference pool: %w", err)
	}
	return pool, nil
}
