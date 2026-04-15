//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"fmt"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

const toolExecutionActivityType = "tool.execution"

type toolExecutionTranslator struct {
	inner   translator.Translator
	started map[string]bool
}

func newTranslator(ctx context.Context, input *adapter.RunAgentInput, opts ...translator.Option) (translator.Translator, error) {
	inner, err := translator.New(ctx, input.ThreadID, input.RunID, opts...)
	if err != nil {
		return nil, fmt.Errorf("create inner translator: %w", err)
	}
	return &toolExecutionTranslator{
		inner:   inner,
		started: make(map[string]bool),
	}, nil
}

func (t *toolExecutionTranslator) Translate(ctx context.Context, evt *event.Event) ([]aguievents.Event, error) {
	innerEvents, err := t.inner.Translate(ctx, evt)
	if err != nil {
		return nil, err
	}
	if evt == nil || evt.Response == nil || !evt.Response.IsToolResultResponse() {
		return innerEvents, nil
	}
	return t.rewriteInnerEvents(innerEvents, evt.Response.IsPartial), nil
}

func (t *toolExecutionTranslator) rewriteInnerEvents(events []aguievents.Event, isPartial bool) []aguievents.Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]aguievents.Event, 0, len(events)+1)
	for _, evt := range events {
		toolResultEvent, ok := evt.(*aguievents.ToolCallResultEvent)
		if !ok {
			out = append(out, evt)
			continue
		}
		rewritten, ok := t.rewriteToolResultEvent(toolResultEvent, isPartial)
		if !ok {
			out = append(out, evt)
			continue
		}
		out = append(out, rewritten...)
	}
	return out
}

func (t *toolExecutionTranslator) rewriteToolResultEvent(
	toolResultEvent *aguievents.ToolCallResultEvent,
	isPartial bool,
) ([]aguievents.Event, bool) {
	if toolResultEvent == nil {
		return nil, false
	}
	if isPartial {
		var update countProgressUpdate
		if err := json.Unmarshal([]byte(toolResultEvent.Content), &update); err != nil {
			return nil, false
		}
		return []aguievents.Event{
			t.partialActivityEvent(toolResultEvent.ToolCallID, update),
		}, true
	}
	if !t.started[toolResultEvent.ToolCallID] {
		return nil, false
	}
	var result countProgressResult
	if err := json.Unmarshal([]byte(toolResultEvent.Content), &result); err != nil {
		return nil, false
	}
	delete(t.started, toolResultEvent.ToolCallID)
	return []aguievents.Event{
		t.finalActivityEvent(toolResultEvent.ToolCallID, result),
		toolResultEvent,
	}, true
}

func (t *toolExecutionTranslator) partialActivityEvent(toolCallID string, update countProgressUpdate) aguievents.Event {
	activityID := activityMessageID(toolCallID)
	progress := calculateActivityProgress(update.Current, update.Total)
	if !t.started[toolCallID] {
		t.started[toolCallID] = true
		return aguievents.NewActivitySnapshotEvent(activityID, toolExecutionActivityType, map[string]any{
			"toolCallId": toolCallID,
			"current":    update.Current,
			"total":      update.Total,
			"progress":   progress,
		})
	}
	return aguievents.NewActivityDeltaEvent(activityID, toolExecutionActivityType, []aguievents.JSONPatchOperation{
		{Op: "replace", Path: "/current", Value: update.Current},
		{Op: "replace", Path: "/total", Value: update.Total},
		{Op: "replace", Path: "/progress", Value: progress},
	})
}

func (t *toolExecutionTranslator) finalActivityEvent(toolCallID string, result countProgressResult) aguievents.Event {
	return aguievents.NewActivityDeltaEvent(activityMessageID(toolCallID), toolExecutionActivityType, []aguievents.JSONPatchOperation{
		{Op: "replace", Path: "/current", Value: result.Completed},
		{Op: "replace", Path: "/total", Value: result.Total},
		{Op: "replace", Path: "/progress", Value: 100},
		{Op: "add", Path: "/result", Value: result},
	})
}

func activityMessageID(toolCallID string) string {
	return "tool-activity-" + toolCallID
}

func calculateActivityProgress(current, total int) float64 {
	if total <= 0 {
		return 100
	}
	return float64(current) / float64(total) * 100
}

func (t *toolExecutionTranslator) PostRunFinalizationEvents(ctx context.Context) ([]aguievents.Event, error) {
	finalizer, ok := t.inner.(translator.PostRunFinalizingTranslator)
	if !ok {
		return nil, nil
	}
	return finalizer.PostRunFinalizationEvents(ctx)
}
