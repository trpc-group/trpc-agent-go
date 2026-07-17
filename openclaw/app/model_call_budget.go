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
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
)

type modelCallBudgetKey struct{}

type modelCallBudgetBypassKey struct{}

const modelCallBudgetRuntimeStateKey = "openclaw.model_call_budget"

type modelCallBudgetFinalRequestConfig struct {
	DisableThinking      bool
	DropReasoningContent bool
	MaxInputTokens       int
	ApproxRunesPerToken  float64
}

type modelCallBudget struct {
	mu                   sync.Mutex
	limit                int
	count                int
	finalizeOnLast       bool
	deadlineWindow       time.Duration
	finalRequest         modelCallBudgetFinalRequestConfig
	lastEvidenceMessages []model.Message
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

func (b *modelCallBudget) reserveFinalCall() error {
	if b == nil || b.limit <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count >= b.limit {
		return agent.NewStopError(
			fmt.Sprintf("max LLM calls (%d) exceeded", b.limit),
		)
	}
	b.count++
	return nil
}

func (b *modelCallBudget) finalConfig() modelCallBudgetFinalRequestConfig {
	if b == nil {
		return modelCallBudgetFinalRequestConfig{}
	}
	return b.finalRequest
}

func (b *modelCallBudget) rememberRequest(req *model.Request) {
	if b == nil || req == nil || !modelCallBudgetHasToolEvidence(req) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(req.Messages) < len(b.lastEvidenceMessages) {
		return
	}
	b.lastEvidenceMessages = append([]model.Message(nil), req.Messages...)
}

func (b *modelCallBudget) applyFinalRequest(
	req *model.Request,
	nonStreaming bool,
) *model.Request {
	if b == nil || req == nil || modelCallBudgetHasToolEvidence(req) {
		return applyFinalModelCallRequest(
			req,
			nonStreaming,
			b.finalConfig(),
		)
	}
	b.mu.Lock()
	messages := append([]model.Message(nil), b.lastEvidenceMessages...)
	b.mu.Unlock()
	if len(messages) == 0 {
		return applyFinalModelCallRequest(
			req,
			nonStreaming,
			b.finalConfig(),
		)
	}
	clone := *req
	clone.Messages = messages
	return applyFinalModelCallRequest(
		&clone,
		nonStreaming,
		b.finalConfig(),
	)
}

func modelCallBudgetReserveFinalCall(
	ctx context.Context,
	budget *modelCallBudget,
) error {
	if err := budget.reserveFinalCall(); err != nil {
		return err
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil
	}
	return inv.IncLLMCallCount()
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
	return modelCallBudgetDeadlineSoon(parentCtx, budget.deadlineWindow) &&
		isModelTimeoutResponse(resp)
}

func modelCallBudgetPrepareFinalRetry(
	ctx context.Context,
	budget *modelCallBudget,
	req *model.Request,
	discarded *model.Response,
) (context.Context, *model.Request, *model.Response, error) {
	var err error
	if discarded != nil {
		ctx, err = llmflow.RunModelRetryAfterCallbacks(ctx, req, discarded)
		if err != nil {
			return ctx, nil, nil, err
		}
	}
	finalReq := budget.applyFinalRequest(req, true)
	ctx, customResp, err := llmflow.RunModelRetryBeforeCallbacks(ctx, finalReq)
	if err != nil {
		return ctx, nil, nil, err
	}
	return ctx, finalReq, customResp, nil
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
		nonStreaming := modelCallBudgetDeadlineSoon(
			ctx,
			budget.deadlineWindow,
		)
		req = budget.applyFinalRequest(req, nonStreaming)
		return m.model.GenerateContent(ctx, req)
	}
	if budget == nil {
		return m.model.GenerateContent(ctx, req)
	}
	budget.rememberRequest(req)
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
			if reserveErr := modelCallBudgetReserveFinalCall(
				ctx,
				budget,
			); reserveErr != nil {
				return nil, reserveErr
			}
			retryCtx, finalReq, customResp, retryErr :=
				modelCallBudgetPrepareFinalRetry(ctx, budget, req, nil)
			if retryErr != nil {
				return nil, retryErr
			}
			if customResp != nil {
				return modelCallBudgetResponseChannel(customResp), nil
			}
			return m.model.GenerateContent(
				withoutModelCallBudget(retryCtx),
				finalReq,
			)
		}
		return nil, err
	}
	out := make(chan *model.Response, 1)
	go func() {
		defer close(out)
		defer cancel()
		timedOut := false
		var discarded *model.Response
		for resp := range ch {
			if modelCallBudgetTimeoutResponse(
				resp,
				prefinalCtx,
				ctx,
				budget,
			) {
				timedOut = true
				discarded = resp
				continue
			}
			if !modelCallBudgetSendResponse(ctx, out, resp) {
				return
			}
		}
		if modelCallBudgetPrefinalTimedOut(prefinalCtx, ctx) {
			timedOut = true
		}
		if ctx.Err() != nil {
			return
		}
		if !timedOut {
			return
		}
		if reserveErr := modelCallBudgetReserveFinalCall(
			ctx,
			budget,
		); reserveErr != nil {
			modelCallBudgetSendResponse(
				ctx,
				out,
				modelCallBudgetErrorResponse(reserveErr),
			)
			return
		}
		retryCtx, finalReq, customResp, retryErr :=
			modelCallBudgetPrepareFinalRetry(ctx, budget, req, discarded)
		if retryErr != nil {
			modelCallBudgetSendResponse(
				ctx,
				out,
				modelCallBudgetErrorResponse(retryErr),
			)
			return
		}
		if customResp != nil {
			modelCallBudgetSendResponse(ctx, out, customResp)
			return
		}
		finalCh, finalErr := m.model.GenerateContent(
			withoutModelCallBudget(retryCtx),
			finalReq,
		)
		if finalErr != nil {
			modelCallBudgetSendResponse(
				ctx,
				out,
				modelCallBudgetErrorResponse(finalErr),
			)
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
		nonStreaming := modelCallBudgetDeadlineSoon(
			ctx,
			budget.deadlineWindow,
		)
		req = budget.applyFinalRequest(req, nonStreaming)
		return m.iter.GenerateContentIter(ctx, req)
	}
	if budget == nil {
		return m.iter.GenerateContentIter(ctx, req)
	}
	budget.rememberRequest(req)
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
			if reserveErr := modelCallBudgetReserveFinalCall(
				ctx,
				budget,
			); reserveErr != nil {
				return nil, reserveErr
			}
			retryCtx, finalReq, customResp, retryErr :=
				modelCallBudgetPrepareFinalRetry(ctx, budget, req, nil)
			if retryErr != nil {
				return nil, retryErr
			}
			if customResp != nil {
				return modelCallBudgetResponseSeq(customResp), nil
			}
			return m.iter.GenerateContentIter(
				withoutModelCallBudget(retryCtx),
				finalReq,
			)
		}
		return nil, err
	}
	return func(yield func(*model.Response) bool) {
		defer cancel()
		timedOut := false
		stopped := false
		var discarded *model.Response
		seq(func(resp *model.Response) bool {
			if modelCallBudgetTimeoutResponse(
				resp,
				prefinalCtx,
				ctx,
				budget,
			) {
				timedOut = true
				discarded = resp
				return true
			}
			if !yield(resp) {
				stopped = true
				return false
			}
			return true
		})
		if stopped {
			return
		}
		if modelCallBudgetPrefinalTimedOut(prefinalCtx, ctx) {
			timedOut = true
		}
		if ctx.Err() != nil {
			return
		}
		if !timedOut {
			return
		}
		if reserveErr := modelCallBudgetReserveFinalCall(
			ctx,
			budget,
		); reserveErr != nil {
			yield(modelCallBudgetErrorResponse(reserveErr))
			return
		}
		retryCtx, finalReq, customResp, retryErr :=
			modelCallBudgetPrepareFinalRetry(ctx, budget, req, discarded)
		if retryErr != nil {
			yield(modelCallBudgetErrorResponse(retryErr))
			return
		}
		if customResp != nil {
			yield(customResp)
			return
		}
		finalSeq, finalErr := m.iter.GenerateContentIter(
			withoutModelCallBudget(retryCtx),
			finalReq,
		)
		if finalErr != nil {
			yield(modelCallBudgetErrorResponse(finalErr))
			return
		}
		finalSeq(yield)
	}, nil
}

func modelCallBudgetResponseChannel(
	resp *model.Response,
) <-chan *model.Response {
	ch := make(chan *model.Response, 1)
	ch <- resp
	close(ch)
	return ch
}

func modelCallBudgetResponseSeq(resp *model.Response) model.Seq[*model.Response] {
	return func(yield func(*model.Response) bool) {
		yield(resp)
	}
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

func modelCallBudgetErrorResponse(err error) *model.Response {
	return &model.Response{
		Error: model.ResponseErrorFromError(
			err,
			model.ErrorTypeFlowError,
		),
		Done:      true,
		Timestamp: time.Now(),
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
	clone.ExtraFields = finalModelCallExtraFields(req.ExtraFields)
	if config.DisableThinking {
		clone.ThinkingEnabled = model.BoolPtr(false)
	}
	clone.Messages = finalModelCallMessages(req.Messages, config)
	clone.Messages = finalModelCallTrimMessages(clone.Messages, config)
	clone.Messages = append(clone.Messages, finalModelCallNoticeMessage(config))
	return &clone
}

func finalModelCallMessages(
	messages []model.Message,
	config modelCallBudgetFinalRequestConfig,
) []model.Message {
	clone := make([]model.Message, 0, len(messages))
	for _, msg := range messages {
		if finalModelCallDropSystemMessage(msg) {
			continue
		}
		clone = append(clone, msg)
	}
	if !config.DropReasoningContent {
		return clone
	}
	for i := range clone {
		clone[i].ReasoningContent = ""
		clone[i].ReasoningSignature = ""
	}
	return clone
}

const finalModelCallSkillOverviewPrefix = "Treat the skill overview " +
	"below as the skills available in this session."

func finalModelCallDropSystemMessage(msg model.Message) bool {
	if msg.Role != model.RoleSystem {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	return strings.HasPrefix(content, finalModelCallSkillOverviewPrefix)
}

const finalModelCallNotice = "[OpenClaw Budget Notice] This is the " +
	"final allowed model call for this run. No further tools are available " +
	"now. Use only the existing conversation and tool results, then " +
	"produce the final user-facing answer immediately. Do not emit tool " +
	"calls, function calls, JSON tool requests, XML-style tool markup such " +
	"as <tool_call>, code blocks that ask to run tools, or descriptions of " +
	"future tool use. Do not ask to continue. If the original task " +
	"requires a final-answer format, follow it exactly."

const (
	finalModelCallSystemBudgetDivisor = 16
	finalModelCallAnchorBudgetDivisor = 2
	finalModelCallMaxAnchorDivisor    = 8
	finalModelCallEvidenceDivisor     = 2
	finalModelCallMaxEvidenceDivisor  = 16
	finalModelCallEvidenceSnippetLen  = 48
	finalModelCallTruncationNotice    = "\n\n[...truncated...]\n\n"
	finalModelCallFormatSnippetLimit  = 700
)

func finalModelCallTrimMessages(
	messages []model.Message,
	config modelCallBudgetFinalRequestConfig,
) []model.Message {
	maxInputTokens := config.MaxInputTokens
	if maxInputTokens <= 0 || len(messages) == 0 {
		return messages
	}
	ctx := context.Background()
	counter := finalModelCallTokenCounter(config)
	budget := finalModelCallTrimBudget(ctx, counter, maxInputTokens)
	if budget <= 0 {
		return nil
	}
	fallback := finalModelCallTailEvidenceMessages(
		ctx,
		counter,
		messages,
		budget,
	)
	trimmed, err := model.NewMiddleOutStrategy(counter).TailorMessages(
		ctx,
		messages,
		budget,
	)
	if err == nil && finalModelCallFits(ctx, counter, trimmed, budget) {
		if finalModelCallPreferTailEvidence(messages, trimmed, fallback) {
			return fallback
		}
		return trimmed
	}
	if finalModelCallFits(ctx, counter, trimmed, budget) {
		if finalModelCallPreferTailEvidence(messages, trimmed, fallback) {
			return fallback
		}
		return trimmed
	}
	if len(fallback) > 0 {
		return fallback
	}
	if len(trimmed) > 0 {
		return trimmed
	}
	return messages
}

func finalModelCallPreferTailEvidence(
	messages []model.Message,
	trimmed []model.Message,
	fallback []model.Message,
) bool {
	if !finalModelCallContainsRecentTailEvidence(messages, fallback) {
		return false
	}
	return !finalModelCallContainsRecentTailEvidence(messages, trimmed)
}

func finalModelCallContainsRecentTailEvidence(
	messages []model.Message,
	candidate []model.Message,
) bool {
	snippet := finalModelCallRecentTailEvidenceSnippet(messages)
	if snippet == "" {
		return false
	}
	for _, msg := range candidate {
		if strings.Contains(msg.Content, snippet) {
			return true
		}
	}
	return false
}

func finalModelCallRecentTailEvidenceSnippet(
	messages []model.Message,
) string {
	headCount := finalModelCallSystemPrefixLen(messages)
	anchor := finalModelCallAnchorUserIndex(messages, headCount)
	if anchor < 0 {
		return ""
	}
	transcript := finalModelCallTranscriptMessages(messages)
	for i := len(transcript) - 1; i > anchor; i-- {
		content := strings.TrimSpace(transcript[i].Content)
		if content != "" {
			return finalModelCallEvidenceSnippet(content)
		}
	}
	return ""
}

func finalModelCallEvidenceSnippet(content string) string {
	runes := []rune(content)
	if len(runes) <= finalModelCallEvidenceSnippetLen {
		return content
	}
	return string(runes[:finalModelCallEvidenceSnippetLen])
}

func finalModelCallTokenCounter(
	config modelCallBudgetFinalRequestConfig,
) model.TokenCounter {
	counterOpts := []model.SimpleTokenCounterOption(nil)
	if config.ApproxRunesPerToken > 0 {
		counterOpts = append(
			counterOpts,
			model.WithApproxRunesPerToken(config.ApproxRunesPerToken),
		)
	}
	return model.NewSimpleTokenCounter(counterOpts...)
}

func finalModelCallTrimBudget(
	ctx context.Context,
	counter model.TokenCounter,
	maxInputTokens int,
) int {
	if maxInputTokens <= 0 {
		return maxInputTokens
	}
	notice := finalModelCallNoticeMessageWithCounter(
		ctx,
		counter,
		maxInputTokens,
	)
	noticeTokens, err := counter.CountTokens(ctx, notice)
	if err != nil || noticeTokens <= 0 {
		return maxInputTokens
	}
	budget := maxInputTokens - noticeTokens
	if budget <= 0 {
		return 0
	}
	return budget
}

func finalModelCallNoticeMessage(
	config modelCallBudgetFinalRequestConfig,
) model.Message {
	return finalModelCallNoticeMessageWithCounter(
		context.Background(),
		finalModelCallTokenCounter(config),
		config.MaxInputTokens,
	)
}

func finalModelCallNoticeMessageWithCounter(
	ctx context.Context,
	counter model.TokenCounter,
	maxTokens int,
) model.Message {
	full := model.NewUserMessage(finalModelCallNotice)
	if maxTokens <= 0 {
		return full
	}
	if tokens, err := counter.CountTokens(ctx, full); err == nil &&
		tokens <= maxTokens {
		return full
	}
	runes := []rune(finalModelCallNotice)
	low, high, best := 0, len(runes), 0
	for low <= high {
		mid := low + (high-low)/2
		candidate := model.NewUserMessage(string(runes[:mid]))
		tokens, err := counter.CountTokens(ctx, candidate)
		if err != nil || tokens > maxTokens {
			high = mid - 1
			continue
		}
		best = mid
		low = mid + 1
	}
	return model.NewUserMessage(string(runes[:best]))
}

func finalModelCallFits(
	ctx context.Context,
	counter model.TokenCounter,
	messages []model.Message,
	maxTokens int,
) bool {
	if len(messages) == 0 || maxTokens <= 0 {
		return false
	}
	tokens, err := counter.CountTokensRange(ctx, messages, 0, len(messages))
	return err == nil && tokens <= maxTokens
}

func finalModelCallTailEvidenceMessages(
	ctx context.Context,
	counter model.TokenCounter,
	messages []model.Message,
	maxTokens int,
) []model.Message {
	headCount := finalModelCallSystemPrefixLen(messages)
	anchor := finalModelCallAnchorUserIndex(messages, headCount)
	if anchor < 0 {
		return nil
	}
	transcript := finalModelCallTranscriptMessages(messages)
	prefix := finalModelCallProtectedPrefix(
		ctx,
		counter,
		transcript[:headCount],
		transcript[anchor],
		maxTokens,
	)
	best := finalModelCallTailEvidenceWithPrefix(
		ctx,
		counter,
		transcript,
		prefix,
		anchor,
		maxTokens,
	)
	if len(best) > len(prefix) {
		return best
	}
	for divisor := finalModelCallAnchorBudgetDivisor; divisor <= finalModelCallMaxAnchorDivisor; divisor *= 2 {
		compactPrefix := finalModelCallProtectedPrefixWithAnchorDivisor(
			ctx,
			counter,
			transcript[:headCount],
			transcript[anchor],
			maxTokens,
			divisor,
		)
		compactBest := finalModelCallTailEvidenceWithPrefix(
			ctx,
			counter,
			transcript,
			compactPrefix,
			anchor,
			maxTokens,
		)
		if len(compactBest) > len(compactPrefix) {
			return compactBest
		}
		compactEvidence := finalModelCallCompactTailEvidenceWithPrefix(
			ctx,
			counter,
			transcript,
			compactPrefix,
			anchor,
			maxTokens,
		)
		if len(compactEvidence) > len(compactPrefix) {
			return compactEvidence
		}
	}
	return best
}

func finalModelCallTailEvidenceWithPrefix(
	ctx context.Context,
	counter model.TokenCounter,
	transcript []model.Message,
	prefix []model.Message,
	anchor int,
	maxTokens int,
) []model.Message {
	best := prefix
	if !finalModelCallFits(ctx, counter, best, maxTokens) {
		return best
	}
	for start := len(transcript) - 1; start > anchor; start-- {
		suffix := finalModelCallNormalizeTail(transcript[start:])
		if len(suffix) == 0 {
			continue
		}
		candidate := make([]model.Message, 0, len(prefix)+len(suffix))
		candidate = append(candidate, prefix...)
		candidate = append(candidate, suffix...)
		if finalModelCallFits(ctx, counter, candidate, maxTokens) {
			best = candidate
			continue
		}
		if len(best) > len(prefix) {
			break
		}
	}
	return best
}

func finalModelCallCompactTailEvidenceWithPrefix(
	ctx context.Context,
	counter model.TokenCounter,
	transcript []model.Message,
	prefix []model.Message,
	anchor int,
	maxTokens int,
) []model.Message {
	for start := len(transcript) - 1; start > anchor; start-- {
		suffix := finalModelCallNormalizeTail(transcript[start:])
		if len(suffix) == 0 {
			continue
		}
		for divisor := finalModelCallEvidenceDivisor; divisor <= finalModelCallMaxEvidenceDivisor; divisor *= 2 {
			compactSuffix := finalModelCallCompactMessages(
				suffix,
				finalModelCallPartRuneLimit(maxTokens, divisor),
			)
			candidate := make([]model.Message, 0,
				len(prefix)+len(compactSuffix))
			candidate = append(candidate, prefix...)
			candidate = append(candidate, compactSuffix...)
			if finalModelCallFits(ctx, counter, candidate, maxTokens) {
				return candidate
			}
		}
	}
	return nil
}

func finalModelCallCompactMessages(
	messages []model.Message,
	contentLimit int,
) []model.Message {
	compact := make([]model.Message, 0, len(messages))
	for _, msg := range messages {
		msg.Content = finalModelCallCompactEvidenceContent(
			msg.Content,
			contentLimit,
		)
		compact = append(compact, msg)
	}
	return compact
}

func finalModelCallCompactEvidenceContent(content string, limit int) string {
	const toolResultPrefix = "[Tool result: "
	if strings.HasPrefix(content, toolResultPrefix) {
		if index := strings.Index(content, "]\n"); index >= 0 {
			payload := strings.TrimSpace(content[index+2:])
			if payload != "" {
				return finalModelCallTrimContent(payload, limit)
			}
		}
	}
	return finalModelCallTrimContent(content, limit)
}

func finalModelCallTranscriptMessages(
	messages []model.Message,
) []model.Message {
	transcript := make([]model.Message, len(messages))
	for i, msg := range messages {
		msg.ToolCalls = nil
		if msg.Role == model.RoleTool {
			msg.Role = model.RoleUser
			msg.Content = finalModelCallToolResultText(msg)
			msg.ToolID = ""
			msg.ToolName = ""
		}
		transcript[i] = msg
	}
	return transcript
}

func finalModelCallToolResultText(msg model.Message) string {
	name := strings.TrimSpace(msg.ToolName)
	if name == "" {
		name = "tool"
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return "[Tool result: " + name + "]"
	}
	return "[Tool result: " + name + "]\n" + content
}

func finalModelCallSystemPrefixLen(messages []model.Message) int {
	count := 0
	for _, msg := range messages {
		if msg.Role != model.RoleSystem {
			break
		}
		count++
	}
	return count
}

func finalModelCallAnchorUserIndex(
	messages []model.Message,
	start int,
) int {
	if start < 0 {
		start = 0
	}
	for i := len(messages) - 1; i >= start; i-- {
		if messages[i].Role == model.RoleUser {
			return i
		}
	}
	for i := len(messages) - 1; i >= start; i-- {
		if messages[i].Role != model.RoleSystem {
			return i
		}
	}
	return -1
}

func finalModelCallProtectedPrefix(
	ctx context.Context,
	counter model.TokenCounter,
	system []model.Message,
	anchor model.Message,
	maxTokens int,
) []model.Message {
	return finalModelCallProtectedPrefixWithAnchorDivisor(
		ctx,
		counter,
		system,
		anchor,
		maxTokens,
		1,
	)
}

func finalModelCallProtectedPrefixWithAnchorDivisor(
	ctx context.Context,
	counter model.TokenCounter,
	system []model.Message,
	anchor model.Message,
	maxTokens int,
	anchorDivisor int,
) []model.Message {
	prefix := make([]model.Message, 0, len(system)+1)
	systemLimit := finalModelCallPartRuneLimit(
		maxTokens,
		finalModelCallSystemBudgetDivisor*max(len(system), 1),
	)
	for _, msg := range system {
		msg.Content = finalModelCallTrimContent(msg.Content, systemLimit)
		prefix = append(prefix, msg)
	}
	anchorLimit := finalModelCallPartRuneLimit(
		maxTokens,
		anchorDivisor,
	)
	anchor.Content = finalModelCallTrimContent(anchor.Content, anchorLimit)
	prefix = append(prefix, anchor)
	if finalModelCallFits(ctx, counter, prefix, maxTokens) {
		return prefix
	}
	for i := range prefix {
		prefix[i].Content = finalModelCallTrimContent(
			prefix[i].Content,
			finalModelCallPartRuneLimit(maxTokens, len(prefix)*2),
		)
	}
	return prefix
}

func finalModelCallPartRuneLimit(maxTokens, divisor int) int {
	if maxTokens <= 0 {
		return 0
	}
	if divisor <= 0 {
		return maxTokens
	}
	limit := maxTokens / divisor
	if limit < 1 {
		return 1
	}
	return limit
}

func finalModelCallTrimContent(content string, limit int) string {
	trimmed := finalModelCallTrimContentPlain(content, limit)
	snippet := finalModelCallAnswerFormatSnippet(content)
	if snippet == "" || strings.Contains(trimmed, snippet) {
		return trimmed
	}
	block := "\n\n" + snippet
	blockRunes := []rune(block)
	if len(blockRunes) >= limit {
		return string(blockRunes[:limit])
	}
	body := finalModelCallTrimContentPlain(
		content,
		limit-len(blockRunes),
	)
	return body + block
}

func finalModelCallTrimContentPlain(content string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	notice := []rune(finalModelCallTruncationNotice)
	if limit <= len(notice)+2 {
		return string(runes[:limit])
	}
	bodyLimit := limit - len(notice)
	head := bodyLimit / 2
	tail := bodyLimit - head
	return string(runes[:head]) +
		finalModelCallTruncationNotice +
		string(runes[len(runes)-tail:])
}

func finalModelCallAnswerFormatSnippet(content string) string {
	lower := strings.ToLower(content)
	index := finalModelCallAnswerFormatIndex(lower)
	if index < 0 {
		return ""
	}
	start := finalModelCallParagraphStart(content, index)
	end := finalModelCallParagraphEnd(content, index)
	snippet := strings.TrimSpace(content[start:end])
	if snippet == "" {
		return ""
	}
	return finalModelCallLimitSnippet(
		snippet,
		index-start,
		finalModelCallFormatSnippetLimit,
	)
}

func finalModelCallAnswerFormatIndex(lower string) int {
	markers := []string{
		"final answer:",
		"answer-format instruction",
		"answer format instruction",
	}
	best := -1
	for _, marker := range markers {
		index := strings.Index(lower, marker)
		if index < 0 {
			continue
		}
		if best < 0 || index < best {
			best = index
		}
	}
	return best
}

func finalModelCallParagraphStart(content string, index int) int {
	if index <= 0 {
		return 0
	}
	start := strings.LastIndex(content[:index], "\n\n")
	if start >= 0 {
		return start + len("\n\n")
	}
	start = strings.LastIndex(content[:index], "\n")
	if start >= 0 {
		return start + len("\n")
	}
	return 0
}

func finalModelCallParagraphEnd(content string, index int) int {
	if index >= len(content) {
		return len(content)
	}
	end := strings.Index(content[index:], "\n\n")
	if end >= 0 {
		return index + end
	}
	end = strings.Index(content[index:], "\n")
	if end >= 0 {
		return index + end
	}
	return len(content)
}

func finalModelCallLimitSnippet(
	snippet string,
	index int,
	limit int,
) string {
	runes := []rune(snippet)
	if limit <= 0 || len(runes) <= limit {
		return snippet
	}
	if index < 0 {
		index = 0
	}
	if index > len(snippet) {
		index = len(snippet)
	}
	marker := utf8.RuneCountInString(snippet[:index])
	if marker > len(runes) {
		marker = len(runes)
	}
	head := limit / 2
	start := marker - head
	if start < 0 {
		start = 0
	}
	if start+limit > len(runes) {
		start = len(runes) - limit
	}
	if start < 0 {
		start = 0
	}
	return string(runes[start : start+limit])
}

func finalModelCallNormalizeTail(
	messages []model.Message,
) []model.Message {
	for len(messages) > 0 && messages[0].Role == model.RoleTool {
		messages = messages[1:]
	}
	return messages
}

func applyFinalModelCallRequest(
	req *model.Request,
	nonStreaming bool,
	finalRequest ...modelCallBudgetFinalRequestConfig,
) *model.Request {
	finalReq := finalModelCallRequest(
		req,
		modelCallBudgetFinalRequestArg(finalRequest),
	)
	if nonStreaming {
		finalReq.Stream = false
	}
	if req == nil {
		return finalReq
	}
	*req = *finalReq
	return req
}

func modelCallBudgetHasToolEvidence(req *model.Request) bool {
	if req == nil {
		return false
	}
	for _, msg := range req.Messages {
		if msg.Role == model.RoleTool ||
			strings.TrimSpace(msg.ToolID) != "" ||
			len(msg.ToolCalls) > 0 {
			return true
		}
	}
	return false
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
