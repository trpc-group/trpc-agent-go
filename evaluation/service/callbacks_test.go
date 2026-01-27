//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCallbacksRegisterNilCallbackNoop(t *testing.T) {
	callbacks := &Callbacks{}

	got := callbacks.Register("noop", nil)

	assert.Same(t, callbacks, got)
	assert.Empty(t, callbacks.BeforeInferenceSet)
	assert.Empty(t, callbacks.AfterInferenceSet)
	assert.Empty(t, callbacks.BeforeInferenceCase)
	assert.Empty(t, callbacks.AfterInferenceCase)
	assert.Empty(t, callbacks.BeforeEvaluateSet)
	assert.Empty(t, callbacks.AfterEvaluateSet)
	assert.Empty(t, callbacks.BeforeEvaluateCase)
	assert.Empty(t, callbacks.AfterEvaluateCase)
}

func TestCallbacksRegisterRegistersAllNonNilPoints(t *testing.T) {
	callbacks := &Callbacks{}

	callbacks.Register("component", &Callback{
		BeforeInferenceSet: func(ctx context.Context, args *BeforeInferenceSetArgs) (*BeforeInferenceSetResult, error) {
			return nil, nil
		},
		AfterInferenceSet: func(ctx context.Context, args *AfterInferenceSetArgs) (*AfterInferenceSetResult, error) {
			return nil, nil
		},
		BeforeInferenceCase: func(ctx context.Context, args *BeforeInferenceCaseArgs) (*BeforeInferenceCaseResult, error) {
			return nil, nil
		},
		AfterInferenceCase: func(ctx context.Context, args *AfterInferenceCaseArgs) (*AfterInferenceCaseResult, error) {
			return nil, nil
		},
		BeforeEvaluateSet: func(ctx context.Context, args *BeforeEvaluateSetArgs) (*BeforeEvaluateSetResult, error) {
			return nil, nil
		},
		AfterEvaluateSet: func(ctx context.Context, args *AfterEvaluateSetArgs) (*AfterEvaluateSetResult, error) {
			return nil, nil
		},
		BeforeEvaluateCase: func(ctx context.Context, args *BeforeEvaluateCaseArgs) (*BeforeEvaluateCaseResult, error) {
			return nil, nil
		},
		AfterEvaluateCase: func(ctx context.Context, args *AfterEvaluateCaseArgs) (*AfterEvaluateCaseResult, error) {
			return nil, nil
		},
	})

	assert.Len(t, callbacks.BeforeInferenceSet, 1)
	assert.Equal(t, "component", callbacks.BeforeInferenceSet[0].Name)
	assert.Len(t, callbacks.AfterInferenceSet, 1)
	assert.Equal(t, "component", callbacks.AfterInferenceSet[0].Name)
	assert.Len(t, callbacks.BeforeInferenceCase, 1)
	assert.Equal(t, "component", callbacks.BeforeInferenceCase[0].Name)
	assert.Len(t, callbacks.AfterInferenceCase, 1)
	assert.Equal(t, "component", callbacks.AfterInferenceCase[0].Name)
	assert.Len(t, callbacks.BeforeEvaluateSet, 1)
	assert.Equal(t, "component", callbacks.BeforeEvaluateSet[0].Name)
	assert.Len(t, callbacks.AfterEvaluateSet, 1)
	assert.Equal(t, "component", callbacks.AfterEvaluateSet[0].Name)
	assert.Len(t, callbacks.BeforeEvaluateCase, 1)
	assert.Equal(t, "component", callbacks.BeforeEvaluateCase[0].Name)
	assert.Len(t, callbacks.AfterEvaluateCase, 1)
	assert.Equal(t, "component", callbacks.AfterEvaluateCase[0].Name)
}

func TestCallbacksRegisterPreservesOrder(t *testing.T) {
	callbacks := &Callbacks{}

	callbacks.Register("first", &Callback{
		BeforeInferenceSet: func(ctx context.Context, args *BeforeInferenceSetArgs) (*BeforeInferenceSetResult, error) {
			return nil, nil
		},
	})
	callbacks.Register("second", &Callback{
		BeforeInferenceSet: func(ctx context.Context, args *BeforeInferenceSetArgs) (*BeforeInferenceSetResult, error) {
			return nil, nil
		},
	})

	assert.Len(t, callbacks.BeforeInferenceSet, 2)
	assert.Equal(t, "first", callbacks.BeforeInferenceSet[0].Name)
	assert.Equal(t, "second", callbacks.BeforeInferenceSet[1].Name)
}
