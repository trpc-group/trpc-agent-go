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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type modelRetryCallbacksKey struct{}

type modelRetryCallbacks struct {
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

func contextWithModelRetryCallbacks(
	ctx context.Context,
	flow *Flow,
	invocation *agent.Invocation,
) context.Context {
	if ctx == nil || flow == nil {
		return ctx
	}
	callbacks := modelRetryCallbacks{
		before: func(
			callbackCtx context.Context,
			req *model.Request,
		) (context.Context, *model.Response, error) {
			return flow.runBeforeModelCallbacks(callbackCtx, invocation, req)
		},
		after: func(
			callbackCtx context.Context,
			req *model.Request,
			resp *model.Response,
		) (context.Context, error) {
			updatedCtx, _, err := flow.runAfterModelCallbacks(
				callbackCtx,
				invocation,
				req,
				resp,
			)
			return updatedCtx, err
		},
	}
	return context.WithValue(ctx, modelRetryCallbacksKey{}, callbacks)
}

// RunModelRetryBeforeCallbacks runs the normal before-model callback chain for
// a physical retry initiated by a model wrapper.
func RunModelRetryBeforeCallbacks(
	ctx context.Context,
	req *model.Request,
) (context.Context, *model.Response, error) {
	if ctx == nil {
		return nil, nil, nil
	}
	callbacks, _ := ctx.Value(modelRetryCallbacksKey{}).(modelRetryCallbacks)
	if callbacks.before == nil {
		return ctx, nil, nil
	}
	return callbacks.before(ctx, req)
}

// RunModelRetryAfterCallbacks pairs a discarded physical response with the
// normal after-model callback chain before a wrapper starts a retry.
func RunModelRetryAfterCallbacks(
	ctx context.Context,
	req *model.Request,
	resp *model.Response,
) (context.Context, error) {
	if ctx == nil {
		return nil, nil
	}
	callbacks, _ := ctx.Value(modelRetryCallbacksKey{}).(modelRetryCallbacks)
	if callbacks.after == nil {
		return ctx, nil
	}
	return callbacks.after(ctx, req, resp)
}
