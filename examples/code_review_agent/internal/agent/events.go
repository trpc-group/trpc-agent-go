//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"fmt"
	"time"

	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	agentmodel "trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	reviewEventInputLoaded   = "cr_agent.input_loaded"
	reviewEventSkillRun      = "cr_agent.skill_run"
	reviewEventSandboxRun    = "cr_agent.sandbox_run"
	reviewEventModelReview   = "cr_agent.model_review"
	reviewEventReportWritten = "cr_agent.report_written"
	reviewEventTaskFinished  = "cr_agent.task_finished"
	reviewEventTaskFailed    = "cr_agent.task_failed"
)

type reviewEventSinkContextKey struct{}

type reviewEventSink func(context.Context, *agentevent.Event)

func withReviewEventSink(ctx context.Context, sink reviewEventSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, reviewEventSinkContextKey{}, sink)
}

func reviewEventSinkFromContext(ctx context.Context) reviewEventSink {
	sink, _ := ctx.Value(reviewEventSinkContextKey{}).(reviewEventSink)
	return sink
}

func (a *Agent) emitReviewEvent(ctx context.Context, taskID, object, content string) {
	if a == nil {
		return
	}
	a.emitEvent(ctx, reviewEvent(taskID, object, content))
}

func (a *Agent) emitReviewResultEvent(ctx context.Context, result review.Result) {
	if a == nil {
		return
	}
	ev := reviewEvent(result.TaskID, reviewEventTaskFinished, result.Conclusion.Status)
	ev.StructuredOutput = result
	a.emitEvent(ctx, ev)
}

func (a *Agent) emitEvent(ctx context.Context, ev *agentevent.Event) {
	if ev == nil {
		return
	}
	if a.cfg.EventSink != nil {
		a.cfg.EventSink(ctx, ev)
	}
	if sink := reviewEventSinkFromContext(ctx); sink != nil {
		sink(ctx, ev.Clone())
	}
}

func reviewEvent(taskID, object, content string) *agentevent.Event {
	now := time.Now()
	return &agentevent.Event{
		Response: &agentmodel.Response{
			Object:  object,
			Created: now.Unix(),
			Model:   "cr-agent",
			Choices: []agentmodel.Choice{{
				Index: 0,
				Message: agentmodel.Message{
					Role:    agentmodel.RoleAssistant,
					Content: content,
				},
			}},
			Done: true,
		},
		InvocationID: taskID,
		Author:       "cr-agent",
		ID:           fmt.Sprintf("%s:%s:%d", taskID, object, now.UnixNano()),
		Timestamp:    now,
	}
}
