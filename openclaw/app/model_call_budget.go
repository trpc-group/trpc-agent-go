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

type modelCallBudgetBypassKey struct{}

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

type modelCallBudgetFactory struct {
	mu             sync.Mutex
	limit          int
	finalizeOnLast bool
	budgets        map[string]*modelCallBudget
}

func newModelCallBudgetFactory(
	limit int,
	finalizeOnLast bool,
) *modelCallBudgetFactory {
	if limit <= 0 {
		return nil
	}
	return &modelCallBudgetFactory{
		limit:          limit,
		finalizeOnLast: finalizeOnLast,
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

func withoutModelCallBudget(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, modelCallBudgetBypassKey{}, true)
}

func modelCallBudgetFromContext(ctx context.Context) *modelCallBudget {
	if ctx == nil {
		return nil
	}
	if bypass, _ := ctx.Value(modelCallBudgetBypassKey{}).(bool); bypass {
		return nil
	}
	budget, _ := ctx.Value(modelCallBudgetKey{}).(*modelCallBudget)
	if budget != nil {
		return budget
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil
	}
	factory, _ := agent.GetRuntimeStateValue[*modelCallBudgetFactory](
		&inv.RunOptions,
		modelCallBudgetRuntimeStateKey,
	)
	if factory == nil {
		return nil
	}
	return factory.budgetFor(inv)
}

func (f *modelCallBudgetFactory) budgetFor(
	inv *agent.Invocation,
) *modelCallBudget {
	if f == nil || inv == nil || f.limit <= 0 {
		return nil
	}
	key := modelCallBudgetInvocationKey(inv)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.budgets == nil {
		f.budgets = make(map[string]*modelCallBudget)
	}
	if budget := f.budgets[key]; budget != nil {
		return budget
	}
	budget := newModelCallBudget(f.limit, f.finalizeOnLast)
	f.budgets[key] = budget
	return budget
}

func modelCallBudgetInvocationKey(inv *agent.Invocation) string {
	if inv.InvocationID != "" {
		return inv.InvocationID
	}
	return fmt.Sprintf("%p", inv)
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

func newModelCallBudgetBypassModel(m model.Model) model.Model {
	if m == nil {
		return nil
	}
	wrapped := &modelCallBudgetBypassModel{model: m}
	if iter, ok := m.(model.IterModel); ok {
		return &modelCallBudgetBypassIterModel{
			modelCallBudgetBypassModel: wrapped,
			iter:                       iter,
		}
	}
	return wrapped
}

type modelCallBudgetModel struct {
	model model.Model
}

func (m *modelCallBudgetModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
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

type modelCallBudgetBypassModel struct {
	model model.Model
}

func (m *modelCallBudgetBypassModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	return m.model.GenerateContent(withoutModelCallBudget(ctx), req)
}

func (m *modelCallBudgetBypassModel) Info() model.Info {
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
	finalize, err := modelCallBudgetFromContext(ctx).use()
	if err != nil {
		return nil, err
	}
	if finalize {
		req = finalModelCallRequest(req)
	}
	return m.iter.GenerateContentIter(ctx, req)
}

type modelCallBudgetBypassIterModel struct {
	*modelCallBudgetBypassModel
	iter model.IterModel
}

func (m *modelCallBudgetBypassIterModel) GenerateContentIter(
	ctx context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	return m.iter.GenerateContentIter(withoutModelCallBudget(ctx), req)
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
		factory := newModelCallBudgetFactory(limit, finalizeOnLast)
		return ctx,
			[]agent.RunOption{
				agent.MergeRuntimeState(map[string]any{
					modelCallBudgetRuntimeStateKey: factory,
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
	clone.ExtraFields = finalModelCallExtraFields(req.ExtraFields)
	clone.Messages = append([]model.Message(nil), req.Messages...)
	clone.Messages = append(clone.Messages, model.NewUserMessage(
		"[OpenClaw Budget Notice] This is the final allowed model call "+
			"for this run. No further tools are available now. Use only "+
			"the existing conversation and tool results, then produce "+
			"the final user-facing answer immediately. Do not emit tool "+
			"calls, function calls, JSON tool requests, XML-style tool "+
			"markup such as <tool_call>, code blocks that ask to run "+
			"tools, or descriptions of future tool use. Do not ask to "+
			"continue. Do not describe plans, next steps, additional "+
			"reading, searching, inspection, calculation, or tool use; "+
			"answer now with the best supported final value. Put the "+
			"answer in visible assistant message content for the user, "+
			"not only in internal reasoning or thinking content. If "+
			"evidence is incomplete, give the best supported final answer "+
			"now. If the original task requires a final-answer format, "+
			"follow it exactly.",
	))
	return &clone
}

func finalModelCallExtraFields(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return nil
	}
	clone := make(map[string]any, len(extra))
	for key, value := range extra {
		switch strings.ToLower(key) {
		case "function_call",
			"functions",
			"parallel_tool_calls",
			"tool_choice",
			"tools":
			continue
		default:
			clone[key] = value
		}
	}
	if len(clone) == 0 {
		return nil
	}
	return clone
}
