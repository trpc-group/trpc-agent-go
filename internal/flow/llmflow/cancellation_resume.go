//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/tooljournal"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const resumeSourceKey = "__resume_source_event__"

func preparePendingToolCalls(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
	if inv == nil || !inv.RunOptions.Resume || inv.Session == nil {
		return nil, nil
	}
	records, err := tooljournal.Index(inv.Session)
	if err != nil {
		return nil, err
	}
	inv.Session.EventMu.RLock()
	events := append([]event.Event(nil), inv.Session.Events...)
	inv.Session.EventMu.RUnlock()
	var tailRequest string
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Response == nil || e.IsPartial || !e.IsValidContent() {
			continue
		}
		if tailRequest == "" {
			tailRequest = e.RequestID
		} else if e.RequestID != "" && e.RequestID != tailRequest {
			return nil, nil
		}
		if e.IsToolCallResponse() {
			if record, ok := records[e.ID]; ok {
				return tooljournal.PrepareResume(ctx, inv, e, record)
			}
			if inv.RunOptions.StrictToolResume {
				return nil, errors.New("cannot resume legacy tool calls without execution evidence")
			}
			// Preserve the original last-event boundary for unrecorded sessions.
			if i != len(events)-1 {
				return nil, nil
			}
			inv.SetState(resumeSourceKey, e.ID)
			return pendingToolCallResponse(inv), nil
		}
		if e.IsToolResultResponse() || e.IsError() {
			continue
		}
		return nil, nil
	}
	return nil, nil
}
