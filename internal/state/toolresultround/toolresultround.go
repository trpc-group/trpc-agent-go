//
// Tencent is pleased to support the open source community by making trpc-agent-go
// available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package toolresultround stores the internal persisted provenance used to
// coordinate per-call tool-result events.
package toolresultround

import "trpc.group/trpc-go/trpc-agent-go/event"

const extensionKey = "trpc_agent.tool_result_round_incomplete"

// Mark records whether the per-tool-call result round was still incomplete
// when evt was emitted.
func Mark(evt *event.Event, incomplete bool) {
	_ = event.SetExtension(evt, extensionKey, incomplete)
}

// HasMarker reports whether evt carries per-tool-call result-round metadata.
func HasMarker(evt *event.Event) bool {
	if evt == nil {
		return false
	}
	_, ok := evt.Extensions[extensionKey]
	return ok
}

// IsIncomplete reports whether evt was emitted before its per-call
// tool-result round completed. Malformed provenance fails closed.
func IsIncomplete(evt *event.Event) bool {
	if evt == nil {
		return false
	}
	value, ok, err := event.GetExtension[bool](evt, extensionKey)
	return err != nil || ok && value
}
