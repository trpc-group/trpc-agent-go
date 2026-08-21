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
			updatedCtx, resp, err := flow.runBeforeModelCallbacks(
				callbackCtx,
				invocation,
				req,
			)
			if err != nil || resp != nil {
				return updatedCtx, resp, err
			}
			// A retry re-runs the callbacks over the same request, and they can
			// change what it carries — a final retry drops tools before asking
			// again. The request is only final once they have returned, so the
			// annotators run here too; skipping them would send the retry with an
			// annotation describing the surface of the attempt before it.
			flow.annotateFinalizedRequest(updatedCtx, invocation, req)
			return updatedCtx, resp, err
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
