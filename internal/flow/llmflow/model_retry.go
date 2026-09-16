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

type modelRetryCallbackBinder interface {
	WithModelRetryCallbacks(
		context.Context,
		func(context.Context, *model.Request) (
			context.Context,
			*model.Response,
			error,
		),
		func(context.Context, *model.Request, *model.Response) (
			context.Context,
			error,
		),
	) context.Context
}

func contextWithModelRetryCallbacks(
	ctx context.Context,
	flow *Flow,
	invocation *agent.Invocation,
	callModel model.Model,
) context.Context {
	binder, ok := callModel.(modelRetryCallbackBinder)
	if ctx == nil || flow == nil || !ok {
		return ctx
	}
	return binder.WithModelRetryCallbacks(
		ctx,
		func(
			callbackCtx context.Context,
			req *model.Request,
		) (context.Context, *model.Response, error) {
			return flow.runBeforeModelCallbacks(
				callbackCtx,
				invocation,
				req,
			)
		},
		func(
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
	)
}
