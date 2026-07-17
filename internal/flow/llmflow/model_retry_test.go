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
	)
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("retry"),
	}}

	ctx, customResponse, err := RunModelRetryBeforeCallbacks(ctx, req)
	require.NoError(t, err)
	require.Nil(t, customResponse)
	ctx, err = RunModelRetryAfterCallbacks(
		ctx,
		req,
		&model.Response{ID: "response"},
	)
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.Equal(t, 1, beforeCalls)
	require.Equal(t, 1, afterCalls)
}

func TestModelRetryCallbacks_NilContext(t *testing.T) {
	ctx, resp, err := RunModelRetryBeforeCallbacks(nil, &model.Request{})
	require.NoError(t, err)
	require.Nil(t, ctx)
	require.Nil(t, resp)

	ctx, err = RunModelRetryAfterCallbacks(nil, nil, nil)
	require.NoError(t, err)
	require.Nil(t, ctx)
}
