//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type streamModeMask uint8

const (
	streamModeMaskMessages streamModeMask = 1 << iota
	streamModeMaskUpdates
	streamModeMaskCheckpoints
	streamModeMaskTasks
	streamModeMaskCustom
)

// StreamModeFilter decides whether an event should be forwarded to callers.
//
// It categorizes events into coarse groups.
// It only affects event forwarding; events are still processed internally.
type StreamModeFilter struct {
	enabled bool
	mask    streamModeMask
}

// NewStreamModeFilter builds a filter from run-level StreamMode selection.
func NewStreamModeFilter(
	enabled bool,
	modes []agent.StreamMode,
) StreamModeFilter {
	if !enabled {
		return StreamModeFilter{}
	}
	return StreamModeFilter{
		enabled: true,
		mask:    streamModeMaskFrom(modes),
	}
}

func streamModeMaskFrom(modes []agent.StreamMode) streamModeMask {
	var mask streamModeMask
	for _, mode := range modes {
		switch mode {
		case agent.StreamModeMessages:
			mask |= streamModeMaskMessages
		case agent.StreamModeUpdates:
			mask |= streamModeMaskUpdates
		case agent.StreamModeCheckpoints:
			mask |= streamModeMaskCheckpoints
		case agent.StreamModeTasks:
			mask |= streamModeMaskTasks
		case agent.StreamModeCustom:
			mask |= streamModeMaskCustom
		case agent.StreamModeDebug:
			mask |= streamModeMaskCheckpoints
			mask |= streamModeMaskTasks
		default:
		}
	}
	return mask
}

// Allows reports whether event should be forwarded to callers.
func (f StreamModeFilter) Allows(e *event.Event) bool {
	if e == nil {
		return false
	}
	if !f.enabled {
		return true
	}
	if isStreamModeErrorEvent(e) {
		return true
	}
	if (f.mask&streamModeMaskMessages) != 0 &&
		isStreamModeMessageEvent(e) {
		return true
	}
	if (f.mask&streamModeMaskUpdates) != 0 &&
		isStreamModeUpdateEvent(e) {
		return true
	}
	if (f.mask&streamModeMaskCheckpoints) != 0 &&
		isStreamModeCheckpointEvent(e) {
		return true
	}
	if (f.mask&streamModeMaskTasks) != 0 &&
		isStreamModeTaskEvent(e) {
		return true
	}
	if (f.mask&streamModeMaskCustom) != 0 &&
		isStreamModeCustomEvent(e) {
		return true
	}
	return false
}

func isStreamModeErrorEvent(e *event.Event) bool {
	if e == nil || e.Response == nil {
		return false
	}
	return e.Response.Error != nil
}

func isStreamModeMessageEvent(e *event.Event) bool {
	if e == nil || e.Response == nil {
		return false
	}
	switch e.Object {
	case model.ObjectTypeChatCompletionChunk,
		model.ObjectTypeChatCompletion:
		return true
	default:
		return false
	}
}

func isStreamModeUpdateEvent(e *event.Event) bool {
	if e == nil || e.Response == nil {
		return false
	}
	switch e.Object {
	case ObjectTypeGraphExecution,
		ObjectTypeGraphChannelUpdate,
		ObjectTypeGraphStateUpdate,
		model.ObjectTypeStateUpdate:
		return true
	default:
		return false
	}
}

func isStreamModeCheckpointEvent(e *event.Event) bool {
	if e == nil || e.Response == nil {
		return false
	}
	switch e.Object {
	case ObjectTypeGraphCheckpoint,
		ObjectTypeGraphCheckpointCreated,
		ObjectTypeGraphCheckpointCommitted,
		ObjectTypeGraphCheckpointInterrupt:
		return true
	default:
		return false
	}
}

func isStreamModeTaskEvent(e *event.Event) bool {
	if e == nil || e.Response == nil {
		return false
	}
	switch e.Object {
	case ObjectTypeGraphBarrier,
		ObjectTypeGraphNodeBarrier,
		ObjectTypeGraphNodeExecution,
		ObjectTypeGraphNodeStart,
		ObjectTypeGraphNodeComplete,
		ObjectTypeGraphNodeError,
		ObjectTypeGraphPregelStep,
		ObjectTypeGraphPregelPlanning,
		ObjectTypeGraphPregelExecution,
		ObjectTypeGraphPregelUpdate:
		return true
	default:
		return false
	}
}

func isStreamModeCustomEvent(e *event.Event) bool {
	if e == nil || e.Response == nil {
		return false
	}
	return e.Object == ObjectTypeGraphNodeCustom
}
