//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package translator

import (
	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/progressevent"
)

func (t *translator) progressEvents(evt *event.Event) []aguievents.Event {
	progress, ok := progressevent.Get(evt)
	if !ok {
		return nil
	}
	// Correlation is included even when optional raw source metadata is disabled.
	payload := struct {
		*event.Progress
		EventID            string `json:"event_id"`
		InvocationID       string `json:"invocation_id"`
		ParentInvocationID string `json:"parent_invocation_id,omitempty"`
	}{progress, evt.ID, evt.InvocationID, evt.ParentInvocationID}
	return []aguievents.Event{aguievents.NewCustomEvent("progress", aguievents.WithValue(payload))}
}
