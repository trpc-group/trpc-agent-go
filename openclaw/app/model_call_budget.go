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
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
)

type modelCallBudgetKey struct{}

type modelCallBudgetBypassKey struct{}

const modelCallBudgetRuntimeStateKey = "openclaw.model_call_budget"

type modelCallBudgetFinalRequestConfig struct {
	DisableThinking bool
}

type modelCallBudget struct {
	mu             sync.Mutex
	limit          int
	count          int
	finalizeOnLast bool
	deadlineWindow time.Duration
	finalRequest   modelCallBudgetFinalRequestConfig
}

func newModelCallBudget(
	limit int,
	finalizeOnLast bool,
	deadlineWindow time.Duration,
	finalRequest ...modelCallBudgetFinalRequestConfig,
) *modelCallBudget {
	if limit <= 0 && deadlineWindow <= 0 {
		return nil
	}
	return &modelCallBudget{
		limit:          limit,
		finalizeOnLast: finalizeOnLast,
		deadlineWindow: deadlineWindow,
		finalRequest:   modelCallBudgetFinalRequestArg(finalRequest),
	}
}

type modelCallBudgetFactory struct {
	mu             sync.Mutex
	limit          int
	finalizeOnLast bool
	deadlineWindow time.Duration
	finalRequest   modelCallBudgetFinalRequestConfig
	budgets        map[string]*modelCallBudget
}

func newModelCallBudgetFactory(
	limit int,
	finalizeOnLast bool,
	deadlineWindow time.Duration,
	finalRequest ...modelCallBudgetFinalRequestConfig,
) *modelCallBudgetFactory {
	if limit <= 0 && deadlineWindow <= 0 {
		return nil
	}
	return &modelCallBudgetFactory{
		limit:          limit,
		finalizeOnLast: finalizeOnLast,
		deadlineWindow: deadlineWindow,
		finalRequest:   modelCallBudgetFinalRequestArg(finalRequest),
	}
}

func modelCallBudgetFinalRequestArg(
	finalRequest []modelCallBudgetFinalRequestConfig,
) modelCallBudgetFinalRequestConfig {
	if len(finalRequest) == 0 {
		return modelCallBudgetFinalRequestConfig{}
	}
	return finalRequest[0]
}

func withModelCallBudget(ctx context.Context, limit int) context.Context {
	return withModelCallBudgetValue(ctx, newModelCallBudget(limit, false, 0))
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
	if f == nil || inv == nil ||
		(f.limit <= 0 && f.deadlineWindow <= 0) {
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
	budget := newModelCallBudget(
		f.limit,
		f.finalizeOnLast,
		f.deadlineWindow,
		f.finalRequest,
	)
	f.budgets[key] = budget
	return budget
}

func modelCallBudgetInvocationKey(inv *agent.Invocation) string {
	if inv.InvocationID != "" {
		return inv.InvocationID
	}
	return fmt.Sprintf("%p", inv)
}

func (b *modelCallBudget) use(ctx context.Context) (bool, error) {
	if b == nil || (b.limit <= 0 && b.deadlineWindow <= 0) {
		return false, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit > 0 {
		b.count++
		if b.count > b.limit {
			return false, agent.NewStopError(
				fmt.Sprintf("max LLM calls (%d) exceeded", b.limit),
			)
		}
		if b.finalizeOnLast && b.count == b.limit {
			return true, nil
		}
	}
	return modelCallBudgetDeadlineSoon(ctx, b.deadlineWindow), nil
}

func (b *modelCallBudget) finalConfig() modelCallBudgetFinalRequestConfig {
	if b == nil {
		return modelCallBudgetFinalRequestConfig{}
	}
	return b.finalRequest
}

func modelCallBudgetDeadlineSoon(
	ctx context.Context,
	window time.Duration,
) bool {
	if ctx == nil || window <= 0 {
		return false
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return false
	}
	return time.Until(deadline) <= window
}

func modelCallBudgetPrefinalContext(
	ctx context.Context,
	window time.Duration,
) (context.Context, context.CancelFunc, bool) {
	if ctx == nil || window <= 0 {
		return ctx, nil, false
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return ctx, nil, false
	}
	prefinalDeadline := deadline.Add(-window)
	if !time.Now().Before(prefinalDeadline) {
		return ctx, nil, false
	}
	child, cancel := context.WithDeadline(ctx, prefinalDeadline)
	return child, cancel, true
}

func modelCallBudgetPrefinalTimedOut(
	prefinalCtx context.Context,
	parentCtx context.Context,
) bool {
	if prefinalCtx == nil || parentCtx == nil {
		return false
	}
	return errors.Is(prefinalCtx.Err(), context.DeadlineExceeded) &&
		parentCtx.Err() == nil
}

func modelCallBudgetPrefinalTimeoutResponse(
	resp *model.Response,
	prefinalCtx context.Context,
	parentCtx context.Context,
) bool {
	if resp == nil || resp.Error == nil {
		return false
	}
	return modelCallBudgetPrefinalTimedOut(prefinalCtx, parentCtx) &&
		strings.Contains(
			strings.ToLower(resp.Error.Message),
			context.DeadlineExceeded.Error(),
		)
}

func modelCallBudgetTimeoutResponse(
	resp *model.Response,
	prefinalCtx context.Context,
	parentCtx context.Context,
	budget *modelCallBudget,
) bool {
	if modelCallBudgetPrefinalTimeoutResponse(
		resp,
		prefinalCtx,
		parentCtx,
	) {
		return true
	}
	if budget == nil || budget.deadlineWindow <= 0 ||
		parentCtx == nil || parentCtx.Err() != nil {
		return false
	}
	if _, ok := parentCtx.Deadline(); !ok {
		return false
	}
	return isModelTimeoutResponse(resp)
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
	budget := modelCallBudgetFromContext(ctx)
	finalize, err := budget.use(ctx)
	if err != nil {
		return nil, err
	}
	if finalize {
		req = applyFinalModelCallRequest(req, budget.finalConfig())
		return m.model.GenerateContent(ctx, req)
	}
	if budget == nil {
		return m.model.GenerateContent(ctx, req)
	}
	prefinalCtx, cancel, ok := modelCallBudgetPrefinalContext(
		ctx,
		budget.deadlineWindow,
	)
	if !ok {
		return m.model.GenerateContent(ctx, req)
	}
	ch, err := m.model.GenerateContent(prefinalCtx, req)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			req = applyFinalModelCallRequest(req, budget.finalConfig())
			return m.model.GenerateContent(withoutModelCallBudget(ctx), req)
		}
		return nil, err
	}
	out := make(chan *model.Response, 1)
	go func() {
		defer close(out)
		defer cancel()
		timedOut := false
		for resp := range ch {
			if modelCallBudgetTimeoutResponse(
				resp,
				prefinalCtx,
				ctx,
				budget,
			) {
				timedOut = true
				continue
			}
			select {
			case out <- resp:
			case <-ctx.Done():
				return
			}
		}
		if modelCallBudgetPrefinalTimedOut(prefinalCtx, ctx) {
			timedOut = true
		}
		if !timedOut || ctx.Err() != nil {
			return
		}
		req = applyFinalModelCallRequest(req, budget.finalConfig())
		finalCh, finalErr := m.model.GenerateContent(
			withoutModelCallBudget(ctx),
			req,
		)
		if finalErr != nil {
			modelCallBudgetSendResponse(ctx, out, &model.Response{
				Error: model.ResponseErrorFromError(
					finalErr,
					model.ErrorTypeFlowError,
				),
				Done:      true,
				Timestamp: time.Now(),
			})
			return
		}
		for resp := range finalCh {
			if !modelCallBudgetSendResponse(ctx, out, resp) {
				return
			}
		}
	}()
	return out, nil
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
	budget := modelCallBudgetFromContext(ctx)
	finalize, err := budget.use(ctx)
	if err != nil {
		return nil, err
	}
	if finalize {
		req = applyFinalModelCallRequest(req, budget.finalConfig())
		return m.iter.GenerateContentIter(ctx, req)
	}
	if budget == nil {
		return m.iter.GenerateContentIter(ctx, req)
	}
	prefinalCtx, cancel, ok := modelCallBudgetPrefinalContext(
		ctx,
		budget.deadlineWindow,
	)
	if !ok {
		return m.iter.GenerateContentIter(ctx, req)
	}
	seq, err := m.iter.GenerateContentIter(prefinalCtx, req)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			req = applyFinalModelCallRequest(req, budget.finalConfig())
			return m.iter.GenerateContentIter(withoutModelCallBudget(ctx), req)
		}
		return nil, err
	}
	return func(yield func(*model.Response) bool) {
		defer cancel()
		timedOut := false
		seq(func(resp *model.Response) bool {
			if modelCallBudgetTimeoutResponse(
				resp,
				prefinalCtx,
				ctx,
				budget,
			) {
				timedOut = true
				return true
			}
			return yield(resp)
		})
		if modelCallBudgetPrefinalTimedOut(prefinalCtx, ctx) {
			timedOut = true
		}
		if !timedOut || ctx.Err() != nil {
			return
		}
		req = applyFinalModelCallRequest(req, budget.finalConfig())
		finalSeq, finalErr := m.iter.GenerateContentIter(
			withoutModelCallBudget(ctx),
			req,
		)
		if finalErr != nil {
			yield(&model.Response{
				Error: model.ResponseErrorFromError(
					finalErr,
					model.ErrorTypeFlowError,
				),
				Done:      true,
				Timestamp: time.Now(),
			})
			return
		}
		finalSeq(yield)
	}, nil
}

func modelCallBudgetSendResponse(
	ctx context.Context,
	ch chan<- *model.Response,
	resp *model.Response,
) bool {
	select {
	case ch <- resp:
		return true
	case <-ctx.Done():
		return false
	}
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
	deadlineWindow time.Duration,
	finalRequest ...modelCallBudgetFinalRequestConfig,
) []gateway.Option {
	if limit <= 0 && deadlineWindow <= 0 {
		return opts
	}
	return append(opts, gateway.WithRunOptionResolver(
		buildModelCallBudgetRunOptionResolver(
			limit,
			finalizeOnLast,
			deadlineWindow,
			modelCallBudgetFinalRequestArg(finalRequest),
		),
	))
}

func buildModelCallBudgetRunOptionResolver(
	limit int,
	finalizeOnLast bool,
	deadlineWindow time.Duration,
	finalRequest ...modelCallBudgetFinalRequestConfig,
) gateway.RunOptionResolver {
	config := modelCallBudgetFinalRequestArg(finalRequest)
	return func(ctx context.Context, _ gateway.RunOptionInput) (
		context.Context,
		[]agent.RunOption,
		error,
	) {
		return ctx, modelCallBudgetRunOptions(
			limit,
			finalizeOnLast,
			deadlineWindow,
			config,
		), nil
	}
}

func modelCallBudgetRunOptions(
	limit int,
	finalizeOnLast bool,
	deadlineWindow time.Duration,
	finalRequest ...modelCallBudgetFinalRequestConfig,
) []agent.RunOption {
	factory := newModelCallBudgetFactory(
		limit,
		finalizeOnLast,
		deadlineWindow,
		modelCallBudgetFinalRequestArg(finalRequest),
	)
	if factory == nil {
		return nil
	}
	return []agent.RunOption{
		agent.MergeRuntimeState(map[string]any{
			modelCallBudgetRuntimeStateKey: factory,
		}),
	}
}

func finalModelCallRequest(
	req *model.Request,
	config modelCallBudgetFinalRequestConfig,
) *model.Request {
	if req == nil {
		req = &model.Request{}
	}
	clone := *req
	clone.Tools = nil
	clone.Stream = false
	clone.ExtraFields = finalModelCallExtraFields(req.ExtraFields)
	if config.DisableThinking {
		clone.ThinkingEnabled = model.BoolPtr(false)
	}
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

func applyFinalModelCallRequest(
	req *model.Request,
	finalRequest ...modelCallBudgetFinalRequestConfig,
) *model.Request {
	finalReq := finalModelCallRequest(
		req,
		modelCallBudgetFinalRequestArg(finalRequest),
	)
	if req == nil {
		return finalReq
	}
	*req = *finalReq
	return req
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
