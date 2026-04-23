//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func snapshotEvent(e *event.Event) *event.Event {
	if e == nil {
		return nil
	}

	snap := *e
	if e.Response != nil {
		snap.Response = e.Response.Clone()
	}
	if e.LongRunningToolIDs != nil {
		snap.LongRunningToolIDs = make(map[string]struct{}, len(e.LongRunningToolIDs))
		for k := range e.LongRunningToolIDs {
			snap.LongRunningToolIDs[k] = struct{}{}
		}
	}
	if e.StateDelta != nil {
		snap.StateDelta = make(map[string][]byte, len(e.StateDelta))
		for k, v := range e.StateDelta {
			if v == nil {
				snap.StateDelta[k] = nil
				continue
			}
			copied := make([]byte, len(v))
			copy(copied, v)
			snap.StateDelta[k] = copied
		}
	}
	if e.Extensions != nil {
		snap.Extensions = make(map[string]json.RawMessage, len(e.Extensions))
		for k, v := range e.Extensions {
			if v == nil {
				snap.Extensions[k] = nil
				continue
			}
			copied := make([]byte, len(v))
			copy(copied, v)
			snap.Extensions[k] = copied
		}
	}
	if e.Actions != nil {
		snap.Actions = &event.EventActions{
			SkipSummarization: e.Actions.SkipSummarization,
		}
	}
	snap.StructuredOutput = nil
	snap.ExecutionTrace = nil
	return &snap
}

func snapshotTrackEvent(trackEvent *session.TrackEvent) *session.TrackEvent {
	if trackEvent == nil {
		return nil
	}

	snap := *trackEvent
	if trackEvent.Payload != nil {
		snap.Payload = make(json.RawMessage, len(trackEvent.Payload))
		copy(snap.Payload, trackEvent.Payload)
	}
	return &snap
}
