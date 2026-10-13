//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func (e *eventEmitter) attachProgress(evt *event.Event, percent float64, message string) {
	inv := e.invocation
	if inv == nil && e.ctx != nil {
		inv, _ = agent.InvocationFromContext(e.ctx)
	}
	if inv == nil || !inv.RunOptions.ProgressEventsEnabled {
		return
	}
	e.progressOnce.Do(func() { e.progressID = uuid.NewString() })
	_ = event.SetExtension(evt, event.ProgressExtensionKey, event.Progress{
		OperationID: e.progressID,
		Kind:        "graph_node",
		Status:      event.ProgressStatusRunning,
		Percent:     &percent,
		Message:     message,
	})
}
