//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inprocess

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type progressAccumulator struct {
	progress Progress
}

func (a *progressAccumulator) consume(evt *event.Event, now time.Time) bool {
	if evt == nil {
		return false
	}
	a.progress.EventCount++
	lastEventAt := evt.Timestamp
	if lastEventAt.IsZero() {
		lastEventAt = now
	}
	a.progress.LastEventAt = cloneTime(lastEventAt)

	rsp := evt.Response
	if rsp == nil {
		return true
	}
	if !rsp.IsPartial {
		a.progress.ToolCallCount += len(rsp.GetToolCallIDs())
		a.progress.ToolResultCount += len(rsp.GetToolResultIDs())
		a.addUsage(rsp.Usage)
	}
	return true
}

func (a *progressAccumulator) snapshot() *Progress {
	if a == nil || a.progress.EventCount == 0 {
		return nil
	}
	return cloneProgress(&a.progress)
}

func (a *progressAccumulator) addUsage(usage *model.Usage) {
	if usage == nil {
		return
	}
	a.progress.PromptTokens += usage.PromptTokens
	a.progress.CompletionTokens += usage.CompletionTokens
	a.progress.TotalTokens += usage.TotalTokens
}
