//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package callback

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

type ctxKey struct{}

func TestRunBeforeInferenceSet_EmptyResultReturnsNil(t *testing.T) {
	callbacks := &service.Callbacks{}
	callbacks.Register("empty", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			return &service.BeforeInferenceSetResult{}, nil
		},
	})

	base := context.Background()
	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
	result, err := RunBeforeInferenceSet(base, callbacks, &service.BeforeInferenceSetArgs{Request: req})
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestRunBeforeInferenceSet_KeepsContextFromEarlierCallbackWhenLaterNil(t *testing.T) {
	callbacks := &service.Callbacks{}
	callbacks.Register("first", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			next := context.WithValue(ctx, ctxKey{}, "value")
			return &service.BeforeInferenceSetResult{Context: next}, nil
		},
	})
	callbacks.Register("second", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			return nil, nil
		},
	})

	base := context.Background()
	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
	result, err := RunBeforeInferenceSet(base, callbacks, &service.BeforeInferenceSetArgs{Request: req})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Context)
	assert.Equal(t, "value", result.Context.Value(ctxKey{}))
}

func TestRunBeforeInferenceSet_DropsEarlierContextWhenLastResultEmpty(t *testing.T) {
	callbacks := &service.Callbacks{}
	callbacks.Register("first", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			next := context.WithValue(ctx, ctxKey{}, "value")
			return &service.BeforeInferenceSetResult{Context: next}, nil
		},
	})
	callbacks.Register("second", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			return &service.BeforeInferenceSetResult{}, nil
		},
	})

	base := context.Background()
	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
	result, err := RunBeforeInferenceSet(base, callbacks, &service.BeforeInferenceSetArgs{Request: req})
	assert.NoError(t, err)
	assert.Nil(t, result)
}
