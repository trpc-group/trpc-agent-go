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

	requested := toStringSet(ids)
	if len(requested) == 0 {
		return ids
	}

	indexByID := eventIndexByID(events)
	matches := toolResponseMatchesByCallEvent(events)
	callByResult := callIndexByResultIndex(matches)
	expanded := copyStringSet(requested)

	for id := range requested {
		idx, ok := indexByID[id]
		if !ok || idx < 0 || idx >= len(events) {
			continue
		}
		addPairedToolRoundIDs(events, idx, matches, callByResult, expanded)
	}

	return orderedExpandedIDs(ids, expanded)
}

func eventIndexByID(events []event.Event) map[string]int {
	indexByID := make(map[string]int, len(events))
	for i, evt := range events {
		if evt.ID != "" {
			indexByID[evt.ID] = i
		}
	}
	return indexByID
}

func copyStringSet(src map[string]struct{}) map[string]struct{} {
	dst := make(map[string]struct{}, len(src))
	for id := range src {
		dst[id] = struct{}{}
	}
	return dst
}

func callIndexByResultIndex(
	matches map[int][]matchedToolResponseEvent,
) map[int]int {
	callByResult := make(map[int]int, len(matches))
	for callIdx, callMatches := range matches {
		for _, match := range callMatches {
			callByResult[match.eventIndex] = callIdx
		}
	}
	return callByResult
}

func addPairedToolRoundIDs(
	events []event.Event,
	idx int,
	matches map[int][]matchedToolResponseEvent,
	callByResult map[int]int,
	expanded map[string]struct{},
) {
	evt := events[idx]
	if evt.Response == nil {
		return
	}
	if evt.IsToolCallResponse() {
		for _, match := range matches[idx] {
			if match.eventIndex >= 0 && match.eventIndex < len(events) {
				expanded[events[match.eventIndex].ID] = struct{}{}
			}
		}
		return
	}
	if !evt.IsToolResultResponse() {
		return
	}
	callIdx, ok := callByResult[idx]
	if !ok || callIdx < 0 || callIdx >= len(events) {
		return
	}
	expanded[events[callIdx].ID] = struct{}{}
}

func orderedExpandedIDs(original []string, expanded map[string]struct{}) []string {
	out := make([]string, 0, len(expanded))
	seen := make(map[string]struct{}, len(expanded))
	for _, id := range original {
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
		matchPendingToolResults(evt, i, pendingCallRounds, responseMatchesByCallEvent)
	}
	return responseMatchesByCallEvent
}

func matchPendingToolResults(
	evt event.Event,
	resultIdx int,
	pendingCallRounds []struct {
		eventIndex int
		pendingIDs map[string]struct{}
	},
	responseMatchesByCallEvent map[int][]matchedToolResponseEvent,
) {
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
				resultIdx,
				choiceIndex,
			)
			break
		}
	}
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
