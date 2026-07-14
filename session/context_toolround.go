//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package session

import (
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type matchedToolResponseEvent struct {
	eventIndex int
}

// expandMaskIDsForToolRounds adds paired tool-call and tool-result event IDs so
// masking never leaves a half-visible tool round in the transcript.
func expandMaskIDsForToolRounds(events []event.Event, ids []string) []string {
	if len(ids) == 0 || len(events) == 0 {
		return ids
	}

	requested := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			requested[id] = struct{}{}
		}
	}
	if len(requested) == 0 {
		return ids
	}

	indexByID := make(map[string]int, len(events))
	for i, evt := range events {
		if evt.ID != "" {
			indexByID[evt.ID] = i
		}
	}

	matches := toolResponseMatchesByCallEvent(events)
	expanded := make(map[string]struct{}, len(requested))
	for id := range requested {
		expanded[id] = struct{}{}
	}

	for id := range requested {
		idx, ok := indexByID[id]
		if !ok || idx < 0 || idx >= len(events) {
			continue
		}
		evt := events[idx]
		if evt.Response == nil {
			continue
		}
		if evt.IsToolCallResponse() {
			for _, match := range matches[idx] {
				if match.eventIndex >= 0 && match.eventIndex < len(events) {
					expanded[events[match.eventIndex].ID] = struct{}{}
				}
			}
			continue
		}
		if !evt.IsToolResultResponse() {
			continue
		}
		for callIdx, callMatches := range matches {
			for _, match := range callMatches {
				if match.eventIndex != idx {
					continue
				}
				if callIdx >= 0 && callIdx < len(events) {
					expanded[events[callIdx].ID] = struct{}{}
				}
			}
		}
	}

	out := make([]string, 0, len(expanded))
	seen := make(map[string]struct{}, len(expanded))
	for _, id := range ids {
		if _, ok := expanded[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for id := range expanded {
		if _, dup := seen[id]; dup {
			continue
		}
		out = append(out, id)
	}
	return out
}

func toolResponseMatchesByCallEvent(events []event.Event) map[int][]matchedToolResponseEvent {
	responseMatchesByCallEvent := make(map[int][]matchedToolResponseEvent)
	var pendingCallRounds []struct {
		eventIndex int
		pendingIDs map[string]struct{}
	}

	for i, evt := range events {
		if evt.Response == nil {
			continue
		}
		if evt.IsToolCallResponse() {
			ids := evt.GetToolCallIDs()
			if len(ids) == 0 {
				continue
			}
			pendingCallRounds = append(pendingCallRounds, struct {
				eventIndex int
				pendingIDs map[string]struct{}
			}{
				eventIndex: i,
				pendingIDs: toStringSet(ids),
			})
			continue
		}
		if !evt.IsToolResultResponse() {
			continue
		}
		for choiceIndex, choice := range evt.Response.Choices {
			responseID := toolResponseIDFromChoice(choice)
			if responseID == "" {
				continue
			}
			for j := len(pendingCallRounds) - 1; j >= 0; j-- {
				if _, ok := pendingCallRounds[j].pendingIDs[responseID]; !ok {
					continue
				}
				delete(pendingCallRounds[j].pendingIDs, responseID)
				callIdx := pendingCallRounds[j].eventIndex
				responseMatchesByCallEvent[callIdx] = appendToolResponseMatch(
					responseMatchesByCallEvent[callIdx],
					i,
					choiceIndex,
				)
				break
			}
		}
	}
	return responseMatchesByCallEvent
}

func toolResponseIDFromChoice(choice model.Choice) string {
	if choice.Message.ToolID != "" {
		return choice.Message.ToolID
	}
	return choice.Delta.ToolID
}

func appendToolResponseMatch(
	matches []matchedToolResponseEvent,
	eventIndex int,
	_ int,
) []matchedToolResponseEvent {
	for i := range matches {
		if matches[i].eventIndex == eventIndex {
			return matches
		}
	}
	return append(matches, matchedToolResponseEvent{eventIndex: eventIndex})
}

func toStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}
