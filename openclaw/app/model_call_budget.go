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
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
)

type modelCallBudgetKey struct{}

const modelCallBudgetRuntimeStateKey = "openclaw.model_call_budget"

type modelCallBudget struct {
	mu    sync.Mutex
	limit int
	count int
}

func newModelCallBudget(limit int) *modelCallBudget {
	if limit <= 0 {
		return nil
	}
	return &modelCallBudget{
		limit: limit,
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

func (b *modelCallBudget) use() error {
	if b == nil || b.limit <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count++
	if b.count <= b.limit {
		return nil
	}
	return agent.NewStopError(
		fmt.Sprintf("max LLM calls (%d) exceeded", b.limit),
	)
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
	if err := modelCallBudgetFromContext(ctx).use(); err != nil {
		return nil, err
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
	if err := modelCallBudgetFromContext(ctx).use(); err != nil {
		return nil, err
	}
	return m.iter.GenerateContentIter(ctx, req)
}

func appendModelCallBudgetGatewayOption(
	opts []gateway.Option,
	limit int,
) []gateway.Option {
	if limit <= 0 {
		return opts
	}
	return append(opts, gateway.WithRunOptionResolver(
		buildModelCallBudgetRunOptionResolver(limit),
	))
}

func buildModelCallBudgetRunOptionResolver(
	limit int,
) gateway.RunOptionResolver {
	return func(ctx context.Context, _ gateway.RunOptionInput) (
		context.Context,
		[]agent.RunOption,
		error,
	) {
		budget := newModelCallBudget(limit)
		return withModelCallBudgetValue(ctx, budget),
			[]agent.RunOption{
				agent.MergeRuntimeState(map[string]any{
					modelCallBudgetRuntimeStateKey: budget,
				}),
			},
			nil
	}
}
