//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/calllimit"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var errQueuedInput = errors.New("queued user input requires a new model request")

type modelCycleKey struct{}
type modelCycle struct {
	control    *steer.Cycle
	cancel     context.CancelFunc
	modelCtx   context.Context
	runCtx     context.Context
	responseID string
	cleanup    []func()
}

// lifetimeContext preserves callback values without inheriting cancellation
// from the model request. WithoutCancel also hides its context.Cause value.
type lifetimeContext struct {
	context.Context
	values context.Context
}

func (c lifetimeContext) Value(key any) any {
	if value := c.values.Value(key); value != nil {
		return value
	}
	return c.Context.Value(key)
}
func contextWithRunLifetime(lifetime, values context.Context) context.Context {
	return lifetimeContext{Context: lifetime, values: context.WithoutCancel(values)}
}

func (f *Flow) emitSteeringError(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event, err error) {
	if !steer.Enabled(inv) || ctx.Err() != nil {
		return
	}
	_ = agent.EmitEvent(ctx, inv, out, event.NewErrorEvent(inv.InvocationID, inv.AgentName, model.ErrorTypeFlowError, err.Error()))
}

func shouldContinueSteeredRun(ctx context.Context, inv *agent.Invocation, err error) bool {
	return errors.Is(err, errQueuedInput) && ctx.Err() == nil && !inv.EndInvocation
}

func shouldFinishSteeredRun(ctx context.Context, inv *agent.Invocation, last *event.Event) bool {
	if inv.EndInvocation || ctx.Err() != nil || (last != nil && last.Error != nil) {
		return true
	}
	return steer.TryFinish(inv)
}

func beginModelCycle(ctx context.Context, inv *agent.Invocation) (context.Context, *modelCycle, error) {
	if !steer.Enabled(inv) {
		return ctx, nil, nil
	}
	if err := steer.Err(inv); err != nil {
		return ctx, nil, err
	}
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	modelCtx, cancel := context.WithCancel(ctx)
	control, ready := steer.BeginModel(inv, cancel)
	if !ready {
		cancel()
		return ctx, nil, errQueuedInput
	}
	cycle := &modelCycle{control: control, modelCtx: modelCtx, runCtx: ctx, cancel: cancel}
	return context.WithValue(modelCtx, modelCycleKey{}, cycle), cycle, nil
}

func finishModelCycle(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event, cycle *modelCycle, err *error) {
	if cycle == nil {
		return
	}
	cycle.control.End()
	cycle.cancel()
	for _, cleanup := range cycle.cleanup {
		cleanup()
	}
	if failure := steer.Err(inv); failure != nil {
		*err = failure
		return
	}
	_, stopped := agent.AsStopError(*err)
	if stopped || !cycle.control.Interrupted() || ctx.Err() != nil || inv.EndInvocation || calllimit.Active(inv) {
		return
	}
	if *err != nil && !errors.Is(*err, context.Canceled) {
		return
	}
	marker := event.NewResponseEvent(inv.InvocationID, inv.AgentName, &model.Response{ID: cycle.responseID, Object: model.ObjectTypeModelInterrupted})
	if emitErr := steer.EmitAndWait(ctx, inv, out, marker, flowEventWaitTimeout(ctx)); emitErr != nil {
		*err = emitErr
	} else {
		*err = errQueuedInput
	}
}

func (p *streamingResponseProcessor) stopForInterruptedResponse(response *model.Response) bool {
	if p.cycle == nil {
		return false
	}
	if response != nil {
		p.cycle.responseID = response.ID
	}
	if !p.cycle.control.Interrupted() {
		return false
	}
	if response != nil && response.Error != nil && !p.isSteeringCancellation(response.Error) {
		*p.err = fmt.Errorf("model response: %s", response.Error.Message)
	}
	return true
}

func (p *streamingResponseProcessor) acceptModelResponse(response *model.Response) bool {
	if p.cycle == nil || response == nil || response.IsPartial {
		return true
	}
	// Metadata and usage-only updates must not close the interruption window.
	if !response.Done && response.Error == nil && !response.IsToolCallResponse() {
		return true
	}
	return p.cycle.control.Accept()
}

func (f *Flow) callSteeredLLM(ctx context.Context, inv *agent.Invocation, req *model.Request, provider model.Model) (context.Context, model.Seq[*model.Response], bool, *modelCycle, error) {
	ctx, cycle, err := beginModelCycle(ctx, inv)
	if err != nil {
		return ctx, nil, false, cycle, err
	}
	ctx, seq, called, err := f.callLLM(ctx, inv, req, provider)
	return ctx, seq, called, cycle, err
}

func responseContext(runCtx, modelCtx context.Context, cycle *modelCycle) context.Context {
	if cycle == nil {
		return modelCtx
	}
	cycle.modelCtx = modelCtx
	return context.WithValue(contextWithRunLifetime(runCtx, modelCtx), modelCycleKey{}, cycle)
}

// Only a provider cancellation associated with our cancelled model context can
// be suppressed. An unrelated error mentioning cancellation remains visible.
func (p *streamingResponseProcessor) isSteeringCancellation(err *model.ResponseError) bool {
	if p.cycle.modelCtx.Err() != context.Canceled {
		return false
	}
	if err.Type == model.ErrorTypeCancelled {
		return !strings.Contains(err.Message, context.DeadlineExceeded.Error())
	}
	if err.Type != model.ErrorTypeStreamError && err.Type != model.ErrorTypeAPIError {
		return false
	}
	return err.Message == context.Canceled.Error() || strings.HasSuffix(err.Message, ": "+context.Canceled.Error())
}
