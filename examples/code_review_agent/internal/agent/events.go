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

func (a *Agent) emitReviewEvent(ctx context.Context, taskID, object, content string) {
	if a == nil || a.cfg.EventSink == nil {
		return
	}
	a.cfg.EventSink(ctx, reviewEvent(taskID, object, content))
}

func (a *Agent) emitReviewResultEvent(ctx context.Context, result review.Result) {
	if a == nil || a.cfg.EventSink == nil {
		return
	}
	ev := reviewEvent(result.TaskID, reviewEventTaskFinished, result.Conclusion.Status)
	ev.StructuredOutput = result
	a.cfg.EventSink(ctx, ev)
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
