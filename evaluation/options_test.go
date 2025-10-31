//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evaluation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

type stubService struct{}

func (stubService) Inference(ctx context.Context, req *service.InferenceRequest) ([]*service.InferenceResult, error) {
	return nil, nil
}

func (stubService) Evaluate(ctx context.Context, req *service.EvaluateRequest) (*evalresult.EvalSetResult, error) {
	return nil, nil
}

func TestNewOptionsDefaults(t *testing.T) {
	opts := newOptions()

	assert.Equal(t, defaultNumRuns, opts.numRuns)
	assert.NotNil(t, opts.evalSetManager)
	assert.NotNil(t, opts.evalResultManager)
	assert.NotNil(t, opts.metricManager)
	assert.NotNil(t, opts.registry)
	assert.Nil(t, opts.evalService)
}

func TestWithEvalSetManager(t *testing.T) {
	custom := evalsetinmemory.New()
	opts := newOptions(WithEvalSetManager(custom))

	assert.Equal(t, custom, opts.evalSetManager)
}

func TestWithEvalResultManager(t *testing.T) {
	custom := evalresultinmemory.New()
	opts := newOptions(WithEvalResultManager(custom))

	assert.Equal(t, custom, opts.evalResultManager)
}

func TestWithMetricManager(t *testing.T) {
	custom := metricinmemory.New()
	opts := newOptions(WithMetricManager(custom))

	assert.Equal(t, custom, opts.metricManager)
}

func TestWithRegistry(t *testing.T) {
	custom := registry.New()
	opts := newOptions(WithRegistry(custom))

	assert.Equal(t, custom, opts.registry)
}

func TestWithEvaluationService(t *testing.T) {
	custom := stubService{}
	opts := newOptions(WithEvaluationService(custom))

	assert.Equal(t, custom, opts.evalService)
}

func TestWithNumRuns(t *testing.T) {
	opts := newOptions(WithNumRuns(5))
	assert.Equal(t, 5, opts.numRuns)
}
