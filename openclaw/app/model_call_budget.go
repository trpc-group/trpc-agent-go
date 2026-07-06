//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
)

type modelCallBudgetKey struct{}

const modelCallBudgetRuntimeStateKey = "openclaw.model_call_budget"

type modelCallBudget struct {
	mu             sync.Mutex
	limit          int
	count          int
	finalizeOnLast bool
}

func newModelCallBudget(
	limit int,
	finalizeOnLast ...bool,
) *modelCallBudget {
	if limit <= 0 {
		return nil
	}
	finalize := false
	if len(finalizeOnLast) > 0 {
		finalize = finalizeOnLast[0]
	}
	return &modelCallBudget{
		limit:          limit,
		finalizeOnLast: finalize,
	}
}

func withModelCallBudget(ctx context.Context, limit int) context.Context {
	return withModelCallBudgetValue(ctx, newModelCallBudget(limit))
}

func withModelCallBudgetValue(
	ctx context.Context,
	budget *modelCallBudget,
) context.Context {
	if budget == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, modelCallBudgetKey{}, budget)
}

func modelCallBudgetFromContext(ctx context.Context) *modelCallBudget {
	if ctx == nil {
		return nil
	}
	if budget, _ := ctx.Value(modelCallBudgetKey{}).(*modelCallBudget); budget != nil {
		return budget
	}
	budget, _ := agent.GetRuntimeStateValueFromContext[*modelCallBudget](
		ctx,
		modelCallBudgetRuntimeStateKey,
	)
	return budget
}

func (b *modelCallBudget) use() (bool, error) {
	if b == nil || b.limit <= 0 {
		return false, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count++
	if b.count > b.limit {
		return false, agent.NewStopError(
			fmt.Sprintf("max LLM calls (%d) exceeded", b.limit),
		)
	}
	return b.finalizeOnLast && b.count == b.limit, nil
}

func newModelCallBudgetModel(m model.Model) model.Model {
	if m == nil {
		return nil
	}
	wrapped := &modelCallBudgetModel{model: m}
	if iter, ok := m.(model.IterModel); ok {
		return &modelCallBudgetIterModel{
			modelCallBudgetModel: wrapped,
			iter:                 iter,
		}
	}
	return wrapped
}

func modelCallBudgetCallbacks() *model.Callbacks {
	return model.NewCallbacks().RegisterBeforeModel(
		func(context.Context, *model.BeforeModelArgs) (
			*model.BeforeModelResult,
			error,
		) {
			return &model.BeforeModelResult{}, nil
		},
	)
}

type modelCallBudgetModel struct {
	model model.Model
}

func (m *modelCallBudgetModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	if modelCallBudgetShouldSkip(req) {
		return m.model.GenerateContent(ctx, req)
	}
	finalize, err := modelCallBudgetFromContext(ctx).use()
	if err != nil {
		return nil, err
	}
	if finalize {
		req = finalModelCallRequest(req)
	}
	return m.model.GenerateContent(ctx, req)
}

func (m *modelCallBudgetModel) Info() model.Info {
	return m.model.Info()
}

type modelCallBudgetIterModel struct {
	*modelCallBudgetModel
	iter model.IterModel
}

func (m *modelCallBudgetIterModel) GenerateContentIter(
	ctx context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	if modelCallBudgetShouldSkip(req) {
		return m.iter.GenerateContentIter(ctx, req)
	}
	finalize, err := modelCallBudgetFromContext(ctx).use()
	if err != nil {
		return nil, err
	}
	if finalize {
		req = finalModelCallRequest(req)
	}
	return m.iter.GenerateContentIter(ctx, req)
}

func appendModelCallBudgetGatewayOption(
	opts []gateway.Option,
	limit int,
	finalizeOnLast bool,
) []gateway.Option {
	if limit <= 0 {
		return opts
	}
	return append(opts, gateway.WithRunOptionResolver(
		buildModelCallBudgetRunOptionResolver(limit, finalizeOnLast),
	))
}

func buildModelCallBudgetRunOptionResolver(
	limit int,
	finalizeOnLast bool,
) gateway.RunOptionResolver {
	return func(ctx context.Context, _ gateway.RunOptionInput) (
		context.Context,
		[]agent.RunOption,
		error,
	) {
		budget := newModelCallBudget(limit, finalizeOnLast)
		return withModelCallBudgetValue(ctx, budget),
			[]agent.RunOption{
				agent.MergeRuntimeState(map[string]any{
					modelCallBudgetRuntimeStateKey: budget,
				}),
			},
			nil
	}
}

func finalModelCallRequest(req *model.Request) *model.Request {
	if req == nil {
		req = &model.Request{}
	}
	clone := *req
	clone.Tools = nil
	clone.Messages = append([]model.Message(nil), req.Messages...)
	clone.Messages = append(clone.Messages, model.NewUserMessage(
		"[OpenClaw Budget Notice] This is the final allowed model call "+
			"for this run. No further tools are available now. Use only "+
			"the existing conversation and tool results, then produce "+
			"the final user-facing answer immediately. Do not emit tool "+
			"calls, function calls, JSON tool requests, XML-style tool "+
			"markup such as <tool_call>, code blocks that ask to run "+
			"tools, or descriptions of future tool use. Do not ask to "+
			"continue. If the original task requires a final-answer "+
			"format, follow it exactly.",
	))
	return &clone
}

func modelCallBudgetShouldSkip(req *model.Request) bool {
	if req == nil || len(req.Messages) == 0 {
		return false
	}
	first := strings.TrimSpace(req.Messages[0].Content)
	return strings.HasPrefix(
		first,
		"Analyze the following conversation between a user and an assistant",
	)
}
