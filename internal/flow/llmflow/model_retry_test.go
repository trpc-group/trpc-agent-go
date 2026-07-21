//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type modelRetryTestContextKey struct{}
type modelRetryTestCallbacksKey struct{}

type modelRetryTestCallbacks struct {
	before func(context.Context, *model.Request) (
		context.Context,
		*model.Response,
		error,
	)
	after func(context.Context, *model.Request, *model.Response) (
		context.Context,
		error,
	)
}

type modelRetryTestBinder struct{}

func (modelRetryTestBinder) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	return nil, nil
}

func (modelRetryTestBinder) Info() model.Info {
	return model.Info{Name: "retry-test-binder"}
}

func (modelRetryTestBinder) WithModelRetryCallbacks(
	ctx context.Context,
	before func(context.Context, *model.Request) (
		context.Context,
		*model.Response,
		error,
	),
	after func(context.Context, *model.Request, *model.Response) (
		context.Context,
		error,
	),
) context.Context {
	return context.WithValue(ctx, modelRetryTestCallbacksKey{},
		modelRetryTestCallbacks{before: before, after: after})
}

func TestModelRetryCallbacks_RunNormalCallbackChain(t *testing.T) {
	const contextValue = "retry-callback-context"
	var beforeCalls int
	var afterCalls int
	callbacks := model.NewCallbacks().
		RegisterBeforeModel(func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			beforeCalls++
			require.Equal(t, "retry", args.Request.Messages[0].Content)
			return &model.BeforeModelResult{Context: context.WithValue(
				ctx,
				modelRetryTestContextKey{},
				contextValue,
			)}, nil
		}).
		RegisterAfterModel(func(
			ctx context.Context,
			args *model.AfterModelArgs,
		) (*model.AfterModelResult, error) {
			afterCalls++
			require.Equal(t, contextValue, ctx.Value(modelRetryTestContextKey{}))
			require.Equal(t, "retry", args.Request.Messages[0].Content)
			require.Equal(t, "response", args.Response.ID)
			return nil, nil
		})
	flow := New(nil, nil, Options{ModelCallbacks: callbacks})
	ctx := contextWithModelRetryCallbacks(
		context.Background(),
		flow,
		agent.NewInvocation(),
		modelRetryTestBinder{},
	)
	bound, ok := ctx.Value(modelRetryTestCallbacksKey{}).(modelRetryTestCallbacks)
	require.True(t, ok)
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("retry"),
	}}

	ctx, customResponse, err := bound.before(ctx, req)
	require.NoError(t, err)
	require.Nil(t, customResponse)
	ctx, err = bound.after(ctx, req, &model.Response{ID: "response"})
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.Equal(t, 1, beforeCalls)
	require.Equal(t, 1, afterCalls)
}

func TestModelRetryCallbacks_UnsupportedModel(t *testing.T) {
	ctx := context.Background()
	require.Equal(t, ctx, contextWithModelRetryCallbacks(
		ctx,
		New(nil, nil, Options{}),
		agent.NewInvocation(),
		&mockModel{},
	))
}
