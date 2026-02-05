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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

type evalCaseInferenceParam struct {
	idx      int
	ctx      context.Context
	req      *service.InferenceRequest
	evalCase *evalset.EvalCase
	svc      *local
	results  []*service.InferenceResult
	wg       *sync.WaitGroup
}

func (p *evalCaseInferenceParam) reset() {
	p.idx = 0
	p.ctx = nil
	p.req = nil
	p.evalCase = nil
	p.svc = nil
	p.results = nil
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
		param.results[param.idx] = param.svc.inferenceEvalCase(param.ctx, param.req, param.evalCase)
	})
	if err != nil {
		return nil, fmt.Errorf("create eval case inference pool: %w", err)
	}
	return pool, nil
}

type evalCaseEvaluationParam struct {
	idx             int
	ctx             context.Context
	req             *service.EvaluateRequest
	inferenceResult *service.InferenceResult
	svc             *local
	results         []*evalresult.EvalCaseResult
	errs            []error
	wg              *sync.WaitGroup
}

func (p *evalCaseEvaluationParam) reset() {
	p.idx = 0
	p.ctx = nil
	p.req = nil
	p.inferenceResult = nil
	p.svc = nil
	p.results = nil
	p.errs = nil
	p.wg = nil
}

var evalCaseEvaluationParamPool = &sync.Pool{
	New: func() any { return new(evalCaseEvaluationParam) },
}

func createEvalCaseEvaluationPool(size int) (*ants.PoolWithFunc, error) {
	if size <= 0 {
		return nil, errors.New("pool size must be greater than 0")
	}
	pool, err := ants.NewPoolWithFunc(size, func(args any) {
		param, ok := args.(*evalCaseEvaluationParam)
		if !ok {
			panic("eval case evaluation pool args type error")
		}
		wg := param.wg
		defer func() {
			wg.Done()
			param.reset()
			evalCaseEvaluationParamPool.Put(param)
		}()
		caseResult, err := param.svc.evaluateCase(param.ctx, param.req, param.inferenceResult)
		if err != nil {
			evalCaseID := ""
			if param.inferenceResult != nil {
				evalCaseID = param.inferenceResult.EvalCaseID
			}
			err = fmt.Errorf("evaluate case (app=%s, evalSetID=%s, evalCaseID=%s): %w",
				param.req.AppName, param.req.EvalSetID, evalCaseID, err)
			param.errs[param.idx] = err
			return
		}
		param.results[param.idx] = caseResult
	})
	if err != nil {
		return nil, fmt.Errorf("create eval case evaluation pool: %w", err)
	}
	return pool, nil
}
