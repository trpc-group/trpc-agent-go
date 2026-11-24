//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"bytes"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type interruptedToolRound struct {
	source  *model.ProviderData
	display []model.ToolCall
	native  []model.ToolCall
}

type interruptedToolRounds map[string]interruptedToolRound

// Bind native call IDs on the detached session snapshot before matching results
// or selecting summary boundaries. Preserve the stored display for projection.
func (p *ContentRequestProcessor) bindInterruptedToolRounds(
	inv *agent.Invocation, filter string, events []event.Event, projector model.ProviderDataProjector,
) interruptedToolRounds {
	if projector == nil {
		return nil
	}
	latestUserIndex, userInvocations := p.visibleUserTurns(inv, filter, events)
	var latestUserInvocation string
	switch {
	case inv.Message.Role == model.RoleUser && model.HasPayload(inv.Message) && !userInvocations[inv.InvocationID]:
		// The processor will insert this invocation's user input into history.
		latestUserInvocation = inv.InvocationID
		latestUserIndex = len(events)
	case latestUserIndex >= 0:
		latestUserInvocation = events[latestUserIndex].InvocationID
	default:
		return nil
	}
	rounds := make(interruptedToolRounds)
	seenNativeReply := make(map[string]bool)
	for i := len(events) - 1; i >= 0; i-- {
		evt := &events[i]
		if !p.canMatchToolRound(*evt, inv, filter) {
			continue
		}
		if evt.ID == "" || evt.InvocationID == "" || evt.InvocationID == inv.InvocationID ||
			evt.InvocationID == latestUserInvocation || seenNativeReply[evt.InvocationID] || len(evt.Choices) != 1 {
			continue
		}
		// A call can arrive after a newer user message. Its own user turn
		// must be visible to identify it as an earlier invocation in that case.
		if i > latestUserIndex && !userInvocations[evt.InvocationID] {
			continue
		}
		// A Responses completion has one assistant choice holding its entire output.
		msg := &evt.Choices[0].Message
		if msg.ProviderData == nil || msg.ProviderData.Protocol != "openai.responses" {
			continue
		}
		seenNativeReply[evt.InvocationID] = true
		native, err := projector.ProjectProviderData(msg.ProviderData)
		if err != nil || len(native.ToolCalls) == 0 {
			continue
		}
		rounds[evt.ID] = interruptedToolRound{source: msg.ProviderData, display: msg.ToolCalls, native: native.ToolCalls}
		msg.ToolCalls = native.ToolCalls
	}
	return rounds
}

func (p *ContentRequestProcessor) visibleUserTurns(
	inv *agent.Invocation, filter string, events []event.Event,
) (int, map[string]bool) {
	last := -1
	seen := make(map[string]bool)
	for i := range events {
		evt := &events[i]
		if isUserTurnEvent(evt) && p.canMatchToolRound(*evt, inv, filter) {
			seen[evt.InvocationID] = true
			last = i
		}
	}
	return last, seen
}

func (rounds interruptedToolRounds) restoreDisplayCalls(events []event.Event) {
	if len(rounds) == 0 {
		return
	}
	for i := range events {
		evt := &events[i]
		if round, ok := rounds[evt.ID]; ok && eventHasProviderData(*evt) {
			evt.Choices[0].Message.ToolCalls = round.display
		}
	}
}

// A window cutting through an old tool round must drop results whose native
// call was dropped, even if its display IDs were rewritten.
func (rounds interruptedToolRounds) windowStart(
	history projectedHistory, limit int, projector model.ProviderDataProjector,
) int {
	messages := history.messages
	if limit <= 0 {
		return 0
	}
	if len(rounds) > 0 && len(history.items) == len(messages) &&
		len(messages) > limit && messages[len(messages)-limit].Role == model.RoleTool {
		messages = append([]model.Message(nil), messages...)
		// Only the prefix being dropped is used by maxHistoryRunsStartIndex.
		for i := 0; i < len(messages)-limit; i++ {
			if calls, ok := rounds.effectiveCalls(history.items[i], projector); ok {
				messages[i].ToolCalls = calls
			}
		}
	}
	return maxHistoryRunsStartIndex(messages, limit)
}

// effectiveCalls resolves the calls in the actual projected payload. The
// display calls may differ after an event projector edits the native record.
func (rounds interruptedToolRounds) effectiveCalls(
	item summaryview.Item, projector model.ProviderDataProjector,
) ([]model.ToolCall, bool) {
	round, ok := rounds[item.EffectiveEvent.ID]
	data := item.Message.ProviderData
	if !ok || data == nil || data.Protocol != "openai.responses" {
		return nil, false
	}
	if data == round.source || (data.Model == round.source.Model && bytes.Equal(data.Payload, round.source.Payload)) {
		return round.native, true
	}
	if projector == nil {
		return nil, false
	}
	projected, err := projector.ProjectProviderData(data)
	return projected.ToolCalls, err == nil
}

func isUserTurnEvent(evt *event.Event) bool {
	return evt.InvocationID != "" && evt.Author == "user" && evt.IsUserMessage()
}

// Add missing outputs only to the selected request history, after windowing and
// compaction. They must neither evict a real message nor be reported as
// compacted successful results.
func (history projectedHistory) withInterruptedToolResults(
	rounds interruptedToolRounds, projector model.ProviderDataProjector,
) projectedHistory {
	if len(rounds) == 0 || len(history.items) != len(history.messages) {
		return history
	}
	var out projectedHistory
	start := 0
	for i, item := range history.items {
		calls, ok := rounds.effectiveCalls(item, projector)
		if !ok {
			continue
		}
		next := i + 1
		for next < len(history.items) && history.items[next].Message.Role == model.RoleTool {
			next++
		}
		missing := missingResponsesToolResults(calls, history.messages[i+1:next])
		if len(missing) == 0 {
			continue
		}
		out.messages = append(out.messages, history.messages[start:next]...)
		out.items = append(out.items, history.items[start:next]...)
		// No stored event or summary cutoff corresponds to these outputs.
		appendProjectedHistory(&out, event.Event{}, summaryview.Boundary{}, missing, false)
		start = next
	}
	if start == 0 {
		return history
	}
	out.messages = append(out.messages, history.messages[start:]...)
	out.items = append(out.items, history.items[start:]...)
	return out
}

func missingResponsesToolResults(calls []model.ToolCall, results []model.Message) []model.Message {
	const missingResult = "No result is available for this tool call. " +
		"The previous run may have been interrupted before the result was recorded. " +
		"The tool may already have run, and its outcome is unknown."
	reported := make(map[string]bool, len(results))
	for _, result := range results {
		reported[result.ToolID] = true
	}
	var missing []model.Message
	for _, call := range calls {
		if !reported[call.ID] {
			missing = append(missing, model.NewToolMessage(call.ID, call.Function.Name, missingResult))
		}
	}
	return missing
}
